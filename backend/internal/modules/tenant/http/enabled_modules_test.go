package http_test

// This file proves the M1 fix (final-review-report.md): GET
// /tenants/{tenantID}/modules lets every staff role read enabled_modules
// without granting tenant.tenant.read, and the response carries no other
// field of pub.Tenant. It reuses the testcontainers TestMain and
// handler/fixture helpers defined in branch_directory_integration_test.go.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/platform/auth"
)

func TestGetEnabledModules_CashierPrincipal_ReturnsOnlyEnabledModules(t *testing.T) {
	ctx := context.Background()
	tenant := createTestTenant(t, ctx, []string{"pos", "catalog"})

	h := newTestHandler(t)
	mux := chi.NewMux()
	h.RegisterRoutes(mux)

	cashier := auth.Principal{
		PersonID: uuid.New(),
		Ctx:      auth.ContextStaff,
		TenantID: tenant.ID,
		BranchID: uuid.New(),
		RoleIDs:  []uuid.UUID{cashierRoleID},
	}

	req := httptest.NewRequest(http.MethodGet, "/tenants/"+tenant.ID.String()+"/modules", nil)
	req = req.WithContext(auth.WithPrincipal(req.Context(), cashier))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body, 1, "response must carry only the enabled_modules key, got: %v", body)
	require.Contains(t, body, "enabled_modules")
	require.ElementsMatch(t, []string{"pos", "catalog"}, body["enabled_modules"])

	// The same principal must still be forbidden from tenant.tenant.read —
	// tenant.modules.read must not have widened it.
	req2 := httptest.NewRequest(http.MethodGet, "/tenants/"+tenant.ID.String()+"/", nil)
	req2 = req2.WithContext(auth.WithPrincipal(req2.Context(), cashier))
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)
	require.Equal(t, http.StatusForbidden, rec2.Code)
}
