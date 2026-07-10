// Package domain contains the billing module's core types.
package domain

import (
	"time"

	"github.com/google/uuid"
)

// InvoiceType distinguishes e-fatura (peer-to-peer GİB) from e-arşiv (taxpayer archive).
type InvoiceType string

const (
	InvoiceTypeEFatura InvoiceType = "e_fatura"
	InvoiceTypeEArsiv  InvoiceType = "e_arsiv"
)

// InvoiceStatus is the lifecycle state of an invoice record.
type InvoiceStatus string

const (
	InvoiceStatusDraft             InvoiceStatus = "draft"
	InvoiceStatusPendingSubmission InvoiceStatus = "pending_submission"
	InvoiceStatusSubmitted         InvoiceStatus = "submitted"
	InvoiceStatusAccepted          InvoiceStatus = "accepted"
	InvoiceStatusRejected          InvoiceStatus = "rejected"
	InvoiceStatusCancelled         InvoiceStatus = "cancelled"
)

// Invoice is the aggregate root for a single e-invoice or e-archive document.
// All monetary amounts are stored in the smallest currency unit (kuruş for TRY).
type Invoice struct {
	ID          uuid.UUID
	TenantID    uuid.UUID
	BranchID    uuid.UUID
	InvoiceType InvoiceType
	Status      InvoiceStatus
	// bare UUID references to POS module — no FK
	CheckID   *uuid.UUID
	PaymentID *uuid.UUID
	// uniqueness guard for generation requests
	IdempotencyKey string
	// official invoice number assigned on submission
	InvoiceNumber string
	// GİB document UUID (generated at creation, included in UBL XML)
	GibUUID uuid.UUID
	// provider transaction reference (e.g. INTL_TXN_ID from EDM)
	ExternalID string
	// supplier party — snapshotted from tenant data at invoice time
	SupplierVKN   string
	SupplierName  string
	SupplierAlias string // GİB posta kutusu alias
	// customer party — snapshotted at invoice time
	CustomerVKN   string
	CustomerName  string
	CustomerAlias string // GİB posta kutusu alias (required for e-fatura)
	// amounts in kuruş
	AmountExcludingTax int64
	TaxAmount          int64
	AmountTotal        int64
	Currency           string
	// lifecycle
	IssueDate       time.Time
	SubmittedAt     *time.Time
	AcceptedAt      *time.Time
	RejectedAt      *time.Time
	RejectionReason string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	// eager-loaded items
	Items []InvoiceItem
}

// InvoiceItem is one line on an invoice.
// Amounts are in kuruş; TaxRateBPS is in basis points (800 = 8%).
type InvoiceItem struct {
	ID              uuid.UUID
	InvoiceID       uuid.UUID
	TenantID        uuid.UUID
	ProductID       *uuid.UUID // nil for manual/ad-hoc lines
	ProductName     string
	Quantity        int32
	UnitPriceAmount int64 // KDV Hariç
	TaxRateBPS      int32
	LineTotal       int64 // KDV Hariç satır toplamı = Quantity * UnitPriceAmount
	TaxAmount       int64 // satır KDV = LineTotal * TaxRateBPS / 10000
	CreatedAt       time.Time
}
