package http_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/inventory/domain"
	"onlinemenu.tr/internal/modules/inventory/repo"
	"onlinemenu.tr/internal/modules/inventory/service"
	"onlinemenu.tr/internal/platform/auth"
)

// findRawByID locates the object in a []json.RawMessage array whose "id"
// field's value equals id.String(), by substring match on the raw bytes.
// A substring match on the id is enough here because id is a UUID (opaque,
// never a JSON-structural character) and each test seeds distinct items.
func findRawByID(t *testing.T, items []json.RawMessage, id uuid.UUID) string {
	t.Helper()
	needle := id.String()
	for _, raw := range items {
		if strings.Contains(string(raw), needle) {
			return string(raw)
		}
	}
	t.Fatalf("no response object found containing id %s", needle)
	return ""
}

// TestListStockItems_VisibilityByResolvedSupplyMode is the ADR-DATA-007 /
// docs/lessons-from-b2b.md end-to-end visibility test: GET
// /api/v1/inventory/stock-items must render an exclusive_hq item using the
// RESTRICTED (BTO-catalog-only) projection for a branch-scoped principal —
// key ABSENCE, not an empty value — while a free/approved_suppliers item
// renders the FULL projection including its resolved supply_mode. A
// tenant-wide-scoped principal (manager) always gets the full projection,
// even for an exclusive_hq item.
func TestListStockItems_VisibilityByResolvedSupplyMode(t *testing.T) {
	tenantID := uuid.New()
	branchID := uuid.New()
	logger := zap.NewNop()

	h := newTestHandler(t)
	mux := chi.NewMux()
	h.RegisterRoutes(mux)

	stockItemSvc := service.NewStockItemService(service.StockItemParams{
		DB: httpSharedPool, Repo: repo.NewStockItemRepo(), SupplyPolicyRepo: repo.NewSupplyPolicyRepo(), Logger: logger,
	})
	policySvc := service.NewSupplyPolicyService(service.SupplyPolicyParams{
		DB: httpSharedPool, Repo: repo.NewSupplyPolicyRepo(), StockRepo: repo.NewStockItemRepo(), Logger: logger,
	})

	// exclusiveItem gets no policy row at all -> resolves to the
	// exclusive_hq DEFAULT (ADR-DATA-007).
	exclusiveItem, err := stockItemSvc.Create(context.Background(), tenantID, service.CreateStockItemRequest{
		SKU: "SKU-EXC-" + uuid.NewString(), Name: "Kofte", Kind: domain.StockItemKindRaw, CanonicalUnit: "kg",
	})
	require.NoError(t, err)

	// freeItem gets an explicit tenant-wide free policy.
	freeItem, err := stockItemSvc.Create(context.Background(), tenantID, service.CreateStockItemRequest{
		SKU: "SKU-FREE-" + uuid.NewString(), Name: "Marul", Kind: domain.StockItemKindRaw, CanonicalUnit: "kg",
	})
	require.NoError(t, err)
	_, err = policySvc.Create(context.Background(), tenantID, service.CreateSupplyPolicyRequest{
		Scope:         domain.SupplyScopeStockItem,
		StockItemID:   &freeItem.ID,
		Mode:          domain.SupplyModeFree,
		EffectiveFrom: time.Now().Add(-time.Hour),
	})
	require.NoError(t, err)

	branchPrincipal := auth.Principal{
		PersonID: uuid.New(), Ctx: auth.ContextStaff, TenantID: tenantID, BranchID: branchID,
		RoleIDs: []uuid.UUID{warehouseRoleID}, // OPA allow=true, scope=branch
	}
	managerPrincipal := auth.Principal{
		PersonID: uuid.New(), Ctx: auth.ContextStaff, TenantID: tenantID, BranchID: branchID,
		RoleIDs: []uuid.UUID{managerRoleID}, // OPA allow=true, scope=tenant
	}

	listAsPrincipal := func(t *testing.T, p auth.Principal) []json.RawMessage {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/inventory/stock-items", nil)
		req = req.WithContext(auth.WithPrincipal(req.Context(), p))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

		var items []json.RawMessage
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &items))
		return items
	}

	t.Run("branch principal: exclusive_hq item is restricted (key absence)", func(t *testing.T) {
		items := listAsPrincipal(t, branchPrincipal)
		exclusiveRaw := findRawByID(t, items, exclusiveItem.ID)

		// Key ABSENCE, not an empty value: none of these keys exist at all
		// in the restricted projection.
		assert.NotContains(t, exclusiveRaw, `"kind"`)
		assert.NotContains(t, exclusiveRaw, `"supply_mode"`)
		assert.NotContains(t, exclusiveRaw, `"is_active"`)
		assert.NotContains(t, exclusiveRaw, `"created_at"`)
		// The BTO catalog fields must still be present.
		assert.Contains(t, exclusiveRaw, `"canonical_unit"`)
		assert.Contains(t, exclusiveRaw, `"sku"`)
	})

	t.Run("branch principal: free item is fully visible with resolved mode", func(t *testing.T) {
		items := listAsPrincipal(t, branchPrincipal)
		freeRaw := findRawByID(t, items, freeItem.ID)

		assert.Contains(t, freeRaw, `"supply_mode":"free"`)
		assert.Contains(t, freeRaw, `"kind"`)
	})

	t.Run("manager (tenant scope) sees full view even for exclusive_hq item", func(t *testing.T) {
		items := listAsPrincipal(t, managerPrincipal)
		exclusiveRaw := findRawByID(t, items, exclusiveItem.ID)

		assert.Contains(t, exclusiveRaw, `"supply_mode":"exclusive_hq"`)
		assert.Contains(t, exclusiveRaw, `"kind"`)
	})

	t.Run("visibility changes when the policy changes", func(t *testing.T) {
		// Before: exclusiveItem is restricted for the branch principal.
		before := findRawByID(t, listAsPrincipal(t, branchPrincipal), exclusiveItem.ID)
		assert.NotContains(t, before, `"supply_mode"`)

		// A new tenant-wide policy row supersedes the "no row -> exclusive_hq
		// default" resolution (DATA-002 immutability: this is a NEW row, not
		// an UPDATE of a previous one -- there was none).
		_, err := policySvc.Create(context.Background(), tenantID, service.CreateSupplyPolicyRequest{
			Scope:               domain.SupplyScopeStockItem,
			StockItemID:         &exclusiveItem.ID,
			Mode:                domain.SupplyModeApprovedSuppliers,
			ApprovedSupplierIDs: []uuid.UUID{uuid.New()},
			EffectiveFrom:       time.Now().Add(-time.Minute),
		})
		require.NoError(t, err)

		after := findRawByID(t, listAsPrincipal(t, branchPrincipal), exclusiveItem.ID)
		assert.Contains(t, after, `"supply_mode":"approved_suppliers"`)
	})
}
