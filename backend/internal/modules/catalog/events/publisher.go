// Package events handles catalog domain event publishing via NATS JetStream.
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

const (
	subjectProductCreated = "catalog.product.created"
	subjectProductUpdated = "catalog.product.updated"
	subjectProductDeleted = "catalog.product.deleted"
)

// ProductCreatedEvent is published when a new product is created.
type ProductCreatedEvent struct {
	EventID   uuid.UUID `json:"event_id"`
	TenantID  uuid.UUID `json:"tenant_id"`
	ProductID uuid.UUID `json:"product_id"`
	OccuredAt time.Time `json:"occurred_at"`
}

// Publisher publishes catalog events to NATS JetStream.
type Publisher struct {
	nc     *nats.Conn
	logger *zap.Logger
}

// PublisherParams groups fx-injected dependencies for NewPublisher.
type PublisherParams struct {
	fx.In

	NC     *nats.Conn
	Logger *zap.Logger
}

// NewPublisher constructs a Publisher for fx injection.
func NewPublisher(p PublisherParams) *Publisher {
	return &Publisher{nc: p.NC, logger: p.Logger}
}

// PublishProductCreated emits a product.created event.
func (p *Publisher) PublishProductCreated(ctx context.Context, tenantID, productID uuid.UUID) error {
	evt := ProductCreatedEvent{
		EventID:   uuid.New(),
		TenantID:  tenantID,
		ProductID: productID,
		OccuredAt: time.Now().UTC(),
	}
	return p.publish(ctx, subjectProductCreated, evt)
}

// PublishProductDeleted emits a product.deleted event.
func (p *Publisher) PublishProductDeleted(ctx context.Context, tenantID, productID uuid.UUID) error {
	evt := struct {
		EventID   uuid.UUID `json:"event_id"`
		TenantID  uuid.UUID `json:"tenant_id"`
		ProductID uuid.UUID `json:"product_id"`
		OccuredAt time.Time `json:"occurred_at"`
	}{
		EventID:   uuid.New(),
		TenantID:  tenantID,
		ProductID: productID,
		OccuredAt: time.Now().UTC(),
	}
	return p.publish(ctx, subjectProductDeleted, evt)
}

func (p *Publisher) publish(_ context.Context, subject string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("catalog/events: marshal %s: %w", subject, err)
	}
	if err := p.nc.Publish(subject, data); err != nil {
		return fmt.Errorf("catalog/events: publish %s: %w", subject, err)
	}
	p.logger.Debug("catalog event published", zap.String("subject", subject))
	return nil
}
