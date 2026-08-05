package service

import (
	"context"
	"fmt"

	"go.uber.org/fx"
	"go.uber.org/zap"

	catalogpub "onlinemenu.tr/internal/modules/catalog/public"
	"onlinemenu.tr/internal/modules/storefront/domain"
	"onlinemenu.tr/internal/platform/auth"
)

// MenuService serves the diner-facing menu.
//
// It holds no *db.Pool on purpose: the storefront owns no catalog table, and
// reaching one directly would break module isolation. Everything comes
// through catalog/public's narrow read model, whose types have no field for
// cost, stock or supplier — so this service could not leak one even if a
// future handler asked it to.
type MenuService struct {
	catalog catalogpub.StorefrontMenuReader
	logger  *zap.Logger
}

// MenuParams groups fx-injected dependencies.
type MenuParams struct {
	fx.In

	Catalog catalogpub.StorefrontMenuReader
	Logger  *zap.Logger
}

func NewMenuService(p MenuParams) *MenuService {
	return &MenuService{catalog: p.Catalog, logger: p.Logger}
}

// GetMenu returns the menu of the branch the guest's QR code belongs to.
//
// Tenant and branch come from the signed session, never from the request: a
// diner cannot ask for another branch's (or another tenant's) menu, because
// there is no parameter through which to ask.
func (s *MenuService) GetMenu(ctx context.Context, guest auth.GuestSession) ([]domain.GuestCategory, error) {
	categories, err := s.catalog.GetStorefrontMenu(ctx, guest.TenantID, guest.BranchID)
	if err != nil {
		return nil, fmt.Errorf("storefront/service/menu: get menu: %w", err)
	}

	out := make([]domain.GuestCategory, 0, len(categories))
	for _, c := range categories {
		out = append(out, domain.GuestCategory{
			ID:        c.ID,
			Name:      c.Name,
			SortOrder: c.SortOrder,
			Products:  toGuestProducts(c.Products),
		})
	}
	return out, nil
}

func toGuestProducts(products []catalogpub.StorefrontProduct) []domain.GuestProduct {
	out := make([]domain.GuestProduct, 0, len(products))
	for _, p := range products {
		allergens := p.Allergens
		if allergens == nil {
			allergens = []string{}
		}
		out = append(out, domain.GuestProduct{
			ID:             p.ID,
			Name:           p.Name,
			Description:    p.Description,
			PriceAmount:    p.PriceAmount,
			Currency:       p.Currency,
			ImageKey:       p.ImageKey,
			Allergens:      allergens,
			IsAvailable:    p.IsAvailable,
			ModifierGroups: toGuestModifierGroups(p.ModifierGroups),
		})
	}
	return out
}

func toGuestModifierGroups(groups []catalogpub.StorefrontModifierGroup) []domain.GuestModifierGroup {
	out := make([]domain.GuestModifierGroup, 0, len(groups))
	for _, g := range groups {
		modifiers := make([]domain.GuestModifier, 0, len(g.Modifiers))
		for _, m := range g.Modifiers {
			modifiers = append(modifiers, domain.GuestModifier{
				ID:         m.ID,
				Name:       m.Name,
				PriceDelta: m.PriceDelta,
			})
		}
		out = append(out, domain.GuestModifierGroup{
			ID:            g.ID,
			Name:          g.Name,
			SelectionType: g.SelectionType,
			MinSelect:     g.MinSelect,
			MaxSelect:     g.MaxSelect,
			Modifiers:     modifiers,
		})
	}
	return out
}
