// Package domain contains the payment module's core types.
package domain

import (
	"time"

	"github.com/google/uuid"
)

// PaymentMethod represents how the payment was made. Vendor-specific codes
// (Token type 1/3/7/9/8/17, …) are mapped inside fiscal adapters only.
type PaymentMethod string

const (
	PaymentMethodCash        PaymentMethod = "cash"
	PaymentMethodTerminal    PaymentMethod = "terminal" // card via ÖKC / EFT-POS terminal
	PaymentMethodMealCard    PaymentMethod = "meal_card"
	PaymentMethodComp        PaymentMethod = "comp"      // ikram
	PaymentMethodNoCharge    PaymentMethod = "no_charge" // ödemesiz
	PaymentMethodOpenAccount PaymentMethod = "open_account"
)

// Valid reports whether the method is a recognised value.
func (m PaymentMethod) Valid() bool {
	switch m {
	case PaymentMethodCash, PaymentMethodTerminal, PaymentMethodMealCard,
		PaymentMethodComp, PaymentMethodNoCharge, PaymentMethodOpenAccount:
		return true
	}
	return false
}

// PaymentStatus is the lifecycle state of a payment record.
// pending → completed | failed; a completed payment whose receipt is later
// cancelled on the device becomes voided (fiş iptali).
type PaymentStatus string

const (
	PaymentStatusPending   PaymentStatus = "pending"
	PaymentStatusCompleted PaymentStatus = "completed"
	PaymentStatusFailed    PaymentStatus = "failed"
	PaymentStatusVoided    PaymentStatus = "voided"
)

// Payment is the aggregate root for a single payment transaction.
type Payment struct {
	ID              uuid.UUID
	TenantID        uuid.UUID
	BranchID        uuid.UUID
	CheckID         *uuid.UUID // nil for delivery orders with no check
	IdempotencyKey  string
	Method          PaymentMethod
	Status          PaymentStatus
	AmountTotal     int64 // in smallest currency unit (kuruş for TRY)
	Currency        string
	FiscalReceiptID *uuid.UUID
	CreatedAt       time.Time
	CompletedAt     *time.Time
}
