package domain

import (
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
	OrderStatusPending    OrderStatus = "pending"
	OrderStatusAccepted   OrderStatus = "accepted"
	OrderStatusRejected   OrderStatus = "rejected"
	OrderStatusPreparing  OrderStatus = "preparing"
	OrderStatusReady      OrderStatus = "ready"
	OrderStatusDelivered  OrderStatus = "delivered"
	OrderStatusCancelled  OrderStatus = "cancelled"
)

func (s OrderStatus) Valid() bool {
	switch s {
	case OrderStatusPending, OrderStatusAccepted, OrderStatusRejected,
		OrderStatusPreparing, OrderStatusReady, OrderStatusDelivered, OrderStatusCancelled:
		return true
	}
	return false
}

// OrderItem is a single line on an order with product data snapshotted at order time.
type OrderItem struct {
	ID                  uuid.UUID
	TenantID            uuid.UUID
	OrderID             uuid.UUID
	ProductID           uuid.UUID
	ProductName         string
	ProductPriceAmount  int64
	ProductCurrency     string
	TaxRateBPS          int
	Quantity            int
	UnitPriceAmount     int64
	Note                string
	CreatedAt           time.Time
}

// Order is a kitchen ticket for one fulfillment event (one channel, one round).
type Order struct {
	ID                    uuid.UUID
	TenantID              uuid.UUID
	BranchID              uuid.UUID
	CheckID               *uuid.UUID
	OrderChannel          OrderChannel
	DeliveryIntegratorID  *uuid.UUID
	Status                OrderStatus
	AcceptDeadlineAt      *time.Time
	AcceptedAt            *time.Time
	AcceptedBy            *uuid.UUID
	RejectedAt            *time.Time
	RejectedBy            *uuid.UUID
	RejectionReason       string
	Note                  string
	Items                 []OrderItem
	CreatedAt             time.Time
	UpdatedAt             time.Time
}
