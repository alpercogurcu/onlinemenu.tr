package domain

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// BTOStatus is the lifecycle state of a branch transfer order (ADR-DATA-006).
type BTOStatus string

const (
	BTOStatusDraft      BTOStatus = "draft"
	BTOStatusSubmitted  BTOStatus = "submitted"
	BTOStatusApproved   BTOStatus = "approved"
	BTOStatusFulfilling BTOStatus = "fulfilling"
	BTOStatusShipped    BTOStatus = "shipped"
	BTOStatusReceived   BTOStatus = "received"
	BTOStatusClosed     BTOStatus = "closed"
	BTOStatusRejected   BTOStatus = "rejected"
	BTOStatusCancelled  BTOStatus = "cancelled"
)

// Valid reports whether s is a recognised BTO status.
func (s BTOStatus) Valid() bool {
	switch s {
	case BTOStatusDraft, BTOStatusSubmitted, BTOStatusApproved, BTOStatusFulfilling,
		BTOStatusShipped, BTOStatusReceived, BTOStatusClosed, BTOStatusRejected, BTOStatusCancelled:
		return true
	}
	return false
}

// ErrInvalidBTOTransition is returned when a branch transfer order status
// transition is not allowed from its current status.
var ErrInvalidBTOTransition = errors.New("inventory/domain: invalid branch transfer order status transition")

// allowedBTOTransitions is the single source of truth for the BTO status
// machine (ADR-DATA-006). rejected, cancelled and closed are terminal.
//
// IMPORTANT (ADR-DATA-006 ownership rule): the shipped and received edges
// are reachable here for validation purposes, but the service layer only
// ever calls TransitionBTOStatus(..., BTOStatusShipped/Received) from the
// shipment-event code path (ShipmentService), never from a caller-facing BTO
// action. There is deliberately no exported BTOService method that lets an
// HTTP caller set status directly to shipped or received — see
// service/transfer_order_service.go and service/shipment_service.go.
var allowedBTOTransitions = map[BTOStatus][]BTOStatus{
	BTOStatusDraft:      {BTOStatusSubmitted, BTOStatusCancelled},
	BTOStatusSubmitted:  {BTOStatusApproved, BTOStatusRejected, BTOStatusCancelled},
	BTOStatusApproved:   {BTOStatusFulfilling, BTOStatusCancelled},
	BTOStatusFulfilling: {BTOStatusShipped},
	BTOStatusShipped:    {BTOStatusReceived},
	BTOStatusReceived:   {BTOStatusClosed},
}

// TransitionBTOStatus validates a proposed BTO status transition.
func TransitionBTOStatus(from, to BTOStatus) error {
	if !to.Valid() {
		return fmt.Errorf("inventory/domain: invalid target BTO status %q: %w", to, ErrInvalidBTOTransition)
	}
	for _, next := range allowedBTOTransitions[from] {
		if next == to {
			return nil
		}
	}
	return fmt.Errorf("inventory/domain: BTO %s -> %s: %w", from, to, ErrInvalidBTOTransition)
}

// BranchTransferOrder is the requesting-side document for a stock transfer
// between branches (ADR-DATA-006). Physical movement is executed by Shipment.
type BranchTransferOrder struct {
	ID                    uuid.UUID
	TenantID              uuid.UUID
	RequestingBranchID    uuid.UUID
	SourceBranchID        uuid.UUID
	Status                BTOStatus
	Priority              Priority
	RequestedDeliveryDate *time.Time
	Note                  string
	CreatedBy             *uuid.UUID
	SubmittedAt           *time.Time
	ApprovedAt            *time.Time
	ApprovedBy            *uuid.UUID
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// BranchTransferOrderItem is a line item on a branch transfer order.
// ShippedQty/ReceivedQty are denormalized from Shipment (ADR-DATA-006): the
// service layer writes them only in reaction to a shipment status transition.
//
// UnitPrice/Currency are the imalathane/HQ -> franchise transfer (sale)
// price (ADR-DATA-006 eklenti / ADR-DATA-007 SS4). They are nil at request
// time -- the requesting branch never sets a price -- and are set only by
// the source branch as part of TransferOrderService.Approve.
type BranchTransferOrderItem struct {
	ID              uuid.UUID
	TenantID        uuid.UUID
	TransferOrderID uuid.UUID
	StockItemID     uuid.UUID
	RequestedQty    float64
	ApprovedQty     *float64
	ShippedQty      float64
	ReceivedQty     float64
	Unit            string
	UnitPrice       *float64
	Currency        *string
	Note            string
}
