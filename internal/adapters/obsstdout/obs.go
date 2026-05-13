// Package obsstdout provides a minimal Obs bundle: a slog-based JSON
// logger writing to stdout, a no-op tracer, and an in-memory meter that
// keeps counters and histogram samples in process. Suitable for local
// dev and tests. Production deployments use the obsotel adapter instead.
package obsstdout

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"

	"codereviewer/internal/ports"
)

// New returns an Obs bundle writing to stdout in JSON. ServiceName is
// added as a base attribute on every log line.
func New(serviceName string) ports.Obs {
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	logger := slog.New(handler)
	if serviceName != "" {
		logger = logger.With("service", serviceName)
	}
	return ports.Obs{
		Tracer: noopTracer{},
		Meter:  newMemoryMeter(),
		Logger: &slogLogger{l: logger},
	}
}

type slogLogger struct{ l *slog.Logger }

func (s *slogLogger) Info(msg string, kv ...any)  { s.l.Info(msg, kv...) }
func (s *slogLogger) Warn(msg string, kv ...any)  { s.l.Warn(msg, kv...) }
func (s *slogLogger) Error(msg string, kv ...any) { s.l.Error(msg, kv...) }

type noopTracer struct{}
type noopSpan struct{}

func (noopTracer) StartSpan(ctx context.Context, _ string, _ ...ports.Attr) (context.Context, ports.Span) {
	return ctx, noopSpan{}
}

func (noopSpan) SetAttribute(string, any) {}
func (noopSpan) RecordError(error)        {}
func (noopSpan) End()                     {}

type memoryMeter struct {
	mu         sync.Mutex
	counters   map[string]*atomic.Int64
	histograms map[string]*histogram
}

func newMemoryMeter() *memoryMeter {
	return &memoryMeter{
		counters:   make(map[string]*atomic.Int64),
		histograms: make(map[string]*histogram),
	}
}

func (m *memoryMeter) Counter(name string) ports.Counter {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.counters[name]
	if !ok {
		c = new(atomic.Int64)
		m.counters[name] = c
	}
	return &memoryCounter{c: c}
}

func (m *memoryMeter) Histogram(name string) ports.Histogram {
	m.mu.Lock()
	defer m.mu.Unlock()
	h, ok := m.histograms[name]
	if !ok {
		h = &histogram{}
		m.histograms[name] = h
	}
	return h
}

type memoryCounter struct{ c *atomic.Int64 }

func (m *memoryCounter) Add(_ context.Context, delta int64, _ ...ports.Attr) {
	m.c.Add(delta)
}

type histogram struct {
	mu     sync.Mutex
	values []float64
}

func (h *histogram) Record(_ context.Context, v float64, _ ...ports.Attr) {
	h.mu.Lock()
	h.values = append(h.values, v)
	h.mu.Unlock()
}
