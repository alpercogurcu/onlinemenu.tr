package main

import (
	"fmt"

	"onlinemenu.tr/pos-desktop/internal/apiclient"
)

// This file adds the Wails bindings for ADR-DATA-008's "PIN akışının
// ayrıntıları": listing a cash session's participants, joining it (setting
// one's own PIN in the same trusted moment), and PIN-based cashier
// switching. Kept as a sibling to cash_session.go rather than growing it —
// same rationale that file's header gives for splitting from pos.go: this is
// a separate concern (who is acting as cashier, not the drawer's own
// lifecycle) with its own DTOs.
//
// Every method here only translates between apiclient's Go-shaped types and
// this file's DTOs — apiclient.Client remains the sole HTTP and token
// authority (see pos.go's file-level comment). In particular: a PIN value
// passed into JoinCashSession/SwitchCashier below is forwarded to
// apiclient and nowhere else — it is never logged, never written to a DTO
// field, never retained on *App, and this file holds no PIN of any kind in
// memory beyond the single call it is passed through.

// ParticipantDTO mirrors apiclient.Participant. FullName is the only
// person-identifying field — no email — matching the backend's field-level
// cut (see payment/http participantResponse's doc comment).
type ParticipantDTO struct {
	PersonID string `json:"person_id"`
	FullName string `json:"full_name"`
	HasPin   bool   `json:"has_pin"`
	Locked   bool   `json:"locked"`
}

func toParticipantDTO(p apiclient.Participant) ParticipantDTO {
	return ParticipantDTO{PersonID: p.PersonID, FullName: p.FullName, HasPin: p.HasPin, Locked: p.Locked}
}

// ListCashSessionParticipants returns every person who has joined sessionID
// — the source list for the POS switch picker (ADR-DATA-008 PIN akışı §3).
func (a *App) ListCashSessionParticipants(sessionID string) ([]ParticipantDTO, error) {
	participants, err := a.api.ListCashSessionParticipants(a.ctx, sessionID)
	if err != nil {
		return nil, err
	}
	out := make([]ParticipantDTO, len(participants))
	for i, p := range participants {
		out[i] = toParticipantDTO(p)
	}
	return out, nil
}

// JoinCashSession records that the CURRENTLY authenticated principal joined
// sessionID and, if pin is non-empty, sets THEIR OWN pin in the same call.
// There is no personID parameter by design: this always acts on whoever the
// current CTX token identifies, so a manager using this station cannot set
// a PIN "for" a cashier — the cashier must be the one authenticated (via
// the full Keycloak flow) when this is called (ADR-DATA-008 PIN akışı §2).
//
// A caller whose current identity is itself PIN-switched (session-scoped)
// is rejected by the backend (pub.ErrSessionScopedPrincipal, 403) — joining
// requires the "trusted moment" of a fresh Keycloak login, which a
// PIN-derived session is not.
func (a *App) JoinCashSession(sessionID, pin string) error {
	return a.api.JoinCashSession(a.ctx, sessionID, pin)
}

// SwitchCashier verifies pin against personID's stored PIN on sessionID
// and, on success, installs the resulting session-scoped CTX token as the
// acting identity for the rest of this shift, then returns the resulting
// Session exactly like SelectKeycloakContext does — same shape, so the
// frontend updates its header/session state the same way it already does
// after a context switch.
//
// On failure this returns apiclient.ErrPinVerificationFailed (wrapped) for
// EVERY negative outcome — wrong pin, pin never set, locked out, or
// personID not a participant of this session — all indistinguishable by
// design (ADR-DATA-008 PIN akışı). The frontend must render this as ONE
// generic message; see lib/errors.ts.
func (a *App) SwitchCashier(sessionID, personID, pin string) (SessionDTO, error) {
	token, err := a.api.SwitchCashier(a.ctx, sessionID, personID, pin)
	if err != nil {
		return SessionDTO{}, err
	}
	a.api.SetSessionToken(token)

	session, err := a.api.WhoAmI(a.ctx)
	if err != nil {
		return SessionDTO{}, fmt.Errorf("whoami after cashier switch: %w", err)
	}
	return SessionDTO{
		Authenticated: true,
		TenantID:      session.TenantID,
		BranchID:      session.BranchID,
		UserID:        session.UserID,
		FullName:      session.FullName,
		Email:         session.Email,
	}, nil
}
