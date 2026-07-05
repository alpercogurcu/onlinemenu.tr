package repo

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"onlinemenu.tr/internal/modules/pos/domain"
)

// CheckRepo manages dine-in check (adisyon) persistence.
type CheckRepo struct{}

func NewCheckRepo() *CheckRepo { return &CheckRepo{} }

// Create inserts a new open check and returns it with server-assigned fields.
//
// A unique_violation on checks_open_table_id_uidx is translated to
// ErrTableOccupied rather than left as a raw pg error: it can only happen
// when a table's status was manually reset to empty/reserved
// (TableService.SetStatus) while some other check still held it open — a
// state CheckService.Open's row lock cannot observe, since the lock is on
// the table row, not on "does any check already reference this table".
func (r *CheckRepo) Create(ctx context.Context, tx pgx.Tx, c domain.Check) (domain.Check, error) {
	const q = `
		INSERT INTO checks (tenant_id, branch_id, table_id, table_label, status, opened_by, note)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, tenant_id, branch_id, table_id, table_label, status, opened_by,
		          closed_by, note, opened_at, closed_at, created_at, updated_at`

	row := tx.QueryRow(ctx, q,
		c.TenantID, c.BranchID, c.TableID, c.TableLabel, string(c.Status), c.OpenedBy, c.Note,
	)
	created, err := scanCheck(row)
	if err != nil {
		if isUniqueViolation(err) {
			return domain.Check{}, ErrTableOccupied
		}
		return domain.Check{}, err
	}
	return created, nil
}

// GetByID returns a check visible to the current tenant context.
func (r *CheckRepo) GetByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (domain.Check, error) {
	const q = `
		SELECT id, tenant_id, branch_id, table_id, table_label, status, opened_by,
		       closed_by, note, opened_at, closed_at, created_at, updated_at
		FROM checks WHERE id = $1`

	c, err := scanCheck(tx.QueryRow(ctx, q, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Check{}, ErrNotFound
		}
		return domain.Check{}, fmt.Errorf("pos/repo/check: get by id: %w", err)
	}
	return c, nil
}

// GetForUpdate locks the check row for the duration of the caller's
// transaction. This is what actually prevents two concurrent Close/Cancel
// calls from both observing "open" and both emitting an outbox event: the
// second caller blocks here until the first commits or rolls back, then
// observes the already-updated status.
func (r *CheckRepo) GetForUpdate(ctx context.Context, tx pgx.Tx, id uuid.UUID) (domain.Check, error) {
	const q = `
		SELECT id, tenant_id, branch_id, table_id, table_label, status, opened_by,
		       closed_by, note, opened_at, closed_at, created_at, updated_at
		FROM checks WHERE id = $1 FOR UPDATE`

	c, err := scanCheck(tx.QueryRow(ctx, q, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Check{}, ErrNotFound
		}
		return domain.Check{}, fmt.Errorf("pos/repo/check: get for update: %w", err)
	}
	return c, nil
}

// List returns all checks visible to the current tenant (open first, then by opened_at desc).
func (r *CheckRepo) List(ctx context.Context, tx pgx.Tx) ([]domain.Check, error) {
	const q = `
		SELECT id, tenant_id, branch_id, table_id, table_label, status, opened_by,
		       closed_by, note, opened_at, closed_at, created_at, updated_at
		FROM checks
		ORDER BY CASE status WHEN 'open' THEN 0 ELSE 1 END, opened_at DESC`

	rows, err := tx.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("pos/repo/check: list: %w", err)
	}
	defer rows.Close()

	var out []domain.Check
	for rows.Next() {
		c, err := scanCheck(rows)
		if err != nil {
			return nil, fmt.Errorf("pos/repo/check: list scan: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetTotal returns the sum of all order items (quantity × unit_price_amount)
// for a check, counting only orders whose status is not one of
// domain.InactiveOrderStatuses (rejected/cancelled). A rejected or cancelled
// order's items must never be billed to the customer — see that variable's
// doc comment. Returns 0 if the check has no active orders.
func (r *CheckRepo) GetTotal(ctx context.Context, tx pgx.Tx, checkID uuid.UUID) (int64, error) {
	excluded := make([]string, len(domain.InactiveOrderStatuses))
	for i, s := range domain.InactiveOrderStatuses {
		excluded[i] = string(s)
	}

	var total int64
	err := tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(oi.quantity * oi.unit_price_amount), 0)
		FROM orders o
		JOIN order_items oi ON oi.order_id = o.id
		WHERE o.check_id = $1 AND o.status <> ALL($2::text[])
	`, checkID, excluded).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("pos/repo/check: get total: %w", err)
	}
	return total, nil
}

// UpdateStatus transitions a check to a new status, guarded on its expected
// current status. Returns ErrInvalidTransition if the row's status no longer
// matches expectedStatus (0 rows affected). Callers should pair this with a
// preceding GetForUpdate in the same transaction: the row lock is what makes
// concurrent Close/Cancel calls serialize (only one observes "open"), while
// this guard is a defense-in-depth check against the expected status.
func (r *CheckRepo) UpdateStatus(ctx context.Context, tx pgx.Tx, id uuid.UUID, status, expectedStatus domain.CheckStatus, closedBy *uuid.UUID) (domain.Check, error) {
	const q = `
		UPDATE checks SET status = $2, closed_by = $4,
		                  closed_at = CASE WHEN $2 IN ('closed','cancelled') THEN NOW() ELSE closed_at END,
		                  updated_at = NOW()
		WHERE id = $1 AND status = $3
		RETURNING id, tenant_id, branch_id, table_id, table_label, status, opened_by,
		          closed_by, note, opened_at, closed_at, created_at, updated_at`

	c, err := scanCheck(tx.QueryRow(ctx, q, id, string(status), string(expectedStatus), closedBy))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Check{}, ErrInvalidTransition
		}
		return domain.Check{}, fmt.Errorf("pos/repo/check: update status: %w", err)
	}
	return c, nil
}

// scanCheck reads one check row from any RowScanner (QueryRow or rows).
func scanCheck(s interface {
	Scan(...any) error
}) (domain.Check, error) {
	var c domain.Check
	var status string
	if err := s.Scan(
		&c.ID, &c.TenantID, &c.BranchID, &c.TableID, &c.TableLabel, &status, &c.OpenedBy,
		&c.ClosedBy, &c.Note, &c.OpenedAt, &c.ClosedAt, &c.CreatedAt, &c.UpdatedAt,
	); err != nil {
		return domain.Check{}, err
	}
	c.Status = domain.CheckStatus(status)
	return c, nil
}
