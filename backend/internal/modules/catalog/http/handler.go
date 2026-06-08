// Package http provides the HTTP layer for the catalog module.
package http

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/catalog/domain"
	pub "onlinemenu.tr/internal/modules/catalog/public"
	"onlinemenu.tr/internal/modules/catalog/service"
	"onlinemenu.tr/internal/platform/auth"
)

// Handler exposes catalog REST endpoints.
type Handler struct {
	categories *service.CategoryService
	products   *service.ProductService
	logger     *zap.Logger
}

// Params groups fx-injected dependencies for NewHandler.
type Params struct {
	fx.In

	Categories *service.CategoryService
	Products   *service.ProductService
	Logger     *zap.Logger
}

// NewHandler constructs a Handler for fx injection.
func NewHandler(p Params) *Handler {
	return &Handler{
		categories: p.Categories,
		products:   p.Products,
		logger:     p.Logger,
	}
}

// RegisterRoutes mounts catalog endpoints on the provided router.
func (h *Handler) RegisterRoutes(r *chi.Mux) {
	r.Route("/api/v1/catalog", func(r chi.Router) {
		r.Get("/categories", h.listCategories)
		r.Post("/categories", h.createCategory)
		r.Get("/categories/{id}", h.getCategory)

		r.Get("/products", h.listProducts)
		r.Post("/products", h.createProduct)
		r.Get("/products/{id}", h.getProduct)
		r.Put("/products/{id}", h.updateProduct)
		r.Delete("/products/{id}", h.deleteProduct)

		r.Get("/categories/{id}/products", h.listByCategory)
	})
}

// requireTenantID extracts the tenant UUID from the auth principal in the request context.
// Writes a 401 response and returns (uuid.Nil, false) if no valid principal is found.
func requireTenantID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	p, err := auth.FromContext(r.Context())
	if err != nil || p.TenantID == uuid.Nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return uuid.Nil, false
	}
	return p.TenantID, true
}

// ---------------------------------------------------------------------------
// Category handlers
// ---------------------------------------------------------------------------

func (h *Handler) listCategories(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := requireTenantID(w, r)
	if !ok {
		return
	}
	cats, err := h.categories.List(r.Context(), tenantID)
	if err != nil {
		h.error(w, r, err)
		return
	}
	respondJSON(w, http.StatusOK, cats)
}

func (h *Handler) getCategory(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := requireTenantID(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	cat, err := h.categories.GetByID(r.Context(), tenantID, id)
	if err != nil {
		h.error(w, r, err)
		return
	}
	respondJSON(w, http.StatusOK, cat)
}

func (h *Handler) createCategory(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := requireTenantID(w, r)
	if !ok {
		return
	}
	var req struct {
		Name        string     `json:"name"`
		Description string     `json:"description"`
		ParentID    *uuid.UUID `json:"parent_id"`
		SortOrder   int16      `json:"sort_order"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		http.Error(w, "name is required", http.StatusUnprocessableEntity)
		return
	}

	cat, err := h.categories.Create(r.Context(), tenantID, domain.Category{
		Name:        req.Name,
		Description: req.Description,
		ParentID:    req.ParentID,
		IsActive:    true,
		SortOrder:   req.SortOrder,
	})
	if err != nil {
		h.error(w, r, err)
		return
	}
	respondJSON(w, http.StatusCreated, cat)
}

// ---------------------------------------------------------------------------
// Product handlers
// ---------------------------------------------------------------------------

func (h *Handler) listProducts(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := requireTenantID(w, r)
	if !ok {
		return
	}
	products, err := h.products.List(r.Context(), tenantID)
	if err != nil {
		h.error(w, r, err)
		return
	}
	respondJSON(w, http.StatusOK, products)
}

func (h *Handler) getProduct(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := requireTenantID(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	p, err := h.products.GetByID(r.Context(), tenantID, id)
	if err != nil {
		h.error(w, r, err)
		return
	}
	respondJSON(w, http.StatusOK, p)
}

func (h *Handler) createProduct(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := requireTenantID(w, r)
	if !ok {
		return
	}
	var req struct {
		CategoryID           *uuid.UUID `json:"category_id"`
		Name                 string     `json:"name"`
		Description          string     `json:"description"`
		PriceAmount          int64      `json:"price_amount"`
		Currency             string     `json:"currency"`
		Unit                 string     `json:"unit"`
		TaxRateBPS           int        `json:"tax_rate_bps"`
		AutoCloseOnZeroStock bool       `json:"auto_close_on_zero_stock"`
		StockQuantity        *int       `json:"stock_quantity"`
		SortOrder            int16      `json:"sort_order"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Name == "" || req.PriceAmount < 0 {
		http.Error(w, "name is required and price_amount must be >= 0", http.StatusUnprocessableEntity)
		return
	}
	if req.Currency == "" {
		req.Currency = "TRY"
	}
	if req.Unit == "" {
		req.Unit = "adet"
	}

	p, err := h.products.Create(r.Context(), tenantID, domain.Product{
		CategoryID:           req.CategoryID,
		Name:                 req.Name,
		Description:          req.Description,
		PriceAmount:          req.PriceAmount,
		Currency:             req.Currency,
		Unit:                 req.Unit,
		TaxRateBPS:           req.TaxRateBPS,
		IsActive:             true,
		AutoCloseOnZeroStock: req.AutoCloseOnZeroStock,
		StockQuantity:        req.StockQuantity,
		SortOrder:            req.SortOrder,
	})
	if err != nil {
		h.error(w, r, err)
		return
	}
	respondJSON(w, http.StatusCreated, p)
}

func (h *Handler) updateProduct(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := requireTenantID(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	var req domain.Product
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	req.ID = id

	updated, err := h.products.Update(r.Context(), tenantID, req)
	if err != nil {
		h.error(w, r, err)
		return
	}
	respondJSON(w, http.StatusOK, updated)
}

func (h *Handler) deleteProduct(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := requireTenantID(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if err := h.products.Delete(r.Context(), tenantID, id); err != nil {
		h.error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listByCategory(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := requireTenantID(w, r)
	if !ok {
		return
	}
	catID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid category id", http.StatusBadRequest)
		return
	}
	products, err := h.products.ListByCategory(r.Context(), tenantID, catID)
	if err != nil {
		h.error(w, r, err)
		return
	}
	respondJSON(w, http.StatusOK, products)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (h *Handler) error(w http.ResponseWriter, _ *http.Request, err error) {
	if errors.Is(err, pub.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	h.logger.Error("catalog handler error", zap.Error(err))
	http.Error(w, "internal server error", http.StatusInternalServerError)
}

func respondJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
