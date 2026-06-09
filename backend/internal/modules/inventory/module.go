// Package inventory manages branch-level stock: levels (current state) and
// transactions (immutable ledger). Tenant-wide stock lives in catalog.products.
// All persistence goes through platform/db.WithTenantTx; direct pool access is forbidden.
package inventory

import (
	"github.com/go-chi/chi/v5"
	"go.uber.org/fx"

	inventoryhttp "onlinemenu.tr/internal/modules/inventory/http"
	pub "onlinemenu.tr/internal/modules/inventory/public"
	"onlinemenu.tr/internal/modules/inventory/repo"
	"onlinemenu.tr/internal/modules/inventory/service"
)

// Module is the fx module definition for the inventory domain.
var Module = fx.Module("inventory",
	fx.Provide(
		repo.NewInventoryLevelRepo,
		repo.NewInventoryTransactionRepo,
		service.NewInventoryService,
		inventoryhttp.NewHandler,
		fx.Annotate(service.NewStockReader, fx.As(new(pub.StockReader))),
	),
	fx.Invoke(func(h *inventoryhttp.Handler, r *chi.Mux) {
		h.RegisterRoutes(r)
	}),
)
