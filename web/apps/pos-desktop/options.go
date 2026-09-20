package main

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"onlinemenu.tr/pos-desktop/internal/apiclient"
)

// optionsCacheTTL bounds how long a product's option groups are served from
// memory. It is short enough that a price change made in the admin panel shows
// up within a couple of minutes, long enough that tapping through categories
// never waits on the network (docs/pos-ux-spec.md §3a "Veri saklama kararı").
// The server re-validates the price at order time, so a stale cache can cost a
// rejected order, never a wrong charge.
const optionsCacheTTL = 2 * time.Minute

const optionsFetchConcurrency = 6

// optionsAPI is the slice of apiclient.Client the resolver needs.
type optionsAPI interface {
	ListModifierGroups(ctx context.Context) ([]apiclient.ModifierGroup, error)
	ListProductModifierGroupIDs(ctx context.Context, productID string) ([]string, error)
	ListModifiers(ctx context.Context, groupID string) ([]apiclient.Modifier, error)
	GetProduct(ctx context.Context, productID string) (apiclient.Product, error)
}

type timed[V any] struct {
	value V
	at    time.Time
}

// catalogKey is the single key of the group-catalog cache (one list call
// covers every group).
const catalogKey = "all"

// optionsResolver turns the catalog's three-step option lookup (product ->
// group IDs, group IDs -> groups, group -> modifiers) into ready-to-render
// ModifierGroupDTOs, caching every step. The product endpoint returns group
// IDs only, so without the cache a product grid of 30 tiles would fan out into
// dozens of requests on every category switch.
//
// It fails soft: any lookup that cannot be completed marks the product
// OptionsUnavailable instead of failing the whole product list, and a refresh
// failure serves the previous (stale) entry rather than downgrading a product
// to option-less — a wrong-but-recent option list beats a silently dropped one.
type optionsResolver struct {
	api optionsAPI
	now func() time.Time

	mu            sync.Mutex
	productGroups map[string]timed[[]string]
	catalog       map[string]timed[map[string]apiclient.ModifierGroup]
	modifiers     map[string]timed[[]apiclient.Modifier]
	products      map[string]timed[apiclient.Product]
}

func newOptionsResolver(api optionsAPI) *optionsResolver {
	return &optionsResolver{
		api:           api,
		now:           time.Now,
		productGroups: map[string]timed[[]string]{},
		catalog:       map[string]timed[map[string]apiclient.ModifierGroup]{},
		modifiers:     map[string]timed[[]apiclient.Modifier]{},
		products:      map[string]timed[apiclient.Product]{},
	}
}

// reset drops every cached entry (logout / tenant switch). The maps are
// cleared in place, never reassigned, so in-flight lookups stay race-free.
func (r *optionsResolver) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	clear(r.productGroups)
	clear(r.catalog)
	clear(r.modifiers)
	clear(r.products)
}

// rememberProducts caches products that a category listing already returned, so
// paying for an item the cashier just sold costs no extra lookup.
func (r *optionsResolver) rememberProducts(products []apiclient.Product) {
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range products {
		r.products[p.ID] = timed[apiclient.Product]{value: p, at: now}
	}
}

// productMeta returns a product's tax rate, category and unit — what the fiscal
// basket needs and an order item does not carry. Cached like the option groups,
// including serving a stale entry when a refresh fails.
func (r *optionsResolver) productMeta(ctx context.Context, productID string) (apiclient.Product, error) {
	return fetchCached(r, r.products, productID, func() (apiclient.Product, error) {
		return r.api.GetProduct(ctx, productID)
	})
}

func fetchCached[V any](r *optionsResolver, cache map[string]timed[V], key string, fetch func() (V, error)) (V, error) {
	now := r.now()
	r.mu.Lock()
	entry, cached := cache[key]
	r.mu.Unlock()
	if cached && now.Sub(entry.at) < optionsCacheTTL {
		return entry.value, nil
	}

	value, err := fetch()
	if err != nil {
		if cached {
			return entry.value, nil
		}
		var zero V
		return zero, err
	}

	r.mu.Lock()
	cache[key] = timed[V]{value: value, at: now}
	r.mu.Unlock()
	return value, nil
}

type lookup[V any] struct {
	value V
	err   error
}

// enrich fills ModifierGroups / OptionsUnavailable on every product in place.
func (r *optionsResolver) enrich(ctx context.Context, products []ProductDTO) {
	ids := make([]lookup[[]string], len(products))
	forEachBounded(len(products), func(i int) {
		id := products[i].ID
		value, err := fetchCached(r, r.productGroups, id, func() ([]string, error) {
			return r.api.ListProductModifierGroupIDs(ctx, id)
		})
		ids[i] = lookup[[]string]{value: value, err: err}
	})

	var needed []string
	seen := map[string]bool{}
	for _, res := range ids {
		if res.err != nil {
			continue
		}
		for _, id := range res.value {
			if !seen[id] {
				seen[id] = true
				needed = append(needed, id)
			}
		}
	}

	var catalog map[string]apiclient.ModifierGroup
	var catalogErr error
	if len(needed) > 0 {
		catalog, catalogErr = fetchCached(r, r.catalog, catalogKey, func() (map[string]apiclient.ModifierGroup, error) {
			groups, err := r.api.ListModifierGroups(ctx)
			if err != nil {
				return nil, err
			}
			byID := make(map[string]apiclient.ModifierGroup, len(groups))
			for _, g := range groups {
				byID[g.ID] = g
			}
			return byID, nil
		})
	}

	modifiers := map[string]lookup[[]apiclient.Modifier]{}
	if catalogErr == nil {
		var groupIDs []string
		for _, id := range needed {
			if _, ok := catalog[id]; ok {
				groupIDs = append(groupIDs, id)
			}
		}
		fetched := make([]lookup[[]apiclient.Modifier], len(groupIDs))
		forEachBounded(len(groupIDs), func(i int) {
			id := groupIDs[i]
			value, err := fetchCached(r, r.modifiers, id, func() ([]apiclient.Modifier, error) {
				return r.api.ListModifiers(ctx, id)
			})
			fetched[i] = lookup[[]apiclient.Modifier]{value: value, err: err}
		})
		for i, id := range groupIDs {
			modifiers[id] = fetched[i]
		}
	}

	for i := range products {
		products[i].ModifierGroups = []ModifierGroupDTO{}
		products[i].OptionsUnavailable = false

		if ids[i].err != nil {
			products[i].OptionsUnavailable = true
			continue
		}
		if len(ids[i].value) == 0 {
			continue
		}
		if catalogErr != nil {
			products[i].OptionsUnavailable = true
			continue
		}
		groups, ok := buildGroups(ids[i].value, catalog, modifiers)
		if !ok {
			products[i].OptionsUnavailable = true
			continue
		}
		products[i].ModifierGroups = groups
	}
}

// buildGroups assembles a product's groups; ok=false means the product cannot
// be offered with a complete, correct option list (a lookup failed, or a
// required group has nothing selectable) and must be flagged unavailable.
func buildGroups(
	groupIDs []string,
	catalog map[string]apiclient.ModifierGroup,
	modifiers map[string]lookup[[]apiclient.Modifier],
) ([]ModifierGroupDTO, bool) {
	groups := make([]ModifierGroupDTO, 0, len(groupIDs))
	for _, id := range groupIDs {
		g, ok := catalog[id]
		if !ok {
			continue
		}
		mods := modifiers[id]
		if mods.err != nil {
			return nil, false
		}
		dto := toGroupDTO(g, mods.value)
		if len(dto.Modifiers) == 0 {
			if dto.IsRequired {
				return nil, false
			}
			continue
		}
		groups = append(groups, dto)
	}
	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].SortOrder != groups[j].SortOrder {
			return groups[i].SortOrder < groups[j].SortOrder
		}
		return groups[i].Name < groups[j].Name
	})
	return groups, true
}

func toGroupDTO(g apiclient.ModifierGroup, mods []apiclient.Modifier) ModifierGroupDTO {
	dto := ModifierGroupDTO{
		ID:            g.ID,
		Name:          g.Name,
		SelectionType: g.SelectionType,
		MinSelections: int(g.MinSelections),
		IsRequired:    g.IsRequired || g.MinSelections > 0,
		SortOrder:     g.SortOrder,
		Modifiers:     make([]ModifierDTO, 0, len(mods)),
	}
	if g.MaxSelections != nil {
		dto.MaxSelections = int(*g.MaxSelections)
	}
	for _, m := range mods {
		if !m.IsActive {
			continue
		}
		dto.Modifiers = append(dto.Modifiers, ModifierDTO{ID: m.ID, Name: m.Name, PriceDelta: m.PriceDelta, SortOrder: m.SortOrder})
	}
	sort.SliceStable(dto.Modifiers, func(i, j int) bool {
		if dto.Modifiers[i].SortOrder != dto.Modifiers[j].SortOrder {
			return dto.Modifiers[i].SortOrder < dto.Modifiers[j].SortOrder
		}
		return dto.Modifiers[i].Name < dto.Modifiers[j].Name
	})
	return dto
}

func forEachBounded(n int, fn func(i int)) {
	sem := make(chan struct{}, optionsFetchConcurrency)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			fn(i)
		}()
	}
	wg.Wait()
}

// ListProductModifierGroups returns one product's option groups, for retrying
// a product whose ListProducts lookup failed (ProductDTO.OptionsUnavailable).
// A lookup that still cannot be completed is an error — the caller then adds
// the product without options and warns the cashier.
func (a *App) ListProductModifierGroups(productID string) ([]ModifierGroupDTO, error) {
	products := []ProductDTO{{ID: productID}}
	a.optionsResolver().enrich(a.ctx, products)
	if products[0].OptionsUnavailable {
		return nil, fmt.Errorf("ürün seçenekleri alınamadı")
	}
	return products[0].ModifierGroups, nil
}

func (a *App) optionsResolver() *optionsResolver {
	a.optionsOnce.Do(func() { a.options = newOptionsResolver(a.api) })
	return a.options
}

// sellableProducts drops deactivated products. GET /catalog/categories/{id}/products
// returns them too, while POST /pos/orders refuses to sell them
// (invalid_order_line) — without this filter the product grid offers tiles that
// can only fail. Found by the live smoke (task pos:test:live).
func sellableProducts(products []apiclient.Product) []apiclient.Product {
	out := make([]apiclient.Product, 0, len(products))
	for _, p := range products {
		if p.IsActive {
			out = append(out, p)
		}
	}
	return out
}
