package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"onlinemenu.tr/pos-desktop/internal/apiclient"
)

// fakeOptionsAPI serves a tiny catalog and counts calls so cache behaviour is
// observable.
type fakeOptionsAPI struct {
	mu sync.Mutex

	products        map[string]apiclient.Product
	productFetches  map[string]int
	groups          []apiclient.ModifierGroup
	productGroupIDs map[string][]string
	modifiers       map[string][]apiclient.Modifier

	groupsErr     error
	productErr    map[string]error
	modifiersErr  map[string]error
	groupsCalls   int
	productCalls  map[string]int
	modifierCalls map[string]int
}

func newFakeOptionsAPI() *fakeOptionsAPI {
	return &fakeOptionsAPI{
		products:        map[string]apiclient.Product{},
		productFetches:  map[string]int{},
		productGroupIDs: map[string][]string{},
		modifiers:       map[string][]apiclient.Modifier{},
		productErr:      map[string]error{},
		modifiersErr:    map[string]error{},
		productCalls:    map[string]int{},
		modifierCalls:   map[string]int{},
	}
}

func (f *fakeOptionsAPI) ListModifierGroups(context.Context) ([]apiclient.ModifierGroup, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.groupsCalls++
	return f.groups, f.groupsErr
}

func (f *fakeOptionsAPI) ListProductModifierGroupIDs(_ context.Context, productID string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.productCalls[productID]++
	if err := f.productErr[productID]; err != nil {
		return nil, err
	}
	return f.productGroupIDs[productID], nil
}

func (f *fakeOptionsAPI) ListModifiers(_ context.Context, groupID string) ([]apiclient.Modifier, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.modifierCalls[groupID]++
	if err := f.modifiersErr[groupID]; err != nil {
		return nil, err
	}
	return f.modifiers[groupID], nil
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

func spiceGroup() (apiclient.ModifierGroup, []apiclient.Modifier) {
	return apiclient.ModifierGroup{ID: "g-spice", Name: "Acı", SelectionType: "single", MinSelections: 1, IsRequired: true, SortOrder: 1},
		[]apiclient.Modifier{
			{ID: "m-hot", GroupID: "g-spice", Name: "Acılı", PriceDelta: 0, IsActive: true, SortOrder: 2},
			{ID: "m-mild", GroupID: "g-spice", Name: "Acısız", PriceDelta: 0, IsActive: true, SortOrder: 1},
			{ID: "m-old", GroupID: "g-spice", Name: "Kaldırıldı", PriceDelta: 0, IsActive: false, SortOrder: 3},
		}
}

func extraGroup() (apiclient.ModifierGroup, []apiclient.Modifier) {
	return apiclient.ModifierGroup{ID: "g-extra", Name: "Ekstra", SelectionType: "multiple", MaxSelections: int16Ptr(2), SortOrder: 2},
		[]apiclient.Modifier{
			{ID: "m-cheese", GroupID: "g-extra", Name: "Peynir", PriceDelta: 1500, IsActive: true, SortOrder: 1},
		}
}

func newResolverWithClock(api optionsAPI, now *time.Time) *optionsResolver {
	r := newOptionsResolver(api)
	r.now = func() time.Time { return *now }
	return r
}

func TestOptionsResolver_Enrich_AttachesSortedActiveModifiers(t *testing.T) {
	api := newFakeOptionsAPI()
	spice, spiceMods := spiceGroup()
	extra, extraMods := extraGroup()
	api.groups = []apiclient.ModifierGroup{extra, spice}
	api.modifiers[spice.ID] = spiceMods
	api.modifiers[extra.ID] = extraMods
	api.productGroupIDs["lahmacun"] = []string{extra.ID, spice.ID}

	products := []ProductDTO{{ID: "lahmacun", Name: "Lahmacun"}, {ID: "cay", Name: "Çay"}}
	newOptionsResolver(api).enrich(context.Background(), products)

	lahmacun := products[0]
	if lahmacun.OptionsUnavailable {
		t.Fatal("lahmacun.OptionsUnavailable = true, want false")
	}
	if len(lahmacun.ModifierGroups) != 2 {
		t.Fatalf("lahmacun has %d groups, want 2", len(lahmacun.ModifierGroups))
	}
	if got := lahmacun.ModifierGroups[0].ID; got != "g-spice" {
		t.Fatalf("groups[0] = %q, want g-spice (sorted by sort_order)", got)
	}
	spiceDTO := lahmacun.ModifierGroups[0]
	if !spiceDTO.IsRequired || spiceDTO.SelectionType != "single" || spiceDTO.MinSelections != 1 {
		t.Fatalf("spice group rules = %+v", spiceDTO)
	}
	if len(spiceDTO.Modifiers) != 2 || spiceDTO.Modifiers[0].ID != "m-mild" || spiceDTO.Modifiers[1].ID != "m-hot" {
		t.Fatalf("spice modifiers = %+v, want active only, sorted [m-mild m-hot]", spiceDTO.Modifiers)
	}
	extraDTO := lahmacun.ModifierGroups[1]
	if extraDTO.MaxSelections != 2 || extraDTO.Modifiers[0].PriceDelta != 1500 {
		t.Fatalf("extra group = %+v", extraDTO)
	}

	cay := products[1]
	if len(cay.ModifierGroups) != 0 || cay.OptionsUnavailable {
		t.Fatalf("cay = %+v, want plain product with no options", cay)
	}
	if cay.ModifierGroups == nil {
		t.Fatal("cay.ModifierGroups is nil — must be an empty slice so the frontend sees [] not null")
	}
}

func TestOptionsResolver_Enrich_NoAssignedGroupsSkipsGroupCatalog(t *testing.T) {
	api := newFakeOptionsAPI()
	products := []ProductDTO{{ID: "cay"}}
	newOptionsResolver(api).enrich(context.Background(), products)
	if api.groupsCalls != 0 {
		t.Fatalf("group catalog fetched %d times for products without groups, want 0", api.groupsCalls)
	}
}

func TestOptionsResolver_Enrich_DropsEmptyOptionalGroupButFlagsEmptyRequiredGroup(t *testing.T) {
	api := newFakeOptionsAPI()
	optional := apiclient.ModifierGroup{ID: "g-opt", Name: "Sos", SelectionType: "multiple", SortOrder: 1}
	required := apiclient.ModifierGroup{ID: "g-req", Name: "Boy", SelectionType: "single", IsRequired: true, SortOrder: 1}
	api.groups = []apiclient.ModifierGroup{optional, required}
	api.modifiers["g-opt"] = []apiclient.Modifier{{ID: "m1", GroupID: "g-opt", Name: "Kapalı", IsActive: false}}
	api.modifiers["g-req"] = nil
	api.productGroupIDs["a"] = []string{"g-opt"}
	api.productGroupIDs["b"] = []string{"g-req"}

	products := []ProductDTO{{ID: "a"}, {ID: "b"}}
	newOptionsResolver(api).enrich(context.Background(), products)

	if len(products[0].ModifierGroups) != 0 || products[0].OptionsUnavailable {
		t.Fatalf("product with only an empty optional group = %+v, want plain product", products[0])
	}
	if !products[1].OptionsUnavailable {
		t.Fatal("product whose required group has no active modifier must be flagged unavailable — it cannot be ordered correctly")
	}
}

func TestOptionsResolver_Enrich_FailSoftPerProduct(t *testing.T) {
	api := newFakeOptionsAPI()
	spice, spiceMods := spiceGroup()
	api.groups = []apiclient.ModifierGroup{spice}
	api.modifiers[spice.ID] = spiceMods
	api.productGroupIDs["ok"] = []string{spice.ID}
	api.productErr["broken"] = errors.New("boom")

	products := []ProductDTO{{ID: "ok"}, {ID: "broken"}}
	newOptionsResolver(api).enrich(context.Background(), products)

	if products[0].OptionsUnavailable || len(products[0].ModifierGroups) != 1 {
		t.Fatalf("healthy product = %+v", products[0])
	}
	if !products[1].OptionsUnavailable || len(products[1].ModifierGroups) != 0 {
		t.Fatalf("broken product = %+v, want unavailable with no groups", products[1])
	}
}

func TestOptionsResolver_Enrich_GroupCatalogFailureFlagsOnlyProductsWithGroups(t *testing.T) {
	api := newFakeOptionsAPI()
	api.groupsErr = errors.New("boom")
	api.productGroupIDs["with"] = []string{"g-spice"}

	products := []ProductDTO{{ID: "with"}, {ID: "without"}}
	newOptionsResolver(api).enrich(context.Background(), products)

	if !products[0].OptionsUnavailable {
		t.Fatal("product with assigned groups must be flagged when the group catalog is unreachable")
	}
	if products[1].OptionsUnavailable {
		t.Fatal("product without groups needs no catalog and must stay plain")
	}
}

func TestOptionsResolver_Enrich_ModifierFailureFlagsProductsUsingThatGroup(t *testing.T) {
	api := newFakeOptionsAPI()
	spice, spiceMods := spiceGroup()
	extra, _ := extraGroup()
	api.groups = []apiclient.ModifierGroup{spice, extra}
	api.modifiers[spice.ID] = spiceMods
	api.modifiersErr[extra.ID] = errors.New("boom")
	api.productGroupIDs["a"] = []string{spice.ID}
	api.productGroupIDs["b"] = []string{spice.ID, extra.ID}

	products := []ProductDTO{{ID: "a"}, {ID: "b"}}
	newOptionsResolver(api).enrich(context.Background(), products)

	if products[0].OptionsUnavailable || len(products[0].ModifierGroups) != 1 {
		t.Fatalf("product a = %+v", products[0])
	}
	if !products[1].OptionsUnavailable {
		t.Fatal("product b uses the failed group and must be flagged: offering only part of its options would silently drop the rest")
	}
}

func TestOptionsResolver_Enrich_CachesWithinTTL(t *testing.T) {
	api := newFakeOptionsAPI()
	spice, spiceMods := spiceGroup()
	api.groups = []apiclient.ModifierGroup{spice}
	api.modifiers[spice.ID] = spiceMods
	api.productGroupIDs["p"] = []string{spice.ID}

	now := time.Now()
	r := newResolverWithClock(api, &now)
	for i := 0; i < 3; i++ {
		r.enrich(context.Background(), []ProductDTO{{ID: "p"}})
	}
	if api.groupsCalls != 1 || api.productCalls["p"] != 1 || api.modifierCalls[spice.ID] != 1 {
		t.Fatalf("calls groups=%d product=%d modifiers=%d, want 1 each (tile taps must not wait on the network)",
			api.groupsCalls, api.productCalls["p"], api.modifierCalls[spice.ID])
	}

	now = now.Add(optionsCacheTTL + time.Second)
	r.enrich(context.Background(), []ProductDTO{{ID: "p"}})
	if api.groupsCalls != 2 || api.productCalls["p"] != 2 || api.modifierCalls[spice.ID] != 2 {
		t.Fatalf("after TTL calls groups=%d product=%d modifiers=%d, want 2 each", api.groupsCalls, api.productCalls["p"], api.modifierCalls[spice.ID])
	}
}

func TestOptionsResolver_Enrich_ServesStaleCacheWhenRefreshFails(t *testing.T) {
	api := newFakeOptionsAPI()
	spice, spiceMods := spiceGroup()
	api.groups = []apiclient.ModifierGroup{spice}
	api.modifiers[spice.ID] = spiceMods
	api.productGroupIDs["p"] = []string{spice.ID}

	now := time.Now()
	r := newResolverWithClock(api, &now)
	r.enrich(context.Background(), []ProductDTO{{ID: "p"}})

	now = now.Add(optionsCacheTTL + time.Second)
	api.groupsErr = errors.New("offline")
	api.productErr["p"] = errors.New("offline")
	api.modifiersErr[spice.ID] = errors.New("offline")

	products := []ProductDTO{{ID: "p"}}
	r.enrich(context.Background(), products)
	if products[0].OptionsUnavailable || len(products[0].ModifierGroups) != 1 {
		t.Fatalf("product = %+v, want the stale options rather than a downgrade to option-less", products[0])
	}
}

func TestOptionsResolver_Reset_DropsCache(t *testing.T) {
	api := newFakeOptionsAPI()
	spice, spiceMods := spiceGroup()
	api.groups = []apiclient.ModifierGroup{spice}
	api.modifiers[spice.ID] = spiceMods
	api.productGroupIDs["p"] = []string{spice.ID}

	r := newOptionsResolver(api)
	r.enrich(context.Background(), []ProductDTO{{ID: "p"}})
	r.reset()
	r.enrich(context.Background(), []ProductDTO{{ID: "p"}})
	if api.groupsCalls != 2 {
		t.Fatalf("groupsCalls = %d, want 2 after reset", api.groupsCalls)
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
