package repo_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/modules/payment/domain"
	"onlinemenu.tr/internal/modules/payment/repo"
)

// TestPaymentRepo_TotalsByMethod exercises the two exclusion rules
// separately so a broken filter can't hide behind the other one:
//   - the pending cash payment is INSIDE the window but excluded by the
//     status filter (only 'completed'/'voided' count),
//   - the boundary cash payment sits exactly at `to` and is excluded by the
//     half-open window ([from, to)), even though its status would count.
func TestPaymentRepo_TotalsByMethod(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	r := repo.NewPaymentRepo()
	branch := uuid.New()

	from := time.Date(2026, 1, 10, 8, 0, 0, 0, time.UTC)
	to := time.Date(2026, 1, 10, 20, 0, 0, 0, time.UTC)

	createCompleted := func(tx pgx.Tx, method domain.PaymentMethod, amount int64, createdAt time.Time) error {
		p, err := r.Create(ctx, tx, domain.Payment{
			TenantID:       tenantA,
			BranchID:       branch,
			IdempotencyKey: uuid.New().String(),
			Method:         method,
			AmountTotal:    amount,
			Currency:       "TRY",
		})
		if err != nil {
			return err
		}
		receiptID, err := r.InsertFiscalReceipt(ctx, tx, domain.FiscalReceipt{
			TenantID:      tenantA,
			PaymentID:     p.ID,
			DeviceType:    "mock",
			ReceiptNumber: uuid.New().String(),
			ReceiptData:   map[string]any{},
		})
		if err != nil {
			return err
		}
		if err := r.Complete(ctx, tx, p.ID, receiptID); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE payments SET created_at=$1 WHERE id=$2`, createdAt, p.ID)
		return err
	}

	createVoided := func(tx pgx.Tx, method domain.PaymentMethod, amount int64, createdAt time.Time) error {
		p, err := r.Create(ctx, tx, domain.Payment{
			TenantID:       tenantA,
			BranchID:       branch,
			IdempotencyKey: uuid.New().String(),
			Method:         method,
			AmountTotal:    amount,
			Currency:       "TRY",
		})
		if err != nil {
			return err
		}
		if err := r.Void(ctx, tx, p.ID); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE payments SET created_at=$1 WHERE id=$2`, createdAt, p.ID)
		return err
	}

	createPending := func(tx pgx.Tx, method domain.PaymentMethod, amount int64, createdAt time.Time) error {
		p, err := r.Create(ctx, tx, domain.Payment{
			TenantID:       tenantA,
			BranchID:       branch,
			IdempotencyKey: uuid.New().String(),
			Method:         method,
			AmountTotal:    amount,
			Currency:       "TRY",
		})
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE payments SET created_at=$1 WHERE id=$2`, createdAt, p.ID)
		return err
	}

	err := sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		if err := createCompleted(tx, domain.PaymentMethodCash, 1000, from.Add(1*time.Hour)); err != nil {
			return err
		}
		if err := createCompleted(tx, domain.PaymentMethodCash, 2500, from.Add(2*time.Hour)); err != nil {
			return err
		}
		if err := createCompleted(tx, domain.PaymentMethodTerminal, 4000, from.Add(3*time.Hour)); err != nil {
			return err
		}
		if err := createVoided(tx, domain.PaymentMethodCash, 700, from.Add(4*time.Hour)); err != nil {
			return err
		}
		// Inside the window, but pending: must be excluded by the status filter.
		if err := createPending(tx, domain.PaymentMethodCash, 999, from.Add(5*time.Hour)); err != nil {
			return err
		}
		// Completed and would otherwise count, but created_at == to: must be
		// excluded by the half-open window upper bound.
		if err := createCompleted(tx, domain.PaymentMethodCash, 1234, to); err != nil {
			return err
		}
		return nil
	})
	require.NoError(t, err)

	var totals []domain.MethodTotal
	err = sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		totals, err = r.TotalsByMethod(ctx, tx, tenantA, branch, from, to)
		return err
	})
	require.NoError(t, err)

	require.Equal(t, []domain.MethodTotal{
		{Method: "cash", Status: "completed", Count: 2, Total: 3500},
		{Method: "cash", Status: "voided", Count: 1, Total: 700},
		{Method: "terminal", Status: "completed", Count: 1, Total: 4000},
	}, totals)
}

// TestCashSessionRepo_ListByBranchWindow verifies the overlap predicate
// (opened_at < to AND (closed_at IS NULL OR closed_at >= from)) and the
// oldest-first ordering, with one session on each side of the boundary:
// A closed inside the window, B still open, C closed before the window.
func TestCashSessionRepo_ListByBranchWindow(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	r := repo.NewCashSessionRepo()
	branch := uuid.New()

	from := time.Date(2026, 1, 10, 8, 0, 0, 0, time.UTC)
	to := time.Date(2026, 1, 10, 20, 0, 0, 0, time.UTC)

	openAndClose := func(tx pgx.Tx, openedAt, closedAt time.Time) (uuid.UUID, error) {
		session, err := r.Open(ctx, tx, domain.CashSession{
			TenantID:             tenantA,
			BranchID:             branch,
			OpeningCountedAmount: 1000,
			OpenedBy:             uuid.New(),
		})
		if err != nil {
			return uuid.Nil, err
		}
		current, err := r.GetByIDForUpdate(ctx, tx, tenantA, session.ID)
		if err != nil {
			return uuid.Nil, err
		}
		session, err = r.SubmitClosingCount(ctx, tx, tenantA, session.ID, current.Status, 1000, nil, "")
		if err != nil {
			return uuid.Nil, err
		}
		if _, err := r.Close(ctx, tx, tenantA, session.ID, session.Status, uuid.New()); err != nil {
			return uuid.Nil, err
		}
		_, err = tx.Exec(ctx, `UPDATE cash_sessions SET opened_at=$1, closed_at=$2 WHERE id=$3`, openedAt, closedAt, session.ID)
		return session.ID, err
	}

	var idA, idB, idC uuid.UUID
	err := sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		// A: opened before the window, closed inside it.
		idA, err = openAndClose(tx, from.Add(-22*time.Hour), from.Add(1*time.Hour))
		if err != nil {
			return err
		}
		// C: opened and closed before the window entirely — must be excluded.
		idC, err = openAndClose(tx, from.Add(-26*time.Hour), from.Add(-1*time.Hour))
		if err != nil {
			return err
		}
		// B: opened inside the window, still open at read time.
		session, err := r.Open(ctx, tx, domain.CashSession{
			TenantID:             tenantA,
			BranchID:             branch,
			OpeningCountedAmount: 500,
			OpenedBy:             uuid.New(),
		})
		if err != nil {
			return err
		}
		idB = session.ID
		_, err = tx.Exec(ctx, `UPDATE cash_sessions SET opened_at=$1 WHERE id=$2`, from.Add(4*time.Hour), session.ID)
		return err
	})
	require.NoError(t, err)

	var sessions []domain.CashSession
	err = sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		sessions, err = r.ListByBranchWindow(ctx, tx, tenantA, branch, from, to)
		return err
	})
	require.NoError(t, err)

	require.Len(t, sessions, 2)
	assert.Equal(t, idA, sessions[0].ID, "A (oldest opened_at) must sort first")
	assert.Equal(t, idB, sessions[1].ID)
	for _, s := range sessions {
		assert.NotEqual(t, idC, s.ID, "C closed entirely before the window must be excluded")
	}
}
