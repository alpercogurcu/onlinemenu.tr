package apiclient

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// Branch-wide fiscal visibility (GET /api/v1/payments/fiscal-pending).
//
// This is the branch-scoped counterpart to GetPayment: where that endpoint
// observes ONE payment this station itself registered (and is gated on
// payment.payment.read, manager-only — see pos.go's GetPayment doc comment),
// this one lists every payment in the branch still awaiting its fiscal record
// PLUS the ones that settled recently, and is gated on
// "payment.fiscal_status.read" — an action the plain "cashier" role holds.
//
// That permission difference is the whole point: a cashier station can see
// that the table it is about to close has a receipt still being cut at the
// station next to it, which polling GetPayment per own-payment-id can never
// show.

// PendingFiscalItem is one payment in the branch still awaiting its fiscal
// record. AgeSeconds is server-computed (relative to the response's AsOf) so
// the client never has to trust its own clock against the server's.
//
// CheckID is a *uuid.UUID server-side and may serialize as null (a payment
// not bound to a check). Decoding it into a plain string is deliberate and
// verified: encoding/json leaves a string untouched on null, so a check-less
// payment arrives as "" — which matches no check id anywhere downstream and
// therefore contributes no dot and blocks no close, exactly as intended.
type PendingFiscalItem struct {
	PaymentID    string    `json:"payment_id"`
	CheckID      string    `json:"check_id"`
	AmountTotal  int64     `json:"amount_total"`
	RegisteredAt time.Time `json:"registered_at"`
	AgeSeconds   int64     `json:"age_seconds"`
}

// SettledFiscalItem is one payment that left the pending set recently.
// Status is one of completed | failed | voided. FailureReason is raw
// technical text from the fiscal device/adapter — it is diagnostic detail,
// never the message shown to a cashier as-is (see the frontend's
// describeError pattern).
type SettledFiscalItem struct {
	PaymentID string `json:"payment_id"`
	CheckID   string `json:"check_id"`
	Status    string `json:"status"`
	// AmountTotal is the registered amount in kuruş. Carried so a payment
	// COMPLETED at another station can be credited client-side instead of
	// snapping back into the collectable balance the moment it leaves the
	// pending list (see the frontend's remoteCompletedOnly).
	AmountTotal   int64     `json:"amount_total"`
	FailureReason string    `json:"failure_reason"`
	SettledAt     time.Time `json:"settled_at"`
}

// BranchPendingFiscal mirrors the endpoint's response envelope.
type BranchPendingFiscal struct {
	BranchID        string              `json:"branch_id"`
	AsOf            time.Time           `json:"as_of"`
	Pending         []PendingFiscalItem `json:"pending"`
	RecentlySettled []SettledFiscalItem `json:"recently_settled"`
}

// ListBranchPendingFiscal calls GET /api/v1/payments/fiscal-pending?branch_id=.
// branchID is required — a station with no branch context (chain-wide staff
// session) has no branch set to ask about, and this rejects before issuing a
// request rather than letting the backend 422.
//
// A 403 from this endpoint is a PERMANENT property of the session's role, not
// a transient fault: callers must stop asking rather than retry (see
// branchFiscalPoller in the main package).
func (c *Client) ListBranchPendingFiscal(ctx context.Context, branchID string) (BranchPendingFiscal, error) {
	if branchID == "" {
		return BranchPendingFiscal{}, fmt.Errorf("apiclient: list branch pending fiscal: branch_id is required")
	}
	var out BranchPendingFiscal
	path := "/api/v1/payments/fiscal-pending?" + url.Values{"branch_id": {branchID}}.Encode()
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return BranchPendingFiscal{}, fmt.Errorf("apiclient: list branch pending fiscal: %w", err)
	}
	return out, nil
}

// CheckSettledPayment is one collected payment on a check: id and amount in
// kuruş, nothing else. The narrowness is the backend's deliberate design (see
// payment/http/check_settlement_handler.go) — method, timestamp and fiscal
// receipt reference belong to payment.payment.read. Do not widen this struct
// to "match" Payment; the whole point is that a cashier may read it.
type CheckSettledPayment struct {
	PaymentID   string `json:"payment_id"`
	AmountTotal int64  `json:"amount_total"`
}

// CheckSettlement mirrors the endpoint's response envelope. Completed always
// arrives as [] (never null) per the backend's contract, so an empty slice
// genuinely means "nothing collected yet" rather than "unreadable".
type CheckSettlement struct {
	CheckID      string                `json:"check_id"`
	AsOf         time.Time             `json:"as_of"`
	Completed    []CheckSettledPayment `json:"completed"`
	PendingTotal int64                 `json:"pending_total"`
}

// GetCheckSettlement calls GET /api/v1/payments/checks/{checkID}/settlement —
// the money already collected on ONE check, gated on
// "payment.fiscal_status.read" (a cashier holds it) rather than
// "payment.payment.read" (manager-only, see ListCheckPayments).
//
// This is what closes the double-charge window ListBranchPendingFiscal could
// only narrow. A payment completed at another till leaves the branch pending
// list and then falls out of `recently_settled` after ~5 minutes; for a
// cashier session nothing else ever credited it, so the check's balance
// snapped back to collectable and the same money could be taken twice. This
// read is windowless and check-scoped, so the credit never expires.
//
// A check belonging to another branch returns an EMPTY settlement, not 403 —
// the backend refuses to confirm the id exists. Callers must therefore not
// read "empty" as "definitely nothing paid" for an arbitrary id; it is only
// meaningful for a check this station legitimately has open.
func (c *Client) GetCheckSettlement(ctx context.Context, checkID string) (CheckSettlement, error) {
	if checkID == "" {
		return CheckSettlement{}, fmt.Errorf("apiclient: get check settlement: check_id is required")
	}
	var out CheckSettlement
	path := "/api/v1/payments/checks/" + url.PathEscape(checkID) + "/settlement"
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return CheckSettlement{}, fmt.Errorf("apiclient: get check settlement: %w", err)
	}
	return out, nil
}
