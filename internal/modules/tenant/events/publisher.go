// Package events defines the NATS subjects and event payloads published by the tenant module.
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	pub "onlinemenu.tr/internal/modules/tenant/public"
	"onlinemenu.tr/internal/platform/eventbus"
)

const (
	SubjectTenantCreated = "tenant.created.v1"
	SubjectBranchCreated = "branch.created.v1"
)

// TenantCreatedEvent is the payload for SubjectTenantCreated.
type TenantCreatedEvent struct {
	EventID    string    `json:"event_id"`
	OccurredAt time.Time `json:"occurred_at"`
	TenantID   string    `json:"tenant_id"`
	Name       string    `json:"name"`
}

// BranchCreatedEvent is the payload for SubjectBranchCreated.
type BranchCreatedEvent struct {
	EventID    string    `json:"event_id"`
	OccurredAt time.Time `json:"occurred_at"`
	TenantID   string    `json:"tenant_id"`
	BranchID   string    `json:"branch_id"`
	Name       string    `json:"name"`
}

// PublishTenantCreated encodes and publishes a TenantCreatedEvent.
func PublishTenantCreated(ctx context.Context, p eventbus.Publisher, t pub.Tenant) error {
	payload, err := json.Marshal(TenantCreatedEvent{
		EventID:    uuid.New().String(),
		OccurredAt: time.Now().UTC(),
		TenantID:   t.ID.String(),
		Name:       t.Name,
	})
	if err != nil {
		return fmt.Errorf("tenant/events: marshal tenant created: %w", err)
	}
	if err := p.Publish(ctx, SubjectTenantCreated, payload); err != nil {
		return fmt.Errorf("tenant/events: publish tenant created: %w", err)
	}
	return nil
}

// PublishBranchCreated encodes and publishes a BranchCreatedEvent.
func PublishBranchCreated(ctx context.Context, p eventbus.Publisher, b pub.Branch) error {
	payload, err := json.Marshal(BranchCreatedEvent{
		EventID:    uuid.New().String(),
		OccurredAt: time.Now().UTC(),
		TenantID:   b.TenantID.String(),
		BranchID:   b.ID.String(),
		Name:       b.Name,
	})
	if err != nil {
		return fmt.Errorf("tenant/events: marshal branch created: %w", err)
	}
	if err := p.Publish(ctx, SubjectBranchCreated, payload); err != nil {
		return fmt.Errorf("tenant/events: publish branch created: %w", err)
	}
	return nil
}
