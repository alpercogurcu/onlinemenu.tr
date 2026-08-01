package service_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/payment/domain"
	pub "onlinemenu.tr/internal/modules/payment/public"
	"onlinemenu.tr/internal/modules/payment/repo"
	"onlinemenu.tr/internal/modules/payment/service"
	"onlinemenu.tr/internal/platform/auth"
)

// shiftManagerRoleID is the system role that holds shifts:create/update in the
// seed migration (identity/000006_seed_system_roles.up.sql) — the only role
// authz.rego grants any payment.cash_session.* write action to.
const shiftManagerRoleID = "00000001-0000-0000-0000-000000000002"

func newCashSessionService() *service.CashSessionService {
	return service.NewCashSessionService(service.CashSessionParams{
		DB:         sharedPool,
		Sessions:   repo.NewCashSessionRepo(),
		FiscalRepo: repo.NewFiscalStatusRepo(),
		Logger:     zap.NewNop(),
	})
}

// cashSessionScopedCtx mirrors scopedCtx (branch_authz_test.go) but is
// parametrised by action, since cash session endpoints span several distinct
// permission strings (read vs. the write actions).
func cashSessionScopedCtx(t *testing.T, principal auth.Principal, action string) (context.Context, bool) {
	t.Helper()
	engine, err := auth.NewEngine(
		auth.EngineConfig{BundlePath: "../../../../configs/opa/bundles"},
		redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 1}),
		zap.NewNop(),
	)
	require.NoError(t, err)

	var (
		captured context.Context
		reached  bool
	)
	handler := auth.RequirePermission(engine, action)(
		http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			captured, reached = r.Context(), true
		}),
	)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/cash-sessions", nil)
	req = req.WithContext(auth.WithPrincipal(req.Context(), principal))
	handler.ServeHTTP(httptest.NewRecorder(), req)
	return captured, reached
}

func shiftManagerPrincipal(branchID uuid.UUID) auth.Principal {
	return staffPrincipal(branchID, shiftManagerRoleID)
}

func cashierPrincipalFor(branchID uuid.UUID) auth.Principal {
	return staffPrincipal(branchID, cashierRoleID)
}

// ---------------------------------------------------------------------------
// Authorization: seed dictionary grants cashier read-only, shift_manager
// read+write (see permission_wiring_test.go's {shifts,*} registry entries and
// authz.rego's cash_session_write_actions block).
// ---------------------------------------------------------------------------

func TestCashSessionOPA_CashierReadOnly_ShiftManagerReadWrite(t *testing.T) {
	branch := uuid.New()
	writeActions := []string{
		"payment.cash_session.open",
		"payment.cash_session.movement",
		"payment.cash_session.submit_closing",
		"payment.cash_session.close",
	}

	for _, action := range writeActions {
		t.Run("cashier denied "+action, func(t *testing.T) {
			_, reached := cashSessionScopedCtx(t, cashierPrincipalFor(branch), action)
			assert.False(t, reached, "cashier must not hold %s — the seed grants shifts:create/update to shift_manager only", action)
		})
		t.Run("shift_manager allowed "+action, func(t *testing.T) {
			_, reached := cashSessionScopedCtx(t, shiftManagerPrincipal(branch), action)
			assert.True(t, reached, "shift_manager must hold %s per the seeded shifts:create/update rows", action)
		})
	}

	_, reached := cashSessionScopedCtx(t, cashierPrincipalFor(branch), "payment.cash_session.read")
	assert.True(t, reached, "cashier holds shifts:read per the seed")
	_, reached = cashSessionScopedCtx(t, shiftManagerPrincipal(branch), "payment.cash_session.read")
	assert.True(t, reached, "shift_manager also holds shifts:read per the seed")
}

// ---------------------------------------------------------------------------
// Open / one-open-per-branch / branch scoping
// ---------------------------------------------------------------------------

func TestCashSessionService_Open_RejectsSecondOpenForBranch(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	svc := newCashSessionService()
	branch := uuid.New()
	manager := shiftManagerPrincipal(branch)

	_, err := svc.Open(ctx, manager, service.OpenCashSessionRequest{BranchID: branch, OpeningCountedAmount: 10000})
	require.NoError(t, err)

	_, err = svc.Open(ctx, manager, service.OpenCashSessionRequest{BranchID: branch, OpeningCountedAmount: 5000})
	require.ErrorIs(t, err, pub.ErrCashSessionAlreadyOpen)
}

func TestCashSessionService_Open_RefusesForeignBranch(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	svc := newCashSessionService()
	ownBranch := uuid.New()
	otherBranch := uuid.New()
	manager := shiftManagerPrincipal(ownBranch)

	_, err := svc.Open(ctx, manager, service.OpenCashSessionRequest{BranchID: otherBranch, OpeningCountedAmount: 1000})
	require.ErrorIs(t, err, pub.ErrBranchForbidden)
}

// ---------------------------------------------------------------------------
// Close guard: fiscal-pending blocks close (the project-specific rule)
// ---------------------------------------------------------------------------

// TestCashSessionService_Close_BlockedByPendingFiscalSubmission is the core
// ADR-DATA-008 rule under test: a branch with an in-flight fiscal registration
// must not be able to close its cash session, because money state is unknown
// until the ÖKC resolves. Draining the submission worker (settling the sale)
// must then unblock the close.
func TestCashSessionService_Close_BlockedByPendingFiscalSubmission(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	svc := newCashSessionService()
	paymentSvc := newPaymentService()
	branch := uuid.New()
	manager := shiftManagerPrincipal(branch)

	opened, err := svc.Open(ctx, manager, service.OpenCashSessionRequest{BranchID: branch, OpeningCountedAmount: 0})
	require.NoError(t, err)
	session := opened.Session

	// RegisterSale enqueues a fiscal submission but does not settle it
	// (ADR-FISCAL-002): this branch now has a pending submission.
	_, err = paymentSvc.RegisterSale(ctx, service.RegisterSaleRequest{
		TenantID: manager.TenantID, BranchID: branch, IdempotencyKey: uuid.New().String(),
		Method: domain.PaymentMethodCash, AmountTotal: 5000, Currency: "TRY",
	})
	require.NoError(t, err)

	_, err = svc.SubmitClosingCount(ctx, manager, session.ID, service.SubmitClosingCountRequest{ClosingCountedAmount: 0})
	require.NoError(t, err)

	_, err = svc.Close(ctx, manager, session.ID)
	var cannotClose *service.CashSessionCannotCloseError
	require.ErrorAs(t, err, &cannotClose, "close must be blocked while the branch has a pending fiscal submission")
	require.Len(t, cannotClose.Reasons, 1)

	// Settle the sale; the guard must now pass.
	drainFiscal(t, paymentSvc)

	closed, err := svc.Close(ctx, manager, session.ID)
	require.NoError(t, err, "close must succeed once the branch has zero pending fiscal submissions")
	assert.Equal(t, domain.CashSessionClosed, closed.Session.Status)
}

// ---------------------------------------------------------------------------
// Rejected transitions
// ---------------------------------------------------------------------------

func TestCashSessionService_Close_RejectedWithoutClosingCount(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	svc := newCashSessionService()
	branch := uuid.New()
	manager := shiftManagerPrincipal(branch)

	opened, err := svc.Open(ctx, manager, service.OpenCashSessionRequest{BranchID: branch, OpeningCountedAmount: 0})
	require.NoError(t, err)
	session := opened.Session

	_, err = svc.Close(ctx, manager, session.ID)
	assert.ErrorIs(t, err, domain.ErrInvalidCashSessionTransition)
}

func TestCashSessionService_RecordMovement_RejectedAfterClosingCountSubmitted(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	svc := newCashSessionService()
	branch := uuid.New()
	manager := shiftManagerPrincipal(branch)

	opened, err := svc.Open(ctx, manager, service.OpenCashSessionRequest{BranchID: branch, OpeningCountedAmount: 0})
	require.NoError(t, err)
	session := opened.Session

	_, err = svc.SubmitClosingCount(ctx, manager, session.ID, service.SubmitClosingCountRequest{ClosingCountedAmount: 0})
	require.NoError(t, err)

	_, err = svc.RecordMovement(ctx, manager, session.ID, service.RecordMovementRequest{
		Direction: domain.CashMovementIn, AmountMinor: 1000, Reason: "gec kalan hareket",
	})
	assert.ErrorIs(t, err, domain.ErrInvalidCashSessionTransition,
		"a movement submitted after the closing count must not silently invalidate the count already recorded")
}

func TestCashSessionService_RecordMovement_RejectsInvalidInput(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	svc := newCashSessionService()
	branch := uuid.New()
	manager := shiftManagerPrincipal(branch)

	opened, err := svc.Open(ctx, manager, service.OpenCashSessionRequest{BranchID: branch, OpeningCountedAmount: 0})
	require.NoError(t, err)
	session := opened.Session

	_, err = svc.RecordMovement(ctx, manager, session.ID, service.RecordMovementRequest{
		Direction: domain.CashMovementDirection("sideways"), AmountMinor: 1000, Reason: "x",
	})
	assert.Error(t, err)

	_, err = svc.RecordMovement(ctx, manager, session.ID, service.RecordMovementRequest{
		Direction: domain.CashMovementIn, AmountMinor: 0, Reason: "x",
	})
	assert.Error(t, err)

	_, err = svc.RecordMovement(ctx, manager, session.ID, service.RecordMovementRequest{
		Direction: domain.CashMovementIn, AmountMinor: 1000, Reason: "",
	})
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// Difference arithmetic (açık / fazla / exact)
// ---------------------------------------------------------------------------

// TestCashSessionService_GetActive_DifferenceArithmetic is the core reconciliation
// formula end to end: expected = opening + completed cash payments + net
// movements; difference = counted - expected. It exercises all three cases —
// short (açık), over (fazla), exact — by only changing the counted amount, so
// the same expected figure is reused across all three assertions.
func TestCashSessionService_GetActive_DifferenceArithmetic(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	svc := newCashSessionService()
	paymentSvc := newPaymentService()
	branch := uuid.New()
	manager := shiftManagerPrincipal(branch)

	opened, err := svc.Open(ctx, manager, service.OpenCashSessionRequest{BranchID: branch, OpeningCountedAmount: 10000})
	require.NoError(t, err)
	session := opened.Session

	// One cash sale (must count) and one terminal sale (must not).
	_, err = paymentSvc.RegisterSale(ctx, service.RegisterSaleRequest{
		TenantID: manager.TenantID, BranchID: branch, IdempotencyKey: uuid.New().String(),
		Method: domain.PaymentMethodCash, AmountTotal: 5000, Currency: "TRY",
	})
	require.NoError(t, err)
	_, err = paymentSvc.RegisterSale(ctx, service.RegisterSaleRequest{
		TenantID: manager.TenantID, BranchID: branch, IdempotencyKey: uuid.New().String(),
		Method: domain.PaymentMethodTerminal, AmountTotal: 99999, Currency: "TRY",
	})
	require.NoError(t, err)
	drainFiscal(t, paymentSvc)

	// Movements: +2000 in, -500 out => net +1500.
	_, err = svc.RecordMovement(ctx, manager, session.ID, service.RecordMovementRequest{
		Direction: domain.CashMovementIn, AmountMinor: 2000, Reason: "bozuk para",
	})
	require.NoError(t, err)
	_, err = svc.RecordMovement(ctx, manager, session.ID, service.RecordMovementRequest{
		Direction: domain.CashMovementOut, AmountMinor: 500, Reason: "kasadan alma",
	})
	require.NoError(t, err)

	// expected = 10000 (opening) + 5000 (cash sale) + 1500 (movements net) = 16500.
	const expected = int64(16500)

	view, err := svc.GetActive(ctx, manager, branch)
	require.NoError(t, err)
	assert.Equal(t, expected, view.ExpectedClose)
	assert.Nil(t, view.Difference, "no closing count submitted yet — nothing to compare")

	// -- açık (short): counted below expected -----------------------------------
	short, err := svc.SubmitClosingCount(ctx, manager, session.ID, service.SubmitClosingCountRequest{
		ClosingCountedAmount: expected - 300,
	})
	require.NoError(t, err)
	require.NotNil(t, short.Session.ClosingCountedAmount)

	view, err = svc.GetActive(ctx, manager, branch)
	require.NoError(t, err)
	require.NotNil(t, view.Difference)
	assert.Equal(t, int64(-300), *view.Difference, "açık: counted below expected must be negative")

	// -- fazla (over): recount above expected ------------------------------------
	_, err = svc.SubmitClosingCount(ctx, manager, session.ID, service.SubmitClosingCountRequest{
		ClosingCountedAmount: expected + 700,
	})
	require.NoError(t, err)

	view, err = svc.GetActive(ctx, manager, branch)
	require.NoError(t, err)
	require.NotNil(t, view.Difference)
	assert.Equal(t, int64(700), *view.Difference, "fazla: counted above expected must be positive")

	// -- exact ------------------------------------------------------------------
	_, err = svc.SubmitClosingCount(ctx, manager, session.ID, service.SubmitClosingCountRequest{
		ClosingCountedAmount: expected,
	})
	require.NoError(t, err)

	view, err = svc.GetActive(ctx, manager, branch)
	require.NoError(t, err)
	require.NotNil(t, view.Difference)
	assert.Equal(t, int64(0), *view.Difference, "exact count must report zero difference")
}

// TestCashSessionService_SubmitClosingCount_RejectsDenominationMismatch pins
// the denomination-sum invariant at the service layer (domain.ValidateDenominations).
func TestCashSessionService_SubmitClosingCount_RejectsDenominationMismatch(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	svc := newCashSessionService()
	branch := uuid.New()
	manager := shiftManagerPrincipal(branch)

	opened, err := svc.Open(ctx, manager, service.OpenCashSessionRequest{BranchID: branch, OpeningCountedAmount: 0})
	require.NoError(t, err)
	session := opened.Session

	_, err = svc.SubmitClosingCount(ctx, manager, session.ID, service.SubmitClosingCountRequest{
		ClosingCountedAmount: 100000,
		Denominations:        []domain.DenominationCount{{DenominationMinor: 20000, Count: 3}}, // sums to 60000
	})
	assert.ErrorIs(t, err, domain.ErrDenominationSumMismatch)
}
