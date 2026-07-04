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

	poshttp "onlinemenu.tr/internal/modules/pos/http"
	"onlinemenu.tr/internal/platform/auth"
)

// TestRegisterRoutes_AllRoutesRequirePermission is the wiring-audit smoke test
// from docs/lessons-from-b2b.md item 1, applied to the pos module. See the
// catalog module's authz_smoke_test.go for the full rationale.
//
// The close-check and place-order routes also carry the Idempotency-Key
// middleware; since auth.RequirePermission is listed first in r.With, an
// unauthorized caller never reaches the idempotency reservation logic
// (verified implicitly: a roleless principal still gets 403, not the
// idempotency middleware's 400 "missing header" response).
func TestRegisterRoutes_AllRoutesRequirePermission(t *testing.T) {
	engine := newSmokeTestEngine(t)
	cache := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 1})
	hwc := poshttp.NewHandler(poshttp.Params{Logger: zap.NewNop(), Engine: engine, Cache: cache})

	mux := chi.NewMux()
	mux.Use(recoverMiddleware)
	hwc.RegisterRoutes(mux)

	principal := auth.Principal{
		PersonID: uuid.New(),
		Ctx:      auth.ContextStaff,
		TenantID: uuid.New(),
		BranchID: uuid.New(),
		// RoleIDs intentionally empty — no seeded system role grants any
		// pos.* action to a roleless principal.
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
