package service_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"onlinemenu.tr/internal/modules/payment/domain"
	pub "onlinemenu.tr/internal/modules/payment/public"
	"onlinemenu.tr/internal/modules/payment/repo"
	"onlinemenu.tr/internal/modules/payment/service"
	"onlinemenu.tr/internal/platform/auth"
)

func expireOperator() auth.Principal {
	return auth.Principal{
		PersonID: uuid.MustParse("11111111-2222-3333-4444-555555555555"),
		Ctx:      auth.ContextStaff,
		TenantID: tenantA,
		BranchID: branchA,
	}
}

// submissionResultPayload reads the audit blob written alongside the terminal
// transition — the "who expired this" record, since there is no audit table.
func submissionResultPayload(t *testing.T, tenantID, submissionID uuid.UUID) map[string]any {
	t.Helper()
	var raw []byte
	err := sharedPool.WithTenantReadTx(context.Background(), tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `
			SELECT result_payload FROM fiscal_submissions WHERE id = $1
		`, submissionID).Scan(&raw)
	})
	require.NoError(t, err)
	if len(raw) == 0 {
		return nil
	}
	var out map[string]any
	require.NoError(t, json.Unmarshal(raw, &out))
	return out
}

// TestExpireSubmission_StrandedSubmitted_FailsPaymentAndRecordsOperator is the
// whole point of the endpoint: a submission whose webhook never arrived is
// driven to the same terminal state the reconciler would produce, releasing the
// check from its permanent fiscal_pending lock.
func TestExpireSubmission_StrandedSubmitted_FailsPaymentAndRecordsOperator(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	svc := newPaymentService()
	op := expireOperator()

	payment := registerPending(t, svc, 4200)
	parkAsSubmitted(t, tenantA, payment.ID, time.Now().UTC().Add(-2*time.Hour))
	subID := submissionID(t, tenantA, payment.ID)

	require.NoError(t, svc.ExpireSubmission(ctx, op, subID, "cihaz kapali, fis basilmadi"))

	settled := fetchPayment(t, svc, payment.ID)
	assert.Equal(t, domain.PaymentStatusFailed, settled.Status)
	assert.Nil(t, settled.FiscalReceiptID)
	assert.Equal(t, string(domain.FiscalSubmissionExpired), submissionStatus(t, tenantA, payment.ID))
	// A failed registration must never look like a sale downstream.
	assert.Zero(t, countOutboxEvents(t, tenantA, payment.ID, "payment.completed"))

	audit := submissionResultPayload(t, tenantA, subID)
	require.NotNil(t, audit, "expire must leave an audit trail in result_payload")
	assert.Equal(t, "manual_operator_expire", audit["source"])
	assert.Equal(t, op.PersonID.String(), audit["expired_by"])
	assert.Equal(t, "cihaz kapali, fis basilmadi", audit["operator_note"])
	assert.Equal(t, "submitted", audit["status_at_call"])
}

// TestExpireSubmission_PendingIsExpirable covers the not-yet-submitted row: a
// basket the worker never managed to hand to the device strands the check just
// as effectively.
func TestExpireSubmission_PendingIsExpirable(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	svc := newPaymentService()

	payment := registerPending(t, svc, 1500)
	subID := submissionID(t, tenantA, payment.ID)

	require.NoError(t, svc.ExpireSubmission(ctx, expireOperator(), subID, ""))

	assert.Equal(t, string(domain.FiscalSubmissionExpired), submissionStatus(t, tenantA, payment.ID))
	assert.Equal(t, domain.PaymentStatusFailed, fetchPayment(t, svc, payment.ID).Status)
}

// TestExpireSubmission_CompletedIsRejected is the race the operator loses: the
// real result landed first. The completed sale — a printed receipt — must
// survive untouched and the operator must be told, not silently answered OK.
func TestExpireSubmission_CompletedIsRejected(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	svc := newPaymentService()

	payment := registerPending(t, svc, 2600)
	drainFiscal(t, svc)
	require.Equal(t, domain.PaymentStatusCompleted, fetchPayment(t, svc, payment.ID).Status)
	subID := submissionID(t, tenantA, payment.ID)

	err := svc.ExpireSubmission(ctx, expireOperator(), subID, "too late")
	require.ErrorIs(t, err, service.ErrSubmissionNotExpirable)

	still := fetchPayment(t, svc, payment.ID)
	assert.Equal(t, domain.PaymentStatusCompleted, still.Status)
	assert.NotNil(t, still.FiscalReceiptID)
	assert.Equal(t, string(domain.FiscalSubmissionCompleted), submissionStatus(t, tenantA, payment.ID))
}

// TestExpireSubmission_ReplayIsRejected: the second call finds the row already
// expired. This is what makes the endpoint safe without an Idempotency-Key —
// the payment can be failed at most once.
func TestExpireSubmission_ReplayIsRejected(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	svc := newPaymentService()

	payment := registerPending(t, svc, 900)
	parkAsSubmitted(t, tenantA, payment.ID, time.Now().UTC().Add(-time.Hour))
	subID := submissionID(t, tenantA, payment.ID)

	require.NoError(t, svc.ExpireSubmission(ctx, expireOperator(), subID, ""))
	err := svc.ExpireSubmission(ctx, expireOperator(), subID, "")
	require.ErrorIs(t, err, service.ErrSubmissionNotExpirable)

	assert.Equal(t, domain.PaymentStatusFailed, fetchPayment(t, svc, payment.ID).Status)
}

// TestExpireSubmission_LateWebhookAfterExpire pins the other race direction:
// once expired, a genuine 'completed' result arriving late is dropped by
// MarkResult's source-state gate. The payment stays failed and NO receipt is
// written — so a device that really did print leaves an accounting discrepancy
// only a human can resolve (see docs/runbook-fiscal-stranded.md).
func TestExpireSubmission_LateWebhookAfterExpire(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	svc := newPaymentService()

	payment := registerPending(t, svc, 3300)
	parkAsSubmitted(t, tenantA, payment.ID, time.Now().UTC().Add(-time.Hour))
	subID := submissionID(t, tenantA, payment.ID)
	require.NoError(t, svc.ExpireSubmission(ctx, expireOperator(), subID, ""))

	// The webhook the operator gave up waiting for, arriving after the fact.
	require.NoError(t, svc.OnFiscalResult(ctx, domain.FiscalResult{
		SubmissionID: subID,
		TenantID:     tenantA,
		BranchID:     branchA,
		PaymentID:    payment.ID,
		Status:       domain.FiscalSubmissionCompleted,
		DeviceType:   "mock",
		ReceiptNo:    "0001",
	}))

	after := fetchPayment(t, svc, payment.ID)
	assert.Equal(t, domain.PaymentStatusFailed, after.Status)
	assert.Nil(t, after.FiscalReceiptID)
	assert.Equal(t, string(domain.FiscalSubmissionExpired), submissionStatus(t, tenantA, payment.ID))
	assert.Zero(t, countOutboxEvents(t, tenantA, payment.ID, "payment.completed"))
}

// newObservedPaymentService returns a service whose logs are capturable, so a
// test can assert the SEVERITY of a dropped fiscal result and not merely its
// absence of side effects.
func newObservedPaymentService() (*service.PaymentService, *observer.ObservedLogs) {
	core, logs := observer.New(zapcore.DebugLevel)
	return service.NewPaymentService(service.Params{
		DB:             sharedPool,
		PaymentRepo:    repo.NewPaymentRepo(),
		SubmissionRepo: repo.NewFiscalSubmissionRepo(),
		StatusRepo:     repo.NewFiscalStatusRepo(),
		Fiscal:         domain.MockFiscalAdapter{},
		Logger:         zap.New(core),
	}), logs
}

// TestExpireSubmission_LateWebhookAfterExpire_LogsWarn pins the log SEVERITY of
// the one dropped result that is not routine. A completed registration landing
// after a manual expire means the device may hold a printed legal receipt this
// server will never record — an accounting discrepancy. At Debug (where every
// other duplicate lives) nobody would ever see it.
func TestExpireSubmission_LateWebhookAfterExpire_LogsWarn(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	svc, logs := newObservedPaymentService()

	payment := registerPending(t, svc, 5400)
	parkAsSubmitted(t, tenantA, payment.ID, time.Now().UTC().Add(-time.Hour))
	subID := submissionID(t, tenantA, payment.ID)
	require.NoError(t, svc.ExpireSubmission(ctx, expireOperator(), subID, ""))

	require.NoError(t, svc.OnFiscalResult(ctx, domain.FiscalResult{
		SubmissionID: subID,
		TenantID:     tenantA,
		BranchID:     branchA,
		PaymentID:    payment.ID,
		Status:       domain.FiscalSubmissionCompleted,
		DeviceType:   "mock",
		ReceiptNo:    "0007",
	}))

	warns := logs.FilterLevelExact(zapcore.WarnLevel).
		FilterMessageSnippet("fiscal result arrived after manual expire").All()
	require.Len(t, warns, 1, "a receipt that may exist on the device must not vanish at Debug level")

	fields := warns[0].ContextMap()
	assert.Equal(t, subID.String(), fields["submission_id"])
	assert.Equal(t, payment.ID.String(), fields["payment_id"])
	assert.Equal(t, "expired", fields["submission_status"])
	assert.Equal(t, "completed", fields["dropped_result_status"])
	assert.Equal(t, "0007", fields["receipt_no"])
}

// TestOnFiscalResult_RoutineDuplicateStaysDebug is the other half of the
// contract: a replayed webhook against an already-completed row is ordinary and
// must NOT raise the alarm, or the warning becomes noise nobody reads.
func TestOnFiscalResult_RoutineDuplicateStaysDebug(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	svc, logs := newObservedPaymentService()

	payment := registerPending(t, svc, 1200)
	drainFiscal(t, svc)
	subID := submissionID(t, tenantA, payment.ID)

	require.NoError(t, svc.OnFiscalResult(ctx, domain.FiscalResult{
		SubmissionID: subID,
		TenantID:     tenantA,
		BranchID:     branchA,
		PaymentID:    payment.ID,
		Status:       domain.FiscalSubmissionCompleted,
		DeviceType:   "mock",
		ReceiptNo:    "0009",
	}))

	assert.Zero(t, logs.FilterMessageSnippet("fiscal result arrived after manual expire").Len(),
		"a routine duplicate delivery must stay at Debug")
	assert.Equal(t, 1, logs.FilterMessageSnippet("duplicate fiscal result ignored").Len())
}

// TestExpireSubmission_UnknownIDIsNotFound exercises a random id only. A foreign
// tenant's id would behave the same because the read runs under RLS, but that
// path is not seeded here — the claim rests on RLS, not on this test.
func TestExpireSubmission_UnknownIDIsNotFound(t *testing.T) {
	requireDB(t)
	svc := newPaymentService()

	err := svc.ExpireSubmission(context.Background(), expireOperator(), uuid.New(), "")
	require.ErrorIs(t, err, pub.ErrNotFound)
}
