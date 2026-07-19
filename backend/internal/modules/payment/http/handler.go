// Package http provides the HTTP layer for the payment module.
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
	engine   *auth.Engine
}

// Params groups fx-injected dependencies.
type Params struct {
	fx.In

	Payments *service.PaymentService
	Logger   *zap.Logger
	Cache    *redis.Client
	Engine   *auth.Engine
}

// HandlerWithCache wraps Handler with the Redis client needed for idempotency middleware.
type HandlerWithCache struct {
	h     *Handler
	cache *redis.Client
}

func NewHandler(p Params) *HandlerWithCache {
	return &HandlerWithCache{
		h:     &Handler{payments: p.Payments, logger: p.Logger, engine: p.Engine},
		cache: p.Cache,
	}
}

// permit builds per-route OPA authorization middleware (ADR-AUTH-001, layer 2).
func (h *Handler) permit(action string) func(http.Handler) http.Handler {
	return auth.RequirePermission(h.engine, action)
}

// RegisterRoutes mounts payment endpoints on the provided router.
// POST /api/v1/payments requires Idempotency-Key (ADR-SEC-003). RequirePermission
// is listed first in r.With so an unauthorized caller never reaches the
// idempotency reservation logic.
func (hwc *HandlerWithCache) RegisterRoutes(r *chi.Mux) {
	r.Route("/api/v1/payments", func(r chi.Router) {
		r.With(hwc.h.permit("payment.payment.read")).Get("/", hwc.h.listPayments)
		r.With(hwc.h.permit("payment.sale.register"), httpx.Idempotency(hwc.cache)).Post("/", hwc.h.registerSale)
		// Static segment: chi resolves it ahead of the /{id} wildcard below.
		r.With(hwc.h.permit("payment.fiscal_status.read")).Get("/fiscal-pending", hwc.h.fiscalPending)
		r.With(hwc.h.permit("payment.fiscal_status.read")).Get("/checks/{checkID}/settlement", hwc.h.checkSettlement)
		r.With(hwc.h.permit("payment.payment.read")).Get("/{id}", hwc.h.getPayment)
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

// paymentResponse is the single snake_case wire shape for every payment
// endpoint (listPayments, registerSale, getPayment). Before this fix,
// registerSale/getPayment serialized the domain.Payment struct directly —
// which has no json tags, so its fields went out verbatim in Go's default
// PascalCase — while listPayments alone went through this DTO. Routing all
// three through toPaymentResponse closes that inconsistency (see
// docs/lessons-from-b2b.md: handlers must not serialize domain structs).
type paymentResponse struct {
	ID              uuid.UUID  `json:"id"`
	TenantID        uuid.UUID  `json:"tenant_id"`
	BranchID        uuid.UUID  `json:"branch_id"`
	CheckID         *uuid.UUID `json:"check_id"`
	Method          string     `json:"method"`
	Status          string     `json:"status"`
	AmountTotal     int64      `json:"amount_total"`
	Currency        string     `json:"currency"`
	FiscalReceiptID *uuid.UUID `json:"fiscal_receipt_id"`
	CreatedAt       string     `json:"created_at"`
	CompletedAt     *string    `json:"completed_at"`
}

// registerSaleRequest is the POST /api/v1/payments body. Lines and meta are
// optional: when lines are omitted the service synthesizes a single-line basket
// for the total, which keeps the mock/dev flow working (ADR-FISCAL-002 §2).
type registerSaleRequest struct {
	BranchID       uuid.UUID           `json:"branch_id"`
	CheckID        *uuid.UUID          `json:"check_id"`
	Method         string              `json:"method"`
	AmountTotal    int64               `json:"amount_total"`
	Currency       string              `json:"currency"`
	Lines          []fiscalLineRequest `json:"lines"`
	Meta           fiscalMetaRequest   `json:"meta"`
	TerminalSerial string              `json:"terminal_serial"`
}

// fiscalLineRequest mirrors domain.FiscalLine. Quantities are thousandths
// (1000 = 1 unit) and tax rates permyriad (1000 = 10.00%).
type fiscalLineRequest struct {
	Name             string    `json:"name"`
	UnitPriceMinor   int64     `json:"unit_price_minor"`
	QuantityMilli    int64     `json:"quantity_milli"`
	TaxRatePermyriad int       `json:"tax_rate_permyriad"`
	CategoryID       uuid.UUID `json:"category_id"`
	Unit             string    `json:"unit"`
}

type fiscalMetaRequest struct {
	TableLabel  string `json:"table_label"`
	WaiterName  string `json:"waiter_name"`
	CheckNumber int    `json:"check_number"`
}

func (m fiscalMetaRequest) toDomain() domain.FiscalMeta {
	return domain.FiscalMeta{
		TableLabel:  m.TableLabel,
		WaiterName:  m.WaiterName,
		CheckNumber: m.CheckNumber,
	}
}

func toFiscalLines(lines []fiscalLineRequest) []domain.FiscalLine {
	if len(lines) == 0 {
		return nil
	}
	out := make([]domain.FiscalLine, len(lines))
	for i, l := range lines {
		out[i] = domain.FiscalLine{
			Name:             l.Name,
			UnitPriceMinor:   l.UnitPriceMinor,
			QuantityMilli:    l.QuantityMilli,
			TaxRatePermyriad: l.TaxRatePermyriad,
			CategoryID:       l.CategoryID,
			Unit:             l.Unit,
		}
	}
	return out
}

func toPaymentResponse(p domain.Payment) paymentResponse {
	resp := paymentResponse{
		ID:              p.ID,
		TenantID:        p.TenantID,
		BranchID:        p.BranchID,
		CheckID:         p.CheckID,
		Method:          string(p.Method),
		Status:          string(p.Status),
		AmountTotal:     p.AmountTotal,
		Currency:        p.Currency,
		FiscalReceiptID: p.FiscalReceiptID,
		CreatedAt:       p.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
	if p.CompletedAt != nil {
		completedAt := p.CompletedAt.Format("2006-01-02T15:04:05Z07:00")
		resp.CompletedAt = &completedAt
	}
	return resp
}

// listPayments calls either PaymentService.ListByCheck (when ?check_id= is
// present — completed payments only, used by POS to guard against double
// payment on a reopened check) or PaymentService.ListByTenant (tenant-wide
// reconciliation view, paginated). Both share the "payment.payment.read"
// permission — a check-scoped read is a strict subset of a tenant-wide one.
func (h *Handler) listPayments(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}

	if v := r.URL.Query().Get("check_id"); v != "" {
		checkID, err := uuid.Parse(v)
		if err != nil {
			http.Error(w, "invalid check_id", http.StatusBadRequest)
			return
		}
		payments, err := h.payments.ListByCheck(r.Context(), p.TenantID, checkID)
		if err != nil {
			h.logger.Error("payment: list by check", zap.Error(err))
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		out := make([]paymentResponse, len(payments))
		for i, pay := range payments {
			out[i] = toPaymentResponse(pay)
		}
		respondJSON(w, http.StatusOK, map[string]any{"payments": out})
		return
	}

	limit := 50
	offset := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	payments, err := h.payments.ListByTenant(r.Context(), p.TenantID, limit, offset)
	if err != nil {
		h.logger.Error("payment: list", zap.Error(err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	out := make([]paymentResponse, len(payments))
	for i, pay := range payments {
		out[i] = toPaymentResponse(pay)
	}
	respondJSON(w, http.StatusOK, map[string]any{"payments": out})
}

func (h *Handler) registerSale(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}

	var req registerSaleRequest
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

	// The payment comes back 'pending': fiscal registration is asynchronous
	// (ADR-FISCAL-002) and clients observe completion via GET or the WS feed.
	payment, err := h.payments.RegisterSale(r.Context(), service.RegisterSaleRequest{
		TenantID:       p.TenantID,
		BranchID:       req.BranchID,
		CheckID:        req.CheckID,
		IdempotencyKey: idempKey,
		Method:         domain.PaymentMethod(req.Method),
		AmountTotal:    req.AmountTotal,
		Currency:       req.Currency,
		Lines:          toFiscalLines(req.Lines),
		Meta:           req.Meta.toDomain(),
		TerminalSerial: req.TerminalSerial,
	})
	if err != nil {
		h.logger.Error("payment: register sale", zap.Error(err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	respondJSON(w, http.StatusCreated, toPaymentResponse(payment))
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
	respondJSON(w, http.StatusOK, toPaymentResponse(payment))
}

func respondJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
