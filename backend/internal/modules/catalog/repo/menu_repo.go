package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"onlinemenu.tr/internal/modules/catalog/domain"
)

// MenuRepo provides data access for the menus table.
type MenuRepo struct{}

// NewMenuRepo constructs a MenuRepo for fx injection.
func NewMenuRepo() *MenuRepo { return &MenuRepo{} }

func (r *MenuRepo) Create(ctx context.Context, tx pgx.Tx, m domain.Menu) (domain.Menu, error) {
	const q = `
		INSERT INTO menus (tenant_id, branch_id, name, description, is_active, valid_from, valid_until, sort_order)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, tenant_id, branch_id, name, COALESCE(description,''), is_active, valid_from, valid_until, sort_order, created_at, updated_at`

	row := tx.QueryRow(ctx, q,
		m.TenantID, m.BranchID, m.Name, emptyToNil(m.Description),
		m.IsActive, m.ValidFrom, m.ValidUntil, m.SortOrder,
	)
	created, err := scanMenu(row)
	if err != nil {
		return domain.Menu{}, fmt.Errorf("catalog/repo/menu: create: %w", err)
	}
	return created, nil
}

func (r *MenuRepo) GetByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (domain.Menu, error) {
	const q = `
		SELECT id, tenant_id, branch_id, name, COALESCE(description,''), is_active, valid_from, valid_until, sort_order, created_at, updated_at
		FROM menus WHERE id = $1`

	row := tx.QueryRow(ctx, q, id)
	m, err := scanMenu(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Menu{}, ErrNotFound
		}
		return domain.Menu{}, fmt.Errorf("catalog/repo/menu: get by id: %w", err)
	}
	return m, nil
}

func (r *MenuRepo) List(ctx context.Context, tx pgx.Tx) ([]domain.Menu, error) {
	const q = `
		SELECT id, tenant_id, branch_id, name, COALESCE(description,''), is_active, valid_from, valid_until, sort_order, created_at, updated_at
		FROM menus
		ORDER BY sort_order, name`

	rows, err := tx.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("catalog/repo/menu: list: %w", err)
	}
	defer rows.Close()

	var out []domain.Menu
	for rows.Next() {
		m, err := scanMenu(rows)
		if err != nil {
			return nil, fmt.Errorf("catalog/repo/menu: list scan: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *MenuRepo) Update(ctx context.Context, tx pgx.Tx, m domain.Menu) (domain.Menu, error) {
	const q = `
		UPDATE menus
		SET name=$1, description=$2, is_active=$3, valid_from=$4, valid_until=$5, sort_order=$6, updated_at=NOW()
		WHERE id=$7
		RETURNING id, tenant_id, branch_id, name, COALESCE(description,''), is_active, valid_from, valid_until, sort_order, created_at, updated_at`

	row := tx.QueryRow(ctx, q,
		m.Name, emptyToNil(m.Description), m.IsActive, m.ValidFrom, m.ValidUntil, m.SortOrder, m.ID,
	)
	updated, err := scanMenu(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Menu{}, ErrNotFound
		}
		return domain.Menu{}, fmt.Errorf("catalog/repo/menu: update: %w", err)
	}
	return updated, nil
}

func scanMenu(row pgx.Row) (domain.Menu, error) {
	var (
		m         domain.Menu
		createdAt time.Time
		updatedAt time.Time
	)
	err := row.Scan(
		&m.ID, &m.TenantID, &m.BranchID, &m.Name, &m.Description,
		&m.IsActive, &m.ValidFrom, &m.ValidUntil, &m.SortOrder,
		&createdAt, &updatedAt,
	)
	if err != nil {
		return domain.Menu{}, err
	}
	m.CreatedAt = createdAt
	m.UpdatedAt = updatedAt
	return m, nil
}

// ---------------------------------------------------------------------------
// MenuItemRepo
// ---------------------------------------------------------------------------

// MenuItemRepo manages the menu_items junction table.
type MenuItemRepo struct{}

// NewMenuItemRepo constructs a MenuItemRepo for fx injection.
func NewMenuItemRepo() *MenuItemRepo { return &MenuItemRepo{} }

// AddItem links a product to a menu, updating override/active/sort if already present.
func (r *MenuItemRepo) AddItem(ctx context.Context, tx pgx.Tx, item domain.MenuItem) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO menu_items (menu_id, product_id, tenant_id, price_override, is_active, sort_order)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (menu_id, product_id) DO UPDATE
			SET price_override = EXCLUDED.price_override,
			    is_active      = EXCLUDED.is_active,
			    sort_order     = EXCLUDED.sort_order`,
		item.MenuID, item.ProductID, item.TenantID,
		item.PriceOverride, item.IsActive, item.SortOrder,
	)
	if err != nil {
		return fmt.Errorf("catalog/repo/menu_item: add item: %w", err)
	}
	return nil
}

// RemoveItem removes a product from a menu. Not-found is silently ignored.
func (r *MenuItemRepo) RemoveItem(ctx context.Context, tx pgx.Tx, menuID, productID uuid.UUID) error {
	_, err := tx.Exec(ctx,
		`DELETE FROM menu_items WHERE menu_id = $1 AND product_id = $2`,
		menuID, productID,
	)
	if err != nil {
		return fmt.Errorf("catalog/repo/menu_item: remove item: %w", err)
	}
	return nil
}

// ListByMenu returns all items in a menu ordered by sort_order.
func (r *MenuItemRepo) ListByMenu(ctx context.Context, tx pgx.Tx, menuID uuid.UUID) ([]domain.MenuItem, error) {
	const q = `
		SELECT menu_id, product_id, tenant_id, price_override, is_active, sort_order
		FROM menu_items
		WHERE menu_id = $1
		ORDER BY sort_order, product_id`

	rows, err := tx.Query(ctx, q, menuID)
	if err != nil {
		return nil, fmt.Errorf("catalog/repo/menu_item: list by menu: %w", err)
	}
	defer rows.Close()

	var out []domain.MenuItem
	for rows.Next() {
		var item domain.MenuItem
		if err := rows.Scan(
			&item.MenuID, &item.ProductID, &item.TenantID,
			&item.PriceOverride, &item.IsActive, &item.SortOrder,
		); err != nil {
			return nil, fmt.Errorf("catalog/repo/menu_item: list scan: %w", err)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
