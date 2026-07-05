package domain

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// OrderChannel is the fulfillment channel for an order.
type OrderChannel string

const (
	OrderChannelDineIn   OrderChannel = "dine_in"
	OrderChannelTakeaway OrderChannel = "takeaway"
	OrderChannelDelivery OrderChannel = "delivery"
)

func (c OrderChannel) Valid() bool {
	switch c {
	case OrderChannelDineIn, OrderChannelTakeaway, OrderChannelDelivery:
		return true
	}
	return false
}

// OrderStatus is the kitchen/delivery lifecycle state of an order.
type OrderStatus string

const (
	OrderStatusPending   OrderStatus = "pending"
	OrderStatusAccepted  OrderStatus = "accepted"
	OrderStatusRejected  OrderStatus = "rejected"
	OrderStatusPreparing OrderStatus = "preparing"
	OrderStatusReady     OrderStatus = "ready"
	OrderStatusDelivered OrderStatus = "delivered"
	OrderStatusCancelled OrderStatus = "cancelled"
)

func (s OrderStatus) Valid() bool {
	switch s {
	case OrderStatusPending, OrderStatusAccepted, OrderStatusRejected,
		OrderStatusPreparing, OrderStatusReady, OrderStatusDelivered, OrderStatusCancelled:
		return true
	}
	return false
}

// ErrInvalidTransition is returned when a status transition is not allowed
// from the order's current status (e.g. pending → delivered, or any move out
// of a terminal status).
var ErrInvalidTransition = errors.New("pos/domain: invalid order status transition")

// allowedOrderTransitions is the single source of truth for the order status
// machine. rejected, delivered and cancelled are terminal: no edges leave them.
var allowedOrderTransitions = map[OrderStatus][]OrderStatus{
	OrderStatusPending:   {OrderStatusAccepted, OrderStatusRejected, OrderStatusCancelled},
	OrderStatusAccepted:  {OrderStatusPreparing, OrderStatusCancelled},
	OrderStatusPreparing: {OrderStatusReady, OrderStatusCancelled},
	OrderStatusReady:     {OrderStatusDelivered, OrderStatusCancelled},
}

// TransitionOrderStatus validates a proposed order status transition and
// returns ErrInvalidTransition if it is not allowed from the current status.
func TransitionOrderStatus(from, to OrderStatus) error {
	if !to.Valid() {
		return fmt.Errorf("pos/domain: invalid target status %q: %w", to, ErrInvalidTransition)
	}
	for _, next := range allowedOrderTransitions[from] {
		if next == to {
			return nil
		}
	}
	return fmt.Errorf("pos/domain: %s -> %s: %w", from, to, ErrInvalidTransition)
}

// InactiveOrderStatuses are order statuses whose line items must be excluded
// from a check's payable total (pos/repo.CheckRepo.GetTotal): the order was
// either rejected before ever reaching the kitchen or cancelled after being
// accepted. Charging the customer for either would bill them for items they
// never received. This is the single source of truth for that exclusion —
// callers must not repeat these values as inline string literals.
var InactiveOrderStatuses = []OrderStatus{OrderStatusRejected, OrderStatusCancelled}

// KitchenActiveOrderStatuses are order statuses still "live" for a kitchen
// display: the order has been placed and has not yet reached a terminal
// status (delivered/rejected/cancelled). This is the single source of truth
// for what pos/repo.OrderRepo.ListActiveByBranch selects for the WebSocket
// snapshot sent to a newly (re)connected kitchen display — callers must not
// repeat these values as inline string literals. Note this is NOT simply the
// complement of InactiveOrderStatuses: "delivered" is terminal but not
// "inactive" for billing purposes, and must not appear here.
var KitchenActiveOrderStatuses = []OrderStatus{
	OrderStatusPending, OrderStatusAccepted, OrderStatusPreparing, OrderStatusReady,
}

// OrderItem is a single line on an order with product data snapshotted at order time.
type OrderItem struct {
	ID                 uuid.UUID
	TenantID           uuid.UUID
	OrderID            uuid.UUID
	ProductID          uuid.UUID
	ProductName        string
	ProductPriceAmount int64
	ProductCurrency    string
	TaxRateBPS         int
	Quantity           int
	UnitPriceAmount    int64
	Note               string
	CreatedAt          time.Time
}

// Order is a kitchen ticket for one fulfillment event (one channel, one round).
type Order struct {
	ID                   uuid.UUID
	TenantID             uuid.UUID
	BranchID             uuid.UUID
	CheckID              *uuid.UUID
	OrderChannel         OrderChannel
	DeliveryIntegratorID *uuid.UUID
	Status               OrderStatus
	AcceptDeadlineAt     *time.Time
	AcceptedAt           *time.Time
	AcceptedBy           *uuid.UUID
	RejectedAt           *time.Time
	RejectedBy           *uuid.UUID
	RejectionReason      string
	Note                 string
	Items                []OrderItem
	CreatedAt            time.Time
	UpdatedAt            time.Time
}
