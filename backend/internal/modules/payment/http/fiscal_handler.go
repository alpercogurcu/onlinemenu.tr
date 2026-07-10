package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/payment/domain"
	"onlinemenu.tr/internal/modules/payment/fiscal/tokenx"
	"onlinemenu.tr/internal/modules/payment/repo"
	"onlinemenu.tr/internal/platform/auth"
)

// fiscalAdminStore is the persistence surface the fiscal admin API needs.
// Declared here, at the point of use, so the handler is testable without a
// database (accept interfaces, return structs).
type fiscalAdminStore interface {
	UpsertTerminal(ctx context.Context, t repo.FiscalTerminal) (repo.FiscalTerminal, error)
	ListTerminals(ctx context.Context, tenantID, branchID uuid.UUID) ([]repo.FiscalTerminal, error)
	GetTerminal(ctx context.Context, tenantID, id uuid.UUID) (repo.FiscalTerminal, error)
	UpdateTerminal(ctx context.Context, tenantID, id uuid.UUID, patch repo.TerminalPatch) (repo.FiscalTerminal, error)
	ReplaceSections(ctx context.Context, tenantID, terminalID uuid.UUID, sections []domain.DeviceSection) error
	ListSections(ctx context.Context, tenantID, terminalID uuid.UUID) ([]repo.FiscalDeviceSection, error)
	ListSectionMappings(ctx context.Context, tenantID, branchID uuid.UUID) ([]repo.FiscalSectionMapping, error)
	ReplaceSectionMappings(ctx context.Context, tenantID, branchID uuid.UUID, mappings []repo.FiscalSectionMapping) error
}

// FiscalHandler exposes the fiscal device administration API: pairing a
// terminal from its QR code, syncing the device's tax sections and mapping
// catalog categories onto them (ADR-FISCAL-002 §2, §5).
//
// These are configuration endpoints, not money movement, so they carry no
// Idempotency-Key requirement (ADR-SEC-003 scopes it to payment/invoice/check/
// order writes). They are idempotent by construction instead: terminal
// registration upserts on (tenant, vendor, serial), and both section sync and
// mapping writes are full replacements.
type FiscalHandler struct {
	store   fiscalAdminStore
	adapter domain.FiscalDeviceAdapter
	logger  *zap.Logger
	engine  *auth.Engine
}

// FiscalParams groups fx-injected dependencies.
type FiscalParams struct {
	fx.In

	Store   *repo.FiscalAdminRepo
	Adapter domain.FiscalDeviceAdapter
	Logger  *zap.Logger
	Engine  *auth.Engine
}

func NewFiscalHandler(p FiscalParams) *FiscalHandler {
	return &FiscalHandler{store: p.Store, adapter: p.Adapter, logger: p.Logger, engine: p.Engine}
}

func (h *FiscalHandler) permit(action string) func(http.Handler) http.Handler {
	return auth.RequirePermission(h.engine, action)
}

// RegisterRoutes mounts the fiscal admin endpoints. Every route is guarded by
// RequirePermission; only the manager role holds these actions (authz.rego's
// manager wildcard) — device pairing and VAT section mapping are back-office
// configuration, never counter-staff actions.
func (h *FiscalHandler) RegisterRoutes(r *chi.Mux) {
	r.Route("/api/v1/fiscal", func(r chi.Router) {
		r.With(h.permit("payment.fiscal_terminal.read")).Get("/terminals", h.listTerminals)
		r.With(h.permit("payment.fiscal_terminal.manage")).Post("/terminals", h.createTerminal)
		r.With(h.permit("payment.fiscal_terminal.manage")).Patch("/terminals/{id}", h.updateTerminal)
		r.With(h.permit("payment.fiscal_terminal.manage")).Post("/terminals/{id}/sync-sections", h.syncSections)
		r.With(h.permit("payment.fiscal_terminal.read")).Get("/terminals/{id}/sections", h.listSections)

		r.With(h.permit("payment.fiscal_section_mapping.read")).Get("/section-mappings", h.listSectionMappings)
		r.With(h.permit("payment.fiscal_section_mapping.manage")).Put("/section-mappings", h.replaceSectionMappings)
	})
}

// ─── DTOs ───────────────────────────────────────────────────────────────────

type terminalResponse struct {
	ID                uuid.UUID `json:"id"`
	TenantID          uuid.UUID `json:"tenant_id"`
	BranchID          uuid.UUID `json:"branch_id"`
	Vendor            string    `json:"vendor"`
	TerminalSerial    string    `json:"terminal_serial"`
	VendorMerchantRef string    `json:"vendor_merchant_ref"`
	VendorBranchRef   string    `json:"vendor_branch_ref"`
	Label             string    `json:"label"`
	BasketMode        string    `json:"basket_mode"`
	IsActive          bool      `json:"is_active"`
	CreatedAt         string    `json:"created_at"`
	UpdatedAt         string    `json:"updated_at"`
}

func toTerminalResponse(t repo.FiscalTerminal) terminalResponse {
	return terminalResponse{
		ID:                t.ID,
		TenantID:          t.TenantID,
		BranchID:          t.BranchID,
		Vendor:            t.Vendor,
		TerminalSerial:    t.TerminalSerial,
		VendorMerchantRef: t.VendorMerchantRef,
		VendorBranchRef:   t.VendorBranchRef,
		Label:             t.Label,
		BasketMode:        t.BasketMode,
		IsActive:          t.IsActive,
		CreatedAt:         t.CreatedAt.Format(time.RFC3339),
		UpdatedAt:         t.UpdatedAt.Format(time.RFC3339),
	}
}

type sectionResponse struct {
	SectionNo    int    `json:"section_no"`
	Name         string `json:"name"`
	TaxPermyriad int    `json:"tax_permyriad"`
	SyncedAt     string `json:"synced_at"`
}

type sectionMappingResponse struct {
	CategoryID uuid.UUID `json:"category_id"`
	SectionNo  int       `json:"section_no"`
}

// createTerminalRequest accepts the device QR verbatim or its three parts
// spelled out. QR wins when both are present: it is the value physically read
// off the device, so it cannot disagree with itself.
type createTerminalRequest struct {
	QR                string    `json:"qr"`
	TerminalSerial    string    `json:"terminal_serial"`
	VendorMerchantRef string    `json:"vendor_merchant_ref"`
	VendorBranchRef   string    `json:"vendor_branch_ref"`
	BranchID          uuid.UUID `json:"branch_id"`
	Label             string    `json:"label"`
	BasketMode        string    `json:"basket_mode"`
}

type updateTerminalRequest struct {
	Label      *string `json:"label"`
	BasketMode *string `json:"basket_mode"`
	IsActive   *bool   `json:"is_active"`
}

type sectionMappingRequest struct {
	CategoryID uuid.UUID `json:"category_id"`
	SectionNo  int       `json:"section_no"`
}

type replaceSectionMappingsRequest struct {
	BranchID uuid.UUID               `json:"branch_id"`
	Mappings []sectionMappingRequest `json:"mappings"`
}

// ─── QR parsing ─────────────────────────────────────────────────────────────

// errInvalidQR reports a QR payload that is not exactly
// merchantRef_branchRef_terminalSerial with all three parts present.
var errInvalidQR = errors.New("qr must be merchantRef_branchRef_terminalSerial")

func parseQR(qr string) (merchantRef, branchRef, serial string, err error) {
	parts := strings.Split(strings.TrimSpace(qr), "_")
	if len(parts) != 3 {
		return "", "", "", errInvalidQR
	}
	for _, p := range parts {
		if strings.TrimSpace(p) == "" {
			return "", "", "", errInvalidQR
		}
	}
	return parts[0], parts[1], parts[2], nil
}

// ─── Handlers ───────────────────────────────────────────────────────────────

func (h *FiscalHandler) createTerminal(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}

	var req createTerminalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.QR != "" {
		merchantRef, branchRef, serial, err := parseQR(req.QR)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		req.VendorMerchantRef, req.VendorBranchRef, req.TerminalSerial = merchantRef, branchRef, serial
	}

	if req.BranchID == uuid.Nil {
		http.Error(w, "branch_id is required", http.StatusUnprocessableEntity)
		return
	}
	if strings.TrimSpace(req.TerminalSerial) == "" {
		http.Error(w, "terminal_serial is required (or supply qr)", http.StatusUnprocessableEntity)
		return
	}
	if req.BasketMode == "" {
		req.BasketMode = string(tokenx.BasketModeInstant)
	}
	// Reject an unknown mode here rather than letting the table's CHECK raise a
	// 23514 that would surface as an opaque 500.
	if !tokenx.BasketMode(req.BasketMode).Valid() {
		http.Error(w, "basket_mode must be 'instant' or 'list'", http.StatusUnprocessableEntity)
		return
	}

	terminal, err := h.store.UpsertTerminal(r.Context(), repo.FiscalTerminal{
		TenantID:          p.TenantID,
		BranchID:          req.BranchID,
		Vendor:            tokenx.Vendor,
		TerminalSerial:    strings.TrimSpace(req.TerminalSerial),
		VendorMerchantRef: req.VendorMerchantRef,
		VendorBranchRef:   req.VendorBranchRef,
		Label:             req.Label,
		BasketMode:        req.BasketMode,
	})
	if err != nil {
		if errors.Is(err, repo.ErrTerminalSerialTaken) {
			http.Error(w, "terminal serial already registered", http.StatusConflict)
			return
		}
		h.logger.Error("payment: upsert fiscal terminal", zap.Error(err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	respondJSON(w, http.StatusCreated, toTerminalResponse(terminal))
}

func (h *FiscalHandler) listTerminals(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	branchID, ok := requireBranchIDQuery(w, r)
	if !ok {
		return
	}

	terminals, err := h.store.ListTerminals(r.Context(), p.TenantID, branchID)
	if err != nil {
		h.logger.Error("payment: list fiscal terminals", zap.Error(err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	out := make([]terminalResponse, len(terminals))
	for i, t := range terminals {
		out[i] = toTerminalResponse(t)
	}
	respondJSON(w, http.StatusOK, out)
}

func (h *FiscalHandler) updateTerminal(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	id, ok := requireURLID(w, r)
	if !ok {
		return
	}

	var req updateTerminalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.BasketMode != nil && !tokenx.BasketMode(*req.BasketMode).Valid() {
		http.Error(w, "basket_mode must be 'instant' or 'list'", http.StatusUnprocessableEntity)
		return
	}

	terminal, err := h.store.UpdateTerminal(r.Context(), p.TenantID, id, repo.TerminalPatch{
		Label:      req.Label,
		BasketMode: req.BasketMode,
		IsActive:   req.IsActive,
	})
	if err != nil {
		if errors.Is(err, repo.ErrTerminalNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		h.logger.Error("payment: update fiscal terminal", zap.Error(err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	respondJSON(w, http.StatusOK, toTerminalResponse(terminal))
}

// syncSections pulls the device's section table and makes the stored copy match
// it exactly. The adapter is only optionally a SectionSyncer (a wire/DLL driver
// may not enumerate sections), so an adapter without the capability answers 501
// instead of pretending the device has no sections — which would erase every
// stored section and break the sale-time section resolve.
func (h *FiscalHandler) syncSections(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	id, ok := requireURLID(w, r)
	if !ok {
		return
	}

	syncer, ok := h.adapter.(domain.SectionSyncer)
	if !ok {
		http.Error(w, "fiscal adapter does not support section sync", http.StatusNotImplemented)
		return
	}

	terminal, err := h.store.GetTerminal(r.Context(), p.TenantID, id)
	if err != nil {
		if errors.Is(err, repo.ErrTerminalNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		h.logger.Error("payment: get fiscal terminal", zap.Error(err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	sections, err := syncer.FetchSections(r.Context(), terminal.TerminalSerial)
	if err != nil {
		// The device or its cloud is unreachable/misconfigured — that is an
		// upstream fault, not ours: 502 keeps it distinguishable in the logs
		// from a genuine server error.
		h.logger.Error("payment: fetch device sections",
			zap.String("terminal_serial", terminal.TerminalSerial), zap.Error(err))
		http.Error(w, "fiscal device unreachable", http.StatusBadGateway)
		return
	}
	if len(sections) == 0 {
		// A device with no sections cannot price a single line. Refusing to
		// persist the empty set preserves the last good sync.
		http.Error(w, "device reported no sections", http.StatusBadGateway)
		return
	}

	if err := h.store.ReplaceSections(r.Context(), p.TenantID, terminal.ID, sections); err != nil {
		h.logger.Error("payment: replace device sections", zap.Error(err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	stored, err := h.store.ListSections(r.Context(), p.TenantID, terminal.ID)
	if err != nil {
		h.logger.Error("payment: list device sections", zap.Error(err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	respondJSON(w, http.StatusOK, toSectionResponses(stored))
}

func (h *FiscalHandler) listSections(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	id, ok := requireURLID(w, r)
	if !ok {
		return
	}

	// Prove the terminal exists (and belongs to this tenant) so an unknown id
	// answers 404 rather than an empty list that reads as "device has none".
	if _, err := h.store.GetTerminal(r.Context(), p.TenantID, id); err != nil {
		if errors.Is(err, repo.ErrTerminalNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		h.logger.Error("payment: get fiscal terminal", zap.Error(err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	sections, err := h.store.ListSections(r.Context(), p.TenantID, id)
	if err != nil {
		h.logger.Error("payment: list device sections", zap.Error(err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	respondJSON(w, http.StatusOK, toSectionResponses(sections))
}

func (h *FiscalHandler) listSectionMappings(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	branchID, ok := requireBranchIDQuery(w, r)
	if !ok {
		return
	}

	mappings, err := h.store.ListSectionMappings(r.Context(), p.TenantID, branchID)
	if err != nil {
		h.logger.Error("payment: list section mappings", zap.Error(err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	respondJSON(w, http.StatusOK, toSectionMappingResponses(mappings))
}

// replaceSectionMappings swaps the branch's entire mapping set. Sending the
// same body twice yields the same state, so no Idempotency-Key is needed.
func (h *FiscalHandler) replaceSectionMappings(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}

	var req replaceSectionMappingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.BranchID == uuid.Nil {
		http.Error(w, "branch_id is required", http.StatusUnprocessableEntity)
		return
	}

	mappings := make([]repo.FiscalSectionMapping, 0, len(req.Mappings))
	seen := make(map[uuid.UUID]struct{}, len(req.Mappings))
	for _, m := range req.Mappings {
		if m.CategoryID == uuid.Nil {
			http.Error(w, "category_id is required for every mapping", http.StatusUnprocessableEntity)
			return
		}
		if m.SectionNo <= 0 {
			http.Error(w, "section_no must be positive", http.StatusUnprocessableEntity)
			return
		}
		// The unique index would reject a duplicate category mid-transaction as
		// an opaque 500; catching it here names the offending field instead.
		if _, dup := seen[m.CategoryID]; dup {
			http.Error(w, "duplicate category_id in mappings", http.StatusUnprocessableEntity)
			return
		}
		seen[m.CategoryID] = struct{}{}
		mappings = append(mappings, repo.FiscalSectionMapping{CategoryID: m.CategoryID, SectionNo: m.SectionNo})
	}

	if err := h.store.ReplaceSectionMappings(r.Context(), p.TenantID, req.BranchID, mappings); err != nil {
		h.logger.Error("payment: replace section mappings", zap.Error(err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	stored, err := h.store.ListSectionMappings(r.Context(), p.TenantID, req.BranchID)
	if err != nil {
		h.logger.Error("payment: list section mappings", zap.Error(err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	respondJSON(w, http.StatusOK, toSectionMappingResponses(stored))
}

// ─── Shared request helpers ─────────────────────────────────────────────────

func requireURLID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return uuid.Nil, false
	}
	return id, true
}

func requireBranchIDQuery(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	raw := r.URL.Query().Get("branch_id")
	if raw == "" {
		http.Error(w, "branch_id is required", http.StatusUnprocessableEntity)
		return uuid.Nil, false
	}
	branchID, err := uuid.Parse(raw)
	if err != nil {
		http.Error(w, "invalid branch_id", http.StatusBadRequest)
		return uuid.Nil, false
	}
	return branchID, true
}

func toSectionResponses(sections []repo.FiscalDeviceSection) []sectionResponse {
	out := make([]sectionResponse, len(sections))
	for i, s := range sections {
		out[i] = sectionResponse{
			SectionNo:    s.SectionNo,
			Name:         s.Name,
			TaxPermyriad: s.TaxPermyriad,
			SyncedAt:     s.SyncedAt.Format(time.RFC3339),
		}
	}
	return out
}

func toSectionMappingResponses(mappings []repo.FiscalSectionMapping) []sectionMappingResponse {
	out := make([]sectionMappingResponse, len(mappings))
	for i, m := range mappings {
		out[i] = sectionMappingResponse{CategoryID: m.CategoryID, SectionNo: m.SectionNo}
	}
	return out
}
