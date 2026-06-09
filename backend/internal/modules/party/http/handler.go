// Package http provides the HTTP layer for the party module.
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

	"onlinemenu.tr/internal/modules/party/domain"
	"onlinemenu.tr/internal/modules/party/service"
	"onlinemenu.tr/internal/platform/auth"
)

// Handler exposes party REST endpoints.
type Handler struct {
	parties *service.PartyService
	logger  *zap.Logger
}

// Params groups fx-injected dependencies.
type Params struct {
	fx.In

	Parties *service.PartyService
	Logger  *zap.Logger
}

// New constructs a Handler.
func New(p Params) *Handler {
	return &Handler{parties: p.Parties, logger: p.Logger}
}

// RegisterRoutes mounts party endpoints on the provided router.
func (h *Handler) RegisterRoutes(r *chi.Mux) {
	r.Route("/api/v1/parties", func(r chi.Router) {
		r.Post("/", h.create)
		r.Get("/", h.list)
		r.Get("/search", h.search)
		r.Get("/{id}", h.get)
		r.Put("/{id}", h.update)
		r.Post("/{id}/contacts", h.addContact)
		r.Delete("/{id}/contacts/{contactId}", h.deleteContact)
	})
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	var body partyBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	party, err := h.parties.CreateParty(r.Context(), p.TenantID, body.toDomain())
	if errors.Is(err, service.ErrInvalidInput) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err != nil {
		h.logger.Error("party: create", zap.Error(err))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, partyResponse(party))
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid party ID", http.StatusBadRequest)
		return
	}
	party, err := h.parties.GetParty(r.Context(), p.TenantID, id)
	if errors.Is(err, service.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		h.logger.Error("party: get", zap.Error(err))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, partyResponse(party))
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid party ID", http.StatusBadRequest)
		return
	}
	var body partyBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	party := body.toDomain()
	party.ID = id
	updated, err := h.parties.UpdateParty(r.Context(), p.TenantID, party)
	if errors.Is(err, service.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if errors.Is(err, service.ErrInvalidInput) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err != nil {
		h.logger.Error("party: update", zap.Error(err))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, partyResponse(updated))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}

	partyType := domain.PartyType(r.URL.Query().Get("type"))
	limit := 50
	offset := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}

	parties, err := h.parties.ListParties(r.Context(), p.TenantID, partyType, limit, offset)
	if err != nil {
		h.logger.Error("party: list", zap.Error(err))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	resp := make([]any, len(parties))
	for i, party := range parties {
		resp[i] = partyResponse(party)
	}
	writeJSON(w, http.StatusOK, map[string]any{"parties": resp})
}

func (h *Handler) search(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	query := r.URL.Query().Get("q")
	if query == "" {
		http.Error(w, "q query parameter required", http.StatusBadRequest)
		return
	}
	parties, err := h.parties.SearchParties(r.Context(), p.TenantID, query, 20)
	if errors.Is(err, service.ErrInvalidInput) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err != nil {
		h.logger.Error("party: search", zap.Error(err))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	resp := make([]any, len(parties))
	for i, party := range parties {
		resp[i] = partyResponse(party)
	}
	writeJSON(w, http.StatusOK, map[string]any{"parties": resp})
}

func (h *Handler) addContact(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	partyID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid party ID", http.StatusBadRequest)
		return
	}
	var body struct {
		Name      string `json:"name"`
		Role      string `json:"role"`
		Phone     string `json:"phone"`
		Email     string `json:"email"`
		IsPrimary bool   `json:"is_primary"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	contact, err := h.parties.AddContact(r.Context(), p.TenantID, domain.Contact{
		PartyID:   partyID,
		Name:      body.Name,
		Role:      body.Role,
		Phone:     body.Phone,
		Email:     body.Email,
		IsPrimary: body.IsPrimary,
	})
	if errors.Is(err, service.ErrInvalidInput) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err != nil {
		h.logger.Error("party: add contact", zap.Error(err))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, contact)
}

func (h *Handler) deleteContact(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	contactID, err := uuid.Parse(chi.URLParam(r, "contactId"))
	if err != nil {
		http.Error(w, "invalid contact ID", http.StatusBadRequest)
		return
	}
	if err := h.parties.DeleteContact(r.Context(), p.TenantID, contactID); err != nil {
		h.logger.Error("party: delete contact", zap.Error(err))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type partyBody struct {
	PartyType         string `json:"party_type"`
	Name              string `json:"name"`
	ShortName         string `json:"short_name"`
	TaxNo             string `json:"tax_no"`
	TaxOffice         string `json:"tax_office"`
	GibAlias          string `json:"gib_alias"`
	Phone             string `json:"phone"`
	Email             string `json:"email"`
	Website           string `json:"website"`
	AddressLine       string `json:"address_line"`
	City              string `json:"city"`
	District          string `json:"district"`
	PostalCode        string `json:"postal_code"`
	PaymentTermsDays  int    `json:"payment_terms_days"`
	CreditLimitAmount int64  `json:"credit_limit_amount"`
	Currency          string `json:"currency"`
	IsActive          bool   `json:"is_active"`
	Notes             string `json:"notes"`
}

func (b partyBody) toDomain() domain.Party {
	return domain.Party{
		PartyType:         domain.PartyType(b.PartyType),
		Name:              b.Name,
		ShortName:         b.ShortName,
		TaxNo:             b.TaxNo,
		TaxOffice:         b.TaxOffice,
		GibAlias:          b.GibAlias,
		Phone:             b.Phone,
		Email:             b.Email,
		Website:           b.Website,
		AddressLine:       b.AddressLine,
		City:              b.City,
		District:          b.District,
		PostalCode:        b.PostalCode,
		PaymentTermsDays:  b.PaymentTermsDays,
		CreditLimitAmount: b.CreditLimitAmount,
		Currency:          b.Currency,
		IsActive:          b.IsActive,
		Notes:             b.Notes,
	}
}

func partyResponse(p domain.Party) map[string]any {
	return map[string]any{
		"id":                  p.ID,
		"party_type":          string(p.PartyType),
		"name":                p.Name,
		"short_name":          p.ShortName,
		"tax_no":              p.TaxNo,
		"tax_office":          p.TaxOffice,
		"gib_alias":           p.GibAlias,
		"phone":               p.Phone,
		"email":               p.Email,
		"address_line":        p.AddressLine,
		"city":                p.City,
		"is_active":           p.IsActive,
		"payment_terms_days":  p.PaymentTermsDays,
		"credit_limit_amount": p.CreditLimitAmount,
		"currency":            p.Currency,
		"contacts":            p.Contacts,
		"created_at":          p.CreatedAt,
	}
}

func requirePrincipal(w http.ResponseWriter, r *http.Request) (auth.Principal, bool) {
	p, err := auth.FromContext(r.Context())
	if err != nil || p.TenantID == uuid.Nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return auth.Principal{}, false
	}
	return p, true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}
