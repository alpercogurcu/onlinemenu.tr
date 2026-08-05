package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/storefront/domain"
	pub "onlinemenu.tr/internal/modules/storefront/public"
	"onlinemenu.tr/internal/modules/storefront/service"
	"onlinemenu.tr/internal/platform/auth"
)

// AdminHandler exposes the staff-facing QR code endpoints. It is deliberately
// a separate type from the guest-facing handler: the two surfaces have
// different auth chains (Keycloak principal + OPA here, QR token + guest
// session there) and sharing a receiver would make it easy to mount a staff
// route on the public router by accident.
type AdminHandler struct {
	qr     *service.QRService
	logger *zap.Logger
	engine *auth.Engine
}

// AdminParams groups fx-injected dependencies.
type AdminParams struct {
	fx.In

	QR     *service.QRService
	Logger *zap.Logger
	Engine *auth.Engine
}

// NewAdminHandler builds the staff-facing QR handler.
func NewAdminHandler(p AdminParams) *AdminHandler {
	return &AdminHandler{qr: p.QR, logger: p.Logger, engine: p.Engine}
}

// permit builds per-route OPA authorization middleware (ADR-AUTH-001, layer 2).
func (h *AdminHandler) permit(action string) func(http.Handler) http.Handler {
	return auth.RequirePermission(h.engine, action)
}

// RegisterRoutes mounts the staff QR endpoints on the provided router.
//
// Two permissions, not five: reading the QR inventory is a counter-staff
// action (a cashier needs to see which table already has a code), while
// creating, revoking and rotating all mint or retire a printed secret and are
// therefore one management permission — the same split pos.table.read /
// pos.table.manage uses.
//
// No Idempotency-Key gate on the POST routes: create is protected by
// storefront_qr_codes_active_table_uidx (a duplicate retry hits the partial
// unique index and gets a 409, not a second live code) and revoke/rotate are
// guarded by the status-transition machine, which is the same reasoning the
// pos module documents for its non-gated POSTs.
func (h *AdminHandler) RegisterRoutes(r *chi.Mux) {
	r.Route("/api/v1/storefront", func(r chi.Router) {
		r.With(h.permit("storefront.qr.read")).Get("/qr-codes", h.listQRCodes)
		r.With(h.permit("storefront.qr.manage")).Post("/qr-codes", h.createQRCode)
		r.With(h.permit("storefront.qr.read")).Get("/qr-codes/{id}", h.getQRCode)
		r.With(h.permit("storefront.qr.manage")).Post("/qr-codes/{id}/revoke", h.revokeQRCode)
		r.With(h.permit("storefront.qr.manage")).Post("/qr-codes/{id}/rotate", h.rotateQRCode)
	})
}

func (h *AdminHandler) listQRCodes(w http.ResponseWriter, r *http.Request) {
	p, ok := requireAdminPrincipal(w, r)
	if !ok {
		return
	}
	branchID, err := uuid.Parse(r.URL.Query().Get("branch_id"))
	if err != nil {
		http.Error(w, "branch_id query parameter is required", http.StatusUnprocessableEntity)
		return
	}

	codes, err := h.qr.List(r.Context(), p.TenantID, p, branchID)
	if err != nil {
		h.adminError(w, err)
		return
	}

	resp := make([]qrCodeResponse, len(codes))
	for i, c := range codes {
		resp[i] = toQRCodeResponse(c)
	}
	respondAdminJSON(w, http.StatusOK, resp)
}

// createQRCode mints a code for a table.
//
// branch_id and table_label come from the client rather than being resolved
// from table_id here: the storefront module may not read pos tables directly
// (module isolation) and pos/public exposes no table lookup today. The values
// are not trusted blindly — branch_id is checked against the caller's branch
// by QRService (ADR-AUTH-001 layer 3), and table_label is display-only text
// that never participates in a lookup.
func (h *AdminHandler) createQRCode(w http.ResponseWriter, r *http.Request) {
	p, ok := requireAdminPrincipal(w, r)
	if !ok {
		return
	}
	var req struct {
		BranchID   uuid.UUID `json:"branch_id"`
		TableID    uuid.UUID `json:"table_id"`
		TableLabel string    `json:"table_label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.BranchID == uuid.Nil || req.TableID == uuid.Nil {
		http.Error(w, "branch_id and table_id are required", http.StatusUnprocessableEntity)
		return
	}

	issued, err := h.qr.Issue(r.Context(), p.TenantID, p, service.IssueRequest{
		BranchID:   req.BranchID,
		TableID:    req.TableID,
		TableLabel: req.TableLabel,
		CreatedBy:  p.PersonID,
	})
	if err != nil {
		h.adminError(w, err)
		return
	}
	respondAdminJSON(w, http.StatusCreated, toIssuedQRCodeResponse(issued))
}

func (h *AdminHandler) getQRCode(w http.ResponseWriter, r *http.Request) {
	p, ok := requireAdminPrincipal(w, r)
	if !ok {
		return
	}
	id, ok := adminPathUUID(w, r)
	if !ok {
		return
	}

	code, err := h.qr.GetByID(r.Context(), p.TenantID, p, id)
	if err != nil {
		h.adminError(w, err)
		return
	}
	respondAdminJSON(w, http.StatusOK, toQRCodeResponse(code))
}

func (h *AdminHandler) revokeQRCode(w http.ResponseWriter, r *http.Request) {
	p, ok := requireAdminPrincipal(w, r)
	if !ok {
		return
	}
	id, ok := adminPathUUID(w, r)
	if !ok {
		return
	}

	code, err := h.qr.Revoke(r.Context(), p.TenantID, p, id, p.PersonID)
	if err != nil {
		h.adminError(w, err)
		return
	}
	respondAdminJSON(w, http.StatusOK, toQRCodeResponse(code))
}

func (h *AdminHandler) rotateQRCode(w http.ResponseWriter, r *http.Request) {
	p, ok := requireAdminPrincipal(w, r)
	if !ok {
		return
	}
	id, ok := adminPathUUID(w, r)
	if !ok {
		return
	}

	issued, err := h.qr.Rotate(r.Context(), p.TenantID, p, id, p.PersonID)
	if err != nil {
		h.adminError(w, err)
		return
	}
	respondAdminJSON(w, http.StatusOK, toIssuedQRCodeResponse(issued))
}

// ---------------------------------------------------------------------------
// DTOs
// ---------------------------------------------------------------------------

// qrCodeResponse is the staff-facing projection of a QR code (ADR-AUTH-001
// layer 4). Fields are listed one by one rather than embedding domain.QRCode:
// that struct carries TokenHash, and an embedded struct would leak it into
// every response the moment someone adds a field. The hash is not the raw
// token, but it is the exact value stored in the unique index — publishing it
// hands out an offline verification oracle for guessed tokens for free.
type qrCodeResponse struct {
	ID         uuid.UUID  `json:"id"`
	TenantID   uuid.UUID  `json:"tenant_id"`
	BranchID   uuid.UUID  `json:"branch_id"`
	TableID    uuid.UUID  `json:"table_id"`
	TableLabel string     `json:"table_label"`
	Status     string     `json:"status"`
	CreatedBy  uuid.UUID  `json:"created_by"`
	RevokedAt  *time.Time `json:"revoked_at"`
	RevokedBy  *uuid.UUID `json:"revoked_by"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

func toQRCodeResponse(c domain.QRCode) qrCodeResponse {
	return qrCodeResponse{
		ID:         c.ID,
		TenantID:   c.TenantID,
		BranchID:   c.BranchID,
		TableID:    c.TableID,
		TableLabel: c.TableLabel,
		Status:     string(c.Status),
		CreatedBy:  c.CreatedBy,
		RevokedAt:  c.RevokedAt,
		RevokedBy:  c.RevokedBy,
		CreatedAt:  c.CreatedAt,
		UpdatedAt:  c.UpdatedAt,
	}
}

// issuedQRCodeResponse is returned by create and rotate ONLY.
//
// Token is the raw token, surfaced here for the one and only time it exists:
// the database stores just its hash, so if the caller loses this response the
// sticker can only be replaced, never recovered. It is kept in a separate type
// from qrCodeResponse precisely so that "which endpoints return the token" is
// answerable by looking at the type's construction sites.
type issuedQRCodeResponse struct {
	QRCode qrCodeResponse `json:"qr_code"`
	Token  string         `json:"token"`
}

func toIssuedQRCodeResponse(i service.IssuedQRCode) issuedQRCodeResponse {
	return issuedQRCodeResponse{QRCode: toQRCodeResponse(i.Code), Token: i.RawToken}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// requireAdminPrincipal extracts the staff principal and verifies TenantID is
// set. Named apart from the guest chain's helpers so a public handler can
// never reach for it by mistake.
func requireAdminPrincipal(w http.ResponseWriter, r *http.Request) (auth.Principal, bool) {
	p, err := auth.FromContext(r.Context())
	if err != nil || p.TenantID == uuid.Nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return auth.Principal{}, false
	}
	return p, true
}

func adminPathUUID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return uuid.Nil, false
	}
	return id, true
}

func (h *AdminHandler) adminError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, pub.ErrNotFound):
		http.Error(w, "not found", http.StatusNotFound)
	case errors.Is(err, pub.ErrInvalidTransition):
		http.Error(w, "invalid status transition", http.StatusConflict)
	case errors.Is(err, pub.ErrBranchForbidden):
		http.Error(w, "forbidden", http.StatusForbidden)
	default:
		h.logger.Error("storefront admin handler error", zap.Error(err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}

func respondAdminJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
