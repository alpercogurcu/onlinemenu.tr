package http

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"

	"onlinemenu.tr/internal/modules/inventory/domain"
	"onlinemenu.tr/internal/modules/inventory/service"
)

type shipmentItemRequest struct {
	StockItemID  uuid.UUID `json:"stock_item_id"`
	RequestedQty float64   `json:"requested_qty"`
	Unit         string    `json:"unit"`
	// UnitPrice/Currency are an optional per-line override (ADR-DATA-006
	// eklenti): when omitted and the shipment is linked to a BTO, the price
	// is copied from the matching branch_transfer_order_items row instead.
	UnitPrice *float64 `json:"unit_price,omitempty"`
	Currency  string   `json:"currency,omitempty"`
}

type createShipmentRequest struct {
	FromWarehouseID uuid.UUID             `json:"from_warehouse_id"`
	ToBranchID      uuid.UUID             `json:"to_branch_id"`
	TransferOrderID *uuid.UUID            `json:"transfer_order_id,omitempty"`
	Priority        string                `json:"priority,omitempty"`
	Note            string                `json:"note,omitempty"`
	Items           []shipmentItemRequest `json:"items"`
}

type shipmentItemResponse struct {
	StockItemID  uuid.UUID `json:"stock_item_id"`
	RequestedQty float64   `json:"requested_qty"`
	ShippedQty   float64   `json:"shipped_qty"`
	ReceivedQty  float64   `json:"received_qty"`
	Unit         string    `json:"unit"`
	UnitPrice    *float64  `json:"unit_price,omitempty"`
	Currency     *string   `json:"currency,omitempty"`
}

type shipmentResponse struct {
	ID              uuid.UUID              `json:"id"`
	FromWarehouseID uuid.UUID              `json:"from_warehouse_id"`
	ToBranchID      uuid.UUID              `json:"to_branch_id"`
	TransferOrderID *uuid.UUID             `json:"transfer_order_id,omitempty"`
	Status          string                 `json:"status"`
	Priority        string                 `json:"priority"`
	Note            string                 `json:"note,omitempty"`
	Items           []shipmentItemResponse `json:"items,omitempty"`
	ShippedAt       *string                `json:"shipped_at,omitempty"`
	ReceivedAt      *string                `json:"received_at,omitempty"`
	CreatedAt       string                 `json:"created_at"`
	UpdatedAt       string                 `json:"updated_at"`
}

type receiveShipmentRequest struct {
	ToWarehouseID uuid.UUID `json:"to_warehouse_id"`
}

func (h *Handler) createShipment(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}

	var req createShipmentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	items := make([]service.CreateShipmentItemRequest, len(req.Items))
	for i, it := range req.Items {
		items[i] = service.CreateShipmentItemRequest{
			StockItemID:  it.StockItemID,
			RequestedQty: it.RequestedQty,
			Unit:         it.Unit,
			UnitPrice:    it.UnitPrice,
			Currency:     it.Currency,
		}
	}

	var createdBy *uuid.UUID
	if p.PersonID != uuid.Nil {
		id := p.PersonID
		createdBy = &id
	}

	sh, shItems, err := h.shipments.Create(r.Context(), p.TenantID, p, service.CreateShipmentRequest{
		FromWarehouseID: req.FromWarehouseID,
		ToBranchID:      req.ToBranchID,
		TransferOrderID: req.TransferOrderID,
		Priority:        domain.Priority(req.Priority),
		Note:            req.Note,
		CreatedBy:       createdBy,
		Items:           items,
	})
	if err != nil {
		h.logError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toShipmentResponse(sh, shItems))
}

func (h *Handler) listShipments(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	warehouseID, err := uuidQuery(r, "warehouse_id")
	if err != nil {
		http.Error(w, "warehouse_id query param is required and must be a valid UUID", http.StatusBadRequest)
		return
	}

	shipments, err := h.shipments.ListByWarehouse(r.Context(), p.TenantID, warehouseID)
	if err != nil {
		h.logError(w, r, err)
		return
	}
	resp := make([]shipmentResponse, len(shipments))
	for i, sh := range shipments {
		resp[i] = toShipmentResponse(sh, nil)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) getShipment(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	id, err := uuidParam(r, "id")
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	sh, err := h.shipments.Get(r.Context(), p.TenantID, id)
	if err != nil {
		h.logError(w, r, err)
		return
	}
	items, err := h.shipments.ListItems(r.Context(), p.TenantID, id)
	if err != nil {
		h.logError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toShipmentResponse(sh, items))
}

func (h *Handler) approveShipment(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	id, err := uuidParam(r, "id")
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	sh, err := h.shipments.Approve(r.Context(), p.TenantID, p, id)
	if err != nil {
		h.logError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toShipmentResponse(sh, nil))
}

func (h *Handler) advanceShipment(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	id, err := uuidParam(r, "id")
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	sh, err := h.shipments.Advance(r.Context(), p.TenantID, p, id)
	if err != nil {
		h.logError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toShipmentResponse(sh, nil))
}

func (h *Handler) receiveShipment(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	id, err := uuidParam(r, "id")
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	var req receiveShipmentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	sh, err := h.shipments.Receive(r.Context(), p.TenantID, p, id, req.ToWarehouseID)
	if err != nil {
		h.logError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toShipmentResponse(sh, nil))
}

func (h *Handler) cancelShipment(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	id, err := uuidParam(r, "id")
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	sh, err := h.shipments.Cancel(r.Context(), p.TenantID, p, id)
	if err != nil {
		h.logError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toShipmentResponse(sh, nil))
}

func toShipmentResponse(sh domain.Shipment, items []domain.ShipmentItem) shipmentResponse {
	resp := shipmentResponse{
		ID:              sh.ID,
		FromWarehouseID: sh.FromWarehouseID,
		ToBranchID:      sh.ToBranchID,
		TransferOrderID: sh.TransferOrderID,
		Status:          string(sh.Status),
		Priority:        string(sh.Priority),
		Note:            sh.Note,
		CreatedAt:       sh.CreatedAt.Format(timeLayout),
		UpdatedAt:       sh.UpdatedAt.Format(timeLayout),
	}
	if sh.ShippedAt != nil {
		s := sh.ShippedAt.Format(timeLayout)
		resp.ShippedAt = &s
	}
	if sh.ReceivedAt != nil {
		s := sh.ReceivedAt.Format(timeLayout)
		resp.ReceivedAt = &s
	}
	if items != nil {
		resp.Items = make([]shipmentItemResponse, len(items))
		for i, it := range items {
			resp.Items[i] = shipmentItemResponse{
				StockItemID:  it.StockItemID,
				RequestedQty: it.RequestedQty,
				ShippedQty:   it.ShippedQty,
				ReceivedQty:  it.ReceivedQty,
				Unit:         it.Unit,
				UnitPrice:    it.UnitPrice,
				Currency:     it.Currency,
			}
		}
	}
	return resp
}
