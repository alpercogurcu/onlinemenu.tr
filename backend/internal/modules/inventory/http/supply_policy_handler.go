package http

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"

	"onlinemenu.tr/internal/modules/inventory/domain"
	"onlinemenu.tr/internal/modules/inventory/service"
)

type createSupplyPolicyRequest struct {
	Scope               string      `json:"scope"`
	StockItemID         *uuid.UUID  `json:"stock_item_id,omitempty"`
	Category            string      `json:"category,omitempty"`
	Mode                string      `json:"mode"`
	ApprovedSupplierIDs []uuid.UUID `json:"approved_supplier_ids,omitempty"`
	EffectiveFrom       *string     `json:"effective_from,omitempty"`
}

type supplyPolicyResponse struct {
	ID                  string      `json:"id"`
	BranchID            *string     `json:"branch_id,omitempty"`
	Scope               string      `json:"scope"`
	StockItemID         *string     `json:"stock_item_id,omitempty"`
	Category            string      `json:"category,omitempty"`
	Mode                string      `json:"mode"`
	ApprovedSupplierIDs []uuid.UUID `json:"approved_supplier_ids,omitempty"`
	EffectiveFrom       string      `json:"effective_from"`
	CreatedAt           string      `json:"created_at"`
}

type effectivePolicyResponse struct {
	StockItemID         string      `json:"stock_item_id"`
	Mode                string      `json:"mode"`
	ApprovedSupplierIDs []uuid.UUID `json:"approved_supplier_ids,omitempty"`
}

func (h *Handler) createSupplyPolicy(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}

	var req createSupplyPolicyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	effectiveFrom := time.Now()
	if req.EffectiveFrom != nil && *req.EffectiveFrom != "" {
		t, err := time.Parse(timeLayout, *req.EffectiveFrom)
		if err != nil {
			http.Error(w, "invalid effective_from", http.StatusBadRequest)
			return
		}
		effectiveFrom = t
	}

	var createdBy *uuid.UUID
	if p.PersonID != uuid.Nil {
		id := p.PersonID
		createdBy = &id
	}

	policy, err := h.supplyPolicies.Create(r.Context(), p.TenantID, service.CreateSupplyPolicyRequest{
		Scope:               domain.SupplyScope(req.Scope),
		StockItemID:         req.StockItemID,
		Category:            req.Category,
		Mode:                domain.SupplyMode(req.Mode),
		ApprovedSupplierIDs: req.ApprovedSupplierIDs,
		EffectiveFrom:       effectiveFrom,
		CreatedBy:           createdBy,
	})
	if err != nil {
		h.logError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toSupplyPolicyResponse(policy))
}

func (h *Handler) listSupplyPolicies(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}

	policies, err := h.supplyPolicies.List(r.Context(), p.TenantID)
	if err != nil {
		h.logError(w, r, err)
		return
	}
	resp := make([]supplyPolicyResponse, len(policies))
	for i, policy := range policies {
		resp[i] = toSupplyPolicyResponse(policy)
	}
	writeJSON(w, http.StatusOK, resp)
}

// getEffectiveSupplyPolicy exposes service.SupplyPolicyService.EffectivePolicyFor
// over HTTP: "how may branch_id source stock_item_id right now" (ADR-DATA-007).
// This is a read, gated by the same inventory.supply_policy.read permission as
// listSupplyPolicies — no separate action, since it reveals no more than the
// list endpoint already does for a manager/warehouse principal.
func (h *Handler) getEffectiveSupplyPolicy(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	stockItemID, err := uuidParam(r, "stockItemID")
	if err != nil {
		http.Error(w, "invalid stock_item_id", http.StatusBadRequest)
		return
	}
	branchID, err := uuidQuery(r, "branch_id")
	if err != nil {
		http.Error(w, "branch_id query param is required and must be a valid UUID", http.StatusBadRequest)
		return
	}

	mode, approved, err := h.supplyPolicies.EffectivePolicyFor(r.Context(), p.TenantID, stockItemID, branchID)
	if err != nil {
		h.logError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, effectivePolicyResponse{
		StockItemID:         stockItemID.String(),
		Mode:                string(mode),
		ApprovedSupplierIDs: approved,
	})
}

func toSupplyPolicyResponse(p domain.SupplyPolicy) supplyPolicyResponse {
	resp := supplyPolicyResponse{
		ID:                  p.ID.String(),
		Scope:               string(p.Scope),
		Category:            p.Category,
		Mode:                string(p.Mode),
		ApprovedSupplierIDs: p.ApprovedSupplierIDs,
		EffectiveFrom:       p.EffectiveFrom.Format(timeLayout),
		CreatedAt:           p.CreatedAt.Format(timeLayout),
	}
	if p.BranchID != nil {
		s := p.BranchID.String()
		resp.BranchID = &s
	}
	if p.StockItemID != nil {
		s := p.StockItemID.String()
		resp.StockItemID = &s
	}
	return resp
}
