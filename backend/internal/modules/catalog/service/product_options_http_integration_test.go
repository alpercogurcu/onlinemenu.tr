package service_test

// GET /api/v1/catalog/products/modifier-groups: the whole option tree of every
// sellable product in one request, so an order screen stops making one call
// per product (production rate-limits per client IP). Runs the real chain:
// chi route (static segment before /products/{id}), embedded OPA, the branch
// guard, ModifierService and Postgres with RLS.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/catalog/domain"
	cataloghttp "onlinemenu.tr/internal/modules/catalog/http"
	"onlinemenu.tr/internal/modules/catalog/repo"
	"onlinemenu.tr/internal/modules/catalog/service"
	"onlinemenu.tr/internal/platform/auth"
)

var catalogWaiterRoleID = uuid.MustParse("00000001-0000-0000-0000-000000000008")

type optionTree []struct {
	ProductID uuid.UUID `json:"product_id"`
	Groups    []struct {
		ID            uuid.UUID `json:"id"`
		Name          string    `json:"name"`
		SelectionType string    `json:"selection_type"`
		MinSelections int16     `json:"min_selections"`
		MaxSelections *int16    `json:"max_selections"`
		IsRequired    bool      `json:"is_required"`
		Modifiers     []struct {
			ID         uuid.UUID `json:"id"`
			Name       string    `json:"name"`
			PriceDelta int64     `json:"price_delta"`
		} `json:"modifiers"`
	} `json:"groups"`
}

func newModifierService() *service.ModifierService {
	return service.NewModifierService(service.ModifierParams{
		DB:           sharedPool,
		GroupRepo:    repo.NewModifierGroupRepo(),
		ModifierRepo: repo.NewModifierRepo(),
		PMGRepo:      repo.NewProductModifierGroupRepo(),
		Logger:       zap.NewNop(),
	})
}

func productOptionsMux(t *testing.T) *chi.Mux {
	t.Helper()
	engine, err := auth.NewEngine(
		auth.EngineConfig{BundlePath: "../../../../configs/opa/bundles"},
		redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 1}),
		zap.NewNop(),
	)
	require.NoError(t, err)
	h := cataloghttp.NewHandler(cataloghttp.Params{
		Products:        newProductService(),
		BranchOverrides: newBranchOverrideService(),
		Modifiers:       newModifierService(),
		Logger:          zap.NewNop(),
		Engine:          engine,
	})
	mux := chi.NewMux()
	h.RegisterRoutes(mux)
	return mux
}

type optionsFixture struct {
	tenantID  uuid.UUID
	branchA   uuid.UUID
	branchB   uuid.UUID
	kebap     uuid.UUID // two groups, one inactive option
	sis       uuid.UUID // switched off in branch B
	ayran     uuid.UUID // no groups
	pasif     uuid.UUID // inactive product with a group
	lahmacun  uuid.UUID // only group has no active option (returned empty)
	cook      uuid.UUID
	extra     uuid.UUID
	dead      uuid.UUID
	az, orta  uuid.UUID
	sos, eski uuid.UUID
}

func seedOptionsFixture(t *testing.T) optionsFixture {
	t.Helper()
	ctx := context.Background()
	f := optionsFixture{tenantID: uuid.New(), branchA: uuid.New(), branchB: uuid.New()}
	products := repo.NewProductRepo()
	groups := repo.NewModifierGroupRepo()
	modifiers := repo.NewModifierRepo()
	links := repo.NewProductModifierGroupRepo()
	one, two := int16(1), int16(2)

	require.NoError(t, sharedPool.WithTenantTx(ctx, f.tenantID, func(tx pgx.Tx) error {
		product := func(name string, active bool) uuid.UUID {
			p, err := products.Create(ctx, tx, domain.Product{
				TenantID: f.tenantID, Name: name, PriceAmount: 10000, Currency: "TRY",
				Unit: "adet", TaxRateBPS: 1000, IsActive: active,
			})
			require.NoError(t, err)
			return p.ID
		}
		group := func(name string, sel domain.SelectionType, minSel int16, maxSel *int16, required bool, sort int16) uuid.UUID {
			g, err := groups.Create(ctx, tx, domain.ModifierGroup{
				TenantID: f.tenantID, Name: name, SelectionType: sel,
				MinSelections: minSel, MaxSelections: maxSel, IsRequired: required, SortOrder: sort,
			})
			require.NoError(t, err)
			return g.ID
		}
		option := func(groupID uuid.UUID, name string, delta int64, active bool, sort int16) uuid.UUID {
			m, err := modifiers.Create(ctx, tx, domain.Modifier{
				TenantID: f.tenantID, GroupID: groupID, Name: name, PriceDelta: delta, IsActive: active, SortOrder: sort,
			})
			require.NoError(t, err)
			return m.ID
		}
		link := func(productID, groupID uuid.UUID, sort int16) {
			require.NoError(t, links.Assign(ctx, tx, productID, groupID, f.tenantID, sort))
		}

		f.kebap = product("Adana Kebap", true)
		f.sis = product("Tavuk Şiş", true)
		f.ayran = product("Ayran", true)
		f.pasif = product("Eski Kebap", false)
		f.lahmacun = product("Lahmacun", true)

		f.cook = group("Pişirme", domain.SelectionSingle, 1, &one, true, 1)
		f.extra = group("Ekstralar", domain.SelectionMultiple, 0, nil, false, 2)
		f.dead = group("Kaldırılan", domain.SelectionMultiple, 0, &two, false, 3)

		f.orta = option(f.cook, "Orta", 0, true, 2)
		f.az = option(f.cook, "Az pişmiş", 0, true, 1)
		f.sos = option(f.extra, "Ekstra sos", 1500, true, 1)
		f.eski = option(f.extra, "Eski sos", 500, false, 2)
		option(f.dead, "Yok artık", 0, false, 1)

		// Assignment order, not group order, decides what the waiter sees first.
		link(f.kebap, f.extra, 2)
		link(f.kebap, f.cook, 1)
		link(f.sis, f.cook, 1)
		link(f.pasif, f.cook, 1)
		link(f.lahmacun, f.dead, 1)
		return nil
	}))

	_, err := newBranchOverrideService().Upsert(ctx, f.tenantID, domain.BranchProductOverride{
		BranchID: f.branchB, ProductID: f.sis, IsAvailable: false,
	})
	require.NoError(t, err)
	return f
}

func getOptions(t *testing.T, mux *chi.Mux, tenantID, actorBranch, roleID uuid.UUID, query string) (*httptest.ResponseRecorder, optionTree) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/products/modifier-groups"+query, nil)
	rec := serve(mux, asPrincipal(req, tenantID, actorBranch, roleID))
	var tree optionTree
	if rec.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &tree))
	}
	return rec, tree
}

func productIDs(tree optionTree) []uuid.UUID {
	out := make([]uuid.UUID, len(tree))
	for i, p := range tree {
		out[i] = p.ProductID
	}
	return out
}

func TestProductOptions_OneRequestCarriesTheWholeActiveTree(t *testing.T) {
	f := seedOptionsFixture(t)
	mux := productOptionsMux(t)

	rec, tree := getOptions(t, mux, f.tenantID, f.branchA, catalogWaiterRoleID, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	// Only sellable products with a group: no Ayran (no groups), no inactive
	// product. Lahmacun stays, its dead group empty, so the client can tell
	// "required but unanswerable" from "no options".
	assert.ElementsMatch(t, []uuid.UUID{f.kebap, f.sis, f.lahmacun}, productIDs(tree))

	for _, p := range tree {
		if p.ProductID == f.lahmacun {
			require.Len(t, p.Groups, 1)
			assert.Equal(t, f.dead, p.Groups[0].ID)
			assert.NotNil(t, p.Groups[0].Modifiers)
			assert.Empty(t, p.Groups[0].Modifiers)
		}
		if p.ProductID != f.kebap {
			continue
		}
		require.Len(t, p.Groups, 2)
		cook, extra := p.Groups[0], p.Groups[1]
		assert.Equal(t, f.cook, cook.ID, "assignment sort_order first")
		assert.Equal(t, "single", cook.SelectionType)
		assert.True(t, cook.IsRequired)
		require.NotNil(t, cook.MaxSelections)
		assert.EqualValues(t, 1, *cook.MaxSelections)
		require.Len(t, cook.Modifiers, 2)
		assert.Equal(t, f.az, cook.Modifiers[0].ID, "options in their own sort order")
		assert.Equal(t, f.orta, cook.Modifiers[1].ID)

		assert.Equal(t, f.extra, extra.ID)
		assert.Nil(t, extra.MaxSelections, "null = no cap")
		require.Len(t, extra.Modifiers, 1, "inactive option is not offered")
		assert.Equal(t, f.sos, extra.Modifiers[0].ID)
		assert.EqualValues(t, 1500, extra.Modifiers[0].PriceDelta)
	}
}

func TestProductOptions_BranchSwitchedOffProductIsDropped(t *testing.T) {
	f := seedOptionsFixture(t)
	mux := productOptionsMux(t)

	// Chain-wide manager naming branch B explicitly.
	rec, tree := getOptions(t, mux, f.tenantID, uuid.Nil, catalogManagerRoleID, "?branch_id="+f.branchB.String())
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.ElementsMatch(t, []uuid.UUID{f.kebap, f.lahmacun}, productIDs(tree))

	// A branch-B waiter without the parameter lands on its own branch.
	rec, tree = getOptions(t, mux, f.tenantID, f.branchB, catalogWaiterRoleID, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.ElementsMatch(t, []uuid.UUID{f.kebap, f.lahmacun}, productIDs(tree))
}

func TestProductOptions_BranchGuardAndTenantIsolation(t *testing.T) {
	f := seedOptionsFixture(t)
	mux := productOptionsMux(t)

	rec, _ := getOptions(t, mux, f.tenantID, f.branchA, catalogWaiterRoleID, "?branch_id="+f.branchB.String())
	assert.Equal(t, http.StatusForbidden, rec.Code, "a branch-bound waiter may not read another branch")

	rec, tree := getOptions(t, mux, uuid.New(), uuid.Nil, catalogManagerRoleID, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Empty(t, tree, "RLS: another tenant sees nothing of this one")
}
