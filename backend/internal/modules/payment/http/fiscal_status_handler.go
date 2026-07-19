package http

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	pub "onlinemenu.tr/internal/modules/payment/public"
	"onlinemenu.tr/internal/modules/payment/service"
)

// fiscalPendingItem is one in-flight fiscal registration. AgeSeconds is
// computed server-side against the same clock as AsOf so stations with drifted
// local clocks still agree on how long a sale has been waiting.
type fiscalPendingItem struct {
	PaymentID    uuid.UUID  `json:"payment_id"`
	CheckID      *uuid.UUID `json:"check_id"`
	AmountTotal  int64      `json:"amount_total"`
	RegisteredAt string     `json:"registered_at"`
	AgeSeconds   int64      `json:"age_seconds"`
}

// fiscalSettledItem is one registration that reached a terminal state inside
// the recency window. FailureReason is the vendor/device error verbatim; the
// client owns any translation.
//
// AmountTotal has the same meaning as on the pending item (kuruş). It is
// repeated here so a station can keep subtracting a just-settled payment from
// the check's outstanding balance; the cashier cannot read the payment back
// any other way (listing payments is manager-only), so without it a completed
// remote payment would look uncollected and be charged twice.
type fiscalSettledItem struct {
	PaymentID     uuid.UUID  `json:"payment_id"`
	CheckID       *uuid.UUID `json:"check_id"`
	AmountTotal   int64      `json:"amount_total"`
	Status        string     `json:"status"`
	FailureReason *string    `json:"failure_reason"`
	SettledAt     string     `json:"settled_at"`
}

type fiscalStatusResponse struct {
	BranchID        uuid.UUID           `json:"branch_id"`
	AsOf            string              `json:"as_of"`
	Pending         []fiscalPendingItem `json:"pending"`
	RecentlySettled []fiscalSettledItem `json:"recently_settled"`
}

// fiscalPending answers GET /api/v1/payments/fiscal-pending?branch_id=<uuid>.
//
// A branch runs several POS stations against one backend. A sale registered on
// station 1 holds a pending fiscal submission that station 2 cannot see, so
// station 2 would either double-charge or optimistically treat the check as
// settled. This endpoint is that missing shared view, polled by every station.
//
// It carries the narrow payment.fiscal_status.read action rather than
// payment.payment.read: cashiers need it (they hold the check open) but must
// not gain the tenant-wide payment history that reconciliation reads expose.
//
// branch_id is client-supplied and therefore untrusted — the service validates
// it against the principal (ADR-AUTH-001 layer 3). RLS only isolates tenants,
// not branches within a chain.
func (h *Handler) fiscalPending(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}

	raw := r.URL.Query().Get("branch_id")
	if raw == "" {
		http.Error(w, "branch_id is required", http.StatusUnprocessableEntity)
		return
	}
	branchID, err := uuid.Parse(raw)
	if err != nil {
		http.Error(w, "invalid branch_id", http.StatusBadRequest)
		return
	}

	status, err := h.payments.FiscalBranchStatusFor(r.Context(), p, branchID)
	switch {
	case errors.Is(err, pub.ErrBranchForbidden):
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	case err != nil:
		h.logger.Error("payment: fiscal branch status", zap.Error(err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	respondJSON(w, http.StatusOK, toFiscalStatusResponse(status))
}

func toFiscalStatusResponse(s service.FiscalBranchStatus) fiscalStatusResponse {
	// Slices are initialized empty, never nil: a nil slice marshals to null and
	// the wire contract requires [].
	resp := fiscalStatusResponse{
		BranchID:        s.BranchID,
		AsOf:            s.AsOf.UTC().Format(time.RFC3339),
		Pending:         make([]fiscalPendingItem, 0, len(s.Pending)),
		RecentlySettled: make([]fiscalSettledItem, 0, len(s.RecentlySettled)),
	}
	for _, item := range s.Pending {
		resp.Pending = append(resp.Pending, fiscalPendingItem{
			PaymentID:    item.PaymentID,
			CheckID:      item.CheckID,
			AmountTotal:  item.AmountTotal,
			RegisteredAt: item.RegisteredAt.UTC().Format(time.RFC3339),
			AgeSeconds:   item.AgeSeconds,
		})
	}
	for _, item := range s.RecentlySettled {
		resp.RecentlySettled = append(resp.RecentlySettled, fiscalSettledItem{
			PaymentID:     item.PaymentID,
			CheckID:       item.CheckID,
			AmountTotal:   item.AmountTotal,
			Status:        item.Status,
			FailureReason: item.FailureReason,
			SettledAt:     item.SettledAt.UTC().Format(time.RFC3339),
		})
	}
	return resp
}
