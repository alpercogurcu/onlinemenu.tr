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

	storefronthttp "onlinemenu.tr/internal/modules/storefront/http"
	"onlinemenu.tr/internal/platform/auth"
)

// TestRegisterRoutes_AllRoutesRequirePermission is the wiring-audit smoke test
// from docs/lessons-from-b2b.md item 1, applied to the storefront module's
// STAFF surface. See the pos module's authz_smoke_test.go for the full
// rationale.
//
// This covers the admin registrar only. The guest-facing registrar is
// deliberately not walked here: it carries no principal and no OPA at all
// (ADR-ARCH-006 §3), so "every route is behind RequirePermission" is the wrong
// assertion for it — its own guard smoke test asserts 401-without-session
// instead. Keeping them in separate tests means adding a public route can
// never accidentally satisfy this one.
//
// The QRService is left nil on purpose: a roleless principal must be rejected
// by the middleware before any handler body runs, so a nil-pointer panic here
// (caught by recoverMiddleware and surfaced as a 500) means a route escaped
// authorization.
func TestRegisterRoutes_AllRoutesRequirePermission(t *testing.T) {
	engine := newSmokeTestEngine(t)
	h := storefronthttp.NewAdminHandler(storefronthttp.AdminParams{Logger: zap.NewNop(), Engine: engine})

	mux := chi.NewMux()
	mux.Use(recoverMiddleware)
	h.RegisterRoutes(mux)

	principal := auth.Principal{
		PersonID: uuid.New(),
		Ctx:      auth.ContextStaff,
		TenantID: uuid.New(),
		BranchID: uuid.New(),
		// RoleIDs intentionally empty — no seeded system role grants any
		// storefront.* action to a roleless principal.
	}

	walked := 0
	err := chi.Walk(mux, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if method == http.MethodOptions {
			return nil
		}
		walked++
		req := httptest.NewRequest(method, routeWithDummyParams(route), nil)
		req = req.WithContext(auth.WithPrincipal(req.Context(), principal))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		require.Equalf(t, http.StatusForbidden, rec.Code,
			"route %s %s must be wired to auth.RequirePermission (got %d)", method, route, rec.Code)
		return nil
	})
	require.NoError(t, err)
	// Guards against the test passing vacuously if RegisterRoutes ever stops
	// mounting anything (a nil-router refactor, a moved Route prefix).
	require.NotZero(t, walked, "no routes were walked — RegisterRoutes mounted nothing")
}

const dummyID = "11111111-1111-1111-1111-111111111111"

func routeWithDummyParams(pattern string) string {
	out := pattern
	for strings.Contains(out, "{") {
		start := strings.Index(out, "{")
		end := strings.Index(out[start:], "}")
		if end < 0 {
			break
		}
		out = out[:start] + dummyID + out[start+end+1:]
	}
	return out
}

func recoverMiddleware(next http.Handler) http.Handler {
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
