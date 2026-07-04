// Package domain defines the inventory module's core types.
// All stock is warehouse-scoped (ADR-DATA-005); a warehouse belongs to a
// branch, but stock itself is never addressed by branch_id directly.
package domain

import (
	"time"

	"github.com/google/uuid"
)

// MovementType classifies a stock movement (db-schema.md STOCK_MOVEMENTS).
type MovementType string

const (
	MovementTypeIn       MovementType = "in"
	MovementTypeOut      MovementType = "out"
	MovementTypeAdjust   MovementType = "adjust"
	MovementTypeTransfer MovementType = "transfer"
	MovementTypeReserve  MovementType = "reserve"
	MovementTypeRelease  MovementType = "release"
)

// Valid reports whether t is a recognised movement type.
func (t MovementType) Valid() bool {
	switch t {
	case MovementTypeIn, MovementTypeOut, MovementTypeAdjust, MovementTypeTransfer, MovementTypeReserve, MovementTypeRelease:
		return true
	}
	return false
}

// AffectsOnHand reports whether this movement type changes StockLevel.OnHand
// directly (in/out/adjust/transfer) as opposed to only Reserved (reserve/release).
func (t MovementType) AffectsOnHand() bool {
	switch t {
	case MovementTypeIn, MovementTypeOut, MovementTypeAdjust, MovementTypeTransfer:
		return true
	}
	return false
}

// CostSource classifies where StockLevel.LastUnitCost was recorded from
// (ADR-DATA-007). CostSourceTransfer is written from a received shipment's
// frozen unit price (ShipmentService.Receive); CostSourcePurchaseReceipt is
// written from a purchase_receipt line on create
// (PurchaseReceiptService.Create, ADR-DATA-007 karar 3). CostSourcePurchaseOrder
// remains reserved for the Faz 2 invoiced purchase-order path.
type CostSource string

const (
	CostSourceTransfer        CostSource = "transfer"
	CostSourcePurchaseOrder   CostSource = "purchase_order"
	CostSourcePurchaseReceipt CostSource = "purchase_receipt"
)

// Valid reports whether c is a recognised cost source.
func (c CostSource) Valid() bool {
	switch c {
	case CostSourceTransfer, CostSourcePurchaseOrder, CostSourcePurchaseReceipt:
		return true
	}
	return false
}

// StockLevel is the materialized current stock for one stock item in one
// warehouse. Available is derived (on_hand - reserved) by the database
// (STORED GENERATED column); application code must never compute or persist
// it independently (see migrations/inventory/000003 for the rationale).
//
// LastUnitCost/LastCostCurrency/LastCostSource/LastCostAt are the
// branch-local cost of this (warehouse, stock_item) pair (ADR-DATA-007).
// LastCostSource is "transfer" from a received shipment's frozen unit price
// (ShipmentService.Receive) or "purchase_receipt" from a purchase receipt
// line (PurchaseReceiptService.Create); "purchase_order" remains reserved
// for the Faz 2 invoiced purchase-order path. All four fields are nil until
// a cost has been recorded.
type StockLevel struct {
	ID               uuid.UUID
	TenantID         uuid.UUID
	WarehouseID      uuid.UUID
	StockItemID      uuid.UUID
	OnHand           float64
	Reserved         float64
	Available        float64
	ReorderPoint     *float64
	Unit             string
	LastUnitCost     *float64
	LastCostCurrency *string
	LastCostSource   *string
	LastCostAt       *time.Time
	UpdatedAt        time.Time
}

// StockMovement is an immutable ledger entry for a stock movement.
//
// Quantity is a positive magnitude for in/out/transfer/reserve/release
// (direction comes from Type); it may be signed only when Type is
// MovementTypeAdjust, since a manual correction can go either direction.
type StockMovement struct {
	ID            uuid.UUID
	TenantID      uuid.UUID
	WarehouseID   uuid.UUID
	StockItemID   uuid.UUID
	Type          MovementType
	Quantity      float64
	ReferenceID   *uuid.UUID // optional: shipment_id, transfer_order_id, purchase_order_id
	ReferenceType *string
	Notes         *string
	CreatedBy     *uuid.UUID
	CreatedAt     time.Time
}
