package http_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/platform/auth"
)

func expirePath(id string) string {
	return "/api/v1/payments/fiscal/submissions/" + id + "/expire"
}

func principalWithRole(roleID string) auth.Principal {
	p := auth.Principal{
		PersonID: uuid.New(),
		Ctx:      auth.ContextStaff,
		TenantID: uuid.New(),
		BranchID: uuid.New(),
	}
	if roleID != "" {
		p.RoleIDs = []uuid.UUID{uuid.MustParse(roleID)}
	}
	return p
}

// TestExpireSubmissionAction_RolePermissions pins who may declare that a fiscal
// registration never happened. Expiring FAILS the payment and reopens the
// check's balance for re-collection, so it reuses the manager-only fiscal
// administration action rather than any counter-facing one — a cashier who
// could expire could also wipe a sale the device actually printed.
func TestExpireSubmissionAction_RolePermissions(t *testing.T) {
	engine := newSmokeTestEngine(t)

	tests := []struct {
		name      string
		roleID    string
		wantAllow bool
	}{
		{"manager via wildcard", managerRoleID, true},
		{"cashier must not fail payments", cashierRoleID, false},
		{"shift manager is still counter staff", shiftManagerRoleID, false},
		{"kitchen", kitchenRoleID, false},
		{"roleless principal", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := engine.Decide(t.Context(), "payment.fiscal_terminal.manage", principalWithRole(tt.roleID))
			require.NoError(t, err)
			assert.Equal(t, tt.wantAllow, d.Allow)
		})
	}
}

// TestExpireSubmissionRoute_ForbidsCashier walks the wired route: a cashier is
// stopped by the middleware before the handler body, which would otherwise
// nil-panic on the absent service.
func TestExpireSubmissionRoute_ForbidsCashier(t *testing.T) {
	mux := newFiscalStatusRouter(t)

	req := httptest.NewRequest(http.MethodPost, expirePath(uuid.NewString()), strings.NewReader(`{}`))
	req = req.WithContext(auth.WithPrincipal(req.Context(), principalWithRole(cashierRoleID)))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

// TestExpireSubmissionRoute_ManagerReachesHandler proves the route resolves and
// the manager clears authorization: the request is refused on input validation
// (400), which runs inside the handler before any service call, so the absence
// of a database is irrelevant. A 403 here would mean the action is wrong.
func TestExpireSubmissionRoute_ManagerReachesHandler(t *testing.T) {
	mux := newFiscalStatusRouter(t)

	req := httptest.NewRequest(http.MethodPost, expirePath("not-a-uuid"), strings.NewReader(`{}`))
	req = req.WithContext(auth.WithPrincipal(req.Context(), principalWithRole(managerRoleID)))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestExpireSubmissionRoute_RejectsOverlongReason: the operator note is
// persisted verbatim into the submission's audit payload, so its size is
// bounded at the edge.
func TestExpireSubmissionRoute_RejectsOverlongReason(t *testing.T) {
	mux := newFiscalStatusRouter(t)

	body := `{"reason":"` + strings.Repeat("a", 501) + `"}`
	req := httptest.NewRequest(http.MethodPost, expirePath(uuid.NewString()), strings.NewReader(body))
	req = req.WithContext(auth.WithPrincipal(req.Context(), principalWithRole(managerRoleID)))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

// TestExpireSubmissionRoute_UnauthenticatedIsRejected: no principal at all must
// not reach the handler.
func TestExpireSubmissionRoute_UnauthenticatedIsRejected(t *testing.T) {
	mux := newFiscalStatusRouter(t)

	req := httptest.NewRequest(http.MethodPost, expirePath(uuid.NewString()), strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.NotEqual(t, http.StatusNoContent, rec.Code)
	assert.GreaterOrEqual(t, rec.Code, http.StatusUnauthorized)
}
