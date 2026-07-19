package repo

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// FiscalPendingRow is one in-flight fiscal registration in a branch: a sale
// some POS station has taken money for but whose fiscal receipt has not been
// confirmed yet.
type FiscalPendingRow struct {
	PaymentID    uuid.UUID
	CheckID      *uuid.UUID
	AmountTotal  int64
	RegisteredAt time.Time
}

// FiscalSettledRow is one fiscal registration that reached a terminal state
// inside the recency window. Status is the PAYMENT's status, not the
// submission's: the payment vocabulary is exactly completed|failed|voided,
// while a submission can also be 'expired' — a state the client has no
// separate handling for (an expired submission always fails its payment).
// FailureReason carries fiscal_submissions.last_error verbatim; translating
// vendor error text is the client's job.
//
// AmountTotal repeats the pending row's amount deliberately. A payment that
// settles drops out of the pending list and releases whatever reservation the
// station held against it; without the amount here the station cannot keep
// deducting it while the settled entry is visible, and the remaining balance
// would look collectable again — the same money taken twice.
type FiscalSettledRow struct {
	PaymentID     uuid.UUID
	CheckID       *uuid.UUID
	AmountTotal   int64
	Status        string
	FailureReason *string
	SettledAt     time.Time
}

// FiscalStatusRepo answers the branch-scoped fiscal status poll.
//
// Both queries read fiscal_submissions rather than payments, because
// payments.completed_at is only written on completion (see PaymentRepo.Fail /
// Void): a failed or voided payment carries no timestamp at all, so the
// "settled in the last N minutes" window is not expressible over payments.
// fiscal_submissions.completed_at is set on every terminal transition by
// MarkResult, which makes it the only usable clock here.
type FiscalStatusRepo struct{}

func NewFiscalStatusRepo() *FiscalStatusRepo { return &FiscalStatusRepo{} }

// ListPendingByBranch returns the branch's in-flight registrations, oldest
// first — the oldest is the one a cashier is most likely blocked on.
// 'submitted' counts as pending: the sale is with the device but unconfirmed,
// which is indistinguishable from 'pending' for a station waiting to close a
// check.
func (r *FiscalStatusRepo) ListPendingByBranch(ctx context.Context, tx pgx.Tx, tenantID, branchID uuid.UUID) ([]FiscalPendingRow, error) {
	rows, err := tx.Query(ctx, `
		SELECT s.payment_id, p.check_id, p.amount_total, s.created_at
		FROM fiscal_submissions s
		JOIN payments p ON p.id = s.payment_id
		WHERE s.tenant_id = $1
		  AND s.branch_id = $2
		  AND s.status IN ('pending', 'submitted')
		ORDER BY s.created_at
	`, tenantID, branchID)
	if err != nil {
		return nil, fmt.Errorf("payment/repo: list pending fiscal submissions: %w", err)
	}
	defer rows.Close()

	out := make([]FiscalPendingRow, 0)
	for rows.Next() {
		var row FiscalPendingRow
		if err := rows.Scan(&row.PaymentID, &row.CheckID, &row.AmountTotal, &row.RegisteredAt); err != nil {
			return nil, fmt.Errorf("payment/repo: scan pending fiscal submission: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("payment/repo: iterate pending fiscal submissions: %w", err)
	}
	return out, nil
}

// ListRecentlySettledByBranch returns registrations that reached a terminal
// state within the window, newest first.
//
// DISTINCT ON (payment_id) because fiscal_submissions_active_payment_idx is
// partial (WHERE status IN ('pending','submitted')): it caps in-flight rows at
// one per payment but places no limit on terminal rows, so the same payment
// may legitimately own several settled submissions (a failed attempt later
// voided, say). Emitting a payment twice with conflicting statuses would make
// the poll self-contradictory, so only the latest outcome is reported.
// cutoff is an absolute instant supplied by the caller, not a duration turned
// into an interval against the database's now(): every timestamp this endpoint
// reports (as_of, age_seconds, the window edge) must derive from ONE clock —
// the API host's — or a station comparing them sees an inconsistent picture
// whenever the DB and API hosts drift apart.
func (r *FiscalStatusRepo) ListRecentlySettledByBranch(
	ctx context.Context,
	tx pgx.Tx,
	tenantID, branchID uuid.UUID,
	cutoff time.Time,
) ([]FiscalSettledRow, error) {
	rows, err := tx.Query(ctx, `
		SELECT DISTINCT ON (s.payment_id)
		       s.payment_id, p.check_id, p.amount_total, p.status, s.last_error, s.completed_at
		FROM fiscal_submissions s
		JOIN payments p ON p.id = s.payment_id
		WHERE s.tenant_id = $1
		  AND s.branch_id = $2
		  AND s.completed_at IS NOT NULL
		  AND s.completed_at > $3
		  AND p.status IN ('completed', 'failed', 'voided')
		ORDER BY s.payment_id, s.completed_at DESC
	`, tenantID, branchID, cutoff.UTC())
	if err != nil {
		return nil, fmt.Errorf("payment/repo: list settled fiscal submissions: %w", err)
	}
	defer rows.Close()

	out := make([]FiscalSettledRow, 0)
	for rows.Next() {
		var row FiscalSettledRow
		if err := rows.Scan(&row.PaymentID, &row.CheckID, &row.AmountTotal, &row.Status, &row.FailureReason, &row.SettledAt); err != nil {
			return nil, fmt.Errorf("payment/repo: scan settled fiscal submission: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("payment/repo: iterate settled fiscal submissions: %w", err)
	}

	// DISTINCT ON dictates the ORDER BY above (payment_id first), so the newest
	// -first ordering the client expects has to be applied after the fact.
	sort.Slice(out, func(i, j int) bool { return out[i].SettledAt.After(out[j].SettledAt) })
	return out, nil
}
