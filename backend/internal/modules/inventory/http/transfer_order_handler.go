package http

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"

	"onlinemenu.tr/internal/modules/inventory/domain"
	"onlinemenu.tr/internal/modules/inventory/service"
	"onlinemenu.tr/internal/platform/auth"
)

type transferOrderItemRequest struct {
	StockItemID  uuid.UUID `json:"stock_item_id"`
	RequestedQty float64   `json:"requested_qty"`
	Unit         string    `json:"unit"`
	Note         string    `json:"note,omitempty"`
}

type createTransferOrderRequest struct {
	RequestingBranchID    uuid.UUID                  `json:"requesting_branch_id"`
	SourceBranchID        uuid.UUID                  `json:"source_branch_id"`
	Priority              string                     `json:"priority,omitempty"`
	RequestedDeliveryDate *string                    `json:"requested_delivery_date,omitempty"`
	Note                  string                     `json:"note,omitempty"`
	Items                 []transferOrderItemRequest `json:"items"`
}

type transferOrderItemResponse struct {
	ID           uuid.UUID `json:"id"`
	StockItemID  uuid.UUID `json:"stock_item_id"`
	RequestedQty float64   `json:"requested_qty"`
	ApprovedQty  *float64  `json:"approved_qty,omitempty"`
	ShippedQty   float64   `json:"shipped_qty"`
	ReceivedQty  float64   `json:"received_qty"`
	Unit         string    `json:"unit"`
	Note         string    `json:"note,omitempty"`
}

type transferOrderResponse struct {
	ID                    uuid.UUID                   `json:"id"`
	RequestingBranchID    uuid.UUID                   `json:"requesting_branch_id"`
	SourceBranchID        uuid.UUID                   `json:"source_branch_id"`
	Status                string                      `json:"status"`
	Priority              string                      `json:"priority"`
	RequestedDeliveryDate *string                     `json:"requested_delivery_date,omitempty"`
	Note                  string                      `json:"note,omitempty"`
	Items                 []transferOrderItemResponse `json:"items,omitempty"`
	CreatedAt             string                      `json:"created_at"`
	UpdatedAt             string                      `json:"updated_at"`
}

type approveTransferOrderRequest struct {
	ApprovedBy uuid.UUID `json:"approved_by"`
	Items      []struct {
		StockItemID uuid.UUID `json:"stock_item_id"`
		ApprovedQty float64   `json:"approved_qty"`
	} `json:"items"`
}

func (h *Handler) createTransferOrder(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}

	var req createTransferOrderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	items := make([]service.CreateTransferOrderItemRequest, len(req.Items))
	for i, it := range req.Items {
		items[i] = service.CreateTransferOrderItemRequest{
			StockItemID:  it.StockItemID,
			RequestedQty: it.RequestedQty,
			Unit:         it.Unit,
			Note:         it.Note,
		}
	}

	var createdBy *uuid.UUID
	if p.PersonID != uuid.Nil {
		id := p.PersonID
		createdBy = &id
	}

	var deliveryDate *time.Time
	if req.RequestedDeliveryDate != nil && *req.RequestedDeliveryDate != "" {
		d, err := time.Parse("2006-01-02", *req.RequestedDeliveryDate)
		if err != nil {
			http.Error(w, "invalid requested_delivery_date (expected YYYY-MM-DD)", http.StatusBadRequest)
			return
		}
		deliveryDate = &d
	}

	order, orderItems, err := h.transfers.Create(r.Context(), p.TenantID, service.CreateTransferOrderRequest{
		RequestingBranchID:    req.RequestingBranchID,
		SourceBranchID:        req.SourceBranchID,
		Priority:              domain.Priority(req.Priority),
		RequestedDeliveryDate: deliveryDate,
		Note:                  req.Note,
		CreatedBy:             createdBy,
		Items:                 items,
	})
	if err != nil {
		h.logError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toTransferOrderResponse(order, orderItems))
}

func (h *Handler) listTransferOrders(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}

	var (
		orders []domain.BranchTransferOrder
		err    error
	)
	if bs := r.URL.Query().Get("requesting_branch_id"); bs != "" {
		branchID, perr := uuid.Parse(bs)
		if perr != nil {
			http.Error(w, "invalid requesting_branch_id", http.StatusBadRequest)
			return
		}
		orders, err = h.transfers.ListByRequestingBranch(r.Context(), p.TenantID, branchID)
	} else if bs := r.URL.Query().Get("source_branch_id"); bs != "" {
		branchID, perr := uuid.Parse(bs)
		if perr != nil {
			http.Error(w, "invalid source_branch_id", http.StatusBadRequest)
			return
		}
		orders, err = h.transfers.ListBySourceBranch(r.Context(), p.TenantID, branchID)
	} else {
		http.Error(w, "requesting_branch_id or source_branch_id query param is required", http.StatusBadRequest)
		return
	}
	if err != nil {
		h.logError(w, r, err)
		return
	}

	resp := make([]transferOrderResponse, len(orders))
	for i, o := range orders {
		resp[i] = toTransferOrderResponse(o, nil)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) getTransferOrder(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	id, err := uuidParam(r, "id")
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	order, err := h.transfers.Get(r.Context(), p.TenantID, id)
	if err != nil {
		h.logError(w, r, err)
		return
	}
	items, err := h.transfers.ListItems(r.Context(), p.TenantID, id)
	if err != nil {
		h.logError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toTransferOrderResponse(order, items))
}

func (h *Handler) submitTransferOrder(w http.ResponseWriter, r *http.Request) {
	h.transferOrderTransition(w, r, func(hr *http.Request, tenantID uuid.UUID, principal auth.Principal, id uuid.UUID) (domain.BranchTransferOrder, error) {
		return h.transfers.Submit(hr.Context(), tenantID, principal, id)
	})
}

func (h *Handler) rejectTransferOrder(w http.ResponseWriter, r *http.Request) {
	h.transferOrderTransition(w, r, func(hr *http.Request, tenantID uuid.UUID, principal auth.Principal, id uuid.UUID) (domain.BranchTransferOrder, error) {
		return h.transfers.Reject(hr.Context(), tenantID, principal, id)
	})
}

func (h *Handler) cancelTransferOrder(w http.ResponseWriter, r *http.Request) {
	h.transferOrderTransition(w, r, func(hr *http.Request, tenantID uuid.UUID, principal auth.Principal, id uuid.UUID) (domain.BranchTransferOrder, error) {
		return h.transfers.Cancel(hr.Context(), tenantID, principal, id)
	})
}

func (h *Handler) fulfilTransferOrder(w http.ResponseWriter, r *http.Request) {
	h.transferOrderTransition(w, r, func(hr *http.Request, tenantID uuid.UUID, principal auth.Principal, id uuid.UUID) (domain.BranchTransferOrder, error) {
		return h.transfers.Fulfil(hr.Context(), tenantID, principal, id)
	})
}

func (h *Handler) approveTransferOrder(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	id, err := uuidParam(r, "id")
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	var req approveTransferOrderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	approvedBy := req.ApprovedBy
	if approvedBy == uuid.Nil {
		approvedBy = p.PersonID
	}

	approvals := make([]service.ApprovalItem, len(req.Items))
	for i, it := range req.Items {
		approvals[i] = service.ApprovalItem{StockItemID: it.StockItemID, ApprovedQty: it.ApprovedQty}
	}

	order, err := h.transfers.Approve(r.Context(), p.TenantID, p, id, approvedBy, approvals)
	if err != nil {
		h.logError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toTransferOrderResponse(order, nil))
}

func (h *Handler) transferOrderTransition(w http.ResponseWriter, r *http.Request, fn func(*http.Request, uuid.UUID, auth.Principal, uuid.UUID) (domain.BranchTransferOrder, error)) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	id, err := uuidParam(r, "id")
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	order, err := fn(r, p.TenantID, p, id)
	if err != nil {
		h.logError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toTransferOrderResponse(order, nil))
}

func toTransferOrderResponse(o domain.BranchTransferOrder, items []domain.BranchTransferOrderItem) transferOrderResponse {
	resp := transferOrderResponse{
		ID:                 o.ID,
		RequestingBranchID: o.RequestingBranchID,
		SourceBranchID:     o.SourceBranchID,
		Status:             string(o.Status),
		Priority:           string(o.Priority),
		Note:               o.Note,
		CreatedAt:          o.CreatedAt.Format(timeLayout),
		UpdatedAt:          o.UpdatedAt.Format(timeLayout),
	}
	if o.RequestedDeliveryDate != nil {
		d := o.RequestedDeliveryDate.Format("2006-01-02")
		resp.RequestedDeliveryDate = &d
	}
	if items != nil {
		resp.Items = make([]transferOrderItemResponse, len(items))
		for i, it := range items {
			resp.Items[i] = transferOrderItemResponse{
				ID:           it.ID,
				StockItemID:  it.StockItemID,
				RequestedQty: it.RequestedQty,
				ApprovedQty:  it.ApprovedQty,
				ShippedQty:   it.ShippedQty,
				ReceivedQty:  it.ReceivedQty,
				Unit:         it.Unit,
				Note:         it.Note,
			}
		}
	}
	return resp
}
