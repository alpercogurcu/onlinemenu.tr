package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/fx"
	"go.uber.org/zap"

	paymentpub "onlinemenu.tr/internal/modules/payment/public"
	"onlinemenu.tr/internal/modules/pos/domain"
	"onlinemenu.tr/internal/modules/pos/repo"
	"onlinemenu.tr/internal/platform/auth"
	"onlinemenu.tr/internal/platform/db"
)

// ErrInvalidRange is returned when From is not strictly before To.
var ErrInvalidRange = errors.New("pos/service/report: from must be before to")

// ErrRangeTooLong is returned when the [From, To) window exceeds
// maxReportRange (92 days) — a bound on the aggregate queries
// ReportRepo.SalesSummary runs, not a business rule (a "day-end" report
// spanning a whole quarter is still a valid request in principle, but
// pilot-scale checks/orders tables have no index tuned for a scan that wide).
var ErrRangeTooLong = errors.New("pos/service/report: range exceeds 92 days")

// ErrInvalidTimezone is returned when TZ is a non-empty string that is not a
// loadable IANA zone name. An empty TZ is no longer an error — see
// DefaultReportTZ — but a caller-supplied garbage value still must be
// reported, not silently defaulted, so a typo'd tz query parameter (e.g.
// "Europe/Istambul") is not answered with a wrong-zone report the client
// never asked for.
var ErrInvalidTimezone = errors.New("pos/service/report: invalid timezone")

// DefaultReportTZ is the timezone SaleDetails uses when
// SaleDetailsRequest.TZ is empty — pilot scope is Turkey-only, so an
// unspecified tz means "the branch's own timezone" for every branch that
// exists today. Exported so http.saleDetails can apply the identical default
// before echoing the resolved value back in the response's "tz" field
// (ReportService.SaleDetails itself returns no TZ field to read that back
// from — see SaleDetails' doc comment).
const DefaultReportTZ = "Europe/Istanbul"

// maxReportRange bounds SaleDetailsRequest's [From, To) window; see
// ErrRangeTooLong.
const maxReportRange = 92 * 24 * time.Hour

// salesSummaryStore is the narrow, DB-transaction-free view of
// repo.ReportRepo.SalesSummary that ReportService depends on — the tenant
// read-transaction wrapping (RLS, ADR-SEC-001/002) lives in dbSalesSummaryStore
// below, not here, so report_service_test.go can fake this interface and
// exercise ReportService's validation/authz/composition logic without a
// database.
type salesSummaryStore interface {
	SalesSummary(ctx context.Context, tenantID uuid.UUID, f domain.SalesSummaryFilter) (domain.SalesSummary, error)
}

// paymentSummary narrows paymentpub.SalesSummaryReader to what ReportService
// calls, for the same DB-free-testing reason as salesSummaryStore. Every
// *paymentpub.SalesSummaryReader implementation already satisfies it.
type paymentSummary interface {
	PaymentTotalsByMethod(ctx context.Context, tenantID, branchID uuid.UUID, from, to time.Time) ([]paymentpub.MethodTotal, error)
	CashSessionsInWindow(ctx context.Context, tenantID, branchID uuid.UUID, from, to time.Time) ([]paymentpub.CashSessionSummary, error)
}

// dbSalesSummaryStore is the production salesSummaryStore: it opens a
// tenant-scoped read transaction (RLS-enforced) and runs
// repo.ReportRepo.SalesSummary inside it. Kept separate from ReportService so
// the service itself never touches *db.Pool directly (see salesSummaryStore's
// doc comment).
type dbSalesSummaryStore struct {
	db   *db.Pool
	repo *repo.ReportRepo
}

func (s *dbSalesSummaryStore) SalesSummary(ctx context.Context, tenantID uuid.UUID, f domain.SalesSummaryFilter) (domain.SalesSummary, error) {
	var summary domain.SalesSummary
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		summary, err = s.repo.SalesSummary(ctx, tx, f)
		return err
	})
	if err != nil {
		return domain.SalesSummary{}, err
	}
	return summary, nil
}

// ReportService answers the pos day-end sales report use case
// (GET /api/v1/pos/reports/sale-details): a branch/window sales summary
// (pos-owned, ReportRepo) merged with the payment-side breakdown
// (payment-owned, consumed only via paymentpub.SalesSummaryReader — pos may
// not import payment beyond its public package).
type ReportService struct {
	repo     salesSummaryStore
	payments paymentSummary
	logger   *zap.Logger
}

// ReportParams groups fx-injected dependencies.
type ReportParams struct {
	fx.In

	DB       *db.Pool
	Repo     *repo.ReportRepo
	Payments paymentpub.SalesSummaryReader
	Logger   *zap.Logger
}

func NewReportService(p ReportParams) *ReportService {
	return &ReportService{
		repo:     &dbSalesSummaryStore{db: p.DB, repo: p.Repo},
		payments: p.Payments,
		logger:   p.Logger,
	}
}

// SaleDetailsRequest is ReportService.SaleDetails' input: one branch and a
// half-open [From, To) window in the given IANA timezone (see
// domain.SalesSummaryFilter.TZ).
type SaleDetailsRequest struct {
	BranchID uuid.UUID
	From, To time.Time
	TZ       string
}

// PaymentTotal is pos's own copy of paymentpub.MethodTotal, field-for-field.
// It exists because pos_http may not import payment_public (.go-arch-lint.yml:
// only pos_service may — module isolation) but still needs to render the
// payment breakdown in the JSON response; ReportService converts at the
// module boundary (see toPaymentTotals) so http/report_handler.go never sees
// a payment package type.
type PaymentTotal struct {
	Method string
	Status string
	Count  int64
	Total  int64
}

// CashSessionSummary is pos's own copy of paymentpub.CashSessionSummary, for
// the same reason as PaymentTotal.
type CashSessionSummary struct {
	ID       uuid.UUID
	Status   string
	OpenedAt time.Time
	ClosedAt *time.Time

	OpeningCountedAmount int64
	CashPaymentsTaken    int64
	MovementsNet         int64
	ExpectedClose        int64

	ClosingCountedAmount *int64
	Difference           *int64
}

// toPaymentTotals converts payment's public type to pos's own at the module
// boundary. Preserves nil: a nil input (paymentpub.SalesSummaryReader
// returns nil for an empty window, see its doc comment) yields a nil output,
// not an empty-but-non-nil slice — ReportService does not normalize nil to
// `[]`, that is http.toSaleDetailsResponse's job (see
// TestReportService_SaleDetails_NilPaymentSlicesPassThrough).
func toPaymentTotals(in []paymentpub.MethodTotal) []PaymentTotal {
	if in == nil {
		return nil
	}
	out := make([]PaymentTotal, len(in))
	for i, m := range in {
		out[i] = PaymentTotal{Method: m.Method, Status: m.Status, Count: m.Count, Total: m.Total}
	}
	return out
}

// toCashSessionSummaries is toPaymentTotals' counterpart for cash sessions —
// same nil-preserving contract.
func toCashSessionSummaries(in []paymentpub.CashSessionSummary) []CashSessionSummary {
	if in == nil {
		return nil
	}
	out := make([]CashSessionSummary, len(in))
	for i, c := range in {
		out[i] = CashSessionSummary{
			ID:                   c.ID,
			Status:               c.Status,
			OpenedAt:             c.OpenedAt,
			ClosedAt:             c.ClosedAt,
			OpeningCountedAmount: c.OpeningCountedAmount,
			CashPaymentsTaken:    c.CashPaymentsTaken,
			MovementsNet:         c.MovementsNet,
			ExpectedClose:        c.ExpectedClose,
			ClosingCountedAmount: c.ClosingCountedAmount,
			Difference:           c.Difference,
		}
	}
	return out
}

// SaleDetails is the day-end sales report: domain.SalesSummary (pos-owned:
// check/order aggregates) plus AverageCheck (derived here, not in the repo —
// it is a presentation figure, not a stored one) and the payment-side
// breakdown (Payments, CashSessions — pos's own PaymentTotal/CashSessionSummary
// types, converted from payment's public ones at the module boundary, see
// toPaymentTotals/toCashSessionSummaries), fetched from payment in a
// separate, later transaction — see SaleDetails' doc comment on the
// consistency window this implies.
type SaleDetails struct {
	domain.SalesSummary
	AverageCheck int64
	Payments     []PaymentTotal
	CashSessions []CashSessionSummary
}

// SaleDetails validates the request, enforces ADR-AUTH-001 layer 3 branch
// authorization, then assembles the report from two independent sources:
//
//  1. pos's own SalesSummary, read inside one tenant-scoped read transaction
//     (RLS-enforced).
//  2. payment's PaymentTotalsByMethod/CashSessionsInWindow, each its own
//     separate transaction in the payment module (module isolation: pos may
//     not share a transaction with payment, only call its public interface).
//
// Steps 1 and 2 are therefore not mutually consistent as of a single instant
// — a payment completing between the two reads can appear in one side's
// figures and not (yet) the other's. This is accepted for a read-only report:
// day-end reconciliation tolerates a few seconds of skew, and forcing
// cross-module transactional consistency here would mean pos taking a
// dependency on payment's transaction, which module isolation forbids.
func (s *ReportService) SaleDetails(ctx context.Context, principal auth.Principal, req SaleDetailsRequest) (SaleDetails, error) {
	// Applied before any validation so every caller of the service — HTTP or
	// otherwise — gets the identical default, not just http.saleDetails (see
	// DefaultReportTZ's doc comment).
	if req.TZ == "" {
		req.TZ = DefaultReportTZ
	}

	if !req.From.Before(req.To) {
		return SaleDetails{}, ErrInvalidRange
	}
	if req.To.Sub(req.From) > maxReportRange {
		return SaleDetails{}, ErrRangeTooLong
	}
	if _, err := time.LoadLocation(req.TZ); err != nil {
		return SaleDetails{}, ErrInvalidTimezone
	}

	if err := requireBranch(ctx, principal, req.BranchID); err != nil {
		return SaleDetails{}, err
	}

	summary, err := s.repo.SalesSummary(ctx, principal.TenantID, domain.SalesSummaryFilter{
		BranchID: req.BranchID,
		From:     req.From,
		To:       req.To,
		TZ:       req.TZ,
	})
	if err != nil {
		return SaleDetails{}, fmt.Errorf("pos/service/report: sale details: %w", err)
	}

	payments, err := s.payments.PaymentTotalsByMethod(ctx, principal.TenantID, req.BranchID, req.From, req.To)
	if err != nil {
		return SaleDetails{}, fmt.Errorf("pos/service/report: payment totals: %w", err)
	}
	sessions, err := s.payments.CashSessionsInWindow(ctx, principal.TenantID, req.BranchID, req.From, req.To)
	if err != nil {
		return SaleDetails{}, fmt.Errorf("pos/service/report: cash sessions: %w", err)
	}

	var avg int64
	if summary.ClosedCheckCount > 0 {
		avg = summary.GrossSales / summary.ClosedCheckCount
	}

	return SaleDetails{
		SalesSummary: summary,
		AverageCheck: avg,
		Payments:     toPaymentTotals(payments),
		CashSessions: toCashSessionSummaries(sessions),
	}, nil
}
