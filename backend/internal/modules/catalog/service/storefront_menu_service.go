package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/fx"
	"go.uber.org/zap"

	pub "onlinemenu.tr/internal/modules/catalog/public"
	"onlinemenu.tr/internal/modules/catalog/repo"
	"onlinemenu.tr/internal/platform/db"
)

// StorefrontMenuService implements pub.StorefrontMenuReader: the catalog's
// only door for the anonymous QR surface (ADR-ARCH-006 §6).
//
// It is a separate service from MenuService rather than three more methods on
// it, because the two answer different questions with different blast radius:
// MenuService serves authenticated staff the full menu administration model,
// this one serves the public a projection that must never widen.
type StorefrontMenuService struct {
	db     *db.Pool
	repo   *repo.StorefrontMenuRepo
	logger *zap.Logger
}

// StorefrontMenuParams groups fx-injected dependencies.
type StorefrontMenuParams struct {
	fx.In

	DB     *db.Pool
	Repo   *repo.StorefrontMenuRepo
	Logger *zap.Logger
}

func NewStorefrontMenuService(p StorefrontMenuParams) *StorefrontMenuService {
	return &StorefrontMenuService{db: p.DB, repo: p.Repo, logger: p.Logger}
}

// GetStorefrontMenu returns the branch's menu, categories in display order.
//
// Menu rows and modifier rows are read in ONE transaction so the diner cannot
// be shown a product priced from one catalog snapshot with options from
// another.
func (s *StorefrontMenuService) GetStorefrontMenu(ctx context.Context, tenantID, branchID uuid.UUID) ([]pub.StorefrontCategory, error) {
	var (
		menuRows     []repo.StorefrontMenuRow
		modifierRows []repo.StorefrontModifierRow
	)
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		menuRows, err = s.repo.ListMenuRows(ctx, tx, branchID)
		if err != nil {
			return err
		}
		productIDs := make([]uuid.UUID, 0, len(menuRows))
		for _, row := range menuRows {
			productIDs = append(productIDs, row.ProductID)
		}
		modifierRows, err = s.repo.ListModifierRows(ctx, tx, productIDs)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("catalog/service/storefront_menu: get menu: %w", err)
	}
	return groupMenu(menuRows, modifierRows), nil
}

// groupMenu folds flat rows into the nested read model. It relies on the
// repo's ORDER BY (categories contiguous, products already sorted within a
// category) rather than re-sorting, so display order has exactly one owner.
func groupMenu(menuRows []repo.StorefrontMenuRow, modifierRows []repo.StorefrontModifierRow) []pub.StorefrontCategory {
	groupsByProduct := make(map[uuid.UUID][]pub.StorefrontModifierGroup)
	for _, row := range modifierRows {
		groups := groupsByProduct[row.ProductID]
		if n := len(groups); n == 0 || groups[n-1].ID != row.GroupID {
			groups = append(groups, pub.StorefrontModifierGroup{
				ID:            row.GroupID,
				Name:          row.GroupName,
				SelectionType: row.SelectionType,
				MinSelect:     row.MinSelect,
				MaxSelect:     row.MaxSelect,
			})
		}
		last := &groups[len(groups)-1]
		last.Modifiers = append(last.Modifiers, pub.StorefrontModifier{
			ID:         row.ModifierID,
			Name:       row.ModifierName,
			PriceDelta: row.PriceDelta,
		})
		groupsByProduct[row.ProductID] = groups
	}

	categories := make([]pub.StorefrontCategory, 0, len(menuRows))
	for _, row := range menuRows {
		if n := len(categories); n == 0 || categories[n-1].ID != row.CategoryID {
			categories = append(categories, pub.StorefrontCategory{
				ID:        row.CategoryID,
				Name:      row.CategoryName,
				SortOrder: row.CategorySortOrder,
			})
		}
		last := &categories[len(categories)-1]
		last.Products = append(last.Products, pub.StorefrontProduct{
			ID:             row.ProductID,
			Name:           row.ProductName,
			Description:    row.Description,
			PriceAmount:    row.PriceAmount,
			Currency:       row.Currency,
			ImageKey:       row.ImageKey,
			Allergens:      []string{},
			IsAvailable:    row.IsAvailable,
			ModifierGroups: groupsByProduct[row.ProductID],
		})
	}
	return categories
}

// groupSelectionLimit is how many options one line may take from a group.
//
// A "single" group is capped at one even when max_selections is NULL — that
// is what the type means, and leaving it uncapped would make the strictest
// group shape the least enforced one. 0 means unbounded (a "multiple" group
// with no max_selections).
//
// min_selections is deliberately NOT enforced here: it is a UI completeness
// rule, and rejecting an order server-side for a missing optional-looking
// choice would turn a tenant's stale catalog configuration into a diner who
// cannot order at all. See the WP2 report's known gaps.
func groupSelectionLimit(m repo.ProductModifier) int {
	if m.SelectionType == "single" {
		return 1
	}
	return m.MaxSelect
}

// PriceCart re-derives every line's price server-side.
//
// The caller's cart carries no prices at all (pub.CartLine has no price
// field), so there is nothing here to "validate" against a client claim —
// the number simply comes from the same read model GetStorefrontMenu serves.
// A line naming a product the branch does not currently sell, or a modifier
// not attached to that product, is rejected as a ValidationError (422): the
// alternative — dropping the line — would let a diner receive a cheaper order
// than the one they submitted.
func (s *StorefrontMenuService) PriceCart(ctx context.Context, tenantID, branchID uuid.UUID, lines []pub.CartLine) ([]pub.PricedLine, error) {
	if len(lines) == 0 {
		return nil, &pub.ValidationError{Msg: "cart is empty"}
	}

	productIDs := make([]uuid.UUID, 0, len(lines))
	modifierIDs := make([]uuid.UUID, 0)
	for _, line := range lines {
		if line.Quantity < 1 {
			return nil, &pub.ValidationError{Msg: "cart line quantity must be positive"}
		}
		productIDs = append(productIDs, line.ProductID)
		modifierIDs = append(modifierIDs, line.ModifierIDs...)
	}

	var (
		priced    map[uuid.UUID]repo.PricedProduct
		modifiers []repo.ProductModifier
	)
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		priced, err = s.repo.PriceProducts(ctx, tx, branchID, productIDs)
		if err != nil {
			return err
		}
		modifiers, err = s.repo.PriceModifiers(ctx, tx, productIDs, modifierIDs)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("catalog/service/storefront_menu: price cart: %w", err)
	}

	// Keyed on the PAIR, not on the modifier id: a modifier that exists but
	// belongs to another product must not price this line.
	type pair struct{ product, modifier uuid.UUID }
	byPair := make(map[pair]repo.ProductModifier, len(modifiers))
	for _, m := range modifiers {
		byPair[pair{m.ProductID, m.ModifierID}] = m
	}

	out := make([]pub.PricedLine, 0, len(lines))
	for _, line := range lines {
		product, ok := priced[line.ProductID]
		if !ok {
			return nil, &pub.ValidationError{Msg: "product is not orderable: " + line.ProductID.String()}
		}

		pl := pub.PricedLine{
			ProductID:       product.ID,
			ProductName:     product.Name,
			BasePriceAmount: product.PriceAmount,
			UnitPriceAmount: product.PriceAmount,
			Currency:        product.Currency,
			TaxRateBPS:      product.TaxRateBPS,
			Quantity:        line.Quantity,
		}
		// Repetition is rejected, not deduped: modifiers.price_delta is
		// signed by design ("negatif olabilir"), so sending one negative
		// modifier thirty times would otherwise walk a line's price down to
		// zero — and the same trick with a positive delta would overcharge a
		// diner whose client double-submitted. Refusing keeps the response an
		// honest answer to what was actually sent.
		seen := make(map[uuid.UUID]struct{}, len(line.ModifierIDs))
		perGroup := make(map[uuid.UUID]int)
		for _, modifierID := range line.ModifierIDs {
			if _, dup := seen[modifierID]; dup {
				return nil, &pub.ValidationError{Msg: "modifier selected more than once: " + modifierID.String()}
			}
			seen[modifierID] = struct{}{}

			m, ok := byPair[pair{line.ProductID, modifierID}]
			if !ok {
				return nil, &pub.ValidationError{Msg: "modifier is not available for this product: " + modifierID.String()}
			}

			// A group's own selection rule is the only server-side bound on
			// how far a line's price can be moved by stacking options: a
			// "single" group means one choice, whatever the client's UI did.
			perGroup[m.GroupID]++
			if limit := groupSelectionLimit(m); limit > 0 && perGroup[m.GroupID] > limit {
				return nil, &pub.ValidationError{Msg: "too many options selected for group: " + m.GroupName}
			}

			// The delta lands in the unit price because pos bills a line as
			// quantity × unit_price_amount (CheckRepo.GetTotal); a delta kept
			// anywhere else would never be charged.
			pl.UnitPriceAmount += m.PriceDelta
			pl.Modifiers = append(pl.Modifiers, pub.PricedModifier{
				ID:         m.ModifierID,
				Name:       m.Name,
				PriceDelta: m.PriceDelta,
			})
		}
		if pl.UnitPriceAmount < 0 {
			// Negative deltas are legal per-modifier (modifiers.price_delta
			// has no non-negative CHECK), but a negative LINE would bill the
			// restaurant. Refusing beats silently clamping to zero.
			return nil, &pub.ValidationError{Msg: "modifier selection yields a negative price"}
		}
		if pl.Currency == "" {
			pl.Currency = "TRY"
		}
		out = append(out, pl)
	}
	return out, nil
}
