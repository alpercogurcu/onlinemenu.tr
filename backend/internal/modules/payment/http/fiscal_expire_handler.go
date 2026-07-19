package http

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"go.uber.org/zap"

	pub "onlinemenu.tr/internal/modules/payment/public"
	"onlinemenu.tr/internal/modules/payment/service"
)

// codeSubmissionNotExpirable is returned when the submission already reached a
// terminal state — most often because the real fiscal result arrived while the
// operator was deciding.
const codeSubmissionNotExpirable = "submission_not_expirable"

// expireSubmissionRequest carries the operator's justification. The body is
// optional; the note is not validated because its only consumer is a human
// reading the audit trail.
type expireSubmissionRequest struct {
	Reason string `json:"reason"`
}

// maxExpireReasonLen bounds what an operator can push into result_payload.
const maxExpireReasonLen = 500

// expireSubmission answers
// POST /api/v1/payments/fiscal/submissions/{id}/expire.
//
// A fiscal registration whose result never arrives leaves its payment 'pending'
// and its check locked in fiscal_pending forever: the reconciler's AutoExpire
// is deliberately off (ADR-FISCAL-002 — a clock cannot tell whether the device
// printed) and VoidSale refuses a non-completed submission (it would race the
// worker). Until this endpoint the only exit was hand-editing the database.
//
// It carries payment.fiscal_terminal.manage, the existing manager-only fiscal
// administration action, rather than a new one: deciding that a device never
// registered a sale is the same back-office judgement as pairing and
// configuring that device, and it must never be a counter-staff action —
// failing a payment reopens the check's balance for re-collection.
//
// Like the other fiscal admin writes this carries no Idempotency-Key
// (ADR-SEC-003 scopes it to money-moving client writes): the endpoint is
// idempotent by construction because the terminal-state gate makes a replay a
// 409, so the payment can be failed at most once.
func (h *Handler) expireSubmission(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	id, ok := requireURLID(w, r)
	if !ok {
		return
	}

	var req expireSubmissionRequest
	// An absent body is a valid call; only a malformed one is rejected.
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	reason := strings.TrimSpace(req.Reason)
	if len(reason) > maxExpireReasonLen {
		http.Error(w, "reason is too long", http.StatusUnprocessableEntity)
		return
	}

	err := h.payments.ExpireSubmission(r.Context(), p, id, reason)
	switch {
	case errors.Is(err, pub.ErrNotFound):
		http.Error(w, "not found", http.StatusNotFound)
	case errors.Is(err, service.ErrSubmissionNotExpirable):
		respondErrorJSON(w, http.StatusConflict, codeSubmissionNotExpirable,
			"submission already reached a terminal state")
	case err != nil:
		h.logger.Error("payment: expire fiscal submission",
			zap.Stringer("submission_id", id), zap.Error(err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// respondErrorJSON writes the {error, code} body the POS client already parses
// for 409s (mirrors pos/http respondError). Only conflicts use it here; the
// module's other error bodies stay plain text until they are migrated as a
// whole.
func respondErrorJSON(w http.ResponseWriter, status int, code, msg string) {
	respondJSON(w, status, map[string]string{"error": msg, "code": code})
}
