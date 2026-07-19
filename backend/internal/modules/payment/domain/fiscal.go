package domain

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// FiscalSubmissionStatus is the lifecycle state of a fiscal registration.
// pending → submitted → completed | failed | voided; expired is set by the
// reconciliation job when the vendor's basket TTL passes without a result.
type FiscalSubmissionStatus string

const (
	FiscalSubmissionPending   FiscalSubmissionStatus = "pending"
	FiscalSubmissionSubmitted FiscalSubmissionStatus = "submitted"
	FiscalSubmissionCompleted FiscalSubmissionStatus = "completed"
	FiscalSubmissionFailed    FiscalSubmissionStatus = "failed"
	FiscalSubmissionVoided    FiscalSubmissionStatus = "voided"
	FiscalSubmissionExpired   FiscalSubmissionStatus = "expired"
)

// FiscalLine is one sale line in vendor-neutral form. Prices are minor
// currency units (kuruş); quantity is in thousandths (1000 = 1 unit) and the
// tax rate in permyriad (1000 = 10.00%) so adapters convert without loss.
type FiscalLine struct {
	Name             string
	UnitPriceMinor   int64
	QuantityMilli    int64
	TaxRatePermyriad int
	CategoryID       uuid.UUID // catalog category; adapters map it to a device section
	Unit             string    // UN/ECE unit code, e.g. C62 (piece)
}

// FiscalPayment is one entry of the payment plan the POS decided on.
type FiscalPayment struct {
	Method      PaymentMethod
	AmountMinor int64
}

// FiscalAdjustKind and FiscalAdjustMode describe a discount or surcharge.
type FiscalAdjustKind string

const (
	FiscalAdjustDiscount  FiscalAdjustKind = "discount"
	FiscalAdjustSurcharge FiscalAdjustKind = "surcharge"
)

type FiscalAdjustMode string

const (
	FiscalAdjustAmount  FiscalAdjustMode = "amount"  // Value is minor currency units
	FiscalAdjustPercent FiscalAdjustMode = "percent" // Value is permyriad (1000 = 10.00%)
)

type FiscalAdjust struct {
	Description string
	Kind        FiscalAdjustKind
	Mode        FiscalAdjustMode
	Value       int64
}

// FiscalCustomer carries the buyer identity required for invoice-linked
// document flows (e-fatura, e-arşiv, bilgi fişi).
type FiscalCustomer struct {
	Name      string
	TaxID     string // TCKN or VKN
	TaxOffice string
	Email     string
	Telephone string
	Address   string
}

// FiscalMeta is display metadata shown on the device and the receipt.
type FiscalMeta struct {
	TableLabel  string // e.g. "Masa 5"
	WaiterName  string
	CheckNumber int
}

// FiscalSale is the vendor-neutral input for registering a sale.
// SubmissionID doubles as the vendor-side idempotency reference (basketID).
type FiscalSale struct {
	SubmissionID uuid.UUID
	TenantID     uuid.UUID
	BranchID     uuid.UUID
	PaymentID    uuid.UUID
	CheckID      *uuid.UUID
	Currency     string
	TotalMinor   int64 // must equal lines minus discounts; vendors reject mismatches
	Lines        []FiscalLine
	Payments     []FiscalPayment
	Discount     *FiscalAdjust
	Customer     *FiscalCustomer
	Meta         FiscalMeta
}

// FiscalSubmissionRef identifies a previously submitted sale for voiding.
type FiscalSubmissionRef struct {
	SubmissionID   uuid.UUID
	TenantID       uuid.UUID
	BranchID       uuid.UUID
	TerminalSerial string
}

// FiscalConfirmedPayment is one payment confirmed by the device. VendorType
// keeps the vendor's raw payment-type code for audit; adapters also map it
// back to a PaymentMethod when a mapping exists.
type FiscalConfirmedPayment struct {
	Method      PaymentMethod
	VendorType  int
	AmountMinor int64
	Description string
}

// FiscalResult is the normalized outcome of a fiscal registration, produced
// by an adapter either synchronously (mock, wire) or from an async vendor
// notification (cloud webhook).
type FiscalResult struct {
	SubmissionID  uuid.UUID
	TenantID      uuid.UUID
	BranchID      uuid.UUID
	PaymentID     uuid.UUID
	Status        FiscalSubmissionStatus // completed | failed | voided
	DeviceType    string
	ReceiptNo     string
	ZNo           string
	VendorRef     string // vendor transaction id (Token: sale UUID)
	FailureReason string
	Payments      []FiscalConfirmedPayment
	Raw           json.RawMessage // vendor payload, persisted for audit

	// CompletedAt is when THIS server learned the outcome, not when the device
	// printed it. It drives fiscal_submissions.completed_at, which the fiscal
	// status poll filters its recency window on, so it must come from the
	// server's clock: an ÖKC whose clock is hours off would otherwise push its
	// result outside the window (invisible to the cashier) or far into the
	// future.
	//
	// Adapters do not need to populate this: FiscalResultSink's implementation
	// (the payment service's OnFiscalResult) overwrites it unconditionally with
	// its own clock reading before persisting, regardless of what a driver, a
	// mock, or a replayed webhook sets here. It remains a field on this struct
	// only because SubmitSale/VoidSale results and reconciliation flow through
	// the same type before reaching the sink.
	CompletedAt time.Time

	// DeviceOperationAt is the vendor/device-reported moment of the sale
	// (Token: operationDate). It carries the legal meaning and is persisted as
	// fiscal_receipts.issued_at — deliberately separate from CompletedAt, which
	// answers a different question ("when did we find out"). Zero when the
	// driver reports no device time; callers fall back to CompletedAt.
	DeviceOperationAt time.Time
}

// FiscalCapabilities declares optional vendor features. POS core flows must
// never require any of these; they only unlock optional shortcuts.
type FiscalCapabilities struct {
	OnDeviceSplit   bool // device can split the bill itself (Token list mode)
	VoidSale        bool
	CustomerInfo    bool
	CurrencyPayment bool
	OperatorRouting bool // route card/meal payments to a specific device app
}

// FiscalDeviceAdapter is implemented by every fiscal device driver.
// ADR-FISCAL-002: SubmitSale must not block on device interaction. A non-nil
// FiscalResult means registration finished synchronously (mock, wire); nil
// means the result arrives later through the vendor's async channel and the
// driver's transport feeds it into FiscalResultSink.
type FiscalDeviceAdapter interface {
	SubmitSale(ctx context.Context, sale FiscalSale) (*FiscalResult, error)
	VoidSale(ctx context.Context, ref FiscalSubmissionRef) (*FiscalResult, error)
	Capabilities() FiscalCapabilities
}

// FiscalResultSink consumes normalized fiscal results. The payment service
// implements it: completes/fails the payment, persists the receipt, and
// publishes the outbox event. Implementations must be idempotent per
// SubmissionID (webhooks and reconciliation may deliver duplicates).
type FiscalResultSink interface {
	OnFiscalResult(ctx context.Context, res FiscalResult) error
}

// FiscalReceipt is the persisted legal record of a completed registration.
type FiscalReceipt struct {
	ID            uuid.UUID
	TenantID      uuid.UUID
	PaymentID     uuid.UUID
	DeviceType    string
	ReceiptNumber string
	ZNo           string
	VendorRef     string
	ReceiptData   map[string]any
	IssuedAt      time.Time
}
