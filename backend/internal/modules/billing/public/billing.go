// Package public exposes the billing module's contract to other modules.
// Imports of internal billing packages from outside the billing module are
// forbidden by go-arch-lint.
package public

import (
	"context"

	"github.com/google/uuid"

	"onlinemenu.tr/internal/modules/billing/domain"
)

// InvoiceReader allows other modules to query invoice data.
// Implemented by billing.BillingService.
type InvoiceReader interface {
	// GetInvoice returns a single invoice by ID.
	GetInvoice(ctx context.Context, tenantID, invoiceID uuid.UUID) (domain.Invoice, error)
	// ListInvoices returns invoices for a tenant with pagination.
	ListInvoices(ctx context.Context, tenantID uuid.UUID, limit, offset int) ([]domain.Invoice, error)
}
