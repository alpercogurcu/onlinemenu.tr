package apiclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// ADR-DATA-008 "PIN akışının ayrıntıları" — participant list, join, and
// PIN-based cashier switching. Every wire struct below mirrors
// payment/http/cash_session_pin_handler.go's request/response DTOs 1:1
// (verified against source, same discipline as cash_session.go's file-level
// comment). This client NEVER stores, logs, or inspects a PIN value beyond
// putting it in the one outgoing request body it belongs in.

// Participant mirrors cash_session_pin_handler.go's participantResponse —
// one entry in a cash session's participant list. FullName is the only
// person-identifying field the backend ever sends here; there is no email.
type Participant struct {
	PersonID string `json:"person_id"`
	FullName string `json:"full_name"`
	HasPin   bool   `json:"has_pin"`
	Locked   bool   `json:"locked"`
}

type participantListResponse struct {
	Participants []Participant `json:"participants"`
}

// ListCashSessionParticipants calls
// GET /api/v1/payments/cash-sessions/{id}/participants.
func (c *Client) ListCashSessionParticipants(ctx context.Context, sessionID string) ([]Participant, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("apiclient: list cash session participants: session id is required")
	}
	var resp participantListResponse
	path := "/api/v1/payments/cash-sessions/" + url.PathEscape(sessionID) + "/participants"
	if err := c.do(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, fmt.Errorf("apiclient: list cash session participants: %w", err)
	}
	return resp.Participants, nil
}

type joinCashSessionRequest struct {
	Pin string `json:"pin"`
}

// JoinCashSession calls POST /api/v1/payments/cash-sessions/{id}/participants
// as the CURRENT principal — it always records/refreshes THIS caller's own
// participation, and (when pin is non-empty) sets THIS caller's own PIN in
// the same call; there is no way to join or set a pin on another person's
// behalf through this method (mirrors the backend: Join always writes to
// principal.PersonID, never a caller-supplied id — see
// payment/service.CashSessionPinService.Join). pin may be empty: joining
// without one still records participation, it just leaves this person
// unselectable for a later PIN switch (see backend's joinCashSessionRequest
// doc comment).
func (c *Client) JoinCashSession(ctx context.Context, sessionID, pin string) error {
	if sessionID == "" {
		return fmt.Errorf("apiclient: join cash session: session id is required")
	}
	req := joinCashSessionRequest{Pin: pin}
	path := "/api/v1/payments/cash-sessions/" + url.PathEscape(sessionID) + "/participants"
	if err := c.do(ctx, http.MethodPost, path, req, nil); err != nil {
		return fmt.Errorf("apiclient: join cash session: %w", err)
	}
	return nil
}

type switchCashierRequest struct {
	PersonID string `json:"person_id"`
	Pin      string `json:"pin"`
}

type switchCashierResponse struct {
	Token string `json:"token"`
}

// ErrPinVerificationFailed is SwitchCashier's sentinel for EVERY negative
// verification outcome the backend can produce — wrong pin, pin never set,
// locked out, or targetPersonID not a participant of this session, all
// answered as an indistinguishable 401 by design (ADR-DATA-008 PIN akışı;
// see payment/http/cash_session_pin_handler.go's switchCashier doc
// comment). Callers must render this as ONE generic message and must never
// attempt to guess or explain which of those reasons applied — doing so
// would reopen the enumeration channel the backend went out of its way to
// close.
var ErrPinVerificationFailed = errors.New("apiclient: pin verification failed")

// SwitchCashier calls POST /api/v1/payments/cash-sessions/{id}/switch,
// which the backend gates with Idempotency-Key (handler.go's RegisterRoutes)
// — hence doIdempotentNoRecovery rather than plain do. NoRecovery, not
// doIdempotent: see doWithHeadersNoRecovery's doc comment for why this
// call's 401 must never trigger the CTX-401 recovery hook.
//
// On success, returns the session-scoped CTX token verbatim — the caller
// (main.App) is responsible for installing it via SetSessionToken; this
// method never does so itself, same discipline as SelectKeycloakContext.
// The pin argument is used only to build the one outgoing request body; it
// is never retained on this Client afterward.
func (c *Client) SwitchCashier(ctx context.Context, sessionID, personID, pin string) (string, error) {
	if sessionID == "" {
		return "", fmt.Errorf("apiclient: switch cashier: session id is required")
	}
	if personID == "" {
		return "", fmt.Errorf("apiclient: switch cashier: person id is required")
	}
	var resp switchCashierResponse
	req := switchCashierRequest{PersonID: personID, Pin: pin}
	path := "/api/v1/payments/cash-sessions/" + url.PathEscape(sessionID) + "/switch"
	err := c.doIdempotentNoRecovery(ctx, http.MethodPost, path, req, &resp)
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusUnauthorized {
			return "", fmt.Errorf("apiclient: switch cashier: %w", ErrPinVerificationFailed)
		}
		return "", fmt.Errorf("apiclient: switch cashier: %w", err)
	}
	return resp.Token, nil
}
