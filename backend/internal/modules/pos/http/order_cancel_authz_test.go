package http_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	poshttp "onlinemenu.tr/internal/modules/pos/http"
	"onlinemenu.tr/internal/platform/auth"
)

// newOrderRouteMux mounts the real pos routes against the real OPA bundle
// with no services wired: a request that gets past authorization panics on
// the nil service and the recover middleware turns it into a 500, so "not
// 403" is exactly "authorization let it through".
func newOrderRouteMux(t *testing.T) *chi.Mux {
	t.Helper()
	engine := newSmokeTestEngine(t)
	cache := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 1})
	hwc := poshttp.NewHandler(poshttp.Params{Logger: zap.NewNop(), Engine: engine, Cache: cache})
	mux := chi.NewMux()
	mux.Use(recoverMiddleware)
	hwc.RegisterRoutes(mux)
	return mux
}

func postAs(mux http.Handler, roleID uuid.UUID, path string) int {
	req := httptest.NewRequest(http.MethodPost, path, nil)
	req = req.WithContext(auth.WithPrincipal(req.Context(), tablePolicyPrincipal(roleID)))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec.Code
}

// TestOrderIntakeAndCancelRoutes_KitchenAndBarDenied is the regression for the
// 2026-09-19 e2e finding: kitchen got 403 on /accept but could reach the same
// transitions through /advance. With /advance narrowed to kitchen targets,
// the intake and cancel routes must stay closed to kitchen/bar.
func TestOrderIntakeAndCancelRoutes_KitchenAndBarDenied(t *testing.T) {
	mux := newOrderRouteMux(t)
	for _, role := range []struct {
		name string
		id   uuid.UUID
	}{
		{"kitchen", tablePolicyKitchenID},
		{"bar", tablePolicyBarID},
	} {
		for _, action := range []string{"accept", "reject", "cancel"} {
			path := "/api/v1/pos/orders/" + dummyID + "/" + action
			assert.Equalf(t, http.StatusForbidden, postAs(mux, role.id, path), "%s must be denied POST %s", role.name, path)
		}
	}
}

func TestOrderCancelRoute_CounterRolesAuthorized(t *testing.T) {
	mux := newOrderRouteMux(t)
	path := "/api/v1/pos/orders/" + dummyID + "/cancel"
	for _, role := range []struct {
		name string
		id   uuid.UUID
	}{
		{"manager", tablePolicyManagerID},
		{"shift_manager", tablePolicyShiftManagerID},
		{"cashier", tablePolicyCashierID},
	} {
		code := postAs(mux, role.id, path)
		assert.NotEqualf(t, http.StatusForbidden, code, "%s must be authorized for POST %s", role.name, path)
		assert.NotContainsf(t, []int{http.StatusNotFound, http.StatusMethodNotAllowed}, code, "POST %s must be routed", path)
	}
}
