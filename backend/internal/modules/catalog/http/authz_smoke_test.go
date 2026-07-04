package http_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	cataloghttp "onlinemenu.tr/internal/modules/catalog/http"
	"onlinemenu.tr/internal/platform/auth"
)

// TestRegisterRoutes_AllRoutesRequirePermission is the "wiring audit" smoke test
// requested in docs/lessons-from-b2b.md item 1: it proves every catalog route is
// actually behind the OPA authorization middleware, not just that the middleware
// exists somewhere in the codebase.
//
// Method: walk the real route table (chi.Walk) and send each request with a
// staff Principal that holds no roles. Every route seeded in
// configs/opa/bundles/authz.rego denies an empty role set, so a route wired to
// RequirePermission always answers 403. A route that is NOT wired reaches the
// underlying handler instead (which, with nil service stubs, panics into chi's
// Recoverer -> 500, or returns something else) — anything other than 403 fails
// the test and names the offending route.
func TestRegisterRoutes_AllRoutesRequirePermission(t *testing.T) {
	engine := newSmokeTestEngine(t)

	h := cataloghttp.NewHandler(cataloghttp.Params{
		Logger: zap.NewNop(),
		Engine: engine,
	})

	mux := chi.NewMux()
	mux.Use(chimiddlewareRecoverer)
	h.RegisterRoutes(mux)

	principal := auth.Principal{
		PersonID: uuid.New(),
		Ctx:      auth.ContextStaff,
		TenantID: uuid.New(),
		BranchID: uuid.New(),
		// RoleIDs intentionally empty: no seeded system role grants any action to a
		// roleless principal, so every properly-wired route must answer 403.
	}

	err := chi.Walk(mux, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if method == http.MethodOptions {
			return nil
		}
		req := httptest.NewRequest(method, routeWithDummyParams(route), nil)
		req = req.WithContext(auth.WithPrincipal(req.Context(), principal))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		require.Equalf(t, http.StatusForbidden, rec.Code,
			"route %s %s must be wired to auth.RequirePermission (got %d)", method, route, rec.Code)
		return nil
	})
	require.NoError(t, err)
}

// routeWithDummyParams replaces chi path parameter placeholders (e.g. "{id}") with
// a fixed, syntactically valid UUID so downstream uuid.Parse calls (which never
// run in this test, since authz middleware short-circuits first) would not panic
// if they did.
func routeWithDummyParams(pattern string) string {
	const dummy = "11111111-1111-1111-1111-111111111111"
	out := pattern
	for strings.Contains(out, "{") {
		start := strings.Index(out, "{")
		end := strings.Index(out[start:], "}")
		if end < 0 {
			break
		}
		out = out[:start] + dummy + out[start+end+1:]
	}
	return out
}

func chimiddlewareRecoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				w.WriteHeader(http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func newSmokeTestEngine(t *testing.T) *auth.Engine {
	t.Helper()
	eng, err := auth.NewEngine(
		auth.EngineConfig{BundlePath: "../../../../configs/opa/bundles"},
		redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 1}),
		zap.NewNop(),
	)
	require.NoError(t, err)
	return eng
}
