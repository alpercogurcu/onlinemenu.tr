// Package inventory manages warehouse-scoped stock: stock items, warehouses,
// stock levels/movements, shipments and branch transfer orders (ADR-DATA-005,
// ADR-DATA-006). All persistence goes through platform/db.WithTenantTx;
// direct pool access is forbidden.
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
		repo.NewStockLevelRepo,
		repo.NewStockMovementRepo,
		repo.NewStockItemRepo,
		repo.NewWarehouseRepo,
		repo.NewShipmentRepo,
		repo.NewShipmentItemRepo,
		repo.NewTransferOrderRepo,
		repo.NewTransferOrderItemRepo,
		service.NewInventoryService,
		service.NewStockItemService,
		service.NewWarehouseService,
		service.NewTransferOrderService,
		service.NewShipmentService,
		inventoryhttp.NewHandler,
		fx.Annotate(service.NewStockReader, fx.As(new(pub.StockReader))),
	),
	fx.Invoke(func(h *inventoryhttp.Handler, r *chi.Mux) {
		h.RegisterRoutes(r)
	}),
)
