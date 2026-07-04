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

	identityhttp "onlinemenu.tr/internal/modules/identity/http"
	"onlinemenu.tr/internal/platform/auth"
)

const dummyID = "11111111-1111-1111-1111-111111111111"

// preContextAllowlist lists the identity routes that are intentionally NOT behind
// auth.RequirePermission: they are the pre-context flow (ADR-AUTH-001 steps 1-3),
// reachable with only a Keycloak-verified Principal that has no TenantID/RoleIDs
// yet. Authentication (the global auth.Middleware) still gates them.
var preContextAllowlist = map[string]bool{
	"GET /v1/identity/me":            true,
	"GET /v1/identity/me/contexts":   true,
	"POST /v1/identity/auth/context": true,
}

// TestRegisterRoutes_AllRoutesRequirePermission is the wiring-audit smoke test
// from docs/lessons-from-b2b.md item 1, applied to the identity module. Every
// route outside preContextAllowlist must deny a context-token principal that
// holds no roles. See the catalog module's authz_smoke_test.go for full rationale.
func TestRegisterRoutes_AllRoutesRequirePermission(t *testing.T) {
	engine := newSmokeTestEngine(t)
	h := identityhttp.NewHandler(nil, nil, nil, nil, zap.NewNop(), engine)

	mux := chi.NewMux()
	mux.Use(recoverMiddleware)
	h.RegisterRoutes(mux)

	tenantID, err := uuid.Parse(dummyID)
	require.NoError(t, err)

	principal := auth.Principal{
		PersonID: uuid.New(),
		Ctx:      auth.ContextStaff,
		TenantID: tenantID,
		BranchID: uuid.New(),
		// RoleIDs intentionally empty — no seeded system role grants identity.* actions.
	}

	err = chi.Walk(mux, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if method == http.MethodOptions {
			return nil
		}
		key := method + " " + route
		req := httptest.NewRequest(method, routeWithDummyParams(route), nil)
		req = req.WithContext(auth.WithPrincipal(req.Context(), principal))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if preContextAllowlist[key] {
			require.NotEqualf(t, http.StatusForbidden, rec.Code,
				"pre-context route %s must NOT require OPA permission (unexpected 403)", key)
			return nil
		}
		require.Equalf(t, http.StatusForbidden, rec.Code,
			"route %s must be wired to auth.RequirePermission (got %d)", key, rec.Code)
		return nil
	})
	require.NoError(t, err)
}

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
