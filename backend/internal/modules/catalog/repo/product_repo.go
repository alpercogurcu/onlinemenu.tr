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

// ProductRepo provides data access for the products table.
type ProductRepo struct{}

// NewProductRepo constructs a ProductRepo for fx injection.
func NewProductRepo() *ProductRepo { return &ProductRepo{} }

// Create inserts a new product and returns the persisted record.
func (r *ProductRepo) Create(ctx context.Context, tx pgx.Tx, p domain.Product) (domain.Product, error) {
	const q = `
		INSERT INTO products (
			tenant_id, category_id, name, description, image_key,
			price_amount, currency, sku, barcode, unit,
			tax_rate_bps, is_active, auto_close_on_zero_stock, stock_quantity, sort_order
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		RETURNING id, tenant_id, category_id, name, COALESCE(description,''), COALESCE(image_key,''),
		          price_amount, currency, COALESCE(sku,''), COALESCE(barcode,''), unit,
		          tax_rate_bps, is_active, auto_close_on_zero_stock, stock_quantity,
		          sort_order, created_at, updated_at`

	row := tx.QueryRow(ctx, q,
		p.TenantID, p.CategoryID, p.Name,
		emptyToNil(p.Description), emptyToNil(p.ImageKey),
		p.PriceAmount, p.Currency,
		emptyToNil(p.SKU), emptyToNil(p.Barcode), p.Unit,
		p.TaxRateBPS, p.IsActive, p.AutoCloseOnZeroStock, p.StockQuantity, p.SortOrder,
	)
	return scanProduct(row)
}

// GetByID fetches a single product by primary key within the RLS tenant context.
func (r *ProductRepo) GetByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (domain.Product, error) {
	const q = `
		SELECT id, tenant_id, category_id, name, COALESCE(description,''), COALESCE(image_key,''),
		       price_amount, currency, COALESCE(sku,''), COALESCE(barcode,''), unit,
		       tax_rate_bps, is_active, auto_close_on_zero_stock, stock_quantity,
		       sort_order, created_at, updated_at
		FROM products WHERE id = $1`

	row := tx.QueryRow(ctx, q, id)
	p, err := scanProduct(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Product{}, ErrNotFound
		}
		return domain.Product{}, fmt.Errorf("catalog/repo/product: get by id: %w", err)
	}
	return p, nil
}

// List returns all products visible to the current RLS tenant context.
func (r *ProductRepo) List(ctx context.Context, tx pgx.Tx) ([]domain.Product, error) {
	const q = `
		SELECT id, tenant_id, category_id, name, COALESCE(description,''), COALESCE(image_key,''),
		       price_amount, currency, COALESCE(sku,''), COALESCE(barcode,''), unit,
		       tax_rate_bps, is_active, auto_close_on_zero_stock, stock_quantity,
		       sort_order, created_at, updated_at
		FROM products
		WHERE is_active = true
		ORDER BY sort_order, name`

	rows, err := tx.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("catalog/repo/product: list: %w", err)
	}
	defer rows.Close()

	var out []domain.Product
	for rows.Next() {
		p, err := scanProduct(rows)
		if err != nil {
			return nil, fmt.Errorf("catalog/repo/product: list scan: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ListByCategory returns products belonging to a specific category.
func (r *ProductRepo) ListByCategory(ctx context.Context, tx pgx.Tx, categoryID uuid.UUID) ([]domain.Product, error) {
	const q = `
		SELECT id, tenant_id, category_id, name, COALESCE(description,''), COALESCE(image_key,''),
		       price_amount, currency, COALESCE(sku,''), COALESCE(barcode,''), unit,
		       tax_rate_bps, is_active, auto_close_on_zero_stock, stock_quantity,
		       sort_order, created_at, updated_at
		FROM products WHERE category_id = $1
		ORDER BY sort_order, name`

	rows, err := tx.Query(ctx, q, categoryID)
	if err != nil {
		return nil, fmt.Errorf("catalog/repo/product: list by category: %w", err)
	}
	defer rows.Close()

	var out []domain.Product
	for rows.Next() {
		p, err := scanProduct(rows)
		if err != nil {
			return nil, fmt.Errorf("catalog/repo/product: list by category scan: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Update persists mutable product field changes.
func (r *ProductRepo) Update(ctx context.Context, tx pgx.Tx, p domain.Product) (domain.Product, error) {
	const q = `
		UPDATE products SET
			category_id=$1, name=$2, description=$3, image_key=$4,
			price_amount=$5, currency=$6, sku=$7, barcode=$8, unit=$9,
			tax_rate_bps=$10, is_active=$11, auto_close_on_zero_stock=$12,
			stock_quantity=$13, sort_order=$14, updated_at=NOW()
		WHERE id=$15
		RETURNING id, tenant_id, category_id, name, COALESCE(description,''), COALESCE(image_key,''),
		          price_amount, currency, COALESCE(sku,''), COALESCE(barcode,''), unit,
		          tax_rate_bps, is_active, auto_close_on_zero_stock, stock_quantity,
		          sort_order, created_at, updated_at`

	row := tx.QueryRow(ctx, q,
		p.CategoryID, p.Name,
		emptyToNil(p.Description), emptyToNil(p.ImageKey),
		p.PriceAmount, p.Currency,
		emptyToNil(p.SKU), emptyToNil(p.Barcode), p.Unit,
		p.TaxRateBPS, p.IsActive, p.AutoCloseOnZeroStock, p.StockQuantity,
		p.SortOrder, p.ID,
	)
	updated, err := scanProduct(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Product{}, ErrNotFound
		}
		return domain.Product{}, fmt.Errorf("catalog/repo/product: update: %w", err)
	}
	return updated, nil
}

// Delete marks a product as inactive (soft delete).
func (r *ProductRepo) Delete(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	const q = `UPDATE products SET is_active=false, updated_at=NOW() WHERE id=$1`
	tag, err := tx.Exec(ctx, q, id)
	if err != nil {
		return fmt.Errorf("catalog/repo/product: delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func scanProduct(row pgx.Row) (domain.Product, error) {
	var (
		p         domain.Product
		createdAt time.Time
		updatedAt time.Time
	)
	err := row.Scan(
		&p.ID, &p.TenantID, &p.CategoryID,
		&p.Name, &p.Description, &p.ImageKey,
		&p.PriceAmount, &p.Currency,
		&p.SKU, &p.Barcode, &p.Unit,
		&p.TaxRateBPS, &p.IsActive, &p.AutoCloseOnZeroStock, &p.StockQuantity,
		&p.SortOrder, &createdAt, &updatedAt,
	)
	if err != nil {
		return domain.Product{}, err
	}
	p.CreatedAt = createdAt
	p.UpdatedAt = updatedAt
	return p, nil
}
