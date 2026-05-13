package http

import (
	"net/http"

	"github.com/google/uuid"

	"onlinemenu.tr/internal/modules/identity/domain"
)

type membershipResponse struct {
	ID       uuid.UUID  `json:"id"`
	PersonID uuid.UUID  `json:"person_id"`
	TenantID uuid.UUID  `json:"tenant_id"`
	BranchID *uuid.UUID `json:"branch_id,omitempty"`
	RoleID   uuid.UUID  `json:"role_id"`
	Status   string     `json:"status"`
}

type membershipListResponse struct {
	Memberships []membershipResponse `json:"memberships"`
}

type createMembershipRequest struct {
	PersonID uuid.UUID  `json:"person_id"`
	BranchID *uuid.UUID `json:"branch_id,omitempty"`
	RoleID   uuid.UUID  `json:"role_id"`
}

type updateMembershipStatusRequest struct {
	Status domain.MembershipStatus `json:"status"`
}

func (h *Handler) ListMemberships(w http.ResponseWriter, r *http.Request) {
	tenantID, err := pathUUID(r, "tenantID")
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid tenant id")
		return
	}

	var personID *uuid.UUID
	if raw := r.URL.Query().Get("person_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			h.writeError(w, http.StatusBadRequest, "invalid person_id")
			return
		}
		personID = &id
	}

	var branchID *uuid.UUID
	if raw := r.URL.Query().Get("branch_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			h.writeError(w, http.StatusBadRequest, "invalid branch_id")
			return
		}
		branchID = &id
	}

	memberships, err := h.memberships.List(r.Context(), tenantID, personID, branchID)
	if err != nil {
		h.handleErr(w, err)
		return
	}

	dtos := make([]membershipResponse, len(memberships))
	for i, m := range memberships {
		dtos[i] = toMembershipResponse(m)
	}

	h.writeJSON(w, http.StatusOK, membershipListResponse{Memberships: dtos})
}

func (h *Handler) CreateMembership(w http.ResponseWriter, r *http.Request) {
	tenantID, err := pathUUID(r, "tenantID")
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid tenant id")
		return
	}

	var body createMembershipRequest
	if err := readJSON(w, r, &body); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	created, err := h.memberships.Create(r.Context(), tenantID, body.PersonID, body.BranchID, body.RoleID)
	if err != nil {
		h.handleErr(w, err)
		return
	}

	h.writeJSON(w, http.StatusCreated, toMembershipResponse(created))
}

func (h *Handler) UpdateMembershipStatus(w http.ResponseWriter, r *http.Request) {
	tenantID, err := pathUUID(r, "tenantID")
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid tenant id")
		return
	}
	membershipID, err := pathUUID(r, "membershipID")
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid membership id")
		return
	}

	var body updateMembershipStatusRequest
	if err := readJSON(w, r, &body); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := h.memberships.UpdateStatus(r.Context(), tenantID, membershipID, body.Status); err != nil {
		h.handleErr(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func toMembershipResponse(m domain.Membership) membershipResponse {
	return membershipResponse{
		ID:       m.ID,
		PersonID: m.PersonID,
		TenantID: m.TenantID,
		BranchID: m.BranchID,
		RoleID:   m.RoleID,
		Status:   string(m.Status),
	}
}
