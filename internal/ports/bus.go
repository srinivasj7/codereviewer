package ports

import "context"

// QueueName names a logical queue (mapped to SQS URL / NATS subject / Kafka topic).
type QueueName string

// Standard queue names used across the system. Adapters map these to
// concrete bus resources via config.
const (
	QueueReview   QueueName = "review-jobs"
	QueueIndex    QueueName = "index-jobs"
	QueueFeedback QueueName = "feedback-events"
	QueueBackfill QueueName = "backfill-jobs"
)

// MessageBus is the asynchronous transport between the webhook gateway
// and the workers. Payloads are opaque bytes — typed wrappers live in
// internal/schemas (e.g. schemas.PublishReviewJob).
type MessageBus interface {
	Publish(ctx context.Context, queue QueueName, payload []byte, opts PublishOpts) error
	Consume(ctx context.Context, queue QueueName, handler ConsumeFunc) (Subscription, error)
	Health(ctx context.Context) (HealthStatus, error)
}

// PublishOpts carries delivery-time options. IdempotencyKey is required —
// the bus (or a dedupe layer in front of it) MUST drop duplicates.
type PublishOpts struct {
	IdempotencyKey string
}

// ConsumeFunc is the handler signature. Implementations MUST call Ack or
// Nack on the ConsumeCtx exactly once before returning.
type ConsumeFunc func(ctx context.Context, payload []byte, cctx ConsumeCtx) error

// ConsumeCtx carries per-delivery state and ack/nack callbacks.
type ConsumeCtx struct {
	Ack     func() error
	Nack    func(reason string) error
	Attempt int // 1-based; first delivery attempt is 1
}

// Subscription is returned by Consume and must be stopped at shutdown.
type Subscription interface {
	Stop() error
}

// HealthStatus reports basic bus liveness.
type HealthStatus struct {
	Healthy   bool
	Detail    string
	CheckedAt int64 // unix nanos
}
