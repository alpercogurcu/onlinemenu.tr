package repo_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/modules/payment/domain"
	"onlinemenu.tr/internal/modules/payment/repo"
)

// Branch isolation, unlike tenant isolation, is NOT enforced by RLS — it lives
// in the query predicate, so these tests need branchB (declared in
// fiscal_status_repo_test.go) as a same-tenant sibling of branchA to be
// meaningful at all.

// seedSettlementCheck creates one check carrying every payment state that
// matters to the settlement read, and returns the ids of the two completed
// ones.
func seedSettlementCheck(t *testing.T, tenantID, branchID, checkID uuid.UUID, keyPrefix string) (uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	r := repo.NewPaymentRepo()

	var completedA, completedB uuid.UUID
	err := sharedPool.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		create := func(key string, amount int64) (domain.Payment, error) {
			return r.Create(ctx, tx, domain.Payment{
				TenantID:       tenantID,
				BranchID:       branchID,
				CheckID:        &checkID,
				IdempotencyKey: keyPrefix + "-" + key,
				Method:         domain.PaymentMethodCash,
				AmountTotal:    amount,
				Currency:       "TRY",
			})
		}
		complete := func(p domain.Payment, receiptNo string) error {
			receiptID, err := r.InsertFiscalReceipt(ctx, tx, domain.FiscalReceipt{
				TenantID:      tenantID,
				PaymentID:     p.ID,
				DeviceType:    "mock",
				ReceiptNumber: receiptNo,
				ReceiptData:   map[string]any{},
			})
			if err != nil {
				return err
			}
			return r.Complete(ctx, tx, p.ID, receiptID)
		}

		a, err := create("completed-a", 12500)
		if err != nil {
			return err
		}
		if err := complete(a, keyPrefix+"-RCPT-A"); err != nil {
			return err
		}
		completedA = a.ID

		b, err := create("completed-b", 3000)
		if err != nil {
			return err
		}
		if err := complete(b, keyPrefix+"-RCPT-B"); err != nil {
			return err
		}
		completedB = b.ID

		if _, err := create("pending", 2500); err != nil {
			return err
		}

		failed, err := create("failed", 7000)
		if err != nil {
			return err
		}
		if err := r.Fail(ctx, tx, failed.ID); err != nil {
			return err
		}

		voided, err := create("voided", 9900)
		if err != nil {
			return err
		}
		if err := complete(voided, keyPrefix+"-RCPT-V"); err != nil {
			return err
		}
		return r.Void(ctx, tx, voided.ID)
	})
	require.NoError(t, err)
	return completedA, completedB
}

func readSettlement(t *testing.T, tenantID, checkID uuid.UUID, branchID *uuid.UUID) ([]repo.CheckSettledRow, int64) {
	t.Helper()
	ctx := context.Background()
	r := repo.NewPaymentRepo()

	var completed []repo.CheckSettledRow
	var pending int64
	err := sharedPool.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		completed, err = r.ListCompletedByCheck(ctx, tx, tenantID, checkID, branchID)
		if err != nil {
			return err
		}
		pending, err = r.PendingTotalForCheckInBranch(ctx, tx, tenantID, checkID, branchID)
		return err
	})
	require.NoError(t, err)
	return completed, pending
}

// TestCheckSettlement_StatusPartition is the core correctness claim: completed
// payments are listed with their exact ids and amounts, pending money is summed
// separately, and failed/voided payments appear in NEITHER.
//
// The failed/voided assertion is the one that guards real money. If a voided
// payment leaked into completed, the station would subtract a refunded amount
// from the balance and the customer would walk out having paid less than the
// check; if it leaked into pending_total, the check could never be closed.
func TestCheckSettlement_StatusPartition(t *testing.T) {
	requireDB(t)
	checkID := uuid.New()
	completedA, completedB := seedSettlementCheck(t, tenantA, branchA, checkID, "settle-partition")

	completed, pending := readSettlement(t, tenantA, checkID, &branchA)

	byID := map[uuid.UUID]int64{}
	for _, row := range completed {
		byID[row.PaymentID] = row.AmountTotal
	}
	assert.Len(t, completed, 2, "only the two completed payments; failed and voided must not appear")
	assert.Equal(t, int64(12500), byID[completedA])
	assert.Equal(t, int64(3000), byID[completedB])

	// Exact sum, not just membership: a duplicated row would still satisfy the
	// per-id assertions above but would double-subtract at the counter.
	var sum int64
	for _, row := range completed {
		sum += row.AmountTotal
	}
	assert.Equal(t, int64(15500), sum)

	// 2500 only. The failed 7000 and voided 9900 must not be waiting on anything.
	assert.Equal(t, int64(2500), pending)
}

// TestCheckSettlement_TenantIsolation verifies RLS (ADR-AUTH-001 layer 1) still
// covers this read: tenant B cannot see tenant A's check even holding its id
// and passing no branch filter at all (tenant scope, the widest case).
func TestCheckSettlement_TenantIsolation(t *testing.T) {
	requireDB(t)
	checkID := uuid.New()
	seedSettlementCheck(t, tenantA, branchA, checkID, "settle-tenant")

	completed, pending := readSettlement(t, tenantB, checkID, nil)

	assert.Empty(t, completed, "RLS must hide another tenant's payments")
	assert.Equal(t, int64(0), pending)
}

// TestCheckSettlement_BranchIsolation verifies the layer-3 scope filter, which
// RLS does NOT provide: branch A and branch B are the same tenant, so only the
// query predicate separates them.
//
// Both halves matter. Asserting only on completed would pass even if
// pending_total were summed tenant-wide — and a non-zero pending_total for a
// check the cashier may not see is exactly the enumeration signal this endpoint
// is specified to withhold (it confirms the check id exists elsewhere in the
// chain).
func TestCheckSettlement_BranchIsolation(t *testing.T) {
	requireDB(t)
	checkID := uuid.New()
	seedSettlementCheck(t, tenantA, branchB, checkID, "settle-branch")

	t.Run("foreign branch reads as an unpaid check, not an error", func(t *testing.T) {
		completed, pending := readSettlement(t, tenantA, checkID, &branchA)
		assert.Empty(t, completed)
		assert.Equal(t, int64(0), pending, "pending must be branch-filtered too, or it leaks the check's existence")
	})

	t.Run("owning branch sees it", func(t *testing.T) {
		completed, pending := readSettlement(t, tenantA, checkID, &branchB)
		assert.Len(t, completed, 2)
		assert.Equal(t, int64(2500), pending)
	})

	t.Run("tenant scope spans branches", func(t *testing.T) {
		completed, pending := readSettlement(t, tenantA, checkID, nil)
		assert.Len(t, completed, 2, "a nil branch filter is manager scope: every branch")
		assert.Equal(t, int64(2500), pending)
	})
}

// TestCheckSettlement_NilBranchMatchesNothing pins the fail-closed direction
// for a branch-scoped principal whose BranchID is unset. uuid.Nil is a real
// value here, not "any branch": payments.branch_id is NOT NULL so it matches no
// row. Failing closed shows an unpaid check and the cashier declines to close —
// far safer than showing another branch's money as collected.
func TestCheckSettlement_NilBranchMatchesNothing(t *testing.T) {
	requireDB(t)
	checkID := uuid.New()
	seedSettlementCheck(t, tenantA, branchA, checkID, "settle-nilbranch")

	nilBranch := uuid.Nil
	completed, pending := readSettlement(t, tenantA, checkID, &nilBranch)

	assert.Empty(t, completed)
	assert.Equal(t, int64(0), pending)
}

// TestCheckSettlement_UnknownCheck verifies the empty case returns an empty
// slice and a zero sum rather than a NULL scan error. The handler turns the
// slice into [] and the client treats 0 as "nothing pending"; there is no
// "unknown" state on this wire contract.
func TestCheckSettlement_UnknownCheck(t *testing.T) {
	requireDB(t)

	completed, pending := readSettlement(t, tenantA, uuid.New(), &branchA)

	assert.NotNil(t, completed, "must be an empty slice, never nil")
	assert.Empty(t, completed)
	assert.Equal(t, int64(0), pending)
}
