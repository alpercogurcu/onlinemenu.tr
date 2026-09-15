package service

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/fx"

	"onlinemenu.tr/internal/modules/pos/domain"
	pub "onlinemenu.tr/internal/modules/pos/public"
	"onlinemenu.tr/internal/modules/pos/repo"
	"onlinemenu.tr/internal/platform/db"
)

// CheckReadService is the check surface other modules consume: the pub.Check
// projection and the pub.CheckWriteGuard verdict.
//
// It exists apart from CheckService, whose reads it duplicates, because of a
// dependency cycle that is otherwise unavoidable. CheckService needs
// payment/public.SaleReader to decide whether a check may close; the payment
// service needs pub.CheckWriteGuard to decide whether a sale may be
// registered. Binding the guard to CheckService would make the fx graph
// PaymentService → CheckWriteGuard → CheckService → SaleReader →
// PaymentService and the app would refuse to start. This type depends on
// nothing but the pool and the check repo, so the cycle never forms — and the
// narrower surface is what a cross-module consumer should see anyway.
type CheckReadService struct {
	db        *db.Pool
	checkRepo *repo.CheckRepo
}

// CheckReadParams groups fx-injected dependencies.
type CheckReadParams struct {
	fx.In

	DB        *db.Pool
	CheckRepo *repo.CheckRepo
}

func NewCheckReadService(p CheckReadParams) *CheckReadService {
	return &CheckReadService{db: p.DB, checkRepo: p.CheckRepo}
}

// GetByID returns a cross-module projection of a check (pub.CheckReader).
func (s *CheckReadService) GetByID(ctx context.Context, tenantID, checkID uuid.UUID) (pub.Check, error) {
	var c domain.Check
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		c, err = s.checkRepo.GetByID(ctx, tx, checkID)
		return err
	})
	if err != nil {
		return pub.Check{}, wrapErr(err, "pos/service/check-read: get by id: %w")
	}
	return pub.Check{
		ID:         c.ID,
		TenantID:   c.TenantID,
		BranchID:   c.BranchID,
		TableLabel: c.TableLabel,
		Status:     c.Status,
		OpenedAt:   c.OpenedAt,
	}, nil
}

// AssertCheckWritable implements pub.CheckWriteGuard.
//
// The read is deliberately NOT locked: the caller runs in its own module's
// transaction, so a lock taken here would be released before that transaction
// writes anything. The residual TOCTOU window (a cashier closes the check
// between this verdict and the caller's insert) is the same one
// CheckService.Close already documents for its payment totals, and it is
// orders of magnitude smaller than the "never checked at all" behaviour this
// replaces. In-module callers that already hold a transaction (see
// OrderService.Place) take the row lock themselves instead of coming through
// here.
func (s *CheckReadService) AssertCheckWritable(ctx context.Context, tenantID, checkID, branchID uuid.UUID) error {
	var c domain.Check
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		c, err = s.checkRepo.GetByID(ctx, tx, checkID)
		return err
	})
	if err != nil {
		return wrapErr(err, "pos/service/check-read: assert writable: %w")
	}
	return assertCheckWritable(c, branchID)
}

var (
	_ pub.CheckReader     = (*CheckReadService)(nil)
	_ pub.CheckWriteGuard = (*CheckReadService)(nil)
)

// assertCheckWritable decides whether anything may still be attached to c.
//
// Branch is compared before status so a caller probing another branch's check
// is told only that the check is not theirs, never whether it is still open —
// the same "do not leak state to someone with no business acting on it"
// reasoning requireBranch's doc comment gives for ordering 403 before 409.
//
// branchID uuid.Nil means "caller named no branch"; the comparison is skipped
// rather than treated as a mismatch, because every branch column involved is
// NOT NULL and a Nil here can only mean the caller had nothing to compare.
func assertCheckWritable(c domain.Check, branchID uuid.UUID) error {
	if branchID != uuid.Nil && c.BranchID != branchID {
		return pub.ErrCheckBranchMismatch
	}
	if c.Status != domain.CheckStatusOpen {
		return pub.ErrCheckNotOpen
	}
	return nil
}
