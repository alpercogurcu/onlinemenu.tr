package e2e_test

// End-to-end test for the pos day-end sales report (GET
// /api/v1/pos/reports/sale-details, possvc.ReportService.SaleDetails):
// open+pay+close two checks (one cash, one terminal), cancel a third, then
// assert the merged report — pos's own SalesSummary plus payment's
// method/cash-session breakdown, wired fx-free exactly like buildServices in
// spine_test.go.
//
// A dedicated branchID (reportBranchID, distinct from spine_test.go's shared
// branchID) keeps this test's cash session and check/order rows isolated
// from every other spine/storefront test sharing sharedPool + TestMain's
// package-lifetime cash session — CashSessionSummary.CashPaymentsTaken sums
// completed cash payments over the session's ENTIRE [OpenedAt, ClosedAt)
// life (not clipped to the report window, see paymentpub.CashSessionSummary's
// doc comment), so reusing the shared session/branch would make the exact
// CashPaymentsTaken assertion below depend on unrelated tests' execution
// order.
//
// requireBranch's principal here uses the direct BranchID-match path
// (auth.Principal.IsStaff() + BranchID == reportBranchID, RoleIDs set to the
// well-known shift_manager system role id for documentation), the same
// pattern staffPrincipal/branchPrincipal already use elsewhere in this test
// suite. No OPA-derived scope is planted in ctx: platform/auth exposes no
// exported inverse of ScopeFromContext, and requireBranch's own direct-match
// path already exercises the branch-authorization boundary this test cares
// about without needing one (see report_service_test.go's
// TestReportService_SaleDetails_BranchForbidden for the ctx-scope-absent
// path unit-tested directly).

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	paymentdomain "onlinemenu.tr/internal/modules/payment/domain"
	paymentrepo "onlinemenu.tr/internal/modules/payment/repo"
	paymentsvc "onlinemenu.tr/internal/modules/payment/service"
	posdomain "onlinemenu.tr/internal/modules/pos/domain"
	posrepo "onlinemenu.tr/internal/modules/pos/repo"
	possvc "onlinemenu.tr/internal/modules/pos/service"
	"onlinemenu.tr/internal/platform/auth"
)

// shiftManagerRoleID mirrors configs/opa/bundles/authz.rego's system_roles
// map — used here only to document intent (RequireBranchAccess/requireBranch
// does not itself consult RoleIDs on the direct-match path, see the file
// header comment).
var shiftManagerRoleID = uuid.MustParse("00000001-0000-0000-0000-000000000002")

// reportShiftManager is a branch-scoped shift_manager principal for
// reportBranchID.
func reportShiftManager(reportBranchID uuid.UUID) auth.Principal {
	return auth.Principal{
		PersonID: uuid.New(),
		Ctx:      auth.ContextStaff,
		TenantID: tenantID,
		BranchID: reportBranchID,
		RoleIDs:  []uuid.UUID{shiftManagerRoleID},
	}
}

// buildReportService wires possvc.ReportService fx-free, mirroring
// buildServices: a fresh payment SalesSummaryService (payment/service, own
// repos) plugged into pos's ReportService as paymentpub.SalesSummaryReader.
func buildReportService() *possvc.ReportService {
	salesSummary := paymentsvc.NewSalesSummaryService(paymentsvc.SalesSummaryParams{
		DB:          sharedPool,
		PaymentRepo: paymentrepo.NewPaymentRepo(),
		Sessions: paymentsvc.NewCashSessionService(paymentsvc.CashSessionParams{
			DB:         sharedPool,
			Sessions:   paymentrepo.NewCashSessionRepo(),
			FiscalRepo: paymentrepo.NewFiscalStatusRepo(),
			Logger:     zap.NewNop(),
		}),
		SessionRepo: paymentrepo.NewCashSessionRepo(),
	})

	return possvc.NewReportService(possvc.ReportParams{
		DB:       sharedPool,
		Repo:     posrepo.NewReportRepo(),
		Payments: salesSummary,
		Logger:   zap.NewNop(),
	})
}

func TestPOSReport_SaleDetails(t *testing.T) {
	ctx := context.Background()
	reportBranchID := uuid.New()
	principal := reportShiftManager(reportBranchID)

	checkSvc, orderSvc, paySvc := buildServices()

	// This test's own cash session, isolated from every other spine test's
	// shared-branch session — see the file header comment.
	cashSessions := paymentsvc.NewCashSessionService(paymentsvc.CashSessionParams{
		DB:         sharedPool,
		Sessions:   paymentrepo.NewCashSessionRepo(),
		FiscalRepo: paymentrepo.NewFiscalStatusRepo(),
		Logger:     zap.NewNop(),
	})
	_, err := cashSessions.Open(ctx, principal, paymentsvc.OpenCashSessionRequest{
		BranchID:             reportBranchID,
		OpeningCountedAmount: 0,
	})
	require.NoError(t, err)

	// Check 1: cash payment, closed.
	checkCash, err := checkSvc.Open(ctx, tenantID, principal, posdomain.Check{
		BranchID:   reportBranchID,
		TableLabel: "R1",
		OpenedBy:   &staffID,
	})
	require.NoError(t, err)
	_, err = orderSvc.Place(ctx, tenantID, principal, posdomain.Order{
		BranchID:     reportBranchID,
		CheckID:      &checkCash.ID,
		OrderChannel: posdomain.OrderChannelDineIn,
		Items: []posdomain.OrderItem{
			{ProductID: prodID, ProductName: "Nakit Kalem", ProductCurrency: "TRY", TaxRateBPS: 1000, Quantity: 1, UnitPriceAmount: 1500},
		},
	})
	require.NoError(t, err)
	_, err = paySvc.RegisterSale(ctx, paymentsvc.RegisterSaleRequest{
		TenantID:       tenantID,
		BranchID:       reportBranchID,
		CheckID:        &checkCash.ID,
		IdempotencyKey: "report-test-cash-001",
		Method:         paymentdomain.PaymentMethodCash,
		AmountTotal:    1500,
		Currency:       "TRY",
	})
	require.NoError(t, err)
	drainFiscal(t, paySvc)
	_, err = checkSvc.Close(ctx, tenantID, principal, checkCash.ID, staffID)
	require.NoError(t, err)

	// Check 2: terminal payment, closed.
	checkTerminal, err := checkSvc.Open(ctx, tenantID, principal, posdomain.Check{
		BranchID:   reportBranchID,
		TableLabel: "R2",
		OpenedBy:   &staffID,
	})
	require.NoError(t, err)
	_, err = orderSvc.Place(ctx, tenantID, principal, posdomain.Order{
		BranchID:     reportBranchID,
		CheckID:      &checkTerminal.ID,
		OrderChannel: posdomain.OrderChannelDineIn,
		Items: []posdomain.OrderItem{
			{ProductID: prodID, ProductName: "Kart Kalem", ProductCurrency: "TRY", TaxRateBPS: 1000, Quantity: 1, UnitPriceAmount: 2000},
		},
	})
	require.NoError(t, err)
	_, err = paySvc.RegisterSale(ctx, paymentsvc.RegisterSaleRequest{
		TenantID:       tenantID,
		BranchID:       reportBranchID,
		CheckID:        &checkTerminal.ID,
		IdempotencyKey: "report-test-terminal-001",
		Method:         paymentdomain.PaymentMethodTerminal,
		AmountTotal:    2000,
		Currency:       "TRY",
	})
	require.NoError(t, err)
	drainFiscal(t, paySvc)
	_, err = checkSvc.Close(ctx, tenantID, principal, checkTerminal.ID, staffID)
	require.NoError(t, err)

	// Check 3: cancelled, no payment.
	checkCancelled, err := checkSvc.Open(ctx, tenantID, principal, posdomain.Check{
		BranchID:   reportBranchID,
		TableLabel: "R3",
		OpenedBy:   &staffID,
	})
	require.NoError(t, err)
	_, err = checkSvc.Cancel(ctx, tenantID, principal, checkCancelled.ID, staffID)
	require.NoError(t, err)

	reportSvc := buildReportService()
	from := time.Now().Add(-1 * time.Hour)
	to := time.Now().Add(1 * time.Hour)

	details, err := reportSvc.SaleDetails(ctx, principal, possvc.SaleDetailsRequest{
		BranchID: reportBranchID,
		From:     from,
		To:       to,
		TZ:       "Europe/Istanbul",
	})
	require.NoError(t, err)

	assert.Equal(t, int64(2), details.ClosedCheckCount)
	assert.Equal(t, int64(1), details.CancelledCheckCount)
	assert.Equal(t, int64(3500), details.GrossSales, "1500 (cash check) + 2000 (terminal check)")
	assert.Equal(t, int64(1750), details.AverageCheck, "3500 / 2 closed checks")

	var sawCashCompleted, sawTerminalCompleted bool
	for _, p := range details.Payments {
		if p.Method == "cash" && p.Status == "completed" {
			sawCashCompleted = true
			assert.Equal(t, int64(1500), p.Total)
		}
		if p.Method == "terminal" && p.Status == "completed" {
			sawTerminalCompleted = true
			assert.Equal(t, int64(2000), p.Total)
		}
	}
	assert.True(t, sawCashCompleted, "expected a completed cash payment row")
	assert.True(t, sawTerminalCompleted, "expected a completed terminal payment row")

	require.GreaterOrEqual(t, len(details.CashSessions), 1)
	assert.Equal(t, int64(1500), details.CashSessions[0].CashPaymentsTaken,
		"this test's dedicated cash session sees only its own 1500 kuruş cash payment")
}
