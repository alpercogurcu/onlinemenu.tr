package service_test

// Branch-scope authorization tests (ADR-AUTH-001 layer 3,
// docs/lessons-from-b2b.md item 6 — "authz rules must be bound to a test or
// the work isn't done"). These run against the shared testcontainers pool
// from integration_test.go's TestMain.
//
// Pattern per rule: allowed branch -> success; foreign branch ->
// pub.ErrBranchForbidden; chain-wide principal (the realistic shape of a
// manager's membership, per identity module's "nil = chain-wide" contract)
// -> exempt.

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/modules/billing/domain"
	pub "onlinemenu.tr/internal/modules/billing/public"
	"onlinemenu.tr/internal/modules/billing/repo"
	"onlinemenu.tr/internal/modules/billing/service"
	"onlinemenu.tr/internal/platform/auth"
)

// branchB is a second branch in tenantA, distinct from branchA, used to
// assert that a principal belonging to ONE branch cannot act on another
// branch's invoice.
var branchB = uuid.MustParse("cccccccc-0000-0000-0000-000000000002")

// branchPrincipal returns a staff principal scoped to a single branch.
func branchPrincipal(branchID uuid.UUID) auth.Principal {
	return auth.Principal{
		PersonID: uuid.New(),
		Ctx:      auth.ContextStaff,
		TenantID: tenantA,
		BranchID: branchID,
		RoleIDs:  []uuid.UUID{uuid.New()},
	}
}

// chainWidePrincipal returns a staff principal with no single-branch
// restriction (BranchID == uuid.Nil), the realistic shape of a manager's
// membership — exempt from branch-scope checks via
// auth.Principal.HasBranchAccess regardless of OPA scope.
func chainWidePrincipal() auth.Principal {
	return auth.Principal{
		PersonID: uuid.New(),
		Ctx:      auth.ContextStaff,
		TenantID: tenantA,
		BranchID: uuid.Nil,
		RoleIDs:  []uuid.UUID{uuid.New()},
	}
}

func newGenerateRequest(branchID uuid.UUID, idempotencyKey string) service.GenerateInvoiceRequest {
	return service.GenerateInvoiceRequest{
		TenantID:       tenantA,
		BranchID:       branchID,
		InvoiceType:    domain.InvoiceTypeEArsiv,
		IdempotencyKey: idempotencyKey,
		SupplierVKN:    "1234567890",
		SupplierName:   "TEST TEDARİKÇİ A.Ş.",
		CustomerVKN:    "9876543210",
		CustomerName:   "ALICI FİRMA LTD.",
		Items: []service.InvoiceItemRequest{
			{ProductName: "Adana Kebap", Quantity: 1, UnitPriceAmount: 10000, TaxRateBPS: 800},
		},
	}
}

func TestBillingAuthz_GenerateInvoice(t *testing.T) {
	ctx := context.Background()
	svc := newBillingService()

	t.Run("own branch may generate", func(t *testing.T) {
		inv, err := svc.GenerateInvoice(ctx, branchPrincipal(branchA), newGenerateRequest(branchA, "authz-own-001"))
		require.NoError(t, err)
		assert.Equal(t, branchA, inv.BranchID)
	})

	t.Run("foreign branch is forbidden from generating", func(t *testing.T) {
		_, err := svc.GenerateInvoice(ctx, branchPrincipal(branchB), newGenerateRequest(branchA, "authz-foreign-001"))
		assert.ErrorIs(t, err, pub.ErrBranchForbidden)
	})

	t.Run("chain-wide principal is exempt", func(t *testing.T) {
		inv, err := svc.GenerateInvoice(ctx, chainWidePrincipal(), newGenerateRequest(branchA, "authz-chainwide-001"))
		require.NoError(t, err)
		assert.Equal(t, branchA, inv.BranchID)
	})
}

// insertDraftInvoice writes an invoice directly via InvoiceRepo (bypassing
// BillingService.GenerateInvoice's own auto-submit-on-create step, which
// would immediately flip a mock-adapter-backed invoice to "submitted") so
// tests have a status="draft" invoice that is actually eligible for
// RetrySubmission. This mirrors the openTestCheck-style lower-level fixture
// helper used by the pos/inventory branch-scope tests.
func insertDraftInvoice(t *testing.T, ctx context.Context, branchID uuid.UUID, key string) uuid.UUID {
	t.Helper()
	invRepo := repo.NewInvoiceRepo()
	prod := uuid.New()
	inv := domain.Invoice{
		TenantID:       tenantA,
		BranchID:       branchID,
		InvoiceType:    domain.InvoiceTypeEArsiv,
		IdempotencyKey: key,
		GibUUID:        uuid.New(),
		SupplierVKN:    "1234567890",
		SupplierName:   "TEST TEDARİKÇİ A.Ş.",
		CustomerVKN:    "9876543210",
		CustomerName:   "ALICI FİRMA LTD.",
		Currency:       "TRY",
		Items: []domain.InvoiceItem{
			{ProductID: &prod, ProductName: "Adana Kebap", Quantity: 1, UnitPriceAmount: 10000, TaxRateBPS: 800, LineTotal: 10000, TaxAmount: 800},
		},
		AmountExcludingTax: 10000,
		TaxAmount:          800,
		AmountTotal:        10800,
	}

	var id uuid.UUID
	err := sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		created, createErr := invRepo.Create(ctx, tx, inv)
		if createErr != nil {
			return createErr
		}
		id = created.ID
		return nil
	})
	require.NoError(t, err)
	return id
}

func TestBillingAuthz_RetrySubmission(t *testing.T) {
	ctx := context.Background()
	svc := newBillingService()

	t.Run("owning branch may retry", func(t *testing.T) {
		id := insertDraftInvoice(t, ctx, branchA, "authz-retry-own-001")
		inv, err := svc.RetrySubmission(ctx, branchPrincipal(branchA), tenantA, id)
		require.NoError(t, err)
		assert.Equal(t, domain.InvoiceStatusSubmitted, inv.Status)
	})

	t.Run("foreign branch is forbidden from retrying", func(t *testing.T) {
		id := insertDraftInvoice(t, ctx, branchA, "authz-retry-foreign-001")
		_, err := svc.RetrySubmission(ctx, branchPrincipal(branchB), tenantA, id)
		assert.ErrorIs(t, err, pub.ErrBranchForbidden)
	})

	t.Run("chain-wide principal is exempt", func(t *testing.T) {
		id := insertDraftInvoice(t, ctx, branchA, "authz-retry-chainwide-001")
		inv, err := svc.RetrySubmission(ctx, chainWidePrincipal(), tenantA, id)
		require.NoError(t, err)
		assert.Equal(t, domain.InvoiceStatusSubmitted, inv.Status)
	})
}
