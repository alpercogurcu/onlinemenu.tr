package service

// Unit tests for ReportService.SaleDetails validation and composition logic.
// Deliberately DB-free: salesSummaryStore and paymentSummary are the narrow
// interfaces ReportService depends on (see report_service.go), faked here so
// this file needs no testcontainers pool — unlike the *_test.go files in this
// package suffixed service_test (e.g. branch_authz_test.go), which run
// against the shared integration pool.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	paymentpub "onlinemenu.tr/internal/modules/payment/public"
	"onlinemenu.tr/internal/modules/pos/domain"
	pub "onlinemenu.tr/internal/modules/pos/public"
	"onlinemenu.tr/internal/platform/auth"
)

// fakeSalesSummaryStore is a test double for salesSummaryStore. called
// records whether SalesSummary was ever invoked, so tests can assert a
// rejected request never reaches it (e.g. branch-forbidden must fail before
// any data access).
type fakeSalesSummaryStore struct {
	summary domain.SalesSummary
	err     error
	called  bool
}

func (f *fakeSalesSummaryStore) SalesSummary(_ context.Context, _ uuid.UUID, _ domain.SalesSummaryFilter) (domain.SalesSummary, error) {
	f.called = true
	return f.summary, f.err
}

// fakePaymentSummary is a test double for paymentSummary (the narrowed
// paymentpub.SalesSummaryReader).
type fakePaymentSummary struct {
	totals   []paymentpub.MethodTotal
	sessions []paymentpub.CashSessionSummary
}

func (f *fakePaymentSummary) PaymentTotalsByMethod(_ context.Context, _, _ uuid.UUID, _, _ time.Time) ([]paymentpub.MethodTotal, error) {
	return f.totals, nil
}

func (f *fakePaymentSummary) CashSessionsInWindow(_ context.Context, _, _ uuid.UUID, _, _ time.Time) ([]paymentpub.CashSessionSummary, error) {
	return f.sessions, nil
}

// reportTestPrincipal returns a branch-scoped staff principal for branchID —
// requireBranch's direct BranchID-match path, no OPA scope needed in ctx
// (mirrors branch_authz_test.go's branchPrincipal).
func reportTestPrincipal(branchID uuid.UUID) auth.Principal {
	return auth.Principal{
		PersonID: uuid.New(),
		Ctx:      auth.ContextStaff,
		TenantID: uuid.New(),
		BranchID: branchID,
		RoleIDs:  []uuid.UUID{uuid.New()},
	}
}

func newTestReportService(store salesSummaryStore, payments paymentSummary) *ReportService {
	return &ReportService{repo: store, payments: payments, logger: zap.NewNop()}
}

func TestReportService_SaleDetails_InvalidRange(t *testing.T) {
	branchID := uuid.New()
	store := &fakeSalesSummaryStore{}
	svc := newTestReportService(store, &fakePaymentSummary{})

	now := time.Now()
	_, err := svc.SaleDetails(context.Background(), reportTestPrincipal(branchID), SaleDetailsRequest{
		BranchID: branchID,
		From:     now,
		To:       now.Add(-time.Hour), // to before from
		TZ:       "Europe/Istanbul",
	})

	require.ErrorIs(t, err, ErrInvalidRange)
	assert.False(t, store.called, "repo must not be called on validation failure")
}

func TestReportService_SaleDetails_InvalidRange_Equal(t *testing.T) {
	branchID := uuid.New()
	store := &fakeSalesSummaryStore{}
	svc := newTestReportService(store, &fakePaymentSummary{})

	now := time.Now()
	_, err := svc.SaleDetails(context.Background(), reportTestPrincipal(branchID), SaleDetailsRequest{
		BranchID: branchID,
		From:     now,
		To:       now, // from == to
		TZ:       "Europe/Istanbul",
	})

	require.ErrorIs(t, err, ErrInvalidRange)
}

func TestReportService_SaleDetails_RangeTooLong(t *testing.T) {
	branchID := uuid.New()
	store := &fakeSalesSummaryStore{}
	svc := newTestReportService(store, &fakePaymentSummary{})

	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(93 * 24 * time.Hour)
	_, err := svc.SaleDetails(context.Background(), reportTestPrincipal(branchID), SaleDetailsRequest{
		BranchID: branchID,
		From:     from,
		To:       to,
		TZ:       "Europe/Istanbul",
	})

	require.ErrorIs(t, err, ErrRangeTooLong)
	assert.False(t, store.called, "repo must not be called on validation failure")
}

func TestReportService_SaleDetails_InvalidTimezone(t *testing.T) {
	branchID := uuid.New()
	store := &fakeSalesSummaryStore{}
	svc := newTestReportService(store, &fakePaymentSummary{})

	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)

	tests := []string{"", "Not/AZone"}
	for _, tz := range tests {
		_, err := svc.SaleDetails(context.Background(), reportTestPrincipal(branchID), SaleDetailsRequest{
			BranchID: branchID,
			From:     from,
			To:       to,
			TZ:       tz,
		})
		require.ErrorIsf(t, err, ErrInvalidTimezone, "tz=%q", tz)
	}
	assert.False(t, store.called, "repo must not be called on validation failure")
}

func TestReportService_SaleDetails_BranchForbidden(t *testing.T) {
	branchID := uuid.New()
	otherBranch := uuid.New()
	store := &fakeSalesSummaryStore{}
	svc := newTestReportService(store, &fakePaymentSummary{})

	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	_, err := svc.SaleDetails(context.Background(), reportTestPrincipal(otherBranch), SaleDetailsRequest{
		BranchID: branchID,
		From:     from,
		To:       to,
		TZ:       "Europe/Istanbul",
	})

	require.ErrorIs(t, err, pub.ErrBranchForbidden)
	assert.False(t, store.called, "repo must not be called when branch is forbidden")
}

func TestReportService_SaleDetails_HappyPath(t *testing.T) {
	branchID := uuid.New()
	summary := domain.SalesSummary{
		ClosedCheckCount:    2,
		CancelledCheckCount: 1,
		GrossSales:          28000,
		CancelledAmount:     4000,
		ItemCount:           4,
		ByTaxRate:           []domain.TaxLine{{RateBPS: 1000, Gross: 23000, Base: 20909, Tax: 2091}},
		ByDay:               []domain.DayLine{{Date: "2026-09-04", Gross: 25000, CheckCount: 1}},
		BySource:            []domain.SourceLine{{Source: "pos", CheckCount: 1, Gross: 25000}},
	}
	store := &fakeSalesSummaryStore{summary: summary}
	closedAt := time.Date(2026, 9, 4, 20, 0, 0, 0, time.UTC)
	closingAmount := int64(52500)
	diff := int64(0)
	payments := &fakePaymentSummary{
		totals: []paymentpub.MethodTotal{{Method: "cash", Status: "completed", Count: 2, Total: 3500}},
		sessions: []paymentpub.CashSessionSummary{{
			ID:                   uuid.New(),
			Status:               "closed",
			OpenedAt:             closedAt.Add(-8 * time.Hour),
			ClosedAt:             &closedAt,
			OpeningCountedAmount: 50000,
			CashPaymentsTaken:    3500,
			MovementsNet:         -1000,
			ExpectedClose:        52500,
			ClosingCountedAmount: &closingAmount,
			Difference:           &diff,
		}},
	}
	svc := newTestReportService(store, payments)

	from := time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	got, err := svc.SaleDetails(context.Background(), reportTestPrincipal(branchID), SaleDetailsRequest{
		BranchID: branchID,
		From:     from,
		To:       to,
		TZ:       "Europe/Istanbul",
	})

	require.NoError(t, err)
	assert.True(t, store.called)
	assert.Equal(t, summary, got.SalesSummary)
	assert.Equal(t, int64(14000), got.AverageCheck, "28000 / 2 closed checks")
	assert.Equal(t, payments.totals, got.Payments)
	assert.Equal(t, payments.sessions, got.CashSessions)
}

func TestReportService_SaleDetails_AverageCheckZeroWhenNoClosedChecks(t *testing.T) {
	branchID := uuid.New()
	store := &fakeSalesSummaryStore{summary: domain.SalesSummary{ClosedCheckCount: 0, GrossSales: 0}}
	svc := newTestReportService(store, &fakePaymentSummary{})

	from := time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	got, err := svc.SaleDetails(context.Background(), reportTestPrincipal(branchID), SaleDetailsRequest{
		BranchID: branchID,
		From:     from,
		To:       to,
		TZ:       "Europe/Istanbul",
	})

	require.NoError(t, err)
	assert.Equal(t, int64(0), got.AverageCheck)
}

// TestReportService_SaleDetails_NilPaymentSlicesPassThrough documents that
// ReportService itself does NOT normalize nil slices from
// paymentpub.SalesSummaryReader (empty window / branch with no payments is a
// real, documented case — see paymentpub.SalesSummaryReader) — that
// normalization is the HTTP DTO's job (report_handler.go's
// toSaleDetailsResponse, see TestToSaleDetailsResponse_ArraysAreNeverNull in
// the http package), so any other consumer of this service (a future
// non-HTTP caller) is not silently handed an empty-but-non-nil slice it
// never asked for.
func TestReportService_SaleDetails_NilPaymentSlicesPassThrough(t *testing.T) {
	branchID := uuid.New()
	store := &fakeSalesSummaryStore{summary: domain.SalesSummary{ClosedCheckCount: 0, GrossSales: 0}}
	svc := newTestReportService(store, &fakePaymentSummary{totals: nil, sessions: nil})

	from := time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	got, err := svc.SaleDetails(context.Background(), reportTestPrincipal(branchID), SaleDetailsRequest{
		BranchID: branchID,
		From:     from,
		To:       to,
		TZ:       "Europe/Istanbul",
	})

	require.NoError(t, err)
	assert.Nil(t, got.Payments)
	assert.Nil(t, got.CashSessions)
}
