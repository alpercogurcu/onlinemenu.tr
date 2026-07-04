package repo

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"onlinemenu.tr/internal/modules/inventory/domain"
)

// StockItemRepo manages stock_items persistence.
type StockItemRepo struct{}

// NewStockItemRepo constructs a StockItemRepo for fx injection.
func NewStockItemRepo() *StockItemRepo { return &StockItemRepo{} }

// Create inserts a new stock item. The caller is responsible for generating
// item.ID (client-side UUIDv7; see migrations/inventory/000002 for rationale).
func (r *StockItemRepo) Create(ctx context.Context, tx pgx.Tx, item domain.StockItem) (domain.StockItem, error) {
	const q = `
		INSERT INTO stock_items (id, tenant_id, sku, name, kind, canonical_unit, category, is_active)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING id, tenant_id, sku, name, kind, canonical_unit, COALESCE(category, ''), is_active, created_at, updated_at`

	row := tx.QueryRow(ctx, q,
		item.ID, item.TenantID, item.SKU, item.Name, string(item.Kind),
		item.CanonicalUnit, emptyToNil(item.Category), item.IsActive,
	)
	return scanStockItem(row)
}

// GetByID fetches a single stock item by primary key within the RLS tenant context.
func (r *StockItemRepo) GetByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (domain.StockItem, error) {
	const q = `
		SELECT id, tenant_id, sku, name, kind, canonical_unit, COALESCE(category, ''), is_active, created_at, updated_at
		FROM stock_items WHERE id = $1`

	row := tx.QueryRow(ctx, q, id)
	item, err := scanStockItem(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.StockItem{}, ErrNotFound
		}
		return domain.StockItem{}, fmt.Errorf("inventory/repo/stock_item: get by id: %w", err)
	}
	return item, nil
}

// List returns all active stock items visible to the current RLS tenant context,
// optionally filtered by kind (empty kind = no filter).
func (r *StockItemRepo) List(ctx context.Context, tx pgx.Tx, kind domain.StockItemKind) ([]domain.StockItem, error) {
	const q = `
		SELECT id, tenant_id, sku, name, kind, canonical_unit, COALESCE(category, ''), is_active, created_at, updated_at
		FROM stock_items
		WHERE is_active = true AND ($1 = '' OR kind = $1)
		ORDER BY name`

	rows, err := tx.Query(ctx, q, string(kind))
	if err != nil {
		return nil, fmt.Errorf("inventory/repo/stock_item: list: %w", err)
	}
	defer rows.Close()

	var out []domain.StockItem
	for rows.Next() {
		item, err := scanStockItem(rows)
		if err != nil {
			return nil, fmt.Errorf("inventory/repo/stock_item: list scan: %w", err)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// Update persists mutable stock item field changes.
func (r *StockItemRepo) Update(ctx context.Context, tx pgx.Tx, item domain.StockItem) (domain.StockItem, error) {
	const q = `
		UPDATE stock_items SET
			sku = $1, name = $2, kind = $3, canonical_unit = $4, category = $5,
			is_active = $6, updated_at = NOW()
		WHERE id = $7
		RETURNING id, tenant_id, sku, name, kind, canonical_unit, COALESCE(category, ''), is_active, created_at, updated_at`

	row := tx.QueryRow(ctx, q,
		item.SKU, item.Name, string(item.Kind), item.CanonicalUnit, emptyToNil(item.Category),
		item.IsActive, item.ID,
	)
	updated, err := scanStockItem(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.StockItem{}, ErrNotFound
		}
		return domain.StockItem{}, fmt.Errorf("inventory/repo/stock_item: update: %w", err)
	}
	return updated, nil
}

// Delete marks a stock item as inactive (soft delete).
func (r *StockItemRepo) Delete(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	const q = `UPDATE stock_items SET is_active = false, updated_at = NOW() WHERE id = $1`
	tag, err := tx.Exec(ctx, q, id)
	if err != nil {
		return fmt.Errorf("inventory/repo/stock_item: delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func scanStockItem(s pgx.Row) (domain.StockItem, error) {
	var item domain.StockItem
	var kind string
	err := s.Scan(
		&item.ID, &item.TenantID, &item.SKU, &item.Name, &kind,
		&item.CanonicalUnit, &item.Category, &item.IsActive,
		&item.CreatedAt, &item.UpdatedAt,
	)
	if err != nil {
		return domain.StockItem{}, err
	}
	item.Kind = domain.StockItemKind(kind)
	return item, nil
}

func emptyToNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}
