package service_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/modules/payment/domain"
	pub "onlinemenu.tr/internal/modules/payment/public"
	"onlinemenu.tr/internal/modules/payment/service"
)

func checkSale(checkID uuid.UUID, amount int64) service.RegisterSaleRequest {
	return service.RegisterSaleRequest{
		TenantID:       tenantA,
		BranchID:       branchA,
		CheckID:        &checkID,
		IdempotencyKey: uuid.NewString(),
		Method:         domain.PaymentMethodTerminal,
		AmountTotal:    amount,
		Currency:       "TRY",
	}
}

// TestRegisterSale_OverpaymentGuard pins the "never collect more than the
// check owes" rule, including the split-bill (alman usulü) path where every
// share up to and including the exact remainder must still be accepted.
func TestRegisterSale_OverpaymentGuard(t *testing.T) {
	requireDB(t)

	type prior struct {
		amount    int64
		completed bool
	}
	tests := []struct {
		name    string
		total   int64
		prior   []prior
		amount  int64
		wantErr error
	}{
		{name: "full payment", total: 10000, amount: 10000},
		{name: "partial payment", total: 10000, amount: 4000},
		{name: "equal to remainder after partial", total: 10000, prior: []prior{{6000, true}}, amount: 4000},
		{name: "split shares fill the check", total: 10000, prior: []prior{{3334, true}, {3333, true}}, amount: 3333},
		{name: "exceeds remainder", total: 10000, prior: []prior{{6000, true}}, amount: 4001, wantErr: pub.ErrPaymentExceedsDue},
		{name: "check already fully paid", total: 10000, prior: []prior{{10000, true}}, amount: 1, wantErr: pub.ErrPaymentExceedsDue},
		{name: "exceeds total on first payment", total: 10000, amount: 10001, wantErr: pub.ErrPaymentExceedsDue},
		{name: "fiscal-pending payment counts as collected", total: 10000, prior: []prior{{6000, false}}, amount: 4001, wantErr: pub.ErrPaymentExceedsDue},
		{name: "fiscal-pending payment leaves exact remainder payable", total: 10000, prior: []prior{{6000, false}}, amount: 4000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newPaymentServiceWithGuard((&stubCheckGuard{}).withTotal(tt.total))
			checkID := uuid.New()

			for _, p := range tt.prior {
				_, err := svc.RegisterSale(context.Background(), checkSale(checkID, p.amount))
				require.NoError(t, err)
				if p.completed {
					drainFiscal(t, svc)
				}
			}

			payment, err := svc.RegisterSale(context.Background(), checkSale(checkID, tt.amount))
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Equal(t, uuid.Nil, payment.ID, "a refused sale must not be persisted")
				return
			}
			require.NoError(t, err)
			assert.NotEqual(t, uuid.Nil, payment.ID)
		})
	}
}

// TestRegisterSale_ConcurrentStationsChargeOnce is the production scenario:
// POS and the web till settle the same adisyon at the same moment, each for
// the full amount. Exactly one may succeed.
func TestRegisterSale_ConcurrentStationsChargeOnce(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	svc := newPaymentServiceWithGuard((&stubCheckGuard{}).withTotal(10000))
	checkID := uuid.New()

	const n = 6
	errs := make([]error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		go func(i int) {
			defer wg.Done()
			_, errs[i] = svc.RegisterSale(ctx, checkSale(checkID, 10000))
		}(i)
	}
	wg.Wait()

	succeeded := 0
	for i, err := range errs {
		if err == nil {
			succeeded++
			continue
		}
		require.ErrorIs(t, err, pub.ErrPaymentExceedsDue, "call %d failed for another reason", i)
	}
	assert.Equal(t, 1, succeeded, "exactly one station may collect the check")

	pending, err := svc.PendingTotalForCheck(ctx, tenantA, checkID)
	require.NoError(t, err)
	assert.Equal(t, int64(10000), pending)
}

// TestRegisterSale_ReplayAfterFullPaymentIsNotAConflict keeps the idempotency
// contract intact under the new guard: once a check is fully paid, a retry of
// the payment that paid it must return that payment, not 409.
func TestRegisterSale_ReplayAfterFullPaymentIsNotAConflict(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	svc := newPaymentServiceWithGuard((&stubCheckGuard{}).withTotal(10000))
	req := checkSale(uuid.New(), 10000)

	first, err := svc.RegisterSale(ctx, req)
	require.NoError(t, err)
	drainFiscal(t, svc)

	second, err := svc.RegisterSale(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, first.ID, second.ID)
}

// TestRegisterSale_ConcurrentSameKeyOnCheck covers the replay discovered under
// the lock: a same-key retry that waited on the winner must return the
// winner's payment rather than count it against the total and answer 409.
func TestRegisterSale_ConcurrentSameKeyOnCheck(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	svc := newPaymentServiceWithGuard((&stubCheckGuard{}).withTotal(10000))
	req := checkSale(uuid.New(), 10000)

	const n = 6
	results := make([]domain.Payment, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = svc.RegisterSale(ctx, req)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		require.NoError(t, err, "call %d", i)
		assert.Equal(t, results[0].ID, results[i].ID)
	}

	var count int
	err := sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT COUNT(*) FROM payments WHERE idempotency_key = $1`, req.IdempotencyKey).Scan(&count)
	})
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

// TestRegisterSale_CheckTotalFailureIsNotAConflict: pos being unreachable is a
// 500, never "tutar kalan borcu aşıyor".
func TestRegisterSale_CheckTotalFailureIsNotAConflict(t *testing.T) {
	requireDB(t)
	svc := newPaymentServiceWithGuard(&stubCheckGuard{totalErr: errors.New("conn closed")})

	payment, err := svc.RegisterSale(context.Background(), checkSale(uuid.New(), 1000))
	require.Error(t, err)
	assert.NotErrorIs(t, err, pub.ErrPaymentExceedsDue)
	assert.Equal(t, uuid.Nil, payment.ID)
}
