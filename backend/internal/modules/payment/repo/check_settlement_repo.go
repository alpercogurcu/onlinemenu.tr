package repo

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// CheckSettledRow is one completed payment against a check, reduced to the two
// fields the counter actually needs: the id to dedupe against the station's own
// tracked list, and the amount to subtract from the outstanding balance.
//
// Method, timestamp and fiscal receipt reference are deliberately absent — this
// row feeds a cashier-visible read (ADR-AUTH-001 layer 4 projection), and a
// cashier holds no payment.payment.read. Widening this struct silently widens
// what counter staff can see, so it stays at id+amount.
type CheckSettledRow struct {
	PaymentID   uuid.UUID
	AmountTotal int64
}

// branchFilter renders the scope predicate shared by both queries below.
//
// A nil branchID means tenant scope (manager): every branch is in view. A
// non-nil one pins the read to that branch. The two queries MUST use the same
// predicate: filtering only the completed list while summing pending across the
// whole tenant would let a branch-A cashier probe a branch-B check id and learn
// from a non-zero pending_total that it exists — the enumeration leak this
// endpoint is specified to avoid.
const branchFilter = ` AND ($3::uuid IS NULL OR branch_id = $3::uuid)`

// ListCompletedByCheck returns the check's collected payments.
//
// payments.status is the authoritative lifecycle (same rationale as
// PendingTotalForCheck): 'voided' and 'failed' are excluded by the equality on
// 'completed', so money that was reversed or never captured cannot read as
// collected and suppress a legitimate re-charge.
//
// Ordering is by id so the response is stable across polls; the client dedupes
// by payment_id and does not depend on chronology (which it could not verify
// anyway — no timestamp is exposed).
func (r *PaymentRepo) ListCompletedByCheck(
	ctx context.Context,
	tx pgx.Tx,
	tenantID, checkID uuid.UUID,
	branchID *uuid.UUID,
) ([]CheckSettledRow, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, amount_total
		FROM payments
		WHERE tenant_id = $1 AND check_id = $2 AND status = 'completed'`+branchFilter+`
		ORDER BY id
	`, tenantID, checkID, branchID)
	if err != nil {
		return nil, fmt.Errorf("payment/repo: list completed payments for check: %w", err)
	}
	defer rows.Close()

	out := make([]CheckSettledRow, 0)
	for rows.Next() {
		var row CheckSettledRow
		if err := rows.Scan(&row.PaymentID, &row.AmountTotal); err != nil {
			return nil, fmt.Errorf("payment/repo: scan completed payment for check: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("payment/repo: iterate completed payments for check: %w", err)
	}
	return out, nil
}

// PendingTotalForCheckInBranch is PendingTotalForCheck with the scope predicate
// applied.
//
// It exists as a separate method rather than a parameter on the original
// because PendingTotalForCheck is part of the public SaleReader contract other
// modules call with tenant scope only; changing its signature would push a
// branch decision onto callers that have no branch to supply.
func (r *PaymentRepo) PendingTotalForCheckInBranch(
	ctx context.Context,
	tx pgx.Tx,
	tenantID, checkID uuid.UUID,
	branchID *uuid.UUID,
) (int64, error) {
	var total int64
	err := tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount_total), 0)
		FROM payments
		WHERE tenant_id = $1 AND check_id = $2 AND status = 'pending'`+branchFilter, tenantID, checkID, branchID).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("payment/repo: pending total for check in branch: %w", err)
	}
	return total, nil
}
