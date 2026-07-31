package http

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"onlinemenu.tr/internal/modules/identity/domain"
	pub "onlinemenu.tr/internal/modules/identity/public"
)

type roleResponse struct {
	ID           uuid.UUID  `json:"id"`
	Name         string     `json:"name"`
	Scope        string     `json:"scope"`
	SystemKey    string     `json:"system_key,omitempty"`
	IsSystem     bool       `json:"is_system"`
	BranchID     *uuid.UUID `json:"branch_id,omitempty"`
	BranchScoped bool       `json:"branch_scoped"`
}

type roleListResponse struct {
	Roles []roleResponse `json:"roles"`
}

type createRoleRequest struct {
	Name         string     `json:"name"`
	BranchID     *uuid.UUID `json:"branch_id,omitempty"`
	BranchScoped bool       `json:"branch_scoped"`
}

// updateRoleRequest carries PUT semantics: both fields are replaced. Omitting
// branch_scoped therefore reads as false and can clear the flag — which is the
// permissive direction and needs no extra guard.
type updateRoleRequest struct {
	Name         string `json:"name"`
	BranchScoped bool   `json:"branch_scoped"`
}

// branchScopeConflictResponse gives the 409 an actionable body: the admin needs
// the count to know how much clean-up the flip is waiting on.
type branchScopeConflictResponse struct {
	Error                string `json:"error"`
	ChainWideMemberships int    `json:"chain_wide_memberships"`
}

func (h *Handler) ListRoles(w http.ResponseWriter, r *http.Request) {
	tenantID, err := pathUUID(r, "tenantID")
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid tenant id")
		return
	}

	roles, err := h.roles.ListForTenant(r.Context(), tenantID)
	if err != nil {
		h.handleErr(w, err)
		return
	}

	dtos := make([]roleResponse, len(roles))
	for i, role := range roles {
		dtos[i] = toRoleResponse(role)
	}

	h.writeJSON(w, http.StatusOK, roleListResponse{Roles: dtos})
}

func (h *Handler) CreateRole(w http.ResponseWriter, r *http.Request) {
	tenantID, err := pathUUID(r, "tenantID")
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid tenant id")
		return
	}

	var body createRoleRequest
	if err := readJSON(w, r, &body); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	var created domain.Role
	if body.BranchID != nil {
		created, err = h.roles.CreateBranchRole(r.Context(), tenantID, *body.BranchID, body.Name, body.BranchScoped)
	} else {
		created, err = h.roles.CreateTenantRole(r.Context(), tenantID, body.Name, body.BranchScoped)
	}
	if err != nil {
		h.handleErr(w, err)
		return
	}

	h.writeJSON(w, http.StatusCreated, toRoleResponse(created))
}

func (h *Handler) UpdateRole(w http.ResponseWriter, r *http.Request) {
	tenantID, err := pathUUID(r, "tenantID")
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid tenant id")
		return
	}
	roleID, err := pathUUID(r, "roleID")
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid role id")
		return
	}

	var body updateRoleRequest
	if err := readJSON(w, r, &body); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	updated, err := h.roles.Update(r.Context(), tenantID, roleID, body.Name, body.BranchScoped)
	if err != nil {
		var conflict pub.BranchScopeConflictError
		if errors.As(err, &conflict) {
			h.writeJSON(w, http.StatusConflict, branchScopeConflictResponse{
				Error:                "role cannot become branch-scoped while chain-wide memberships exist",
				ChainWideMemberships: conflict.ChainWideMemberships,
			})
			return
		}
		h.handleErr(w, err)
		return
	}

	h.writeJSON(w, http.StatusOK, toRoleResponse(updated))
}

func (h *Handler) DeleteRole(w http.ResponseWriter, r *http.Request) {
	tenantID, err := pathUUID(r, "tenantID")
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid tenant id")
		return
	}
	roleID, err := pathUUID(r, "roleID")
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid role id")
		return
	}

	if err := h.roles.Delete(r.Context(), tenantID, roleID); err != nil {
		h.handleErr(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func toRoleResponse(r domain.Role) roleResponse {
	return roleResponse{
		ID:        r.ID,
		Name:      r.Name,
		Scope:     string(r.Scope()),
		SystemKey: r.SystemKey,
		IsSystem:  r.IsSystem,
		BranchID:  r.BranchID,
	}
}
