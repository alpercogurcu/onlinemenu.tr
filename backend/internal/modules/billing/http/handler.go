// Package http provides the HTTP layer for the billing module.
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

	"onlinemenu.tr/internal/modules/billing/domain"
	"onlinemenu.tr/internal/modules/billing/service"
	"onlinemenu.tr/internal/platform/auth"
	"onlinemenu.tr/internal/platform/httpx"
)

// Handler exposes billing REST endpoints.
type Handler struct {
	billing *service.BillingService
	logger  *zap.Logger
	engine  *auth.Engine
}

// Params groups fx-injected dependencies.
type Params struct {
	fx.In

	Billing *service.BillingService
	Logger  *zap.Logger
	Cache   *redis.Client
	Engine  *auth.Engine
}

// HandlerWithCache wraps Handler with the Redis client for idempotency middleware.
type HandlerWithCache struct {
	h     *Handler
	cache *redis.Client
}

// New constructs a HandlerWithCache.
func New(p Params) *HandlerWithCache {
	return &HandlerWithCache{
		h:     &Handler{billing: p.Billing, logger: p.Logger, engine: p.Engine},
		cache: p.Cache,
	}
}

// permit builds per-route OPA authorization middleware (ADR-AUTH-001, layer 2).
func (h *Handler) permit(action string) func(http.Handler) http.Handler {
	return auth.RequirePermission(h.engine, action)
}

// RegisterRoutes mounts billing endpoints on the provided router.
// Faz 1: invoices are manager-only (see configs/opa/bundles/authz.rego).
func (hwc *HandlerWithCache) RegisterRoutes(r *chi.Mux) {
	r.Route("/api/v1/invoices", func(r chi.Router) {
		r.With(hwc.h.permit("billing.invoice.create"), httpx.Idempotency(hwc.cache)).Post("/", hwc.h.generate)
		r.With(hwc.h.permit("billing.invoice.read")).Get("/", hwc.h.list)
		r.With(hwc.h.permit("billing.invoice.read")).Get("/{id}", hwc.h.get)
		r.With(hwc.h.permit("billing.invoice.retry")).Post("/{id}/retry", hwc.h.retry)
	})
}

func (h *Handler) generate(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}

	var body struct {
		BranchID     uuid.UUID      `json:"branch_id"`
		InvoiceType  string         `json:"invoice_type"`
		CheckID      *uuid.UUID     `json:"check_id"`
		PaymentID    *uuid.UUID     `json:"payment_id"`
		SupplierVKN  string         `json:"supplier_vkn"`
		SupplierName string         `json:"supplier_name"`
		SupplierAlias string        `json:"supplier_alias"`
		CustomerVKN  string         `json:"customer_vkn"`
		CustomerName string         `json:"customer_name"`
		Items        []itemRequest  `json:"items"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey == "" {
		http.Error(w, "Idempotency-Key header required", http.StatusBadRequest)
		return
	}

	invType := domain.InvoiceType(body.InvoiceType)
	if invType == "" {
		invType = domain.InvoiceTypeEArsiv
	}

	items := make([]service.InvoiceItemRequest, len(body.Items))
	for i, item := range body.Items {
		items[i] = service.InvoiceItemRequest{
			ProductID:       item.ProductID,
			ProductName:     item.ProductName,
			Quantity:        item.Quantity,
			UnitPriceAmount: item.UnitPriceAmount,
			TaxRateBPS:      item.TaxRateBPS,
		}
	}

	inv, err := h.billing.GenerateInvoice(r.Context(), service.GenerateInvoiceRequest{
		TenantID:      p.TenantID,
		BranchID:      body.BranchID,
		InvoiceType:   invType,
		IdempotencyKey: idempotencyKey,
		CheckID:       body.CheckID,
		PaymentID:     body.PaymentID,
		SupplierVKN:   body.SupplierVKN,
		SupplierName:  body.SupplierName,
		SupplierAlias: body.SupplierAlias,
		CustomerVKN:   body.CustomerVKN,
		CustomerName:  body.CustomerName,
		Items:         items,
	})
	if err != nil {
		h.logger.Error("billing: generate invoice", zap.Error(err))
		http.Error(w, "failed to generate invoice", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusCreated, invoiceResponse(inv))
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}

	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		http.Error(w, "invalid invoice ID", http.StatusBadRequest)
		return
	}

	inv, err := h.billing.GetInvoice(r.Context(), p.TenantID, id)
	if errors.Is(err, service.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		h.logger.Error("billing: get invoice", zap.Error(err))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, invoiceResponse(inv))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}

	limit := 50
	offset := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}

	invoices, err := h.billing.ListInvoices(r.Context(), p.TenantID, limit, offset)
	if err != nil {
		h.logger.Error("billing: list invoices", zap.Error(err))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	resp := make([]any, len(invoices))
	for i, inv := range invoices {
		resp[i] = invoiceResponse(inv)
	}
	writeJSON(w, http.StatusOK, map[string]any{"invoices": resp})
}

func (h *Handler) retry(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}

	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		http.Error(w, "invalid invoice ID", http.StatusBadRequest)
		return
	}

	inv, err := h.billing.RetrySubmission(r.Context(), p.TenantID, id)
	if errors.Is(err, service.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		h.logger.Error("billing: retry submission", zap.Error(err))
		http.Error(w, "retry failed: "+err.Error(), http.StatusUnprocessableEntity)
		return
	}

	writeJSON(w, http.StatusOK, invoiceResponse(inv))
}

// itemRequest is the JSON representation of an invoice line item in the request body.
type itemRequest struct {
	ProductID       *uuid.UUID `json:"product_id"`
	ProductName     string     `json:"product_name"`
	Quantity        int32      `json:"quantity"`
	UnitPriceAmount int64      `json:"unit_price_amount"`
	TaxRateBPS      int32      `json:"tax_rate_bps"`
}

func invoiceResponse(inv domain.Invoice) map[string]any {
	return map[string]any{
		"id":                   inv.ID,
		"invoice_type":         string(inv.InvoiceType),
		"status":               string(inv.Status),
		"check_id":             inv.CheckID,
		"payment_id":           inv.PaymentID,
		"invoice_number":       inv.InvoiceNumber,
		"gib_uuid":             inv.GibUUID,
		"external_id":          inv.ExternalID,
		"supplier_vkn":         inv.SupplierVKN,
		"supplier_name":        inv.SupplierName,
		"customer_vkn":         inv.CustomerVKN,
		"customer_name":        inv.CustomerName,
		"amount_excluding_tax": inv.AmountExcludingTax,
		"tax_amount":           inv.TaxAmount,
		"amount_total":         inv.AmountTotal,
		"currency":             inv.Currency,
		"issue_date":           inv.IssueDate.Format("2006-01-02"),
		"submitted_at":         inv.SubmittedAt,
		"accepted_at":          inv.AcceptedAt,
		"created_at":           inv.CreatedAt,
	}
}

func requirePrincipal(w http.ResponseWriter, r *http.Request) (auth.Principal, bool) {
	p, err := auth.FromContext(r.Context())
	if err != nil || p.TenantID == uuid.Nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return auth.Principal{}, false
	}
	return p, true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}
