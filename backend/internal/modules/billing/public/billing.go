// Package public exposes the billing module's contract to other modules.
// Imports of internal billing packages from outside the billing module are
// forbidden by go-arch-lint.
package public

import (
	"context"
	"errors"

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

// ErrBranchForbidden is returned when the acting principal attempts a
// branch-scoped invoice action (generate, retry submission) on/for a branch
// it does not have access to (ADR-AUTH-001, layer 3). Tenant-wide principals
// (OPA scope "tenant", e.g. manager) are exempt. The resource (or requested
// branch_id) is already known to be tenant-visible (RLS, layer 1, already
// passed) so this is not treated as a not-found — the HTTP layer maps it to
// 403 Forbidden.
var ErrBranchForbidden = errors.New("billing: forbidden for this branch")
