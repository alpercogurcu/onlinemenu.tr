// Package party wires the party module via uber-go/fx.
package party

import (
	"github.com/go-chi/chi/v5"
	"go.uber.org/fx"

	partyhttp "onlinemenu.tr/internal/modules/party/http"
	"onlinemenu.tr/internal/modules/party/repo"
	"onlinemenu.tr/internal/modules/party/service"
)

// Module is the fx module definition for the party domain.
var Module = fx.Module("party",
	fx.Provide(
		repo.NewPartyRepo,
		service.New,
		partyhttp.New,
	),
	fx.Invoke(func(h *partyhttp.Handler, r *chi.Mux) {
		h.RegisterRoutes(r)
	}),
)
