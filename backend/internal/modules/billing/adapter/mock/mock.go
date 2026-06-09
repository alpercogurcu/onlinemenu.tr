// Package mock provides a no-op BillingAdapter for testing and development.
package mock

import (
	"context"
	"time"

	"onlinemenu.tr/internal/modules/billing/domain"
)

// Adapter always succeeds and returns predictable values.
// Use in unit tests and local development when no real EDM credentials are configured.
type Adapter struct{}

// New returns a MockAdapter.
func New() *Adapter { return &Adapter{} }

func (a *Adapter) CheckRecipient(_ context.Context, req domain.CheckRecipientRequest) (domain.RecipientInfo, error) {
	return domain.RecipientInfo{
		VKN:          req.VKN,
		Alias:        "urn:mail:mock@edm.com.tr",
		CompanyName:  "MOCK ŞIRKET A.Ş.",
		IsRegistered: true,
	}, nil
}

func (a *Adapter) SubmitInvoice(_ context.Context, inv domain.Invoice) (domain.SubmitResult, error) {
	return domain.SubmitResult{
		ExternalID:  "MOCK-" + inv.GibUUID.String(),
		SubmittedAt: time.Now().UTC(),
	}, nil
}

func (a *Adapter) GetInvoiceStatus(_ context.Context, _ string) (domain.InvoiceStatusResult, error) {
	return domain.InvoiceStatusResult{
		Status:      "ACCEPTED",
		Description: "Mock: kabul edildi",
	}, nil
}

var _ domain.BillingAdapter = (*Adapter)(nil)
