package repo

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"onlinemenu.tr/internal/modules/inventory/domain"
)

// WarehouseRepo manages warehouses persistence.
type WarehouseRepo struct{}

// NewWarehouseRepo constructs a WarehouseRepo for fx injection.
func NewWarehouseRepo() *WarehouseRepo { return &WarehouseRepo{} }

// Create inserts a new warehouse and returns the persisted record.
func (r *WarehouseRepo) Create(ctx context.Context, tx pgx.Tx, w domain.Warehouse) (domain.Warehouse, error) {
	const q = `
		INSERT INTO warehouses (tenant_id, branch_id, name, warehouse_type, is_active)
		VALUES ($1,$2,$3,$4,$5)
		RETURNING id, tenant_id, branch_id, name, warehouse_type, is_active, created_at, updated_at`

	row := tx.QueryRow(ctx, q, w.TenantID, w.BranchID, w.Name, string(w.WarehouseType), w.IsActive)
	return scanWarehouse(row)
}

// GetByID fetches a single warehouse by primary key within the RLS tenant context.
func (r *WarehouseRepo) GetByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (domain.Warehouse, error) {
	const q = `
		SELECT id, tenant_id, branch_id, name, warehouse_type, is_active, created_at, updated_at
		FROM warehouses WHERE id = $1`

	row := tx.QueryRow(ctx, q, id)
	w, err := scanWarehouse(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Warehouse{}, ErrNotFound
		}
		return domain.Warehouse{}, fmt.Errorf("inventory/repo/warehouse: get by id: %w", err)
	}
	return w, nil
}

// List returns all active warehouses visible to the current RLS tenant context,
// optionally filtered by branch (uuid.Nil branchID = no filter).
func (r *WarehouseRepo) List(ctx context.Context, tx pgx.Tx, branchID uuid.UUID) ([]domain.Warehouse, error) {
	const q = `
		SELECT id, tenant_id, branch_id, name, warehouse_type, is_active, created_at, updated_at
		FROM warehouses
		WHERE is_active = true AND ($1 = '00000000-0000-0000-0000-000000000000'::uuid OR branch_id = $1)
		ORDER BY name`

	rows, err := tx.Query(ctx, q, branchID)
	if err != nil {
		return nil, fmt.Errorf("inventory/repo/warehouse: list: %w", err)
	}
	defer rows.Close()

	var out []domain.Warehouse
	for rows.Next() {
		w, err := scanWarehouse(rows)
		if err != nil {
			return nil, fmt.Errorf("inventory/repo/warehouse: list scan: %w", err)
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// Update persists mutable warehouse field changes.
func (r *WarehouseRepo) Update(ctx context.Context, tx pgx.Tx, w domain.Warehouse) (domain.Warehouse, error) {
	const q = `
		UPDATE warehouses SET
			branch_id = $1, name = $2, warehouse_type = $3, is_active = $4, updated_at = NOW()
		WHERE id = $5
		RETURNING id, tenant_id, branch_id, name, warehouse_type, is_active, created_at, updated_at`

	row := tx.QueryRow(ctx, q, w.BranchID, w.Name, string(w.WarehouseType), w.IsActive, w.ID)
	updated, err := scanWarehouse(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Warehouse{}, ErrNotFound
		}
		return domain.Warehouse{}, fmt.Errorf("inventory/repo/warehouse: update: %w", err)
	}
	return updated, nil
}

// Delete marks a warehouse as inactive (soft delete).
func (r *WarehouseRepo) Delete(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	const q = `UPDATE warehouses SET is_active = false, updated_at = NOW() WHERE id = $1`
	tag, err := tx.Exec(ctx, q, id)
	if err != nil {
		return fmt.Errorf("inventory/repo/warehouse: delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func scanWarehouse(s pgx.Row) (domain.Warehouse, error) {
	var w domain.Warehouse
	var whType string
	err := s.Scan(&w.ID, &w.TenantID, &w.BranchID, &w.Name, &whType, &w.IsActive, &w.CreatedAt, &w.UpdatedAt)
	if err != nil {
		return domain.Warehouse{}, err
	}
	w.WarehouseType = domain.WarehouseType(whType)
	return w, nil
}
