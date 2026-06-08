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

// CategoryRepo provides data access for the categories table.
type CategoryRepo struct{}

// NewCategoryRepo constructs a CategoryRepo for fx injection.
func NewCategoryRepo() *CategoryRepo { return &CategoryRepo{} }

// Create inserts a new category row and returns the persisted record.
func (r *CategoryRepo) Create(ctx context.Context, tx pgx.Tx, c domain.Category) (domain.Category, error) {
	const q = `
		INSERT INTO categories (tenant_id, branch_id, parent_id, name, description, image_key, is_active, sort_order)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, tenant_id, branch_id, parent_id, name, COALESCE(description,''), COALESCE(image_key,''), is_active, sort_order, created_at, updated_at`

	row := tx.QueryRow(ctx, q,
		c.TenantID, c.BranchID, c.ParentID, c.Name,
		emptyToNil(c.Description), emptyToNil(c.ImageKey),
		c.IsActive, c.SortOrder,
	)
	return scanCategory(row)
}

// GetByID fetches a single category by primary key within the RLS tenant context.
func (r *CategoryRepo) GetByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (domain.Category, error) {
	const q = `
		SELECT id, tenant_id, branch_id, parent_id, name, COALESCE(description,''), COALESCE(image_key,''),
		       is_active, sort_order, created_at, updated_at
		FROM categories WHERE id = $1`

	row := tx.QueryRow(ctx, q, id)
	c, err := scanCategory(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Category{}, ErrNotFound
		}
		return domain.Category{}, fmt.Errorf("catalog/repo/category: get by id: %w", err)
	}
	return c, nil
}

// List returns all active categories visible to the current RLS tenant context.
func (r *CategoryRepo) List(ctx context.Context, tx pgx.Tx) ([]domain.Category, error) {
	const q = `
		SELECT id, tenant_id, branch_id, parent_id, name, COALESCE(description,''), COALESCE(image_key,''),
		       is_active, sort_order, created_at, updated_at
		FROM categories
		ORDER BY sort_order, name`

	rows, err := tx.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("catalog/repo/category: list: %w", err)
	}
	defer rows.Close()

	var out []domain.Category
	for rows.Next() {
		c, err := scanCategory(rows)
		if err != nil {
			return nil, fmt.Errorf("catalog/repo/category: list scan: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Update persists name, description, image_key, is_active, and sort_order changes.
func (r *CategoryRepo) Update(ctx context.Context, tx pgx.Tx, c domain.Category) (domain.Category, error) {
	const q = `
		UPDATE categories
		SET name=$1, description=$2, image_key=$3, is_active=$4, sort_order=$5, updated_at=NOW()
		WHERE id=$6
		RETURNING id, tenant_id, branch_id, parent_id, name, COALESCE(description,''), COALESCE(image_key,''),
		          is_active, sort_order, created_at, updated_at`

	row := tx.QueryRow(ctx, q,
		c.Name, emptyToNil(c.Description), emptyToNil(c.ImageKey),
		c.IsActive, c.SortOrder, c.ID,
	)
	updated, err := scanCategory(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Category{}, ErrNotFound
		}
		return domain.Category{}, fmt.Errorf("catalog/repo/category: update: %w", err)
	}
	return updated, nil
}

func scanCategory(row pgx.Row) (domain.Category, error) {
	var (
		c         domain.Category
		createdAt time.Time
		updatedAt time.Time
	)
	err := row.Scan(
		&c.ID, &c.TenantID, &c.BranchID, &c.ParentID,
		&c.Name, &c.Description, &c.ImageKey,
		&c.IsActive, &c.SortOrder,
		&createdAt, &updatedAt,
	)
	if err != nil {
		return domain.Category{}, err
	}
	c.CreatedAt = createdAt
	c.UpdatedAt = updatedAt
	return c, nil
}

func emptyToNil(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
