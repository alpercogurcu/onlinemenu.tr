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
		          reorder_point, unit, last_unit_cost, last_cost_currency, last_cost_source,
		          last_cost_at, updated_at`

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
		          reorder_point, unit, last_unit_cost, last_cost_currency, last_cost_source,
		          last_cost_at, updated_at`

	row := tx.QueryRow(ctx, q, tenantID, warehouseID, stockItemID, delta, unit)
	return scanLevel(row)
}

// GetByStockItem returns the current stock level for a stock item in a warehouse.
func (r *StockLevelRepo) GetByStockItem(ctx context.Context, tx pgx.Tx, warehouseID, stockItemID uuid.UUID) (domain.StockLevel, error) {
	const q = `
		SELECT id, tenant_id, warehouse_id, stock_item_id, on_hand, reserved, available,
		       reorder_point, unit, last_unit_cost, last_cost_currency, last_cost_source,
		       last_cost_at, updated_at
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
		       reorder_point, unit, last_unit_cost, last_cost_currency, last_cost_source,
		       last_cost_at, updated_at
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

// SetLastCost records the branch-local cost of a (warehouse, stock_item)
// pair (ADR-DATA-007). It is an UPDATE, not an upsert: the caller is expected
// to have already established the stock_levels row in the same transaction
// (e.g. via AdjustOnHand) before recording its cost.
func (r *StockLevelRepo) SetLastCost(ctx context.Context, tx pgx.Tx, tenantID, warehouseID, stockItemID uuid.UUID, unitCost float64, currency string, source domain.CostSource) error {
	const q = `
		UPDATE stock_levels SET
			last_unit_cost     = $1,
			last_cost_currency = $2,
			last_cost_source   = $3,
			last_cost_at       = NOW(),
			updated_at         = NOW()
		WHERE tenant_id = $4 AND warehouse_id = $5 AND stock_item_id = $6`

	tag, err := tx.Exec(ctx, q, unitCost, currency, string(source), tenantID, warehouseID, stockItemID)
	if err != nil {
		return fmt.Errorf("inventory/repo: set last cost: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func scanLevel(s pgx.Row) (domain.StockLevel, error) {
	var l domain.StockLevel
	err := s.Scan(
		&l.ID, &l.TenantID, &l.WarehouseID, &l.StockItemID,
		&l.OnHand, &l.Reserved, &l.Available,
		&l.ReorderPoint, &l.Unit,
		&l.LastUnitCost, &l.LastCostCurrency, &l.LastCostSource, &l.LastCostAt,
		&l.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.StockLevel{}, ErrNotFound
		}
		return domain.StockLevel{}, fmt.Errorf("inventory/repo: scan level: %w", err)
	}
	return l, nil
}
