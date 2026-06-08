// Package catalog manages the tenant's product catalog: categories, products,
// modifier groups, and menu assignments.
// All persistence goes through platform/db.WithTenantTx; direct pool access is forbidden.
package catalog

import (
	"context"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/fx"

	cataloghttp "onlinemenu.tr/internal/modules/catalog/http"
	pub "onlinemenu.tr/internal/modules/catalog/public"
	"onlinemenu.tr/internal/modules/catalog/repo"
	"onlinemenu.tr/internal/modules/catalog/service"
)

// Module is the fx module definition for the catalog domain.
var Module = fx.Module("catalog",
	fx.Provide(
		repo.NewCategoryRepo,
		repo.NewProductRepo,
		repo.NewModifierGroupRepo,
		repo.NewModifierRepo,
		repo.NewProductModifierGroupRepo,
		repo.NewMenuRepo,
		repo.NewMenuItemRepo,
		service.NewCategoryService,
		service.NewProductService,
		service.NewModifierService,
		service.NewMenuService,
		cataloghttp.NewHandler,
		fx.Annotate(newProductReader, fx.As(new(pub.ProductReader))),
	),
	fx.Invoke(func(h *cataloghttp.Handler, r *chi.Mux) {
		h.RegisterRoutes(r)
	}),
)

// productReaderAdapter satisfies pub.ProductReader using ProductService.
type productReaderAdapter struct{ svc *service.ProductService }

func newProductReader(svc *service.ProductService) *productReaderAdapter {
	return &productReaderAdapter{svc: svc}
}

func (a *productReaderAdapter) GetByID(ctx context.Context, tenantID, productID uuid.UUID) (pub.Product, error) {
	p, err := a.svc.GetByID(ctx, tenantID, productID)
	if err != nil {
		return pub.Product{}, err
	}
	return pub.Product{
		ID:          p.ID,
		Name:        p.Name,
		PriceAmount: p.PriceAmount,
		Currency:    p.Currency,
		TaxRateBPS:  p.TaxRateBPS,
		IsActive:    p.IsActive,
	}, nil
}
