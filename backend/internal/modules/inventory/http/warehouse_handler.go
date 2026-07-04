package http

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"

	"onlinemenu.tr/internal/modules/inventory/domain"
	"onlinemenu.tr/internal/modules/inventory/service"
)

type warehouseRequest struct {
	BranchID      string `json:"branch_id"`
	Name          string `json:"name"`
	WarehouseType string `json:"warehouse_type"`
	IsActive      *bool  `json:"is_active,omitempty"`
}

type warehouseResponse struct {
	ID            string `json:"id"`
	BranchID      string `json:"branch_id"`
	Name          string `json:"name"`
	WarehouseType string `json:"warehouse_type"`
	IsActive      bool   `json:"is_active"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

func (h *Handler) createWarehouse(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}

	var req warehouseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	branchID, err := uuid.Parse(req.BranchID)
	if err != nil {
		http.Error(w, "invalid branch_id", http.StatusBadRequest)
		return
	}

	wh, err := h.warehouses.Create(r.Context(), p.TenantID, service.CreateWarehouseRequest{
		BranchID:      branchID,
		Name:          req.Name,
		WarehouseType: domain.WarehouseType(req.WarehouseType),
	})
	if err != nil {
		h.logError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toWarehouseResponse(wh))
}

func (h *Handler) listWarehouses(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}

	var branchID uuid.UUID
	if bs := r.URL.Query().Get("branch_id"); bs != "" {
		var err error
		branchID, err = uuid.Parse(bs)
		if err != nil {
			http.Error(w, "invalid branch_id", http.StatusBadRequest)
			return
		}
	}

	items, err := h.warehouses.List(r.Context(), p.TenantID, branchID)
	if err != nil {
		h.logError(w, r, err)
		return
	}
	resp := make([]warehouseResponse, len(items))
	for i, wh := range items {
		resp[i] = toWarehouseResponse(wh)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) getWarehouse(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	id, err := uuidParam(r, "id")
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	wh, err := h.warehouses.Get(r.Context(), p.TenantID, id)
	if err != nil {
		h.logError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toWarehouseResponse(wh))
}

func (h *Handler) updateWarehouse(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	id, err := uuidParam(r, "id")
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	var req warehouseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	branchID, err := uuid.Parse(req.BranchID)
	if err != nil {
		http.Error(w, "invalid branch_id", http.StatusBadRequest)
		return
	}
	isActive := true
	if req.IsActive != nil {
		isActive = *req.IsActive
	}

	wh, err := h.warehouses.Update(r.Context(), p.TenantID, p, service.UpdateWarehouseRequest{
		ID:            id,
		BranchID:      branchID,
		Name:          req.Name,
		WarehouseType: domain.WarehouseType(req.WarehouseType),
		IsActive:      isActive,
	})
	if err != nil {
		h.logError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toWarehouseResponse(wh))
}

func (h *Handler) deleteWarehouse(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	id, err := uuidParam(r, "id")
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	if err := h.warehouses.Delete(r.Context(), p.TenantID, p, id); err != nil {
		h.logError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func toWarehouseResponse(wh domain.Warehouse) warehouseResponse {
	return warehouseResponse{
		ID:            wh.ID.String(),
		BranchID:      wh.BranchID.String(),
		Name:          wh.Name,
		WarehouseType: string(wh.WarehouseType),
		IsActive:      wh.IsActive,
		CreatedAt:     wh.CreatedAt.Format(timeLayout),
		UpdatedAt:     wh.UpdatedAt.Format(timeLayout),
	}
}
