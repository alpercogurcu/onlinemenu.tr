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

func (h *Handler) listStockItems(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	kind := domain.StockItemKind(r.URL.Query().Get("kind"))

	items, err := h.stockItems.List(r.Context(), p.TenantID, kind)
	if err != nil {
		h.logError(w, r, err)
		return
	}
	resp := make([]stockItemResponse, len(items))
	for i, item := range items {
		resp[i] = toStockItemResponse(item)
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

// timeLayout is the shared RFC3339 layout used across inventory JSON responses.
const timeLayout = "2006-01-02T15:04:05Z07:00"
