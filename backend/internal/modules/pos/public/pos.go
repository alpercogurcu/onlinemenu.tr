// Package public exposes the POS module's read interface for consumption
// by other modules (e.g., payment). No direct DB access across module boundaries.
package public

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"onlinemenu.tr/internal/modules/pos/domain"
)

// ErrNotFound is returned when a requested resource does not exist.
var ErrNotFound = errors.New("pos: not found")

// ErrInvalidTransition is returned when a requested status change is not
// allowed from the resource's current status.
var ErrInvalidTransition = errors.New("pos: invalid status transition")

// ErrBranchForbidden is returned when the acting principal attempts a
// branch-scoped action (check open/close/cancel, order place/accept/reject/
// advance) on a resource belonging to a branch it does not have access to
// (ADR-AUTH-001, layer 3 / docs/lessons-from-b2b.md item 6). Tenant-wide
// principals (OPA scope "tenant", e.g. manager) are exempt. The resource is
// already known to be tenant-visible (RLS, layer 1, already passed), so this
// is not treated as a not-found — the HTTP layer maps it to 403 Forbidden.
var ErrBranchForbidden = errors.New("pos: forbidden for this branch")

// CheckReader allows other modules to read check state without importing POS internals.
type CheckReader interface {
	GetByID(ctx context.Context, tenantID, checkID uuid.UUID) (Check, error)
}

// Check is a read-only projection of the POS check for cross-module consumption.
type Check struct {
	ID         uuid.UUID
	TenantID   uuid.UUID
	BranchID   uuid.UUID
	TableLabel string
	Status     domain.CheckStatus
	OpenedAt   time.Time
}
