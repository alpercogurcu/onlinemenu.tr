package apiclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ADR-DATA-008: kasa oturumu (cash session). Every wire struct below mirrors
// payment/http/cash_session_handler.go's response DTOs 1:1 (verified against
// source, not assumed — same discipline as pos.go's file-level comment).

// DenominationCount is one line of a kupur dokumu, mirroring
// payment/domain.DenominationCount / the handler's denominationRequest.
type DenominationCount struct {
	DenominationMinor int64 `json:"denomination_minor"`
	Count             int   `json:"count"`
}

// CashSession mirrors cash_session_handler.go's cashSessionResponse. The
// reconciliation figures (MovementsNet..Difference) are computed fresh by the
// backend on every read (ADR-DATA-008) — never cache them across calls,
// always take the values from the most recent response.
type CashSession struct {
	ID                   string              `json:"id"`
	BranchID             string              `json:"branch_id"`
	Status               string              `json:"status"`
	OpeningCountedAmount int64               `json:"opening_counted_amount"`
	OpeningNotes         string              `json:"opening_notes"`
	OpenedBy             string              `json:"opened_by"`
	OpenedAt             time.Time           `json:"opened_at"`
	ClosingCountedAmount *int64              `json:"closing_counted_amount"`
	ClosingDenominations []DenominationCount `json:"closing_denominations"`
	ClosingNotes         string              `json:"closing_notes"`
	ClosingSubmittedAt   *time.Time          `json:"closing_submitted_at"`
	ClosedBy             *string             `json:"closed_by"`
	ClosedAt             *time.Time          `json:"closed_at"`
	MovementsNet         int64               `json:"movements_net"`
	CashPaymentsTaken    int64               `json:"cash_payments_taken"`
	ExpectedClose        int64               `json:"expected_close"`
	Difference           *int64              `json:"difference"`
}

// CashMovement mirrors the handler's cashMovementResponse.
type CashMovement struct {
	ID          string    `json:"id"`
	SessionID   string    `json:"session_id"`
	Direction   string    `json:"direction"`
	AmountMinor int64     `json:"amount_minor"`
	Reason      string    `json:"reason"`
	CreatedBy   string    `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
}

// ErrNoActiveCashSession is returned by GetActiveCashSession when the branch
// has no open session (backend 404 "no open cash session for this branch" —
// see cash_session_handler.go's getActiveCashSession). This is an expected,
// common steady state (every branch starts a shift with no session open),
// not a fault — callers should treat it like LoginWithKeycloak's
// needs-context-selection branch, not like a transient read failure.
var ErrNoActiveCashSession = errors.New("apiclient: no active cash session for branch")

// CashSessionCannotCloseError mirrors payment/service.CashSessionCannotCloseError's
// wire shape (cash_session_handler.go's cannotCloseResponse: {"code":"cannot_close",
// "reasons":[...]}). Returned as a distinguishable typed error (errors.As)
// rather than left folded into APIError's opaque body, so callers can extract
// Reasons without regex-parsing JSON out of an error string — the reasons are
// meant to reach the cashier VERBATIM (ADR-DATA-008's cannotClose guard
// collects every blocking reason on purpose).
type CashSessionCannotCloseError struct {
	Reasons []string
}

func (e *CashSessionCannotCloseError) Error() string {
	return fmt.Sprintf("apiclient: cash session cannot close: %s", strings.Join(e.Reasons, "; "))
}

// cannotCloseWireResponse mirrors cash_session_handler.go's cannotCloseResponse.
type cannotCloseWireResponse struct {
	Code    string   `json:"code"`
	Reasons []string `json:"reasons"`
}

// asCannotCloseError inspects a 409 APIError's body for the cannot_close JSON
// shape. Returns nil, false for any other 409 (e.g. the plain-text
// domain.ErrInvalidCashSessionTransition body from submit-closing-count/close
// called out of turn — that must fall through to the generic APIError path,
// not be mis-parsed here).
func asCannotCloseError(apiErr *APIError) (*CashSessionCannotCloseError, bool) {
	if apiErr.StatusCode != http.StatusConflict {
		return nil, false
	}
	var wire cannotCloseWireResponse
	if err := json.Unmarshal([]byte(apiErr.Body), &wire); err != nil {
		return nil, false
	}
	if wire.Code != "cannot_close" {
		return nil, false
	}
	return &CashSessionCannotCloseError{Reasons: wire.Reasons}, true
}

type openCashSessionRequest struct {
	BranchID             string `json:"branch_id"`
	OpeningCountedAmount int64  `json:"opening_counted_amount"`
	OpeningNotes         string `json:"opening_notes"`
}

// OpenCashSession calls POST /api/v1/payments/cash-sessions. Not
// idempotency-key-gated on the backend (handler.go's RegisterRoutes only
// puts httpx.Idempotency on the movements route for this feature — see that
// route's comment) and has no natural client-side dedup key, so a retry here
// is left to the caller, same as OpenCheck.
func (c *Client) OpenCashSession(ctx context.Context, branchID string, openingCountedAmount int64, openingNotes string) (CashSession, error) {
	if branchID == "" {
		return CashSession{}, fmt.Errorf("apiclient: open cash session: branch_id is required")
	}
	if openingCountedAmount < 0 {
		return CashSession{}, fmt.Errorf("apiclient: open cash session: opening_counted_amount must not be negative")
	}
	var out CashSession
	req := openCashSessionRequest{BranchID: branchID, OpeningCountedAmount: openingCountedAmount, OpeningNotes: openingNotes}
	if err := c.do(ctx, http.MethodPost, "/api/v1/payments/cash-sessions", req, &out); err != nil {
		return CashSession{}, fmt.Errorf("apiclient: open cash session: %w", err)
	}
	return out, nil
}

// GetActiveCashSession calls GET /api/v1/payments/cash-sessions/active?branch_id=.
// Returns ErrNoActiveCashSession (wrapped) when the branch has no open session —
// see that var's doc comment for why this is not folded into a generic error.
func (c *Client) GetActiveCashSession(ctx context.Context, branchID string) (CashSession, error) {
	if branchID == "" {
		return CashSession{}, fmt.Errorf("apiclient: get active cash session: branch_id is required")
	}
	var out CashSession
	path := "/api/v1/payments/cash-sessions/active?" + url.Values{"branch_id": {branchID}}.Encode()
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	if err == nil {
		return out, nil
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
		return CashSession{}, fmt.Errorf("apiclient: get active cash session: %w", ErrNoActiveCashSession)
	}
	return CashSession{}, fmt.Errorf("apiclient: get active cash session: %w", err)
}

type recordCashMovementRequest struct {
	Direction   string `json:"direction"`
	AmountMinor int64  `json:"amount_minor"`
	Reason      string `json:"reason"`
}

// RecordCashMovement calls POST /api/v1/payments/cash-sessions/{id}/movements
// (Idempotency-Key required — handler.go's RegisterRoutes gates this one
// route with httpx.Idempotency, deliberately unlike submit-closing-count and
// close: a bare INSERT with no natural dedup, see that route's comment).
// doIdempotent mints and manages the key exactly as it does for
// PlaceOrder/CloseCheck/RegisterCashPayment — no caller-supplied key needed.
func (c *Client) RecordCashMovement(ctx context.Context, sessionID, direction string, amountMinor int64, reason string) (CashMovement, error) {
	if sessionID == "" {
		return CashMovement{}, fmt.Errorf("apiclient: record cash movement: session id is required")
	}
	if amountMinor <= 0 {
		return CashMovement{}, fmt.Errorf("apiclient: record cash movement: amount_minor must be positive")
	}
	if strings.TrimSpace(reason) == "" {
		return CashMovement{}, fmt.Errorf("apiclient: record cash movement: reason is required")
	}
	var out CashMovement
	req := recordCashMovementRequest{Direction: direction, AmountMinor: amountMinor, Reason: reason}
	path := "/api/v1/payments/cash-sessions/" + url.PathEscape(sessionID) + "/movements"
	if err := c.doIdempotent(ctx, http.MethodPost, path, req, &out); err != nil {
		return CashMovement{}, fmt.Errorf("apiclient: record cash movement: %w", err)
	}
	return out, nil
}

type submitClosingCountRequest struct {
	ClosingCountedAmount int64               `json:"closing_counted_amount"`
	Denominations        []DenominationCount `json:"denominations"`
	Notes                string              `json:"notes"`
}

// SubmitClosingCount calls POST /api/v1/payments/cash-sessions/{id}/closing-count.
// Not idempotency-key-gated on the backend, and deliberately so on this
// client too: the closing_control self-loop (domain.allowedTransitions) makes
// resubmission ("recount") a normal, expected path — retrying it under a
// fresh call is exactly the intended recovery, not a hazard to guard against.
func (c *Client) SubmitClosingCount(ctx context.Context, sessionID string, closingCountedAmount int64, denominations []DenominationCount, notes string) (CashSession, error) {
	if sessionID == "" {
		return CashSession{}, fmt.Errorf("apiclient: submit closing count: session id is required")
	}
	if closingCountedAmount < 0 {
		return CashSession{}, fmt.Errorf("apiclient: submit closing count: closing_counted_amount must not be negative")
	}
	var out CashSession
	req := submitClosingCountRequest{ClosingCountedAmount: closingCountedAmount, Denominations: denominations, Notes: notes}
	path := "/api/v1/payments/cash-sessions/" + url.PathEscape(sessionID) + "/closing-count"
	if err := c.do(ctx, http.MethodPost, path, req, &out); err != nil {
		return CashSession{}, fmt.Errorf("apiclient: submit closing count: %w", err)
	}
	return out, nil
}

// CloseCashSession calls POST /api/v1/payments/cash-sessions/{id}/close.
// On the backend's cannotClose guard refusing (409 with the cannot_close JSON
// body — ADR-DATA-008, today: pending fiscal submissions in the branch), this
// returns a *CashSessionCannotCloseError (use errors.As) carrying every
// blocking reason verbatim, instead of the generic *APIError every other 409
// on this session (e.g. calling close before submitting a closing count)
// still returns unchanged.
func (c *Client) CloseCashSession(ctx context.Context, sessionID string) (CashSession, error) {
	if sessionID == "" {
		return CashSession{}, fmt.Errorf("apiclient: close cash session: session id is required")
	}
	var out CashSession
	path := "/api/v1/payments/cash-sessions/" + url.PathEscape(sessionID) + "/close"
	err := c.do(ctx, http.MethodPost, path, nil, &out)
	if err == nil {
		return out, nil
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		if cannotClose, ok := asCannotCloseError(apiErr); ok {
			return CashSession{}, cannotClose
		}
	}
	return CashSession{}, fmt.Errorf("apiclient: close cash session: %w", err)
}
