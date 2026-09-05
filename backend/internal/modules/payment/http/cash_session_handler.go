package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/payment/domain"
	pub "onlinemenu.tr/internal/modules/payment/public"
	"onlinemenu.tr/internal/modules/payment/service"
)

// ─── Wire DTOs ──────────────────────────────────────────────────────────────

// denominationRequest mirrors domain.DenominationCount.
type denominationRequest struct {
	DenominationMinor int64 `json:"denomination_minor"`
	Count             int   `json:"count"`
}

func toDenominationRequests(d []domain.DenominationCount) []denominationRequest {
	out := make([]denominationRequest, 0, len(d))
	for _, item := range d {
		out = append(out, denominationRequest{DenominationMinor: item.DenominationMinor, Count: item.Count})
	}
	return out
}

func toDomainDenominations(d []denominationRequest) []domain.DenominationCount {
	if len(d) == 0 {
		return nil
	}
	out := make([]domain.DenominationCount, len(d))
	for i, item := range d {
		out[i] = domain.DenominationCount{DenominationMinor: item.DenominationMinor, Count: item.Count}
	}
	return out
}

// cashSessionResponse is the single wire shape for a cash session, mirroring
// paymentResponse's rationale: handlers never serialize the domain struct
// directly (docs/lessons-from-b2b.md).
type cashSessionResponse struct {
	ID                   uuid.UUID             `json:"id"`
	BranchID             uuid.UUID             `json:"branch_id"`
	Status               string                `json:"status"`
	OpeningCountedAmount int64                 `json:"opening_counted_amount"`
	OpeningNotes         string                `json:"opening_notes"`
	OpenedBy             uuid.UUID             `json:"opened_by"`
	OpenedAt             string                `json:"opened_at"`
	ClosingCountedAmount *int64                `json:"closing_counted_amount"`
	ClosingDenominations []denominationRequest `json:"closing_denominations"`
	ClosingNotes         string                `json:"closing_notes"`
	ClosingSubmittedAt   *string               `json:"closing_submitted_at"`
	ClosedBy             *uuid.UUID            `json:"closed_by"`
	ClosedAt             *string               `json:"closed_at"`
	// Reconciliation figures — always present, computed fresh on every read
	// (ADR-DATA-008), never the persisted struct's own fields.
	MovementsNet      int64  `json:"movements_net"`
	CashPaymentsTaken int64  `json:"cash_payments_taken"`
	ExpectedClose     int64  `json:"expected_close"`
	Difference        *int64 `json:"difference"`
}

func formatTimePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}

func toCashSessionResponse(v service.CashSessionView) cashSessionResponse {
	s := v.Session
	return cashSessionResponse{
		ID:                   s.ID,
		BranchID:             s.BranchID,
		Status:               string(s.Status),
		OpeningCountedAmount: s.OpeningCountedAmount,
		OpeningNotes:         s.OpeningNotes,
		OpenedBy:             s.OpenedBy,
		OpenedAt:             s.OpenedAt.UTC().Format(time.RFC3339),
		ClosingCountedAmount: s.ClosingCountedAmount,
		ClosingDenominations: toDenominationRequests(s.ClosingDenominations),
		ClosingNotes:         s.ClosingNotes,
		ClosingSubmittedAt:   formatTimePtr(s.ClosingSubmittedAt),
		ClosedBy:             s.ClosedBy,
		ClosedAt:             formatTimePtr(s.ClosedAt),
		MovementsNet:         v.MovementsNet,
		CashPaymentsTaken:    v.CashPaymentsTaken,
		ExpectedClose:        v.ExpectedClose,
		Difference:           v.Difference,
	}
}

// ─── Handlers ───────────────────────────────────────────────────────────────

type openCashSessionRequest struct {
	BranchID             uuid.UUID `json:"branch_id"`
	OpeningCountedAmount int64     `json:"opening_counted_amount"`
	OpeningNotes         string    `json:"opening_notes"`
}

// openCashSession answers POST /api/v1/payments/cash-sessions.
func (h *Handler) openCashSession(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	var req openCashSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	view, err := h.sessions.Open(r.Context(), p, service.OpenCashSessionRequest{
		BranchID:             req.BranchID,
		OpeningCountedAmount: req.OpeningCountedAmount,
		OpeningNotes:         req.OpeningNotes,
	})
	switch {
	case errors.Is(err, pub.ErrInvalidInput):
		// 422, not 500: the caller sent a bad value. Falling through to the
		// generic arm would both lie to the client and log a false alarm.
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	case errors.Is(err, pub.ErrBranchForbidden):
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	case errors.Is(err, pub.ErrCashSessionAlreadyOpen):
		http.Error(w, "branch already has an open cash session", http.StatusConflict)
		return
	case err != nil:
		h.logger.Error("payment: open cash session", zap.Error(err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	respondJSON(w, http.StatusCreated, toCashSessionResponse(view))
}

// getActiveCashSession answers GET /api/v1/payments/cash-sessions/active?branch_id=<uuid>.
func (h *Handler) getActiveCashSession(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	branchID, ok := requireBranchIDQuery(w, r)
	if !ok {
		return
	}

	view, err := h.sessions.GetActive(r.Context(), p, branchID)
	switch {
	case errors.Is(err, pub.ErrInvalidInput):
		// 422, not 500: the caller sent a bad value. Falling through to the
		// generic arm would both lie to the client and log a false alarm.
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	case errors.Is(err, pub.ErrBranchForbidden):
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	case errors.Is(err, pub.ErrNotFound):
		http.Error(w, "no open cash session for this branch", http.StatusNotFound)
		return
	case err != nil:
		h.logger.Error("payment: get active cash session", zap.Error(err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	respondJSON(w, http.StatusOK, toCashSessionResponse(view))
}

type recordCashMovementRequest struct {
	Direction   string `json:"direction"`
	AmountMinor int64  `json:"amount_minor"`
	Reason      string `json:"reason"`
}

type cashMovementResponse struct {
	ID          uuid.UUID `json:"id"`
	SessionID   uuid.UUID `json:"session_id"`
	Direction   string    `json:"direction"`
	AmountMinor int64     `json:"amount_minor"`
	Reason      string    `json:"reason"`
	CreatedBy   uuid.UUID `json:"created_by"`
	CreatedAt   string    `json:"created_at"`
}

// recordCashMovement answers POST /api/v1/payments/cash-sessions/{id}/movements.
func (h *Handler) recordCashMovement(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	sessionID, ok := requireURLID(w, r)
	if !ok {
		return
	}
	var req recordCashMovementRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	movement, err := h.sessions.RecordMovement(r.Context(), p, sessionID, service.RecordMovementRequest{
		Direction:   domain.CashMovementDirection(req.Direction),
		AmountMinor: req.AmountMinor,
		Reason:      req.Reason,
	})
	switch {
	case errors.Is(err, pub.ErrInvalidInput):
		// 422, not 500: the caller sent a bad value. Falling through to the
		// generic arm would both lie to the client and log a false alarm.
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	case errors.Is(err, pub.ErrBranchForbidden):
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	case errors.Is(err, pub.ErrNotFound):
		http.Error(w, "cash session not found", http.StatusNotFound)
		return
	case errors.Is(err, domain.ErrInvalidCashSessionTransition):
		http.Error(w, err.Error(), http.StatusConflict)
		return
	case err != nil:
		h.logger.Error("payment: record cash movement", zap.Error(err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	respondJSON(w, http.StatusCreated, cashMovementResponse{
		ID:          movement.ID,
		SessionID:   movement.SessionID,
		Direction:   string(movement.Direction),
		AmountMinor: movement.AmountMinor,
		Reason:      movement.Reason,
		CreatedBy:   movement.CreatedBy,
		CreatedAt:   movement.CreatedAt.UTC().Format(time.RFC3339),
	})
}

func toCashMovementResponse(m domain.CashMovement) cashMovementResponse {
	return cashMovementResponse{
		ID:          m.ID,
		SessionID:   m.SessionID,
		Direction:   string(m.Direction),
		AmountMinor: m.AmountMinor,
		Reason:      m.Reason,
		CreatedBy:   m.CreatedBy,
		CreatedAt:   m.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// listCashMovements answers GET /api/v1/payments/cash-sessions/{id}/movements
// — the hareket defteri (movement ledger) the POS cash-session screen renders
// alongside the live reconciliation figures already exposed on
// cashSessionResponse.
func (h *Handler) listCashMovements(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	sessionID, ok := requireURLID(w, r)
	if !ok {
		return
	}

	movements, err := h.sessions.ListMovements(r.Context(), p, sessionID)
	switch {
	case errors.Is(err, pub.ErrBranchForbidden):
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	case errors.Is(err, pub.ErrNotFound):
		http.Error(w, "cash session not found", http.StatusNotFound)
		return
	case err != nil:
		h.logger.Error("payment: list cash movements", zap.Error(err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	out := make([]cashMovementResponse, len(movements))
	for i, m := range movements {
		out[i] = toCashMovementResponse(m)
	}
	respondJSON(w, http.StatusOK, out)
}

type submitClosingCountRequest struct {
	ClosingCountedAmount int64                 `json:"closing_counted_amount"`
	Denominations        []denominationRequest `json:"denominations"`
	Notes                string                `json:"notes"`
}

// submitClosingCount answers POST /api/v1/payments/cash-sessions/{id}/closing-count.
func (h *Handler) submitClosingCount(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	sessionID, ok := requireURLID(w, r)
	if !ok {
		return
	}
	var req submitClosingCountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	view, err := h.sessions.SubmitClosingCount(r.Context(), p, sessionID, service.SubmitClosingCountRequest{
		ClosingCountedAmount: req.ClosingCountedAmount,
		Denominations:        toDomainDenominations(req.Denominations),
		Notes:                req.Notes,
	})
	switch {
	case errors.Is(err, pub.ErrInvalidInput):
		// 422, not 500: the caller sent a bad value. Falling through to the
		// generic arm would both lie to the client and log a false alarm.
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	case errors.Is(err, pub.ErrBranchForbidden):
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	case errors.Is(err, pub.ErrNotFound):
		http.Error(w, "cash session not found", http.StatusNotFound)
		return
	case errors.Is(err, domain.ErrDenominationSumMismatch):
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	case errors.Is(err, domain.ErrInvalidCashSessionTransition):
		http.Error(w, err.Error(), http.StatusConflict)
		return
	case err != nil:
		h.logger.Error("payment: submit closing count", zap.Error(err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	respondJSON(w, http.StatusOK, toCashSessionResponse(view))
}

// cannotCloseResponse is a structured blocker list, not a flattened string —
// the whole point of collecting every reason in cannotClose() is for a caller
// (eventually the POS UI) to render which blockers apply, not just the first.
type cannotCloseResponse struct {
	Code    string   `json:"code"`
	Reasons []string `json:"reasons"`
}

// closeCashSession answers POST /api/v1/payments/cash-sessions/{id}/close.
func (h *Handler) closeCashSession(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	sessionID, ok := requireURLID(w, r)
	if !ok {
		return
	}

	view, err := h.sessions.Close(r.Context(), p, sessionID)
	var cannotClose *service.CashSessionCannotCloseError
	switch {
	case errors.Is(err, pub.ErrInvalidInput):
		// 422, not 500: the caller sent a bad value. Falling through to the
		// generic arm would both lie to the client and log a false alarm.
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	case errors.Is(err, pub.ErrBranchForbidden):
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	case errors.Is(err, pub.ErrNotFound):
		http.Error(w, "cash session not found", http.StatusNotFound)
		return
	case errors.As(err, &cannotClose):
		respondJSON(w, http.StatusConflict, cannotCloseResponse{Code: "cannot_close", Reasons: cannotClose.Reasons})
		return
	case errors.Is(err, domain.ErrInvalidCashSessionTransition):
		http.Error(w, err.Error(), http.StatusConflict)
		return
	case err != nil:
		h.logger.Error("payment: close cash session", zap.Error(err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	respondJSON(w, http.StatusOK, toCashSessionResponse(view))
}
