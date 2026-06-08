package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/fx"
	"go.uber.org/zap"

	paymentpub "onlinemenu.tr/internal/modules/payment/public"
	"onlinemenu.tr/internal/modules/pos/domain"
	pub "onlinemenu.tr/internal/modules/pos/public"
	"onlinemenu.tr/internal/modules/pos/repo"
	"onlinemenu.tr/internal/platform/db"
)

// ErrInsufficientPayment is returned when the total paid is less than the check total.
var ErrInsufficientPayment = errors.New("pos/service/check: payment insufficient to close check")

// CheckService manages dine-in check (adisyon) lifecycle.
type CheckService struct {
	db         *db.Pool
	checkRepo  *repo.CheckRepo
	saleReader paymentpub.SaleReader
	logger     *zap.Logger
}

// CheckParams groups fx-injected dependencies.
type CheckParams struct {
	fx.In

	DB         *db.Pool
	CheckRepo  *repo.CheckRepo
	SaleReader paymentpub.SaleReader
	Logger     *zap.Logger
}

func NewCheckService(p CheckParams) *CheckService {
	return &CheckService{
		db:         p.DB,
		checkRepo:  p.CheckRepo,
		saleReader: p.SaleReader,
		logger:     p.Logger,
	}
}

func (s *CheckService) Open(ctx context.Context, tenantID uuid.UUID, c domain.Check) (domain.Check, error) {
	c.TenantID = tenantID
	c.Status = domain.CheckStatusOpen
	var created domain.Check
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		created, err = s.checkRepo.Create(ctx, tx, c)
		if err != nil {
			return err
		}
		return repo.InsertOutbox(ctx, tx, tenantID, "check", created.ID.String(), "check.opened", map[string]any{
			"tenant_id":   tenantID,
			"check_id":    created.ID,
			"branch_id":   created.BranchID,
			"table_label": created.TableLabel,
			"opened_by":   created.OpenedBy,
		})
	})
	if err != nil {
		return domain.Check{}, fmt.Errorf("pos/service/check: open: %w", err)
	}
	return created, nil
}

func (s *CheckService) GetByID(ctx context.Context, tenantID, checkID uuid.UUID) (domain.Check, error) {
	var c domain.Check
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		c, err = s.checkRepo.GetByID(ctx, tx, checkID)
		return err
	})
	if err != nil {
		return domain.Check{}, wrapErr(err, "pos/service/check: get by id: %w")
	}
	return c, nil
}

func (s *CheckService) List(ctx context.Context, tenantID uuid.UUID) ([]domain.Check, error) {
	var checks []domain.Check
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		checks, err = s.checkRepo.List(ctx, tx)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("pos/service/check: list: %w", err)
	}
	return checks, nil
}

// Close closes a check after verifying total payments cover the check total.
// Returns ErrInsufficientPayment if the paid amount is less than the order total.
func (s *CheckService) Close(ctx context.Context, tenantID, checkID, closedBy uuid.UUID) (domain.Check, error) {
	// SaleReader manages its own transaction; call outside the write tx.
	paid, err := s.saleReader.TotalPaidForCheck(ctx, tenantID, checkID)
	if err != nil {
		return domain.Check{}, fmt.Errorf("pos/service/check: close: read payment total: %w", err)
	}

	var closed domain.Check
	err = s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		total, err := s.checkRepo.GetTotal(ctx, tx, checkID)
		if err != nil {
			return err
		}
		if paid < total {
			return ErrInsufficientPayment
		}

		closed, err = s.checkRepo.UpdateStatus(ctx, tx, checkID, domain.CheckStatusClosed, &closedBy)
		if err != nil {
			return err
		}
		return repo.InsertOutbox(ctx, tx, tenantID, "check", checkID.String(), "check.closed", map[string]any{
			"tenant_id": tenantID,
			"check_id":  checkID,
			"closed_by": closedBy,
		})
	})
	if err != nil {
		return domain.Check{}, wrapErr(err, "pos/service/check: close: %w")
	}
	return closed, nil
}

func (s *CheckService) Cancel(ctx context.Context, tenantID, checkID, cancelledBy uuid.UUID) (domain.Check, error) {
	var cancelled domain.Check
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		cancelled, err = s.checkRepo.UpdateStatus(ctx, tx, checkID, domain.CheckStatusCancelled, &cancelledBy)
		if err != nil {
			return err
		}
		return repo.InsertOutbox(ctx, tx, tenantID, "check", checkID.String(), "check.cancelled", map[string]any{
			"tenant_id":    tenantID,
			"check_id":     checkID,
			"cancelled_by": cancelledBy,
		})
	})
	if err != nil {
		return domain.Check{}, wrapErr(err, "pos/service/check: cancel: %w")
	}
	return cancelled, nil
}

// GetPublic returns a cross-module projection of a check.
func (s *CheckService) GetPublic(ctx context.Context, tenantID, checkID uuid.UUID) (pub.Check, error) {
	c, err := s.GetByID(ctx, tenantID, checkID)
	if err != nil {
		return pub.Check{}, err
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

func wrapErr(err error, format string) error {
	if errors.Is(err, repo.ErrNotFound) {
		return pub.ErrNotFound
	}
	return fmt.Errorf(format, err)
}
