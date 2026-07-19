package http_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"onlinemenu.tr/internal/platform/auth"
)

func settlementPath(checkID string) string {
	return "/api/v1/payments/checks/" + checkID + "/settlement"
}

// TestCheckSettlementRoute_ForbidsUnauthorizedRole walks the actual wired route
// to prove the permission middleware is attached: a kitchen principal is
// stopped before the handler body, which would otherwise nil-panic on the
// absent service. The panic-vs-403 distinction is what makes this test
// meaningful — a route registered without the middleware would reach the
// handler and be caught by recoverMiddleware as a 500, not a 403.
func TestCheckSettlementRoute_ForbidsUnauthorizedRole(t *testing.T) {
	mux := newFiscalStatusRouter(t)

	for _, tt := range []struct {
		name   string
		roleID string
	}{
		{"kitchen has no business with money", kitchenRoleID},
		{"bar likewise", barRoleID},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := auth.Principal{
				PersonID: uuid.New(),
				Ctx:      auth.ContextStaff,
				TenantID: uuid.New(),
				BranchID: uuid.New(),
				RoleIDs:  []uuid.UUID{uuid.MustParse(tt.roleID)},
			}

			req := httptest.NewRequest(http.MethodGet, settlementPath(uuid.NewString()), nil)
			req = req.WithContext(auth.WithPrincipal(req.Context(), p))
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			assert.Equal(t, http.StatusForbidden, rec.Code)
		})
	}
}

// TestCheckSettlementRoute_CashierReachesHandler proves the 200-path opens for
// a cashier: the request clears the permission middleware and is refused on
// input validation (400), not authorization (403).
//
// This is the whole point of the endpoint. The cashier is the role that holds
// the check open at the counter and the role that cannot read payments; if the
// route were wired to payment.payment.read this would be a 403 and the POS
// would fall back to the 5-minute fiscal window that double-charges.
//
// A malformed check id is used so validation short-circuits before any service
// call — no database needed.
func TestCheckSettlementRoute_CashierReachesHandler(t *testing.T) {
	mux := newFiscalStatusRouter(t)

	cashier := auth.Principal{
		PersonID: uuid.New(),
		Ctx:      auth.ContextStaff,
		TenantID: uuid.New(),
		BranchID: uuid.New(),
		RoleIDs:  []uuid.UUID{uuid.MustParse(cashierRoleID)},
	}

	req := httptest.NewRequest(http.MethodGet, settlementPath("not-a-uuid"), nil)
	req = req.WithContext(auth.WithPrincipal(req.Context(), cashier))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code,
		"cashier must clear authorization; a 403 here means the route carries the wrong action")
}

// TestCheckSettlementRoute_RequiresPrincipal: an unauthenticated caller is
// rejected before the handler touches a service.
func TestCheckSettlementRoute_RequiresPrincipal(t *testing.T) {
	mux := newFiscalStatusRouter(t)

	req := httptest.NewRequest(http.MethodGet, settlementPath(uuid.NewString()), nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.NotEqual(t, http.StatusOK, rec.Code)
	assert.Contains(t, []int{http.StatusUnauthorized, http.StatusForbidden}, rec.Code)
}
