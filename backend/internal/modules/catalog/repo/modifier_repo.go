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

// ModifierGroupRepo provides data access for the modifier_groups table.
type ModifierGroupRepo struct{}

// NewModifierGroupRepo constructs a ModifierGroupRepo for fx injection.
func NewModifierGroupRepo() *ModifierGroupRepo { return &ModifierGroupRepo{} }

func (r *ModifierGroupRepo) Create(ctx context.Context, tx pgx.Tx, g domain.ModifierGroup) (domain.ModifierGroup, error) {
	const q = `
		INSERT INTO modifier_groups (tenant_id, name, selection_type, min_selections, max_selections, is_required, sort_order)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, tenant_id, name, selection_type, min_selections, max_selections, is_required, sort_order, created_at, updated_at`

	row := tx.QueryRow(ctx, q,
		g.TenantID, g.Name, string(g.SelectionType),
		g.MinSelections, g.MaxSelections, g.IsRequired, g.SortOrder,
	)
	created, err := scanModifierGroup(row)
	if err != nil {
		return domain.ModifierGroup{}, fmt.Errorf("catalog/repo/modifier_group: create: %w", err)
	}
	return created, nil
}

func (r *ModifierGroupRepo) GetByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (domain.ModifierGroup, error) {
	const q = `
		SELECT id, tenant_id, name, selection_type, min_selections, max_selections, is_required, sort_order, created_at, updated_at
		FROM modifier_groups WHERE id = $1`

	row := tx.QueryRow(ctx, q, id)
	g, err := scanModifierGroup(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ModifierGroup{}, ErrNotFound
		}
		return domain.ModifierGroup{}, fmt.Errorf("catalog/repo/modifier_group: get by id: %w", err)
	}
	return g, nil
}

func (r *ModifierGroupRepo) List(ctx context.Context, tx pgx.Tx) ([]domain.ModifierGroup, error) {
	const q = `
		SELECT id, tenant_id, name, selection_type, min_selections, max_selections, is_required, sort_order, created_at, updated_at
		FROM modifier_groups
		ORDER BY sort_order, name`

	rows, err := tx.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("catalog/repo/modifier_group: list: %w", err)
	}
	defer rows.Close()

	var out []domain.ModifierGroup
	for rows.Next() {
		g, err := scanModifierGroup(rows)
		if err != nil {
			return nil, fmt.Errorf("catalog/repo/modifier_group: list scan: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (r *ModifierGroupRepo) Update(ctx context.Context, tx pgx.Tx, g domain.ModifierGroup) (domain.ModifierGroup, error) {
	const q = `
		UPDATE modifier_groups
		SET name=$1, selection_type=$2, min_selections=$3, max_selections=$4, is_required=$5, sort_order=$6, updated_at=NOW()
		WHERE id=$7
		RETURNING id, tenant_id, name, selection_type, min_selections, max_selections, is_required, sort_order, created_at, updated_at`

	row := tx.QueryRow(ctx, q,
		g.Name, string(g.SelectionType), g.MinSelections, g.MaxSelections,
		g.IsRequired, g.SortOrder, g.ID,
	)
	updated, err := scanModifierGroup(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ModifierGroup{}, ErrNotFound
		}
		return domain.ModifierGroup{}, fmt.Errorf("catalog/repo/modifier_group: update: %w", err)
	}
	return updated, nil
}

func (r *ModifierGroupRepo) Delete(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	tag, err := tx.Exec(ctx, `DELETE FROM modifier_groups WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("catalog/repo/modifier_group: delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func scanModifierGroup(row pgx.Row) (domain.ModifierGroup, error) {
	var (
		g         domain.ModifierGroup
		selType   string
		createdAt time.Time
		updatedAt time.Time
	)
	err := row.Scan(
		&g.ID, &g.TenantID, &g.Name, &selType,
		&g.MinSelections, &g.MaxSelections,
		&g.IsRequired, &g.SortOrder,
		&createdAt, &updatedAt,
	)
	if err != nil {
		return domain.ModifierGroup{}, err
	}
	g.SelectionType = domain.SelectionType(selType)
	g.CreatedAt = createdAt
	g.UpdatedAt = updatedAt
	return g, nil
}

// ---------------------------------------------------------------------------
// ModifierRepo
// ---------------------------------------------------------------------------

// ModifierRepo provides data access for the modifiers table.
type ModifierRepo struct{}

// NewModifierRepo constructs a ModifierRepo for fx injection.
func NewModifierRepo() *ModifierRepo { return &ModifierRepo{} }

func (r *ModifierRepo) Create(ctx context.Context, tx pgx.Tx, m domain.Modifier) (domain.Modifier, error) {
	const q = `
		INSERT INTO modifiers (tenant_id, group_id, name, price_delta, is_active, sort_order)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, tenant_id, group_id, name, price_delta, is_active, sort_order, created_at, updated_at`

	row := tx.QueryRow(ctx, q,
		m.TenantID, m.GroupID, m.Name, m.PriceDelta, m.IsActive, m.SortOrder,
	)
	created, err := scanModifier(row)
	if err != nil {
		return domain.Modifier{}, fmt.Errorf("catalog/repo/modifier: create: %w", err)
	}
	return created, nil
}

func (r *ModifierRepo) GetByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (domain.Modifier, error) {
	const q = `
		SELECT id, tenant_id, group_id, name, price_delta, is_active, sort_order, created_at, updated_at
		FROM modifiers WHERE id = $1`

	row := tx.QueryRow(ctx, q, id)
	m, err := scanModifier(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Modifier{}, ErrNotFound
		}
		return domain.Modifier{}, fmt.Errorf("catalog/repo/modifier: get by id: %w", err)
	}
	return m, nil
}

func (r *ModifierRepo) ListByGroup(ctx context.Context, tx pgx.Tx, groupID uuid.UUID) ([]domain.Modifier, error) {
	const q = `
		SELECT id, tenant_id, group_id, name, price_delta, is_active, sort_order, created_at, updated_at
		FROM modifiers
		WHERE group_id = $1
		ORDER BY sort_order, name`

	rows, err := tx.Query(ctx, q, groupID)
	if err != nil {
		return nil, fmt.Errorf("catalog/repo/modifier: list by group: %w", err)
	}
	defer rows.Close()

	var out []domain.Modifier
	for rows.Next() {
		m, err := scanModifier(rows)
		if err != nil {
			return nil, fmt.Errorf("catalog/repo/modifier: list by group scan: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *ModifierRepo) Update(ctx context.Context, tx pgx.Tx, m domain.Modifier) (domain.Modifier, error) {
	const q = `
		UPDATE modifiers
		SET name=$1, price_delta=$2, is_active=$3, sort_order=$4, updated_at=NOW()
		WHERE id=$5
		RETURNING id, tenant_id, group_id, name, price_delta, is_active, sort_order, created_at, updated_at`

	row := tx.QueryRow(ctx, q,
		m.Name, m.PriceDelta, m.IsActive, m.SortOrder, m.ID,
	)
	updated, err := scanModifier(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Modifier{}, ErrNotFound
		}
		return domain.Modifier{}, fmt.Errorf("catalog/repo/modifier: update: %w", err)
	}
	return updated, nil
}

func (r *ModifierRepo) Delete(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	tag, err := tx.Exec(ctx, `DELETE FROM modifiers WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("catalog/repo/modifier: delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func scanModifier(row pgx.Row) (domain.Modifier, error) {
	var (
		m         domain.Modifier
		createdAt time.Time
		updatedAt time.Time
	)
	err := row.Scan(
		&m.ID, &m.TenantID, &m.GroupID, &m.Name,
		&m.PriceDelta, &m.IsActive, &m.SortOrder,
		&createdAt, &updatedAt,
	)
	if err != nil {
		return domain.Modifier{}, err
	}
	m.CreatedAt = createdAt
	m.UpdatedAt = updatedAt
	return m, nil
}

// ---------------------------------------------------------------------------
// ProductModifierGroupRepo
// ---------------------------------------------------------------------------

// ProductModifierGroupRepo manages the product_modifier_groups junction table.
type ProductModifierGroupRepo struct{}

// NewProductModifierGroupRepo constructs a ProductModifierGroupRepo for fx injection.
func NewProductModifierGroupRepo() *ProductModifierGroupRepo { return &ProductModifierGroupRepo{} }

// Assign links a modifier group to a product, updating sort_order if already linked.
func (r *ProductModifierGroupRepo) Assign(ctx context.Context, tx pgx.Tx, productID, groupID, tenantID uuid.UUID, sortOrder int16) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO product_modifier_groups (product_id, group_id, tenant_id, sort_order)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (product_id, group_id) DO UPDATE SET sort_order = EXCLUDED.sort_order`,
		productID, groupID, tenantID, sortOrder,
	)
	if err != nil {
		return fmt.Errorf("catalog/repo/product_modifier_group: assign: %w", err)
	}
	return nil
}

// Remove unlinks a modifier group from a product. Not-found is silently ignored.
func (r *ProductModifierGroupRepo) Remove(ctx context.Context, tx pgx.Tx, productID, groupID uuid.UUID) error {
	_, err := tx.Exec(ctx,
		`DELETE FROM product_modifier_groups WHERE product_id = $1 AND group_id = $2`,
		productID, groupID,
	)
	if err != nil {
		return fmt.Errorf("catalog/repo/product_modifier_group: remove: %w", err)
	}
	return nil
}

// ListByProduct returns the group IDs assigned to a product, ordered by sort_order.
func (r *ProductModifierGroupRepo) ListByProduct(ctx context.Context, tx pgx.Tx, productID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, `
		SELECT group_id FROM product_modifier_groups
		WHERE product_id = $1
		ORDER BY sort_order, group_id`,
		productID,
	)
	if err != nil {
		return nil, fmt.Errorf("catalog/repo/product_modifier_group: list by product: %w", err)
	}
	defer rows.Close()

	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("catalog/repo/product_modifier_group: list scan: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
