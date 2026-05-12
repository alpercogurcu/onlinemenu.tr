// Package tenant manages tenant lifecycle, branch configuration, and module enablement.
// All persistence goes through platform/db.WithTenantTx; direct pool access is forbidden.
package tenant

import (
	"github.com/go-chi/chi/v5"
	"go.uber.org/fx"

	tenanthttp "onlinemenu.tr/internal/modules/tenant/http"
	"onlinemenu.tr/internal/modules/tenant/repo"
	"onlinemenu.tr/internal/modules/tenant/service"
)

// Module is the fx module definition for the tenant domain.
var Module = fx.Module("tenant",
	fx.Provide(
		repo.NewTenantRepo,
		repo.NewBranchRepo,
		repo.NewDocumentRepo,
		repo.NewIntegratorRepo,
		repo.NewHoursRepo,
		service.NewService,
		tenanthttp.NewHandler,
	),
	// Mount routes onto the shared chi.Mux provided by cmd/api/main.go.
	fx.Invoke(func(h *tenanthttp.Handler, r *chi.Mux) {
		h.RegisterRoutes(r)
	}),
)
