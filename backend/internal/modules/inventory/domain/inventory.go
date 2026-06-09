// Package domain defines the inventory module's core types.
// All stock is branch-scoped; tenant-wide stock is a catalog concern.
package domain

import (
	"time"

	"github.com/google/uuid"
)

// TransactionType classifies a stock movement.
type TransactionType string

const (
	TransactionTypeRestock     TransactionType = "restock"
	TransactionTypeConsumption TransactionType = "consumption"
	TransactionTypeWaste       TransactionType = "waste"
	TransactionTypeAdjustment  TransactionType = "adjustment"
)

// Valid reports whether t is a recognised transaction type.
func (t TransactionType) Valid() bool {
	switch t {
	case TransactionTypeRestock, TransactionTypeConsumption, TransactionTypeWaste, TransactionTypeAdjustment:
		return true
	}
	return false
}

// InventoryLevel is the materialized current stock for one product in one branch.
type InventoryLevel struct {
	ID        uuid.UUID
	TenantID  uuid.UUID
	BranchID  uuid.UUID
	ProductID uuid.UUID
	Quantity  float64 // NUMERIC(12,3) — supports fractional units
	UpdatedAt time.Time
}

// InventoryTransaction is an immutable ledger entry for a stock movement.
type InventoryTransaction struct {
	ID            uuid.UUID
	TenantID      uuid.UUID
	BranchID      uuid.UUID
	ProductID     uuid.UUID
	Type          TransactionType
	QuantityDelta float64    // positive = stock in, negative = stock out
	ReferenceID   *uuid.UUID // optional: order_id or purchase_order_id
	ReferenceType *string
	Notes         *string
	CreatedBy     *uuid.UUID
	CreatedAt     time.Time
}
