package http

import "github.com/go-chi/chi/v5"

// RegisterRoutes mounts all tenant HTTP routes onto the provided router.
func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Route("/tenants/{tenantID}", func(r chi.Router) {
		// tenantAccessMiddleware enforces that the JWT's TenantID matches the path param.
		r.Use(h.tenantAccessMiddleware)

		r.Get("/", h.GetTenant)
		r.Put("/", h.UpdateTenant)

		r.Route("/branches", func(r chi.Router) {
			r.Get("/", h.ListBranches)
			r.Post("/", h.CreateBranch)

			r.Route("/{branchID}", func(r chi.Router) {
				// branchAccessMiddleware enforces that the principal has access to this branch.
				r.Use(h.branchAccessMiddleware)

				r.Get("/", h.GetBranch)

				r.Route("/documents", func(r chi.Router) {
					r.Get("/", h.ListBranchDocuments)
					r.Post("/", h.CreateBranchDocument)
					r.Patch("/{docID}/status", h.UpdateBranchDocumentStatus)
					r.Delete("/{docID}", h.DeleteBranchDocument)
				})

				r.Route("/hours", func(r chi.Router) {
					r.Get("/regular", h.GetRegularHours)
					r.Put("/regular", h.SetRegularHours)
					r.Get("/special", h.GetSpecialHours)
					r.Put("/special", h.UpsertSpecialHours)
					r.Delete("/special/{date}", h.DeleteSpecialHours)
				})
			})
		})

		r.Route("/documents", func(r chi.Router) {
			r.Get("/", h.ListDocuments)
			r.Post("/", h.CreateDocument)
			r.Get("/{docID}", h.GetDocument)
			r.Patch("/{docID}/status", h.UpdateDocumentStatus)
			r.Delete("/{docID}", h.DeleteDocument)
		})

		r.Route("/integrators", func(r chi.Router) {
			r.Get("/", h.ListIntegrators)
			r.Post("/", h.CreateIntegrator)
			r.Put("/{integratorID}", h.UpdateIntegrator)
			r.Delete("/{integratorID}", h.DeleteIntegrator)
		})
	})
}
