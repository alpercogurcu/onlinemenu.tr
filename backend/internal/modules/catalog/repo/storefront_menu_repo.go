package repo

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// StorefrontMenuRepo serves the anonymous diner read model (ADR-ARCH-006 §6).
//
// Every query here lists its columns one by one into a narrow row struct.
// `SELECT *` and the wide domain structs are banned on this path: products
// carries stock_quantity and auto_close_on_zero_stock, and a widening
// projection is exactly how such a column reaches a public response. The row
// types below have no field for cost, stock, supplier or internal notes, so
// there is nowhere for one to land.
type StorefrontMenuRepo struct{}

func NewStorefrontMenuRepo() *StorefrontMenuRepo { return &StorefrontMenuRepo{} }

// StorefrontMenuRow is one (category, product) pair of the guest menu.
// Grouping into categories is the service's job — see MenuService.
type StorefrontMenuRow struct {
	CategoryID        uuid.UUID
	CategoryName      string
	CategorySortOrder int16
	ProductID         uuid.UUID
	ProductName       string
	Description       string
	PriceAmount       int64
	Currency          string
	ImageKey          string
	IsAvailable       bool
}

// StorefrontModifierRow is one (product, group, modifier) triple. Rows arrive
// grouped by product and group so the caller can fold them without sorting.
// MaxSelect 0 means "no upper bound" (modifier_groups.max_selections IS NULL).
type StorefrontModifierRow struct {
	ProductID     uuid.UUID
	GroupID       uuid.UUID
	GroupName     string
	SelectionType string
	MinSelect     int
	MaxSelect     int
	ModifierID    uuid.UUID
	ModifierName  string
	PriceDelta    int64
}

// PricedProduct is the authoritative unit price and tax rate of one product,
// read from the same model the diner browsed.
type PricedProduct struct {
	ID          uuid.UUID
	Name        string
	PriceAmount int64
	Currency    string
	TaxRateBPS  int
}

// ProductModifier pairs a modifier with a product it is legitimately attached
// to, so a caller can verify a submitted (product, modifier) pair instead of
// trusting the client's claim that they belong together.
//
// The group's selection rules travel with it because they are the only thing
// bounding how many options one line may carry: price_delta is signed, so an
// unbounded selection is a way to move a line's price anywhere the caller
// likes. MaxSelect 0 means "no explicit upper bound" (max_selections IS NULL).
type ProductModifier struct {
	ProductID     uuid.UUID
	ModifierID    uuid.UUID
	Name          string
	PriceDelta    int64
	GroupID       uuid.UUID
	GroupName     string
	SelectionType string
	MaxSelect     int
}

// storefrontMenuItemsCTE is the single definition of "which price does this
// branch's diner see for this product".
//
// A branch can match several active menus at once (its own plus every
// tenant-wide menu, all within their validity window), so the same product
// may carry more than one price_override. DISTINCT ON picks one deterministic
// winner — branch-specific menu first, then menu sort_order, then menu id as
// a stable tiebreaker.
//
// Both ListMenuRows and PriceProducts embed this identical CTE, and that is
// the point: if browsing and re-pricing resolved the winner differently, the
// server would charge a price the diner was never shown, and no per-endpoint
// test would notice.
//
// $1 = branch_id.
const storefrontMenuItemsCTE = `
	WITH visible_items AS (
	    SELECT DISTINCT ON (mi.product_id)
	           mi.product_id,
	           COALESCE(mi.price_override, p.price_amount) AS price_amount
	    FROM menu_items mi
	    JOIN menus    m ON m.id = mi.menu_id
	    JOIN products p ON p.id = mi.product_id
	    WHERE mi.is_active
	      AND m.is_active
	      AND (m.branch_id = $1 OR m.branch_id IS NULL)
	      AND (m.valid_from  IS NULL OR m.valid_from  <= CURRENT_DATE)
	      AND (m.valid_until IS NULL OR m.valid_until >= CURRENT_DATE)
	      AND p.is_active
	    ORDER BY mi.product_id, (m.branch_id IS NOT NULL) DESC, m.sort_order, m.id
	)`

// dineInAvailabilityJoin resolves product_channel_availability with OPT-OUT
// semantics: a product is available unless an explicit dine_in row says
// otherwise.
//
// This deviates from the plan's literal "dine_in AND is_available" inner
// join, deliberately: the table has no rows in any current environment, so an
// inner join would render an empty menu everywhere, e2e included. Only an
// explicit is_available = FALSE hides a product.
const dineInAvailabilityJoin = `
	LEFT JOIN product_channel_availability pca
	       ON pca.product_id = p.id
	      AND pca.order_channel = 'dine_in'
	      AND pca.integrator_slug IS NULL`

// uncategorisedID is the synthetic category a product falls into when its
// category was deleted (products.category_id ON DELETE SET NULL) or is
// inactive. Such products are still sellable; dropping them would hide items
// from the menu and look like a catalog bug from the floor.
const uncategorisedID = `'00000000-0000-0000-0000-000000000000'::uuid`

// ListMenuRows returns the branch's menu as flat (category, product) rows,
// ordered so that a single pass can fold them into contiguous categories.
func (r *StorefrontMenuRepo) ListMenuRows(ctx context.Context, tx pgx.Tx, branchID uuid.UUID) ([]StorefrontMenuRow, error) {
	const q = storefrontMenuItemsCTE + `
		SELECT COALESCE(c.id, ` + uncategorisedID + `),
		       COALESCE(c.name, ''),
		       COALESCE(c.sort_order, 32767),
		       p.id,
		       p.name,
		       COALESCE(p.description, ''),
		       vi.price_amount,
		       p.currency,
		       COALESCE(p.image_key, ''),
		       (pca.is_available IS DISTINCT FROM FALSE)
		FROM visible_items vi
		JOIN products p ON p.id = vi.product_id
		LEFT JOIN categories c ON c.id = p.category_id AND c.is_active` +
		dineInAvailabilityJoin + `
		ORDER BY COALESCE(c.sort_order, 32767),
		         COALESCE(c.name, ''),
		         COALESCE(c.id, ` + uncategorisedID + `),
		         p.sort_order, p.name`

	rows, err := tx.Query(ctx, q, branchID)
	if err != nil {
		return nil, fmt.Errorf("catalog/repo/storefront_menu: list menu rows: %w", err)
	}
	defer rows.Close()

	var out []StorefrontMenuRow
	for rows.Next() {
		var row StorefrontMenuRow
		if err := rows.Scan(
			&row.CategoryID, &row.CategoryName, &row.CategorySortOrder,
			&row.ProductID, &row.ProductName, &row.Description,
			&row.PriceAmount, &row.Currency, &row.ImageKey, &row.IsAvailable,
		); err != nil {
			return nil, fmt.Errorf("catalog/repo/storefront_menu: list menu rows scan: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// ListModifierRows loads every listed product's active modifiers in ONE query
// (never one per product), ordered by product then group then modifier.
func (r *StorefrontMenuRepo) ListModifierRows(ctx context.Context, tx pgx.Tx, productIDs []uuid.UUID) ([]StorefrontModifierRow, error) {
	if len(productIDs) == 0 {
		return nil, nil
	}

	const q = `
		SELECT pmg.product_id,
		       g.id, g.name, g.selection_type, g.min_selections, COALESCE(g.max_selections, 0),
		       mo.id, mo.name, mo.price_delta
		FROM product_modifier_groups pmg
		JOIN modifier_groups g  ON g.id = pmg.group_id
		JOIN modifiers       mo ON mo.group_id = g.id AND mo.is_active
		WHERE pmg.product_id = ANY($1::uuid[])
		ORDER BY pmg.product_id, pmg.sort_order, g.sort_order, g.id, mo.sort_order, mo.name`

	rows, err := tx.Query(ctx, q, uuidStringSlice(productIDs))
	if err != nil {
		return nil, fmt.Errorf("catalog/repo/storefront_menu: list modifier rows: %w", err)
	}
	defer rows.Close()

	var out []StorefrontModifierRow
	for rows.Next() {
		var row StorefrontModifierRow
		if err := rows.Scan(
			&row.ProductID,
			&row.GroupID, &row.GroupName, &row.SelectionType, &row.MinSelect, &row.MaxSelect,
			&row.ModifierID, &row.ModifierName, &row.PriceDelta,
		); err != nil {
			return nil, fmt.Errorf("catalog/repo/storefront_menu: list modifier rows scan: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// PriceProducts returns the effective price of each requested product that is
// actually on the branch's menu AND open for dine_in.
//
// A requested product missing from the result is the caller's signal to
// reject the line: unknown id, another tenant's product (RLS already made it
// invisible), a product no active menu carries, or one explicitly closed for
// dine_in are all indistinguishable here — deliberately, since the diner gets
// the same "bu ürün şu anda sipariş edilemiyor" either way.
func (r *StorefrontMenuRepo) PriceProducts(ctx context.Context, tx pgx.Tx, branchID uuid.UUID, productIDs []uuid.UUID) (map[uuid.UUID]PricedProduct, error) {
	out := make(map[uuid.UUID]PricedProduct, len(productIDs))
	if len(productIDs) == 0 {
		return out, nil
	}

	const q = storefrontMenuItemsCTE + `
		SELECT p.id, p.name, vi.price_amount, p.currency, p.tax_rate_bps
		FROM visible_items vi
		JOIN products p ON p.id = vi.product_id` +
		dineInAvailabilityJoin + `
		WHERE p.id = ANY($2::uuid[])
		  AND (pca.is_available IS DISTINCT FROM FALSE)`

	rows, err := tx.Query(ctx, q, branchID, uuidStringSlice(productIDs))
	if err != nil {
		return nil, fmt.Errorf("catalog/repo/storefront_menu: price products: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var p PricedProduct
		if err := rows.Scan(&p.ID, &p.Name, &p.PriceAmount, &p.Currency, &p.TaxRateBPS); err != nil {
			return nil, fmt.Errorf("catalog/repo/storefront_menu: price products scan: %w", err)
		}
		out[p.ID] = p
	}
	return out, rows.Err()
}

// PriceModifiers returns the active modifiers among modifierIDs, each paired
// with every product it is attached to. A submitted (product, modifier) pair
// absent from the result is invalid: the modifier does not exist, is
// inactive, or belongs to a group that is not on that product.
func (r *StorefrontMenuRepo) PriceModifiers(ctx context.Context, tx pgx.Tx, productIDs, modifierIDs []uuid.UUID) ([]ProductModifier, error) {
	if len(productIDs) == 0 || len(modifierIDs) == 0 {
		return nil, nil
	}

	const q = `
		SELECT pmg.product_id, mo.id, mo.name, mo.price_delta,
		       g.id, g.name, g.selection_type, COALESCE(g.max_selections, 0)
		FROM modifiers mo
		JOIN modifier_groups         g   ON g.id = mo.group_id
		JOIN product_modifier_groups pmg ON pmg.group_id = g.id
		WHERE mo.is_active
		  AND pmg.product_id = ANY($1::uuid[])
		  AND mo.id = ANY($2::uuid[])`

	rows, err := tx.Query(ctx, q, uuidStringSlice(productIDs), uuidStringSlice(modifierIDs))
	if err != nil {
		return nil, fmt.Errorf("catalog/repo/storefront_menu: price modifiers: %w", err)
	}
	defer rows.Close()

	var out []ProductModifier
	for rows.Next() {
		var pm ProductModifier
		if err := rows.Scan(
			&pm.ProductID, &pm.ModifierID, &pm.Name, &pm.PriceDelta,
			&pm.GroupID, &pm.GroupName, &pm.SelectionType, &pm.MaxSelect,
		); err != nil {
			return nil, fmt.Errorf("catalog/repo/storefront_menu: price modifiers scan: %w", err)
		}
		out = append(out, pm)
	}
	return out, rows.Err()
}

// uuidStringSlice converts ids for an ANY($n::uuid[]) parameter.
//
// The pools run under pgx.QueryExecModeSimpleProtocol (pgBouncer
// transaction-mode safety, ADR-SEC-001/002), which cannot resolve the element
// OID of a non-driver-native slice type and fails []uuid.UUID array params
// with "cannot find encode plan" — the same reason pos
// CheckRepo.TotalsByCheckIDs sends []string.
func uuidStringSlice(ids []uuid.UUID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id.String()
	}
	return out
}
