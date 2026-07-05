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

	"github.com/redis/go-redis/v9"

	"onlinemenu.tr/internal/modules/pos/domain"
	pub "onlinemenu.tr/internal/modules/pos/public"
	"onlinemenu.tr/internal/modules/pos/service"
	"onlinemenu.tr/internal/platform/auth"
	"onlinemenu.tr/internal/platform/httpx"
)

// Handler exposes POS REST endpoints.
type Handler struct {
	checks *service.CheckService
	orders *service.OrderService
	logger *zap.Logger
	engine *auth.Engine
}

// Params groups fx-injected dependencies.
type Params struct {
	fx.In

	Checks *service.CheckService
	Orders *service.OrderService
	Logger *zap.Logger
	Cache  *redis.Client
	Engine *auth.Engine
}

// HandlerWithCache wraps Handler with the Redis client needed for the
// idempotency middleware (ADR-SEC-003).
type HandlerWithCache struct {
	h     *Handler
	cache *redis.Client
}

func NewHandler(p Params) *HandlerWithCache {
	return &HandlerWithCache{
		h:     &Handler{checks: p.Checks, orders: p.Orders, logger: p.Logger, engine: p.Engine},
		cache: p.Cache,
	}
}

// permit builds per-route OPA authorization middleware (ADR-AUTH-001, layer 2).
func (h *Handler) permit(action string) func(http.Handler) http.Handler {
	return auth.RequirePermission(h.engine, action)
}

// RegisterRoutes mounts POS endpoints on the provided router.
// ADR-SEC-003: order creation and check close require Idempotency-Key —
// both are POST endpoints with side effects (kitchen ticket dispatch, fiscal
// close) that must not be duplicated by client retries. Open/cancel/accept/
// reject/advance are not idempotency-key-gated: cancel/accept/reject/advance
// are already guarded by the status-transition machine (a retry lands on an
// already-transitioned row and gets a 409, not a duplicate side effect), and
// open-check has no equivalent natural dedup key from the client today.
//
// Every route also carries auth.RequirePermission (ADR-AUTH-001, layer 2). Where
// a route combines both, RequirePermission is listed first in r.With so a
// caller without permission never reaches the idempotency reservation logic.
func (hwc *HandlerWithCache) RegisterRoutes(r *chi.Mux) {
	r.Route("/api/v1/pos", func(r chi.Router) {
		r.With(hwc.h.permit("pos.check.read")).Get("/checks", hwc.h.listChecks)
		r.With(hwc.h.permit("pos.check.open")).Post("/checks", hwc.h.openCheck)
		r.With(hwc.h.permit("pos.check.read")).Get("/checks/{id}", hwc.h.getCheck)
		r.With(hwc.h.permit("pos.check.close"), httpx.Idempotency(hwc.cache)).Post("/checks/{id}/close", hwc.h.closeCheck)
		r.With(hwc.h.permit("pos.check.cancel")).Post("/checks/{id}/cancel", hwc.h.cancelCheck)
		r.With(hwc.h.permit("pos.order.read")).Get("/checks/{id}/orders", hwc.h.listOrdersByCheck)

		r.With(hwc.h.permit("pos.order.place"), httpx.Idempotency(hwc.cache)).Post("/orders", hwc.h.placeOrder)
		r.With(hwc.h.permit("pos.order.read")).Get("/orders/{id}", hwc.h.getOrder)
		r.With(hwc.h.permit("pos.order.accept")).Post("/orders/{id}/accept", hwc.h.acceptOrder)
		r.With(hwc.h.permit("pos.order.reject")).Post("/orders/{id}/reject", hwc.h.rejectOrder)
		r.With(hwc.h.permit("pos.order.advance")).Post("/orders/{id}/advance", hwc.h.advanceOrder)
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
	resp := make([]checkResponse, len(checks))
	for i, c := range checks {
		resp[i] = toCheckResponse(c)
	}
	respondJSON(w, http.StatusOK, resp)
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

	c, err := h.checks.Open(r.Context(), p.TenantID, p, domain.Check{
		BranchID:   req.BranchID,
		TableLabel: req.TableLabel,
		Note:       req.Note,
		OpenedBy:   p.PersonID,
	})
	if err != nil {
		h.error(w, r, err)
		return
	}
	respondJSON(w, http.StatusCreated, toCheckResponse(c))
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
	respondJSON(w, http.StatusOK, toCheckResponse(c))
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
	c, err := h.checks.Close(r.Context(), p.TenantID, p, id, p.PersonID)
	if err != nil {
		h.error(w, r, err)
		return
	}
	respondJSON(w, http.StatusOK, toCheckResponse(c))
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
	c, err := h.checks.Cancel(r.Context(), p.TenantID, p, id, p.PersonID)
	if err != nil {
		h.error(w, r, err)
		return
	}
	respondJSON(w, http.StatusOK, toCheckResponse(c))
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
	resp := make([]orderResponse, len(orders))
	for i, o := range orders {
		resp[i] = toOrderResponse(o)
	}
	respondJSON(w, http.StatusOK, resp)
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

	o, err := h.orders.Place(r.Context(), p.TenantID, p, domain.Order{
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
	respondJSON(w, http.StatusCreated, toOrderResponse(o))
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
	respondJSON(w, http.StatusOK, toOrderResponse(o))
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
	o, err := h.orders.Accept(r.Context(), p.TenantID, p, id, p.PersonID)
	if err != nil {
		h.error(w, r, err)
		return
	}
	respondJSON(w, http.StatusOK, toOrderResponse(o))
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
	o, err := h.orders.Reject(r.Context(), p.TenantID, p, id, p.PersonID, req.Reason)
	if err != nil {
		h.error(w, r, err)
		return
	}
	respondJSON(w, http.StatusOK, toOrderResponse(o))
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
	o, err := h.orders.AdvanceStatus(r.Context(), p.TenantID, p, id, domain.OrderStatus(req.Status))
	if err != nil {
		h.error(w, r, err)
		return
	}
	respondJSON(w, http.StatusOK, toOrderResponse(o))
}

// ---------------------------------------------------------------------------
// Response DTOs
// ---------------------------------------------------------------------------

type checkResponse struct {
	ID         uuid.UUID  `json:"id"`
	TenantID   uuid.UUID  `json:"tenant_id"`
	BranchID   uuid.UUID  `json:"branch_id"`
	TableLabel string     `json:"table_label"`
	Status     string     `json:"status"`
	Note       string     `json:"note"`
	OpenedAt   time.Time  `json:"opened_at"`
	ClosedAt   *time.Time `json:"closed_at"`
}

func toCheckResponse(c domain.Check) checkResponse {
	return checkResponse{
		ID:         c.ID,
		TenantID:   c.TenantID,
		BranchID:   c.BranchID,
		TableLabel: c.TableLabel,
		Status:     string(c.Status),
		Note:       c.Note,
		OpenedAt:   c.OpenedAt,
		ClosedAt:   c.ClosedAt,
	}
}

type orderItemResponse struct {
	ID              uuid.UUID `json:"id"`
	ProductID       uuid.UUID `json:"product_id"`
	ProductName     string    `json:"product_name"`
	Quantity        int       `json:"quantity"`
	UnitPriceAmount int64     `json:"unit_price_amount"`
	Note            string    `json:"note"`
}

type orderResponse struct {
	ID           uuid.UUID           `json:"id"`
	TenantID     uuid.UUID           `json:"tenant_id"`
	BranchID     uuid.UUID           `json:"branch_id"`
	CheckID      *uuid.UUID          `json:"check_id"`
	OrderChannel string              `json:"order_channel"`
	Status       string              `json:"status"`
	Note         string              `json:"note"`
	Items        []orderItemResponse `json:"items"`
	CreatedAt    time.Time           `json:"created_at"`
	UpdatedAt    time.Time           `json:"updated_at"`
}

func toOrderResponse(o domain.Order) orderResponse {
	items := make([]orderItemResponse, len(o.Items))
	for i, it := range o.Items {
		items[i] = orderItemResponse{
			ID:              it.ID,
			ProductID:       it.ProductID,
			ProductName:     it.ProductName,
			Quantity:        it.Quantity,
			UnitPriceAmount: it.UnitPriceAmount,
			Note:            it.Note,
		}
	}
	return orderResponse{
		ID:           o.ID,
		TenantID:     o.TenantID,
		BranchID:     o.BranchID,
		CheckID:      o.CheckID,
		OrderChannel: string(o.OrderChannel),
		Status:       string(o.Status),
		Note:         o.Note,
		Items:        items,
		CreatedAt:    o.CreatedAt,
		UpdatedAt:    o.UpdatedAt,
	}
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
	if errors.Is(err, pub.ErrInvalidTransition) {
		http.Error(w, "invalid status transition", http.StatusConflict)
		return
	}
	if errors.Is(err, pub.ErrBranchForbidden) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if errors.Is(err, service.ErrInsufficientPayment) {
		http.Error(w, "payment insufficient to close check", http.StatusConflict)
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
