package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/modules/payment/repo"
	"onlinemenu.tr/internal/modules/payment/service"
)

// TestSalesSummaryService_CashSessionsInWindow_MapsBuildViewFields pins the
// mapping from CashSessionService.buildView's CashSessionView (already
// exercised in full by TestCashSessionService_GetActive_DifferenceArithmetic)
// into pub.CashSessionSummary — in particular that Difference/
// ClosingCountedAmount stay nil for a still-open session and become non-nil,
// with the correct arithmetic, once a closing count is submitted and the
// session closes. CashSessionRepo.ListByBranchWindow's own filtering/ordering
// is covered by the repo-level integration test; this test only checks the
// field-by-field translation done in SalesSummaryService.
func TestSalesSummaryService_CashSessionsInWindow_MapsBuildViewFields(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	cashSvc := newCashSessionService()
	sut := service.NewSalesSummaryService(service.SalesSummaryParams{
		DB:          sharedPool,
		PaymentRepo: repo.NewPaymentRepo(),
		Sessions:    cashSvc,
		SessionRepo: repo.NewCashSessionRepo(),
	})

	branch := uuid.New()
	manager := shiftManagerPrincipal(branch)

	opened, err := cashSvc.Open(ctx, manager, service.OpenCashSessionRequest{BranchID: branch, OpeningCountedAmount: 5000})
	require.NoError(t, err)
	session := opened.Session

	from := session.OpenedAt.Add(-time.Hour)
	to := session.OpenedAt.Add(time.Hour)

	// -- still open: ClosingCountedAmount/Difference must both be nil --------
	summaries, err := sut.CashSessionsInWindow(ctx, manager.TenantID, branch, from, to)
	require.NoError(t, err)
	require.Len(t, summaries, 1)
	open := summaries[0]
	assert.Equal(t, session.ID, open.ID)
	assert.Equal(t, "opened", open.Status)
	assert.True(t, session.OpenedAt.Equal(open.OpenedAt))
	assert.Nil(t, open.ClosedAt)
	assert.Equal(t, int64(5000), open.OpeningCountedAmount)
	assert.Equal(t, int64(0), open.CashPaymentsTaken)
	assert.Equal(t, int64(0), open.MovementsNet)
	assert.Equal(t, int64(5000), open.ExpectedClose)
	assert.Nil(t, open.ClosingCountedAmount)
	assert.Nil(t, open.Difference)

	// -- closed with a counted surplus of 300: both must become non-nil ------
	const closingCounted = int64(5300)
	_, err = cashSvc.SubmitClosingCount(ctx, manager, session.ID, service.SubmitClosingCountRequest{
		ClosingCountedAmount: closingCounted,
	})
	require.NoError(t, err)
	closedView, err := cashSvc.Close(ctx, manager, session.ID)
	require.NoError(t, err)

	summaries, err = sut.CashSessionsInWindow(ctx, manager.TenantID, branch, from, to.Add(time.Hour))
	require.NoError(t, err)
	require.Len(t, summaries, 1)
	closed := summaries[0]
	assert.Equal(t, session.ID, closed.ID)
	assert.Equal(t, "closed", closed.Status)
	require.NotNil(t, closed.ClosedAt)
	assert.True(t, closedView.Session.ClosedAt.Equal(*closed.ClosedAt))
	require.NotNil(t, closed.ClosingCountedAmount)
	assert.Equal(t, closingCounted, *closed.ClosingCountedAmount)
	require.NotNil(t, closed.Difference)
	assert.Equal(t, int64(300), *closed.Difference, "5300 counted - 5000 expected close = 300")
}
