package domain

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ShipmentStatus is the physical fulfilment lifecycle of a shipment.
// Shipment is the SOLE owner of the "received" fact (ADR-DATA-006):
// BranchTransferOrder never sets its own status to received directly — it is
// always derived from a Shipment's transition to ShipmentStatusReceived.
type ShipmentStatus string

const (
	ShipmentStatusDraft     ShipmentStatus = "draft"
	ShipmentStatusApproved  ShipmentStatus = "approved"
	ShipmentStatusInTransit ShipmentStatus = "in_transit"
	ShipmentStatusReceived  ShipmentStatus = "received"
	ShipmentStatusCancelled ShipmentStatus = "cancelled"
)

// Valid reports whether s is a recognised shipment status.
func (s ShipmentStatus) Valid() bool {
	switch s {
	case ShipmentStatusDraft, ShipmentStatusApproved, ShipmentStatusInTransit,
		ShipmentStatusReceived, ShipmentStatusCancelled:
		return true
	}
	return false
}

// ErrInvalidShipmentTransition is returned when a shipment status transition
// is not allowed from the shipment's current status.
var ErrInvalidShipmentTransition = errors.New("inventory/domain: invalid shipment status transition")

// allowedShipmentTransitions is the single source of truth for the shipment
// status machine (ADR-DATA-006, lessons-from-b2b item 2: one allowedTransitions
// map, one Transition() entry point — never a scattered `status = ...`).
// received and cancelled are terminal.
var allowedShipmentTransitions = map[ShipmentStatus][]ShipmentStatus{
	ShipmentStatusDraft:     {ShipmentStatusApproved, ShipmentStatusCancelled},
	ShipmentStatusApproved:  {ShipmentStatusInTransit, ShipmentStatusCancelled},
	ShipmentStatusInTransit: {ShipmentStatusReceived},
}

// TransitionShipmentStatus validates a proposed shipment status transition.
func TransitionShipmentStatus(from, to ShipmentStatus) error {
	if !to.Valid() {
		return fmt.Errorf("inventory/domain: invalid target shipment status %q: %w", to, ErrInvalidShipmentTransition)
	}
	for _, next := range allowedShipmentTransitions[from] {
		if next == to {
			return nil
		}
	}
	return fmt.Errorf("inventory/domain: shipment %s -> %s: %w", from, to, ErrInvalidShipmentTransition)
}

// Priority is a shared priority level for BranchTransferOrder and Shipment.
type Priority string

const (
	PriorityNormal Priority = "normal"
	PriorityUrgent Priority = "urgent"
)

// Valid reports whether p is a recognised priority.
func (p Priority) Valid() bool {
	switch p {
	case PriorityNormal, PriorityUrgent:
		return true
	}
	return false
}

// Shipment is the physical movement of stock from a warehouse to a branch
// (db-schema.md SHIPMENTS). It is the sole owner of the "received" fact for
// any linked BranchTransferOrder (ADR-DATA-006).
type Shipment struct {
	ID              uuid.UUID
	TenantID        uuid.UUID
	FromWarehouseID uuid.UUID
	ToBranchID      uuid.UUID
	TransferOrderID *uuid.UUID // optional link to the requesting BTO
	Status          ShipmentStatus
	Priority        Priority
	Note            string
	CreatedBy       *uuid.UUID
	ShippedAt       *time.Time
	ReceivedAt      *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// ShipmentItem is a line item on a shipment.
type ShipmentItem struct {
	ShipmentID   uuid.UUID
	TenantID     uuid.UUID
	StockItemID  uuid.UUID
	RequestedQty float64
	ShippedQty   float64
	ReceivedQty  float64
	Unit         string
}
