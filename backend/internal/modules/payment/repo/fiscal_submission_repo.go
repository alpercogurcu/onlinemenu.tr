package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"onlinemenu.tr/internal/modules/payment/domain"
)

// ErrActiveSubmissionExists is returned when a payment already has a fiscal
// submission in a non-terminal state (partial unique index on payment_id
// WHERE status IN ('pending','submitted')).
var ErrActiveSubmissionExists = errors.New("payment/repo: active fiscal submission already exists")

// FiscalSubmission is the persisted record of one fiscal registration attempt.
// SalePayload is the marshalled domain.FiscalSale; the worker replays it to the
// adapter without reconstructing it from the payment aggregate.
type FiscalSubmission struct {
	ID             uuid.UUID
	TenantID       uuid.UUID
	BranchID       uuid.UUID
	PaymentID      uuid.UUID
	AdapterType    string
	TerminalSerial string
	Status         domain.FiscalSubmissionStatus
	SalePayload    []byte
	RetryCount     int
	// SubmittedAt is populated only by ListStaleSubmitted; the claim path leaves
	// it nil because a pending row has not been submitted yet.
	SubmittedAt *time.Time
}

// FiscalSubmissionRepo persists fiscal submissions (ADR-FISCAL-002).
type FiscalSubmissionRepo struct{}

func NewFiscalSubmissionRepo() *FiscalSubmissionRepo { return &FiscalSubmissionRepo{} }

// Insert writes a pending submission inside the caller's transaction, so the
// payment row and its fiscal submission commit atomically (outbox pattern).
func (r *FiscalSubmissionRepo) Insert(ctx context.Context, tx pgx.Tx, sub FiscalSubmission) error {
	// Pass JSONB as string: under QueryExecModeSimpleProtocol a []byte would be
	// sent as hex-bytea, not valid JSON (same rationale as InsertFiscalReceipt).
	_, err := tx.Exec(ctx, `
		INSERT INTO fiscal_submissions
			(id, tenant_id, branch_id, payment_id, adapter_type, terminal_serial, status, sale_payload)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
	`, sub.ID, sub.TenantID, sub.BranchID, sub.PaymentID, sub.AdapterType,
		nullableText(sub.TerminalSerial), string(domain.FiscalSubmissionPending), string(sub.SalePayload))
	if err != nil {
		if isUniqueViolation(err) {
			return ErrActiveSubmissionExists
		}
		return fmt.Errorf("payment/repo: insert fiscal submission: %w", err)
	}
	return nil
}

// ClaimPending atomically marks up to batchSize eligible pending submissions as
// claimed and returns them for submission to the device adapter.
//
// The caller MUST supply a cross-tenant transaction (db.WithAllTenantsTx): the
// worker scans every tenant's rows, which the per-tenant RLS policy forbids.
// See the fiscal_submissions all-tenants policy in the payment migrations.
//
// Eligible rows are pending, past their retry backoff, and either never claimed
// or claimed longer than staleAfter ago — the latter recovers rows stranded by a
// worker that crashed between claim and result.
//
// CAUTION: reclaiming can re-submit a basket the vendor already received. Token
// does NOT document what a duplicate basketID POST does (ADR-FISCAL-002 open
// question); until certification testing settles it, staleAfter must stay well
// above the adapter's worst-case submit time so overlap cannot happen in
// normal operation.
func (r *FiscalSubmissionRepo) ClaimPending(ctx context.Context, tx pgx.Tx, batchSize int, staleAfter time.Duration) ([]FiscalSubmission, error) {
	if batchSize <= 0 {
		batchSize = 20
	}
	// Truncated to whole seconds for the INTERVAL literal; guard a sub-second
	// staleAfter from rounding to 0 and making every claimed row instantly
	// reclaimable (mirrors outbox.claimBatch).
	staleSeconds := int(staleAfter.Seconds())
	if staleSeconds < 1 {
		staleSeconds = 1
	}

	query := fmt.Sprintf(`
		UPDATE fiscal_submissions
		SET claimed_at = NOW()
		WHERE id IN (
			SELECT id
			FROM fiscal_submissions
			WHERE status = 'pending'
			  AND (next_retry_at IS NULL OR next_retry_at <= NOW())
			  AND (claimed_at IS NULL OR claimed_at <= NOW() - INTERVAL '%d seconds')
			ORDER BY created_at
			FOR UPDATE SKIP LOCKED
			LIMIT %d
		)
		RETURNING id, tenant_id, branch_id, payment_id, adapter_type,
		          terminal_serial, status, sale_payload, retry_count
	`, staleSeconds, batchSize)

	rows, err := tx.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("payment/repo: claim pending submissions: %w", err)
	}
	defer rows.Close()

	var batch []FiscalSubmission
	for rows.Next() {
		var (
			sub      FiscalSubmission
			terminal *string
			status   string
		)
		if err := rows.Scan(
			&sub.ID, &sub.TenantID, &sub.BranchID, &sub.PaymentID, &sub.AdapterType,
			&terminal, &status, &sub.SalePayload, &sub.RetryCount,
		); err != nil {
			return nil, fmt.Errorf("payment/repo: scan claimed submission: %w", err)
		}
		if terminal != nil {
			sub.TerminalSerial = *terminal
		}
		sub.Status = domain.FiscalSubmissionStatus(status)
		batch = append(batch, sub)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("payment/repo: claim pending rows: %w", err)
	}
	return batch, nil
}

// MarkSubmitted moves a claimed row to submitted, meaning the adapter accepted
// the basket and the result will arrive asynchronously.
//
// It reports whether the transition happened. A false result is not an error: a
// synchronous vendor callback may have already driven the row to a terminal
// state before the worker got here.
func (r *FiscalSubmissionRepo) MarkSubmitted(ctx context.Context, tx pgx.Tx, id uuid.UUID) (bool, error) {
	tag, err := tx.Exec(ctx, `
		UPDATE fiscal_submissions
		SET status = 'submitted', submitted_at = NOW(), claimed_at = NULL
		WHERE id = $1 AND status = 'pending'
	`, id)
	if err != nil {
		return false, fmt.Errorf("payment/repo: mark submission submitted: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// MarkResult applies a terminal outcome and reports whether this call performed
// the transition. Duplicate deliveries (webhook retry, reconciliation sweep)
// find the row already terminal and get (false, nil) — the caller must then skip
// every side effect (receipt insert, payment update, outbox event).
//
// Allowed source states depend on the target: a sale completes or fails only
// from pending/submitted, while a void may also cancel an already-completed
// registration (fiş iptali).
func (r *FiscalSubmissionRepo) MarkResult(
	ctx context.Context,
	tx pgx.Tx,
	id uuid.UUID,
	status domain.FiscalSubmissionStatus,
	resultPayload []byte,
	lastError string,
	completedAt time.Time,
) (bool, error) {
	var sourceStates string
	switch status {
	case domain.FiscalSubmissionCompleted, domain.FiscalSubmissionFailed, domain.FiscalSubmissionExpired:
		sourceStates = `('pending','submitted')`
	case domain.FiscalSubmissionVoided:
		sourceStates = `('pending','submitted','completed')`
	default:
		return false, fmt.Errorf("payment/repo: mark result: %q is not a terminal status", status)
	}

	var completed *time.Time
	if !completedAt.IsZero() {
		utc := completedAt.UTC()
		completed = &utc
	}

	tag, err := tx.Exec(ctx, fmt.Sprintf(`
		UPDATE fiscal_submissions
		SET status         = $2,
		    result_payload = $3,
		    last_error     = $4,
		    completed_at   = COALESCE($5, NOW()),
		    claimed_at     = NULL
		WHERE id = $1 AND status IN %s
	`, sourceStates), id, string(status), nullableJSON(resultPayload), nullableText(lastError), completed)
	if err != nil {
		return false, fmt.Errorf("payment/repo: mark submission result: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// MarkRetry schedules the next submission attempt after a transient adapter
// error. claimed_at is cleared so the row becomes eligible as soon as
// next_retry_at elapses, rather than also waiting out the stale-claim window.
func (r *FiscalSubmissionRepo) MarkRetry(
	ctx context.Context,
	tx pgx.Tx,
	id uuid.UUID,
	retryCount int,
	nextRetryAt time.Time,
	lastError string,
) error {
	tag, err := tx.Exec(ctx, `
		UPDATE fiscal_submissions
		SET retry_count   = $2,
		    next_retry_at = $3,
		    last_error    = $4,
		    claimed_at    = NULL
		WHERE id = $1 AND status = 'pending'
	`, id, retryCount, nextRetryAt.UTC(), nullableText(lastError))
	if err != nil {
		return fmt.Errorf("payment/repo: mark submission retry: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListStaleSubmitted returns submissions that were handed to a device but never
// reached a terminal state within staleAfter — a webhook that never arrived, a
// device left off the sales screen, or a basket abandoned by the cashier.
//
// Like ClaimPending this scans every tenant, so the caller must supply a
// cross-tenant transaction (db.WithAllTenantsReadTx). No row is claimed: the
// reconciler's only write goes through MarkResult, which is idempotent, so two
// replicas observing the same stale row cannot double-apply it.
func (r *FiscalSubmissionRepo) ListStaleSubmitted(ctx context.Context, tx pgx.Tx, batchSize int, staleAfter time.Duration) ([]FiscalSubmission, error) {
	if batchSize <= 0 {
		batchSize = 100
	}
	staleSeconds := int(staleAfter.Seconds())
	if staleSeconds < 1 {
		staleSeconds = 1
	}

	query := fmt.Sprintf(`
		SELECT id, tenant_id, branch_id, payment_id, adapter_type,
		       terminal_serial, status, sale_payload, retry_count, submitted_at
		FROM fiscal_submissions
		WHERE status = 'submitted'
		  AND submitted_at IS NOT NULL
		  AND submitted_at <= NOW() - INTERVAL '%d seconds'
		ORDER BY submitted_at
		LIMIT %d
	`, staleSeconds, batchSize)

	rows, err := tx.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("payment/repo: list stale submissions: %w", err)
	}
	defer rows.Close()

	var out []FiscalSubmission
	for rows.Next() {
		var (
			sub      FiscalSubmission
			terminal *string
			status   string
		)
		if err := rows.Scan(
			&sub.ID, &sub.TenantID, &sub.BranchID, &sub.PaymentID, &sub.AdapterType,
			&terminal, &status, &sub.SalePayload, &sub.RetryCount, &sub.SubmittedAt,
		); err != nil {
			return nil, fmt.Errorf("payment/repo: scan stale submission: %w", err)
		}
		if terminal != nil {
			sub.TerminalSerial = *terminal
		}
		sub.Status = domain.FiscalSubmissionStatus(status)
		out = append(out, sub)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("payment/repo: stale submission rows: %w", err)
	}
	return out, nil
}

// GetByPaymentID returns the most recent submission for a payment.
func (r *FiscalSubmissionRepo) GetByPaymentID(ctx context.Context, tx pgx.Tx, paymentID uuid.UUID) (FiscalSubmission, error) {
	row := tx.QueryRow(ctx, `
		SELECT id, tenant_id, branch_id, payment_id, adapter_type,
		       terminal_serial, status, sale_payload, retry_count
		FROM fiscal_submissions
		WHERE payment_id = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, paymentID)

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
		return FiscalSubmission{}, fmt.Errorf("payment/repo: get submission by payment: %w", err)
	}
	if terminal != nil {
		sub.TerminalSerial = *terminal
	}
	sub.Status = domain.FiscalSubmissionStatus(status)
	return sub, nil
}

func nullableText(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func nullableJSON(b []byte) *string {
	if len(b) == 0 {
		return nil
	}
	s := string(b)
	return &s
}

// SubmissionRouting is the tenant/payment identity a vendor webhook needs to
// enrich its result before it can reach the sink.
type SubmissionRouting struct {
	TenantID  uuid.UUID
	BranchID  uuid.UUID
	PaymentID uuid.UUID
}

// GetRouting resolves a submission id to its owning tenant, branch and
// payment. A vendor webhook carries only the basketID (= submission id) and no
// tenant context, so the caller MUST run this in db.WithAllTenantsReadTx; the
// all-tenants SELECT policy on fiscal_submissions exists for exactly this.
func (r *FiscalSubmissionRepo) GetRouting(ctx context.Context, tx pgx.Tx, id uuid.UUID) (SubmissionRouting, error) {
	var routing SubmissionRouting
	err := tx.QueryRow(ctx, `
		SELECT tenant_id, branch_id, payment_id
		FROM fiscal_submissions
		WHERE id = $1
	`, id).Scan(&routing.TenantID, &routing.BranchID, &routing.PaymentID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SubmissionRouting{}, ErrNotFound
		}
		return SubmissionRouting{}, fmt.Errorf("payment/repo: get submission routing: %w", err)
	}
	return routing, nil
}
