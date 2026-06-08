// Package eventbus provides a NATS JetStream publisher and subscriber
// for domain events emitted by module outbox workers.
package eventbus

import (
	"context"
	"fmt"
	"sync"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

// Config holds NATS connection settings injected via fx.
type Config struct {
	URL string

	// StreamName is the JetStream stream that persists domain events.
	StreamName string

	// Subjects lists the NATS subjects this stream captures.
	// Format: "<module>.<event>" e.g. "tenant.created.v1"
	Subjects []string
}

// Publisher sends domain events to a NATS JetStream subject.
type Publisher interface {
	Publish(ctx context.Context, subject string, payload []byte) error
}

// Subscriber handles incoming events from a NATS JetStream consumer.
type Subscriber interface {
	Subscribe(ctx context.Context, subject, durableName string, handler HandlerFunc) error
}

// HandlerFunc processes a single event message.
// Returning a non-nil error causes the message to be NAK'd for redelivery.
type HandlerFunc func(ctx context.Context, msg jetstream.Msg) error

// Bus implements both Publisher and Subscriber on top of NATS JetStream.
type Bus struct {
	conn       *nats.Conn
	js         jetstream.JetStream
	streamName string
	logger     *zap.Logger

	// mu guards the consumers slice.
	mu        sync.Mutex
	consumers []jetstream.ConsumeContext
}

// Module registers the Bus with fx lifecycle.
var Module = fx.Module("eventbus",
	fx.Provide(NewBus),
)

// NewBus connects to NATS and ensures the domain event stream exists.
func NewBus(lc fx.Lifecycle, cfg Config, logger *zap.Logger) (*Bus, error) {
	conn, err := nats.Connect(cfg.URL,
		nats.MaxReconnects(-1),
		nats.RetryOnFailedConnect(true),
	)
	if err != nil {
		return nil, fmt.Errorf("eventbus: connect to NATS: %w", err)
	}

	js, err := jetstream.New(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("eventbus: create JetStream context: %w", err)
	}

	bus := &Bus{conn: conn, js: js, streamName: cfg.StreamName, logger: logger}

	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			_, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
				Name:     cfg.StreamName,
				Subjects: cfg.Subjects,
			})
			if err != nil {
				return fmt.Errorf("eventbus: ensure stream %q: %w", cfg.StreamName, err)
			}
			logger.Info("eventbus ready", zap.String("stream", cfg.StreamName))
			return nil
		},
		OnStop: func(_ context.Context) error {
			// Stop all active consumers before draining so in-flight callbacks
			// are not interrupted mid-execution.
			bus.stopAllConsumers()

			// Drain waits for both publish buffers and subscription callbacks
			// to complete, providing a cleaner shutdown than FlushTimeout+Close.
			if err := conn.Drain(); err != nil {
				logger.Warn("eventbus: drain incomplete", zap.Error(err))
			}
			logger.Info("eventbus closed")
			return nil
		},
	})

	return bus, nil
}

// Publish sends payload to the given NATS subject.
func (b *Bus) Publish(ctx context.Context, subject string, payload []byte) error {
	if _, err := b.js.Publish(ctx, subject, payload); err != nil {
		return fmt.Errorf("eventbus: publish to %q: %w", subject, err)
	}
	return nil
}

// PublishMsg sends a nats.Msg to JetStream, preserving headers such as
// Nats-Msg-Id for per-message deduplication.
func (b *Bus) PublishMsg(ctx context.Context, msg *nats.Msg) error {
	if _, err := b.js.PublishMsg(ctx, msg); err != nil {
		return fmt.Errorf("eventbus: publish msg to %q: %w", msg.Subject, err)
	}
	return nil
}

// Subscribe creates a durable push consumer filtered to subject and calls handler
// for each message. The consumer is registered with the Bus so that OnStop can
// shut it down cleanly even if ctx is never cancelled by the caller.
func (b *Bus) Subscribe(ctx context.Context, subject, durableName string, handler HandlerFunc) error {
	cons, err := b.js.CreateOrUpdateConsumer(ctx, b.streamName, jetstream.ConsumerConfig{
		Durable:       durableName,
		AckPolicy:     jetstream.AckExplicitPolicy,
		FilterSubject: subject,
	})
	if err != nil {
		return fmt.Errorf("eventbus: create consumer %q: %w", durableName, err)
	}

	cc, err := cons.Consume(func(msg jetstream.Msg) {
		if err := handler(ctx, msg); err != nil {
			b.logger.Error("eventbus: handler error — NAKing",
				zap.String("subject", msg.Subject()),
				zap.Error(err),
			)
			_ = msg.Nak()
			return
		}
		_ = msg.Ack()
	})
	if err != nil {
		return fmt.Errorf("eventbus: start consume %q: %w", durableName, err)
	}

	b.addConsumer(cc)

	// Allow individual subscription cancellation via the caller's context.
	// OnStop calls stopAllConsumers(), which is idempotent for already-stopped consumers.
	go func() {
		<-ctx.Done()
		cc.Stop()
	}()

	return nil
}

func (b *Bus) addConsumer(cc jetstream.ConsumeContext) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.consumers = append(b.consumers, cc)
}

func (b *Bus) stopAllConsumers() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, cc := range b.consumers {
		cc.Stop()
	}
	b.consumers = nil
}
