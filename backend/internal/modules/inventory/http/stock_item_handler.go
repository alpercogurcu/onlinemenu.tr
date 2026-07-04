package http

import (
	"encoding/json"
	"net/http"

	"onlinemenu.tr/internal/modules/inventory/domain"
	"onlinemenu.tr/internal/modules/inventory/service"
)

type stockItemRequest struct {
	SKU           string `json:"sku"`
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	CanonicalUnit string `json:"canonical_unit"`
	Category      string `json:"category,omitempty"`
	IsActive      *bool  `json:"is_active,omitempty"`
}

// stockItemResponse is the FULL projection: rendered for stock items the
// acting principal is entitled to see in detail (ADR-DATA-007 mode
// approved_suppliers/free, or any item seen by a tenant-wide-scoped
// principal). SupplyMode is only populated by listStockItems (it has no
// meaning for a single Create/Get/Update call, which is why omitempty keeps
// the key absent there rather than emitting an empty string).
type stockItemResponse struct {
	ID            string `json:"id"`
	SKU           string `json:"sku"`
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	CanonicalUnit string `json:"canonical_unit"`
	Category      string `json:"category,omitempty"`
	IsActive      bool   `json:"is_active"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
	SupplyMode    string `json:"supply_mode,omitempty"`
}

// restrictedStockItemResponse is the RESTRICTED projection for an
// exclusive_hq-mode item viewed by a branch-scoped principal (ADR-DATA-007
// / DATA-005 İlke 4 revizyonu): only the BTO catalog fields a branch needs
// to place an order. Cost/supplier/category/status fields are not merely
// empty here — the JSON key itself does not exist, because this is a
// distinct Go type, not the full struct with fields blanked out
// (docs/lessons-from-b2b.md: visibility must be field absence, never an
// opt-in row flag or a same-shape-but-empty response).
type restrictedStockItemResponse struct {
	ID            string `json:"id"`
	SKU           string `json:"sku"`
	Name          string `json:"name"`
	CanonicalUnit string `json:"canonical_unit"`
}

func (h *Handler) createStockItem(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}

	var req stockItemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	item, err := h.stockItems.Create(r.Context(), p.TenantID, service.CreateStockItemRequest{
		SKU:           req.SKU,
		Name:          req.Name,
		Kind:          domain.StockItemKind(req.Kind),
		CanonicalUnit: req.CanonicalUnit,
		Category:      req.Category,
	})
	if err != nil {
		h.logError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toStockItemResponse(item))
}

// listStockItems renders each item as EITHER the full projection OR the
// restricted BTO-catalog-only projection, per its ADR-DATA-007 resolved
// supply mode for the acting principal (service.StockItemService.List does
// the resolution; this handler only picks which DTO type to serialize).
// The response is []any because the two DTO types have genuinely different
// JSON shapes — a single shared struct with omitempty would still emit
// empty-valued keys, which is exactly the leak this projection must avoid.
func (h *Handler) listStockItems(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	kind := domain.StockItemKind(r.URL.Query().Get("kind"))

	views, err := h.stockItems.List(r.Context(), p.TenantID, kind, p)
	if err != nil {
		h.logError(w, r, err)
		return
	}
	resp := make([]any, len(views))
	for i, v := range views {
		if v.Restricted {
			resp[i] = toRestrictedStockItemResponse(v.Item)
			continue
		}
		resp[i] = toFullStockItemResponse(v.Item, v.Mode)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) getStockItem(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	id, err := uuidParam(r, "id")
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	item, err := h.stockItems.Get(r.Context(), p.TenantID, id)
	if err != nil {
		h.logError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toStockItemResponse(item))
}

func (h *Handler) updateStockItem(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	id, err := uuidParam(r, "id")
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	var req stockItemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	isActive := true
	if req.IsActive != nil {
		isActive = *req.IsActive
	}

	item, err := h.stockItems.Update(r.Context(), p.TenantID, service.UpdateStockItemRequest{
		ID:            id,
		SKU:           req.SKU,
		Name:          req.Name,
		Kind:          domain.StockItemKind(req.Kind),
		CanonicalUnit: req.CanonicalUnit,
		Category:      req.Category,
		IsActive:      isActive,
	})
	if err != nil {
		h.logError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toStockItemResponse(item))
}

func (h *Handler) deleteStockItem(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	id, err := uuidParam(r, "id")
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	if err := h.stockItems.Delete(r.Context(), p.TenantID, id); err != nil {
		h.logError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func toStockItemResponse(item domain.StockItem) stockItemResponse {
	return stockItemResponse{
		ID:            item.ID.String(),
		SKU:           item.SKU,
		Name:          item.Name,
		Kind:          string(item.Kind),
		CanonicalUnit: item.CanonicalUnit,
		Category:      item.Category,
		IsActive:      item.IsActive,
		CreatedAt:     item.CreatedAt.Format(timeLayout),
		UpdatedAt:     item.UpdatedAt.Format(timeLayout),
	}
}

// toFullStockItemResponse is toStockItemResponse plus the resolved
// ADR-DATA-007 supply mode, used only by listStockItems's unrestricted branch.
func toFullStockItemResponse(item domain.StockItem, mode domain.SupplyMode) stockItemResponse {
	resp := toStockItemResponse(item)
	resp.SupplyMode = string(mode)
	return resp
}

// toRestrictedStockItemResponse renders the BTO-catalog-only projection for
// an exclusive_hq item viewed by a branch-scoped principal.
func toRestrictedStockItemResponse(item domain.StockItem) restrictedStockItemResponse {
	return restrictedStockItemResponse{
		ID:            item.ID.String(),
		SKU:           item.SKU,
		Name:          item.Name,
		CanonicalUnit: item.CanonicalUnit,
	}
}

// timeLayout is the shared RFC3339 layout used across inventory JSON responses.
const timeLayout = "2006-01-02T15:04:05Z07:00"
