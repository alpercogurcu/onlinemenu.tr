// Package hrcore wires the hr-core module via uber-go/fx.
package hrcore

import (
	"github.com/go-chi/chi/v5"
	"go.uber.org/fx"

	hrhttp "onlinemenu.tr/internal/modules/hr-core/http"
	"onlinemenu.tr/internal/modules/hr-core/repo"
	"onlinemenu.tr/internal/modules/hr-core/service"
)

// Module is the fx module definition for the hr-core domain.
var Module = fx.Module("hr-core",
	fx.Provide(
		repo.NewEmployeeRepo,
		service.New,
		hrhttp.New,
	),
	fx.Invoke(func(h *hrhttp.Handler, r *chi.Mux) {
		h.RegisterRoutes(r)
	}),
)
