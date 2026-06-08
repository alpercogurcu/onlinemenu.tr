package domain

import (
	"time"

	"github.com/google/uuid"
)

// MembershipStatus tracks the lifecycle of a person-branch-role assignment.
type MembershipStatus string

const (
	MembershipActive     MembershipStatus = "active"
	MembershipSuspended  MembershipStatus = "suspended"
	MembershipTerminated MembershipStatus = "terminated"
)

// Membership binds a Person to a Role within a specific tenant and optionally
// a specific branch. A person may hold multiple memberships at the same branch
// (e.g. both "cashier" and "kitchen"); permissions are union-ed across all
// active memberships for the selected context.
//
// BranchID == nil means a chain-wide membership (e.g. chain owner or chain auditor).
type Membership struct {
	ID        uuid.UUID
	PersonID  uuid.UUID
	TenantID  uuid.UUID
	BranchID  *uuid.UUID // nil = chain-wide
	RoleID    uuid.UUID
	Status    MembershipStatus
	CreatedAt time.Time
	UpdatedAt time.Time
}

// IsActive reports whether the membership is in the active state.
func (m Membership) IsActive() bool { return m.Status == MembershipActive }

// ContextItem is a lightweight summary of a membership used in the context-selection
// response returned by GET /identity/me/contexts.
type ContextItem struct {
	MembershipID uuid.UUID
	TenantID     uuid.UUID
	TenantName   string
	BranchID     *uuid.UUID
	BranchName   string
	RoleID       uuid.UUID
	RoleName     string
}
