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
	tableRepo  *repo.TableRepo
	saleReader paymentpub.SaleReader
	logger     *zap.Logger
}

// CheckParams groups fx-injected dependencies.
type CheckParams struct {
	fx.In

	DB         *db.Pool
	CheckRepo  *repo.CheckRepo
	TableRepo  *repo.TableRepo
	SaleReader paymentpub.SaleReader
	Logger     *zap.Logger
}

func NewCheckService(p CheckParams) *CheckService {
	return &CheckService{
		db:         p.DB,
		checkRepo:  p.CheckRepo,
		tableRepo:  p.TableRepo,
		saleReader: p.SaleReader,
		logger:     p.Logger,
	}
}

// Open creates a new check for the given branch. The acting principal must
// belong to the requested branch_id (ADR-AUTH-001 layer 3 / security
// sprint); there is no persisted entity yet at this point, so the
// client-supplied branch_id is what gets validated.
//
// When c.TableID is set (dine-in via the floor plan), the referenced table
// row is locked (TableRepo.GetTableForUpdate) inside this same write
// transaction before the check is created. That row lock — not a unique
// index alone — is what makes two concurrent Open calls against the same
// table resolve to exactly one success: the second caller blocks on the
// lock, then observes the table already "occupied" and returns
// pub.ErrTableOccupied (409). c.TableLabel is overwritten from the table's
// name regardless of any client-supplied value, so receipts/KDS keep
// rendering a consistent label. c.TableID is left nil for masasız satış
// (takeaway/delivery) checks, which never touch a table row.
//
// c.Pax (guest count) defaults to 1 when the caller supplies 0 or a
// negative value. This is the single choke point every Open caller (HTTP
// handler, e2e spine, service integration tests) goes through, so the
// default lives here rather than at the HTTP layer or relying on the
// column's DB DEFAULT — Create's INSERT lists pax explicitly, so a
// zero-value Go int would otherwise write 0, not fall back to the column
// default.
func (s *CheckService) Open(ctx context.Context, tenantID uuid.UUID, principal auth.Principal, c domain.Check) (domain.Check, error) {
	if err := requireBranch(ctx, principal, c.BranchID); err != nil {
		return domain.Check{}, err
	}
	if c.Pax < 1 {
		c.Pax = 1
	}
	c.TenantID = tenantID
	c.Status = domain.CheckStatusOpen
	var created domain.Check
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		if c.TableID != nil {
			table, err := s.tableRepo.GetTableForUpdate(ctx, tx, *c.TableID)
			if err != nil {
				return err
			}
			if table.BranchID != c.BranchID {
				return pub.ErrTableBranchMismatch
			}
			if table.Status != domain.TableStatusEmpty && table.Status != domain.TableStatusReserved {
				return pub.ErrTableOccupied
			}
			if _, err := s.tableRepo.UpdateStatus(ctx, tx, table.ID, domain.TableStatusOccupied, table.Status); err != nil {
				if errors.Is(err, repo.ErrInvalidTransition) {
					return pub.ErrTableOccupied
				}
				return err
			}
			c.TableLabel = table.Name
		}

		var err error
		created, err = s.checkRepo.Create(ctx, tx, c)
		if err != nil {
			return err
		}
		return repo.InsertOutbox(ctx, tx, tenantID, "check", created.ID.String(), "check.opened", map[string]any{
			"tenant_id":   tenantID,
			"check_id":    created.ID,
			"branch_id":   created.BranchID,
			"table_id":    created.TableID,
			"table_label": created.TableLabel,
			"opened_by":   created.OpenedBy,
		})
	})
	if err != nil {
		if errors.Is(err, pub.ErrTableOccupied) || errors.Is(err, pub.ErrTableBranchMismatch) {
			return domain.Check{}, err
		}
		if errors.Is(err, repo.ErrTableOccupied) {
			// Backstop path: the table row lock saw "empty"/"reserved" (its
			// status was manually reset while another check still held it),
			// but checks_open_table_id_uidx caught the still-live check —
			// see CheckRepo.Create's doc comment.
			return domain.Check{}, pub.ErrTableOccupied
		}
		if errors.Is(err, repo.ErrNotFound) {
			return domain.Check{}, pub.ErrNotFound
		}
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

// GetByIDWithTotal is GetByID plus the check's current bill total (kurus —
// CheckRepo.GetTotal, rejected/cancelled order items excluded). It exists
// alongside the plain GetByID rather than replacing it because GetByID's
// signature is depended on by GetPublic (cross-module pub.Check projection
// consumed by payment) and ws/hub.go's table-label lookup — neither needs
// nor should carry the extra query. Both reads happen in the same
// tenant-scoped transaction so the total reflects the same RLS-visible
// snapshot as the check row.
func (s *CheckService) GetByIDWithTotal(ctx context.Context, tenantID, checkID uuid.UUID) (domain.Check, int64, error) {
	var c domain.Check
	var total int64
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		c, err = s.checkRepo.GetByID(ctx, tx, checkID)
		if err != nil {
			return err
		}
		total, err = s.checkRepo.GetTotal(ctx, tx, checkID)
		return err
	})
	if err != nil {
		return domain.Check{}, 0, wrapErr(err, "pos/service/check: get by id with total: %w")
	}
	return c, total, nil
}

// CheckListFilter narrows CheckService.List's result set. Both fields are
// optional (nil = no filter on that column), mirroring ZonePatch/TablePatch's
// pointer-field "not supplied" convention. Validation (is Status a known
// value, is BranchID a well-formed uuid) is the HTTP layer's job — see
// Handler.listChecks — this type only carries already-validated values.
type CheckListFilter struct {
	Status   *domain.CheckStatus
	BranchID *uuid.UUID
}

// List returns the tenant's checks, optionally narrowed by filter (status
// and/or branch_id — both optional, see CheckListFilter). branch_id here is
// a convenience filter, not an isolation boundary: unlike the write paths
// (Open/Close/Cancel), this read endpoint does not enforce that the acting
// principal belongs to the requested branch — RLS (tenant scope) already
// bounds visibility, and a branch-scoped principal calling without a
// branch_id filter simply sees every branch's checks, same as before this
// filter existed.
// List's second return value is each returned check's current bill total
// (kurus, keyed by check ID — CheckRepo.GetTotal's rejected/cancelled
// exclusion applies identically), computed via CheckRepo.TotalsByCheckIDs in
// the same tenant-scoped read transaction as the check rows themselves —
// one batch query for the whole page, not one GetTotal call per check
// (N+1). A check absent from the map (no active orders) has a total of 0;
// callers must treat a missing key that way, not as an error.
func (s *CheckService) List(ctx context.Context, tenantID uuid.UUID, filter CheckListFilter) ([]domain.Check, map[uuid.UUID]int64, error) {
	var checks []domain.Check
	var totals map[uuid.UUID]int64
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		checks, err = s.checkRepo.List(ctx, tx, repo.ListFilter{
			Status:   filter.Status,
			BranchID: filter.BranchID,
		})
		if err != nil {
			return err
		}
		ids := make([]uuid.UUID, len(checks))
		for i, c := range checks {
			ids[i] = c.ID
		}
		totals, err = s.checkRepo.TotalsByCheckIDs(ctx, tx, ids)
		return err
	})
	if err != nil {
		return nil, nil, fmt.Errorf("pos/service/check: list: %w", err)
	}
	return checks, totals, nil
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
		if err := s.releaseTableToCleaning(ctx, tx, current.TableID); err != nil {
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
		if err := s.releaseTableToCleaning(ctx, tx, current.TableID); err != nil {
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

// releaseTableToCleaning moves a check's table to "cleaning" after the check
// is closed/cancelled, tolerating the no-op case. It is a no-op when
// tableID is nil (masasız satış checks never touched a table). The update
// is guarded on the table's expected current status ("occupied" ->
// "cleaning") but, unlike CheckRepo.UpdateStatus, a 0-rows-affected outcome
// is NOT an error: if staff had already manually reset the table's status
// in the meantime (TableService.SetStatus), the check close/cancel that
// triggered this call must still succeed — closing the adisyon takes
// priority over keeping the floor plan's derived state perfectly in sync.
func (s *CheckService) releaseTableToCleaning(ctx context.Context, tx pgx.Tx, tableID *uuid.UUID) error {
	if tableID == nil {
		return nil
	}
	_, err := s.tableRepo.UpdateStatusIfCurrent(ctx, tx, *tableID, domain.TableStatusCleaning, domain.TableStatusOccupied)
	if err != nil {
		return fmt.Errorf("pos/service/check: release table to cleaning: %w", err)
	}
	return nil
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
