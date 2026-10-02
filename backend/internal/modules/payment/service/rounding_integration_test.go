package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/payment/domain"
	pub "onlinemenu.tr/internal/modules/payment/public"
	"onlinemenu.tr/internal/modules/payment/repo"
	"onlinemenu.tr/internal/modules/payment/service"
	pospub "onlinemenu.tr/internal/modules/pos/public"
	"onlinemenu.tr/internal/platform/auth"
)

// stubRoundingPolicy is pos's BranchRoundingPolicyReader as payment sees it.
type stubRoundingPolicy struct {
	mu     sync.Mutex
	policy pospub.RoundingPolicy
	err    error
}

func (s *stubRoundingPolicy) BranchRoundingPolicy(_ context.Context, _, _ uuid.UUID) (pospub.RoundingPolicy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.policy, s.err
}

func (s *stubRoundingPolicy) set(p pospub.RoundingPolicy) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.policy = p
}

var _ pospub.BranchRoundingPolicyReader = (*stubRoundingPolicy)(nil)

// roundingOn is a branch that lets both cash and card round to ₺5, at most
// ₺10 per check — the defaults with both switches on.
func roundingOn() pospub.RoundingPolicy {
	return pospub.RoundingPolicy{CashEnabled: true, CardEnabled: true, StepMinor: 500, MaxPerCheckMinor: 1000}
}

// namedAdapter is the mock under another adapter type, standing in for a
// real ÖKC driver (tokenx) whose discount handling is not confirmed yet.
type namedAdapter struct {
	domain.MockFiscalAdapter
	name string
}

func (a namedAdapter) AdapterType() string { return a.name }

func newRoundingService(total int64, policy pospub.BranchRoundingPolicyReader, adapter domain.FiscalDeviceAdapter) *service.PaymentService {
	return service.NewPaymentService(service.Params{
		DB:             sharedPool,
		PaymentRepo:    repo.NewPaymentRepo(),
		SubmissionRepo: repo.NewFiscalSubmissionRepo(),
		StatusRepo:     repo.NewFiscalStatusRepo(),
		SessionRepo:    repo.NewCashSessionRepo(),
		Checks:         (&stubCheckGuard{}).withTotal(total),
		Rounding:       policy,
		Fiscal:         adapter,
		Logger:         zap.NewNop(),
	})
}

func roundedSale(checkID uuid.UUID, method domain.PaymentMethod, amount, rounding int64) service.RegisterSaleRequest {
	return service.RegisterSaleRequest{
		TenantID:       tenantA,
		BranchID:       branchA,
		CheckID:        &checkID,
		IdempotencyKey: uuid.NewString(),
		Method:         method,
		AmountTotal:    amount,
		RoundingAmount: rounding,
		Currency:       "TRY",
		Lines: []domain.FiscalLine{{
			Name: "Burger", UnitPriceMinor: amount + rounding, QuantityMilli: 1000, Unit: "C62",
		}},
	}
}

// TestRegisterSale_RoundingRules pins kasa-rapor-programi G.2's server rules:
// every violation is refused with ErrRoundingNotAllowed (HTTP 422) and
// nothing is persisted; a valid rounded sale goes through.
func TestRegisterSale_RoundingRules(t *testing.T) {
	requireDB(t)

	cashOnly := roundingOn()
	cashOnly.CardEnabled = false

	type prior struct {
		amount, rounding int64
	}
	tests := []struct {
		name     string
		total    int64
		policy   pospub.RoundingPolicy
		prior    []prior
		method   domain.PaymentMethod
		amount   int64
		rounding int64
		noCheck  bool
		wantErr  error
	}{
		{name: "cash rounds the full remainder down", total: 43750, policy: roundingOn(), method: domain.PaymentMethodCash, amount: 43500, rounding: 250},
		{name: "card rounds when the card switch is on", total: 43750, policy: roundingOn(), method: domain.PaymentMethodTerminal, amount: 43500, rounding: 250},
		{name: "rounding closes the remainder after a partial payment", total: 43750, policy: roundingOn(), prior: []prior{{20000, 0}}, method: domain.PaymentMethodCash, amount: 23500, rounding: 250},

		// rule: method must be enabled at the branch
		{name: "card refused when only cash rounds", total: 43750, policy: cashOnly, method: domain.PaymentMethodTerminal, amount: 43500, rounding: 250, wantErr: pub.ErrRoundingNotAllowed},
		{name: "rounding off for every method", total: 43750, policy: pospub.RoundingPolicy{StepMinor: 500, MaxPerCheckMinor: 1000}, method: domain.PaymentMethodCash, amount: 43500, rounding: 250, wantErr: pub.ErrRoundingNotAllowed},
		{name: "meal card never rounds", total: 43750, policy: roundingOn(), method: domain.PaymentMethodMealCard, amount: 43500, rounding: 250, wantErr: pub.ErrRoundingNotAllowed},
		// rule: rounding < step
		{name: "rounding equal to the step", total: 44000, policy: roundingOn(), method: domain.PaymentMethodCash, amount: 43500, rounding: 500, wantErr: pub.ErrRoundingNotAllowed},
		// rule: amount % step == 0
		{name: "amount not a step multiple", total: 43750, policy: roundingOn(), method: domain.PaymentMethodCash, amount: 43600, rounding: 150, wantErr: pub.ErrRoundingNotAllowed},
		// rule: amount + rounding == remaining
		{name: "rounded payment that leaves a remainder", total: 50000, policy: roundingOn(), method: domain.PaymentMethodCash, amount: 43500, rounding: 250, wantErr: pub.ErrRoundingNotAllowed},
		{name: "rounded payment above the remainder is an overpayment", total: 43500, policy: roundingOn(), method: domain.PaymentMethodCash, amount: 43500, rounding: 250, wantErr: pub.ErrPaymentExceedsDue},
		// rule: Σ rounding per check <= max
		{name: "single rounding above the ceiling", total: 43750, policy: pospub.RoundingPolicy{CashEnabled: true, StepMinor: 500, MaxPerCheckMinor: 200}, method: domain.PaymentMethodCash, amount: 43500, rounding: 250, wantErr: pub.ErrRoundingNotAllowed},
		{name: "earlier rounding counts toward the ceiling", total: 43750, policy: pospub.RoundingPolicy{CashEnabled: true, StepMinor: 50, MaxPerCheckMinor: 45}, prior: []prior{{20000, 40}}, method: domain.PaymentMethodCash, amount: 23700, rounding: 10, wantErr: pub.ErrRoundingNotAllowed},
		// rule: rounding needs a check
		{name: "rounding without a check", total: 43750, policy: roundingOn(), method: domain.PaymentMethodCash, amount: 43500, rounding: 250, noCheck: true, wantErr: pub.ErrRoundingNotAllowed},
		{name: "negative rounding is invalid input", total: 43750, policy: roundingOn(), method: domain.PaymentMethodCash, amount: 43500, rounding: -250, wantErr: pub.ErrInvalidInput},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy := &stubRoundingPolicy{policy: tt.policy}
			svc := newRoundingService(tt.total, policy, domain.MockFiscalAdapter{})
			checkID := uuid.New()

			for _, p := range tt.prior {
				// A prior rounded payment is set up under a policy that
				// allows it; the rule under test applies to the new sale.
				policy.set(pospub.RoundingPolicy{CashEnabled: true, CardEnabled: true, StepMinor: 50, MaxPerCheckMinor: 1000})
				// Only a closing payment may round, so the prior one gets a
				// check total it closes exactly.
				req := roundedSale(checkID, domain.PaymentMethodCash, p.amount, p.rounding)
				_, err := newRoundingService(p.amount+p.rounding, policy, domain.MockFiscalAdapter{}).RegisterSale(context.Background(), req)
				require.NoError(t, err)
				policy.set(tt.policy)
			}

			req := roundedSale(checkID, tt.method, tt.amount, tt.rounding)
			if tt.noCheck {
				req.CheckID = nil
			}
			payment, err := svc.RegisterSale(context.Background(), req)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				assert.Equal(t, uuid.Nil, payment.ID, "a refused sale must not be persisted")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.amount, payment.AmountTotal)
			assert.Equal(t, tt.rounding, payment.RoundingAmount)
		})
	}
}

// TestRegisterSale_RoundingRefusedOnRealDevice: until Token confirms how its
// device spreads a sale discount across VAT rates (G.2), rounding is only
// allowed on the mock adapter, whatever the branch policy says.
func TestRegisterSale_RoundingRefusedOnRealDevice(t *testing.T) {
	requireDB(t)
	svc := newRoundingService(43750, &stubRoundingPolicy{policy: roundingOn()}, namedAdapter{name: "tokenx"})

	payment, err := svc.RegisterSale(context.Background(), roundedSale(uuid.New(), domain.PaymentMethodCash, 43500, 250))
	require.ErrorIs(t, err, pub.ErrRoundingNotAllowed)
	assert.Equal(t, uuid.Nil, payment.ID)
}

// TestRegisterSale_RoundingPolicyReadFailureIsNotARefusal: pos being
// unreachable is a 500, never "rounding not allowed"; a missing reader fails
// closed the same way.
func TestRegisterSale_RoundingPolicyReadFailureIsNotARefusal(t *testing.T) {
	requireDB(t)

	for name, reader := range map[string]pospub.BranchRoundingPolicyReader{
		"read error":       &stubRoundingPolicy{err: errors.New("conn closed")},
		"reader not wired": nil,
	} {
		t.Run(name, func(t *testing.T) {
			svc := newRoundingService(43750, reader, domain.MockFiscalAdapter{})
			_, err := svc.RegisterSale(context.Background(), roundedSale(uuid.New(), domain.PaymentMethodCash, 43500, 250))
			require.Error(t, err)
			assert.NotErrorIs(t, err, pub.ErrRoundingNotAllowed)
		})
	}
}

// TestRegisterSale_RoundedPaymentSettlesTheCheck is the wiring the whole
// design rests on: the paid/pending sums count amount_total +
// rounding_amount, so the check reads as fully paid, the overpayment guard
// refuses even one more kuruş, and the drawer only expects the cash taken.
func TestRegisterSale_RoundedPaymentSettlesTheCheck(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	const total = int64(43750)
	svc := newRoundingService(total, &stubRoundingPolicy{policy: roundingOn()}, domain.MockFiscalAdapter{})
	checkID := uuid.New()

	// Settle what sibling tests left pending first: the drain below would
	// otherwise complete their cash too and pollute the delta.
	drainFiscal(t, svc)
	cashBefore := sumCompletedCash(ctx, t)

	payment, err := svc.RegisterSale(ctx, roundedSale(checkID, domain.PaymentMethodCash, 43500, 250))
	require.NoError(t, err)

	pending, err := svc.PendingTotalForCheck(ctx, tenantA, checkID)
	require.NoError(t, err)
	assert.Equal(t, total, pending, "a pending rounded payment reserves amount + rounding")

	_, err = svc.RegisterSale(ctx, checkSale(checkID, 1))
	require.ErrorIs(t, err, pub.ErrPaymentExceedsDue, "the rounded payment closed the check")

	drainFiscal(t, svc)
	assert.Equal(t, domain.PaymentStatusCompleted, fetchPayment(t, svc, payment.ID).Status,
		"the mock adapter accepts the Yuvarlama-discounted basket")

	paid, err := svc.TotalPaidForCheck(ctx, tenantA, checkID)
	require.NoError(t, err)
	assert.Equal(t, total, paid, "Close's paid-in-full check sees the full check total")

	assert.Equal(t, int64(43500), sumCompletedCash(ctx, t)-cashBefore,
		"the drawer expects the cash taken, not the rounding conceded")

	settlement, err := svc.CheckSettlementFor(ctx, auth.Principal{TenantID: tenantA, BranchID: branchA}, checkID)
	require.NoError(t, err)
	require.Len(t, settlement.Completed, 1)
	assert.Equal(t, int64(43500), settlement.Completed[0].AmountTotal)
	assert.Equal(t, int64(250), settlement.Completed[0].RoundingAmount)
}

// TestRegisterSale_RoundedFiscalSale: the receipt prints the items at full
// price and the concession as a "Yuvarlama" discount, totalling the money
// taken.
func TestRegisterSale_RoundedFiscalSale(t *testing.T) {
	requireDB(t)
	ctx := context.Background()

	tests := []struct {
		name  string
		lines bool
	}{
		{name: "with the POS basket", lines: true},
		{name: "synthesized line", lines: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newRoundingService(43750, &stubRoundingPolicy{policy: roundingOn()}, domain.MockFiscalAdapter{})
			req := roundedSale(uuid.New(), domain.PaymentMethodCash, 43500, 250)
			if !tt.lines {
				req.Lines = nil
			}
			payment, err := svc.RegisterSale(ctx, req)
			require.NoError(t, err)

			sale := storedFiscalSale(ctx, t, payment.ID)
			assert.Equal(t, int64(43500), sale.TotalMinor)
			require.NotNil(t, sale.Discount)
			assert.Equal(t, domain.FiscalAdjust{
				Description: "Yuvarlama", Kind: domain.FiscalAdjustDiscount, Mode: domain.FiscalAdjustAmount, Value: 250,
			}, *sale.Discount)
			require.Len(t, sale.Payments, 1)
			assert.Equal(t, int64(43500), sale.Payments[0].AmountMinor)
			require.NoError(t, sale.ValidateTotal())
		})
	}

	t.Run("an ordinary sale carries no discount", func(t *testing.T) {
		svc := newRoundingService(43750, &stubRoundingPolicy{policy: roundingOn()}, domain.MockFiscalAdapter{})
		payment, err := svc.RegisterSale(ctx, roundedSale(uuid.New(), domain.PaymentMethodCash, 43750, 0))
		require.NoError(t, err)
		sale := storedFiscalSale(ctx, t, payment.ID)
		assert.Nil(t, sale.Discount)
		require.NoError(t, sale.ValidateTotal())
	})
}

// TestRegisterSale_RoundedReplay: a retried rounded payment returns the
// original, including after the branch switched rounding off in between.
func TestRegisterSale_RoundedReplay(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	policy := &stubRoundingPolicy{policy: roundingOn()}
	svc := newRoundingService(43750, policy, domain.MockFiscalAdapter{})
	req := roundedSale(uuid.New(), domain.PaymentMethodCash, 43500, 250)

	first, err := svc.RegisterSale(ctx, req)
	require.NoError(t, err)
	drainFiscal(t, svc)

	policy.set(pospub.RoundingPolicy{StepMinor: 500, MaxPerCheckMinor: 1000})
	second, err := svc.RegisterSale(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, first.ID, second.ID)
	assert.Equal(t, int64(250), second.RoundingAmount)
}

// TestRegisterSale_ConcurrentRoundedSalesStayUnderCeiling: two stations
// rounding the same check at once — only one may concede.
func TestRegisterSale_ConcurrentRoundedSalesStayUnderCeiling(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	svc := newRoundingService(43750, &stubRoundingPolicy{policy: roundingOn()}, domain.MockFiscalAdapter{})
	checkID := uuid.New()

	const n = 4
	errs := make([]error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		go func(i int) {
			defer wg.Done()
			_, errs[i] = svc.RegisterSale(ctx, roundedSale(checkID, domain.PaymentMethodCash, 43500, 250))
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
	assert.Equal(t, 1, succeeded)
}

func sumCompletedCash(ctx context.Context, t *testing.T) int64 {
	t.Helper()
	var total int64
	err := sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		total, err = repo.NewCashSessionRepo().SumCompletedCashPayments(ctx, tx, tenantA, branchA, time.Unix(0, 0), nil)
		return err
	})
	require.NoError(t, err)
	return total
}

func storedFiscalSale(ctx context.Context, t *testing.T, paymentID uuid.UUID) domain.FiscalSale {
	t.Helper()
	var payload []byte
	err := sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT sale_payload FROM fiscal_submissions WHERE payment_id = $1`, paymentID).Scan(&payload)
	})
	require.NoError(t, err)
	var sale domain.FiscalSale
	require.NoError(t, json.Unmarshal(payload, &sale))
	return sale
}
