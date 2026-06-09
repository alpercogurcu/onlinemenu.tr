// Package billing wires the billing module via uber-go/fx.
package billing

import (
	"github.com/go-chi/chi/v5"
	"go.uber.org/fx"

	"onlinemenu.tr/internal/modules/billing/adapter/mock"
	"onlinemenu.tr/internal/modules/billing/domain"
	billinghttp "onlinemenu.tr/internal/modules/billing/http"
	"onlinemenu.tr/internal/modules/billing/repo"
	"onlinemenu.tr/internal/modules/billing/service"
)

// Module is the fx module definition for the billing domain.
var Module = fx.Module("billing",
	fx.Provide(
		repo.NewInvoiceRepo,
		newBillingAdapter,
		service.New,
		billinghttp.New,
	),
	fx.Invoke(func(h *billinghttp.HandlerWithCache, r *chi.Mux) {
		h.RegisterRoutes(r)
	}),
)

// newBillingAdapter returns the active BillingAdapter.
// Faz 1: defaults to MockAdapter.
// To use EDM in production, replace this with:
//
//	edm.New(edm.Config{Endpoint: ..., CredentialsFn: ...}, redisClient)
func newBillingAdapter() domain.BillingAdapter {
	return mock.New()
}
