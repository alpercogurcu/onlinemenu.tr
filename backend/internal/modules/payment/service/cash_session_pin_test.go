package service_test

// Integration tests for CashSessionPinService (ADR-DATA-008 PIN akışı).
// identity is faked at the interface boundary (fakeCashierPinService /
// fakeMembershipResolver below) — this package must not import identity
// internals, and go-arch-lint would refuse it if it tried. Redis is faked
// with miniredis (same convention as internal/platform/httpx/idempotency_test.go).
// cash_sessions/cash_session_participants themselves are real, via sharedPool
// (see integration_test.go's TestMain).

import (
	"context"
	"sync"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	identitypub "onlinemenu.tr/internal/modules/identity/public"
	pub "onlinemenu.tr/internal/modules/payment/public"
	"onlinemenu.tr/internal/modules/payment/repo"
	"onlinemenu.tr/internal/modules/payment/service"
	"onlinemenu.tr/internal/platform/auth"
)

// --- fakes -------------------------------------------------------------

// fakeCashierPinService is an in-memory stand-in for identity's real
// PinService, keyed exactly like the real (tenant, person) scope. It
// reproduces the one behaviour callers depend on: VerifyPin returns the
// identical sentinel for "wrong pin" and "no pin set", never anything else.
type fakeCashierPinService struct {
	mu   sync.Mutex
	pins map[[2]uuid.UUID]string
}

func newFakeCashierPinService() *fakeCashierPinService {
	return &fakeCashierPinService{pins: map[[2]uuid.UUID]string{}}
}

func (f *fakeCashierPinService) SetOwnPin(_ context.Context, tenantID, personID uuid.UUID, pin string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pins[[2]uuid.UUID{tenantID, personID}] = pin
	return nil
}

func (f *fakeCashierPinService) VerifyPin(_ context.Context, tenantID, personID uuid.UUID, pin string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	stored, ok := f.pins[[2]uuid.UUID{tenantID, personID}]
	if !ok || stored != pin {
		return identitypub.ErrPinVerificationFailed
	}
	return nil
}

func (f *fakeCashierPinService) ResetPin(_ context.Context, tenantID, personID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.pins, [2]uuid.UUID{tenantID, personID})
	return nil
}

// fakeMembershipResolver always returns a fixed non-empty role set — the pin
// service under test does not make authorization decisions based on the
// resolved roles, it only needs to embed something into the issued token.
type fakeMembershipResolver struct{ roleIDs []uuid.UUID }

func (f *fakeMembershipResolver) ActiveRoleIDsAt(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) ([]uuid.UUID, error) {
	return f.roleIDs, nil
}

// --- harness -------------------------------------------------------------

func newTestRedis(t *testing.T) *redis.Client {
	t.Helper()
	mr := miniredis.RunT(t)
	return redis.NewClient(&redis.Options{Addr: mr.Addr()})
}

func newTestSigner(t *testing.T) *auth.ContextTokenSigner {
	t.Helper()
	s, err := auth.NewContextTokenSigner([]byte("cash-session-pin-test-secret-32b"))
	require.NoError(t, err)
	return s
}

type pinTestDeps struct {
	svc      *service.CashSessionPinService
	pins     *fakeCashierPinService
	signer   *auth.ContextTokenSigner
	observed *observer.ObservedLogs
}

func newCashSessionPinService(t *testing.T) pinTestDeps {
	t.Helper()
	core, observed := observer.New(zapcore.DebugLevel)
	pins := newFakeCashierPinService()
	signer := newTestSigner(t)

	svc := service.NewCashSessionPinService(service.CashSessionPinParams{
		DB:          sharedPool,
		Sessions:    repo.NewCashSessionRepo(),
		Pins:        pins,
		Memberships: &fakeMembershipResolver{roleIDs: []uuid.UUID{uuid.New()}},
		Signer:      signer,
		Redis:       newTestRedis(t),
		Logger:      zap.New(core),
	})
	return pinTestDeps{svc: svc, pins: pins, signer: signer, observed: observed}
}

// newTestScope creates one (tenantID, branchID) pair and two staff
// principals sharing it — a cashier (who joins/is switched to) and a
// shift_manager (who opens/closes the session and resets PINs). Every
// principal in a single test MUST share the same tenant/branch, or the
// session one principal opens is invisible (RLS) to another's calls — this
// is the one thing to get right when building fixtures for this service.
type testScope struct {
	tenantID, branchID uuid.UUID
	cashier, manager   auth.Principal
}

func newTestScope() testScope {
	tenantID, branchID := uuid.New(), uuid.New()
	mk := func(roleID string) auth.Principal {
		return auth.Principal{
			PersonID: uuid.New(), Ctx: auth.ContextStaff,
			TenantID: tenantID, BranchID: branchID,
			RoleIDs: []uuid.UUID{uuid.MustParse(roleID)},
		}
	}
	return testScope{
		tenantID: tenantID, branchID: branchID,
		cashier: mk(cashierRoleID), manager: mk(shiftManagerRoleID),
	}
}

func (s testScope) openSession(t *testing.T) uuid.UUID {
	t.Helper()
	requireDB(t)
	view, err := newCashSessionService().Open(t.Context(), s.manager, service.OpenCashSessionRequest{
		BranchID: s.branchID, OpeningCountedAmount: 10000,
	})
	require.NoError(t, err)
	return view.Session.ID
}

func (s testScope) closeSession(t *testing.T, sessionID uuid.UUID) {
	t.Helper()
	css := newCashSessionService()
	_, err := css.SubmitClosingCount(t.Context(), s.manager, sessionID, service.SubmitClosingCountRequest{ClosingCountedAmount: 10000})
	require.NoError(t, err)
	_, err = css.Close(t.Context(), s.manager, sessionID)
	require.NoError(t, err)
}

// --- tests -----------------------------------------------------------------

func TestCashSessionPinService_Join_ThenSwitch_Succeeds(t *testing.T) {
	requireDB(t)
	scope := newTestScope()
	sessionID := scope.openSession(t)
	deps := newCashSessionPinService(t)

	require.NoError(t, deps.svc.Join(t.Context(), scope.cashier, sessionID, "1234"))

	token, err := deps.svc.Switch(t.Context(), scope.manager, sessionID, scope.cashier.PersonID, "1234")
	require.NoError(t, err)
	require.NotEmpty(t, token)

	p, err := deps.signer.Verify(token)
	require.NoError(t, err)
	assert.Equal(t, scope.cashier.PersonID, p.PersonID)
	assert.Equal(t, sessionID, p.SessionID)
	assert.Equal(t, scope.branchID, p.BranchID)
}

func TestCashSessionPinService_Join_RejectsSessionScopedPrincipal(t *testing.T) {
	requireDB(t)
	scope := newTestScope()
	sessionID := scope.openSession(t)
	deps := newCashSessionPinService(t)

	impersonated := scope.cashier
	impersonated.SessionID = uuid.New() // simulates an already-pin-switched principal

	err := deps.svc.Join(t.Context(), impersonated, sessionID, "1234")
	assert.ErrorIs(t, err, pub.ErrSessionScopedPrincipal)
}

func TestCashSessionPinService_Switch_WrongPin_Refused(t *testing.T) {
	requireDB(t)
	scope := newTestScope()
	sessionID := scope.openSession(t)
	deps := newCashSessionPinService(t)

	require.NoError(t, deps.svc.Join(t.Context(), scope.cashier, sessionID, "1234"))

	_, err := deps.svc.Switch(t.Context(), scope.manager, sessionID, scope.cashier.PersonID, "0000")
	assert.ErrorIs(t, err, pub.ErrPinVerificationFailed)
}

func TestCashSessionPinService_Switch_NonParticipant_SameErrorAsWrongPin(t *testing.T) {
	requireDB(t)
	scope := newTestScope()
	sessionID := scope.openSession(t)
	deps := newCashSessionPinService(t)

	neverJoined := uuid.New()
	// Give the target a pin in the fake store directly (bypassing Join) so
	// this proves participation, not pin-existence, is what's missing.
	require.NoError(t, deps.pins.SetOwnPin(t.Context(), scope.tenantID, neverJoined, "1234"))

	_, err := deps.svc.Switch(t.Context(), scope.manager, sessionID, neverJoined, "1234")
	assert.ErrorIs(t, err, pub.ErrPinVerificationFailed,
		"a correct pin for a non-participant must fail exactly like a wrong pin")
}

func TestCashSessionPinService_Switch_LocksAfter5Failures(t *testing.T) {
	requireDB(t)
	scope := newTestScope()
	sessionID := scope.openSession(t)
	deps := newCashSessionPinService(t)

	require.NoError(t, deps.svc.Join(t.Context(), scope.cashier, sessionID, "1234"))

	for i := 0; i < 5; i++ {
		_, err := deps.svc.Switch(t.Context(), scope.manager, sessionID, scope.cashier.PersonID, "0000")
		require.ErrorIs(t, err, pub.ErrPinVerificationFailed, "attempt %d", i+1)
	}

	// The 6th attempt, even with the CORRECT pin, must still be refused.
	_, err := deps.svc.Switch(t.Context(), scope.manager, sessionID, scope.cashier.PersonID, "1234")
	assert.ErrorIs(t, err, pub.ErrPinVerificationFailed, "6th attempt must be locked out even with the correct pin")
}

func TestCashSessionPinService_ReJoin_ClearsLockout(t *testing.T) {
	requireDB(t)
	scope := newTestScope()
	sessionID := scope.openSession(t)
	deps := newCashSessionPinService(t)

	require.NoError(t, deps.svc.Join(t.Context(), scope.cashier, sessionID, "1234"))

	for i := 0; i < 5; i++ {
		_, err := deps.svc.Switch(t.Context(), scope.manager, sessionID, scope.cashier.PersonID, "0000")
		require.Error(t, err)
	}
	_, err := deps.svc.Switch(t.Context(), scope.manager, sessionID, scope.cashier.PersonID, "1234")
	require.ErrorIs(t, err, pub.ErrPinVerificationFailed, "must be locked before re-join")

	// Re-join via the full Keycloak flow (SessionID == uuid.Nil) is the ONLY
	// thing ADR-DATA-008 §5 says clears the lock.
	require.NoError(t, deps.svc.Join(t.Context(), scope.cashier, sessionID, "1234"))

	token, err := deps.svc.Switch(t.Context(), scope.manager, sessionID, scope.cashier.PersonID, "1234")
	require.NoError(t, err, "lockout must be cleared after re-join")
	assert.NotEmpty(t, token)
}

func TestCashSessionPinService_Switch_ClosedSession_Refused(t *testing.T) {
	requireDB(t)
	scope := newTestScope()
	sessionID := scope.openSession(t)
	deps := newCashSessionPinService(t)

	require.NoError(t, deps.svc.Join(t.Context(), scope.cashier, sessionID, "1234"))
	scope.closeSession(t, sessionID)

	_, err := deps.svc.Switch(t.Context(), scope.manager, sessionID, scope.cashier.PersonID, "1234")
	assert.ErrorIs(t, err, pub.ErrCashSessionClosed)
}

// TestCashSessionPinService_IsOpen_ReflectsSessionLifecycle is the "actual
// mechanism" behind ADR-DATA-008 §4's "closing invalidates every token
// derived from it": IsOpen is what platform/auth.RequireOpenSession consults
// on every request carrying a session-scoped token (via
// payment.CashSessionOpenChecker), so this proves the source of truth it
// reads flips the moment the session closes.
func TestCashSessionPinService_IsOpen_ReflectsSessionLifecycle(t *testing.T) {
	requireDB(t)
	scope := newTestScope()
	sessionID := scope.openSession(t)
	deps := newCashSessionPinService(t)

	open, err := deps.svc.IsOpen(t.Context(), scope.tenantID, sessionID)
	require.NoError(t, err)
	assert.True(t, open)

	scope.closeSession(t, sessionID)

	open, err = deps.svc.IsOpen(t.Context(), scope.tenantID, sessionID)
	require.NoError(t, err)
	assert.False(t, open)
}

func TestCashSessionPinService_IsOpen_UnknownSession_ReportsNotOpen(t *testing.T) {
	requireDB(t)
	deps := newCashSessionPinService(t)
	open, err := deps.svc.IsOpen(t.Context(), uuid.New(), uuid.New())
	require.NoError(t, err, "an unknown session must not be an error path — RequireOpenSession would otherwise 500 instead of 401")
	assert.False(t, open)
}

func TestCashSessionPinService_ResetPin_ClearsAccess(t *testing.T) {
	requireDB(t)
	scope := newTestScope()
	sessionID := scope.openSession(t)
	deps := newCashSessionPinService(t)

	require.NoError(t, deps.svc.Join(t.Context(), scope.cashier, sessionID, "1234"))
	_, err := deps.svc.Switch(t.Context(), scope.manager, sessionID, scope.cashier.PersonID, "1234")
	require.NoError(t, err)

	require.NoError(t, deps.svc.ResetPin(t.Context(), scope.manager, sessionID, scope.cashier.PersonID))

	_, err = deps.svc.Switch(t.Context(), scope.manager, sessionID, scope.cashier.PersonID, "1234")
	assert.ErrorIs(t, err, pub.ErrPinVerificationFailed, "the pin manager just reset must no longer verify")
}

// TestCashSessionPinService_PinNeverLogged is the log-leak assertion the
// task explicitly calls for, not just an "I was careful" claim: it captures
// every log record emitted across a full join+switch(fail)+switch(success)
// cycle and asserts the literal pin string appears in none of them, in
// neither the message nor any structured field.
func TestCashSessionPinService_PinNeverLogged(t *testing.T) {
	requireDB(t)
	scope := newTestScope()
	sessionID := scope.openSession(t)
	deps := newCashSessionPinService(t)
	const pin = "914773" // a distinctive 6-digit value unlikely to appear incidentally

	require.NoError(t, deps.svc.Join(t.Context(), scope.cashier, sessionID, pin))
	_, _ = deps.svc.Switch(t.Context(), scope.manager, sessionID, scope.cashier.PersonID, "000000")
	_, err := deps.svc.Switch(t.Context(), scope.manager, sessionID, scope.cashier.PersonID, pin)
	require.NoError(t, err)

	for _, entry := range deps.observed.All() {
		assert.NotContains(t, entry.Message, pin, "pin leaked into a log message")
		for _, f := range entry.Context {
			assert.NotContains(t, f.String, pin, "pin leaked into a structured log field")
			assert.NotContains(t, f.Key, pin, "pin leaked into a structured log field key")
		}
	}
}
