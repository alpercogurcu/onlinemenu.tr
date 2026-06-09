package domain

import (
	"context"
	"time"
)

// BillingAdapter is the interface all e-invoice providers must satisfy.
// Domain-owned per ADR INT-001 (omnichannel spec section 1).
type BillingAdapter interface {
	// CheckRecipient looks up whether the given VKN is registered on GİB e-invoice portal.
	// Returns ErrNotRegistered when VKN is found but not an e-invoice user.
	CheckRecipient(ctx context.Context, req CheckRecipientRequest) (RecipientInfo, error)
	// SubmitInvoice sends a compiled invoice to the provider.
	// Returns a SubmitResult containing the provider's transaction reference.
	SubmitInvoice(ctx context.Context, inv Invoice) (SubmitResult, error)
	// GetInvoiceStatus polls the provider for the current status of a submitted invoice.
	GetInvoiceStatus(ctx context.Context, externalID string) (InvoiceStatusResult, error)
}

// CheckRecipientRequest holds the lookup parameters.
type CheckRecipientRequest struct {
	TenantID string // GİB sender VKN for session context
	VKN      string // recipient VKN to look up
}

// RecipientInfo is the result of a GİB alias lookup.
type RecipientInfo struct {
	VKN        string
	Alias      string // GİB posta kutusu alias
	CompanyName string
	IsRegistered bool // false = e-arşiv only
}

// SubmitResult is returned after successfully submitting an invoice to the provider.
type SubmitResult struct {
	ExternalID  string    // provider's transaction ID (e.g. INTL_TXN_ID in EDM)
	SubmittedAt time.Time
}

// InvoiceStatusResult carries the current provider-side status.
type InvoiceStatusResult struct {
	Status      string
	Description string
}
