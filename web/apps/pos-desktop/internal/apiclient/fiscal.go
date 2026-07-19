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
