package main

import (
	"errors"
	"fmt"

	"onlinemenu.tr/pos-desktop/internal/apiclient"
)

// This file adds the Wails bindings for ADR-DATA-008's kasa oturumu (cash
// session) flow: açılış -> durum/nakit giriş-çıkış -> sayım -> kapanış. Kept
// as a sibling to pos.go rather than growing it (pos.go is already the
// cashier check/order/payment flow; this is a separate lifecycle with its
// own DTOs). Every method here only translates between apiclient's
// Go-shaped types and this file's DTOs — apiclient.Client remains the sole
// HTTP and token authority (see pos.go's file-level comment).

// DenominationCountDTO mirrors apiclient.DenominationCount — one line of a
// kupur dokumu. The fixed Turkish denomination list itself lives in the
// frontend (lib/cashSession.ts's TURKISH_DENOMINATIONS), not here or on the
// backend (ADR-DATA-008): this DTO only carries whatever the cashier counted.
type DenominationCountDTO struct {
	DenominationMinor int64 `json:"denomination_minor"`
	Count             int   `json:"count"`
}

// CashSessionDTO mirrors apiclient.CashSession. Reconciliation figures
// (MovementsNet..Difference) are computed fresh by the backend on EVERY
// call that returns this DTO (ADR-DATA-008) — a caller must always use the
// figures from the most recent response, never memoize them across a
// GetActiveCashSession poll (see the frontend hook's staleness handling for
// closing_control, which exists precisely because these numbers move).
type CashSessionDTO struct {
	ID                   string                 `json:"id"`
	BranchID             string                 `json:"branch_id"`
	Status               string                 `json:"status"`
	OpeningCountedAmount int64                  `json:"opening_counted_amount"`
	OpeningNotes         string                 `json:"opening_notes"`
	OpenedAt             string                 `json:"opened_at"`
	ClosingCountedAmount *int64                 `json:"closing_counted_amount,omitempty"`
	ClosingDenominations []DenominationCountDTO `json:"closing_denominations,omitempty"`
	ClosingNotes         string                 `json:"closing_notes,omitempty"`
	ClosingSubmittedAt   *string                `json:"closing_submitted_at,omitempty"`
	ClosedAt             *string                `json:"closed_at,omitempty"`
	MovementsNet         int64                  `json:"movements_net"`
	CashPaymentsTaken    int64                  `json:"cash_payments_taken"`
	ExpectedClose        int64                  `json:"expected_close"`
	Difference           *int64                 `json:"difference,omitempty"`
}

// CashMovementDTO mirrors apiclient.CashMovement.
type CashMovementDTO struct {
	ID          string `json:"id"`
	SessionID   string `json:"session_id"`
	Direction   string `json:"direction"`
	AmountMinor int64  `json:"amount_minor"`
	Reason      string `json:"reason"`
	CreatedAt   string `json:"created_at"`
}

// CashSessionStateDTO is GetActiveCashSession's return shape: either an open
// session (HasActiveSession true) or none (the ordinary start-of-shift
// state). Mirrors KeycloakLoginResultDTO's discriminated-result pattern
// (app.go) rather than using the error channel for what is an expected,
// common steady state — see apiclient.ErrNoActiveCashSession's doc comment.
type CashSessionStateDTO struct {
	HasActiveSession bool           `json:"has_active_session"`
	Session          CashSessionDTO `json:"session,omitempty"`
}

// CashSessionCloseResultDTO is CloseCashSession's return shape: either a
// closed session (CannotClose false) or the list of blocking reasons the
// backend's cannotClose guard returned (ADR-DATA-008), verbatim — never
// collapsed into a single error string, so the cashier sees every reason
// standing between them and closing the drawer, exactly as the backend's
// CashSessionCannotCloseError collects them.
//
// Reasons is ALWAYS a non-nil (possibly empty) slice, never left as a nil
// zero value: a nil []string marshals to JSON null, and the generated
// TypeScript types this field as string[] — a caller that .map()s over a
// decoded null crashes (see CheckSettlementDTO.Completed's doc comment for
// the same trap already documented once in this file's sibling).
type CashSessionCloseResultDTO struct {
	CannotClose bool           `json:"cannot_close"`
	Reasons     []string       `json:"reasons"`
	Session     CashSessionDTO `json:"session,omitempty"`
}

func toCashSessionDTO(s apiclient.CashSession) CashSessionDTO {
	dto := CashSessionDTO{
		ID:                   s.ID,
		BranchID:             s.BranchID,
		Status:               s.Status,
		OpeningCountedAmount: s.OpeningCountedAmount,
		OpeningNotes:         s.OpeningNotes,
		OpenedAt:             s.OpenedAt.Format(rfc3339Millis),
		ClosingCountedAmount: s.ClosingCountedAmount,
		ClosingNotes:         s.ClosingNotes,
		MovementsNet:         s.MovementsNet,
		CashPaymentsTaken:    s.CashPaymentsTaken,
		ExpectedClose:        s.ExpectedClose,
		Difference:           s.Difference,
	}
	if len(s.ClosingDenominations) > 0 {
		dto.ClosingDenominations = make([]DenominationCountDTO, len(s.ClosingDenominations))
		for i, d := range s.ClosingDenominations {
			dto.ClosingDenominations[i] = DenominationCountDTO{DenominationMinor: d.DenominationMinor, Count: d.Count}
		}
	}
	if s.ClosingSubmittedAt != nil {
		formatted := s.ClosingSubmittedAt.Format(rfc3339Millis)
		dto.ClosingSubmittedAt = &formatted
	}
	if s.ClosedAt != nil {
		formatted := s.ClosedAt.Format(rfc3339Millis)
		dto.ClosedAt = &formatted
	}
	return dto
}

// OpenCashSession opens a new cash session for branchID. openingCountedAmount
// is in kuruş — what the cashier counted in the drawer before the first sale.
// A 409 here means the branch already has an open session (another cashier
// opened it, or this station's own earlier open call already landed) — the
// caller should re-fetch GetActiveCashSession rather than retry blindly.
func (a *App) OpenCashSession(branchID string, openingCountedAmount int64, openingNotes string) (CashSessionDTO, error) {
	if branchID == "" {
		return CashSessionDTO{}, fmt.Errorf("şube seçilmeden kasa açılamaz")
	}
	session, err := a.api.OpenCashSession(a.ctx, branchID, openingCountedAmount, openingNotes)
	if err != nil {
		return CashSessionDTO{}, err
	}
	return toCashSessionDTO(session), nil
}

// GetActiveCashSession returns the branch's current open session (opened or
// closing_control), or HasActiveSession=false if the branch has not started
// a shift yet — see CashSessionStateDTO's doc comment for why this is not an
// error.
func (a *App) GetActiveCashSession(branchID string) (CashSessionStateDTO, error) {
	if branchID == "" {
		return CashSessionStateDTO{}, fmt.Errorf("şube bilgisi eksik — oturum yeniden açılmalı")
	}
	session, err := a.api.GetActiveCashSession(a.ctx, branchID)
	if errors.Is(err, apiclient.ErrNoActiveCashSession) {
		return CashSessionStateDTO{HasActiveSession: false}, nil
	}
	if err != nil {
		return CashSessionStateDTO{}, err
	}
	return CashSessionStateDTO{HasActiveSession: true, Session: toCashSessionDTO(session)}, nil
}

// RecordCashMovement records an in-shift cash in/out (bozuk para koyma /
// kasadan para alma) against sessionID. amountMinor is in kuruş and must be
// positive; direction is "in" or "out". Idempotency-Key handling is entirely
// internal to apiclient.Client (doIdempotent) — this binding does not manage
// one itself, same as PlaceOrder/CloseCheck/RegisterCashPayment.
func (a *App) RecordCashMovement(sessionID, direction string, amountMinor int64, reason string) (CashMovementDTO, error) {
	movement, err := a.api.RecordCashMovement(a.ctx, sessionID, direction, amountMinor, reason)
	if err != nil {
		return CashMovementDTO{}, err
	}
	return CashMovementDTO{
		ID:          movement.ID,
		SessionID:   movement.SessionID,
		Direction:   movement.Direction,
		AmountMinor: movement.AmountMinor,
		Reason:      movement.Reason,
		CreatedAt:   movement.CreatedAt.Format(rfc3339Millis),
	}, nil
}

// ListCashMovements returns the full hareket defteri (movement ledger) for
// sessionID, oldest first — the durum ekranı's cash-in/cash-out list.
func (a *App) ListCashMovements(sessionID string) ([]CashMovementDTO, error) {
	movements, err := a.api.ListCashMovements(a.ctx, sessionID)
	if err != nil {
		return nil, err
	}
	out := make([]CashMovementDTO, len(movements))
	for i, m := range movements {
		out[i] = CashMovementDTO{
			ID:          m.ID,
			SessionID:   m.SessionID,
			Direction:   m.Direction,
			AmountMinor: m.AmountMinor,
			Reason:      m.Reason,
			CreatedAt:   m.CreatedAt.Format(rfc3339Millis),
		}
	}
	return out, nil
}

// SubmitClosingCount records the cashier's sayım (closing count) and moves
// the session to closing_control. Calling this again while already in
// closing_control is a deliberate "recount" path (ADR-DATA-008's
// closing_control self-loop), not an error — the frontend uses exactly this
// to let the cashier redo a stale count (see the frontend hook's staleness
// detection: expected_close is computed fresh on every read, never stored,
// so it can move between a count being submitted and the cashier hitting
// "kapat").
func (a *App) SubmitClosingCount(sessionID string, closingCountedAmount int64, denominations []DenominationCountDTO, notes string) (CashSessionDTO, error) {
	in := make([]apiclient.DenominationCount, len(denominations))
	for i, d := range denominations {
		in[i] = apiclient.DenominationCount{DenominationMinor: d.DenominationMinor, Count: d.Count}
	}
	session, err := a.api.SubmitClosingCount(a.ctx, sessionID, closingCountedAmount, in, notes)
	if err != nil {
		return CashSessionDTO{}, err
	}
	return toCashSessionDTO(session), nil
}

// CloseCashSession finalises the session. See CashSessionCloseResultDTO's
// doc comment for the cannot-close discriminated result — a refusal from the
// backend's cannotClose guard (e.g. pending fiscal submissions in the
// branch) is NOT surfaced through the error channel, so the frontend can
// render every reason instead of a flattened error string. A genuine
// transport/auth/not-found/invalid-transition failure still returns via the
// normal error channel, unchanged.
func (a *App) CloseCashSession(sessionID string) (CashSessionCloseResultDTO, error) {
	session, err := a.api.CloseCashSession(a.ctx, sessionID)
	var cannotClose *apiclient.CashSessionCannotCloseError
	if errors.As(err, &cannotClose) {
		reasons := make([]string, len(cannotClose.Reasons))
		copy(reasons, cannotClose.Reasons)
		return CashSessionCloseResultDTO{CannotClose: true, Reasons: reasons}, nil
	}
	if err != nil {
		return CashSessionCloseResultDTO{}, err
	}
	return CashSessionCloseResultDTO{Reasons: []string{}, Session: toCashSessionDTO(session)}, nil
}
