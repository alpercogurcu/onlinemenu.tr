package http

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/payment/service"
)

// checkSettledPayment is one collected payment on the wire: id and amount, in
// kuruş. No method, no timestamp, no fiscal receipt reference — this is a
// cashier-visible projection and those fields belong to payment.payment.read
// (ADR-AUTH-001 layer 4). Adding a field here widens counter staff's view
// without any permission change to signal it.
type checkSettledPayment struct {
	PaymentID   uuid.UUID `json:"payment_id"`
	AmountTotal int64     `json:"amount_total"`
}

type checkSettlementResponse struct {
	CheckID      uuid.UUID             `json:"check_id"`
	AsOf         string                `json:"as_of"`
	Completed    []checkSettledPayment `json:"completed"`
	PendingTotal int64                 `json:"pending_total"`
}

// checkSettlement answers GET /api/v1/payments/checks/{checkID}/settlement.
//
// The double-charge this fixes: a cashier cannot read payments (no
// payment.payment.read — that action opens tenant-wide reconciliation), so the
// POS inferred collected money from the fiscal poll's 5-minute
// recently_settled window. After the window the payment vanished from every
// view the cashier had and the check's balance jumped back to the full amount,
// inviting a second collection. This endpoint is windowless and check-scoped.
//
// It carries the existing payment.fiscal_status.read action rather than a new
// one: the scope that action already grants is "the money state of the sale in
// front of you", and this is the same scope keyed by check instead of branch.
//
// Another branch's check_id returns an empty settlement, not 403 — see
// service.CheckSettlementFor. Rejecting would confirm the id exists.
func (h *Handler) checkSettlement(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}

	checkID, err := uuid.Parse(chi.URLParam(r, "checkID"))
	if err != nil {
		http.Error(w, "invalid check id", http.StatusBadRequest)
		return
	}

	settlement, err := h.payments.CheckSettlementFor(r.Context(), p, checkID)
	if err != nil {
		h.logger.Error("payment: check settlement", zap.Error(err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	respondJSON(w, http.StatusOK, toCheckSettlementResponse(settlement))
}

func toCheckSettlementResponse(s service.CheckSettlement) checkSettlementResponse {
	// Completed is initialized empty, never nil: a nil slice marshals to null
	// and the contract requires []. A client that treats null as "unknown"
	// would fall back to the full balance — the double-charge again.
	resp := checkSettlementResponse{
		CheckID:      s.CheckID,
		AsOf:         s.AsOf.UTC().Format(time.RFC3339),
		Completed:    make([]checkSettledPayment, 0, len(s.Completed)),
		PendingTotal: s.PendingTotal,
	}
	for _, item := range s.Completed {
		resp.Completed = append(resp.Completed, checkSettledPayment{
			PaymentID:   item.PaymentID,
			AmountTotal: item.AmountTotal,
		})
	}
	return resp
}
