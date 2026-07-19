package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/payment/domain"
	pub "onlinemenu.tr/internal/modules/payment/public"
	"onlinemenu.tr/internal/modules/payment/repo"
	"onlinemenu.tr/internal/platform/auth"
)

// ErrSubmissionNotExpirable reports a submission that is already in a terminal
// state (completed, failed, expired, voided). Only an unresolved registration
// can be expired; a resolved one has an answer from the device and must not be
// overwritten by an operator.
var ErrSubmissionNotExpirable = errors.New("payment/service: submission is not in an expirable state")

// manualExpireSource labels the audit payload so an expired submission's origin
// (operator vs. reconciler) is readable straight off result_payload.
const manualExpireSource = "manual_operator_expire"

// ExpireSubmission is the operator's escape hatch for a fiscal registration
// whose result never arrived: the webhook was lost, the device was left off the
// sales screen, or the basket was abandoned. Without it the payment stays
// 'pending' forever and the check stays locked in fiscal_pending, recoverable
// only by editing the database by hand (docs/runbook-fiscal-stranded.md).
//
// It is deliberately MANUAL. The reconciler's AutoExpire stays off because a
// missing result does not prove the device failed to print (ADR-FISCAL-002):
// only a human who has looked at the device can make that call. This function
// is that human's recorded decision, not a clock's.
//
// The outcome is identical to the reconciler's expire path — the same
// domain.FiscalSubmissionExpired result through the same sink — so the
// transition stays idempotent and the payment is failed exactly once.
func (s *PaymentService) ExpireSubmission(ctx context.Context, p auth.Principal, submissionID uuid.UUID, reason string) error {
	if submissionID == uuid.Nil {
		return pub.ErrNotFound
	}

	sub, err := s.getSubmission(ctx, p.TenantID, submissionID)
	if err != nil {
		return err
	}
	// Pre-check: reject a terminal row before writing anything, so the common
	// "operator is too late, the webhook already landed" case answers 409 with
	// the payment untouched.
	if !isExpirable(sub.Status) {
		return fmt.Errorf("%w: %s", ErrSubmissionNotExpirable, sub.Status)
	}

	audit, err := json.Marshal(map[string]any{
		"source":         manualExpireSource,
		"expired_by":     p.PersonID,
		"expired_at":     time.Now().UTC().Format(time.RFC3339),
		"operator_note":  reason,
		"status_at_call": string(sub.Status),
	})
	if err != nil {
		return fmt.Errorf("payment/service: expire submission: marshal audit: %w", err)
	}

	// Audit trail, no new table: who/when lands in result_payload (immutable
	// once the row goes terminal) and in the log line below. The submission row
	// itself is the record of the intervention.
	s.logger.Warn("payment: fiscal submission manually expired by operator",
		zap.Stringer("submission_id", sub.ID),
		zap.Stringer("payment_id", sub.PaymentID),
		zap.Stringer("tenant_id", sub.TenantID),
		zap.Stringer("branch_id", sub.BranchID),
		zap.Stringer("operator_person_id", p.PersonID),
		zap.String("terminal_serial", sub.TerminalSerial),
		zap.String("status_at_call", string(sub.Status)),
		zap.String("operator_note", reason),
	)

	if err := s.OnFiscalResult(ctx, domain.FiscalResult{
		SubmissionID:  sub.ID,
		TenantID:      sub.TenantID,
		BranchID:      sub.BranchID,
		PaymentID:     sub.PaymentID,
		Status:        domain.FiscalSubmissionExpired,
		DeviceType:    sub.AdapterType,
		FailureReason: manualExpireFailureReason(p.PersonID, reason),
		Raw:           audit,
	}); err != nil {
		return fmt.Errorf("payment/service: expire submission: %w", err)
	}

	// The sink swallows a lost transition by design (duplicate webhook
	// deliveries must be no-ops), so success there does not prove WE expired
	// the row: a genuine result may have won the race between the pre-check and
	// the write. Re-read and let the operator see 409 instead of a 200 that
	// silently did nothing.
	after, err := s.getSubmission(ctx, p.TenantID, submissionID)
	if err != nil {
		return err
	}
	if after.Status != domain.FiscalSubmissionExpired {
		s.logger.Warn("payment: manual expire lost the race to a real fiscal result",
			zap.Stringer("submission_id", sub.ID),
			zap.String("final_status", string(after.Status)),
		)
		return fmt.Errorf("%w: %s", ErrSubmissionNotExpirable, after.Status)
	}
	return nil
}

// warnIfLostAfterExpire raises the one dropped fiscal result that is NOT
// routine: a genuine 'completed' or 'voided' outcome arriving for a submission
// an operator already expired by hand.
//
// Every other lost transition is expected — a retried webhook, a reconciler
// sweep racing the worker — and stays at Debug. This one is different: the
// device may well have printed a legal receipt that this server will now never
// record, so the sale exists on the ÖKC's Z report and nowhere else. That is an
// accounting discrepancy only a human can close (docs/runbook-fiscal-stranded.md
// §5), and it must not be discoverable only by turning on Debug logging.
//
// Best effort by design: a failed status read must not abort the caller's
// transaction path, which is a legitimate no-op either way.
func (s *PaymentService) warnIfLostAfterExpire(ctx context.Context, tx pgx.Tx, res domain.FiscalResult) {
	if res.Status != domain.FiscalSubmissionCompleted && res.Status != domain.FiscalSubmissionVoided {
		return
	}
	current, err := s.submissionRepo.GetByID(ctx, tx, res.SubmissionID)
	if err != nil || current.Status != domain.FiscalSubmissionExpired {
		return
	}
	s.logger.Warn("payment: fiscal result arrived after manual expire — receipt may exist on device, reconcile manually",
		zap.Stringer("submission_id", res.SubmissionID),
		zap.Stringer("payment_id", res.PaymentID),
		zap.Stringer("tenant_id", res.TenantID),
		zap.Stringer("branch_id", res.BranchID),
		zap.String("submission_status", string(current.Status)),
		zap.String("dropped_result_status", string(res.Status)),
		zap.String("receipt_no", res.ReceiptNo),
		zap.String("vendor_ref", res.VendorRef),
	)
}

func (s *PaymentService) getSubmission(ctx context.Context, tenantID, submissionID uuid.UUID) (repo.FiscalSubmission, error) {
	var sub repo.FiscalSubmission
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		sub, err = s.submissionRepo.GetByID(ctx, tx, submissionID)
		return err
	})
	if errors.Is(err, repo.ErrNotFound) {
		return repo.FiscalSubmission{}, pub.ErrNotFound
	}
	if err != nil {
		return repo.FiscalSubmission{}, fmt.Errorf("payment/service: expire submission: load: %w", err)
	}
	return sub, nil
}

// isExpirable mirrors MarkResult's source-state gate for an expired target:
// only an unresolved registration may be expired.
func isExpirable(status domain.FiscalSubmissionStatus) bool {
	return status == domain.FiscalSubmissionPending || status == domain.FiscalSubmissionSubmitted
}

func manualExpireFailureReason(operator uuid.UUID, note string) string {
	reason := fmt.Sprintf("manually expired by operator %s; no fiscal result received", operator)
	if note != "" {
		reason += ": " + note
	}
	return reason
}
