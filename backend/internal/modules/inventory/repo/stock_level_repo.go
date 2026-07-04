package repo

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"onlinemenu.tr/internal/modules/inventory/domain"
)

// StockLevelRepo manages stock_levels persistence (warehouse+stock_item scoped).
type StockLevelRepo struct{}

// NewStockLevelRepo constructs a StockLevelRepo for fx injection.
func NewStockLevelRepo() *StockLevelRepo { return &StockLevelRepo{} }

// AdjustOnHand applies a signed delta to a stock item's on_hand quantity in a
// warehouse, atomically. Creates the level record if it does not exist
// (initial delta becomes on_hand). on_hand is clamped to zero rather than
// going negative.
func (r *StockLevelRepo) AdjustOnHand(ctx context.Context, tx pgx.Tx, tenantID, warehouseID, stockItemID uuid.UUID, delta float64, unit string) (domain.StockLevel, error) {
	const q = `
		INSERT INTO stock_levels (tenant_id, warehouse_id, stock_item_id, on_hand, unit)
		VALUES ($1, $2, $3, GREATEST(0, $4::numeric), $5)
		ON CONFLICT (tenant_id, warehouse_id, stock_item_id)
		DO UPDATE SET
			on_hand    = GREATEST(0, stock_levels.on_hand + $4::numeric),
			updated_at = NOW()
		RETURNING id, tenant_id, warehouse_id, stock_item_id, on_hand, reserved, available,
		          reorder_point, unit, updated_at`

	row := tx.QueryRow(ctx, q, tenantID, warehouseID, stockItemID, delta, unit)
	return scanLevel(row)
}

// AdjustReserved applies a signed delta to a stock item's reserved quantity in
// a warehouse (reserve/release movements). Reserved is clamped to zero.
func (r *StockLevelRepo) AdjustReserved(ctx context.Context, tx pgx.Tx, tenantID, warehouseID, stockItemID uuid.UUID, delta float64, unit string) (domain.StockLevel, error) {
	const q = `
		INSERT INTO stock_levels (tenant_id, warehouse_id, stock_item_id, reserved, unit)
		VALUES ($1, $2, $3, GREATEST(0, $4::numeric), $5)
		ON CONFLICT (tenant_id, warehouse_id, stock_item_id)
		DO UPDATE SET
			reserved   = GREATEST(0, stock_levels.reserved + $4::numeric),
			updated_at = NOW()
		RETURNING id, tenant_id, warehouse_id, stock_item_id, on_hand, reserved, available,
		          reorder_point, unit, updated_at`

	row := tx.QueryRow(ctx, q, tenantID, warehouseID, stockItemID, delta, unit)
	return scanLevel(row)
}

// GetByStockItem returns the current stock level for a stock item in a warehouse.
func (r *StockLevelRepo) GetByStockItem(ctx context.Context, tx pgx.Tx, warehouseID, stockItemID uuid.UUID) (domain.StockLevel, error) {
	const q = `
		SELECT id, tenant_id, warehouse_id, stock_item_id, on_hand, reserved, available,
		       reorder_point, unit, updated_at
		FROM stock_levels
		WHERE warehouse_id = $1 AND stock_item_id = $2`

	row := tx.QueryRow(ctx, q, warehouseID, stockItemID)
	l, err := scanLevel(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.StockLevel{}, ErrNotFound
		}
		return domain.StockLevel{}, fmt.Errorf("inventory/repo: get level: %w", err)
	}
	return l, nil
}

// ListByWarehouse returns all stock levels for a warehouse.
func (r *StockLevelRepo) ListByWarehouse(ctx context.Context, tx pgx.Tx, warehouseID uuid.UUID) ([]domain.StockLevel, error) {
	const q = `
		SELECT id, tenant_id, warehouse_id, stock_item_id, on_hand, reserved, available,
		       reorder_point, unit, updated_at
		FROM stock_levels
		WHERE warehouse_id = $1
		ORDER BY stock_item_id`

	rows, err := tx.Query(ctx, q, warehouseID)
	if err != nil {
		return nil, fmt.Errorf("inventory/repo: list by warehouse: %w", err)
	}
	defer rows.Close()

	var levels []domain.StockLevel
	for rows.Next() {
		l, err := scanLevel(rows)
		if err != nil {
			return nil, fmt.Errorf("inventory/repo: list by warehouse scan: %w", err)
		}
		levels = append(levels, l)
	}
	return levels, rows.Err()
}

func scanLevel(s pgx.Row) (domain.StockLevel, error) {
	var l domain.StockLevel
	err := s.Scan(
		&l.ID, &l.TenantID, &l.WarehouseID, &l.StockItemID,
		&l.OnHand, &l.Reserved, &l.Available,
		&l.ReorderPoint, &l.Unit, &l.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.StockLevel{}, ErrNotFound
		}
		return domain.StockLevel{}, fmt.Errorf("inventory/repo: scan level: %w", err)
	}
	return l, nil
}
