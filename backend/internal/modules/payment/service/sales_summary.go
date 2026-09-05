package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/fx"

	"onlinemenu.tr/internal/modules/payment/domain"
	pub "onlinemenu.tr/internal/modules/payment/public"
	"onlinemenu.tr/internal/modules/payment/repo"
	"onlinemenu.tr/internal/platform/db"
)

// SalesSummaryService answers the pos report's payment-side questions. It
// lives in payment because payments and cash sessions are this module's
// tables; pos reaches it only through pub.SalesSummaryReader.
type SalesSummaryService struct {
	db          *db.Pool
	paymentRepo *repo.PaymentRepo
	sessions    *CashSessionService
	sessionRepo *repo.CashSessionRepo
}

// SalesSummaryParams groups fx-injected dependencies.
type SalesSummaryParams struct {
	fx.In

	DB          *db.Pool
	PaymentRepo *repo.PaymentRepo
	Sessions    *CashSessionService
	SessionRepo *repo.CashSessionRepo
}

func NewSalesSummaryService(p SalesSummaryParams) *SalesSummaryService {
	return &SalesSummaryService{
		db:          p.DB,
		paymentRepo: p.PaymentRepo,
		sessions:    p.Sessions,
		sessionRepo: p.SessionRepo,
	}
}

var _ pub.SalesSummaryReader = (*SalesSummaryService)(nil)

// PaymentTotalsByMethod groups payments created in [from, to) for one branch
// by (method, status). See PaymentRepo.TotalsByMethod for the exact filter
// and window semantics.
func (s *SalesSummaryService) PaymentTotalsByMethod(ctx context.Context, tenantID, branchID uuid.UUID, from, to time.Time) ([]pub.MethodTotal, error) {
	var rows []domain.MethodTotal
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		rows, err = s.paymentRepo.TotalsByMethod(ctx, tx, tenantID, branchID, from, to)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("payment/service: payment totals by method: %w", err)
	}

	totals := make([]pub.MethodTotal, len(rows))
	for i, r := range rows {
		totals[i] = pub.MethodTotal{
			Method: r.Method,
			Status: r.Status,
			Count:  r.Count,
			Total:  r.Total,
		}
	}
	return totals, nil
}

// CashSessionsInWindow lists sessions overlapping [from, to) for one branch,
// each with its live reconciliation figures computed by
// CashSessionService.buildView — the same computation the cash-session API
// uses for "right now", here applied to every session the window covers.
func (s *SalesSummaryService) CashSessionsInWindow(ctx context.Context, tenantID, branchID uuid.UUID, from, to time.Time) ([]pub.CashSessionSummary, error) {
	var summaries []pub.CashSessionSummary
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		sessions, err := s.sessionRepo.ListByBranchWindow(ctx, tx, tenantID, branchID, from, to)
		if err != nil {
			return err
		}
		summaries = make([]pub.CashSessionSummary, len(sessions))
		for i, session := range sessions {
			view, err := s.sessions.buildView(ctx, tx, session)
			if err != nil {
				return err
			}
			summaries[i] = pub.CashSessionSummary{
				ID:                   view.Session.ID,
				Status:               string(view.Session.Status),
				OpenedAt:             view.Session.OpenedAt,
				ClosedAt:             view.Session.ClosedAt,
				OpeningCountedAmount: view.Session.OpeningCountedAmount,
				CashPaymentsTaken:    view.CashPaymentsTaken,
				MovementsNet:         view.MovementsNet,
				ExpectedClose:        view.ExpectedClose,
				ClosingCountedAmount: view.Session.ClosingCountedAmount,
				Difference:           view.Difference,
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("payment/service: cash sessions in window: %w", err)
	}
	return summaries, nil
}
