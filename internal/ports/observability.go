package ports

import "context"

// Obs bundles tracing, metrics, and logging. Adapters construct an Obs
// once at boot; the pipeline takes it as a single dependency.
type Obs struct {
	Tracer Tracer
	Meter  Meter
	Logger Logger
}

// Tracer creates spans. The pilot adapter is OpenTelemetry; the testing
// adapter is a no-op.
type Tracer interface {
	StartSpan(ctx context.Context, name string, attrs ...Attr) (context.Context, Span)
}

// Span is one tracing span. Callers MUST call End exactly once.
type Span interface {
	SetAttribute(key string, value any)
	RecordError(err error)
	End()
}

// Attr is a key/value pair for span/log attributes.
type Attr struct {
	Key   string
	Value any
}

// Meter creates counters and histograms.
type Meter interface {
	Counter(name string) Counter
	Histogram(name string) Histogram
}

// Counter is a monotonically increasing measure.
type Counter interface {
	Add(ctx context.Context, delta int64, attrs ...Attr)
}

// Histogram records distributions (latencies, sizes, etc.).
type Histogram interface {
	Record(ctx context.Context, value float64, attrs ...Attr)
}

// Logger emits structured records. Implementations MUST NOT include
// payload bodies (diffs, code chunks, LLM content) — only metadata.
type Logger interface {
	Info(msg string, kv ...any)
	Warn(msg string, kv ...any)
	Error(msg string, kv ...any)
}
