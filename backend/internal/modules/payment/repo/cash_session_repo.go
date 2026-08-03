package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"onlinemenu.tr/internal/modules/payment/domain"
)

// ErrCashSessionAlreadyOpen is returned when Open collides with the branch's
// existing open session (cash_sessions_one_open_per_branch, ADR-DATA-008
// Karar 1). The unique index is the actual enforcement mechanism — this
// sentinel only translates its violation into a typed error the service layer
// can distinguish from any other insert failure.
var ErrCashSessionAlreadyOpen = errors.New("payment/repo: branch already has an open cash session")

// CashSessionRepo handles persistence for CashSession and CashMovement.
//
// It also owns the "completed cash payments in a session's window" query
// (SumCompletedCashPayments) even though that reads the payments table: this
// is reconciliation-specific aggregation (ADR-DATA-008's expected-close
// formula), not general payment access, so it belongs with the rest of the
// session's read model rather than on PaymentRepo.
type CashSessionRepo struct{}

func NewCashSessionRepo() *CashSessionRepo { return &CashSessionRepo{} }

// Open inserts a new cash session directly in the 'opened' status. There is
// no intermediate persisted opening_control row — the API is single-step (the
// opening count is supplied at creation) — but the transition is still
// validated through domain.Transition so status assignment never bypasses the
// state machine, including at creation.
func (r *CashSessionRepo) Open(ctx context.Context, tx pgx.Tx, s domain.CashSession) (domain.CashSession, error) {
	if err := domain.Transition(domain.CashSessionOpeningControl, domain.CashSessionOpened); err != nil {
		return domain.CashSession{}, fmt.Errorf("payment/repo: open cash session: %w", err)
	}

	s.ID = uuid.New()
	s.Status = domain.CashSessionOpened
	now := time.Now().UTC()
	s.OpenedAt = now
	s.CreatedAt = now
	s.UpdatedAt = now

	_, err := tx.Exec(ctx, `
		INSERT INTO cash_sessions
			(id, tenant_id, branch_id, status, opening_counted_amount, opening_notes,
			 opened_by, opened_at, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
	`, s.ID, s.TenantID, s.BranchID, string(s.Status), s.OpeningCountedAmount, s.OpeningNotes,
		s.OpenedBy, s.OpenedAt, s.CreatedAt, s.UpdatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return domain.CashSession{}, ErrCashSessionAlreadyOpen
		}
		return domain.CashSession{}, fmt.Errorf("payment/repo: open cash session: %w", err)
	}
	return s, nil
}

// GetActiveByBranch returns the branch's current non-closed session (opened or
// closing_control) — the same set the partial unique index enforces
// uniqueness over. Returns domain.ErrNotFound if the branch has no open
// session.
func (r *CashSessionRepo) GetActiveByBranch(ctx context.Context, tx pgx.Tx, tenantID, branchID uuid.UUID) (domain.CashSession, error) {
	row := tx.QueryRow(ctx, `
		SELECT `+cashSessionColumns+`
		FROM cash_sessions
		WHERE tenant_id = $1 AND branch_id = $2 AND status IN ('opened', 'closing_control')
	`, tenantID, branchID)
	s, err := scanCashSession(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.CashSession{}, domain.ErrNotFound
	}
	return s, err
}

// GetActiveByBranchForShare is GetActiveByBranch under a FOR SHARE row lock.
// PaymentService.RegisterSale uses this (not the plain read) for its cash
// session guard: a plain SELECT takes a per-statement READ COMMITTED
// snapshot and blocks on nothing, so a concurrent Close (whose
// GetByIDForUpdate holds FOR UPDATE on this same row) could commit strictly
// between the guard's read and the payment INSERT — the payment would then
// carry a created_at past the session's closed_at and vanish from
// SumCompletedCashPayments's window forever, exactly the money-invisibility
// bug this guard exists to prevent.
//
// FOR SHARE, not FOR UPDATE: many concurrent cash RegisterSale calls against
// the same open session must proceed together (they do not conflict with
// each other), while any one of them holding the row blocks Close's FOR
// UPDATE until they commit — and, symmetrically, a Close that grabbed the
// X-lock first blocks this call until Close commits, at which point Postgres
// re-evaluates the WHERE clause against the now-closed row (EvalPlanQual)
// and this returns zero rows rather than the stale pre-close version. Either
// interleaving lands on the correct side: the payment is guaranteed to be
// inside an open window, or refused outright.
func (r *CashSessionRepo) GetActiveByBranchForShare(ctx context.Context, tx pgx.Tx, tenantID, branchID uuid.UUID) (domain.CashSession, error) {
	row := tx.QueryRow(ctx, `
		SELECT `+cashSessionColumns+`
		FROM cash_sessions
		WHERE tenant_id = $1 AND branch_id = $2 AND status IN ('opened', 'closing_control')
		FOR SHARE
	`, tenantID, branchID)
	s, err := scanCashSession(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.CashSession{}, domain.ErrNotFound
	}
	return s, err
}

// GetByIDForUpdate loads a session row locked FOR UPDATE, for use inside the
// transaction that will mutate it (submit closing count / close). Locking
// here — rather than relying only on the guarded UPDATEs below — keeps the
// read-then-decide-then-write sequence (status checks, cannotClose guard)
// consistent against a concurrent mutation of the same session.
func (r *CashSessionRepo) GetByIDForUpdate(ctx context.Context, tx pgx.Tx, tenantID, id uuid.UUID) (domain.CashSession, error) {
	row := tx.QueryRow(ctx, `
		SELECT `+cashSessionColumns+`
		FROM cash_sessions
		WHERE tenant_id = $1 AND id = $2
		FOR UPDATE
	`, tenantID, id)
	s, err := scanCashSession(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.CashSession{}, domain.ErrNotFound
	}
	return s, err
}

// GetByID loads a session row with no lock — for reads that only need the
// current status/branch (the PIN-switch flow's session lookup, and
// CashSessionOpenChecker's per-request liveness check), as opposed to
// GetByIDForUpdate's FOR UPDATE lock which is only needed by writers.
func (r *CashSessionRepo) GetByID(ctx context.Context, tx pgx.Tx, tenantID, id uuid.UUID) (domain.CashSession, error) {
	row := tx.QueryRow(ctx, `
		SELECT `+cashSessionColumns+`
		FROM cash_sessions
		WHERE tenant_id = $1 AND id = $2
	`, tenantID, id)
	s, err := scanCashSession(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.CashSession{}, domain.ErrNotFound
	}
	return s, err
}

// UpsertParticipant records that personID joined sessionID (ADR-DATA-008 PIN
// akışı §4). ON CONFLICT refreshes joined_at rather than doing nothing: a
// re-join is the ADR §5 mechanism that clears a person's PIN lockout for
// this session (see service.CashSessionPinService.Join), so the row must
// visibly reflect "joined again", not silently stay at its first join time.
func (r *CashSessionRepo) UpsertParticipant(ctx context.Context, tx pgx.Tx, p domain.CashSessionParticipant) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO cash_session_participants (tenant_id, session_id, branch_id, person_id, joined_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (tenant_id, session_id, person_id)
		DO UPDATE SET joined_at = now()
	`, p.TenantID, p.SessionID, p.BranchID, p.PersonID)
	if err != nil {
		return fmt.Errorf("payment/repo: upsert cash session participant: %w", err)
	}
	return nil
}

// IsParticipant reports whether personID has ever joined sessionID.
func (r *CashSessionRepo) IsParticipant(ctx context.Context, tx pgx.Tx, tenantID, sessionID, personID uuid.UUID) (bool, error) {
	var exists bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM cash_session_participants
			WHERE tenant_id = $1 AND session_id = $2 AND person_id = $3
		)
	`, tenantID, sessionID, personID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("payment/repo: is cash session participant: %w", err)
	}
	return exists, nil
}

// ListParticipants returns every person who has ever joined sessionID
// (ADR-DATA-008 PIN akışı §4), ordered by joined_at so the earliest joiner —
// typically whoever opened the till — sorts first.
func (r *CashSessionRepo) ListParticipants(ctx context.Context, tx pgx.Tx, tenantID, sessionID uuid.UUID) ([]domain.CashSessionParticipant, error) {
	rows, err := tx.Query(ctx, `
		SELECT tenant_id, session_id, branch_id, person_id, joined_at
		FROM cash_session_participants
		WHERE tenant_id = $1 AND session_id = $2
		ORDER BY joined_at ASC
	`, tenantID, sessionID)
	if err != nil {
		return nil, fmt.Errorf("payment/repo: list cash session participants: %w", err)
	}
	defer rows.Close()

	var out []domain.CashSessionParticipant
	for rows.Next() {
		var p domain.CashSessionParticipant
		if err := rows.Scan(&p.TenantID, &p.SessionID, &p.BranchID, &p.PersonID, &p.JoinedAt); err != nil {
			return nil, fmt.Errorf("payment/repo: list cash session participants: scan: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("payment/repo: list cash session participants: %w", err)
	}
	return out, nil
}

// SubmitClosingCount persists a (possibly amended) closing count and moves the
// session to closing_control. from must be the status already read under
// FOR UPDATE in the same transaction; the WHERE clause re-asserts it so a
// concurrent transition between the read and this write aborts loudly
// (0 rows affected) instead of silently overwriting it.
func (r *CashSessionRepo) SubmitClosingCount(
	ctx context.Context, tx pgx.Tx,
	tenantID, id uuid.UUID, from domain.CashSessionStatus,
	closingCountedAmount int64, denominations []domain.DenominationCount, notes string,
) (domain.CashSession, error) {
	if err := domain.Transition(from, domain.CashSessionClosingControl); err != nil {
		return domain.CashSession{}, fmt.Errorf("payment/repo: submit closing count: %w", err)
	}

	denomJSON, err := marshalDenominations(denominations)
	if err != nil {
		return domain.CashSession{}, fmt.Errorf("payment/repo: submit closing count: %w", err)
	}
	now := time.Now().UTC()

	tag, err := tx.Exec(ctx, `
		UPDATE cash_sessions
		SET status = $1, closing_counted_amount = $2, closing_denominations = $3,
		    closing_notes = $4, closing_submitted_at = $5, updated_at = $5
		WHERE tenant_id = $6 AND id = $7 AND status = $8
	`, string(domain.CashSessionClosingControl), closingCountedAmount, denomJSON, notes, now,
		tenantID, id, string(from))
	if err != nil {
		return domain.CashSession{}, fmt.Errorf("payment/repo: submit closing count: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.CashSession{}, domain.ErrNotFound
	}
	return r.GetByIDForUpdate(ctx, tx, tenantID, id)
}

// Close finalises a session. from must be the status read under FOR UPDATE in
// the same transaction, same re-assertion rationale as SubmitClosingCount.
func (r *CashSessionRepo) Close(ctx context.Context, tx pgx.Tx, tenantID, id uuid.UUID, from domain.CashSessionStatus, closedBy uuid.UUID) (domain.CashSession, error) {
	if err := domain.Transition(from, domain.CashSessionClosed); err != nil {
		return domain.CashSession{}, fmt.Errorf("payment/repo: close cash session: %w", err)
	}
	now := time.Now().UTC()

	tag, err := tx.Exec(ctx, `
		UPDATE cash_sessions
		SET status = $1, closed_by = $2, closed_at = $3, updated_at = $3
		WHERE tenant_id = $4 AND id = $5 AND status = $6
	`, string(domain.CashSessionClosed), closedBy, now, tenantID, id, string(from))
	if err != nil {
		return domain.CashSession{}, fmt.Errorf("payment/repo: close cash session: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.CashSession{}, domain.ErrNotFound
	}
	return r.GetByIDForUpdate(ctx, tx, tenantID, id)
}

// InsertMovement records one in-shift cash in/out.
func (r *CashSessionRepo) InsertMovement(ctx context.Context, tx pgx.Tx, m domain.CashMovement) (domain.CashMovement, error) {
	m.ID = uuid.New()
	m.CreatedAt = time.Now().UTC()

	_, err := tx.Exec(ctx, `
		INSERT INTO cash_movements
			(id, tenant_id, branch_id, session_id, direction, amount_minor, reason, created_by, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
	`, m.ID, m.TenantID, m.BranchID, m.SessionID, string(m.Direction), m.AmountMinor, m.Reason, m.CreatedBy, m.CreatedAt)
	if err != nil {
		return domain.CashMovement{}, fmt.Errorf("payment/repo: insert cash movement: %w", err)
	}
	return m, nil
}

// SumMovementsNet returns the net cash movement total for a session: 'in'
// movements add, 'out' movements subtract. Zero when the session has no
// movements yet.
func (r *CashSessionRepo) SumMovementsNet(ctx context.Context, tx pgx.Tx, tenantID, sessionID uuid.UUID) (int64, error) {
	var net int64
	err := tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(CASE WHEN direction = 'in' THEN amount_minor ELSE -amount_minor END), 0)
		FROM cash_movements
		WHERE tenant_id = $1 AND session_id = $2
	`, tenantID, sessionID).Scan(&net)
	if err != nil {
		return 0, fmt.Errorf("payment/repo: sum cash movements: %w", err)
	}
	return net, nil
}

// SumCompletedCashPayments returns the total of completed cash payments taken
// in the branch within [since, until). until == nil means "no upper bound"
// (an open session reconciled live). Only method = 'cash' counts — terminal,
// meal_card, comp, no_charge and open_account payments never touch the
// physical drawer this session reconciles, so they must never enter the sum
// (a != 'terminal' style negation would silently absorb them; this is a
// positive == 'cash' filter instead).
//
// The window is created_at (when the cashier took the money), not
// completed_at (when fiscal registration confirmed it): the close guard
// (cannotClose) already refuses to close while the branch has ANY pending
// fiscal submission, so by the time a close actually succeeds every cash sale
// taken during the session's window has already reached a terminal fiscal
// state. Keying on completed_at instead would let a sale that completes
// fiscal registration after the session closes permanently fall outside every
// window (Odoo's _compute_cash_balance note this repo's audit read: the
// formula must stop moving once the session is closed).
func (r *CashSessionRepo) SumCompletedCashPayments(ctx context.Context, tx pgx.Tx, tenantID, branchID uuid.UUID, since time.Time, until *time.Time) (int64, error) {
	var total int64
	var err error
	if until == nil {
		err = tx.QueryRow(ctx, `
			SELECT COALESCE(SUM(amount_total), 0)
			FROM payments
			WHERE tenant_id = $1 AND branch_id = $2 AND method = 'cash' AND status = 'completed'
			  AND created_at >= $3
		`, tenantID, branchID, since.UTC()).Scan(&total)
	} else {
		err = tx.QueryRow(ctx, `
			SELECT COALESCE(SUM(amount_total), 0)
			FROM payments
			WHERE tenant_id = $1 AND branch_id = $2 AND method = 'cash' AND status = 'completed'
			  AND created_at >= $3 AND created_at < $4
		`, tenantID, branchID, since.UTC(), until.UTC()).Scan(&total)
	}
	if err != nil {
		return 0, fmt.Errorf("payment/repo: sum completed cash payments: %w", err)
	}
	return total, nil
}

const cashSessionColumns = `
	id, tenant_id, branch_id, status, opening_counted_amount, opening_notes,
	opened_by, opened_at, closing_counted_amount, closing_denominations,
	closing_notes, closing_submitted_at, closed_by, closed_at, created_at, updated_at`

func scanCashSession(row pgx.Row) (domain.CashSession, error) {
	var s domain.CashSession
	var status string
	var denomJSON []byte
	err := row.Scan(
		&s.ID, &s.TenantID, &s.BranchID, &status, &s.OpeningCountedAmount, &s.OpeningNotes,
		&s.OpenedBy, &s.OpenedAt, &s.ClosingCountedAmount, &denomJSON,
		&s.ClosingNotes, &s.ClosingSubmittedAt, &s.ClosedBy, &s.ClosedAt, &s.CreatedAt, &s.UpdatedAt,
	)
	if err != nil {
		return domain.CashSession{}, err
	}
	s.Status = domain.CashSessionStatus(status)
	if len(denomJSON) > 0 {
		if err := json.Unmarshal(denomJSON, &s.ClosingDenominations); err != nil {
			return domain.CashSession{}, fmt.Errorf("payment/repo: unmarshal closing denominations: %w", err)
		}
	}
	return s, nil
}

// marshalDenominations returns nil (SQL NULL) for an empty breakdown rather
// than the JSON literal "[]" or "null" — GetActive's zero-value check
// (len(denomJSON) > 0) relies on a nil column round-tripping to a nil/empty
// slice. The non-nil case is returned as a string, not []byte: this pool runs
// in simple-protocol mode (see repo/integration_test.go's DefaultQueryExecMode),
// where a []byte parameter is sent as hex-bytea rather than JSON text —
// exactly the pitfall PaymentRepo.InsertFiscalReceipt documents for
// fiscal_receipts.receipt_data. Postgres coerces the text parameter to jsonb
// on assignment.
func marshalDenominations(d []domain.DenominationCount) (any, error) {
	if len(d) == 0 {
		return nil, nil
	}
	data, err := json.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("marshal denominations: %w", err)
	}
	return string(data), nil
}
