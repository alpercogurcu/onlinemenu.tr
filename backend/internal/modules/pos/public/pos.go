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
