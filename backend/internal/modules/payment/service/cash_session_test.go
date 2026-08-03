package service_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
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

// System role ids from identity/000006_seed_system_roles.up.sql. cashier and
// shift_manager both hold the full cash session lifecycle (shifts:create and
// shifts:update were widened to cashier in identity/000014); kitchen holds no
// shifts permission at all and serves as the negative control.
const (
	shiftManagerRoleID = "00000001-0000-0000-0000-000000000002"
	kitchenRoleID      = "00000001-0000-0000-0000-000000000004"
)

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
// Authorization: cashier and shift_manager both hold the full cash session
// lifecycle. identity/000014 widened shifts:create+update to the cashier —
// the person who actually counts the drawer — because requiring a
// shift_manager for every open/close makes the feature unusable in the
// single-till restaurant ADR-DATA-008 targets.
//
// Manager approval of a NON-ZERO difference is a separate control and is not
// asserted here; it does not exist yet (see docs/backlog-pilot.md).
// ---------------------------------------------------------------------------

func TestCashSessionOPA_CashierAndShiftManagerHoldFullLifecycle(t *testing.T) {
	branch := uuid.New()
	actions := []string{
		"payment.cash_session.read",
		"payment.cash_session.open",
		"payment.cash_session.movement",
		"payment.cash_session.submit_closing",
		"payment.cash_session.close",
	}

	for _, action := range actions {
		t.Run("cashier allowed "+action, func(t *testing.T) {
			_, reached := cashSessionScopedCtx(t, cashierPrincipalFor(branch), action)
			assert.True(t, reached, "cashier must hold %s — they count their own drawer (identity/000014)", action)
		})
		t.Run("shift_manager allowed "+action, func(t *testing.T) {
			_, reached := cashSessionScopedCtx(t, shiftManagerPrincipal(branch), action)
			assert.True(t, reached, "shift_manager must hold %s per the seeded shifts rows", action)
		})
	}
}

// TestCashSessionOPA_KitchenDeniedEntirely is the negative control: widening
// the write actions to the cashier must not have opened them to every
// branch-scoped role.
func TestCashSessionOPA_KitchenDeniedEntirely(t *testing.T) {
	branch := uuid.New()
	for _, action := range []string{"payment.cash_session.read", "payment.cash_session.open", "payment.cash_session.close"} {
		t.Run("kitchen denied "+action, func(t *testing.T) {
			_, reached := cashSessionScopedCtx(t, staffPrincipal(branch, kitchenRoleID), action)
			assert.False(t, reached, "kitchen holds no shifts permission in the seed")
		})
	}
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

// TestCashSessionService_RejectsInvalidInput pins every caller-input rejection
// to pub.ErrInvalidInput, not merely to "some error".
//
// The sentinel IS the contract: the HTTP layer switches on it to answer 422.
// Without it these fall through to the handlers' generic `err != nil` arm and
// become 500 "internal server error" — telling the cashier the server broke
// when they typed a bad amount, and filling the error log with false alarms
// that mask real faults. An earlier version of this test asserted only
// assert.Error, which is exactly why that gap survived review.
func TestCashSessionService_RejectsInvalidInput(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	svc := newCashSessionService()
	branch := uuid.New()
	manager := shiftManagerPrincipal(branch)

	opened, err := svc.Open(ctx, manager, service.OpenCashSessionRequest{BranchID: branch, OpeningCountedAmount: 0})
	require.NoError(t, err)
	session := opened.Session

	movements := map[string]service.RecordMovementRequest{
		"unknown direction": {Direction: domain.CashMovementDirection("sideways"), AmountMinor: 1000, Reason: "x"},
		"zero amount":       {Direction: domain.CashMovementIn, AmountMinor: 0, Reason: "x"},
		"negative amount":   {Direction: domain.CashMovementIn, AmountMinor: -1, Reason: "x"},
		"blank reason":      {Direction: domain.CashMovementIn, AmountMinor: 1000, Reason: "   "},
	}
	for name, req := range movements {
		t.Run("movement: "+name, func(t *testing.T) {
			_, err := svc.RecordMovement(ctx, manager, session.ID, req)
			assert.ErrorIs(t, err, pub.ErrInvalidInput)
		})
	}

	// The principal's own branch, deliberately: requireBranch runs before the
	// amount check (authorization precedes validation — fail-closed), so a
	// foreign branch id would surface ErrBranchForbidden and never reach the
	// rule under test.
	t.Run("open: negative opening count", func(t *testing.T) {
		_, err := svc.Open(ctx, manager, service.OpenCashSessionRequest{BranchID: branch, OpeningCountedAmount: -1})
		assert.ErrorIs(t, err, pub.ErrInvalidInput)
	})

	t.Run("open: missing branch", func(t *testing.T) {
		_, err := svc.Open(ctx, manager, service.OpenCashSessionRequest{OpeningCountedAmount: 0})
		assert.ErrorIs(t, err, pub.ErrInvalidInput)
	})

	t.Run("closing count: negative", func(t *testing.T) {
		_, err := svc.SubmitClosingCount(ctx, manager, session.ID, service.SubmitClosingCountRequest{
			ClosingCountedAmount: -1,
		})
		assert.ErrorIs(t, err, pub.ErrInvalidInput)
	})
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
// ---------------------------------------------------------------------------
// RegisterSale cash-method guard: a branch with no open cash session must
// refuse cash payments (ADR-DATA-008 gap fix). Cash taken while no session
// exists falls outside every session window a reconciliation will ever
// compute — SumCompletedCashPayments's window is always [OpenedAt, ClosedAt)
// of SOME session — so the money is not merely hard to reconcile, it is
// permanently invisible. Only PaymentMethodCash is gated; every other method
// settles somewhere other than the physical drawer this session reconciles.
// ---------------------------------------------------------------------------

func TestPaymentService_RegisterSale_RefusesCashWithNoOpenSession(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	paySvc := newPaymentService()
	branch := uuid.New()
	tenantID := uuid.New()

	_, err := paySvc.RegisterSale(ctx, service.RegisterSaleRequest{
		TenantID: tenantID, BranchID: branch, IdempotencyKey: uuid.New().String(),
		Method: domain.PaymentMethodCash, AmountTotal: 1000, Currency: "TRY",
	})
	require.ErrorIs(t, err, pub.ErrNoCashSessionOpen,
		"a cash payment with no open cash session for the branch must be refused, or the drawer can never balance")
}

func TestPaymentService_RegisterSale_AcceptsCashWithOpenSession(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	cashSvc := newCashSessionService()
	paySvc := newPaymentService()
	branch := uuid.New()
	manager := shiftManagerPrincipal(branch)

	_, err := cashSvc.Open(ctx, manager, service.OpenCashSessionRequest{BranchID: branch, OpeningCountedAmount: 0})
	require.NoError(t, err)

	payment, err := paySvc.RegisterSale(ctx, service.RegisterSaleRequest{
		TenantID: manager.TenantID, BranchID: branch, IdempotencyKey: uuid.New().String(),
		Method: domain.PaymentMethodCash, AmountTotal: 1000, Currency: "TRY",
	})
	require.NoError(t, err, "a cash payment must be accepted while the branch has an open cash session")
	assert.Equal(t, domain.PaymentStatusPending, payment.Status)
}

// TestPaymentService_RegisterSale_AcceptsCashDuringClosingControl pins that
// closing_control still counts as "open" for this guard — GetActiveByBranch
// (the read the close guard itself relies on for one-open-per-branch) treats
// opened and closing_control as the same active set. A session mid-count is
// still a real drawer a cashier could be taking cash from at the register
// while a manager elsewhere is counting yesterday's float; refusing cash here
// would just be a second, redundant place cash goes missing from the books.
func TestPaymentService_RegisterSale_AcceptsCashDuringClosingControl(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	cashSvc := newCashSessionService()
	paySvc := newPaymentService()
	branch := uuid.New()
	manager := shiftManagerPrincipal(branch)

	opened, err := cashSvc.Open(ctx, manager, service.OpenCashSessionRequest{BranchID: branch, OpeningCountedAmount: 0})
	require.NoError(t, err)
	_, err = cashSvc.SubmitClosingCount(ctx, manager, opened.Session.ID, service.SubmitClosingCountRequest{ClosingCountedAmount: 0})
	require.NoError(t, err)

	active, err := cashSvc.GetActive(ctx, manager, branch)
	require.NoError(t, err)
	require.Equal(t, domain.CashSessionClosingControl, active.Session.Status, "precondition: session is mid-close")

	_, err = paySvc.RegisterSale(ctx, service.RegisterSaleRequest{
		TenantID: manager.TenantID, BranchID: branch, IdempotencyKey: uuid.New().String(),
		Method: domain.PaymentMethodCash, AmountTotal: 500, Currency: "TRY",
	})
	require.NoError(t, err, "closing_control is still an open drawer for this guard's purposes")
}

// TestPaymentService_RegisterSale_NonCashMethodUnaffectedByNoOpenSession pins
// the guard's method scope: terminal (ÖKC), meal_card, comp, no_charge and
// open_account never touch the physical drawer, so none of them may be
// blocked by a missing cash session. Only terminal is exercised directly —
// domain.PaymentMethod.Valid()'s full enumeration is already pinned elsewhere
// (domain/payment.go); this test only needs one representative non-cash
// method to prove the guard does not fire for it.
func TestPaymentService_RegisterSale_NonCashMethodUnaffectedByNoOpenSession(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	paySvc := newPaymentService()
	branch := uuid.New()
	tenantID := uuid.New()

	payment, err := paySvc.RegisterSale(ctx, service.RegisterSaleRequest{
		TenantID: tenantID, BranchID: branch, IdempotencyKey: uuid.New().String(),
		Method: domain.PaymentMethodTerminal, AmountTotal: 1000, Currency: "TRY",
	})
	require.NoError(t, err, "a terminal payment must never require a cash session — it never touches the drawer")
	assert.Equal(t, domain.PaymentStatusPending, payment.Status)
}

// TestPaymentService_RegisterSale_ConcurrentWithSessionClose_NeverInvisible
// is the TOCTOU case: RegisterSale's cash-session guard and CashSessionService.Close
// race on the same branch. The guard reads under FOR SHARE
// (CashSessionRepo.GetActiveByBranchForShare) specifically so this cannot
// happen: either Close's FOR UPDATE (via GetByIDForUpdate) wins and blocks
// the guard until it commits — at which point Postgres re-evaluates the
// guard's WHERE clause against the now-closed row and it sees nothing — or
// the guard's FOR SHARE wins and blocks Close until the payment transaction
// commits, so closed_at is stamped strictly after the payment's created_at.
//
// The one outcome that must never happen: RegisterSale succeeds, Close also
// succeeds, and the payment's created_at falls outside the closed session's
// window — that is a cash sale reconciliation can never find again. The test
// runs enough iterations that a missing lock (a regression back to a plain
// SELECT) would be expected to surface it.
func TestPaymentService_RegisterSale_ConcurrentWithSessionClose_NeverInvisible(t *testing.T) {
	requireDB(t)
	cashSvc := newCashSessionService()
	paySvc := newPaymentService()

	for i := 0; i < 25; i++ {
		ctx := context.Background()
		branch := uuid.New()
		manager := shiftManagerPrincipal(branch)

		opened, err := cashSvc.Open(ctx, manager, service.OpenCashSessionRequest{BranchID: branch, OpeningCountedAmount: 0})
		require.NoError(t, err)
		_, err = cashSvc.SubmitClosingCount(ctx, manager, opened.Session.ID, service.SubmitClosingCountRequest{ClosingCountedAmount: 0})
		require.NoError(t, err)

		var wg sync.WaitGroup
		var payment domain.Payment
		var payErr error
		var closedView service.CashSessionView
		var closeErr error

		wg.Add(2)
		go func() {
			defer wg.Done()
			payment, payErr = paySvc.RegisterSale(ctx, service.RegisterSaleRequest{
				TenantID: manager.TenantID, BranchID: branch, IdempotencyKey: uuid.New().String(),
				Method: domain.PaymentMethodCash, AmountTotal: 1000, Currency: "TRY",
			})
		}()
		go func() {
			defer wg.Done()
			closedView, closeErr = cashSvc.Close(ctx, manager, opened.Session.ID)
		}()
		wg.Wait()

		switch {
		case payErr != nil:
			// Lost the race outright: the guard saw the session already gone.
			require.ErrorIs(t, payErr, pub.ErrNoCashSessionOpen, "iteration %d", i)
		case closeErr != nil:
			// Close lost the race: RegisterSale's own new pending fiscal
			// submission blocks cannotClose. Assert it is specifically that
			// guard, not some unrelated failure the loop would otherwise mask.
			var cannotClose *service.CashSessionCannotCloseError
			require.ErrorAs(t, closeErr, &cannotClose,
				"iteration %d: close failed for an unexpected reason: %v", i, closeErr)
		default:
			// Both succeeded: the payment MUST be inside the now-closed window.
			require.NotNil(t, closedView.Session.ClosedAt, "iteration %d", i)
			require.True(t, payment.CreatedAt.Before(*closedView.Session.ClosedAt),
				"iteration %d: payment created_at %s must precede closed_at %s or reconciliation loses it forever",
				i, payment.CreatedAt, *closedView.Session.ClosedAt)
		}
	}
}

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
