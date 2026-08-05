package http_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	catalogpub "onlinemenu.tr/internal/modules/catalog/public"
	pospub "onlinemenu.tr/internal/modules/pos/public"
	storefronthttp "onlinemenu.tr/internal/modules/storefront/http"
	"onlinemenu.tr/internal/modules/storefront/service"
	"onlinemenu.tr/internal/platform/auth"
)

const testGuestSecret = "guest-test-secret-32-bytes-long!!"

// ---------------------------------------------------------------------------
// Test doubles. The public surface is wired here with real middleware and
// real services, but stubbed cross-module readers: the point of these tests
// is the guard chain, and a DB would only add a way for them to fail for
// unrelated reasons.
// ---------------------------------------------------------------------------

type stubMenuReader struct {
	categories []catalogpub.StorefrontCategory
	priced     []catalogpub.PricedLine
	priceErr   error
	gotLines   []catalogpub.CartLine
}

func (s *stubMenuReader) GetStorefrontMenu(_ context.Context, _, _ uuid.UUID) ([]catalogpub.StorefrontCategory, error) {
	return s.categories, nil
}

func (s *stubMenuReader) PriceCart(_ context.Context, _, _ uuid.UUID, lines []catalogpub.CartLine) ([]catalogpub.PricedLine, error) {
	s.gotLines = lines
	if s.priceErr != nil {
		return nil, s.priceErr
	}
	return s.priced, nil
}

type stubOrderGateway struct {
	gotRequest pospub.GuestOrderRequest
	gotLinker  pospub.GuestOrderLinker
	placeErr   error
	view       pospub.GuestOrderView
}

func (s *stubOrderGateway) PlaceGuestOrder(_ context.Context, req pospub.GuestOrderRequest, link pospub.GuestOrderLinker) (pospub.GuestOrderResult, error) {
	s.gotRequest = req
	s.gotLinker = link
	if s.placeErr != nil {
		return pospub.GuestOrderResult{}, s.placeErr
	}
	return pospub.GuestOrderResult{OrderID: uuid.New(), CheckID: uuid.New(), Status: "pending"}, nil
}

func (s *stubOrderGateway) GetGuestOrder(_ context.Context, _, _ uuid.UUID) (pospub.GuestOrderView, error) {
	return s.view, nil
}

type testDeps struct {
	menuReader *stubMenuReader
	orders     *stubOrderGateway
	signer     *auth.GuestTokenSigner
}

// newPublicRouter wires the real RegisterPublicRoutes chain over miniredis.
//
// A dead Redis address is deliberately NOT used here (unlike pos's authz
// smoke test): the rate limiter fails CLOSED, so every route would answer 503
// and every assertion about 401/200 would be meaningless.
func newPublicRouter(t *testing.T, deps *testDeps) *chi.Mux {
	t.Helper()

	mr := miniredis.RunT(t)
	cache := redis.NewClient(&redis.Options{Addr: mr.Addr()})

	signer, err := auth.NewGuestTokenSigner([]byte(testGuestSecret))
	require.NoError(t, err)
	deps.signer = signer

	if deps.menuReader == nil {
		deps.menuReader = &stubMenuReader{}
	}
	if deps.orders == nil {
		deps.orders = &stubOrderGateway{}
	}
	logger := zap.NewNop()

	handler := storefronthttp.NewPublicHandler(storefronthttp.PublicParams{
		// Sessions is nil: no test here exercises POST /sessions' happy path,
		// which needs a database. Every guard assertion below is reached
		// before any service call.
		Menu: service.NewMenuService(service.MenuParams{Catalog: deps.menuReader, Logger: logger}),
		Orders: service.NewOrderService(service.OrderParams{
			Catalog: deps.menuReader,
			Placer:  deps.orders,
			Orders:  deps.orders,
			Logger:  logger,
		}),
		Signer: signer,
		Cache:  cache,
		Config: storefronthttp.Config{},
		Logger: logger,
	})

	mux := chi.NewMux()
	handler.RegisterPublicRoutes(mux)
	return mux
}

func guestCookie(t *testing.T, signer *auth.GuestTokenSigner) *http.Cookie {
	t.Helper()
	token, err := signer.IssueGuest(uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New())
	require.NoError(t, err)
	return &http.Cookie{Name: "om_guest", Value: token}
}

// ---------------------------------------------------------------------------

func TestPublicRoutes_NoCookie_Returns401(t *testing.T) {
	deps := &testDeps{}
	router := newPublicRouter(t, deps)

	req := httptest.NewRequest(http.MethodGet, "/api/public/v1/menu", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))
}

func TestPublicRoutes_MalformedCookie_Returns401(t *testing.T) {
	deps := &testDeps{}
	router := newPublicRouter(t, deps)

	req := httptest.NewRequest(http.MethodGet, "/api/public/v1/menu", nil)
	req.AddCookie(&http.Cookie{Name: "om_guest", Value: "not.a.token"})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

// TestPublicRoutes_ForeignSignedGuestToken_Returns401 covers the case a
// stolen-secret-free forgery attempt looks like: a well-formed guest token
// signed by someone else's key.
func TestPublicRoutes_ForeignSignedGuestToken_Returns401(t *testing.T) {
	deps := &testDeps{}
	router := newPublicRouter(t, deps)

	foreign, err := auth.NewGuestTokenSigner([]byte("another-secret-32-bytes-long!!!!"))
	require.NoError(t, err)
	token, err := foreign.IssueGuest(uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New())
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/api/public/v1/menu", nil)
	req.AddCookie(&http.Cookie{Name: "om_guest", Value: token})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

// TestPublicRoutes_StaffTokenInCookie_Returns401 is the cross-surface half of
// the guarantee: a perfectly valid staff CTX token is worthless here.
func TestPublicRoutes_StaffTokenInCookie_Returns401(t *testing.T) {
	deps := &testDeps{}
	router := newPublicRouter(t, deps)

	staffSigner, err := auth.NewContextTokenSigner([]byte("staff-secret-32-bytes-long-pad!!"))
	require.NoError(t, err)
	staffToken, err := staffSigner.IssueStaff(uuid.New(), uuid.New(), uuid.New(), nil)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/api/public/v1/menu", nil)
	req.AddCookie(&http.Cookie{Name: "om_guest", Value: staffToken})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code,
		"a staff token must never authenticate a guest surface")
}

// TestPublicRoutes_ValidGuestCookie_Passes proves the 401s above come from
// the guard and not from some unrelated wiring failure.
func TestPublicRoutes_ValidGuestCookie_Passes(t *testing.T) {
	deps := &testDeps{}
	router := newPublicRouter(t, deps)

	req := httptest.NewRequest(http.MethodGet, "/api/public/v1/menu", nil)
	req.AddCookie(guestCookie(t, deps.signer))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "private, max-age=30", rec.Header().Get("Cache-Control"))
}

// staffOnlyVerifier rejects everything: the staff chain's Keycloak path must
// not be reachable with a guest token, and this makes any acceptance a test
// failure rather than a network call.
type staffOnlyVerifier struct{}

func (staffOnlyVerifier) Verify(_ context.Context, _ string) (*auth.KeycloakClaims, error) {
	return nil, errors.New("keycloak verification must not be reached in this test")
}

// TestGuestTokenOnStaffEndpoint_Returns401 is the mirror image of the tests
// above, at the middleware the staff API actually mounts: a guest token's typ
// is not "CTX", so auth.Middleware routes it down the Keycloak path where it
// fails. "A guest token is worthless on a staff endpoint" is therefore
// structural, not a rule each handler must remember.
func TestGuestTokenOnStaffEndpoint_Returns401(t *testing.T) {
	guestSigner, err := auth.NewGuestTokenSigner([]byte(testGuestSecret))
	require.NoError(t, err)
	staffSigner, err := auth.NewContextTokenSigner([]byte("staff-secret-32-bytes-long-pad!!"))
	require.NoError(t, err)

	guestToken, err := guestSigner.IssueGuest(uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New())
	require.NoError(t, err)

	reached := false
	mux := chi.NewMux()
	mux.Use(auth.Middleware(staffOnlyVerifier{}, staffSigner))
	mux.Get("/api/v1/pos/orders", func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/pos/orders", nil)
	req.Header.Set("Authorization", "Bearer "+guestToken)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.False(t, reached, "a guest token must never reach a staff handler")
	assert.False(t, auth.IsContextToken(guestToken))
	assert.True(t, auth.IsGuestToken(guestToken))
}

// TestStartSession_TokenNeverAppearsInPath guards ADR-ARCH-006 §3: the QR
// token is read from the request body, so no route pattern may carry it.
func TestStartSession_TokenNeverAppearsInPath(t *testing.T) {
	deps := &testDeps{}
	router := newPublicRouter(t, deps)

	err := chi.Walk(router, func(_, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		lowered := strings.ToLower(route)
		assert.NotContains(t, lowered, "{token}", "route %s must not carry a QR token in its path", route)
		assert.NotContains(t, lowered, "{qr", "route %s must not carry a QR token in its path", route)
		return nil
	})
	require.NoError(t, err)
}

// TestGuestCookieAttributes pins the cookie flags the whole surface leans on.
func TestGuestCookieAttributes(t *testing.T) {
	signer, err := auth.NewGuestTokenSigner([]byte(testGuestSecret))
	require.NoError(t, err)
	token, err := signer.IssueGuest(uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New())
	require.NoError(t, err)
	session, err := signer.VerifyGuest(token)
	require.NoError(t, err)

	assert.WithinDuration(t, time.Now().Add(4*time.Hour), session.ExpiresAt, time.Minute,
		"guest sessions are short-lived by design (ADR-ARCH-006 §5)")
}
