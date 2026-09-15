package service_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/payment/domain"
	pub "onlinemenu.tr/internal/modules/payment/public"
	"onlinemenu.tr/internal/modules/payment/repo"
	"onlinemenu.tr/internal/modules/payment/service"
	pospub "onlinemenu.tr/internal/modules/pos/public"
)

// stubCheckGuard stands in for pos's CheckReadService. Payment may not read a
// pos table, so the real guard cannot be built from this package — and the
// contract under test is exactly "what payment does with pos's verdict",
// which a stub states more precisely than a seeded check would.
type stubCheckGuard struct {
	mu      sync.Mutex
	verdict error
	calls   int
}

func (g *stubCheckGuard) AssertCheckWritable(_ context.Context, _, _, _ uuid.UUID) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls++
	return g.verdict
}

func (g *stubCheckGuard) set(err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.verdict = err
}

func (g *stubCheckGuard) callCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls
}

var _ pospub.CheckWriteGuard = (*stubCheckGuard)(nil)

// writableCheckGuard is the default for the package's other tests: every
// check they name is open.
var writableCheckGuard = &stubCheckGuard{}

func newPaymentServiceWithGuard(guard pospub.CheckWriteGuard) *service.PaymentService {
	return service.NewPaymentService(service.Params{
		DB:             sharedPool,
		PaymentRepo:    repo.NewPaymentRepo(),
		SubmissionRepo: repo.NewFiscalSubmissionRepo(),
		StatusRepo:     repo.NewFiscalStatusRepo(),
		SessionRepo:    repo.NewCashSessionRepo(),
		Checks:         guard,
		Fiscal:         domain.MockFiscalAdapter{},
		Logger:         zap.NewNop(),
	})
}

// TestRegisterSale_RejectsUnwritableCheck is the payment half of the
// 2026-09-15 production finding: POST /api/v1/payments collected cash against
// a closed adisyon and minted a fiscal receipt for it, answering 201.
func TestRegisterSale_RejectsUnwritableCheck(t *testing.T) {
	requireDB(t)
	ctx := context.Background()

	tests := []struct {
		name    string
		verdict error
		wantErr error
	}{
		{
			name: "open check",
		},
		{
			name:    "closed or cancelled check",
			verdict: pospub.ErrCheckNotOpen,
			wantErr: pub.ErrCheckNotOpen,
		},
		{
			name:    "check of another branch",
			verdict: pospub.ErrCheckBranchMismatch,
			wantErr: pub.ErrCheckBranchMismatch,
		},
		{
			name:    "unknown check id",
			verdict: pospub.ErrNotFound,
			wantErr: pub.ErrCheckNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			guard := &stubCheckGuard{verdict: tt.verdict}
			svc := newPaymentServiceWithGuard(guard)
			checkID := uuid.New()

			payment, err := svc.RegisterSale(ctx, service.RegisterSaleRequest{
				TenantID:       tenantA,
				BranchID:       branchA,
				CheckID:        &checkID,
				IdempotencyKey: uuid.NewString(),
				Method:         domain.PaymentMethodCash,
				AmountTotal:    2500,
				Currency:       "TRY",
			})
			assert.Equal(t, 1, guard.callCount(), "the check must be consulted exactly once")
			if tt.wantErr == nil {
				require.NoError(t, err)
				assert.NotEqual(t, uuid.Nil, payment.ID)
				return
			}
			assert.ErrorIs(t, err, tt.wantErr)
			assert.Equal(t, uuid.Nil, payment.ID, "a refused sale must not be persisted")
		})
	}
}

// TestRegisterSale_NoCheckID_SkipsGuard pins that masasız satış (paket
// servis, tezgah üstü) is untouched: there is no adisyon to validate, and
// consulting pos for one would be a pointless round trip that could only
// fail.
func TestRegisterSale_NoCheckID_SkipsGuard(t *testing.T) {
	requireDB(t)
	ctx := context.Background()

	guard := &stubCheckGuard{verdict: pospub.ErrCheckNotOpen}
	svc := newPaymentServiceWithGuard(guard)

	payment, err := svc.RegisterSale(ctx, service.RegisterSaleRequest{
		TenantID:       tenantA,
		BranchID:       branchA,
		IdempotencyKey: uuid.NewString(),
		Method:         domain.PaymentMethodCash,
		AmountTotal:    1500,
		Currency:       "TRY",
	})
	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, payment.ID)
	assert.Equal(t, 0, guard.callCount())
}

// TestRegisterSale_ClosedCheckStillReplaysIdempotently is the rule the guard
// had to be placed carefully to keep: a payment legitimately taken while the
// adisyon was open must keep replaying its original result afterwards. A POS
// station that loses the response and retries after the check closed would
// otherwise be told "adisyon kapalı" for money it already collected — and
// would plausibly collect it again.
func TestRegisterSale_ClosedCheckStillReplaysIdempotently(t *testing.T) {
	requireDB(t)
	ctx := context.Background()

	guard := &stubCheckGuard{}
	svc := newPaymentServiceWithGuard(guard)
	checkID := uuid.New()
	req := service.RegisterSaleRequest{
		TenantID:       tenantA,
		BranchID:       branchA,
		CheckID:        &checkID,
		IdempotencyKey: uuid.NewString(),
		Method:         domain.PaymentMethodCash,
		AmountTotal:    4000,
		Currency:       "TRY",
	}

	first, err := svc.RegisterSale(ctx, req)
	require.NoError(t, err)

	guard.set(pospub.ErrCheckNotOpen)

	second, err := svc.RegisterSale(ctx, req)
	require.NoError(t, err, "a replay of an accepted payment must not start conflicting")
	assert.Equal(t, first.ID, second.ID)
}

// TestRegisterSale_GuardFailureIsNotAConflict separates "pos refused" from
// "pos could not be asked". Collapsing the two would tell a cashier the
// adisyon is closed every time the connection pool is exhausted.
func TestRegisterSale_GuardFailureIsNotAConflict(t *testing.T) {
	requireDB(t)
	ctx := context.Background()

	guard := &stubCheckGuard{verdict: errors.New("conn closed")}
	svc := newPaymentServiceWithGuard(guard)
	checkID := uuid.New()

	_, err := svc.RegisterSale(ctx, service.RegisterSaleRequest{
		TenantID:       tenantA,
		BranchID:       branchA,
		CheckID:        &checkID,
		IdempotencyKey: uuid.NewString(),
		Method:         domain.PaymentMethodCash,
		AmountTotal:    1000,
		Currency:       "TRY",
	})
	require.Error(t, err)
	assert.NotErrorIs(t, err, pub.ErrCheckNotOpen)
	assert.NotErrorIs(t, err, pub.ErrCheckBranchMismatch)
	assert.NotErrorIs(t, err, pub.ErrCheckNotFound)
}
