package repo

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"onlinemenu.tr/internal/modules/inventory/domain"
)

// StockMovementRepo manages stock_movements persistence.
// Movements are immutable: no Update or Delete methods (DATA-002).
type StockMovementRepo struct{}

// NewStockMovementRepo constructs a StockMovementRepo for fx injection.
func NewStockMovementRepo() *StockMovementRepo { return &StockMovementRepo{} }

// Create inserts a new movement record. Returns the persisted movement.
func (r *StockMovementRepo) Create(ctx context.Context, tx pgx.Tx, m domain.StockMovement) (domain.StockMovement, error) {
	const q = `
		INSERT INTO stock_movements
		    (tenant_id, warehouse_id, stock_item_id, movement_type, quantity,
		     reference_id, reference_type, notes, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		RETURNING id, tenant_id, warehouse_id, stock_item_id, movement_type, quantity,
		          reference_id, reference_type, notes, created_by, created_at`

	row := tx.QueryRow(ctx, q,
		m.TenantID, m.WarehouseID, m.StockItemID, string(m.Type), m.Quantity,
		m.ReferenceID, m.ReferenceType, m.Notes, m.CreatedBy,
	)
	return scanMovement(row)
}

// ListByStockItem returns movements for a stock item in a warehouse, newest first.
func (r *StockMovementRepo) ListByStockItem(ctx context.Context, tx pgx.Tx, warehouseID, stockItemID uuid.UUID, limit int) ([]domain.StockMovement, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	const q = `
		SELECT id, tenant_id, warehouse_id, stock_item_id, movement_type, quantity,
		       reference_id, reference_type, notes, created_by, created_at
		FROM stock_movements
		WHERE warehouse_id = $1 AND stock_item_id = $2
		ORDER BY created_at DESC
		LIMIT $3`

	rows, err := tx.Query(ctx, q, warehouseID, stockItemID, limit)
	if err != nil {
		return nil, fmt.Errorf("inventory/repo: list movements: %w", err)
	}
	defer rows.Close()

	var out []domain.StockMovement
	for rows.Next() {
		m, err := scanMovement(rows)
		if err != nil {
			return nil, fmt.Errorf("inventory/repo: list movements scan: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ListByWarehouse returns recent movements for an entire warehouse, newest first.
func (r *StockMovementRepo) ListByWarehouse(ctx context.Context, tx pgx.Tx, warehouseID uuid.UUID, limit int) ([]domain.StockMovement, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	const q = `
		SELECT id, tenant_id, warehouse_id, stock_item_id, movement_type, quantity,
		       reference_id, reference_type, notes, created_by, created_at
		FROM stock_movements
		WHERE warehouse_id = $1
		ORDER BY created_at DESC
		LIMIT $2`

	rows, err := tx.Query(ctx, q, warehouseID, limit)
	if err != nil {
		return nil, fmt.Errorf("inventory/repo: list warehouse movements: %w", err)
	}
	defer rows.Close()

	var out []domain.StockMovement
	for rows.Next() {
		m, err := scanMovement(rows)
		if err != nil {
			return nil, fmt.Errorf("inventory/repo: list warehouse movements scan: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func scanMovement(s pgx.Row) (domain.StockMovement, error) {
	var m domain.StockMovement
	var movementType string
	err := s.Scan(
		&m.ID, &m.TenantID, &m.WarehouseID, &m.StockItemID,
		&movementType, &m.Quantity,
		&m.ReferenceID, &m.ReferenceType, &m.Notes, &m.CreatedBy,
		&m.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.StockMovement{}, ErrNotFound
		}
		return domain.StockMovement{}, fmt.Errorf("inventory/repo: scan movement: %w", err)
	}
	m.Type = domain.MovementType(movementType)
	return m, nil
}
