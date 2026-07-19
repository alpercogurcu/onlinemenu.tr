package main

import (
	"context"
	"errors"
	"time"

	"onlinemenu.tr/pos-desktop/internal/apiclient"
)

// branchFiscalPendingEvent is the Wails event topic the frontend subscribes
// to for branch-wide pending-fiscal snapshots (see the frontend's
// useBranchFiscalPending hook). Mirrors hardwarePrinterEvent's role for
// printer connectivity.
const branchFiscalPendingEvent = "fiscal:branch-pending"

const (
	// fiscalPollActiveInterval is the cadence while the branch actually has
	// payments awaiting a fiscal record — a cashier standing at the till
	// waiting to close an adisyon needs the dot to clear promptly.
	fiscalPollActiveInterval = 3 * time.Second

	// fiscalPollIdleInterval is the backed-off cadence after
	// fiscalPollEmptyRounds consecutive empty snapshots. A branch with
	// nothing pending is the overwhelmingly common steady state; polling it
	// every 3s for a whole shift is pure load with no observable difference.
	fiscalPollIdleInterval = 15 * time.Second

	// fiscalPollEmptyRounds is how many consecutive empty snapshots back the
	// cadence off. The first non-empty snapshot restores it immediately.
	fiscalPollEmptyRounds = 3
)

// BranchPendingFiscalDTO is the JSON shape emitted to the frontend on every
// successful poll — including EMPTY ones. Emitting empties is load-bearing:
// it is the only way the frontend can CLEAR a dot for a payment that settled
// at another station. A poller that only spoke up when something was pending
// would leave stale amber dots on the masa planı forever.
type BranchPendingFiscalDTO struct {
	BranchID        string                 `json:"branch_id"`
	AsOf            string                 `json:"as_of"`
	Pending         []PendingFiscalItemDTO `json:"pending"`
	RecentlySettled []SettledFiscalItemDTO `json:"recently_settled"`
}

// PendingFiscalItemDTO mirrors apiclient.PendingFiscalItem.
type PendingFiscalItemDTO struct {
	PaymentID    string `json:"payment_id"`
	CheckID      string `json:"check_id"`
	AmountTotal  int64  `json:"amount_total"`
	RegisteredAt string `json:"registered_at"`
	AgeSeconds   int64  `json:"age_seconds"`
}

// SettledFiscalItemDTO mirrors apiclient.SettledFiscalItem. FailureReason is
// carried through verbatim as DIAGNOSTIC detail — the frontend renders a
// Turkish message and keeps this raw text as the drill-down, never as the
// primary text (see the frontend's describeError pattern).
type SettledFiscalItemDTO struct {
	PaymentID string `json:"payment_id"`
	CheckID   string `json:"check_id"`
	Status    string `json:"status"`
	// AmountTotal is in kuruş. NOT omitempty: the frontend subtracts this from
	// what the cashier may still collect, so a legitimate 0 must arrive as 0
	// rather than as an absent key it has to guess about.
	AmountTotal   int64  `json:"amount_total"`
	FailureReason string `json:"failure_reason,omitempty"`
	SettledAt     string `json:"settled_at"`
}

// branchFiscalPoller polls the branch-wide pending-fiscal endpoint on a
// single context-cancellable goroutine and pushes each snapshot to the
// frontend.
//
// Shaped like hardware.Printer (Start / Wait, injected collaborators) rather
// than reaching into App directly, so it can be unit-tested against a fake
// fetch with no Wails runtime context in sight — runtime.EventsEmit panics
// outside a real Wails lifecycle (see app.go's openURL doc comment for the
// same seam).
//
// IN-FLIGHT GUARD: there is none, and none is needed — the run loop is
// strictly sequential (fetch, THEN arm the next timer), so two ticks can
// never overlap by construction. This is stronger than a boolean guard,
// which would still let a slow fetch queue a tick that fires the instant it
// returns.
type branchFiscalPoller struct {
	branchID string

	// fetch reads one snapshot. Receives the poller's own cancellable
	// context so Stop aborts an in-flight HTTP request immediately rather
	// than letting shutdown block on the client's 15s timeout.
	fetch func(ctx context.Context, branchID string) (apiclient.BranchPendingFiscal, error)

	// emit publishes a snapshot to the frontend.
	emit func(BranchPendingFiscalDTO)

	// logWarn records a transient fetch failure, and the single terminal 403
	// that stops the loop for good (see run).
	logWarn func(msg string)

	cancel context.CancelFunc
	done   chan struct{}
}

// Start launches the poll loop against a child of ctx. Safe to call exactly
// once per poller instance; App.syncBranchFiscalPoller enforces that by
// constructing a fresh poller per branch.
func (p *branchFiscalPoller) Start(ctx context.Context) {
	pollCtx, cancel := context.WithCancel(ctx)
	p.cancel = cancel
	p.done = make(chan struct{})
	go p.run(pollCtx)
}

// Stop cancels the loop and waits for the goroutine to fully exit — the
// station must never leave a background poller running past logout, a branch
// switch, or process shutdown (the same rule app.go's shutdown states for
// the hardware poller).
//
// Safe on a never-started or already-stopped poller.
func (p *branchFiscalPoller) Stop() {
	if p.cancel == nil {
		return
	}
	p.cancel()
	<-p.done
}

// run is the poll loop. Every exit path — cancellation and the terminal 403 —
// closes done, so a Stop that races a self-exit (the 403 case) never hangs.
func (p *branchFiscalPoller) run(ctx context.Context) {
	defer close(p.done)

	interval := fiscalPollActiveInterval
	emptyRounds := 0

	for {
		snapshot, err := p.fetch(ctx, p.branchID)
		switch {
		case err == nil:
			p.emit(toBranchPendingFiscalDTO(snapshot))
			if len(snapshot.Pending) == 0 && len(snapshot.RecentlySettled) == 0 {
				emptyRounds++
				if emptyRounds >= fiscalPollEmptyRounds {
					interval = fiscalPollIdleInterval
				}
			} else {
				emptyRounds = 0
				interval = fiscalPollActiveInterval
			}

		case ctx.Err() != nil:
			// Cancelled mid-request — an expected shutdown path, not a fault.
			return

		case isForbidden(err):
			// 403 is a property of this session's ROLE, not a transient
			// fault: this station will never be granted
			// payment.fiscal_status.read, so retrying every 3s for the rest
			// of the shift would be a guaranteed-useless loop against the
			// authz engine. Log ONCE and stop for good; a later login (which
			// builds a fresh poller) may have a different role.
			p.logWarn("branch fiscal poll forbidden (payment.fiscal_status.read) — şube geneli mali kayıt görünürlüğü bu oturumda kapalı: " + err.Error())
			return

		default:
			// Network blip, 5xx, decode failure: transient. Keep polling —
			// but do not let a failing endpoint hold the cadence at 3s
			// forever, so failures count toward the same backoff as empties.
			p.logWarn("branch fiscal poll failed: " + err.Error())
			emptyRounds++
			if emptyRounds >= fiscalPollEmptyRounds {
				interval = fiscalPollIdleInterval
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// isForbidden reports whether err is (or wraps) a 403 from the backend.
// Uses the typed *apiclient.APIError rather than string matching — the
// frontend's string matching (see errors.ts) is a Wails-IPC constraint that
// does not apply on this side of the boundary.
func isForbidden(err error) bool {
	var apiErr *apiclient.APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == 403
}

func toBranchPendingFiscalDTO(s apiclient.BranchPendingFiscal) BranchPendingFiscalDTO {
	pending := make([]PendingFiscalItemDTO, len(s.Pending))
	for i, item := range s.Pending {
		pending[i] = PendingFiscalItemDTO{
			PaymentID:    item.PaymentID,
			CheckID:      item.CheckID,
			AmountTotal:  item.AmountTotal,
			RegisteredAt: item.RegisteredAt.Format(rfc3339Millis),
			AgeSeconds:   item.AgeSeconds,
		}
	}
	settled := make([]SettledFiscalItemDTO, len(s.RecentlySettled))
	for i, item := range s.RecentlySettled {
		settled[i] = SettledFiscalItemDTO{
			PaymentID:     item.PaymentID,
			CheckID:       item.CheckID,
			Status:        item.Status,
			AmountTotal:   item.AmountTotal,
			FailureReason: item.FailureReason,
			SettledAt:     item.SettledAt.Format(rfc3339Millis),
		}
	}
	return BranchPendingFiscalDTO{
		BranchID:        s.BranchID,
		AsOf:            s.AsOf.Format(rfc3339Millis),
		Pending:         pending,
		RecentlySettled: settled,
	}
}
