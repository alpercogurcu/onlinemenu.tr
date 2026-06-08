// Package http provides the HTTP layer for the payment module.
package http

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"github.com/redis/go-redis/v9"

	"onlinemenu.tr/internal/modules/payment/domain"
	pub "onlinemenu.tr/internal/modules/payment/public"
	"onlinemenu.tr/internal/modules/payment/service"
	"onlinemenu.tr/internal/platform/auth"
	"onlinemenu.tr/internal/platform/httpx"
)

// Handler exposes payment REST endpoints.
type Handler struct {
	payments *service.PaymentService
	logger   *zap.Logger
}

// Params groups fx-injected dependencies.
type Params struct {
	fx.In

	Payments *service.PaymentService
	Logger   *zap.Logger
	Cache    *redis.Client
}

// HandlerWithCache wraps Handler with the Redis client needed for idempotency middleware.
type HandlerWithCache struct {
	h     *Handler
	cache *redis.Client
}

func NewHandler(p Params) *HandlerWithCache {
	return &HandlerWithCache{
		h:     &Handler{payments: p.Payments, logger: p.Logger},
		cache: p.Cache,
	}
}

// RegisterRoutes mounts payment endpoints on the provided router.
// POST /api/v1/payments requires Idempotency-Key (ADR-SEC-003).
func (hwc *HandlerWithCache) RegisterRoutes(r *chi.Mux) {
	r.Route("/api/v1/payments", func(r chi.Router) {
		r.With(httpx.Idempotency(hwc.cache)).Post("/", hwc.h.registerSale)
		r.Get("/{id}", hwc.h.getPayment)
	})
}

// requirePrincipal extracts and validates the auth principal.
func requirePrincipal(w http.ResponseWriter, r *http.Request) (auth.Principal, bool) {
	p, err := auth.FromContext(r.Context())
	if err != nil || p.TenantID == uuid.Nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return auth.Principal{}, false
	}
	return p, true
}

func (h *Handler) registerSale(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}

	var req struct {
		BranchID    uuid.UUID  `json:"branch_id"`
		CheckID     *uuid.UUID `json:"check_id"`
		Method      string     `json:"method"`
		AmountTotal int64      `json:"amount_total"`
		Currency    string     `json:"currency"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.BranchID == uuid.Nil {
		http.Error(w, "branch_id is required", http.StatusUnprocessableEntity)
		return
	}

	// Idempotency-Key is enforced by the middleware before we reach here.
	// We read it here for the service call.
	idempKey := r.Header.Get("Idempotency-Key")

	payment, err := h.payments.RegisterSale(r.Context(), service.RegisterSaleRequest{
		TenantID:       p.TenantID,
		BranchID:       req.BranchID,
		CheckID:        req.CheckID,
		IdempotencyKey: idempKey,
		Method:         domain.PaymentMethod(req.Method),
		AmountTotal:    req.AmountTotal,
		Currency:       req.Currency,
	})
	if err != nil {
		h.logger.Error("payment: register sale", zap.Error(err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	respondJSON(w, http.StatusCreated, payment)
}

func (h *Handler) getPayment(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	payment, err := h.payments.GetByID(r.Context(), p.TenantID, id)
	if err != nil {
		if errors.Is(err, pub.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		h.logger.Error("payment: get by id", zap.Error(err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	respondJSON(w, http.StatusOK, payment)
}

func respondJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
