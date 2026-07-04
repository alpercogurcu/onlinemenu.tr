package http

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"

	"onlinemenu.tr/internal/modules/inventory/domain"
	"onlinemenu.tr/internal/modules/inventory/service"
)

// receiptDateLayout is the date-only layout for purchase_receipts.receipt_date.
const receiptDateLayout = "2006-01-02"

type purchaseReceiptItemRequest struct {
	StockItemID uuid.UUID `json:"stock_item_id"`
	Quantity    float64   `json:"quantity"`
	Unit        string    `json:"unit"`
	UnitPrice   float64   `json:"unit_price"`
	LineTotal   float64   `json:"line_total,omitempty"`
	Brand       string    `json:"brand,omitempty"`
}

type createPurchaseReceiptRequest struct {
	WarehouseID     uuid.UUID                    `json:"warehouse_id"`
	SupplierPartyID *uuid.UUID                   `json:"supplier_party_id,omitempty"`
	SupplierName    string                       `json:"supplier_name,omitempty"`
	ReceiptNo       string                       `json:"receipt_no,omitempty"`
	ReceiptDate     string                       `json:"receipt_date,omitempty"`
	Total           float64                      `json:"total,omitempty"`
	Currency        string                       `json:"currency,omitempty"`
	Note            string                       `json:"note,omitempty"`
	Items           []purchaseReceiptItemRequest `json:"items"`
}

type purchaseReceiptItemResponse struct {
	ID          uuid.UUID `json:"id"`
	StockItemID uuid.UUID `json:"stock_item_id"`
	Quantity    float64   `json:"quantity"`
	Unit        string    `json:"unit"`
	UnitPrice   float64   `json:"unit_price"`
	LineTotal   float64   `json:"line_total"`
	Brand       string    `json:"brand,omitempty"`
}

type purchaseReceiptResponse struct {
	ID              uuid.UUID                     `json:"id"`
	WarehouseID     uuid.UUID                     `json:"warehouse_id"`
	SupplierPartyID *uuid.UUID                    `json:"supplier_party_id,omitempty"`
	SupplierName    string                        `json:"supplier_name,omitempty"`
	ReceiptNo       string                        `json:"receipt_no,omitempty"`
	ReceiptDate     string                        `json:"receipt_date"`
	Total           float64                       `json:"total"`
	Currency        string                        `json:"currency"`
	Note            string                        `json:"note,omitempty"`
	Items           []purchaseReceiptItemResponse `json:"items,omitempty"`
	CreatedAt       string                        `json:"created_at"`
}

func (h *Handler) createPurchaseReceipt(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}

	var req createPurchaseReceiptRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	var receiptDate time.Time
	if req.ReceiptDate != "" {
		t, err := time.Parse(receiptDateLayout, req.ReceiptDate)
		if err != nil {
			http.Error(w, "invalid receipt_date (expected YYYY-MM-DD)", http.StatusBadRequest)
			return
		}
		receiptDate = t
	}

	items := make([]service.CreateReceiptItemRequest, len(req.Items))
	for i, it := range req.Items {
		items[i] = service.CreateReceiptItemRequest{
			StockItemID: it.StockItemID,
			Quantity:    it.Quantity,
			Unit:        it.Unit,
			UnitPrice:   it.UnitPrice,
			LineTotal:   it.LineTotal,
			Brand:       it.Brand,
		}
	}

	var createdBy *uuid.UUID
	if p.PersonID != uuid.Nil {
		id := p.PersonID
		createdBy = &id
	}

	rcpt, rcptItems, err := h.purchaseReceipts.CreateReceipt(r.Context(), p.TenantID, p, service.CreateReceiptRequest{
		WarehouseID:     req.WarehouseID,
		SupplierPartyID: req.SupplierPartyID,
		SupplierName:    req.SupplierName,
		ReceiptNo:       req.ReceiptNo,
		ReceiptDate:     receiptDate,
		Total:           req.Total,
		Currency:        req.Currency,
		Note:            req.Note,
		CreatedBy:       createdBy,
		Items:           items,
	})
	if err != nil {
		h.logError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toPurchaseReceiptResponse(rcpt, rcptItems))
}

func (h *Handler) listPurchaseReceipts(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	warehouseID, err := uuidQuery(r, "warehouse_id")
	if err != nil {
		http.Error(w, "warehouse_id query param is required and must be a valid UUID", http.StatusBadRequest)
		return
	}

	receipts, err := h.purchaseReceipts.ListByWarehouse(r.Context(), p.TenantID, warehouseID)
	if err != nil {
		h.logError(w, r, err)
		return
	}
	resp := make([]purchaseReceiptResponse, len(receipts))
	for i, rcpt := range receipts {
		resp[i] = toPurchaseReceiptResponse(rcpt, nil)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) getPurchaseReceipt(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	id, err := uuidParam(r, "id")
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	rcpt, err := h.purchaseReceipts.Get(r.Context(), p.TenantID, id)
	if err != nil {
		h.logError(w, r, err)
		return
	}
	items, err := h.purchaseReceipts.ListItems(r.Context(), p.TenantID, id)
	if err != nil {
		h.logError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toPurchaseReceiptResponse(rcpt, items))
}

func toPurchaseReceiptResponse(rcpt domain.PurchaseReceipt, items []domain.PurchaseReceiptItem) purchaseReceiptResponse {
	resp := purchaseReceiptResponse{
		ID:              rcpt.ID,
		WarehouseID:     rcpt.WarehouseID,
		SupplierPartyID: rcpt.SupplierPartyID,
		SupplierName:    rcpt.SupplierName,
		ReceiptNo:       rcpt.ReceiptNo,
		ReceiptDate:     rcpt.ReceiptDate.Format(receiptDateLayout),
		Total:           rcpt.Total,
		Currency:        rcpt.Currency,
		Note:            rcpt.Note,
		CreatedAt:       rcpt.CreatedAt.Format(timeLayout),
	}
	if items != nil {
		resp.Items = make([]purchaseReceiptItemResponse, len(items))
		for i, it := range items {
			resp.Items[i] = purchaseReceiptItemResponse{
				ID:          it.ID,
				StockItemID: it.StockItemID,
				Quantity:    it.Quantity,
				Unit:        it.Unit,
				UnitPrice:   it.UnitPrice,
				LineTotal:   it.LineTotal,
				Brand:       it.Brand,
			}
		}
	}
	return resp
}
