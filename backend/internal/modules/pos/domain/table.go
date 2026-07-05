package domain

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// TableZone groups dine-in tables within a branch (e.g. a floor or terrace).
type TableZone struct {
	ID        uuid.UUID
	TenantID  uuid.UUID
	BranchID  uuid.UUID
	Name      string
	Floor     int
	IsActive  bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// TableStatus is the floor-plan state of a dine-in table.
type TableStatus string

const (
	TableStatusEmpty    TableStatus = "empty"
	TableStatusOccupied TableStatus = "occupied"
	TableStatusReserved TableStatus = "reserved"
	TableStatusCleaning TableStatus = "cleaning"
)

func (s TableStatus) Valid() bool {
	switch s {
	case TableStatusEmpty, TableStatusOccupied, TableStatusReserved, TableStatusCleaning:
		return true
	}
	return false
}

// allowedTableTransitions is the single source of truth for the table status
// machine (ADR-DATA-006 discipline: one allowedTransitions table, no
// scattered if-chains):
//
//	empty     -> occupied (check opened against this table), reserved, cleaning
//	occupied  -> cleaning (check closed/cancelled), empty (manual override,
//	             e.g. staff correcting a mistaken open)
//	reserved  -> occupied (guest seated / check opened), empty (reservation
//	             cancelled)
//	cleaning  -> empty (cleaned, ready for next guest)
//
// "occupied" is reachable here because the machine itself must allow it —
// CheckService.Open drives that edge transactionally when a table is
// attached to a newly opened check. The manual HTTP status-change endpoint
// additionally rejects "occupied" as an explicit target (see
// service.TableService.SetStatus) so staff cannot mark a table occupied
// without a real check backing it; that is a service-level policy on top of
// this machine, not a relaxation of it.
var allowedTableTransitions = map[TableStatus][]TableStatus{
	TableStatusEmpty:    {TableStatusOccupied, TableStatusReserved, TableStatusCleaning},
	TableStatusOccupied: {TableStatusCleaning, TableStatusEmpty},
	TableStatusReserved: {TableStatusOccupied, TableStatusEmpty},
	TableStatusCleaning: {TableStatusEmpty},
}

// TransitionTableStatus validates a proposed table status transition and
// returns ErrInvalidTransition (shared sentinel with the order status
// machine — see order.go) if it is not allowed from the current status.
func TransitionTableStatus(from, to TableStatus) error {
	if !to.Valid() {
		return fmt.Errorf("pos/domain: invalid target table status %q: %w", to, ErrInvalidTransition)
	}
	for _, next := range allowedTableTransitions[from] {
		if next == to {
			return nil
		}
	}
	return fmt.Errorf("pos/domain: %s -> %s: %w", from, to, ErrInvalidTransition)
}

// Table is a physical dine-in table within a zone.
type Table struct {
	ID             uuid.UUID
	TenantID       uuid.UUID
	BranchID       uuid.UUID
	ZoneID         uuid.UUID
	Name           string
	Capacity       int
	Status         TableStatus
	LayoutPosition json.RawMessage
	IsActive       bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
}
