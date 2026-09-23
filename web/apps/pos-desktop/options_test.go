package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"onlinemenu.tr/pos-desktop/internal/apiclient"
)

// fakeOptionsAPI serves a tiny option tree and counts calls so cache behaviour
// is observable.
type fakeOptionsAPI struct {
	mu sync.Mutex

	branch         string
	tree           []apiclient.ProductOptions
	treeErr        error
	treeCalls      map[string]int
	products       map[string]apiclient.Product
	productFetches map[string]int
	productErr     map[string]error
}

func newFakeOptionsAPI() *fakeOptionsAPI {
	return &fakeOptionsAPI{
		treeCalls:      map[string]int{},
		products:       map[string]apiclient.Product{},
		productFetches: map[string]int{},
		productErr:     map[string]error{},
	}
}

func (f *fakeOptionsAPI) ListProductOptions(_ context.Context, branchID string) ([]apiclient.ProductOptions, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.treeCalls[branchID]++
	return f.tree, f.treeErr
}

func (f *fakeOptionsAPI) CurrentBranchID() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.branch
}

func (f *fakeOptionsAPI) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	total := 0
	for _, n := range f.treeCalls {
		total += n
	}
	return total
}

func (f *fakeOptionsAPI) GetProduct(_ context.Context, productID string) (apiclient.Product, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.productFetches[productID]++
	if err := f.productErr[productID]; err != nil {
		return apiclient.Product{}, err
	}
	p, ok := f.products[productID]
	if !ok {
		return apiclient.Product{}, errors.New("not found")
	}
	return p, nil
}

func int16Ptr(v int16) *int16 { return &v }

// Groups arrive in the server's assignment order with active options only
// (the apiclient marks them IsActive) — spice first, then extra.
func spiceGroup() apiclient.ProductOptionGroup {
	return apiclient.ProductOptionGroup{
		ModifierGroup: apiclient.ModifierGroup{ID: "g-spice", Name: "Acı", SelectionType: "single", MinSelections: 1, IsRequired: true, SortOrder: 5},
		Modifiers: []apiclient.Modifier{
			{ID: "m-mild", GroupID: "g-spice", Name: "Acısız", IsActive: true, SortOrder: 1},
			{ID: "m-hot", GroupID: "g-spice", Name: "Acılı", IsActive: true, SortOrder: 2},
		},
	}
}

func extraGroup() apiclient.ProductOptionGroup {
	return apiclient.ProductOptionGroup{
		ModifierGroup: apiclient.ModifierGroup{ID: "g-extra", Name: "Ekstra", SelectionType: "multiple", MaxSelections: int16Ptr(2), SortOrder: 1},
		Modifiers:     []apiclient.Modifier{{ID: "m-cheese", GroupID: "g-extra", Name: "Peynir", PriceDelta: 1500, IsActive: true, SortOrder: 1}},
	}
}

func newResolverWithClock(api optionsAPI, now *time.Time) *optionsResolver {
	r := newOptionsResolver(api)
	r.now = func() time.Time { return *now }
	return r
}

func TestOptionsResolver_Enrich_AttachesGroupsInServerOrder(t *testing.T) {
	api := newFakeOptionsAPI()
	api.tree = []apiclient.ProductOptions{{ProductID: "lahmacun", Groups: []apiclient.ProductOptionGroup{spiceGroup(), extraGroup()}}}

	products := []ProductDTO{{ID: "lahmacun", Name: "Lahmacun"}, {ID: "cay", Name: "Çay"}}
	newOptionsResolver(api).enrich(context.Background(), products)

	lahmacun := products[0]
	if lahmacun.OptionsUnavailable || len(lahmacun.ModifierGroups) != 2 {
		t.Fatalf("lahmacun = %+v, want two groups", lahmacun)
	}
	if got := lahmacun.ModifierGroups[0].ID; got != "g-spice" {
		t.Fatalf("groups[0] = %q, want g-spice (assignment order from the server, not group sort_order)", got)
	}
	spice := lahmacun.ModifierGroups[0]
	if !spice.IsRequired || spice.SelectionType != "single" || spice.MinSelections != 1 {
		t.Fatalf("spice group rules = %+v", spice)
	}
	if len(spice.Modifiers) != 2 || spice.Modifiers[0].ID != "m-mild" || spice.Modifiers[1].ID != "m-hot" {
		t.Fatalf("spice modifiers = %+v", spice.Modifiers)
	}
	extra := lahmacun.ModifierGroups[1]
	if extra.MaxSelections != 2 || extra.Modifiers[0].PriceDelta != 1500 {
		t.Fatalf("extra group = %+v", extra)
	}

	cay := products[1]
	if len(cay.ModifierGroups) != 0 || cay.OptionsUnavailable {
		t.Fatalf("cay = %+v, want plain product with no options", cay)
	}
	if cay.ModifierGroups == nil {
		t.Fatal("cay.ModifierGroups is nil — must be an empty slice so the frontend sees [] not null")
	}
}

func TestOptionsResolver_Enrich_OneRequestForAWholeGrid(t *testing.T) {
	api := newFakeOptionsAPI()
	api.tree = []apiclient.ProductOptions{{ProductID: "p0", Groups: []apiclient.ProductOptionGroup{spiceGroup()}}}
	products := make([]ProductDTO, 30)
	for i := range products {
		products[i] = ProductDTO{ID: fmt.Sprintf("p%d", i)}
	}
	newOptionsResolver(api).enrich(context.Background(), products)
	if got := api.calls(); got != 1 {
		t.Fatalf("option requests for a 30-tile grid = %d, want 1 (production rate-limits per IP)", got)
	}
}

func TestOptionsResolver_Enrich_AsksForTheSessionBranchAndCachesPerBranch(t *testing.T) {
	api := newFakeOptionsAPI()
	api.branch = "b-1"
	r := newOptionsResolver(api)
	r.enrich(context.Background(), []ProductDTO{{ID: "p"}})
	r.enrich(context.Background(), []ProductDTO{{ID: "p"}})
	api.branch = "b-2"
	r.enrich(context.Background(), []ProductDTO{{ID: "p"}})
	if api.treeCalls["b-1"] != 1 || api.treeCalls["b-2"] != 1 {
		t.Fatalf("tree calls = %v, want one per branch", api.treeCalls)
	}
}

func TestOptionsResolver_Enrich_DropsEmptyOptionalGroupButFlagsEmptyRequiredGroup(t *testing.T) {
	api := newFakeOptionsAPI()
	optional := apiclient.ProductOptionGroup{ModifierGroup: apiclient.ModifierGroup{ID: "g-opt", Name: "Sos", SelectionType: "multiple"}, Modifiers: []apiclient.Modifier{}}
	required := apiclient.ProductOptionGroup{ModifierGroup: apiclient.ModifierGroup{ID: "g-req", Name: "Boy", SelectionType: "single", IsRequired: true}, Modifiers: []apiclient.Modifier{}}
	api.tree = []apiclient.ProductOptions{
		{ProductID: "a", Groups: []apiclient.ProductOptionGroup{optional}},
		{ProductID: "b", Groups: []apiclient.ProductOptionGroup{required}},
	}

	products := []ProductDTO{{ID: "a"}, {ID: "b"}}
	newOptionsResolver(api).enrich(context.Background(), products)

	if len(products[0].ModifierGroups) != 0 || products[0].OptionsUnavailable {
		t.Fatalf("product with only an empty optional group = %+v, want plain product", products[0])
	}
	if !products[1].OptionsUnavailable {
		t.Fatal("product whose required group has no active modifier must be flagged unavailable — it cannot be ordered correctly")
	}
}

func TestOptionsResolver_Enrich_TreeFailureFlagsEveryProduct(t *testing.T) {
	api := newFakeOptionsAPI()
	api.treeErr = errors.New("boom")

	products := []ProductDTO{{ID: "with"}, {ID: "without"}}
	newOptionsResolver(api).enrich(context.Background(), products)

	for _, p := range products {
		if !p.OptionsUnavailable || p.ModifierGroups == nil {
			t.Fatalf("product = %+v, want flagged (options unknown) with an empty group list", p)
		}
	}
}

func TestOptionsResolver_Enrich_CachesWithinTTL(t *testing.T) {
	api := newFakeOptionsAPI()
	api.tree = []apiclient.ProductOptions{{ProductID: "p", Groups: []apiclient.ProductOptionGroup{spiceGroup()}}}

	now := time.Now()
	r := newResolverWithClock(api, &now)
	for i := 0; i < 3; i++ {
		r.enrich(context.Background(), []ProductDTO{{ID: "p"}})
	}
	if got := api.calls(); got != 1 {
		t.Fatalf("tree calls = %d, want 1 (tile taps must not wait on the network)", got)
	}

	now = now.Add(optionsCacheTTL + time.Second)
	r.enrich(context.Background(), []ProductDTO{{ID: "p"}})
	if got := api.calls(); got != 2 {
		t.Fatalf("after TTL tree calls = %d, want 2", got)
	}
}

func TestOptionsResolver_Enrich_ServesStaleCacheWhenRefreshFails(t *testing.T) {
	api := newFakeOptionsAPI()
	api.tree = []apiclient.ProductOptions{{ProductID: "p", Groups: []apiclient.ProductOptionGroup{spiceGroup()}}}

	now := time.Now()
	r := newResolverWithClock(api, &now)
	r.enrich(context.Background(), []ProductDTO{{ID: "p"}})

	now = now.Add(optionsCacheTTL + time.Second)
	api.treeErr = errors.New("offline")

	products := []ProductDTO{{ID: "p"}}
	r.enrich(context.Background(), products)
	if products[0].OptionsUnavailable || len(products[0].ModifierGroups) != 1 {
		t.Fatalf("product = %+v, want the stale options rather than a downgrade to option-less", products[0])
	}
}

func TestOptionsResolver_Reset_DropsCache(t *testing.T) {
	api := newFakeOptionsAPI()
	r := newOptionsResolver(api)
	r.enrich(context.Background(), []ProductDTO{{ID: "p"}})
	r.reset()
	r.enrich(context.Background(), []ProductDTO{{ID: "p"}})
	if got := api.calls(); got != 2 {
		t.Fatalf("tree calls = %d, want 2 after reset", got)
	}
}

func TestOptionsResolver_ProductMeta_FetchesOnceAndServesFromCache(t *testing.T) {
	api := newFakeOptionsAPI()
	api.products["p1"] = apiclient.Product{ID: "p1", CategoryID: "cat", TaxRateBPS: 1000, Unit: "adet"}

	r := newOptionsResolver(api)
	for i := 0; i < 3; i++ {
		got, err := r.productMeta(context.Background(), "p1")
		if err != nil || got.CategoryID != "cat" {
			t.Fatalf("productMeta = %+v, %v", got, err)
		}
	}
	if api.productFetches["p1"] != 1 {
		t.Fatalf("fetches = %d, want 1", api.productFetches["p1"])
	}
}

func TestOptionsResolver_RememberProducts_SavesTheFetchForProductsAlreadyListed(t *testing.T) {
	api := newFakeOptionsAPI()
	r := newOptionsResolver(api)
	r.rememberProducts([]apiclient.Product{{ID: "p1", CategoryID: "cat", TaxRateBPS: 2000, Unit: "lt"}})

	got, err := r.productMeta(context.Background(), "p1")
	if err != nil || got.TaxRateBPS != 2000 {
		t.Fatalf("productMeta = %+v, %v", got, err)
	}
	if api.productFetches["p1"] != 0 {
		t.Fatalf("fetches = %d, want 0 — the category listing already carried this product", api.productFetches["p1"])
	}
}

func TestOptionsResolver_ProductMeta_ServesStaleWhenRefreshFails(t *testing.T) {
	api := newFakeOptionsAPI()
	api.products["p1"] = apiclient.Product{ID: "p1", TaxRateBPS: 1000}
	now := time.Now()
	r := newResolverWithClock(api, &now)
	if _, err := r.productMeta(context.Background(), "p1"); err != nil {
		t.Fatalf("first fetch: %v", err)
	}

	now = now.Add(optionsCacheTTL + time.Second)
	api.productErr["p1"] = errors.New("offline")
	got, err := r.productMeta(context.Background(), "p1")
	if err != nil || got.TaxRateBPS != 1000 {
		t.Fatalf("productMeta = %+v, %v; want the stale entry", got, err)
	}
}

func TestOptionsResolver_ProductMeta_UnknownProductFails(t *testing.T) {
	if _, err := newOptionsResolver(newFakeOptionsAPI()).productMeta(context.Background(), "nope"); err == nil {
		t.Fatal("expected an error for a product the catalog does not know")
	}
}

func TestSellableProducts_DropsDeactivatedOnes(t *testing.T) {
	got := sellableProducts([]apiclient.Product{
		{ID: "a", Name: "Aktif", IsActive: true},
		{ID: "b", Name: "Pasif", IsActive: false},
		{ID: "c", Name: "Aktif 2", IsActive: true},
	})
	if len(got) != 2 || got[0].ID != "a" || got[1].ID != "c" {
		t.Fatalf("sellable = %+v, want the two active products in order", got)
	}
	if got := sellableProducts(nil); got == nil || len(got) != 0 {
		t.Fatalf("no products must give an empty (non-nil) list, got %v", got)
	}
}
