// Package payment wires the payment module via uber-go/fx.
package payment

import (
	"context"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/fx"

	"onlinemenu.tr/internal/modules/payment/domain"
	paymenthttp "onlinemenu.tr/internal/modules/payment/http"
	pub "onlinemenu.tr/internal/modules/payment/public"
	"onlinemenu.tr/internal/modules/payment/repo"
	"onlinemenu.tr/internal/modules/payment/service"
)

// Module is the fx module definition for the payment domain.
var Module = fx.Module("payment",
	fx.Provide(
		repo.NewPaymentRepo,
		newFiscalAdapter,
		service.NewPaymentService,
		paymenthttp.NewHandler,
		fx.Annotate(newSaleReader, fx.As(new(pub.SaleReader))),
	),
	fx.Invoke(func(h *paymenthttp.HandlerWithCache, r *chi.Mux) {
		h.RegisterRoutes(r)
	}),
)

func newFiscalAdapter() domain.FiscalDeviceAdapter {
	return domain.MockFiscalAdapter{}
}

// saleReaderAdapter adapts PaymentService to the pub.SaleReader interface.
type saleReaderAdapter struct{ svc *service.PaymentService }

func newSaleReader(svc *service.PaymentService) *saleReaderAdapter {
	return &saleReaderAdapter{svc: svc}
}

func (a *saleReaderAdapter) TotalPaidForCheck(ctx context.Context, tenantID, checkID uuid.UUID) (int64, error) {
	return a.svc.TotalPaidForCheck(ctx, tenantID, checkID)
}
