package http_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
)

func TestCreateBranch_DuplicateSlug_Returns409(t *testing.T) {
	ctx := context.Background()
	tenant := createTestTenant(t, ctx, []string{"pos"})
	existing := createTestBranch(t, ctx, tenant.ID)
	h := newTestHandler(t)

	payload := `{"name":"Kopya Şube","slug":"` + existing.Slug + `","ownership_type":"sube","operation_type":"restoran","identity_type":"kurumsal","tax_no":"DUPTAX1","is_active":true}`
	req := httptest.NewRequest(http.MethodPost, "/tenants/"+tenant.ID.String()+"/branches/", strings.NewReader(payload))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("tenantID", tenant.ID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	h.CreateBranch(rec, req)

	require.Equal(t, http.StatusConflict, rec.Code, "body: %s", rec.Body.String())
	var body map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, "branch_slug_taken", body["error"])
}
