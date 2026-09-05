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
	posws "onlinemenu.tr/internal/modules/pos/ws"
	"onlinemenu.tr/internal/platform/auth"
)

// Module is the fx module definition for the POS domain.
var Module = fx.Module("pos",
	fx.Provide(
		repo.NewCheckRepo,
		repo.NewOrderRepo,
		repo.NewTableRepo,
		repo.NewReportRepo,
		service.NewCheckService,
		service.NewOrderService,
		service.NewTableService,
		service.NewReportService,
		poshttp.NewHandler,
		posws.NewHub,
		fx.Annotate(newCheckReader, fx.As(new(pub.CheckReader))),
		// Guest (QR) entry points — ADR-ARCH-006 §8. Three narrow interfaces
		// instead of one wide one: the storefront's session guard needs only
		// the table read, its polling endpoint only the order read, and only
		// the cart submission may write.
		newGuestOrderPlacer,
		newGuestOrderReader,
		newGuestTableReader,
	),
	fx.Invoke(func(h *poshttp.HandlerWithCache, r *chi.Mux) {
		h.RegisterRoutes(r)
	}),
	fx.Invoke(func(hub *posws.Hub, r *chi.Mux, engine *auth.Engine, lc fx.Lifecycle) {
		hub.RegisterRoutes(r, engine)
		hub.Register(lc)
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

// guestOrderAdapter satisfies the guest-facing pub interfaces using
// OrderService. It exists as a distinct type so a cross-module consumer can
// only ever reach the three guest methods, never the principal-taking staff
// ones on the same service.
type guestOrderAdapter struct{ svc *service.OrderService }

func newGuestOrderPlacer(svc *service.OrderService) pub.GuestOrderPlacer {
	return &guestOrderAdapter{svc: svc}
}

func newGuestOrderReader(svc *service.OrderService) pub.GuestOrderReader {
	return &guestOrderAdapter{svc: svc}
}

func newGuestTableReader(svc *service.OrderService) pub.GuestTableReader {
	return &guestOrderAdapter{svc: svc}
}

func (a *guestOrderAdapter) PlaceGuestOrder(ctx context.Context, req pub.GuestOrderRequest, link pub.GuestOrderLinker) (pub.GuestOrderResult, error) {
	return a.svc.PlaceGuest(ctx, req, link)
}

func (a *guestOrderAdapter) GetGuestOrder(ctx context.Context, tenantID, orderID uuid.UUID) (pub.GuestOrderView, error) {
	return a.svc.GetGuestOrder(ctx, tenantID, orderID)
}

func (a *guestOrderAdapter) GetGuestTable(ctx context.Context, tenantID, tableID uuid.UUID) (pub.GuestTable, error) {
	return a.svc.GetGuestTable(ctx, tenantID, tableID)
}
