package http

import "github.com/go-chi/chi/v5"

// RegisterRoutes mounts all tenant HTTP routes onto the provided router.
// Faz 1: back-office tenant configuration is manager-only (see
// configs/opa/bundles/authz.rego) — no other seeded system role has tenant.* grants.
func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Route("/tenants/{tenantID}", func(r chi.Router) {
		// tenantAccessMiddleware enforces that the JWT's TenantID matches the path param.
		r.Use(h.tenantAccessMiddleware)

		r.With(h.permit("tenant.tenant.read")).Get("/", h.GetTenant)
		r.With(h.permit("tenant.tenant.update")).Put("/", h.UpdateTenant)

		r.Route("/branches", func(r chi.Router) {
			r.With(h.permit("tenant.branch.read")).Get("/", h.ListBranches)
			r.With(h.permit("tenant.branch.create")).Post("/", h.CreateBranch)

			r.Route("/{branchID}", func(r chi.Router) {
				// branchAccessMiddleware enforces that the principal has access to this branch
				// AND that the branch actually belongs to the path tenant. It is applied
				// per-route, after h.permit, rather than once via r.Use: OPA's cheap
				// role/permission check should reject an unauthorized caller before we pay
				// for the DB round trip branchAccessMiddleware needs to verify branch
				// ownership, and mounting it inline keeps chi.Walk-based wiring-audit tests
				// (authz_smoke_test.go) exercising the permission check for every route.

				r.With(h.permit("tenant.branch.read"), h.branchAccessMiddleware).Get("/", h.GetBranch)

				r.Route("/documents", func(r chi.Router) {
					r.With(h.permit("tenant.branch_document.read"), h.branchAccessMiddleware).Get("/", h.ListBranchDocuments)
					r.With(h.permit("tenant.branch_document.create"), h.branchAccessMiddleware).Post("/", h.CreateBranchDocument)
					r.With(h.permit("tenant.branch_document.update"), h.branchAccessMiddleware).Patch("/{docID}/status", h.UpdateBranchDocumentStatus)
					r.With(h.permit("tenant.branch_document.delete"), h.branchAccessMiddleware).Delete("/{docID}", h.DeleteBranchDocument)
				})

				r.Route("/hours", func(r chi.Router) {
					r.With(h.permit("tenant.hours.read"), h.branchAccessMiddleware).Get("/regular", h.GetRegularHours)
					r.With(h.permit("tenant.hours.update"), h.branchAccessMiddleware).Put("/regular", h.SetRegularHours)
					r.With(h.permit("tenant.hours.read"), h.branchAccessMiddleware).Get("/special", h.GetSpecialHours)
					r.With(h.permit("tenant.hours.update"), h.branchAccessMiddleware).Put("/special", h.UpsertSpecialHours)
					r.With(h.permit("tenant.hours.delete"), h.branchAccessMiddleware).Delete("/special/{date}", h.DeleteSpecialHours)
				})
			})
		})

		r.Route("/documents", func(r chi.Router) {
			r.With(h.permit("tenant.document.read")).Get("/", h.ListDocuments)
			r.With(h.permit("tenant.document.create")).Post("/", h.CreateDocument)
			r.With(h.permit("tenant.document.read")).Get("/{docID}", h.GetDocument)
			r.With(h.permit("tenant.document.update")).Patch("/{docID}/status", h.UpdateDocumentStatus)
			r.With(h.permit("tenant.document.delete")).Delete("/{docID}", h.DeleteDocument)
		})

		r.Route("/integrators", func(r chi.Router) {
			r.With(h.permit("tenant.integrator.read")).Get("/", h.ListIntegrators)
			r.With(h.permit("tenant.integrator.create")).Post("/", h.CreateIntegrator)
			r.With(h.permit("tenant.integrator.update")).Put("/{integratorID}", h.UpdateIntegrator)
			r.With(h.permit("tenant.integrator.delete")).Delete("/{integratorID}", h.DeleteIntegrator)
		})
	})
}
