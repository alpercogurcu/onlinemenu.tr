package repo_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/modules/payment/domain"
	"onlinemenu.tr/internal/modules/payment/repo"
)

// branchB is a second branch of tenantA: the multi-branch chain case the
// branch-scoped poll must not leak across (RLS only isolates tenants).
var branchB = uuid.MustParse("cccccccc-0000-0000-0000-000000000002")

// seedSubmission creates a payment plus its fiscal submission and drives the
// submission to the requested state, returning the payment id. status ==
// "pending"/"submitted" leaves it in flight; terminal statuses also transition
// the payment so p.status matches what the poll reports.
func seedSubmission(
	t *testing.T,
	tenantID, branchID uuid.UUID,
	status domain.FiscalSubmissionStatus,
	lastError string,
	completedAt time.Time,
) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	payments := repo.NewPaymentRepo()
	subs := repo.NewFiscalSubmissionRepo()
	checkID := uuid.New()

	var paymentID uuid.UUID
	err := sharedPool.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		created, err := payments.Create(ctx, tx, domain.Payment{
			TenantID:       tenantID,
			BranchID:       branchID,
			CheckID:        &checkID,
			IdempotencyKey: uuid.NewString(),
			Method:         domain.PaymentMethodCash,
			AmountTotal:    12500,
			Currency:       "TRY",
		})
		if err != nil {
			return err
		}
		paymentID = created.ID

		payload, err := json.Marshal(map[string]any{"total": 12500})
		if err != nil {
			return err
		}
		if err := subs.Insert(ctx, tx, repo.FiscalSubmission{
			ID:          uuid.New(),
			TenantID:    tenantID,
			BranchID:    branchID,
			PaymentID:   paymentID,
			AdapterType: "mock",
			Status:      domain.FiscalSubmissionPending,
			SalePayload: payload,
		}); err != nil {
			return err
		}
		return nil
	})
	require.NoError(t, err)

	if status == domain.FiscalSubmissionPending {
		return paymentID
	}

	err = sharedPool.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		sub, err := subs.GetByPaymentID(ctx, tx, paymentID)
		if err != nil {
			return err
		}
		if status == domain.FiscalSubmissionSubmitted {
			_, err = subs.MarkSubmitted(ctx, tx, sub.ID)
			return err
		}
		if _, err := subs.MarkResult(ctx, tx, sub.ID, status, nil, lastError, completedAt); err != nil {
			return err
		}
		switch status {
		case domain.FiscalSubmissionCompleted:
			receiptID, err := payments.InsertFiscalReceipt(ctx, tx, domain.FiscalReceipt{
				TenantID: tenantID, PaymentID: paymentID, ReceiptNumber: uuid.NewString(),
			})
			if err != nil {
				return err
			}
			return payments.Complete(ctx, tx, paymentID, receiptID)
		case domain.FiscalSubmissionVoided:
			return payments.Void(ctx, tx, paymentID)
		default:
			return payments.Fail(ctx, tx, paymentID)
		}
	})
	require.NoError(t, err)
	return paymentID
}

func listPending(t *testing.T, tenantID, branchID uuid.UUID) []repo.FiscalPendingRow {
	t.Helper()
	var out []repo.FiscalPendingRow
	err := sharedPool.WithTenantReadTx(context.Background(), tenantID, func(tx pgx.Tx) error {
		var err error
		out, err = repo.NewFiscalStatusRepo().ListPendingByBranch(context.Background(), tx, tenantID, branchID)
		return err
	})
	require.NoError(t, err)
	return out
}

// listSettled takes the window as a duration for readability and converts it
// to the absolute cutoff the repo expects, using the Go clock — the same
// single-clock rule the production caller follows.
func listSettled(t *testing.T, tenantID, branchID uuid.UUID, window time.Duration) []repo.FiscalSettledRow {
	t.Helper()
	cutoff := time.Now().UTC().Add(-window)
	var out []repo.FiscalSettledRow
	err := sharedPool.WithTenantReadTx(context.Background(), tenantID, func(tx pgx.Tx) error {
		var err error
		out, err = repo.NewFiscalStatusRepo().ListRecentlySettledByBranch(context.Background(), tx, tenantID, branchID, cutoff)
		return err
	})
	require.NoError(t, err)
	return out
}

func pendingFor(rows []repo.FiscalPendingRow, id uuid.UUID) []repo.FiscalPendingRow {
	var out []repo.FiscalPendingRow
	for _, r := range rows {
		if r.PaymentID == id {
			out = append(out, r)
		}
	}
	return out
}

func containsPayment(rows []repo.FiscalPendingRow, id uuid.UUID) bool {
	return len(pendingFor(rows, id)) > 0
}

func settledFor(rows []repo.FiscalSettledRow, id uuid.UUID) []repo.FiscalSettledRow {
	var out []repo.FiscalSettledRow
	for _, r := range rows {
		if r.PaymentID == id {
			out = append(out, r)
		}
	}
	return out
}

// TestFiscalStatusRepo_ListPendingByBranch covers the in-flight statuses the
// poll must surface and the ones it must not.
func TestFiscalStatusRepo_ListPendingByBranch(t *testing.T) {
	requireDB(t)
	now := time.Now().UTC()

	tests := []struct {
		name        string
		status      domain.FiscalSubmissionStatus
		wantPending bool
	}{
		{"pending is in flight", domain.FiscalSubmissionPending, true},
		{"submitted is still awaiting the device", domain.FiscalSubmissionSubmitted, true},
		{"completed has settled", domain.FiscalSubmissionCompleted, false},
		{"failed has settled", domain.FiscalSubmissionFailed, false},
		{"voided has settled", domain.FiscalSubmissionVoided, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			paymentID := seedSubmission(t, tenantA, branchA, tt.status, "", now)
			assert.Equal(t, tt.wantPending, containsPayment(listPending(t, tenantA, branchA), paymentID))
		})
	}
}

// TestFiscalStatusRepo_PendingCarriesPaymentFacts asserts the joined payment
// columns the client needs to render the waiting sale.
func TestFiscalStatusRepo_PendingCarriesPaymentFacts(t *testing.T) {
	requireDB(t)
	paymentID := seedSubmission(t, tenantA, branchB, domain.FiscalSubmissionPending, "", time.Time{})

	rows := pendingFor(listPending(t, tenantA, branchB), paymentID)
	require.Len(t, rows, 1)
	assert.Equal(t, int64(12500), rows[0].AmountTotal)
	assert.NotNil(t, rows[0].CheckID)
	assert.False(t, rows[0].RegisteredAt.IsZero())
}

// TestFiscalStatusRepo_ListRecentlySettledByBranch asserts the reported status
// is the PAYMENT's (completed|failed|voided) and that last_error passes through
// verbatim, including the expired submission that fails its payment.
func TestFiscalStatusRepo_ListRecentlySettledByBranch(t *testing.T) {
	requireDB(t)
	now := time.Now().UTC()

	tests := []struct {
		name       string
		status     domain.FiscalSubmissionStatus
		lastError  string
		wantStatus string
		wantReason *string
	}{
		{"completed", domain.FiscalSubmissionCompleted, "", "completed", nil},
		{"failed carries the raw device error", domain.FiscalSubmissionFailed, "E-1234: kagit yok", "failed", ptr("E-1234: kagit yok")},
		{"voided", domain.FiscalSubmissionVoided, "", "voided", nil},
		{"expired submission reports its payment as failed", domain.FiscalSubmissionExpired, "basket TTL elapsed", "failed", ptr("basket TTL elapsed")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			paymentID := seedSubmission(t, tenantA, branchA, tt.status, tt.lastError, now)
			rows := settledFor(listSettled(t, tenantA, branchA, 5*time.Minute), paymentID)
			require.Len(t, rows, 1)
			assert.Equal(t, tt.wantStatus, rows[0].Status)
			assert.Equal(t, tt.wantReason, rows[0].FailureReason)
			assert.False(t, rows[0].SettledAt.IsZero())
			// The settled row keeps the amount so the station can go on
			// deducting a payment that has just left the pending list.
			assert.Equal(t, int64(12500), rows[0].AmountTotal)
			assert.NotNil(t, rows[0].CheckID)
		})
	}
}

// TestFiscalStatusRepo_SettledWindowExcludesOlder proves the recency window is
// actually applied — without it the response would grow into a shift-long
// history that a polling station re-downloads every few seconds.
func TestFiscalStatusRepo_SettledWindowExcludesOlder(t *testing.T) {
	requireDB(t)
	old := time.Now().UTC().Add(-30 * time.Minute)
	paymentID := seedSubmission(t, tenantA, branchA, domain.FiscalSubmissionCompleted, "", old)

	assert.Empty(t, settledFor(listSettled(t, tenantA, branchA, 5*time.Minute), paymentID),
		"a submission settled 30 minutes ago must fall outside the 5 minute window")
	assert.Len(t, settledFor(listSettled(t, tenantA, branchA, time.Hour), paymentID), 1,
		"and must reappear once the window covers it")
}

// TestFiscalStatusRepo_SettledDeduplicatesPayment guards the DISTINCT ON.
// fiscal_submissions_active_payment_idx is partial (pending|submitted only),
// so nothing stops a payment from owning several TERMINAL submission rows.
// Reporting the same payment twice with conflicting statuses would make the
// poll self-contradictory for the station reading it.
func TestFiscalStatusRepo_SettledDeduplicatesPayment(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	subs := repo.NewFiscalSubmissionRepo()
	now := time.Now().UTC()

	paymentID := seedSubmission(t, tenantA, branchA, domain.FiscalSubmissionFailed, "first attempt failed", now.Add(-2*time.Minute))

	// A second terminal row for the same payment, settled later.
	err := sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		payload, err := json.Marshal(map[string]any{"total": 12500})
		if err != nil {
			return err
		}
		id := uuid.New()
		if err := subs.Insert(ctx, tx, repo.FiscalSubmission{
			ID: id, TenantID: tenantA, BranchID: branchA, PaymentID: paymentID,
			AdapterType: "mock", Status: domain.FiscalSubmissionPending, SalePayload: payload,
		}); err != nil {
			return err
		}
		_, err = subs.MarkResult(ctx, tx, id, domain.FiscalSubmissionVoided, nil, "", now)
		return err
	})
	require.NoError(t, err)

	rows := settledFor(listSettled(t, tenantA, branchA, 5*time.Minute), paymentID)
	require.Len(t, rows, 1, "a payment must appear at most once in recently_settled")
	assert.Equal(t, now.Truncate(time.Millisecond), rows[0].SettledAt.UTC().Truncate(time.Millisecond),
		"the latest outcome wins")
}

// TestFiscalStatusRepo_BranchIsolation: two branches of the SAME tenant. RLS
// cannot catch this — it only filters tenant_id — so the query's branch
// predicate is the sole barrier.
func TestFiscalStatusRepo_BranchIsolation(t *testing.T) {
	requireDB(t)
	inA := seedSubmission(t, tenantA, branchA, domain.FiscalSubmissionPending, "", time.Time{})
	inB := seedSubmission(t, tenantA, branchB, domain.FiscalSubmissionPending, "", time.Time{})

	pendingA := listPending(t, tenantA, branchA)
	assert.True(t, containsPayment(pendingA, inA))
	assert.False(t, containsPayment(pendingA, inB), "branch A's poll must not see branch B's pending sale")
}

// TestFiscalStatusRepo_RLSIsolation: tenant A's poll must never observe tenant
// B's pending registrations, even when both use the same branch id — a hostile
// or buggy client can send any branch_id it likes.
func TestFiscalStatusRepo_RLSIsolation(t *testing.T) {
	requireDB(t)
	now := time.Now().UTC()

	pendingB := seedSubmission(t, tenantB, branchA, domain.FiscalSubmissionPending, "", time.Time{})
	settledB := seedSubmission(t, tenantB, branchA, domain.FiscalSubmissionCompleted, "", now)

	assert.False(t, containsPayment(listPending(t, tenantA, branchA), pendingB),
		"tenant A must not see tenant B's pending submission")
	assert.Empty(t, settledFor(listSettled(t, tenantA, branchA, 5*time.Minute), settledB),
		"tenant A must not see tenant B's settled submission")

	assert.True(t, containsPayment(listPending(t, tenantB, branchA), pendingB),
		"tenant B still sees its own row (proving the rows exist and the isolation is real)")
}

// TestFiscalStatusRepo_EmptyBranchReturnsEmptySlices: the repo returns
// allocated empty slices, never nil, so the JSON contract's [] holds even
// before the DTO layer.
func TestFiscalStatusRepo_EmptyBranchReturnsEmptySlices(t *testing.T) {
	requireDB(t)
	quietBranch := uuid.New()

	pending := listPending(t, tenantA, quietBranch)
	settled := listSettled(t, tenantA, quietBranch, 5*time.Minute)

	assert.NotNil(t, pending)
	assert.Empty(t, pending)
	assert.NotNil(t, settled)
	assert.Empty(t, settled)
}

func ptr(s string) *string { return &s }
