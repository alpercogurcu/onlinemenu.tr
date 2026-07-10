package http

import (
	"crypto/subtle"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/payment/domain"
	"onlinemenu.tr/internal/modules/payment/fiscal/tokenx"
	"onlinemenu.tr/internal/modules/payment/repo"
	"onlinemenu.tr/internal/platform/db"
)

// maxWebhookBody caps the request body; real Token payloads are a few KB.
const maxWebhookBody = 1 << 20

// TokenXWebhookHandler ingests Token X Connect Cloud notifications.
//
// Token documents no webhook signature (ADR-FISCAL-002 open question #4), so
// authenticity rests on the unguessable secret path segment plus an optional
// TokenX IP allowlist at the edge. The sink is idempotent per submission, so a
// replayed or duplicated delivery is harmless.
type TokenXWebhookHandler struct {
	db     *db.Pool
	subs   *repo.FiscalSubmissionRepo
	sink   domain.FiscalResultSink
	secret string
	logger *zap.Logger
}

func NewTokenXWebhookHandler(pool *db.Pool, subs *repo.FiscalSubmissionRepo, sink domain.FiscalResultSink, secret string, logger *zap.Logger) *TokenXWebhookHandler {
	return &TokenXWebhookHandler{db: pool, subs: subs, sink: sink, secret: secret, logger: logger}
}

// RegisterRoutes mounts the webhook endpoint. The route only exists when a
// secret is configured; an empty secret would make the path guessable.
func (h *TokenXWebhookHandler) RegisterRoutes(r *chi.Mux) {
	if h.secret == "" {
		h.logger.Warn("payment: tokenx webhook secret not configured — webhook endpoint disabled")
		return
	}
	r.Post("/webhooks/fiscal/tokenx/{secret}", h.handle)
}

func (h *TokenXWebhookHandler) handle(w http.ResponseWriter, r *http.Request) {
	if subtle.ConstantTimeCompare([]byte(chi.URLParam(r, "secret")), []byte(h.secret)) != 1 {
		// 404, not 401: don't confirm the endpoint exists to a path scanner.
		http.NotFound(w, r)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBody))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}

	op, err := tokenx.WebhookOperation(body)
	if err != nil {
		h.logger.Warn("payment: undecodable tokenx webhook", zap.Error(err))
		http.Error(w, "bad payload", http.StatusBadRequest)
		return
	}

	switch op {
	case tokenx.OperationBasketCompleted:
		h.handleCompleted(w, r, body)
	case tokenx.OperationBasketLocked, tokenx.OperationBasketUnlocked:
		// Lock state is a POS-presence signal (another terminal is handling the
		// basket). No payment state changes; real-time POS fan-out comes later.
		if evt, err := tokenx.ParseLockEvent(body); err == nil {
			h.logger.Info("payment: tokenx basket lock state",
				zap.String("operation", op),
				zap.Stringer("submission_id", evt.SubmissionID),
				zap.String("terminal_id", evt.TerminalID),
			)
		}
		w.WriteHeader(http.StatusOK)
	default:
		// Unknown operations are acknowledged so the vendor does not retry
		// them forever; log for forward compatibility.
		h.logger.Warn("payment: unhandled tokenx webhook operation", zap.String("operation", op))
		w.WriteHeader(http.StatusOK)
	}
}

func (h *TokenXWebhookHandler) handleCompleted(w http.ResponseWriter, r *http.Request, body []byte) {
	res, err := tokenx.ParseWebhook(body)
	if err != nil {
		h.logger.Warn("payment: invalid tokenx completion webhook", zap.Error(err))
		http.Error(w, "bad payload", http.StatusBadRequest)
		return
	}

	// The webhook carries only the basketID; recover the owning tenant and
	// payment from our own submission record before touching any state.
	var routing repo.SubmissionRouting
	err = h.db.WithAllTenantsReadTx(r.Context(), func(tx pgx.Tx) error {
		var err error
		routing, err = h.subs.GetRouting(r.Context(), tx, res.SubmissionID)
		return err
	})
	if errors.Is(err, repo.ErrNotFound) {
		// Not ours (another environment sharing the credentials, or a stale
		// basket). Acknowledge so the vendor stops retrying.
		h.logger.Warn("payment: tokenx webhook for unknown submission",
			zap.Stringer("submission_id", res.SubmissionID))
		w.WriteHeader(http.StatusOK)
		return
	}
	if err != nil {
		h.logger.Error("payment: tokenx webhook routing lookup failed", zap.Error(err))
		http.Error(w, "routing lookup failed", http.StatusInternalServerError)
		return
	}

	res.TenantID = routing.TenantID
	res.BranchID = routing.BranchID
	res.PaymentID = routing.PaymentID
	res.DeviceType = tokenx.DeviceType

	if err := h.sink.OnFiscalResult(r.Context(), res); err != nil {
		// 5xx invites a vendor retry; OnFiscalResult is idempotent, so that is
		// the desired recovery path for transient DB failures.
		h.logger.Error("payment: tokenx webhook processing failed",
			zap.Stringer("submission_id", res.SubmissionID), zap.Error(err))
		http.Error(w, "processing failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}
