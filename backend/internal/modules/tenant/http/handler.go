// Package http wires the tenant HTTP handlers onto a chi router.
package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	pub "onlinemenu.tr/internal/modules/tenant/public"
	"onlinemenu.tr/internal/modules/tenant/service"
	"onlinemenu.tr/internal/platform/auth"
)

// maxBodyBytes is the hard cap on incoming JSON request bodies (1 MB).
const maxBodyBytes = 1 << 20

// dateLayout is the expected format for date path parameters.
const dateLayout = "2006-01-02"

// Handler exposes the tenant service over HTTP.
type Handler struct {
	svc    *service.Service
	logger *zap.Logger
}

// NewHandler constructs a Handler for fx injection.
func NewHandler(svc *service.Service, logger *zap.Logger) *Handler {
	return &Handler{svc: svc, logger: logger}
}

// tenantAccessMiddleware verifies that the authenticated principal owns the tenant
// indicated in the URL path. This prevents cross-tenant data access even when RLS
// is set from the path parameter.
func (h *Handler) tenantAccessMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenantID, err := pathUUID(r, "tenantID")
		if err != nil {
			h.writeError(w, r, http.StatusBadRequest, "invalid tenant id")
			return
		}
		principal, err := auth.FromContext(r.Context())
		if err != nil {
			h.writeError(w, r, http.StatusUnauthorized, "unauthorized")
			return
		}
		if principal.TenantID != tenantID {
			h.writeError(w, r, http.StatusForbidden, "tenant access denied")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// branchAccessMiddleware verifies that the authenticated principal has access to the
// branch indicated in the URL path. Applied at the /{branchID} sub-router level so
// every branch-scoped endpoint is protected without per-handler duplication.
func (h *Handler) branchAccessMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		branchID, err := pathUUID(r, "branchID")
		if err != nil {
			h.writeError(w, r, http.StatusBadRequest, "invalid branch id")
			return
		}
		principal, err := auth.FromContext(r.Context())
		if err != nil {
			h.writeError(w, r, http.StatusUnauthorized, "unauthorized")
			return
		}
		if !principal.HasBranchAccess(branchID) {
			h.writeError(w, r, http.StatusForbidden, "branch access denied")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// GetTenant handles GET /tenants/{tenantID}.
func (h *Handler) GetTenant(w http.ResponseWriter, r *http.Request) {
	tenantID, err := pathUUID(r, "tenantID")
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid tenant id")
		return
	}

	t, err := h.svc.GetByID(r.Context(), tenantID)
	if err != nil {
		h.handleServiceErr(w, r, err, "tenant not found")
		return
	}
	h.writeJSON(w, r, http.StatusOK, t)
}

// UpdateTenant handles PUT /tenants/{tenantID}.
func (h *Handler) UpdateTenant(w http.ResponseWriter, r *http.Request) {
	tenantID, err := pathUUID(r, "tenantID")
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid tenant id")
		return
	}

	var body pub.Tenant
	if err := readJSON(w, r, &body); err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid request body")
		return
	}
	body.ID = tenantID

	updated, err := h.svc.Update(r.Context(), body)
	if err != nil {
		h.handleServiceErr(w, r, err, "tenant not found")
		return
	}
	h.writeJSON(w, r, http.StatusOK, updated)
}

// ListBranches handles GET /tenants/{tenantID}/branches.
func (h *Handler) ListBranches(w http.ResponseWriter, r *http.Request) {
	tenantID, err := pathUUID(r, "tenantID")
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid tenant id")
		return
	}

	branches, err := h.svc.ListBranches(r.Context(), tenantID)
	if err != nil {
		h.handleServiceErr(w, r, err, "")
		return
	}
	h.writeJSON(w, r, http.StatusOK, branches)
}

// GetBranch handles GET /tenants/{tenantID}/branches/{branchID}.
func (h *Handler) GetBranch(w http.ResponseWriter, r *http.Request) {
	tenantID, branchID, ok := tenantBranchIDs(h, w, r)
	if !ok {
		return
	}

	b, err := h.svc.GetBranch(r.Context(), tenantID, branchID)
	if err != nil {
		h.handleServiceErr(w, r, err, "branch not found")
		return
	}
	h.writeJSON(w, r, http.StatusOK, b)
}

// CreateBranch handles POST /tenants/{tenantID}/branches.
func (h *Handler) CreateBranch(w http.ResponseWriter, r *http.Request) {
	tenantID, err := pathUUID(r, "tenantID")
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid tenant id")
		return
	}

	var body pub.Branch
	if err := readJSON(w, r, &body); err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid request body")
		return
	}
	body.TenantID = tenantID

	created, err := h.svc.CreateBranch(r.Context(), body)
	if err != nil {
		h.handleServiceErr(w, r, err, "")
		return
	}
	h.writeJSON(w, r, http.StatusCreated, created)
}

// ListDocuments handles GET /tenants/{tenantID}/documents.
func (h *Handler) ListDocuments(w http.ResponseWriter, r *http.Request) {
	tenantID, err := pathUUID(r, "tenantID")
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid tenant id")
		return
	}

	docs, err := h.svc.ListDocuments(r.Context(), tenantID)
	if err != nil {
		h.handleServiceErr(w, r, err, "")
		return
	}
	h.writeJSON(w, r, http.StatusOK, docs)
}

// CreateDocument handles POST /tenants/{tenantID}/documents.
func (h *Handler) CreateDocument(w http.ResponseWriter, r *http.Request) {
	tenantID, err := pathUUID(r, "tenantID")
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid tenant id")
		return
	}

	var body pub.Document
	if err := readJSON(w, r, &body); err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid request body")
		return
	}
	body.TenantID = tenantID
	body.Status = pub.DocStatusPending

	created, err := h.svc.CreateDocument(r.Context(), body)
	if err != nil {
		h.handleServiceErr(w, r, err, "")
		return
	}
	h.writeJSON(w, r, http.StatusCreated, created)
}

// GetDocument handles GET /tenants/{tenantID}/documents/{docID}.
func (h *Handler) GetDocument(w http.ResponseWriter, r *http.Request) {
	tenantID, err := pathUUID(r, "tenantID")
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid tenant id")
		return
	}
	docID, err := pathUUID(r, "docID")
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid document id")
		return
	}

	doc, err := h.svc.GetDocument(r.Context(), tenantID, docID)
	if err != nil {
		h.handleServiceErr(w, r, err, "document not found")
		return
	}
	h.writeJSON(w, r, http.StatusOK, doc)
}

// UpdateDocumentStatus handles PATCH /tenants/{tenantID}/documents/{docID}/status.
func (h *Handler) UpdateDocumentStatus(w http.ResponseWriter, r *http.Request) {
	tenantID, err := pathUUID(r, "tenantID")
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid tenant id")
		return
	}
	docID, err := pathUUID(r, "docID")
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid document id")
		return
	}

	var body struct {
		Status pub.DocumentStatus `json:"status"`
		Note   string             `json:"rejection_note"`
	}
	if err := readJSON(w, r, &body); err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := h.svc.UpdateDocumentStatus(r.Context(), tenantID, docID, body.Status, body.Note); err != nil {
		h.handleServiceErr(w, r, err, "document not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DeleteDocument handles DELETE /tenants/{tenantID}/documents/{docID}.
func (h *Handler) DeleteDocument(w http.ResponseWriter, r *http.Request) {
	tenantID, err := pathUUID(r, "tenantID")
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid tenant id")
		return
	}
	docID, err := pathUUID(r, "docID")
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid document id")
		return
	}

	if err := h.svc.DeleteDocument(r.Context(), tenantID, docID); err != nil {
		h.handleServiceErr(w, r, err, "document not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListBranchDocuments handles GET /tenants/{tenantID}/branches/{branchID}/documents.
func (h *Handler) ListBranchDocuments(w http.ResponseWriter, r *http.Request) {
	tenantID, branchID, ok := tenantBranchIDs(h, w, r)
	if !ok {
		return
	}

	docs, err := h.svc.ListBranchDocuments(r.Context(), tenantID, branchID)
	if err != nil {
		h.handleServiceErr(w, r, err, "")
		return
	}
	h.writeJSON(w, r, http.StatusOK, docs)
}

// CreateBranchDocument handles POST /tenants/{tenantID}/branches/{branchID}/documents.
func (h *Handler) CreateBranchDocument(w http.ResponseWriter, r *http.Request) {
	tenantID, branchID, ok := tenantBranchIDs(h, w, r)
	if !ok {
		return
	}

	var body pub.BranchDocument
	if err := readJSON(w, r, &body); err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid request body")
		return
	}
	body.TenantID = tenantID
	body.BranchID = branchID
	body.Status = pub.DocStatusPending

	created, err := h.svc.CreateBranchDocument(r.Context(), body)
	if err != nil {
		h.handleServiceErr(w, r, err, "")
		return
	}
	h.writeJSON(w, r, http.StatusCreated, created)
}

// UpdateBranchDocumentStatus handles PATCH /tenants/{tenantID}/branches/{branchID}/documents/{docID}/status.
func (h *Handler) UpdateBranchDocumentStatus(w http.ResponseWriter, r *http.Request) {
	tenantID, branchID, ok := tenantBranchIDs(h, w, r)
	if !ok {
		return
	}

	docID, err := pathUUID(r, "docID")
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid document id")
		return
	}

	var body struct {
		Status pub.DocumentStatus `json:"status"`
		Note   string             `json:"rejection_note"`
	}
	if err := readJSON(w, r, &body); err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := h.svc.UpdateBranchDocumentStatus(r.Context(), tenantID, branchID, docID, body.Status, body.Note); err != nil {
		h.handleServiceErr(w, r, err, "document not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DeleteBranchDocument handles DELETE /tenants/{tenantID}/branches/{branchID}/documents/{docID}.
func (h *Handler) DeleteBranchDocument(w http.ResponseWriter, r *http.Request) {
	tenantID, branchID, ok := tenantBranchIDs(h, w, r)
	if !ok {
		return
	}

	docID, err := pathUUID(r, "docID")
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid document id")
		return
	}

	if err := h.svc.DeleteBranchDocument(r.Context(), tenantID, branchID, docID); err != nil {
		h.handleServiceErr(w, r, err, "document not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GetRegularHours handles GET /tenants/{tenantID}/branches/{branchID}/hours/regular.
func (h *Handler) GetRegularHours(w http.ResponseWriter, r *http.Request) {
	tenantID, branchID, ok := tenantBranchIDs(h, w, r)
	if !ok {
		return
	}

	hours, err := h.svc.GetRegularHours(r.Context(), tenantID, branchID)
	if err != nil {
		h.handleServiceErr(w, r, err, "")
		return
	}
	h.writeJSON(w, r, http.StatusOK, hours)
}

// SetRegularHours handles PUT /tenants/{tenantID}/branches/{branchID}/hours/regular.
func (h *Handler) SetRegularHours(w http.ResponseWriter, r *http.Request) {
	tenantID, branchID, ok := tenantBranchIDs(h, w, r)
	if !ok {
		return
	}

	var hours []pub.RegularHours
	if err := readJSON(w, r, &hours); err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := h.svc.SetRegularHours(r.Context(), tenantID, branchID, hours); err != nil {
		h.handleServiceErr(w, r, err, "")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GetSpecialHours handles GET /tenants/{tenantID}/branches/{branchID}/hours/special.
func (h *Handler) GetSpecialHours(w http.ResponseWriter, r *http.Request) {
	tenantID, branchID, ok := tenantBranchIDs(h, w, r)
	if !ok {
		return
	}

	hours, err := h.svc.GetSpecialHours(r.Context(), tenantID, branchID)
	if err != nil {
		h.handleServiceErr(w, r, err, "")
		return
	}
	h.writeJSON(w, r, http.StatusOK, hours)
}

// UpsertSpecialHours handles PUT /tenants/{tenantID}/branches/{branchID}/hours/special.
func (h *Handler) UpsertSpecialHours(w http.ResponseWriter, r *http.Request) {
	tenantID, branchID, ok := tenantBranchIDs(h, w, r)
	if !ok {
		return
	}

	var sh pub.SpecialHours
	if err := readJSON(w, r, &sh); err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid request body")
		return
	}
	sh.TenantID = tenantID
	sh.BranchID = branchID

	if err := h.svc.UpsertSpecialHours(r.Context(), tenantID, branchID, sh); err != nil {
		h.handleServiceErr(w, r, err, "")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DeleteSpecialHours handles DELETE /tenants/{tenantID}/branches/{branchID}/hours/special/{date}.
func (h *Handler) DeleteSpecialHours(w http.ResponseWriter, r *http.Request) {
	tenantID, branchID, ok := tenantBranchIDs(h, w, r)
	if !ok {
		return
	}

	dateStr := chi.URLParam(r, "date")
	// Parse as UTC to match the UTC date stored by UpsertSpecialHours (ADR-DATA-003).
	date, err := time.Parse(dateLayout, dateStr)
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid date format, expected YYYY-MM-DD")
		return
	}

	if err := h.svc.DeleteSpecialHours(r.Context(), tenantID, branchID, date); err != nil {
		h.handleServiceErr(w, r, err, "special hours not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListIntegrators handles GET /tenants/{tenantID}/integrators.
func (h *Handler) ListIntegrators(w http.ResponseWriter, r *http.Request) {
	tenantID, err := pathUUID(r, "tenantID")
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid tenant id")
		return
	}

	integrators, err := h.svc.ListIntegrators(r.Context(), tenantID)
	if err != nil {
		h.handleServiceErr(w, r, err, "")
		return
	}
	h.writeJSON(w, r, http.StatusOK, integrators)
}

// CreateIntegrator handles POST /tenants/{tenantID}/integrators.
func (h *Handler) CreateIntegrator(w http.ResponseWriter, r *http.Request) {
	tenantID, err := pathUUID(r, "tenantID")
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid tenant id")
		return
	}

	var body pub.BillingIntegrator
	if err := readJSON(w, r, &body); err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid request body")
		return
	}
	body.TenantID = tenantID

	created, err := h.svc.CreateIntegrator(r.Context(), body)
	if err != nil {
		h.handleServiceErr(w, r, err, "")
		return
	}
	h.writeJSON(w, r, http.StatusCreated, created)
}

// UpdateIntegrator handles PUT /tenants/{tenantID}/integrators/{integratorID}.
func (h *Handler) UpdateIntegrator(w http.ResponseWriter, r *http.Request) {
	tenantID, err := pathUUID(r, "tenantID")
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid tenant id")
		return
	}
	integratorID, err := pathUUID(r, "integratorID")
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid integrator id")
		return
	}

	var body pub.BillingIntegrator
	if err := readJSON(w, r, &body); err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid request body")
		return
	}
	body.TenantID = tenantID
	body.ID = integratorID

	updated, err := h.svc.UpdateIntegrator(r.Context(), body)
	if err != nil {
		h.handleServiceErr(w, r, err, "integrator not found")
		return
	}
	h.writeJSON(w, r, http.StatusOK, updated)
}

// DeleteIntegrator handles DELETE /tenants/{tenantID}/integrators/{integratorID}.
func (h *Handler) DeleteIntegrator(w http.ResponseWriter, r *http.Request) {
	tenantID, err := pathUUID(r, "tenantID")
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid tenant id")
		return
	}
	integratorID, err := pathUUID(r, "integratorID")
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid integrator id")
		return
	}

	if err := h.svc.DeleteIntegrator(r.Context(), tenantID, integratorID); err != nil {
		h.handleServiceErr(w, r, err, "integrator not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- helpers ---

func (h *Handler) writeJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// Headers are already sent; the client receives a truncated body.
		// Log for observability — this should not happen with well-typed structs.
		h.logger.Warn("response encode failed",
			zap.Error(err),
			zap.String("method", r.Method),
			zap.String("path", r.URL.Path),
			zap.String("tenant_id", chi.URLParam(r, "tenantID")),
		)
	}
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	h.writeJSON(w, r, status, map[string]string{"error": msg})
}

// handleServiceErr maps service errors to HTTP status codes.
func (h *Handler) handleServiceErr(w http.ResponseWriter, r *http.Request, err error, notFoundMsg string) {
	switch {
	case errors.Is(err, pub.ErrNotFound):
		if notFoundMsg == "" {
			notFoundMsg = "not found"
		}
		h.writeError(w, r, http.StatusNotFound, notFoundMsg)
	case errors.Is(err, pub.ErrInvalid):
		h.writeError(w, r, http.StatusUnprocessableEntity, "invalid input")
	default:
		h.logger.Error("internal service error",
			zap.Error(err),
			zap.String("method", r.Method),
			zap.String("path", r.URL.Path),
			zap.String("tenant_id", chi.URLParam(r, "tenantID")),
		)
		h.writeError(w, r, http.StatusInternalServerError, "internal error")
	}
}

// readJSON decodes the request body into v with a size cap to prevent DoS.
func readJSON(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

func pathUUID(r *http.Request, param string) (uuid.UUID, error) {
	return uuid.Parse(chi.URLParam(r, param))
}

// tenantBranchIDs extracts both tenantID and branchID and writes an error when either is invalid.
func tenantBranchIDs(h *Handler, w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, bool) {
	tenantID, err := pathUUID(r, "tenantID")
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid tenant id")
		return uuid.UUID{}, uuid.UUID{}, false
	}
	branchID, err := pathUUID(r, "branchID")
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid branch id")
		return uuid.UUID{}, uuid.UUID{}, false
	}
	return tenantID, branchID, true
}
