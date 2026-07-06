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
	tables *service.TableService
	logger *zap.Logger
	engine *auth.Engine
}

// Params groups fx-injected dependencies.
type Params struct {
	fx.In

	Checks *service.CheckService
	Orders *service.OrderService
	Tables *service.TableService
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
		h:     &Handler{checks: p.Checks, orders: p.Orders, tables: p.Tables, logger: p.Logger, engine: p.Engine},
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

		// Table plan (Sprint-5 Wave 1): zone CRUD + table CRUD/status are
		// manager/shift_manager only (pos.table.manage); reading the plan is
		// open to every branch-facing role (pos.table.read) since cashier,
		// waiter, kitchen and bar all need to see table state.
		r.With(hwc.h.permit("pos.table.read")).Get("/zones", hwc.h.listZones)
		r.With(hwc.h.permit("pos.table.manage")).Post("/zones", hwc.h.createZone)
		r.With(hwc.h.permit("pos.table.manage")).Patch("/zones/{id}", hwc.h.updateZone)
		r.With(hwc.h.permit("pos.table.read")).Get("/tables", hwc.h.listTables)
		r.With(hwc.h.permit("pos.table.manage")).Post("/tables", hwc.h.createTable)
		r.With(hwc.h.permit("pos.table.manage")).Patch("/tables/{id}", hwc.h.updateTable)
		r.With(hwc.h.permit("pos.table.manage")).Post("/tables/{id}/status", hwc.h.setTableStatus)
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

// listChecks supports two optional query filters, status and branch_id (both
// narrowing, not restricting, the tenant-wide result set — see
// CheckService.List's doc comment on why branch_id is not enforced against
// the principal here). Either or both may be present; absent means "no
// filter on that column", so an empty query string must not be treated as an
// invalid value — only a *present but malformed* value is a 422.
func (h *Handler) listChecks(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	var filter service.CheckListFilter
	if raw := r.URL.Query().Get("status"); raw != "" {
		status := domain.CheckStatus(raw)
		if !status.Valid() {
			http.Error(w, "invalid status", http.StatusUnprocessableEntity)
			return
		}
		filter.Status = &status
	}
	if raw := r.URL.Query().Get("branch_id"); raw != "" {
		branchID, err := uuid.Parse(raw)
		if err != nil {
			http.Error(w, "invalid branch_id", http.StatusUnprocessableEntity)
			return
		}
		filter.BranchID = &branchID
	}
	checks, totals, err := h.checks.List(r.Context(), p.TenantID, filter)
	if err != nil {
		h.error(w, r, err)
		return
	}
	resp := make([]checkResponse, len(checks))
	for i, c := range checks {
		resp[i] = toCheckResponse(c)
		total := totals[c.ID]
		resp[i].Total = &total
	}
	respondJSON(w, http.StatusOK, resp)
}

func (h *Handler) openCheck(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	// Pax (guest count) is optional: an omitted or zero/negative value falls
	// back to CheckService.Open's default of 1 — existing pos-desktop/admin
	// clients that don't yet send pax keep working unchanged.
	var req struct {
		BranchID   uuid.UUID  `json:"branch_id"`
		TableID    *uuid.UUID `json:"table_id"`
		TableLabel string     `json:"table_label"`
		Pax        int        `json:"pax"`
		Note       string     `json:"note"`
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
		TableID:    req.TableID,
		TableLabel: req.TableLabel,
		Pax:        req.Pax,
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
	c, total, err := h.checks.GetByIDWithTotal(r.Context(), p.TenantID, id)
	if err != nil {
		h.error(w, r, err)
		return
	}
	resp := toCheckResponse(c)
	resp.Total = &total
	respondJSON(w, http.StatusOK, resp)
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
// Table plan handlers (zones + tables)
// ---------------------------------------------------------------------------

func (h *Handler) listZones(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	branchID, err := uuid.Parse(r.URL.Query().Get("branch_id"))
	if err != nil {
		http.Error(w, "branch_id query parameter is required", http.StatusUnprocessableEntity)
		return
	}
	zones, err := h.tables.ListZones(r.Context(), p.TenantID, p, branchID)
	if err != nil {
		h.error(w, r, err)
		return
	}
	resp := make([]zoneResponse, len(zones))
	for i, z := range zones {
		resp[i] = toZoneResponse(z)
	}
	respondJSON(w, http.StatusOK, resp)
}

func (h *Handler) createZone(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	var req struct {
		BranchID uuid.UUID `json:"branch_id"`
		Name     string    `json:"name"`
		Floor    int       `json:"floor"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.BranchID == uuid.Nil || req.Name == "" {
		http.Error(w, "branch_id and name are required", http.StatusUnprocessableEntity)
		return
	}
	z, err := h.tables.CreateZone(r.Context(), p.TenantID, p, domain.TableZone{
		BranchID: req.BranchID,
		Name:     req.Name,
		Floor:    req.Floor,
		IsActive: true,
	})
	if err != nil {
		h.error(w, r, err)
		return
	}
	respondJSON(w, http.StatusCreated, toZoneResponse(z))
}

func (h *Handler) updateZone(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	// Pointer fields so an omitted field (PATCH semantics) leaves the current
	// value untouched — see service.ZonePatch's doc comment.
	var req struct {
		Name     *string `json:"name"`
		Floor    *int    `json:"floor"`
		IsActive *bool   `json:"is_active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	z, err := h.tables.UpdateZone(r.Context(), p.TenantID, p, id, service.ZonePatch{
		Name:     req.Name,
		Floor:    req.Floor,
		IsActive: req.IsActive,
	})
	if err != nil {
		h.error(w, r, err)
		return
	}
	respondJSON(w, http.StatusOK, toZoneResponse(z))
}

// listTables returns the branch's floor plan grouped by zone (ordered by
// zone floor, then zone name, then table name — matching
// TableRepo.ListTablesByBranch's ORDER BY) so the cash register can draw
// the entire labeled plan from this single request, without a second call
// to GET /zones.
func (h *Handler) listTables(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	branchID, err := uuid.Parse(r.URL.Query().Get("branch_id"))
	if err != nil {
		http.Error(w, "branch_id query parameter is required", http.StatusUnprocessableEntity)
		return
	}
	entries, err := h.tables.ListTables(r.Context(), p.TenantID, p, branchID)
	if err != nil {
		h.error(w, r, err)
		return
	}
	respondJSON(w, http.StatusOK, toZonePlanResponse(entries))
}

func (h *Handler) createTable(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	var req struct {
		BranchID uuid.UUID       `json:"branch_id"`
		ZoneID   uuid.UUID       `json:"zone_id"`
		Name     string          `json:"name"`
		Capacity int             `json:"capacity"`
		Layout   json.RawMessage `json:"layout_position"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.BranchID == uuid.Nil || req.ZoneID == uuid.Nil || req.Name == "" {
		http.Error(w, "branch_id, zone_id and name are required", http.StatusUnprocessableEntity)
		return
	}
	t, err := h.tables.CreateTable(r.Context(), p.TenantID, p, domain.Table{
		BranchID:       req.BranchID,
		ZoneID:         req.ZoneID,
		Name:           req.Name,
		Capacity:       req.Capacity,
		LayoutPosition: req.Layout,
		IsActive:       true,
	})
	if err != nil {
		h.error(w, r, err)
		return
	}
	respondJSON(w, http.StatusCreated, toTableResponse(service.TablePlanEntry{Table: t}))
}

func (h *Handler) updateTable(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	// Pointer/nilable fields so an omitted field (PATCH semantics) leaves the
	// current value untouched — see service.TablePatch's doc comment.
	var req struct {
		ZoneID   *uuid.UUID      `json:"zone_id"`
		Name     *string         `json:"name"`
		Capacity *int            `json:"capacity"`
		Layout   json.RawMessage `json:"layout_position"`
		IsActive *bool           `json:"is_active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	t, err := h.tables.UpdateTable(r.Context(), p.TenantID, p, id, service.TablePatch{
		ZoneID:   req.ZoneID,
		Name:     req.Name,
		Capacity: req.Capacity,
		Layout:   req.Layout,
		IsActive: req.IsActive,
	})
	if err != nil {
		h.error(w, r, err)
		return
	}
	respondJSON(w, http.StatusOK, toTableResponse(service.TablePlanEntry{Table: t}))
}

func (h *Handler) setTableStatus(w http.ResponseWriter, r *http.Request) {
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
	t, err := h.tables.SetStatus(r.Context(), p.TenantID, p, id, domain.TableStatus(req.Status))
	if err != nil {
		h.error(w, r, err)
		return
	}
	respondJSON(w, http.StatusOK, toTableResponse(service.TablePlanEntry{Table: t}))
}

// ---------------------------------------------------------------------------
// Response DTOs
// ---------------------------------------------------------------------------

// checkResponse.Total (kurus, orders in domain.InactiveOrderStatuses
// excluded — see CheckRepo.GetTotal) is only populated by the list and get
// endpoints, which fetch it (list via a batch query, get via a single
// query — see CheckService.List/GetByIDWithTotal); open/close/cancel leave
// it nil so the field is omitted from the response rather than emitting a
// misleading "total: 0" for a check that may already have real orders.
// Pax, in contrast, always comes back — it lives directly on domain.Check,
// no extra query needed.
type checkResponse struct {
	ID         uuid.UUID  `json:"id"`
	TenantID   uuid.UUID  `json:"tenant_id"`
	BranchID   uuid.UUID  `json:"branch_id"`
	TableID    *uuid.UUID `json:"table_id"`
	TableLabel string     `json:"table_label"`
	Pax        int        `json:"pax"`
	Status     string     `json:"status"`
	Note       string     `json:"note"`
	OpenedAt   time.Time  `json:"opened_at"`
	ClosedAt   *time.Time `json:"closed_at"`
	Total      *int64     `json:"total,omitempty"`
}

func toCheckResponse(c domain.Check) checkResponse {
	return checkResponse{
		ID:         c.ID,
		TenantID:   c.TenantID,
		BranchID:   c.BranchID,
		TableID:    c.TableID,
		TableLabel: c.TableLabel,
		Pax:        c.Pax,
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

type zoneResponse struct {
	ID       uuid.UUID `json:"id"`
	BranchID uuid.UUID `json:"branch_id"`
	Name     string    `json:"name"`
	Floor    int       `json:"floor"`
	IsActive bool      `json:"is_active"`
}

func toZoneResponse(z domain.TableZone) zoneResponse {
	return zoneResponse{
		ID:       z.ID,
		BranchID: z.BranchID,
		Name:     z.Name,
		Floor:    z.Floor,
		IsActive: z.IsActive,
	}
}

// tableResponse is the shape the cash register draws one floor-plan row
// from: the table itself plus the id of the check currently open against it
// (null when the table is not occupied by a check). This is the Wave-2
// contract — pos-desktop's masa seçimi UI consumes GET /tables verbatim.
type tableResponse struct {
	ID             uuid.UUID       `json:"id"`
	BranchID       uuid.UUID       `json:"branch_id"`
	ZoneID         uuid.UUID       `json:"zone_id"`
	Name           string          `json:"name"`
	Capacity       int             `json:"capacity"`
	Status         string          `json:"status"`
	LayoutPosition json.RawMessage `json:"layout_position"`
	IsActive       bool            `json:"is_active"`
	ActiveCheckID  *uuid.UUID      `json:"active_check_id"`
}

func toTableResponse(e service.TablePlanEntry) tableResponse {
	return tableResponse{
		ID:             e.Table.ID,
		BranchID:       e.Table.BranchID,
		ZoneID:         e.Table.ZoneID,
		Name:           e.Table.Name,
		Capacity:       e.Table.Capacity,
		Status:         string(e.Table.Status),
		LayoutPosition: e.Table.LayoutPosition,
		IsActive:       e.Table.IsActive,
		ActiveCheckID:  e.ActiveCheckID,
	}
}

// zonePlanResponse is GET /tables's actual response shape (Sprint-5 Wave 1 /
// Wave-2 contract): the floor plan grouped by zone so the cash register can
// render zone-labeled sections from this single request, with no follow-up
// call to GET /zones needed. Zone order matches
// TableRepo.ListTablesByBranch's ORDER BY (floor, zone name); tables within
// a zone are in table-name order.
type zonePlanResponse struct {
	ZoneID   uuid.UUID       `json:"zone_id"`
	ZoneName string          `json:"zone_name"`
	Floor    int             `json:"floor"`
	Tables   []tableResponse `json:"tables"`
}

// toZonePlanResponse groups TablePlanEntry rows (already ordered by
// zone floor/name, then table name — see ListTablesByBranch) into
// zone-labeled sections without re-sorting, preserving that order.
func toZonePlanResponse(entries []service.TablePlanEntry) []zonePlanResponse {
	out := []zonePlanResponse{}
	for _, e := range entries {
		if len(out) == 0 || out[len(out)-1].ZoneID != e.Table.ZoneID {
			out = append(out, zonePlanResponse{
				ZoneID:   e.Table.ZoneID,
				ZoneName: e.ZoneName,
				Floor:    e.ZoneFloor,
			})
		}
		last := &out[len(out)-1]
		last.Tables = append(last.Tables, toTableResponse(e))
	}
	return out
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
	if errors.Is(err, pub.ErrTableOccupied) {
		http.Error(w, "table is already occupied", http.StatusConflict)
		return
	}
	if errors.Is(err, pub.ErrTableBranchMismatch) {
		http.Error(w, "table does not belong to this branch", http.StatusUnprocessableEntity)
		return
	}
	if errors.Is(err, service.ErrManualOccupyForbidden) {
		http.Error(w, "table can only become occupied by opening a check", http.StatusUnprocessableEntity)
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
