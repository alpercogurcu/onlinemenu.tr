// Package domain contains the payment module's core types.
package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// PaymentMethod represents how the payment was made.
type PaymentMethod string

const (
	PaymentMethodCash     PaymentMethod = "cash"
	PaymentMethodTerminal PaymentMethod = "terminal"
)

// Valid reports whether the method is a recognised value.
func (m PaymentMethod) Valid() bool {
	return m == PaymentMethodCash || m == PaymentMethodTerminal
}

// PaymentStatus is the lifecycle state of a payment record.
type PaymentStatus string

const (
	PaymentStatusPending   PaymentStatus = "pending"
	PaymentStatusCompleted PaymentStatus = "completed"
	PaymentStatusFailed    PaymentStatus = "failed"
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

// FiscalSale is the input handed to a FiscalDeviceAdapter for registration.
type FiscalSale struct {
	TenantID    uuid.UUID
	PaymentID   uuid.UUID
	AmountTotal int64
	Currency    string
	Method      PaymentMethod
}

// FiscalReceipt is the response from a fiscal device after successful registration.
type FiscalReceipt struct {
	ID            uuid.UUID
	TenantID      uuid.UUID
	PaymentID     uuid.UUID
	DeviceType    string
	ReceiptNumber string
	ReceiptData   map[string]any
	IssuedAt      time.Time
}

// FiscalDeviceAdapter is the interface all fiscal device drivers must satisfy.
// ADR-FISCAL-001: every payment must call RegisterSale, even for mock/none devices.
type FiscalDeviceAdapter interface {
	RegisterSale(ctx context.Context, sale FiscalSale) (FiscalReceipt, error)
}
