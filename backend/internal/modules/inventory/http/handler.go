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
	svc              *service.InventoryService
	stockItems       *service.StockItemService
	warehouses       *service.WarehouseService
	transfers        *service.TransferOrderService
	shipments        *service.ShipmentService
	supplyPolicies   *service.SupplyPolicyService
	purchaseReceipts *service.PurchaseReceiptService
	logger           *zap.Logger
	engine           *auth.Engine
}

// Params groups fx-injected dependencies.
type Params struct {
	fx.In

	Svc              *service.InventoryService
	StockItems       *service.StockItemService
	Warehouses       *service.WarehouseService
	Transfers        *service.TransferOrderService
	Shipments        *service.ShipmentService
	SupplyPolicies   *service.SupplyPolicyService
	PurchaseReceipts *service.PurchaseReceiptService
	Logger           *zap.Logger
	Engine           *auth.Engine
}

// NewHandler constructs a Handler for fx injection.
func NewHandler(p Params) *Handler {
	return &Handler{
		svc:              p.Svc,
		stockItems:       p.StockItems,
		warehouses:       p.Warehouses,
		transfers:        p.Transfers,
		shipments:        p.Shipments,
		supplyPolicies:   p.SupplyPolicies,
		purchaseReceipts: p.PurchaseReceipts,
		logger:           p.Logger,
		engine:           p.Engine,
	}
}

// permit builds per-route OPA authorization middleware (ADR-AUTH-001, layer 2).
func (h *Handler) permit(action string) func(http.Handler) http.Handler {
	return auth.RequirePermission(h.engine, action)
}

// RegisterRoutes mounts inventory endpoints on the router.
func (h *Handler) RegisterRoutes(r *chi.Mux) {
	r.Route("/api/v1/inventory", func(r chi.Router) {
		// Stock levels / movements are warehouse+stock_item scoped (ADR-DATA-005).
		// manager/warehouse only (ADR-DATA-005 İlke 4) — see authz.rego.
		r.With(h.permit("inventory.level.read")).Get("/levels", h.listLevels)
		r.With(h.permit("inventory.level.read")).Get("/levels/{stockItemID}", h.getLevel)
		r.With(h.permit("inventory.movement.create")).Post("/movements", h.recordMovement)
		r.With(h.permit("inventory.movement.read")).Get("/movements", h.listMovements)

		// Stock items (ADR-DATA-005 Faz 1)
		r.With(h.permit("inventory.stock_item.create")).Post("/stock-items", h.createStockItem)
		r.With(h.permit("inventory.stock_item.read")).Get("/stock-items", h.listStockItems)
		r.With(h.permit("inventory.stock_item.read")).Get("/stock-items/{id}", h.getStockItem)
		r.With(h.permit("inventory.stock_item.update")).Put("/stock-items/{id}", h.updateStockItem)
		r.With(h.permit("inventory.stock_item.delete")).Delete("/stock-items/{id}", h.deleteStockItem)

		// Warehouses (ADR-DATA-005 Faz 1)
		r.With(h.permit("inventory.warehouse.create")).Post("/warehouses", h.createWarehouse)
		r.With(h.permit("inventory.warehouse.read")).Get("/warehouses", h.listWarehouses)
		r.With(h.permit("inventory.warehouse.read")).Get("/warehouses/{id}", h.getWarehouse)
		r.With(h.permit("inventory.warehouse.update")).Put("/warehouses/{id}", h.updateWarehouse)
		r.With(h.permit("inventory.warehouse.delete")).Delete("/warehouses/{id}", h.deleteWarehouse)

		// Branch transfer orders (ADR-DATA-006 Faz 1). Note: there is
		// deliberately no route that sets status to shipped/received directly
		// — those transitions are owned by the shipment endpoints below.
		r.With(h.permit("inventory.transfer_order.create")).Post("/transfer-orders", h.createTransferOrder)
		r.With(h.permit("inventory.transfer_order.read")).Get("/transfer-orders", h.listTransferOrders)
		r.With(h.permit("inventory.transfer_order.read")).Get("/transfer-orders/{id}", h.getTransferOrder)
		r.With(h.permit("inventory.transfer_order.submit")).Post("/transfer-orders/{id}/submit", h.submitTransferOrder)
		r.With(h.permit("inventory.transfer_order.approve")).Post("/transfer-orders/{id}/approve", h.approveTransferOrder)
		r.With(h.permit("inventory.transfer_order.reject")).Post("/transfer-orders/{id}/reject", h.rejectTransferOrder)
		r.With(h.permit("inventory.transfer_order.cancel")).Post("/transfer-orders/{id}/cancel", h.cancelTransferOrder)
		r.With(h.permit("inventory.transfer_order.fulfil")).Post("/transfer-orders/{id}/fulfil", h.fulfilTransferOrder)

		// Shipments (ADR-DATA-006 Faz 1). advance = fulfilling->in_transit,
		// receive = in_transit->received (sole owner of the "received" fact).
		r.With(h.permit("inventory.shipment.create")).Post("/shipments", h.createShipment)
		r.With(h.permit("inventory.shipment.read")).Get("/shipments", h.listShipments)
		r.With(h.permit("inventory.shipment.read")).Get("/shipments/{id}", h.getShipment)
		r.With(h.permit("inventory.shipment.advance")).Post("/shipments/{id}/approve", h.approveShipment)
		r.With(h.permit("inventory.shipment.advance")).Post("/shipments/{id}/advance", h.advanceShipment)
		r.With(h.permit("inventory.shipment.receive")).Post("/shipments/{id}/receive", h.receiveShipment)
		r.With(h.permit("inventory.shipment.cancel")).Post("/shipments/{id}/cancel", h.cancelShipment)

		// Supply policies (ADR-DATA-007): create is manager-only (authz.rego
		// grants "inventory.supply_policy.create" only via the manager
		// wildcard, not to the warehouse role); read is manager+warehouse,
		// mirroring the rest of inventory management.
		r.With(h.permit("inventory.supply_policy.create")).Post("/supply-policies", h.createSupplyPolicy)
		r.With(h.permit("inventory.supply_policy.read")).Get("/supply-policies", h.listSupplyPolicies)
		r.With(h.permit("inventory.supply_policy.read")).Get("/supply-policies/effective/{stockItemID}", h.getEffectiveSupplyPolicy)

		// Purchase receipts (ADR-DATA-007 karar 3): elden fiş / faturasız
		// alım. Immutable documents — no update/delete route (a correction
		// is a new receipt).
		r.With(h.permit("inventory.purchase_receipt.create")).Post("/purchase-receipts", h.createPurchaseReceipt)
		r.With(h.permit("inventory.purchase_receipt.read")).Get("/purchase-receipts", h.listPurchaseReceipts)
		r.With(h.permit("inventory.purchase_receipt.read")).Get("/purchase-receipts/{id}", h.getPurchaseReceipt)
	})
}

// ============================================================
// Request / response types
// ============================================================

type movementRequest struct {
	WarehouseID   uuid.UUID  `json:"warehouse_id"`
	StockItemID   uuid.UUID  `json:"stock_item_id"`
	Type          string     `json:"movement_type"`
	Quantity      float64    `json:"quantity"`
	Unit          string     `json:"unit"`
	ReferenceID   *uuid.UUID `json:"reference_id,omitempty"`
	ReferenceType *string    `json:"reference_type,omitempty"`
	Notes         *string    `json:"notes,omitempty"`
}

type levelResponse struct {
	ID           uuid.UUID `json:"id"`
	WarehouseID  uuid.UUID `json:"warehouse_id"`
	StockItemID  uuid.UUID `json:"stock_item_id"`
	OnHand       float64   `json:"on_hand"`
	Reserved     float64   `json:"reserved"`
	Available    float64   `json:"available"`
	ReorderPoint *float64  `json:"reorder_point,omitempty"`
	Unit         string    `json:"unit"`
	UpdatedAt    string    `json:"updated_at"`
}

type movementResponse struct {
	ID            uuid.UUID  `json:"id"`
	WarehouseID   uuid.UUID  `json:"warehouse_id"`
	StockItemID   uuid.UUID  `json:"stock_item_id"`
	Type          string     `json:"movement_type"`
	Quantity      float64    `json:"quantity"`
	ReferenceID   *uuid.UUID `json:"reference_id,omitempty"`
	ReferenceType *string    `json:"reference_type,omitempty"`
	Notes         *string    `json:"notes,omitempty"`
	CreatedAt     string     `json:"created_at"`
}

type recordMovementResponse struct {
	Movement movementResponse `json:"movement"`
	Level    levelResponse    `json:"level"`
}

// ============================================================
// Handlers
// ============================================================

func (h *Handler) listLevels(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	warehouseID, err := uuidQuery(r, "warehouse_id")
	if err != nil {
		http.Error(w, "warehouse_id query param is required and must be a valid UUID", http.StatusBadRequest)
		return
	}

	levels, err := h.svc.ListLevelsByWarehouse(r.Context(), p.TenantID, warehouseID)
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
	stockItemID, err := uuidParam(r, "stockItemID")
	if err != nil {
		http.Error(w, "invalid stock_item_id", http.StatusBadRequest)
		return
	}
	warehouseID, err := uuidQuery(r, "warehouse_id")
	if err != nil {
		http.Error(w, "warehouse_id query param is required and must be a valid UUID", http.StatusBadRequest)
		return
	}

	lvl, err := h.svc.GetLevel(r.Context(), p.TenantID, warehouseID, stockItemID)
	if err != nil {
		h.logError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toLevelResponse(lvl))
}

func (h *Handler) recordMovement(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}

	var req movementRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	var createdBy *uuid.UUID
	if p.PersonID != uuid.Nil {
		id := p.PersonID
		createdBy = &id
	}

	svcReq := service.RecordMovementRequest{
		WarehouseID:   req.WarehouseID,
		StockItemID:   req.StockItemID,
		Type:          domain.MovementType(req.Type),
		Quantity:      req.Quantity,
		Unit:          req.Unit,
		ReferenceID:   req.ReferenceID,
		ReferenceType: req.ReferenceType,
		Notes:         req.Notes,
		CreatedBy:     createdBy,
	}

	mv, lvl, err := h.svc.RecordMovement(r.Context(), p.TenantID, p, svcReq)
	if err != nil {
		h.logError(w, r, err)
		return
	}

	writeJSON(w, http.StatusCreated, recordMovementResponse{
		Movement: toMovementResponse(mv),
		Level:    toLevelResponse(lvl),
	})
}

func (h *Handler) listMovements(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	warehouseID, err := uuidQuery(r, "warehouse_id")
	if err != nil {
		http.Error(w, "warehouse_id query param is required and must be a valid UUID", http.StatusBadRequest)
		return
	}

	limit := 100
	if ls := r.URL.Query().Get("limit"); ls != "" {
		if n, err := strconv.Atoi(ls); err == nil && n > 0 {
			limit = n
		}
	}

	stockItemIDStr := r.URL.Query().Get("stock_item_id")
	if stockItemIDStr != "" {
		stockItemID, err := uuid.Parse(stockItemIDStr)
		if err != nil {
			http.Error(w, "invalid stock_item_id", http.StatusBadRequest)
			return
		}
		mvs, err := h.svc.ListMovementsByStockItem(r.Context(), p.TenantID, warehouseID, stockItemID, limit)
		if err != nil {
			h.logError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, toMovementSlice(mvs))
		return
	}

	mvs, err := h.svc.ListMovementsByWarehouse(r.Context(), p.TenantID, warehouseID, limit)
	if err != nil {
		h.logError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toMovementSlice(mvs))
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
	if errors.Is(err, pub.ErrBranchForbidden) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var ve *pub.ValidationError
	if errors.As(err, &ve) {
		http.Error(w, ve.Msg, http.StatusUnprocessableEntity)
		return
	}
	var spv *pub.ErrSupplyPolicyViolation
	if errors.As(err, &spv) {
		http.Error(w, spv.Msg, http.StatusUnprocessableEntity)
		return
	}
	var te *pub.TransitionError
	if errors.As(err, &te) {
		http.Error(w, te.Error(), http.StatusConflict)
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

func toLevelResponse(l domain.StockLevel) levelResponse {
	return levelResponse{
		ID:           l.ID,
		WarehouseID:  l.WarehouseID,
		StockItemID:  l.StockItemID,
		OnHand:       l.OnHand,
		Reserved:     l.Reserved,
		Available:    l.Available,
		ReorderPoint: l.ReorderPoint,
		Unit:         l.Unit,
		UpdatedAt:    l.UpdatedAt.Format(timeLayout),
	}
}

func toMovementResponse(m domain.StockMovement) movementResponse {
	return movementResponse{
		ID:            m.ID,
		WarehouseID:   m.WarehouseID,
		StockItemID:   m.StockItemID,
		Type:          string(m.Type),
		Quantity:      m.Quantity,
		ReferenceID:   m.ReferenceID,
		ReferenceType: m.ReferenceType,
		Notes:         m.Notes,
		CreatedAt:     m.CreatedAt.Format(timeLayout),
	}
}

func toMovementSlice(mvs []domain.StockMovement) []movementResponse {
	resp := make([]movementResponse, len(mvs))
	for i, m := range mvs {
		resp[i] = toMovementResponse(m)
	}
	return resp
}
