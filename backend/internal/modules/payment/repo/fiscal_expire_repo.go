package repo

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"onlinemenu.tr/internal/modules/payment/domain"
)

// GetByID reads a single submission by its own id.
//
// Unlike GetRouting (webhook path, cross-tenant by necessity) this MUST run in
// a tenant-scoped transaction: the manual expire endpoint takes the submission
// id from the caller, so RLS is what turns another tenant's id into "not
// found" rather than an actionable row.
func (r *FiscalSubmissionRepo) GetByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (FiscalSubmission, error) {
	row := tx.QueryRow(ctx, `
		SELECT id, tenant_id, branch_id, payment_id, adapter_type,
		       terminal_serial, status, sale_payload, retry_count
		FROM fiscal_submissions
		WHERE id = $1
	`, id)

	var (
		sub      FiscalSubmission
		terminal *string
		status   string
	)
	err := row.Scan(
		&sub.ID, &sub.TenantID, &sub.BranchID, &sub.PaymentID, &sub.AdapterType,
		&terminal, &status, &sub.SalePayload, &sub.RetryCount,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return FiscalSubmission{}, ErrNotFound
	}
	if err != nil {
		return FiscalSubmission{}, fmt.Errorf("payment/repo: get submission by id: %w", err)
	}
	if terminal != nil {
		sub.TerminalSerial = *terminal
	}
	sub.Status = domain.FiscalSubmissionStatus(status)
	return sub, nil
}
