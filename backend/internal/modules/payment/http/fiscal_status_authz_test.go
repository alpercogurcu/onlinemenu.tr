package http_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	paymenthttp "onlinemenu.tr/internal/modules/payment/http"
	"onlinemenu.tr/internal/platform/auth"
)

// Well-known system role ids from migrations/identity/000006_seed_system_roles.
const (
	cashierRoleID      = "00000001-0000-0000-0000-000000000001"
	shiftManagerRoleID = "00000001-0000-0000-0000-000000000002"
	kitchenRoleID      = "00000001-0000-0000-0000-000000000004"
	barRoleID          = "00000001-0000-0000-0000-000000000005"
	managerRoleID      = "00000001-0000-0000-0000-000000000006"
)

// newFiscalStatusRouter wires the real payment routes with a nil service. The
// fiscal-pending tests below never reach a service call: they assert on the
// permission middleware and on input validation, both of which run first.
func newFiscalStatusRouter(t *testing.T) *chi.Mux {
	t.Helper()
	engine := newSmokeTestEngine(t)
	cache := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 1})
	hwc := paymenthttp.NewHandler(paymenthttp.Params{Logger: zap.NewNop(), Engine: engine, Cache: cache})

	mux := chi.NewMux()
	mux.Use(recoverMiddleware)
	hwc.RegisterRoutes(mux)
	return mux
}

// TestFiscalStatusAction_RolePermissions pins who may poll fiscal registration
// status. The bug this endpoint exists to fix: cashier holds no
// payment.payment.read, so a station polling that action got 403 and treated
// the silence as "settled". The fix is this NARROW action for cashier — not a
// widening of payment.payment.read, which would hand counter staff the
// tenant-wide payment history.
func TestFiscalStatusAction_RolePermissions(t *testing.T) {
	engine := newSmokeTestEngine(t)

	tests := []struct {
		name      string
		roleID    string
		wantAllow bool
		wantScope string
	}{
		{"cashier polls from the counter", cashierRoleID, true, "branch"},
		{"shift manager", shiftManagerRoleID, true, "branch"},
		{"manager via wildcard", managerRoleID, true, "tenant"},
		{"kitchen has no business with money", kitchenRoleID, false, ""},
		{"bar likewise", barRoleID, false, ""},
		{"roleless principal", "", false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := auth.Principal{
				PersonID: uuid.New(),
				Ctx:      auth.ContextStaff,
				TenantID: uuid.New(),
				BranchID: uuid.New(),
			}
			if tt.roleID != "" {
				p.RoleIDs = []uuid.UUID{uuid.MustParse(tt.roleID)}
			}

			d, err := engine.Decide(t.Context(), "payment.fiscal_status.read", p)
			require.NoError(t, err)
			assert.Equal(t, tt.wantAllow, d.Allow)
			if tt.wantAllow {
				assert.Equal(t, tt.wantScope, d.Scope,
					"scope drives the service-layer branch check; a wrong scope silently disables it")
			}
		})
	}
}

// TestFiscalStatusAction_DoesNotWidenPaymentRead: the new action must not have
// leaked payment.payment.read to the cashier as a side effect. Reconciliation
// reads stay with shift_manager and manager.
func TestFiscalStatusAction_DoesNotWidenPaymentRead(t *testing.T) {
	engine := newSmokeTestEngine(t)

	cashier := auth.Principal{
		PersonID: uuid.New(),
		Ctx:      auth.ContextStaff,
		TenantID: uuid.New(),
		BranchID: uuid.New(),
		RoleIDs:  []uuid.UUID{uuid.MustParse(cashierRoleID)},
	}

	d, err := engine.Decide(t.Context(), "payment.payment.read", cashier)
	require.NoError(t, err)
	assert.False(t, d.Allow, "cashier must still be denied the tenant-wide payment history")
}

// TestFiscalPendingRoute_ForbidsUnauthorizedRole walks the actual wired route
// (not just the policy) to prove the middleware is attached with the new
// action: a kitchen principal is stopped before the handler body, which would
// otherwise nil-panic on the absent service.
func TestFiscalPendingRoute_ForbidsUnauthorizedRole(t *testing.T) {
	mux := newFiscalStatusRouter(t)

	kitchen := auth.Principal{
		PersonID: uuid.New(),
		Ctx:      auth.ContextStaff,
		TenantID: uuid.New(),
		BranchID: uuid.New(),
		RoleIDs:  []uuid.UUID{uuid.MustParse(kitchenRoleID)},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/payments/fiscal-pending?branch_id="+uuid.NewString(), nil)
	req = req.WithContext(auth.WithPrincipal(req.Context(), kitchen))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

// TestFiscalPendingRoute_RejectsMalformedBranchID proves the route resolves to
// the new handler for an authorized cashier — the request gets past the
// permission middleware and is refused on input validation (400), not 403.
// Validation runs before any service call, so no database is needed.
func TestFiscalPendingRoute_RejectsMalformedBranchID(t *testing.T) {
	mux := newFiscalStatusRouter(t)

	cashier := auth.Principal{
		PersonID: uuid.New(),
		Ctx:      auth.ContextStaff,
		TenantID: uuid.New(),
		BranchID: uuid.New(),
		RoleIDs:  []uuid.UUID{uuid.MustParse(cashierRoleID)},
	}

	tests := []struct {
		name     string
		query    string
		wantCode int
	}{
		{"missing branch_id", "", http.StatusUnprocessableEntity},
		{"unparseable branch_id", "?branch_id=not-a-uuid", http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/payments/fiscal-pending"+tt.query, nil)
			req = req.WithContext(auth.WithPrincipal(req.Context(), cashier))
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			assert.Equal(t, tt.wantCode, rec.Code,
				"cashier must reach the handler; a 403 here means the route lost payment.fiscal_status.read")
		})
	}
}
