package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/modules/payment/domain"
	pub "onlinemenu.tr/internal/modules/payment/public"
	"onlinemenu.tr/internal/modules/payment/service"
	"onlinemenu.tr/internal/platform/auth"
)

// TestFiscalBranchStatusFor_CashierSeesOtherStationsSale is the scenario the
// endpoint exists for, end to end at the highest layer reachable without a
// live HTTP stack: station 1 registers a sale, station 2's cashier polls and
// sees it pending, the fiscal worker settles it, and the next poll reports the
// outcome — all under a cashier principal carrying the real OPA scope, never
// payment.payment.read.
func TestFiscalBranchStatusFor_CashierSeesOtherStationsSale(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	svc := newPaymentService()

	cashier := auth.Principal{
		PersonID: uuid.New(),
		Ctx:      auth.ContextStaff,
		TenantID: tenantA,
		BranchID: branchA,
		RoleIDs:  []uuid.UUID{uuid.MustParse(cashierRoleID)},
	}
	scoped, reached := scopedCtx(t, cashier)
	require.True(t, reached, "cashier must hold payment.fiscal_status.read")

	checkID := uuid.New()
	sale, err := svc.RegisterSale(ctx, service.RegisterSaleRequest{
		TenantID:       tenantA,
		BranchID:       branchA,
		CheckID:        &checkID,
		IdempotencyKey: uuid.New().String(),
		Method:         domain.PaymentMethodCash,
		AmountTotal:    4200,
		Currency:       "TRY",
	})
	require.NoError(t, err)

	status, err := svc.FiscalBranchStatusFor(scoped, cashier, branchA)
	require.NoError(t, err)
	assert.Equal(t, branchA, status.BranchID)
	assert.WithinDuration(t, time.Now(), status.AsOf, time.Minute,
		"as_of is the server clock the stations synchronise their age display against")

	pending := findPending(status.Pending, sale.ID)
	require.NotNil(t, pending, "the sale started on another station must be visible to this one")
	assert.Equal(t, int64(4200), pending.AmountTotal)
	require.NotNil(t, pending.CheckID)
	assert.Equal(t, checkID, *pending.CheckID)
	assert.GreaterOrEqual(t, pending.AgeSeconds, int64(0))
	assert.Empty(t, findSettled(status.RecentlySettled, sale.ID))

	drainFiscal(t, svc)

	settledStatus, err := svc.FiscalBranchStatusFor(scoped, cashier, branchA)
	require.NoError(t, err)
	assert.Nil(t, findPending(settledStatus.Pending, sale.ID),
		"a settled sale must leave the pending list so the station stops waiting on it")

	settled := findSettled(settledStatus.RecentlySettled, sale.ID)
	require.Len(t, settled, 1)
	assert.Equal(t, "completed", settled[0].Status)
	assert.Nil(t, settled[0].FailureReason)
	assert.False(t, settled[0].SettledAt.IsZero())
	// Same amount the pending entry reported: the station must be able to keep
	// deducting it across the pending -> settled transition, or the check's
	// balance would briefly read as collectable again and be charged twice.
	assert.Equal(t, pending.AmountTotal, settled[0].AmountTotal)
	assert.Equal(t, int64(4200), settled[0].AmountTotal)
}

// TestFiscalBranchStatusFor_RefusesForeignBranch: the same cashier polling a
// sibling branch of its own chain. RLS cannot stop this — it filters tenant_id
// only — so a refusal here is the sole barrier.
func TestFiscalBranchStatusFor_RefusesForeignBranch(t *testing.T) {
	requireDB(t)
	svc := newPaymentService()

	cashier := auth.Principal{
		PersonID: uuid.New(),
		Ctx:      auth.ContextStaff,
		TenantID: tenantA,
		BranchID: branchA,
		RoleIDs:  []uuid.UUID{uuid.MustParse(cashierRoleID)},
	}
	scoped, reached := scopedCtx(t, cashier)
	require.True(t, reached)

	_, err := svc.FiscalBranchStatusFor(scoped, cashier, uuid.New())
	require.ErrorIs(t, err, pub.ErrBranchForbidden)
}

// TestFiscalBranchStatusFor_RejectsNilBranch guards the degenerate input: a
// client omitting branch_id must not be answered with some tenant-wide view.
func TestFiscalBranchStatusFor_RejectsNilBranch(t *testing.T) {
	requireDB(t)
	svc := newPaymentService()

	principal := auth.Principal{
		PersonID: uuid.New(),
		Ctx:      auth.ContextStaff,
		TenantID: tenantA,
		BranchID: branchA,
	}

	_, err := svc.FiscalBranchStatusFor(context.Background(), principal, uuid.Nil)
	require.Error(t, err)
}

// TestFiscalBranchStatusFor_QuietBranchReturnsEmptyLists: the idle-branch case
// every station hits on most polls. Lists must be allocated, never nil, so the
// JSON contract's [] holds.
func TestFiscalBranchStatusFor_QuietBranchReturnsEmptyLists(t *testing.T) {
	requireDB(t)
	svc := newPaymentService()

	quietBranch := uuid.New()
	// Branch-scoped principal pointed at its own (empty) branch: a nil BranchID
	// no longer grants chain-wide access here (see requireBranch).
	staff := auth.Principal{
		PersonID: uuid.New(),
		Ctx:      auth.ContextStaff,
		TenantID: tenantA,
		BranchID: quietBranch,
	}

	status, err := svc.FiscalBranchStatusFor(context.Background(), staff, quietBranch)
	require.NoError(t, err)
	assert.NotNil(t, status.Pending)
	assert.Empty(t, status.Pending)
	assert.NotNil(t, status.RecentlySettled)
	assert.Empty(t, status.RecentlySettled)
}

func findPending(items []service.FiscalPendingItem, paymentID uuid.UUID) *service.FiscalPendingItem {
	for i := range items {
		if items[i].PaymentID == paymentID {
			return &items[i]
		}
	}
	return nil
}

func findSettled(items []service.FiscalSettledItem, paymentID uuid.UUID) []service.FiscalSettledItem {
	var out []service.FiscalSettledItem
	for _, it := range items {
		if it.PaymentID == paymentID {
			out = append(out, it)
		}
	}
	return out
}

// TestOnFiscalResult_ReceiptCarriesDeviceTimeNotServerTime pins the clock
// split. fiscal_receipts.issued_at is the receipt's LEGAL timestamp and must
// stay the device's operation time, even though completed_at (which drives the
// fiscal status window) is now the server's. Collapsing the two would silently
// restamp every receipt with server time.
func TestOnFiscalResult_ReceiptCarriesDeviceTimeNotServerTime(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	svc := newPaymentService()

	deviceAt := time.Date(2026, 7, 10, 15, 4, 5, 0, time.UTC)
	serverAt := time.Now().UTC()

	payment, err := svc.RegisterSale(ctx, service.RegisterSaleRequest{
		TenantID:       tenantA,
		BranchID:       branchA,
		IdempotencyKey: uuid.New().String(),
		Method:         domain.PaymentMethodCash,
		AmountTotal:    3300,
	})
	require.NoError(t, err)

	require.NoError(t, svc.OnFiscalResult(ctx, domain.FiscalResult{
		SubmissionID:      submissionID(t, tenantA, payment.ID),
		TenantID:          tenantA,
		BranchID:          branchA,
		PaymentID:         payment.ID,
		Status:            domain.FiscalSubmissionCompleted,
		DeviceType:        "beko_x30tr_cloud",
		ReceiptNo:         "0042",
		CompletedAt:       serverAt,
		DeviceOperationAt: deviceAt,
	}))

	issuedAt, completedAt := receiptAndSubmissionTimes(t, payment.ID)
	assert.WithinDuration(t, deviceAt, issuedAt, time.Second,
		"issued_at is the legal receipt time and must follow the device clock")
	assert.WithinDuration(t, serverAt, completedAt, time.Second,
		"completed_at drives the fiscal status window and must follow the server clock")
}

// TestOnFiscalResult_ReceiptFallsBackToServerTime: drivers that report no
// device time (the synchronous mock, or a vendor payload with an unparseable
// operationDate) must still get a receipt, stamped with the server clock.
func TestOnFiscalResult_ReceiptFallsBackToServerTime(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	svc := newPaymentService()

	serverAt := time.Now().UTC()

	payment, err := svc.RegisterSale(ctx, service.RegisterSaleRequest{
		TenantID:       tenantA,
		BranchID:       branchA,
		IdempotencyKey: uuid.New().String(),
		Method:         domain.PaymentMethodCash,
		AmountTotal:    4400,
	})
	require.NoError(t, err)

	require.NoError(t, svc.OnFiscalResult(ctx, domain.FiscalResult{
		SubmissionID: submissionID(t, tenantA, payment.ID),
		TenantID:     tenantA,
		BranchID:     branchA,
		PaymentID:    payment.ID,
		Status:       domain.FiscalSubmissionCompleted,
		DeviceType:   "mock",
		ReceiptNo:    "0043",
		CompletedAt:  serverAt,
		// DeviceOperationAt deliberately zero.
	}))

	issuedAt, _ := receiptAndSubmissionTimes(t, payment.ID)
	assert.WithinDuration(t, serverAt, issuedAt, time.Second)
}

// TestOnFiscalResult_IgnoresAdapterSuppliedCompletedAt is the single-authority
// guarantee itself: OnFiscalResult must overwrite whatever CompletedAt an
// adapter hands it with the server's own clock before persisting. A driver
// that fed a device timestamp straight into CompletedAt (a synchronous ÖKC
// with a skewed clock, for instance) must not be able to push the row outside
// or inside the fiscal status poll's recency window.
func TestOnFiscalResult_IgnoresAdapterSuppliedCompletedAt(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	svc := newPaymentService()

	// A submission-time this far off the real clock could only end up in
	// completed_at if the stamp in OnFiscalResult were removed or bypassed.
	deviceSkewedCompletedAt := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	before := time.Now().UTC()

	payment, err := svc.RegisterSale(ctx, service.RegisterSaleRequest{
		TenantID:       tenantA,
		BranchID:       branchA,
		IdempotencyKey: uuid.New().String(),
		Method:         domain.PaymentMethodCash,
		AmountTotal:    1900,
	})
	require.NoError(t, err)

	require.NoError(t, svc.OnFiscalResult(ctx, domain.FiscalResult{
		SubmissionID: submissionID(t, tenantA, payment.ID),
		TenantID:     tenantA,
		BranchID:     branchA,
		PaymentID:    payment.ID,
		Status:       domain.FiscalSubmissionCompleted,
		DeviceType:   "beko_x30tr_cloud",
		ReceiptNo:    "0055",
		CompletedAt:  deviceSkewedCompletedAt,
	}))
	after := time.Now().UTC()

	_, completedAt := receiptAndSubmissionTimes(t, payment.ID)
	assert.False(t, completedAt.Before(before) || completedAt.After(after),
		"completed_at must be stamped by the server's OnFiscalResult clock, not the caller-supplied value %s", deviceSkewedCompletedAt)
}

func receiptAndSubmissionTimes(t *testing.T, paymentID uuid.UUID) (issuedAt, completedAt time.Time) {
	t.Helper()
	ctx := context.Background()
	err := sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT r.issued_at, s.completed_at
			FROM fiscal_receipts r
			JOIN fiscal_submissions s ON s.payment_id = r.payment_id
			WHERE r.payment_id = $1
		`, paymentID).Scan(&issuedAt, &completedAt)
	})
	require.NoError(t, err)
	return issuedAt, completedAt
}
