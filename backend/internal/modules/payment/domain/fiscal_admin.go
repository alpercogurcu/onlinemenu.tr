package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Fiscal administration value types and sentinels.
//
// These are plain data carried across the fiscal admin API (ADR-FISCAL-002
// §2, §5). They live in domain rather than repo because both the HTTP layer
// and the repo layer speak them: leaving them in repo forced payment_http to
// import payment_repo, which the module boundary forbids.
var (
	// ErrNotFound is the shared "row absent" sentinel for the payment module.
	ErrNotFound = errors.New("payment/repo: not found")
	// ErrTerminalNotFound means the addressed fiscal terminal does not exist
	// for this tenant.
	ErrTerminalNotFound = errors.New("payment/repo: fiscal terminal not found")
	// ErrTerminalSerialTaken means the device serial is already registered.
	ErrTerminalSerialTaken = errors.New("payment/repo: fiscal terminal serial already registered")
)

// FiscalTerminal is a registered fiscal device belonging to a branch.
type FiscalTerminal struct {
	ID                uuid.UUID
	TenantID          uuid.UUID
	BranchID          uuid.UUID
	Vendor            string
	TerminalSerial    string
	VendorMerchantRef string
	VendorBranchRef   string
	Label             string
	BasketMode        string
	IsActive          bool
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// FiscalDeviceSection is one department (kısım) synchronized from a device.
type FiscalDeviceSection struct {
	SectionNo    int
	Name         string
	TaxPermyriad int
	SyncedAt     time.Time
}

// FiscalSectionMapping binds a catalog category to a device section.
type FiscalSectionMapping struct {
	CategoryID uuid.UUID
	SectionNo  int
}

// TerminalPatch is a sparse update of a registered terminal; nil fields are
// left untouched.
type TerminalPatch struct {
	Label      *string
	BasketMode *string
	IsActive   *bool
}

// SubmissionRouting is the tenant/payment identity a vendor webhook needs to
// enrich its result before it can reach the sink.
type SubmissionRouting struct {
	TenantID  uuid.UUID
	BranchID  uuid.UUID
	PaymentID uuid.UUID
}
