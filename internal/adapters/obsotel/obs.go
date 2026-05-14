// Package obsotel wires the OpenTelemetry SDK to the ports.Obs bundle.
// Traces and metrics export via OTLP HTTP to a configured collector
// endpoint; logs continue to flow through slog/JSON on stdout (the
// collector picks them up via a container log shipper).
//
// Boot order:
//
//  1. Call New(ctx, ServiceName, OtlpEndpoint) — returns Obs + Shutdown.
//  2. Use Obs in pipelines as usual.
//  3. defer shutdown(ctx) at process exit so pending spans/metrics flush.
//
// If OtlpEndpoint is empty, New still installs in-memory providers so
// instrumentation calls don't panic, but nothing leaves the process —
// equivalent to a misconfigured exporter. The caller (boot.PickObservability)
// should fall back to obsstdout in that case.
package obsotel

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	otelmetric "go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	oteltrace "go.opentelemetry.io/otel/trace"

	"codereviewer/internal/adapters/obsstdout"
	"codereviewer/internal/ports"
)

// New constructs an Obs bundle backed by OTLP HTTP exporters and a
// returns a shutdown function that flushes both providers. The
// shutdown function returns the first non-nil error from either
// provider's Shutdown call; callers should log and continue.
func New(ctx context.Context, serviceName, otlpEndpoint string) (ports.Obs, func(context.Context) error, error) {
	res, err := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceName(serviceName)),
		resource.WithProcessRuntimeName(),
		resource.WithProcessRuntimeVersion(),
		resource.WithHost(),
	)
	if err != nil {
		return ports.Obs{}, nil, fmt.Errorf("build resource: %w", err)
	}

	traceExp, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpoint(otlpEndpoint),
		otlptracehttp.WithInsecure(),
	)
	if err != nil {
		return ports.Obs{}, nil, fmt.Errorf("trace exporter: %w", err)
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithBatcher(traceExp,
			sdktrace.WithBatchTimeout(5*time.Second),
		),
	)
	otel.SetTracerProvider(tp)

	metricExp, err := otlpmetrichttp.New(ctx,
		otlpmetrichttp.WithEndpoint(otlpEndpoint),
		otlpmetrichttp.WithInsecure(),
	)
	if err != nil {
		// Best-effort: tear down the tracer we already built.
		_ = tp.Shutdown(ctx)
		return ports.Obs{}, nil, fmt.Errorf("metric exporter: %w", err)
	}
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExp,
			sdkmetric.WithInterval(30*time.Second),
		)),
	)
	otel.SetMeterProvider(mp)

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if serviceName != "" {
		logger = logger.With("service", serviceName)
	}

	obs := ports.Obs{
		Tracer: &otelTracer{t: tp.Tracer(serviceName)},
		Meter:  &otelMeter{m: mp.Meter(serviceName)},
		Logger: obsstdout.NewScrubbingLogger(&slogLogger{l: logger}, 0),
	}

	shutdown := func(ctx context.Context) error {
		var firstErr error
		if err := tp.Shutdown(ctx); err != nil {
			firstErr = fmt.Errorf("tracer shutdown: %w", err)
		}
		if err := mp.Shutdown(ctx); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("meter shutdown: %w", err)
		}
		return firstErr
	}
	return obs, shutdown, nil
}

type otelTracer struct{ t oteltrace.Tracer }

func (o *otelTracer) StartSpan(ctx context.Context, name string, attrs ...ports.Attr) (context.Context, ports.Span) {
	ctx, span := o.t.Start(ctx, name, oteltrace.WithAttributes(toOtelAttrs(attrs)...))
	return ctx, &otelSpan{s: span}
}

type otelSpan struct{ s oteltrace.Span }

func (o *otelSpan) SetAttribute(key string, value any) {
	o.s.SetAttributes(toOtelAttr(key, value))
}
func (o *otelSpan) RecordError(err error) {
	if err == nil {
		return
	}
	o.s.RecordError(err)
	o.s.SetStatus(codes.Error, err.Error())
}
func (o *otelSpan) End() { o.s.End() }

type otelMeter struct{ m otelmetric.Meter }

func (o *otelMeter) Counter(name string) ports.Counter {
	c, err := o.m.Int64Counter(name)
	if err != nil {
		return noopCounter{}
	}
	return &otelCounter{c: c}
}
func (o *otelMeter) Histogram(name string) ports.Histogram {
	h, err := o.m.Float64Histogram(name)
	if err != nil {
		return noopHistogram{}
	}
	return &otelHistogram{h: h}
}

type otelCounter struct{ c otelmetric.Int64Counter }

func (o *otelCounter) Add(ctx context.Context, delta int64, attrs ...ports.Attr) {
	o.c.Add(ctx, delta, otelmetric.WithAttributes(toOtelAttrs(attrs)...))
}

type otelHistogram struct{ h otelmetric.Float64Histogram }

func (o *otelHistogram) Record(ctx context.Context, v float64, attrs ...ports.Attr) {
	o.h.Record(ctx, v, otelmetric.WithAttributes(toOtelAttrs(attrs)...))
}

type noopCounter struct{}

func (noopCounter) Add(context.Context, int64, ...ports.Attr) {}

type noopHistogram struct{}

func (noopHistogram) Record(context.Context, float64, ...ports.Attr) {}

type slogLogger struct{ l *slog.Logger }

func (s *slogLogger) Info(msg string, kv ...any)  { s.l.Info(msg, kv...) }
func (s *slogLogger) Warn(msg string, kv ...any)  { s.l.Warn(msg, kv...) }
func (s *slogLogger) Error(msg string, kv ...any) { s.l.Error(msg, kv...) }

func toOtelAttrs(attrs []ports.Attr) []attribute.KeyValue {
	out := make([]attribute.KeyValue, 0, len(attrs))
	for _, a := range attrs {
		out = append(out, toOtelAttr(a.Key, a.Value))
	}
	return out
}

// toOtelAttr maps a port Attr to attribute.KeyValue. Unknown types fall
// back to fmt.Sprintf — the OTLP attribute schema only accepts a fixed
// set of primitives, and silently dropping the attribute is worse than
// stringifying it.
func toOtelAttr(key string, v any) attribute.KeyValue {
	switch x := v.(type) {
	case string:
		return attribute.String(key, x)
	case bool:
		return attribute.Bool(key, x)
	case int:
		return attribute.Int(key, x)
	case int64:
		return attribute.Int64(key, x)
	case float64:
		return attribute.Float64(key, x)
	}
	return attribute.String(key, fmt.Sprintf("%v", v))
}
