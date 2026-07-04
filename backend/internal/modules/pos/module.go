// Package pos wires the POS module via uber-go/fx.
package pos

import (
	"context"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/fx"

	poshttp "onlinemenu.tr/internal/modules/pos/http"
	pub "onlinemenu.tr/internal/modules/pos/public"
	"onlinemenu.tr/internal/modules/pos/repo"
	"onlinemenu.tr/internal/modules/pos/service"
)

// Module is the fx module definition for the POS domain.
var Module = fx.Module("pos",
	fx.Provide(
		repo.NewCheckRepo,
		repo.NewOrderRepo,
		service.NewCheckService,
		service.NewOrderService,
		poshttp.NewHandler,
		fx.Annotate(newCheckReader, fx.As(new(pub.CheckReader))),
	),
	fx.Invoke(func(h *poshttp.HandlerWithCache, r *chi.Mux) {
		h.RegisterRoutes(r)
	}),
)

// checkReaderAdapter satisfies pub.CheckReader using CheckService.
type checkReaderAdapter struct{ svc *service.CheckService }

func newCheckReader(svc *service.CheckService) *checkReaderAdapter {
	return &checkReaderAdapter{svc: svc}
}

func (a *checkReaderAdapter) GetByID(ctx context.Context, tenantID, checkID uuid.UUID) (pub.Check, error) {
	return a.svc.GetPublic(ctx, tenantID, checkID)
}
