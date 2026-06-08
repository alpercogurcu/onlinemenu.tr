// Package http provides the HTTP layer for the POS module.
package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/pos/domain"
	pub "onlinemenu.tr/internal/modules/pos/public"
	"onlinemenu.tr/internal/modules/pos/service"
	"onlinemenu.tr/internal/platform/auth"
)

// Handler exposes POS REST endpoints.
type Handler struct {
	checks *service.CheckService
	orders *service.OrderService
	logger *zap.Logger
}

// Params groups fx-injected dependencies.
type Params struct {
	fx.In

	Checks *service.CheckService
	Orders *service.OrderService
	Logger *zap.Logger
}

func NewHandler(p Params) *Handler {
	return &Handler{checks: p.Checks, orders: p.Orders, logger: p.Logger}
}

// RegisterRoutes mounts POS endpoints on the provided router.
func (h *Handler) RegisterRoutes(r *chi.Mux) {
	r.Route("/api/v1/pos", func(r chi.Router) {
		r.Get("/checks", h.listChecks)
		r.Post("/checks", h.openCheck)
		r.Get("/checks/{id}", h.getCheck)
		r.Post("/checks/{id}/close", h.closeCheck)
		r.Post("/checks/{id}/cancel", h.cancelCheck)
		r.Get("/checks/{id}/orders", h.listOrdersByCheck)

		r.Post("/orders", h.placeOrder)
		r.Get("/orders/{id}", h.getOrder)
		r.Post("/orders/{id}/accept", h.acceptOrder)
		r.Post("/orders/{id}/reject", h.rejectOrder)
		r.Post("/orders/{id}/advance", h.advanceOrder)
	})
}

// requirePrincipal extracts the auth principal and verifies TenantID is set.
func requirePrincipal(w http.ResponseWriter, r *http.Request) (auth.Principal, bool) {
	p, err := auth.FromContext(r.Context())
	if err != nil || p.TenantID == uuid.Nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return auth.Principal{}, false
	}
	return p, true
}

// ---------------------------------------------------------------------------
// Check handlers
// ---------------------------------------------------------------------------

func (h *Handler) listChecks(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	checks, err := h.checks.List(r.Context(), p.TenantID)
	if err != nil {
		h.error(w, r, err)
		return
	}
	respondJSON(w, http.StatusOK, checks)
}

func (h *Handler) openCheck(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	var req struct {
		BranchID   uuid.UUID `json:"branch_id"`
		TableLabel string    `json:"table_label"`
		Note       string    `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.BranchID == uuid.Nil {
		http.Error(w, "branch_id is required", http.StatusUnprocessableEntity)
		return
	}

	c, err := h.checks.Open(r.Context(), p.TenantID, domain.Check{
		BranchID:   req.BranchID,
		TableLabel: req.TableLabel,
		Note:       req.Note,
		OpenedBy:   p.PersonID,
	})
	if err != nil {
		h.error(w, r, err)
		return
	}
	respondJSON(w, http.StatusCreated, c)
}

func (h *Handler) getCheck(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	c, err := h.checks.GetByID(r.Context(), p.TenantID, id)
	if err != nil {
		h.error(w, r, err)
		return
	}
	respondJSON(w, http.StatusOK, c)
}

func (h *Handler) closeCheck(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	c, err := h.checks.Close(r.Context(), p.TenantID, id, p.PersonID)
	if err != nil {
		h.error(w, r, err)
		return
	}
	respondJSON(w, http.StatusOK, c)
}

func (h *Handler) cancelCheck(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	c, err := h.checks.Cancel(r.Context(), p.TenantID, id, p.PersonID)
	if err != nil {
		h.error(w, r, err)
		return
	}
	respondJSON(w, http.StatusOK, c)
}

func (h *Handler) listOrdersByCheck(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	checkID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid check id", http.StatusBadRequest)
		return
	}
	orders, err := h.orders.ListByCheck(r.Context(), p.TenantID, checkID)
	if err != nil {
		h.error(w, r, err)
		return
	}
	respondJSON(w, http.StatusOK, orders)
}

// ---------------------------------------------------------------------------
// Order handlers
// ---------------------------------------------------------------------------

func (h *Handler) placeOrder(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	var req struct {
		BranchID             uuid.UUID        `json:"branch_id"`
		CheckID              *uuid.UUID       `json:"check_id"`
		OrderChannel         string           `json:"order_channel"`
		DeliveryIntegratorID *uuid.UUID       `json:"delivery_integrator_id"`
		AcceptDeadlineAt     *time.Time       `json:"accept_deadline_at"`
		Note                 string           `json:"note"`
		Items                []orderItemInput `json:"items"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.BranchID == uuid.Nil {
		http.Error(w, "branch_id is required", http.StatusUnprocessableEntity)
		return
	}
	if len(req.Items) == 0 {
		http.Error(w, "items is required", http.StatusUnprocessableEntity)
		return
	}

	items := make([]domain.OrderItem, len(req.Items))
	for i, it := range req.Items {
		items[i] = domain.OrderItem{
			ProductID:          it.ProductID,
			ProductName:        it.ProductName,
			ProductPriceAmount: it.ProductPriceAmount,
			ProductCurrency:    it.ProductCurrency,
			TaxRateBPS:         it.TaxRateBPS,
			Quantity:           it.Quantity,
			UnitPriceAmount:    it.UnitPriceAmount,
			Note:               it.Note,
		}
		if items[i].ProductCurrency == "" {
			items[i].ProductCurrency = "TRY"
		}
	}

	o, err := h.orders.Place(r.Context(), p.TenantID, domain.Order{
		BranchID:             req.BranchID,
		CheckID:              req.CheckID,
		OrderChannel:         domain.OrderChannel(req.OrderChannel),
		DeliveryIntegratorID: req.DeliveryIntegratorID,
		AcceptDeadlineAt:     req.AcceptDeadlineAt,
		Note:                 req.Note,
		Items:                items,
	})
	if err != nil {
		h.error(w, r, err)
		return
	}
	respondJSON(w, http.StatusCreated, o)
}

func (h *Handler) getOrder(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	o, err := h.orders.GetByID(r.Context(), p.TenantID, id)
	if err != nil {
		h.error(w, r, err)
		return
	}
	respondJSON(w, http.StatusOK, o)
}

func (h *Handler) acceptOrder(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	o, err := h.orders.Accept(r.Context(), p.TenantID, id, p.PersonID)
	if err != nil {
		h.error(w, r, err)
		return
	}
	respondJSON(w, http.StatusOK, o)
}

func (h *Handler) rejectOrder(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	o, err := h.orders.Reject(r.Context(), p.TenantID, id, p.PersonID, req.Reason)
	if err != nil {
		h.error(w, r, err)
		return
	}
	respondJSON(w, http.StatusOK, o)
}

func (h *Handler) advanceOrder(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	var req struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	o, err := h.orders.AdvanceStatus(r.Context(), p.TenantID, id, domain.OrderStatus(req.Status))
	if err != nil {
		h.error(w, r, err)
		return
	}
	respondJSON(w, http.StatusOK, o)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

type orderItemInput struct {
	ProductID          uuid.UUID `json:"product_id"`
	ProductName        string    `json:"product_name"`
	ProductPriceAmount int64     `json:"product_price_amount"`
	ProductCurrency    string    `json:"product_currency"`
	TaxRateBPS         int       `json:"tax_rate_bps"`
	Quantity           int       `json:"quantity"`
	UnitPriceAmount    int64     `json:"unit_price_amount"`
	Note               string    `json:"note"`
}

func (h *Handler) error(w http.ResponseWriter, _ *http.Request, err error) {
	if errors.Is(err, pub.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	h.logger.Error("pos handler error", zap.Error(err))
	http.Error(w, "internal server error", http.StatusInternalServerError)
}

func respondJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
