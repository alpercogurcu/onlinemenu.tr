package http

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"onlinemenu.tr/internal/modules/catalog/domain"
	"onlinemenu.tr/internal/modules/catalog/service"
	"onlinemenu.tr/internal/platform/auth"
)

// branchOverrideResponse is one branch's deviation from the tenant catalog.
//
// price_amount is null when the branch only toggles availability, which is a
// different statement from 0 ("sold here, for free") — hence a pointer rather
// than an omitempty int.
type branchOverrideResponse struct {
	BranchID    uuid.UUID `json:"branch_id"`
	ProductID   uuid.UUID `json:"product_id"`
	IsAvailable bool      `json:"is_available"`
	PriceAmount *int64    `json:"price_amount"`
	UpdatedAt   string    `json:"updated_at"`
}

func toBranchOverrideResponse(o domain.BranchProductOverride) branchOverrideResponse {
	return branchOverrideResponse{
		BranchID:    o.BranchID,
		ProductID:   o.ProductID,
		IsAvailable: o.IsAvailable,
		PriceAmount: o.PriceAmount,
		UpdatedAt:   o.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}
}

// branchFromPath resolves {branchID} and applies the layer-3 guard. Every
// override route goes through it, so a branch-bound principal can never aim
// one at another branch even though OPA allowed the action.
func (h *Handler) branchFromPath(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	branchID, err := uuid.Parse(chi.URLParam(r, "branchID"))
	if err != nil {
		http.Error(w, "invalid branch id", http.StatusBadRequest)
		return uuid.Nil, false
	}
	principal, err := auth.FromContext(r.Context())
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return uuid.Nil, false
	}
	if err := service.RequireBranchAccess(r.Context(), principal, branchID); err != nil {
		h.error(w, r, err)
		return uuid.Nil, false
	}
	return branchID, true
}

func (h *Handler) listBranchOverrides(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := requireTenantID(w, r)
	if !ok {
		return
	}
	branchID, ok := h.branchFromPath(w, r)
	if !ok {
		return
	}
	overrides, err := h.branchOverrides.ListByBranch(r.Context(), tenantID, branchID)
	if err != nil {
		h.error(w, r, err)
		return
	}
	out := make([]branchOverrideResponse, len(overrides))
	for i, o := range overrides {
		out[i] = toBranchOverrideResponse(o)
	}
	respondJSON(w, http.StatusOK, out)
}

func (h *Handler) putBranchOverride(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := requireTenantID(w, r)
	if !ok {
		return
	}
	branchID, ok := h.branchFromPath(w, r)
	if !ok {
		return
	}
	productID, err := uuid.Parse(chi.URLParam(r, "productID"))
	if err != nil {
		http.Error(w, "invalid product id", http.StatusBadRequest)
		return
	}

	// is_available is a pointer so an omitted field means "leave the product
	// sellable" rather than Go's zero value false, which would silently close
	// a product whenever a client sent only a price.
	var req struct {
		IsAvailable *bool  `json:"is_available"`
		PriceAmount *int64 `json:"price_amount"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	available := true
	if req.IsAvailable != nil {
		available = *req.IsAvailable
	}

	saved, err := h.branchOverrides.Upsert(r.Context(), tenantID, domain.BranchProductOverride{
		BranchID:    branchID,
		ProductID:   productID,
		IsAvailable: available,
		PriceAmount: req.PriceAmount,
	})
	if err != nil {
		h.error(w, r, err)
		return
	}
	respondJSON(w, http.StatusOK, toBranchOverrideResponse(saved))
}

func (h *Handler) deleteBranchOverride(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := requireTenantID(w, r)
	if !ok {
		return
	}
	branchID, ok := h.branchFromPath(w, r)
	if !ok {
		return
	}
	productID, err := uuid.Parse(chi.URLParam(r, "productID"))
	if err != nil {
		http.Error(w, "invalid product id", http.StatusBadRequest)
		return
	}
	if err := h.branchOverrides.Delete(r.Context(), tenantID, branchID, productID); err != nil {
		h.error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// branchIDFromQuery reads the optional ?branch_id= of the product listings.
//
// Absent means "tenant default", which is what every caller got before
// ADR-DATA-009 — the parameter is additive, never required. Present means the
// caller must be entitled to that branch, so the same layer-3 guard runs.
func (h *Handler) branchIDFromQuery(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	raw := r.URL.Query().Get("branch_id")
	if raw == "" {
		return uuid.Nil, true
	}
	branchID, err := uuid.Parse(raw)
	if err != nil {
		http.Error(w, "invalid branch_id", http.StatusBadRequest)
		return uuid.Nil, false
	}
	principal, err := auth.FromContext(r.Context())
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return uuid.Nil, false
	}
	if err := service.RequireBranchAccess(r.Context(), principal, branchID); err != nil {
		h.error(w, r, err)
		return uuid.Nil, false
	}
	return branchID, true
}
