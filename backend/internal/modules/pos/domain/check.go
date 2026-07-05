package domain

import (
	"time"

	"github.com/google/uuid"
)

// CheckStatus is the lifecycle state of a dine-in tab.
type CheckStatus string

const (
	CheckStatusOpen      CheckStatus = "open"
	CheckStatusClosed    CheckStatus = "closed"
	CheckStatusCancelled CheckStatus = "cancelled"
)

func (s CheckStatus) Valid() bool {
	switch s {
	case CheckStatusOpen, CheckStatusClosed, CheckStatusCancelled:
		return true
	}
	return false
}

// Check represents a dine-in table session (adisyon) that accumulates orders.
type Check struct {
	ID uuid.UUID
	// TableID is set when the check was opened against a floor-plan Table
	// (domain/table.go). It is nil for masasız satış (takeaway/paket servis)
	// checks — TableLabel keeps rendering unchanged in that case.
	TableID    *uuid.UUID
	TenantID   uuid.UUID
	BranchID   uuid.UUID
	TableLabel string
	Status     CheckStatus
	OpenedBy   uuid.UUID
	ClosedBy   *uuid.UUID
	Note       string
	OpenedAt   time.Time
	ClosedAt   *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}
