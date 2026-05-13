package http

import (
	"net/http"

	"github.com/google/uuid"

	"onlinemenu.tr/internal/modules/identity/domain"
	"onlinemenu.tr/internal/platform/auth"
)

type meResponse struct {
	Person personDTO `json:"person"`
}

type personDTO struct {
	ID       uuid.UUID `json:"id"`
	Email    string    `json:"email"`
	FullName string    `json:"full_name"`
	Phone    string    `json:"phone"`
}

type contextListResponse struct {
	Contexts []contextItemDTO `json:"contexts"`
	Customer bool             `json:"customer"`
}

type contextItemDTO struct {
	MembershipID uuid.UUID  `json:"membership_id"`
	TenantID     uuid.UUID  `json:"tenant_id"`
	TenantName   string     `json:"tenant_name"`
	BranchID     *uuid.UUID `json:"branch_id,omitempty"`
	BranchName   string     `json:"branch_name,omitempty"`
	RoleID       uuid.UUID  `json:"role_id"`
	RoleName     string     `json:"role_name"`
}

type selectContextRequest struct {
	MembershipID *uuid.UUID `json:"membership_id"`
	Customer     bool       `json:"customer"`
}

type tokenResponse struct {
	Token string `json:"token"`
}

func (h *Handler) GetMe(w http.ResponseWriter, r *http.Request) {
	principal, err := auth.FromContext(r.Context())
	if err != nil {
		h.writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if principal.IsPreContext() {
		h.writeError(w, http.StatusForbidden, "context selection required")
		return
	}

	person, err := h.persons.GetByID(r.Context(), principal.PersonID)
	if err != nil {
		h.handleErr(w, err)
		return
	}

	h.writeJSON(w, http.StatusOK, meResponse{
		Person: personDTO{
			ID:       person.ID,
			Email:    person.Email,
			FullName: person.FullName,
			Phone:    person.Phone,
		},
	})
}

func (h *Handler) ListContexts(w http.ResponseWriter, r *http.Request) {
	principal, err := auth.FromContext(r.Context())
	if err != nil {
		h.writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	// Customer-context tokens must not expose staff membership details. To switch
	// to a staff context the person must re-authenticate with a Keycloak token.
	if principal.IsCustomer() {
		h.writeJSON(w, http.StatusOK, contextListResponse{Contexts: []contextItemDTO{}, Customer: true})
		return
	}

	var items []domain.ContextItem
	if principal.IsPreContext() {
		items, err = h.memberships.ListContexts(r.Context(), principal.KeycloakSub, h.persons)
	} else {
		items, err = h.memberships.ListContextsByPerson(r.Context(), principal.PersonID)
	}
	if err != nil {
		h.handleErr(w, err)
		return
	}

	dtos := make([]contextItemDTO, len(items))
	for i, item := range items {
		dtos[i] = contextItemDTO{
			MembershipID: item.MembershipID,
			TenantID:     item.TenantID,
			TenantName:   item.TenantName,
			BranchID:     item.BranchID,
			BranchName:   item.BranchName,
			RoleID:       item.RoleID,
			RoleName:     item.RoleName,
		}
	}

	h.writeJSON(w, http.StatusOK, contextListResponse{
		Contexts: dtos,
		Customer: false,
	})
}

func (h *Handler) SelectContext(w http.ResponseWriter, r *http.Request) {
	principal, err := auth.FromContext(r.Context())
	if err != nil {
		h.writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var body selectContextRequest
	if err := readJSON(w, r, &body); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if body.Customer && body.MembershipID != nil {
		h.writeError(w, http.StatusBadRequest, "membership_id and customer are mutually exclusive")
		return
	}
	if !body.Customer && body.MembershipID == nil {
		h.writeError(w, http.StatusBadRequest, "membership_id or customer required")
		return
	}

	var keycloakSub string
	if principal.IsPreContext() {
		keycloakSub = principal.KeycloakSub
	} else {
		p, err := h.persons.GetByID(r.Context(), principal.PersonID)
		if err != nil {
			h.handleErr(w, err)
			return
		}
		keycloakSub = p.KeycloakSub
	}

	var token string
	if body.Customer {
		token, err = h.contexts.SelectCustomerContext(r.Context(), keycloakSub, h.persons)
	} else {
		token, err = h.contexts.SelectContext(r.Context(), keycloakSub, *body.MembershipID, h.persons, h.memberships)
	}
	if err != nil {
		h.handleErr(w, err)
		return
	}

	h.writeJSON(w, http.StatusOK, tokenResponse{Token: token})
}
