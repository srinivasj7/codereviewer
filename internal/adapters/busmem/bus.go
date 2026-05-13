// Package busmem is an in-process MessageBus suitable for local
// development and unit tests. Publish synchronously invokes the
// registered consumer, so tests can assert on side effects without
// barrier synchronization. Idempotency keys deduplicate publishes.
package busmem

import (
	"context"
	"fmt"
	"sync"
	"time"

	"codereviewer/internal/ports"
)

// Bus is an in-process implementation of ports.MessageBus. Safe for
// concurrent use; one consumer per queue.
type Bus struct {
	mu       sync.Mutex
	handlers map[ports.QueueName]ports.ConsumeFunc
	seen     map[string]struct{}
}

// New returns an empty Bus.
func New() *Bus {
	return &Bus{
		handlers: make(map[ports.QueueName]ports.ConsumeFunc),
		seen:     make(map[string]struct{}),
	}
}

// Publish synchronously invokes the consumer for queue, after deduping
// on opts.IdempotencyKey. Publishes to a queue with no consumer fail.
func (b *Bus) Publish(ctx context.Context, queue ports.QueueName, payload []byte, opts ports.PublishOpts) error {
	b.mu.Lock()
	if opts.IdempotencyKey != "" {
		if _, dup := b.seen[opts.IdempotencyKey]; dup {
			b.mu.Unlock()
			return nil
		}
		b.seen[opts.IdempotencyKey] = struct{}{}
	}
	handler, ok := b.handlers[queue]
	b.mu.Unlock()
	if !ok {
		return fmt.Errorf("busmem: no consumer for queue %q", queue)
	}
	cctx := ports.ConsumeCtx{
		Ack:     func() error { return nil },
		Nack:    func(string) error { return nil },
		Attempt: 1,
	}
	return handler(ctx, payload, cctx)
}

// Consume registers a single handler for queue. Calling Consume twice
// for the same queue is an error.
func (b *Bus) Consume(_ context.Context, queue ports.QueueName, handler ports.ConsumeFunc) (ports.Subscription, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, exists := b.handlers[queue]; exists {
		return nil, fmt.Errorf("busmem: queue %q already has a consumer", queue)
	}
	b.handlers[queue] = handler
	return &subscription{b: b, queue: queue}, nil
}

// Health always reports healthy for the in-memory bus.
func (b *Bus) Health(_ context.Context) (ports.HealthStatus, error) {
	return ports.HealthStatus{
		Healthy:   true,
		Detail:    "busmem",
		CheckedAt: time.Now().UnixNano(),
	}, nil
}

type subscription struct {
	b     *Bus
	queue ports.QueueName
}

func (s *subscription) Stop() error {
	s.b.mu.Lock()
	defer s.b.mu.Unlock()
	delete(s.b.handlers, s.queue)
	return nil
}
