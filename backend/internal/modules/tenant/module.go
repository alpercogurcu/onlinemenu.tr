// Package tenant manages tenant lifecycle, branch configuration, and module enablement.
// All persistence goes through platform/db.WithTenantTx; direct pool access is forbidden.
package tenant

import (
	"context"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/fx"

	tenanthttp "onlinemenu.tr/internal/modules/tenant/http"
	pub "onlinemenu.tr/internal/modules/tenant/public"
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
		// Adapter exposes Service through the public interface consumed by
		// other modules (e.g. identity_service — R2 branch existence check).
		fx.Annotate(newTenantReader, fx.As(new(pub.TenantReader))),
	),
	// Mount routes onto the shared chi.Mux provided by cmd/api/main.go.
	fx.Invoke(func(h *tenanthttp.Handler, r *chi.Mux) {
		h.RegisterRoutes(r)
	}),
)

// tenantReaderAdapter delegates to Service, satisfying pub.TenantReader —
// mirrors the identity module's personReaderAdapter/membershipResolverAdapter
// pattern for exposing a service through its narrower public contract.
type tenantReaderAdapter struct{ svc *service.Service }

func newTenantReader(svc *service.Service) *tenantReaderAdapter {
	return &tenantReaderAdapter{svc: svc}
}

func (a *tenantReaderAdapter) GetByID(ctx context.Context, tenantID uuid.UUID) (pub.Tenant, error) {
	return a.svc.GetByID(ctx, tenantID)
}

func (a *tenantReaderAdapter) GetBranch(ctx context.Context, tenantID, branchID uuid.UUID) (pub.Branch, error) {
	return a.svc.GetBranch(ctx, tenantID, branchID)
}

func (a *tenantReaderAdapter) IsModuleEnabled(ctx context.Context, tenantID uuid.UUID, module string) (bool, error) {
	return a.svc.IsModuleEnabled(ctx, tenantID, module)
}

func (a *tenantReaderAdapter) GetEffectiveIntegrator(ctx context.Context, tenantID, branchID uuid.UUID, provider pub.BillingProvider) (pub.BillingIntegrator, error) {
	return a.svc.GetEffectiveIntegrator(ctx, tenantID, branchID, provider)
}
