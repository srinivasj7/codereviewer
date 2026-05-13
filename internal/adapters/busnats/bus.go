// Package busnats is a NATS JetStream MessageBus adapter. Each queue
// maps to a JetStream stream with WorkQueuePolicy retention and a
// 5-minute duplicate window keyed on opts.IdempotencyKey. Consumers
// are durable and use explicit acks; on Nack or AckWait expiry the
// message is redelivered up to MaxDeliver times.
package busnats

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"codereviewer/internal/ports"
)

// Bus is the NATS JetStream implementation of ports.MessageBus.
type Bus struct {
	nc *nats.Conn
	js jetstream.JetStream

	mu        sync.Mutex
	consumers []jetstream.ConsumeContext
}

// New connects to NATS and returns a Bus. The caller is responsible for
// calling Close at shutdown.
func New(_ context.Context, url string) (*Bus, error) {
	nc, err := nats.Connect(url,
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2*time.Second),
		nats.Name("codereviewer"),
	)
	if err != nil {
		return nil, fmt.Errorf("connect nats: %w", err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("init jetstream: %w", err)
	}
	return &Bus{nc: nc, js: js}, nil
}

// Close stops all active consumers and closes the NATS connection.
func (b *Bus) Close() {
	b.mu.Lock()
	for _, c := range b.consumers {
		c.Stop()
	}
	b.mu.Unlock()
	b.nc.Close()
}

// Publish writes a message to the queue's stream. IdempotencyKey maps
// to the Nats-Msg-Id header for JetStream's duplicate-window dedup.
func (b *Bus) Publish(ctx context.Context, queue ports.QueueName, payload []byte, opts ports.PublishOpts) error {
	if _, err := b.ensureStream(ctx, queue); err != nil {
		return fmt.Errorf("ensure stream: %w", err)
	}
	msg := &nats.Msg{Subject: string(queue), Data: payload}
	if opts.IdempotencyKey != "" {
		msg.Header = nats.Header{}
		msg.Header.Set(jetstream.MsgIDHeader, opts.IdempotencyKey)
	}
	if _, err := b.js.PublishMsg(ctx, msg); err != nil {
		return fmt.Errorf("publish %s: %w", queue, err)
	}
	return nil
}

// Consume registers a durable handler for the queue. The handler runs
// once per delivery; it must call ctx.Ack or ctx.Nack on the supplied
// ConsumeCtx. Returning an error without calling Ack/Nack defers to
// AckWait expiry, which triggers redelivery.
func (b *Bus) Consume(ctx context.Context, queue ports.QueueName, handler ports.ConsumeFunc) (ports.Subscription, error) {
	stream, err := b.ensureStream(ctx, queue)
	if err != nil {
		return nil, fmt.Errorf("ensure stream: %w", err)
	}
	consumer, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Name:       string(queue) + "-worker",
		Durable:    string(queue) + "-worker",
		AckPolicy:  jetstream.AckExplicitPolicy,
		MaxDeliver: 5,
		AckWait:    5 * time.Minute,
	})
	if err != nil {
		return nil, fmt.Errorf("create consumer for %s: %w", queue, err)
	}

	cc, err := consumer.Consume(func(msg jetstream.Msg) {
		meta, _ := msg.Metadata()
		attempt := 1
		if meta != nil {
			attempt = int(meta.NumDelivered)
		}
		cctx := ports.ConsumeCtx{
			Ack:     func() error { return msg.Ack() },
			Nack:    func(_ string) error { return msg.Nak() },
			Attempt: attempt,
		}
		// Each delivery gets a fresh context to avoid coupling worker
		// lifetimes to whatever ctx was passed into Consume.
		hctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		// Handler decides ack/nack; if it returns an error without
		// either, AckWait redelivers automatically.
		_ = handler(hctx, msg.Data(), cctx)
	})
	if err != nil {
		return nil, fmt.Errorf("start consume: %w", err)
	}

	b.mu.Lock()
	b.consumers = append(b.consumers, cc)
	b.mu.Unlock()

	return &subscription{cc: cc}, nil
}

// Health reports the underlying NATS connection state.
func (b *Bus) Health(_ context.Context) (ports.HealthStatus, error) {
	if !b.nc.IsConnected() {
		return ports.HealthStatus{
			Healthy:   false,
			Detail:    "nats disconnected",
			CheckedAt: time.Now().UnixNano(),
		}, nil
	}
	return ports.HealthStatus{
		Healthy:   true,
		Detail:    "nats: " + b.nc.ConnectedUrl(),
		CheckedAt: time.Now().UnixNano(),
	}, nil
}

func (b *Bus) ensureStream(ctx context.Context, queue ports.QueueName) (jetstream.Stream, error) {
	name := string(queue)
	stream, err := b.js.Stream(ctx, name)
	if err == nil {
		return stream, nil
	}
	return b.js.CreateStream(ctx, jetstream.StreamConfig{
		Name:       name,
		Subjects:   []string{name},
		Retention:  jetstream.WorkQueuePolicy,
		Discard:    jetstream.DiscardOld,
		Duplicates: 5 * time.Minute,
		MaxAge:     24 * time.Hour,
	})
}

type subscription struct {
	cc jetstream.ConsumeContext
}

func (s *subscription) Stop() error {
	s.cc.Stop()
	return nil
}
