// Package payment wires the payment module via uber-go/fx.
package payment

import (
	"context"
	"fmt"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/payment/domain"
	"onlinemenu.tr/internal/modules/payment/fiscal/tokenx"
	paymenthttp "onlinemenu.tr/internal/modules/payment/http"
	pub "onlinemenu.tr/internal/modules/payment/public"
	"onlinemenu.tr/internal/modules/payment/repo"
	"onlinemenu.tr/internal/modules/payment/service"
	"onlinemenu.tr/internal/platform/db"
)

// FiscalConfig selects and configures the fiscal device adapter. It is
// provided from cmd/api/main.go (the only place os.Getenv is allowed).
//
// DeviceType is the process-wide default adapter. Per-branch selection via
// branch_settings.fiscal_device_type lands together with the admin UI; the
// submission rows already record adapter_type, so that switch is additive.
type FiscalConfig struct {
	DeviceType    string // "mock" (default) | tokenx.DeviceType
	WebhookSecret string
	TokenX        tokenx.Config
}

// Module is the fx module definition for the payment domain.
var Module = fx.Module("payment",
	fx.Provide(
		repo.NewPaymentRepo,
		repo.NewFiscalSubmissionRepo,
		repo.NewFiscalStatusRepo,
		repo.NewFiscalTerminalDirectory,
		repo.NewFiscalSectionDirectory,
		repo.NewFiscalAdminRepo,
		newFiscalAdapter,
		service.NewPaymentService,
		fx.Annotate(func(s *service.PaymentService) domain.FiscalResultSink { return s },
			fx.As(new(domain.FiscalResultSink))),
		service.NewSubmissionWorker,
		service.NewReconciler,
		newTokenXWebhookHandler,
		paymenthttp.NewHandler,
		paymenthttp.NewFiscalHandler,
		fx.Annotate(newSaleReader, fx.As(new(pub.SaleReader))),
	),
	fx.Invoke(func(h *paymenthttp.HandlerWithCache, fh *paymenthttp.FiscalHandler, wh *paymenthttp.TokenXWebhookHandler, r *chi.Mux) {
		h.RegisterRoutes(r)
		fh.RegisterRoutes(r)
		wh.RegisterRoutes(r)
	}),
	fx.Invoke(registerSubmissionWorker),
	fx.Invoke(registerReconciler),
)

// newFiscalAdapter is the adapter factory (ADR-FISCAL-002 §4).
func newFiscalAdapter(cfg FiscalConfig, terminals *repo.FiscalTerminalDirectory, sections *repo.FiscalSectionDirectory) (domain.FiscalDeviceAdapter, error) {
	switch cfg.DeviceType {
	case "", "mock":
		// ADR-FISCAL-001: 'none' does not exist as an adapter — registration is
		// always attempted, and the mock is the only no-op implementation.
		return domain.MockFiscalAdapter{}, nil
	case tokenx.DeviceType:
		adapter, err := tokenx.New(cfg.TokenX, sections, terminals)
		if err != nil {
			return nil, fmt.Errorf("payment: build tokenx adapter: %w", err)
		}
		return adapter, nil
	default:
		return nil, fmt.Errorf("payment: unknown fiscal device type %q", cfg.DeviceType)
	}
}

func newTokenXWebhookHandler(pool *db.Pool, subs *repo.FiscalSubmissionRepo, sink domain.FiscalResultSink, cfg FiscalConfig, logger *zap.Logger) *paymenthttp.TokenXWebhookHandler {
	return paymenthttp.NewTokenXWebhookHandler(pool, subs, sink, cfg.WebhookSecret, logger)
}

// registerSubmissionWorker ties the polling worker to the fx lifecycle.
// Shutdown order note: this module's hooks start BEFORE registerHTTPServer's
// (module order in cmd/api/main.go), so fx's reverse-order stop drains HTTP
// first and stops the worker last — the desired sequence. Reordering the
// modules in main.go would silently invert this.
func registerSubmissionWorker(lc fx.Lifecycle, w *service.SubmissionWorker, logger *zap.Logger) {
	registerLoop(lc, logger, "payment: fiscal submission worker", w.Run)
}

// registerReconciler runs the stale-submission sweep (ADR-FISCAL-002): it
// surfaces submissions whose webhook never arrived and expires those past the
// vendor basket TTL, so a lost result can never strand a payment silently.
func registerReconciler(lc fx.Lifecycle, r *service.Reconciler, logger *zap.Logger) {
	registerLoop(lc, logger, "payment: fiscal reconciler", r.Run)
}

// registerLoop attaches a cancellable polling loop to the fx lifecycle and
// blocks OnStop until the loop observes cancellation and exits.
func registerLoop(lc fx.Lifecycle, logger *zap.Logger, name string, run func(context.Context)) {
	var cancel context.CancelFunc
	done := make(chan struct{})
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error { //nolint:contextcheck // runCtx must outlive fx's short-lived OnStart context — the loop runs for the app's full lifetime and is cancelled explicitly in OnStop below.
			runCtx, c := context.WithCancel(context.Background())
			cancel = c
			go func() {
				defer close(done)
				run(runCtx)
			}()
			logger.Info(name + " started")
			return nil
		},
		OnStop: func(stopCtx context.Context) error {
			if cancel != nil {
				cancel()
			}
			select {
			case <-done:
			case <-stopCtx.Done():
				logger.Warn(name + " stop deadline exceeded")
			}
			logger.Info(name + " stopped")
			return nil
		},
	})
}

// saleReaderAdapter adapts PaymentService to the pub.SaleReader interface.
type saleReaderAdapter struct{ svc *service.PaymentService }

func newSaleReader(svc *service.PaymentService) *saleReaderAdapter {
	return &saleReaderAdapter{svc: svc}
}

func (a *saleReaderAdapter) TotalPaidForCheck(ctx context.Context, tenantID, checkID uuid.UUID) (int64, error) {
	return a.svc.TotalPaidForCheck(ctx, tenantID, checkID)
}

func (a *saleReaderAdapter) PendingTotalForCheck(ctx context.Context, tenantID, checkID uuid.UUID) (int64, error) {
	return a.svc.PendingTotalForCheck(ctx, tenantID, checkID)
}
