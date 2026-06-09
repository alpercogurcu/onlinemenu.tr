// Package http provides the HTTP layer for the hr-core module.
package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/hr-core/domain"
	"onlinemenu.tr/internal/modules/hr-core/service"
	"onlinemenu.tr/internal/platform/auth"
)

// Handler exposes hr-core REST endpoints.
type Handler struct {
	hr     *service.HRService
	logger *zap.Logger
}

// Params groups fx-injected dependencies.
type Params struct {
	fx.In

	HR     *service.HRService
	Logger *zap.Logger
}

// New constructs a Handler.
func New(p Params) *Handler {
	return &Handler{hr: p.HR, logger: p.Logger}
}

// RegisterRoutes mounts hr endpoints on the provided router.
func (h *Handler) RegisterRoutes(r *chi.Mux) {
	r.Route("/api/v1/employees", func(r chi.Router) {
		r.Post("/", h.create)
		r.Get("/", h.list)
		r.Get("/{id}", h.get)
		r.Put("/{id}", h.update)
		r.Post("/{id}/terminate", h.terminate)
	})
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	var body employeeBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	e, err := h.hr.CreateEmployee(r.Context(), p.TenantID, body.toDomain())
	if errors.Is(err, service.ErrInvalidInput) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if errors.Is(err, service.ErrDuplicateEmployee) {
		http.Error(w, "employee profile already exists for this person", http.StatusConflict)
		return
	}
	if err != nil {
		h.logger.Error("hr-core: create employee", zap.Error(err))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, employeeResponse(e))
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid employee ID", http.StatusBadRequest)
		return
	}
	e, err := h.hr.GetEmployee(r.Context(), p.TenantID, id)
	if errors.Is(err, service.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		h.logger.Error("hr-core: get employee", zap.Error(err))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, employeeResponse(e))
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid employee ID", http.StatusBadRequest)
		return
	}
	var body employeeBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	e := body.toDomain()
	e.ID = id
	updated, err := h.hr.UpdateEmployee(r.Context(), p.TenantID, e)
	if errors.Is(err, service.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if errors.Is(err, service.ErrInvalidInput) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err != nil {
		h.logger.Error("hr-core: update employee", zap.Error(err))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, employeeResponse(updated))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}

	status := domain.EmployeeStatus(r.URL.Query().Get("status"))
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

	employees, err := h.hr.ListEmployees(r.Context(), p.TenantID, status, limit, offset)
	if err != nil {
		h.logger.Error("hr-core: list employees", zap.Error(err))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	resp := make([]any, len(employees))
	for i, e := range employees {
		resp[i] = employeeResponse(e)
	}
	writeJSON(w, http.StatusOK, map[string]any{"employees": resp})
}

func (h *Handler) terminate(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid employee ID", http.StatusBadRequest)
		return
	}
	var body struct {
		TerminationDate string `json:"termination_date"`
		Notes           string `json:"notes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	termDate, err := time.Parse("2006-01-02", body.TerminationDate)
	if err != nil {
		http.Error(w, "termination_date must be in YYYY-MM-DD format", http.StatusBadRequest)
		return
	}
	updated, err := h.hr.TerminateEmployee(r.Context(), p.TenantID, id, service.TerminateRequest{
		TerminationDate: termDate,
		Notes:           body.Notes,
	})
	if errors.Is(err, service.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if errors.Is(err, service.ErrInvalidInput) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err != nil {
		h.logger.Error("hr-core: terminate employee", zap.Error(err))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, employeeResponse(updated))
}

type employeeBody struct {
	PersonID         string `json:"person_id"`
	Department       string `json:"department"`
	JobTitle         string `json:"job_title"`
	EmploymentType   string `json:"employment_type"`
	TCKimlikHash     string `json:"tc_kimlik_hash"`
	HireDate         string `json:"hire_date"`
	ContactPhone     string `json:"contact_phone"`
	ContactAddress   string `json:"contact_address"`
	EmergencyName    string `json:"emergency_name"`
	EmergencyPhone   string `json:"emergency_phone"`
	EmergencyRelation string `json:"emergency_relation"`
	Status           string `json:"status"`
	Notes            string `json:"notes"`
}

func (b employeeBody) toDomain() domain.Employee {
	e := domain.Employee{
		Department:     b.Department,
		JobTitle:       b.JobTitle,
		EmploymentType: domain.EmploymentType(b.EmploymentType),
		TCKimlikHash:   b.TCKimlikHash,
		Status:         domain.EmployeeStatus(b.Status),
		Notes:          b.Notes,
		ContactInfo: domain.ContactInfo{
			Phone:   b.ContactPhone,
			Address: b.ContactAddress,
		},
		EmergencyContact: domain.EmergencyContact{
			Name:     b.EmergencyName,
			Phone:    b.EmergencyPhone,
			Relation: b.EmergencyRelation,
		},
	}
	if id, err := uuid.Parse(b.PersonID); err == nil {
		e.PersonID = id
	}
	if d, err := time.Parse("2006-01-02", b.HireDate); err == nil {
		e.HireDate = d
	}
	return e
}

func employeeResponse(e domain.Employee) map[string]any {
	resp := map[string]any{
		"id":              e.ID,
		"person_id":       e.PersonID,
		"department":      e.Department,
		"job_title":       e.JobTitle,
		"employment_type": string(e.EmploymentType),
		"hire_date":       e.HireDate.Format("2006-01-02"),
		"status":          string(e.Status),
		"notes":           e.Notes,
		"contact_info":    e.ContactInfo,
		"emergency_contact": e.EmergencyContact,
		"created_at":      e.CreatedAt,
	}
	if e.TerminationDate != nil {
		resp["termination_date"] = e.TerminationDate.Format("2006-01-02")
	}
	return resp
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
