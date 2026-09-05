package repo_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/modules/payment/domain"
	"onlinemenu.tr/internal/modules/payment/repo"
)

// ---------------------------------------------------------------------------
// One-open-session-per-branch (ADR-DATA-008 Karar 1)
// ---------------------------------------------------------------------------

// TestCashSessionRepo_Open_SecondOpenSameBranchIsRejected is the sequential
// half of the constraint: the partial unique index, not application logic,
// must be what stops a second open session.
func TestCashSessionRepo_Open_SecondOpenSameBranchIsRejected(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	r := repo.NewCashSessionRepo()
	branch := uuid.New()

	openSession := func() (domain.CashSession, error) {
		var s domain.CashSession
		err := sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
			var err error
			s, err = r.Open(ctx, tx, domain.CashSession{
				TenantID:             tenantA,
				BranchID:             branch,
				OpeningCountedAmount: 10000,
				OpenedBy:             uuid.New(),
			})
			return err
		})
		return s, err
	}

	first, err := openSession()
	require.NoError(t, err)
	assert.Equal(t, domain.CashSessionOpened, first.Status)

	_, err = openSession()
	require.ErrorIs(t, err, repo.ErrCashSessionAlreadyOpen)
}

// TestCashSessionRepo_Open_ConcurrentSameBranch_ExactlyOneWins is the
// concurrency-safety proof the task specifically calls for: N goroutines race
// to open the same branch, and the unique index — not a check-then-insert in
// Go — must ensure exactly one succeeds.
func TestCashSessionRepo_Open_ConcurrentSameBranch_ExactlyOneWins(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	r := repo.NewCashSessionRepo()
	branch := uuid.New()

	const n = 8
	var wg sync.WaitGroup
	wg.Add(n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			errs[i] = sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
				_, err := r.Open(ctx, tx, domain.CashSession{
					TenantID:             tenantA,
					BranchID:             branch,
					OpeningCountedAmount: 5000,
					OpenedBy:             uuid.New(),
				})
				return err
			})
		}(i)
	}
	wg.Wait()

	var succeeded, alreadyOpen int
	for _, err := range errs {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, repo.ErrCashSessionAlreadyOpen):
			alreadyOpen++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	assert.Equal(t, 1, succeeded, "exactly one concurrent Open must win the race")
	assert.Equal(t, n-1, alreadyOpen, "every loser must observe ErrCashSessionAlreadyOpen, not a raw constraint error")

	var count int
	err := sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT COUNT(*) FROM cash_sessions WHERE tenant_id = $1 AND branch_id = $2
		`, tenantA, branch).Scan(&count)
	})
	require.NoError(t, err)
	assert.Equal(t, 1, count, "exactly one cash_sessions row may exist for the branch")
}

// ---------------------------------------------------------------------------
// Lifecycle: opened -> closing_control -> closed, and rejected transitions
// ---------------------------------------------------------------------------

// TestCashSessionRepo_FullLifecycle drives one session through every state
// and checks the persisted fields at each step.
func TestCashSessionRepo_FullLifecycle(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	r := repo.NewCashSessionRepo()
	branch := uuid.New()
	openedBy := uuid.New()
	closedBy := uuid.New()

	var session domain.CashSession
	err := sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		session, err = r.Open(ctx, tx, domain.CashSession{
			TenantID:             tenantA,
			BranchID:             branch,
			OpeningCountedAmount: 20000,
			OpeningNotes:         "acilis",
			OpenedBy:             openedBy,
		})
		return err
	})
	require.NoError(t, err)
	assert.Equal(t, domain.CashSessionOpened, session.Status)
	assert.Equal(t, int64(20000), session.OpeningCountedAmount)

	// Read it back via GetActiveByBranch.
	err = sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		active, err := r.GetActiveByBranch(ctx, tx, tenantA, branch)
		if err != nil {
			return err
		}
		assert.Equal(t, session.ID, active.ID)
		return nil
	})
	require.NoError(t, err)

	// Submit closing count.
	denoms := []domain.DenominationCount{{DenominationMinor: 20000, Count: 1}}
	err = sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		current, err := r.GetByIDForUpdate(ctx, tx, tenantA, session.ID)
		if err != nil {
			return err
		}
		session, err = r.SubmitClosingCount(ctx, tx, tenantA, session.ID, current.Status, 20000, denoms, "tam sayim")
		return err
	})
	require.NoError(t, err)
	assert.Equal(t, domain.CashSessionClosingControl, session.Status)
	require.NotNil(t, session.ClosingCountedAmount)
	assert.Equal(t, int64(20000), *session.ClosingCountedAmount)
	require.Len(t, session.ClosingDenominations, 1)
	assert.Equal(t, int64(20000), session.ClosingDenominations[0].DenominationMinor)

	// Recount (closing_control -> closing_control self-loop).
	err = sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		current, err := r.GetByIDForUpdate(ctx, tx, tenantA, session.ID)
		if err != nil {
			return err
		}
		session, err = r.SubmitClosingCount(ctx, tx, tenantA, session.ID, current.Status, 19500, nil, "yeniden sayildi")
		return err
	})
	require.NoError(t, err)
	assert.Equal(t, int64(19500), *session.ClosingCountedAmount)
	assert.Empty(t, session.ClosingDenominations, "an omitted breakdown on recount must clear the previous one, not leave it stale")

	// Close.
	err = sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		current, err := r.GetByIDForUpdate(ctx, tx, tenantA, session.ID)
		if err != nil {
			return err
		}
		session, err = r.Close(ctx, tx, tenantA, session.ID, current.Status, closedBy)
		return err
	})
	require.NoError(t, err)
	assert.Equal(t, domain.CashSessionClosed, session.Status)
	require.NotNil(t, session.ClosedBy)
	assert.Equal(t, closedBy, *session.ClosedBy)
	require.NotNil(t, session.ClosedAt)

	// A closed session must no longer be "active" for the branch, and the
	// branch must now accept a new Open (proves the partial index only covers
	// opened/closing_control, not closed).
	err = sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		_, err := r.GetActiveByBranch(ctx, tx, tenantA, branch)
		return err
	})
	assert.ErrorIs(t, err, domain.ErrNotFound)

	err = sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		_, err := r.Open(ctx, tx, domain.CashSession{
			TenantID: tenantA, BranchID: branch, OpeningCountedAmount: 0, OpenedBy: openedBy,
		})
		return err
	})
	assert.NoError(t, err, "a closed session must free the branch for a new one")
}

// TestCashSessionRepo_Close_RejectsFromOpened proves the state guard: closing
// without first submitting a closing count is rejected, not silently allowed.
func TestCashSessionRepo_Close_RejectsFromOpened(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	r := repo.NewCashSessionRepo()
	branch := uuid.New()

	var session domain.CashSession
	err := sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		session, err = r.Open(ctx, tx, domain.CashSession{
			TenantID: tenantA, BranchID: branch, OpeningCountedAmount: 1000, OpenedBy: uuid.New(),
		})
		return err
	})
	require.NoError(t, err)

	err = sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		_, err := r.Close(ctx, tx, tenantA, session.ID, domain.CashSessionOpened, uuid.New())
		return err
	})
	require.ErrorIs(t, err, domain.ErrInvalidCashSessionTransition)
}

// ---------------------------------------------------------------------------
// Participants (ADR-DATA-008 PIN akışı §4)
// ---------------------------------------------------------------------------

// TestCashSessionRepo_ListParticipants_OrderedByJoinedAt proves ListParticipants
// returns every joined participant ordered earliest-first, and that a re-join
// (UpsertParticipant's ON CONFLICT DO UPDATE) refreshes joined_at rather than
// duplicating the row.
func TestCashSessionRepo_ListParticipants_OrderedByJoinedAt(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	r := repo.NewCashSessionRepo()
	sessionID, branchID := uuid.New(), uuid.New()
	first, second := uuid.New(), uuid.New()

	join := func(personID uuid.UUID) {
		err := sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
			return r.UpsertParticipant(ctx, tx, domain.CashSessionParticipant{
				TenantID: tenantA, SessionID: sessionID, BranchID: branchID, PersonID: personID,
			})
		})
		require.NoError(t, err)
	}

	join(first)
	time.Sleep(10 * time.Millisecond) // ensure joined_at strictly orders first < second
	join(second)

	var participants []domain.CashSessionParticipant
	err := sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		participants, err = r.ListParticipants(ctx, tx, tenantA, sessionID)
		return err
	})
	require.NoError(t, err)
	require.Len(t, participants, 2)
	assert.Equal(t, first, participants[0].PersonID)
	assert.Equal(t, second, participants[1].PersonID)

	// Re-join `first` — must still report exactly 2 rows (upsert, not insert),
	// and `first` must now sort last since its joined_at was just refreshed.
	join(first)
	err = sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		participants, err = r.ListParticipants(ctx, tx, tenantA, sessionID)
		return err
	})
	require.NoError(t, err)
	require.Len(t, participants, 2, "re-join must update the existing row, not insert a duplicate")
	assert.Equal(t, second, participants[0].PersonID)
	assert.Equal(t, first, participants[1].PersonID)
}

// TestCashSessionRepo_ListParticipants_NoParticipants_ReturnsEmpty guards
// against a nil-vs-empty surprise at the HTTP layer: a session nobody has
// joined must produce a zero-length slice, not an error.
func TestCashSessionRepo_ListParticipants_NoParticipants_ReturnsEmpty(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	r := repo.NewCashSessionRepo()

	var participants []domain.CashSessionParticipant
	err := sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		participants, err = r.ListParticipants(ctx, tx, tenantA, uuid.New())
		return err
	})
	require.NoError(t, err)
	assert.Empty(t, participants)
}

// ---------------------------------------------------------------------------
// Movements
// ---------------------------------------------------------------------------

func TestCashSessionRepo_SumMovementsNet(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	r := repo.NewCashSessionRepo()
	branch := uuid.New()

	var session domain.CashSession
	err := sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		session, err = r.Open(ctx, tx, domain.CashSession{
			TenantID: tenantA, BranchID: branch, OpeningCountedAmount: 0, OpenedBy: uuid.New(),
		})
		return err
	})
	require.NoError(t, err)

	err = sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		for _, m := range []domain.CashMovement{
			{TenantID: tenantA, BranchID: branch, SessionID: session.ID, Direction: domain.CashMovementIn, AmountMinor: 5000, Reason: "bozuk para", CreatedBy: uuid.New()},
			{TenantID: tenantA, BranchID: branch, SessionID: session.ID, Direction: domain.CashMovementOut, AmountMinor: 2000, Reason: "kasadan alma", CreatedBy: uuid.New()},
			{TenantID: tenantA, BranchID: branch, SessionID: session.ID, Direction: domain.CashMovementOut, AmountMinor: 1000, Reason: "kasadan alma 2", CreatedBy: uuid.New()},
		} {
			if _, err := r.InsertMovement(ctx, tx, m); err != nil {
				return err
			}
		}
		return nil
	})
	require.NoError(t, err)

	var net int64
	err = sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		net, err = r.SumMovementsNet(ctx, tx, tenantA, session.ID)
		return err
	})
	require.NoError(t, err)
	assert.Equal(t, int64(2000), net) // 5000 - 2000 - 1000
}

func TestCashSessionRepo_SumMovementsNet_NoMovements(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	r := repo.NewCashSessionRepo()

	var net int64
	err := sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		net, err = r.SumMovementsNet(ctx, tx, tenantA, uuid.New())
		return err
	})
	require.NoError(t, err)
	assert.Equal(t, int64(0), net)
}

// TestCashSessionRepo_ListMovements_OrderedByCreatedAt is the movement
// ledger's read side (the POS cash-session screen's defter): every movement
// ever recorded against a session, oldest first, so the ledger renders as
// the cashier lived it rather than in insertion/id order.
func TestCashSessionRepo_ListMovements_OrderedByCreatedAt(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	r := repo.NewCashSessionRepo()
	branch := uuid.New()

	var session domain.CashSession
	err := sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		session, err = r.Open(ctx, tx, domain.CashSession{
			TenantID: tenantA, BranchID: branch, OpeningCountedAmount: 0, OpenedBy: uuid.New(),
		})
		return err
	})
	require.NoError(t, err)

	base := time.Now().UTC().Add(-time.Hour)
	inserted := make([]domain.CashMovement, 3)
	err = sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		for i, m := range []domain.CashMovement{
			{TenantID: tenantA, BranchID: branch, SessionID: session.ID, Direction: domain.CashMovementIn, AmountMinor: 5000, Reason: "bozuk para", CreatedBy: uuid.New()},
			{TenantID: tenantA, BranchID: branch, SessionID: session.ID, Direction: domain.CashMovementOut, AmountMinor: 2000, Reason: "kasadan alma", CreatedBy: uuid.New()},
			{TenantID: tenantA, BranchID: branch, SessionID: session.ID, Direction: domain.CashMovementIn, AmountMinor: 1000, Reason: "bozuk para 2", CreatedBy: uuid.New()},
		} {
			got, err := r.InsertMovement(ctx, tx, m)
			if err != nil {
				return err
			}
			inserted[i] = got
		}
		// InsertMovement stamps CreatedAt from time.Now(), which can tie at
		// this test's resolution; force a deterministic, strictly increasing
		// order so the assertion below is not a coin flip.
		for i, m := range inserted {
			ts := base.Add(time.Duration(i) * time.Minute)
			if _, err := tx.Exec(ctx, `UPDATE cash_movements SET created_at = $1 WHERE id = $2`, ts, m.ID); err != nil {
				return err
			}
			inserted[i].CreatedAt = ts
		}
		return nil
	})
	require.NoError(t, err)

	var movements []domain.CashMovement
	err = sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		movements, err = r.ListMovements(ctx, tx, tenantA, session.ID)
		return err
	})
	require.NoError(t, err)

	require.Len(t, movements, 3)
	for i, want := range inserted {
		assert.Equal(t, want.ID, movements[i].ID, "movement %d out of order", i)
		assert.Equal(t, want.Direction, movements[i].Direction)
		assert.Equal(t, want.AmountMinor, movements[i].AmountMinor)
		assert.Equal(t, want.Reason, movements[i].Reason)
		assert.Equal(t, want.CreatedBy, movements[i].CreatedBy)
		assert.WithinDuration(t, want.CreatedAt, movements[i].CreatedAt, time.Second)
	}
}

// TestCashSessionRepo_ListMovements_NoMovements_ReturnsEmpty guards the
// zero-movements case: a freshly opened session's ledger must render as an
// empty list, not an error or a nil the JSON layer would serialize as null.
func TestCashSessionRepo_ListMovements_NoMovements_ReturnsEmpty(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	r := repo.NewCashSessionRepo()

	var movements []domain.CashMovement
	err := sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		movements, err = r.ListMovements(ctx, tx, tenantA, uuid.New())
		return err
	})
	require.NoError(t, err)
	assert.Empty(t, movements)
}

// ---------------------------------------------------------------------------
// SumCompletedCashPayments — the expected-close window
// ---------------------------------------------------------------------------

// TestCashSessionRepo_SumCompletedCashPayments_FiltersMethodStatusAndWindow
// pins every dimension of the filter in one place: only 'cash' counts (not
// 'terminal'), only 'completed' counts (not 'pending'/'failed'), and only
// created_at within [since, until) counts.
func TestCashSessionRepo_SumCompletedCashPayments_FiltersMethodStatusAndWindow(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	r := repo.NewCashSessionRepo()
	pr := repo.NewPaymentRepo()
	branch := uuid.New()
	since := time.Now().UTC().Add(-time.Hour)

	makeCompleted := func(method domain.PaymentMethod, amount int64, createdAt time.Time) {
		err := sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
			p, err := pr.Create(ctx, tx, domain.Payment{
				TenantID: tenantA, BranchID: branch, IdempotencyKey: uuid.New().String(),
				Method: method, AmountTotal: amount, Currency: "TRY",
			})
			if err != nil {
				return err
			}
			// Backdate created_at directly — Create always stamps "now".
			if _, err := tx.Exec(ctx, `UPDATE payments SET created_at = $2 WHERE id = $1`, p.ID, createdAt); err != nil {
				return err
			}
			receiptID, err := pr.InsertFiscalReceipt(ctx, tx, domain.FiscalReceipt{
				TenantID: tenantA, PaymentID: p.ID, DeviceType: "mock", ReceiptNumber: uuid.New().String(),
			})
			if err != nil {
				return err
			}
			return pr.Complete(ctx, tx, p.ID, receiptID)
		})
		require.NoError(t, err)
	}

	makePending := func(method domain.PaymentMethod, amount int64) {
		err := sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
			_, err := pr.Create(ctx, tx, domain.Payment{
				TenantID: tenantA, BranchID: branch, IdempotencyKey: uuid.New().String(),
				Method: method, AmountTotal: amount, Currency: "TRY",
			})
			return err
		})
		require.NoError(t, err)
	}

	// In window, cash, completed — must count.
	makeCompleted(domain.PaymentMethodCash, 10000, since.Add(time.Minute))
	// In window, terminal, completed — must NOT count (method filter).
	makeCompleted(domain.PaymentMethodTerminal, 99999, since.Add(time.Minute))
	// In window, cash, pending — must NOT count (status filter).
	makePending(domain.PaymentMethodCash, 77777)
	// Before window, cash, completed — must NOT count (window filter).
	makeCompleted(domain.PaymentMethodCash, 55555, since.Add(-time.Minute))

	var total int64
	err := sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		total, err = r.SumCompletedCashPayments(ctx, tx, tenantA, branch, since, nil)
		return err
	})
	require.NoError(t, err)
	assert.Equal(t, int64(10000), total)
}

// TestCashSessionRepo_SumCompletedCashPayments_UpperBoundExcludesAfterUntil
// proves a closed session's window stops moving: a cash sale completed after
// `until` (the session's closed_at) must not retroactively change the sum.
func TestCashSessionRepo_SumCompletedCashPayments_UpperBoundExcludesAfterUntil(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	r := repo.NewCashSessionRepo()
	pr := repo.NewPaymentRepo()
	branch := uuid.New()
	since := time.Now().UTC().Add(-time.Hour)
	until := since.Add(30 * time.Minute)

	createAt := func(createdAt time.Time, amount int64) {
		err := sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
			p, err := pr.Create(ctx, tx, domain.Payment{
				TenantID: tenantA, BranchID: branch, IdempotencyKey: uuid.New().String(),
				Method: domain.PaymentMethodCash, AmountTotal: amount, Currency: "TRY",
			})
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE payments SET created_at = $2 WHERE id = $1`, p.ID, createdAt); err != nil {
				return err
			}
			receiptID, err := pr.InsertFiscalReceipt(ctx, tx, domain.FiscalReceipt{
				TenantID: tenantA, PaymentID: p.ID, DeviceType: "mock", ReceiptNumber: uuid.New().String(),
			})
			if err != nil {
				return err
			}
			return pr.Complete(ctx, tx, p.ID, receiptID)
		})
		require.NoError(t, err)
	}

	createAt(since.Add(time.Minute), 4000) // inside window
	createAt(until.Add(time.Minute), 9000) // after until — must be excluded

	var total int64
	err := sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		total, err = r.SumCompletedCashPayments(ctx, tx, tenantA, branch, since, &until)
		return err
	})
	require.NoError(t, err)
	assert.Equal(t, int64(4000), total)
}
