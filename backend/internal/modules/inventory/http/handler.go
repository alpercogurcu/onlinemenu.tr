// Package http implements the inventory module's REST API.
// All routes require a valid principal with TenantID in context (set by auth middleware).
package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/inventory/domain"
	pub "onlinemenu.tr/internal/modules/inventory/public"
	"onlinemenu.tr/internal/modules/inventory/service"
	"onlinemenu.tr/internal/platform/auth"
)

// Handler is the inventory HTTP handler.
type Handler struct {
	svc    *service.InventoryService
	logger *zap.Logger
	engine *auth.Engine
}

// Params groups fx-injected dependencies.
type Params struct {
	fx.In

	Svc    *service.InventoryService
	Logger *zap.Logger
	Engine *auth.Engine
}

// NewHandler constructs a Handler for fx injection.
func NewHandler(p Params) *Handler {
	return &Handler{svc: p.Svc, logger: p.Logger, engine: p.Engine}
}

// permit builds per-route OPA authorization middleware (ADR-AUTH-001, layer 2).
func (h *Handler) permit(action string) func(http.Handler) http.Handler {
	return auth.RequirePermission(h.engine, action)
}

// RegisterRoutes mounts inventory endpoints on the router.
func (h *Handler) RegisterRoutes(r *chi.Mux) {
	r.Route("/api/v1/inventory", func(r chi.Router) {
		r.With(h.permit("inventory.level.read")).Get("/levels", h.listLevels)
		r.With(h.permit("inventory.level.read")).Get("/levels/{productID}", h.getLevel)
		r.With(h.permit("inventory.transaction.create")).Post("/transactions", h.recordAdjustment)
		r.With(h.permit("inventory.transaction.read")).Get("/transactions", h.listTransactions)
	})
}

// ============================================================
// Request / response types
// ============================================================

type adjustmentRequest struct {
	BranchID      uuid.UUID  `json:"branch_id"`
	ProductID     uuid.UUID  `json:"product_id"`
	Type          string     `json:"type"`
	QuantityDelta float64    `json:"quantity_delta"`
	ReferenceID   *uuid.UUID `json:"reference_id,omitempty"`
	ReferenceType *string    `json:"reference_type,omitempty"`
	Notes         *string    `json:"notes,omitempty"`
}

type levelResponse struct {
	ID        uuid.UUID `json:"id"`
	ProductID uuid.UUID `json:"product_id"`
	BranchID  uuid.UUID `json:"branch_id"`
	Quantity  float64   `json:"quantity"`
	UpdatedAt string    `json:"updated_at"`
}

type transactionResponse struct {
	ID            uuid.UUID  `json:"id"`
	ProductID     uuid.UUID  `json:"product_id"`
	BranchID      uuid.UUID  `json:"branch_id"`
	Type          string     `json:"type"`
	QuantityDelta float64    `json:"quantity_delta"`
	ReferenceID   *uuid.UUID `json:"reference_id,omitempty"`
	ReferenceType *string    `json:"reference_type,omitempty"`
	Notes         *string    `json:"notes,omitempty"`
	CreatedAt     string     `json:"created_at"`
}

type recordAdjustmentResponse struct {
	Transaction transactionResponse `json:"transaction"`
	Level       levelResponse       `json:"level"`
}

// ============================================================
// Handlers
// ============================================================

func (h *Handler) listLevels(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	branchID, err := uuidQuery(r, "branch_id")
	if err != nil {
		http.Error(w, "branch_id query param is required and must be a valid UUID", http.StatusBadRequest)
		return
	}

	levels, err := h.svc.ListLevelsByBranch(r.Context(), p.TenantID, branchID)
	if err != nil {
		h.logError(w, r, err)
		return
	}

	resp := make([]levelResponse, len(levels))
	for i, l := range levels {
		resp[i] = toLevelResponse(l)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) getLevel(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	productID, err := uuidParam(r, "productID")
	if err != nil {
		http.Error(w, "invalid product_id", http.StatusBadRequest)
		return
	}
	branchID, err := uuidQuery(r, "branch_id")
	if err != nil {
		http.Error(w, "branch_id query param is required and must be a valid UUID", http.StatusBadRequest)
		return
	}

	lvl, err := h.svc.GetLevel(r.Context(), p.TenantID, branchID, productID)
	if err != nil {
		h.logError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toLevelResponse(lvl))
}

func (h *Handler) recordAdjustment(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}

	var req adjustmentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	var createdBy *uuid.UUID
	if p.PersonID != uuid.Nil {
		id := p.PersonID
		createdBy = &id
	}

	svcReq := service.RecordAdjustmentRequest{
		BranchID:      req.BranchID,
		ProductID:     req.ProductID,
		Type:          domain.TransactionType(req.Type),
		QuantityDelta: req.QuantityDelta,
		ReferenceID:   req.ReferenceID,
		ReferenceType: req.ReferenceType,
		Notes:         req.Notes,
		CreatedBy:     createdBy,
	}

	tx, lvl, err := h.svc.RecordAdjustment(r.Context(), p.TenantID, svcReq)
	if err != nil {
		h.logError(w, r, err)
		return
	}

	writeJSON(w, http.StatusCreated, recordAdjustmentResponse{
		Transaction: toTransactionResponse(tx),
		Level:       toLevelResponse(lvl),
	})
}

func (h *Handler) listTransactions(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	branchID, err := uuidQuery(r, "branch_id")
	if err != nil {
		http.Error(w, "branch_id query param is required and must be a valid UUID", http.StatusBadRequest)
		return
	}

	limit := 100
	if ls := r.URL.Query().Get("limit"); ls != "" {
		if n, err := strconv.Atoi(ls); err == nil && n > 0 {
			limit = n
		}
	}

	productIDStr := r.URL.Query().Get("product_id")
	if productIDStr != "" {
		productID, err := uuid.Parse(productIDStr)
		if err != nil {
			http.Error(w, "invalid product_id", http.StatusBadRequest)
			return
		}
		txs, err := h.svc.ListTransactionsByProduct(r.Context(), p.TenantID, branchID, productID, limit)
		if err != nil {
			h.logError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, toTransactionSlice(txs))
		return
	}

	txs, err := h.svc.ListTransactionsByBranch(r.Context(), p.TenantID, branchID, limit)
	if err != nil {
		h.logError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toTransactionSlice(txs))
}

// ============================================================
// Helpers
// ============================================================

func requirePrincipal(w http.ResponseWriter, r *http.Request) (auth.Principal, bool) {
	p, err := auth.FromContext(r.Context())
	if err != nil || p.TenantID == uuid.Nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return auth.Principal{}, false
	}
	return p, true
}

func (h *Handler) logError(w http.ResponseWriter, _ *http.Request, err error) {
	if errors.Is(err, pub.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	var ve *pub.ValidationError
	if errors.As(err, &ve) {
		http.Error(w, ve.Msg, http.StatusUnprocessableEntity)
		return
	}
	h.logger.Error("inventory handler error", zap.Error(err))
	http.Error(w, "internal server error", http.StatusInternalServerError)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func uuidParam(r *http.Request, key string) (uuid.UUID, error) {
	return uuid.Parse(chi.URLParam(r, key))
}

func uuidQuery(r *http.Request, key string) (uuid.UUID, error) {
	return uuid.Parse(r.URL.Query().Get(key))
}

func toLevelResponse(l domain.InventoryLevel) levelResponse {
	return levelResponse{
		ID:        l.ID,
		ProductID: l.ProductID,
		BranchID:  l.BranchID,
		Quantity:  l.Quantity,
		UpdatedAt: l.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

func toTransactionResponse(t domain.InventoryTransaction) transactionResponse {
	return transactionResponse{
		ID:            t.ID,
		ProductID:     t.ProductID,
		BranchID:      t.BranchID,
		Type:          string(t.Type),
		QuantityDelta: t.QuantityDelta,
		ReferenceID:   t.ReferenceID,
		ReferenceType: t.ReferenceType,
		Notes:         t.Notes,
		CreatedAt:     t.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

func toTransactionSlice(txs []domain.InventoryTransaction) []transactionResponse {
	resp := make([]transactionResponse, len(txs))
	for i, t := range txs {
		resp[i] = toTransactionResponse(t)
	}
	return resp
}
