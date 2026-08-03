package http

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	pub "onlinemenu.tr/internal/modules/payment/public"
)

// joinCashSessionRequest is the POST .../participants body. Pin is optional
// (service.CashSessionPinService.Join): joining always records participation;
// a supplied pin is set/replaced in the same call. Omitting it just means
// this person cannot be PIN-switched into later, not that they cannot join.
type joinCashSessionRequest struct {
	Pin string `json:"pin"`
}

// joinCashSession answers POST /api/v1/payments/cash-sessions/{id}/participants.
func (h *Handler) joinCashSession(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	sessionID, ok := requireURLID(w, r)
	if !ok {
		return
	}
	var req joinCashSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	err := h.sessionPin.Join(r.Context(), p, sessionID, req.Pin)
	switch {
	case errors.Is(err, pub.ErrInvalidInput):
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	case errors.Is(err, pub.ErrSessionScopedPrincipal):
		// 403, not 401: the caller IS authenticated, just not in the
		// "trusted moment" this action requires.
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	case errors.Is(err, pub.ErrBranchForbidden):
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	case errors.Is(err, pub.ErrNotFound):
		http.Error(w, "cash session not found", http.StatusNotFound)
		return
	case errors.Is(err, pub.ErrCashSessionClosed):
		http.Error(w, "cash session is closed", http.StatusConflict)
		return
	case err != nil:
		h.logger.Error("payment: join cash session", zap.Error(err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// switchCashierRequest is the POST .../switch body.
type switchCashierRequest struct {
	PersonID uuid.UUID `json:"person_id"`
	Pin      string    `json:"pin"`
}

type switchCashierResponse struct {
	Token string `json:"token"`
}

// switchCashier answers POST /api/v1/payments/cash-sessions/{id}/switch.
//
// Every negative outcome — wrong PIN, PIN never set, person not a
// participant of this session, or a person_id that does not exist at all —
// answers identically: 401 with the same body. Do not add a case that
// distinguishes any of these; that is precisely the enumeration channel
// ADR-DATA-008's PIN akışı requires closed (see
// service.CashSessionPinService.Switch and identity/service/pin.go).
func (h *Handler) switchCashier(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	sessionID, ok := requireURLID(w, r)
	if !ok {
		return
	}
	var req switchCashierRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.PersonID == uuid.Nil {
		http.Error(w, "person_id is required", http.StatusUnprocessableEntity)
		return
	}

	token, err := h.sessionPin.Switch(r.Context(), p, sessionID, req.PersonID, req.Pin)
	switch {
	case errors.Is(err, pub.ErrBranchForbidden):
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	case errors.Is(err, pub.ErrNotFound):
		http.Error(w, "cash session not found", http.StatusNotFound)
		return
	case errors.Is(err, pub.ErrCashSessionClosed):
		http.Error(w, "cash session is closed", http.StatusConflict)
		return
	case errors.Is(err, pub.ErrPinVerificationFailed):
		// Single generic message, same for every negative-outcome branch —
		// see the function doc comment.
		http.Error(w, "verification failed", http.StatusUnauthorized)
		return
	case err != nil:
		h.logger.Error("payment: switch cashier", zap.Error(err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	respondJSON(w, http.StatusOK, switchCashierResponse{Token: token})
}

// resetCashierPin answers
// POST /api/v1/payments/cash-sessions/{id}/participants/{personID}/pin-reset.
// Manager-only (OPA payment.cash_session.pin_reset). It never accepts or
// returns a PIN value — see identitypub.CashierPinService.ResetPin.
func (h *Handler) resetCashierPin(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	sessionID, ok := requireURLID(w, r)
	if !ok {
		return
	}
	personID, err := uuid.Parse(chi.URLParam(r, "personID"))
	if err != nil {
		http.Error(w, "invalid person id", http.StatusBadRequest)
		return
	}

	err = h.sessionPin.ResetPin(r.Context(), p, sessionID, personID)
	switch {
	case errors.Is(err, pub.ErrBranchForbidden):
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	case errors.Is(err, pub.ErrNotFound):
		http.Error(w, "cash session not found", http.StatusNotFound)
		return
	case err != nil:
		h.logger.Error("payment: reset cashier pin", zap.Error(err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
