// Package events defines the NATS subjects and event payloads published by the identity module.
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"onlinemenu.tr/internal/platform/eventbus"
)

const (
	SubjectPersonCreated     = "person.created.v1"
	SubjectMembershipCreated = "membership.created.v1"
	SubjectMembershipUpdated = "membership.updated.v1"
	SubjectRoleUpdated       = "role.updated.v1"
)

// PersonCreatedEvent is the payload for SubjectPersonCreated.
type PersonCreatedEvent struct {
	EventID    string    `json:"event_id"`
	OccurredAt time.Time `json:"occurred_at"`
	PersonID   string    `json:"person_id"`
	Email      string    `json:"email"`
	FullName   string    `json:"full_name"`
}

// MembershipCreatedEvent is the payload for SubjectMembershipCreated.
type MembershipCreatedEvent struct {
	EventID      string    `json:"event_id"`
	OccurredAt   time.Time `json:"occurred_at"`
	MembershipID string    `json:"membership_id"`
	PersonID     string    `json:"person_id"`
	TenantID     string    `json:"tenant_id"`
	RoleID       string    `json:"role_id"`
	BranchID     *string   `json:"branch_id,omitempty"`
}

// MembershipUpdatedEvent is the payload for SubjectMembershipUpdated.
type MembershipUpdatedEvent struct {
	EventID      string    `json:"event_id"`
	OccurredAt   time.Time `json:"occurred_at"`
	MembershipID string    `json:"membership_id"`
	TenantID     string    `json:"tenant_id"`
	Status       string    `json:"status"`
}

// RoleUpdatedEvent is the payload for SubjectRoleUpdated.
// TenantID is empty for system roles.
type RoleUpdatedEvent struct {
	EventID    string    `json:"event_id"`
	OccurredAt time.Time `json:"occurred_at"`
	RoleID     string    `json:"role_id"`
	TenantID   string    `json:"tenant_id,omitempty"`
}

// PublishPersonCreated encodes and publishes a PersonCreatedEvent.
func PublishPersonCreated(ctx context.Context, p eventbus.Publisher, personID uuid.UUID, email, fullName string) error {
	payload, err := json.Marshal(PersonCreatedEvent{
		EventID:    uuid.New().String(),
		OccurredAt: time.Now().UTC(),
		PersonID:   personID.String(),
		Email:      email,
		FullName:   fullName,
	})
	if err != nil {
		return fmt.Errorf("identity/events: marshal person created: %w", err)
	}
	if err := p.Publish(ctx, SubjectPersonCreated, payload); err != nil {
		return fmt.Errorf("identity/events: publish person created: %w", err)
	}
	return nil
}

// PublishMembershipCreated encodes and publishes a MembershipCreatedEvent.
// branchID may be nil for chain-wide memberships.
func PublishMembershipCreated(ctx context.Context, p eventbus.Publisher, membershipID, personID, tenantID, roleID uuid.UUID, branchID *uuid.UUID) error {
	evt := MembershipCreatedEvent{
		EventID:      uuid.New().String(),
		OccurredAt:   time.Now().UTC(),
		MembershipID: membershipID.String(),
		PersonID:     personID.String(),
		TenantID:     tenantID.String(),
		RoleID:       roleID.String(),
	}
	if branchID != nil {
		s := branchID.String()
		evt.BranchID = &s
	}
	payload, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("identity/events: marshal membership created: %w", err)
	}
	if err := p.Publish(ctx, SubjectMembershipCreated, payload); err != nil {
		return fmt.Errorf("identity/events: publish membership created: %w", err)
	}
	return nil
}

// PublishMembershipUpdated encodes and publishes a MembershipUpdatedEvent.
func PublishMembershipUpdated(ctx context.Context, p eventbus.Publisher, membershipID, tenantID uuid.UUID, status string) error {
	payload, err := json.Marshal(MembershipUpdatedEvent{
		EventID:      uuid.New().String(),
		OccurredAt:   time.Now().UTC(),
		MembershipID: membershipID.String(),
		TenantID:     tenantID.String(),
		Status:       status,
	})
	if err != nil {
		return fmt.Errorf("identity/events: marshal membership updated: %w", err)
	}
	if err := p.Publish(ctx, SubjectMembershipUpdated, payload); err != nil {
		return fmt.Errorf("identity/events: publish membership updated: %w", err)
	}
	return nil
}

// PublishRoleUpdated encodes and publishes a RoleUpdatedEvent.
// tenantID is uuid.Nil for system roles.
func PublishRoleUpdated(ctx context.Context, p eventbus.Publisher, roleID, tenantID uuid.UUID) error {
	evt := RoleUpdatedEvent{
		EventID:    uuid.New().String(),
		OccurredAt: time.Now().UTC(),
		RoleID:     roleID.String(),
	}
	if tenantID != uuid.Nil {
		evt.TenantID = tenantID.String()
	}
	payload, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("identity/events: marshal role updated: %w", err)
	}
	if err := p.Publish(ctx, SubjectRoleUpdated, payload); err != nil {
		return fmt.Errorf("identity/events: publish role updated: %w", err)
	}
	return nil
}
