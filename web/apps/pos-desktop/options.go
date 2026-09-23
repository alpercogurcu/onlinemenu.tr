package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"onlinemenu.tr/pos-desktop/internal/apiclient"
)

// optionsCacheTTL bounds how long the option tree is served from memory. It is
// short enough that a price change made in the admin panel shows up within a
// couple of minutes, long enough that tapping through categories never waits
// on the network (docs/pos-ux-spec.md §3a "Veri saklama kararı"). The server
// re-validates the price at order time, so a stale cache can cost a rejected
// order, never a wrong charge.
const optionsCacheTTL = 2 * time.Minute

// optionsAPI is the slice of apiclient.Client the resolver needs.
type optionsAPI interface {
	ListProductOptions(ctx context.Context, branchID string) ([]apiclient.ProductOptions, error)
	GetProduct(ctx context.Context, productID string) (apiclient.Product, error)
	CurrentBranchID() string
}

type timed[V any] struct {
	value V
	at    time.Time
}

// optionTree is one branch's option groups keyed by product id.
type optionTree map[string][]apiclient.ProductOptionGroup

// optionsResolver turns the catalog's option tree (one request for every
// product: GET /catalog/products/modifier-groups) into ready-to-render
// ModifierGroupDTOs, cached per branch.
//
// It fails soft: when the tree cannot be read every product is marked
// OptionsUnavailable instead of failing the whole product list, and a refresh
// failure serves the previous (stale) tree rather than downgrading products to
// option-less — a wrong-but-recent option list beats a silently dropped one.
type optionsResolver struct {
	api optionsAPI
	now func() time.Time

	mu       sync.Mutex
	trees    map[string]timed[optionTree]
	products map[string]timed[apiclient.Product]
}

func newOptionsResolver(api optionsAPI) *optionsResolver {
	return &optionsResolver{
		api:      api,
		now:      time.Now,
		trees:    map[string]timed[optionTree]{},
		products: map[string]timed[apiclient.Product]{},
	}
}

// reset drops every cached entry (logout / tenant switch). The maps are
// cleared in place, never reassigned, so in-flight lookups stay race-free.
func (r *optionsResolver) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	clear(r.trees)
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

// enrich fills ModifierGroups / OptionsUnavailable on every product in place.
func (r *optionsResolver) enrich(ctx context.Context, products []ProductDTO) {
	branchID := r.api.CurrentBranchID()
	tree, err := fetchCached(r, r.trees, branchID, func() (optionTree, error) {
		list, err := r.api.ListProductOptions(ctx, branchID)
		if err != nil {
			return nil, err
		}
		byProduct := make(optionTree, len(list))
		for _, p := range list {
			byProduct[p.ProductID] = p.Groups
		}
		return byProduct, nil
	})

	for i := range products {
		products[i].ModifierGroups = []ModifierGroupDTO{}
		products[i].OptionsUnavailable = false
		if err != nil {
			products[i].OptionsUnavailable = true
			continue
		}
		groups, ok := buildGroups(tree[products[i].ID])
		if !ok {
			products[i].OptionsUnavailable = true
			continue
		}
		products[i].ModifierGroups = groups
	}
}

// buildGroups assembles a product's groups in the server's (assignment)
// order; ok=false means the product cannot be offered with a complete,
// correct option list — a required group has nothing selectable — and must be
// flagged unavailable. An empty optional group is simply left out.
func buildGroups(groups []apiclient.ProductOptionGroup) ([]ModifierGroupDTO, bool) {
	out := make([]ModifierGroupDTO, 0, len(groups))
	for _, g := range groups {
		dto := toGroupDTO(g.ModifierGroup, g.Modifiers)
		if len(dto.Modifiers) == 0 {
			if dto.IsRequired {
				return nil, false
			}
			continue
		}
		out = append(out, dto)
	}
	return out, true
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
	return dto
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
