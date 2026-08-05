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
	"go.uber.org/fx"

	"onlinemenu.tr/internal/modules/storefront/repo"
	"onlinemenu.tr/internal/modules/storefront/service"
)

// Module is the fx module definition for the storefront domain.
//
// No fx.Invoke route registration yet: the HTTP surface (public guest routes
// and admin QR CRUD) lands in WP2/WP3. Wiring the module now keeps the fx
// graph honest — a missing dependency fails at startup, not later.
var Module = fx.Module("storefront",
	fx.Provide(
		repo.NewQRCodeRepo,
		repo.NewGuestOrderRepo,
		service.NewQRService,
		service.NewSessionService,
	),
)
