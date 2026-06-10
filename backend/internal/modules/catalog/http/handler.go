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
	modifiers  *service.ModifierService
	menus      *service.MenuService
	logger     *zap.Logger
}

// Params groups fx-injected dependencies for NewHandler.
type Params struct {
	fx.In

	Categories *service.CategoryService
	Products   *service.ProductService
	Modifiers  *service.ModifierService
	Menus      *service.MenuService
	Logger     *zap.Logger
}

// NewHandler constructs a Handler for fx injection.
func NewHandler(p Params) *Handler {
	return &Handler{
		categories: p.Categories,
		products:   p.Products,
		modifiers:  p.Modifiers,
		menus:      p.Menus,
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

		// Modifier groups
		r.Post("/modifier-groups", h.createModifierGroup)
		r.Get("/modifier-groups", h.listModifierGroups)
		r.Get("/modifier-groups/{id}", h.getModifierGroup)
		r.Put("/modifier-groups/{id}", h.updateModifierGroup)
		r.Delete("/modifier-groups/{id}", h.deleteModifierGroup)

		// Modifiers within a group
		r.Post("/modifier-groups/{id}/modifiers", h.createModifier)
		r.Get("/modifier-groups/{id}/modifiers", h.listModifiers)
		r.Put("/modifier-groups/{groupID}/modifiers/{id}", h.updateModifier)
		r.Delete("/modifier-groups/{groupID}/modifiers/{id}", h.deleteModifier)

		// Product ↔ modifier group assignments
		r.Post("/products/{id}/modifier-groups", h.assignModifierGroup)
		r.Get("/products/{id}/modifier-groups", h.listProductModifierGroups)
		r.Delete("/products/{id}/modifier-groups/{groupID}", h.removeModifierGroup)

		// Menus
		r.Post("/menus", h.createMenu)
		r.Get("/menus", h.listMenus)
		r.Get("/menus/{id}", h.getMenu)
		r.Put("/menus/{id}", h.updateMenu)

		// Menu items
		r.Post("/menus/{id}/items", h.addMenuItem)
		r.Get("/menus/{id}/items", h.listMenuItems)
		r.Delete("/menus/{id}/items/{productID}", h.removeMenuItem)
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
	out := make([]categoryResponse, len(cats))
	for i, c := range cats {
		out[i] = toCategoryResponse(c)
	}
	respondJSON(w, http.StatusOK, out)
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
	respondJSON(w, http.StatusOK, toCategoryResponse(cat))
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
	respondJSON(w, http.StatusCreated, toCategoryResponse(cat))
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
	out := make([]productResponse, len(products))
	for i, p := range products {
		out[i] = toProductResponse(p)
	}
	respondJSON(w, http.StatusOK, out)
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
	respondJSON(w, http.StatusOK, toProductResponse(p))
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
	respondJSON(w, http.StatusCreated, toProductResponse(p))
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

	var req struct {
		CategoryID           *uuid.UUID `json:"category_id"`
		Name                 string     `json:"name"`
		Description          string     `json:"description"`
		PriceAmount          int64      `json:"price_amount"`
		Currency             string     `json:"currency"`
		Unit                 string     `json:"unit"`
		TaxRateBPS           int        `json:"tax_rate_bps"`
		IsActive             bool       `json:"is_active"`
		AutoCloseOnZeroStock bool       `json:"auto_close_on_zero_stock"`
		StockQuantity        *int       `json:"stock_quantity"`
		SortOrder            int16      `json:"sort_order"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	updated, err := h.products.Update(r.Context(), tenantID, domain.Product{
		ID:                   id,
		CategoryID:           req.CategoryID,
		Name:                 req.Name,
		Description:          req.Description,
		PriceAmount:          req.PriceAmount,
		Currency:             req.Currency,
		Unit:                 req.Unit,
		TaxRateBPS:           req.TaxRateBPS,
		IsActive:             req.IsActive,
		AutoCloseOnZeroStock: req.AutoCloseOnZeroStock,
		StockQuantity:        req.StockQuantity,
		SortOrder:            req.SortOrder,
	})
	if err != nil {
		h.error(w, r, err)
		return
	}
	respondJSON(w, http.StatusOK, toProductResponse(updated))
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
	out := make([]productResponse, len(products))
	for i, p := range products {
		out[i] = toProductResponse(p)
	}
	respondJSON(w, http.StatusOK, out)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (h *Handler) error(w http.ResponseWriter, _ *http.Request, err error) {
	if errors.Is(err, pub.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	var ve *pub.ValidationError
	if errors.As(err, &ve) {
		http.Error(w, ve.Msg, http.StatusUnprocessableEntity)
		return
	}
	h.logger.Error("catalog handler error", zap.Error(err))
	http.Error(w, "internal server error", http.StatusInternalServerError)
}

// ---------------------------------------------------------------------------
// Modifier group handlers
// ---------------------------------------------------------------------------

func (h *Handler) createModifierGroup(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := requireTenantID(w, r)
	if !ok {
		return
	}
	var req struct {
		Name          string `json:"name"`
		SelectionType string `json:"selection_type"`
		MinSelections int16  `json:"min_selections"`
		MaxSelections *int16 `json:"max_selections"`
		IsRequired    bool   `json:"is_required"`
		SortOrder     int16  `json:"sort_order"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		http.Error(w, "name is required", http.StatusUnprocessableEntity)
		return
	}
	if req.SelectionType == "" {
		req.SelectionType = "single"
	}
	g, err := h.modifiers.CreateGroup(r.Context(), tenantID, domain.ModifierGroup{
		Name:          req.Name,
		SelectionType: domain.SelectionType(req.SelectionType),
		MinSelections: req.MinSelections,
		MaxSelections: req.MaxSelections,
		IsRequired:    req.IsRequired,
		SortOrder:     req.SortOrder,
	})
	if err != nil {
		h.error(w, r, err)
		return
	}
	respondJSON(w, http.StatusCreated, toModifierGroupResponse(g))
}

func (h *Handler) listModifierGroups(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := requireTenantID(w, r)
	if !ok {
		return
	}
	groups, err := h.modifiers.ListGroups(r.Context(), tenantID)
	if err != nil {
		h.error(w, r, err)
		return
	}
	out := make([]modifierGroupResponse, len(groups))
	for i, g := range groups {
		out[i] = toModifierGroupResponse(g)
	}
	respondJSON(w, http.StatusOK, out)
}

func (h *Handler) getModifierGroup(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := requireTenantID(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	g, err := h.modifiers.GetGroup(r.Context(), tenantID, id)
	if err != nil {
		h.error(w, r, err)
		return
	}
	respondJSON(w, http.StatusOK, toModifierGroupResponse(g))
}

func (h *Handler) updateModifierGroup(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := requireTenantID(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	var req struct {
		Name          string `json:"name"`
		SelectionType string `json:"selection_type"`
		MinSelections int16  `json:"min_selections"`
		MaxSelections *int16 `json:"max_selections"`
		IsRequired    bool   `json:"is_required"`
		SortOrder     int16  `json:"sort_order"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	updated, err := h.modifiers.UpdateGroup(r.Context(), tenantID, domain.ModifierGroup{
		ID:            id,
		Name:          req.Name,
		SelectionType: domain.SelectionType(req.SelectionType),
		MinSelections: req.MinSelections,
		MaxSelections: req.MaxSelections,
		IsRequired:    req.IsRequired,
		SortOrder:     req.SortOrder,
	})
	if err != nil {
		h.error(w, r, err)
		return
	}
	respondJSON(w, http.StatusOK, toModifierGroupResponse(updated))
}

func (h *Handler) deleteModifierGroup(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := requireTenantID(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if err := h.modifiers.DeleteGroup(r.Context(), tenantID, id); err != nil {
		h.error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Modifier handlers
// ---------------------------------------------------------------------------

func (h *Handler) createModifier(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := requireTenantID(w, r)
	if !ok {
		return
	}
	groupID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid group id", http.StatusBadRequest)
		return
	}
	var req struct {
		Name       string `json:"name"`
		PriceDelta int64  `json:"price_delta"`
		IsActive   bool   `json:"is_active"`
		SortOrder  int16  `json:"sort_order"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	m, err := h.modifiers.CreateModifier(r.Context(), tenantID, domain.Modifier{
		GroupID:    groupID,
		Name:       req.Name,
		PriceDelta: req.PriceDelta,
		IsActive:   req.IsActive,
		SortOrder:  req.SortOrder,
	})
	if err != nil {
		h.error(w, r, err)
		return
	}
	respondJSON(w, http.StatusCreated, toModifierResponse(m))
}

func (h *Handler) listModifiers(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := requireTenantID(w, r)
	if !ok {
		return
	}
	groupID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid group id", http.StatusBadRequest)
		return
	}
	modifiers, err := h.modifiers.ListModifiers(r.Context(), tenantID, groupID)
	if err != nil {
		h.error(w, r, err)
		return
	}
	out := make([]modifierResponse, len(modifiers))
	for i, mod := range modifiers {
		out[i] = toModifierResponse(mod)
	}
	respondJSON(w, http.StatusOK, out)
}

func (h *Handler) updateModifier(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := requireTenantID(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	var req struct {
		Name       string `json:"name"`
		PriceDelta int64  `json:"price_delta"`
		IsActive   bool   `json:"is_active"`
		SortOrder  int16  `json:"sort_order"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	updated, err := h.modifiers.UpdateModifier(r.Context(), tenantID, domain.Modifier{
		ID:         id,
		Name:       req.Name,
		PriceDelta: req.PriceDelta,
		IsActive:   req.IsActive,
		SortOrder:  req.SortOrder,
	})
	if err != nil {
		h.error(w, r, err)
		return
	}
	respondJSON(w, http.StatusOK, toModifierResponse(updated))
}

func (h *Handler) deleteModifier(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := requireTenantID(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if err := h.modifiers.DeleteModifier(r.Context(), tenantID, id); err != nil {
		h.error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Product ↔ modifier group assignment handlers
// ---------------------------------------------------------------------------

func (h *Handler) assignModifierGroup(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := requireTenantID(w, r)
	if !ok {
		return
	}
	productID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid product id", http.StatusBadRequest)
		return
	}
	var req struct {
		GroupID   uuid.UUID `json:"group_id"`
		SortOrder int16     `json:"sort_order"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.GroupID == uuid.Nil {
		http.Error(w, "group_id is required", http.StatusUnprocessableEntity)
		return
	}
	if err := h.modifiers.AssignGroup(r.Context(), tenantID, productID, req.GroupID, req.SortOrder); err != nil {
		h.error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listProductModifierGroups(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := requireTenantID(w, r)
	if !ok {
		return
	}
	productID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid product id", http.StatusBadRequest)
		return
	}
	ids, err := h.modifiers.ListProductGroups(r.Context(), tenantID, productID)
	if err != nil {
		h.error(w, r, err)
		return
	}
	respondJSON(w, http.StatusOK, ids)
}

func (h *Handler) removeModifierGroup(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := requireTenantID(w, r)
	if !ok {
		return
	}
	productID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid product id", http.StatusBadRequest)
		return
	}
	groupID, err := uuid.Parse(chi.URLParam(r, "groupID"))
	if err != nil {
		http.Error(w, "invalid group id", http.StatusBadRequest)
		return
	}
	if err := h.modifiers.RemoveGroup(r.Context(), tenantID, productID, groupID); err != nil {
		h.error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Menu handlers
// ---------------------------------------------------------------------------

func (h *Handler) createMenu(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := requireTenantID(w, r)
	if !ok {
		return
	}
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		IsActive    bool   `json:"is_active"`
		SortOrder   int16  `json:"sort_order"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		http.Error(w, "name is required", http.StatusUnprocessableEntity)
		return
	}
	m, err := h.menus.Create(r.Context(), tenantID, domain.Menu{
		Name:        req.Name,
		Description: req.Description,
		IsActive:    req.IsActive,
		SortOrder:   req.SortOrder,
	})
	if err != nil {
		h.error(w, r, err)
		return
	}
	respondJSON(w, http.StatusCreated, toMenuResponse(m))
}

func (h *Handler) listMenus(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := requireTenantID(w, r)
	if !ok {
		return
	}
	menus, err := h.menus.List(r.Context(), tenantID)
	if err != nil {
		h.error(w, r, err)
		return
	}
	out := make([]menuResponse, len(menus))
	for i, menu := range menus {
		out[i] = toMenuResponse(menu)
	}
	respondJSON(w, http.StatusOK, out)
}

func (h *Handler) getMenu(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := requireTenantID(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	m, err := h.menus.GetByID(r.Context(), tenantID, id)
	if err != nil {
		h.error(w, r, err)
		return
	}
	respondJSON(w, http.StatusOK, toMenuResponse(m))
}

func (h *Handler) updateMenu(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := requireTenantID(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		IsActive    bool   `json:"is_active"`
		SortOrder   int16  `json:"sort_order"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	updated, err := h.menus.Update(r.Context(), tenantID, domain.Menu{
		ID:          id,
		Name:        req.Name,
		Description: req.Description,
		IsActive:    req.IsActive,
		SortOrder:   req.SortOrder,
	})
	if err != nil {
		h.error(w, r, err)
		return
	}
	respondJSON(w, http.StatusOK, toMenuResponse(updated))
}

// ---------------------------------------------------------------------------
// Menu item handlers
// ---------------------------------------------------------------------------

func (h *Handler) addMenuItem(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := requireTenantID(w, r)
	if !ok {
		return
	}
	menuID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid menu id", http.StatusBadRequest)
		return
	}
	var req struct {
		ProductID     uuid.UUID `json:"product_id"`
		PriceOverride *int64    `json:"price_override"`
		IsActive      bool      `json:"is_active"`
		SortOrder     int16     `json:"sort_order"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.ProductID == uuid.Nil {
		http.Error(w, "product_id is required", http.StatusUnprocessableEntity)
		return
	}
	if err := h.menus.AddItem(r.Context(), tenantID, domain.MenuItem{
		MenuID:        menuID,
		ProductID:     req.ProductID,
		PriceOverride: req.PriceOverride,
		IsActive:      req.IsActive,
		SortOrder:     req.SortOrder,
	}); err != nil {
		h.error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listMenuItems(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := requireTenantID(w, r)
	if !ok {
		return
	}
	menuID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid menu id", http.StatusBadRequest)
		return
	}
	items, err := h.menus.ListItems(r.Context(), tenantID, menuID)
	if err != nil {
		h.error(w, r, err)
		return
	}
	out := make([]menuItemResponse, len(items))
	for i, item := range items {
		out[i] = toMenuItemResponse(item)
	}
	respondJSON(w, http.StatusOK, out)
}

func (h *Handler) removeMenuItem(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := requireTenantID(w, r)
	if !ok {
		return
	}
	menuID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid menu id", http.StatusBadRequest)
		return
	}
	productID, err := uuid.Parse(chi.URLParam(r, "productID"))
	if err != nil {
		http.Error(w, "invalid product id", http.StatusBadRequest)
		return
	}
	if err := h.menus.RemoveItem(r.Context(), tenantID, menuID, productID); err != nil {
		h.error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func respondJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
