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
	"onlinemenu.tr/internal/platform/auth"
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

// Open creates a new check for the given branch. The acting principal must
// belong to the requested branch_id (ADR-AUTH-001 layer 3 / security
// sprint); there is no persisted entity yet at this point, so the
// client-supplied branch_id is what gets validated.
func (s *CheckService) Open(ctx context.Context, tenantID uuid.UUID, principal auth.Principal, c domain.Check) (domain.Check, error) {
	if err := requireBranch(ctx, principal, c.BranchID); err != nil {
		return domain.Check{}, err
	}
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
//
// The check row is locked (GetForUpdate) before the open-status check. That
// lock — not the UpdateStatus guard alone — is what makes two concurrent
// Close calls emit exactly one check.closed event: both could otherwise read
// "open" before either had updated it. The second caller blocks on the lock,
// then observes the already-closed status and returns ErrInvalidTransition.
//
// Known residual race (out of scope for this fix): the payment total is read
// via SaleReader in a separate transaction before the lock is acquired, so a
// payment arriving between that read and the lock is not reflected in this
// Close call. This addresses double-close, not that TOCTOU window.
//
// The acting principal must belong to the check's branch (ADR-AUTH-001 layer
// 3 / security sprint) — checked right after the row is locked and loaded,
// but BEFORE the status/transition check, so a branch-forbidden caller gets
// 403 rather than a 409 that would otherwise leak the check's current status.
func (s *CheckService) Close(ctx context.Context, tenantID uuid.UUID, principal auth.Principal, checkID, closedBy uuid.UUID) (domain.Check, error) {
	// SaleReader manages its own transaction; call outside the write tx.
	paid, err := s.saleReader.TotalPaidForCheck(ctx, tenantID, checkID)
	if err != nil {
		return domain.Check{}, fmt.Errorf("pos/service/check: close: read payment total: %w", err)
	}

	var closed domain.Check
	err = s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		current, err := s.checkRepo.GetForUpdate(ctx, tx, checkID)
		if err != nil {
			return err
		}
		if err := requireBranch(ctx, principal, current.BranchID); err != nil {
			return err
		}
		if current.Status != domain.CheckStatusOpen {
			return repo.ErrInvalidTransition
		}

		total, err := s.checkRepo.GetTotal(ctx, tx, checkID)
		if err != nil {
			return err
		}
		if paid < total {
			return ErrInsufficientPayment
		}

		closed, err = s.checkRepo.UpdateStatus(ctx, tx, checkID, domain.CheckStatusClosed, domain.CheckStatusOpen, &closedBy)
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

// Cancel cancels an open check. Like Close, the row lock (GetForUpdate) is
// what serializes concurrent Cancel/Close attempts against the same check.
// The acting principal must belong to the check's branch (ADR-AUTH-001 layer
// 3 / security sprint) — checked right after loading, before the
// status/transition check (see Close for the 403-vs-409 rationale).
func (s *CheckService) Cancel(ctx context.Context, tenantID uuid.UUID, principal auth.Principal, checkID, cancelledBy uuid.UUID) (domain.Check, error) {
	var cancelled domain.Check
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		current, err := s.checkRepo.GetForUpdate(ctx, tx, checkID)
		if err != nil {
			return err
		}
		if err := requireBranch(ctx, principal, current.BranchID); err != nil {
			return err
		}
		if current.Status != domain.CheckStatusOpen {
			return repo.ErrInvalidTransition
		}

		cancelled, err = s.checkRepo.UpdateStatus(ctx, tx, checkID, domain.CheckStatusCancelled, domain.CheckStatusOpen, &cancelledBy)
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

// wrapErr maps repo/domain sentinel errors to their pub equivalents so HTTP
// handlers can translate them (404 for not-found, 409 for invalid transitions);
// anything else is wrapped with operation context via format.
func wrapErr(err error, format string) error {
	if errors.Is(err, repo.ErrNotFound) {
		return pub.ErrNotFound
	}
	if errors.Is(err, repo.ErrInvalidTransition) || errors.Is(err, domain.ErrInvalidTransition) {
		return pub.ErrInvalidTransition
	}
	return fmt.Errorf(format, err)
}
