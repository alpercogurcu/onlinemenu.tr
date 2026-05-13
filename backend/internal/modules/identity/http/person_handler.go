package http

import (
	"net/http"

	"github.com/google/uuid"

	"onlinemenu.tr/internal/modules/identity/domain"
)

type personDetailResponse struct {
	ID          uuid.UUID `json:"id"`
	KeycloakSub string    `json:"keycloak_sub"`
	Email       string    `json:"email"`
	FullName    string    `json:"full_name"`
	Phone       string    `json:"phone"`
}

type createPersonRequest struct {
	KeycloakSub string `json:"keycloak_sub"`
	Email       string `json:"email"`
	FullName    string `json:"full_name"`
	Phone       string `json:"phone"`
}

// TODO: restrict to platform admin only; tenant-scoped admin check is deferred to Faz 2.
func (h *Handler) GetPerson(w http.ResponseWriter, r *http.Request) {
	personID, err := pathUUID(r, "personID")
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid person id")
		return
	}

	person, err := h.persons.GetByID(r.Context(), personID)
	if err != nil {
		h.handleErr(w, err)
		return
	}

	h.writeJSON(w, http.StatusOK, personDetailResponse{
		ID:          person.ID,
		KeycloakSub: person.KeycloakSub,
		Email:       person.Email,
		FullName:    person.FullName,
		Phone:       person.Phone,
	})
}

// TODO: restrict to platform admin only; tenant-scoped admin check is deferred to Faz 2.
func (h *Handler) CreatePerson(w http.ResponseWriter, r *http.Request) {
	var body createPersonRequest
	if err := readJSON(w, r, &body); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	created, err := h.persons.Create(r.Context(), domain.Person{
		KeycloakSub: body.KeycloakSub,
		Email:       body.Email,
		FullName:    body.FullName,
		Phone:       body.Phone,
	})
	if err != nil {
		h.handleErr(w, err)
		return
	}

	h.writeJSON(w, http.StatusCreated, personDetailResponse{
		ID:          created.ID,
		KeycloakSub: created.KeycloakSub,
		Email:       created.Email,
		FullName:    created.FullName,
		Phone:       created.Phone,
	})
}
