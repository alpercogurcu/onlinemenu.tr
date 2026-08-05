// Package storefront is the customer-facing QR dine-in surface (ADR-ARCH-006):
// it owns table QR codes, anonymous guest sessions and the binding between a
// guest session and the pos orders it placed.
//
// It reaches the menu and the order machine only through catalog/public and
// pos/public — it never writes a pos row itself. All persistence goes through
// platform/db.WithTenantTx, except the single pre-tenant token lookup, which
// goes through platform/db.WithQRTokenLookupTx.
package storefront

import (
	"github.com/go-chi/chi/v5"
	"go.uber.org/fx"

	storefronthttp "onlinemenu.tr/internal/modules/storefront/http"
	"onlinemenu.tr/internal/modules/storefront/repo"
	"onlinemenu.tr/internal/modules/storefront/service"
)

// Module is the fx module definition for the storefront domain.
//
// The PUBLIC (anonymous QR) and ADMIN (staff QR CRUD) surfaces keep separate
// registrars so that each has its own wiring-audit smoke test (a guard missing
// from one cannot be masked by the other being correct).
var Module = fx.Module("storefront",
	fx.Provide(
		repo.NewQRCodeRepo,
		repo.NewGuestOrderRepo,
		service.NewQRService,
		service.NewSessionService,
		service.NewMenuService,
		service.NewOrderService,
		storefronthttp.NewPublicHandler,
		storefronthttp.NewAdminHandler,
	),
	fx.Invoke(func(h *storefronthttp.PublicHandlerWithCache, r *chi.Mux) {
		h.RegisterPublicRoutes(r)
	}),
	fx.Invoke(func(h *storefronthttp.AdminHandler, r *chi.Mux) {
		h.RegisterRoutes(r)
	}),
)
