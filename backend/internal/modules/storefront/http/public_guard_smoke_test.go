package http_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
)

// TestRegisterPublicRoutes_EveryRouteRequiresGuestSession is the wiring-audit
// smoke test (docs/lessons-from-b2b.md item 1) for the public surface — the
// counterpart of pos's authz_smoke_test.go, and the evidence behind cmd/api's
// claim that skipping the staff auth middleware for /api/public/v1/* is safe.
//
// It walks every registered route and asserts that, without a guest session
// cookie, it answers 401 — /sessions excepted, since minting the session is
// precisely what it does. A future route added outside the guarded group
// fails HERE, by name, instead of quietly serving anonymous traffic.
func TestRegisterPublicRoutes_EveryRouteRequiresGuestSession(t *testing.T) {
	deps := &testDeps{}
	router := newPublicRouter(t, deps)

	walked := 0
	err := chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if method == http.MethodOptions {
			return nil
		}
		if isSessionRoute(route) {
			return nil
		}
		walked++

		req := httptest.NewRequest(method, routeWithDummyParams(route), strings.NewReader(`{}`))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		require.Equalf(t, http.StatusUnauthorized, rec.Code,
			"route %s %s must sit behind requireGuestSession (got %d)", method, route, rec.Code)
		return nil
	})
	require.NoError(t, err)
	require.NotZero(t, walked, "the walk must actually visit guarded routes")
}

// TestRegisterPublicRoutes_SessionRouteIsTheOnlyUnguardedOne states the
// exemption explicitly, so widening it is a deliberate edit to this list
// rather than a side effect of adding a route.
func TestRegisterPublicRoutes_SessionRouteIsTheOnlyUnguardedOne(t *testing.T) {
	deps := &testDeps{}
	router := newPublicRouter(t, deps)

	var unguarded []string
	err := chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if method == http.MethodOptions {
			return nil
		}
		req := httptest.NewRequest(method, routeWithDummyParams(route), strings.NewReader(`{}`))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			unguarded = append(unguarded, method+" "+route)
		}
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, []string{"POST /api/public/v1/sessions"}, unguarded)
}

func isSessionRoute(route string) bool {
	return strings.HasSuffix(route, "/sessions")
}

// dummyID / routeWithDummyParams / recoverMiddleware / newSmokeTestEngine live
// in authz_smoke_test.go (same package http_test, admin surface). They are
// reused here rather than redefined — a second copy would not compile.
