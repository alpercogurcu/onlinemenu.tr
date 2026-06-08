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
func (r *CheckRepo) Create(ctx context.Context, tx pgx.Tx, c domain.Check) (domain.Check, error) {
	const q = `
		INSERT INTO checks (tenant_id, branch_id, table_label, status, opened_by, note)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, tenant_id, branch_id, table_label, status, opened_by,
		          closed_by, note, opened_at, closed_at, created_at, updated_at`

	row := tx.QueryRow(ctx, q,
		c.TenantID, c.BranchID, c.TableLabel, string(c.Status), c.OpenedBy, c.Note,
	)
	return scanCheck(row)
}

// GetByID returns a check visible to the current tenant context.
func (r *CheckRepo) GetByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (domain.Check, error) {
	const q = `
		SELECT id, tenant_id, branch_id, table_label, status, opened_by,
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

// List returns all checks visible to the current tenant (open first, then by opened_at desc).
func (r *CheckRepo) List(ctx context.Context, tx pgx.Tx) ([]domain.Check, error) {
	const q = `
		SELECT id, tenant_id, branch_id, table_label, status, opened_by,
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

// UpdateStatus transitions a check to a new status.
func (r *CheckRepo) UpdateStatus(ctx context.Context, tx pgx.Tx, id uuid.UUID, status domain.CheckStatus, closedBy *uuid.UUID) (domain.Check, error) {
	const q = `
		UPDATE checks SET status = $2, closed_by = $3,
		                  closed_at = CASE WHEN $2 IN ('closed','cancelled') THEN NOW() ELSE closed_at END,
		                  updated_at = NOW()
		WHERE id = $1
		RETURNING id, tenant_id, branch_id, table_label, status, opened_by,
		          closed_by, note, opened_at, closed_at, created_at, updated_at`

	c, err := scanCheck(tx.QueryRow(ctx, q, id, string(status), closedBy))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Check{}, ErrNotFound
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
		&c.ID, &c.TenantID, &c.BranchID, &c.TableLabel, &status, &c.OpenedBy,
		&c.ClosedBy, &c.Note, &c.OpenedAt, &c.ClosedAt, &c.CreatedAt, &c.UpdatedAt,
	); err != nil {
		return domain.Check{}, err
	}
	c.Status = domain.CheckStatus(status)
	return c, nil
}
