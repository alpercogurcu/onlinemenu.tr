package repo

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"onlinemenu.tr/internal/modules/catalog/domain"
)

// BranchOverrideRepo provides data access for branch_product_overrides
// (ADR-DATA-009). Tenant scoping is RLS's job; every method runs inside
// platform/db.WithTenantTx.
type BranchOverrideRepo struct{}

// NewBranchOverrideRepo constructs a BranchOverrideRepo for fx injection.
func NewBranchOverrideRepo() *BranchOverrideRepo { return &BranchOverrideRepo{} }

const branchOverrideColumns = `tenant_id, branch_id, product_id, is_available, price_amount, updated_at`

// ListByBranch returns every override configured for one branch, newest
// product order irrelevant — sorted by product id so the response is stable
// across calls.
func (r *BranchOverrideRepo) ListByBranch(ctx context.Context, tx pgx.Tx, branchID uuid.UUID) ([]domain.BranchProductOverride, error) {
	const q = `SELECT ` + branchOverrideColumns + `
		FROM branch_product_overrides
		WHERE branch_id = $1
		ORDER BY product_id`

	rows, err := tx.Query(ctx, q, branchID)
	if err != nil {
		return nil, fmt.Errorf("catalog/repo/branch_override: list by branch: %w", err)
	}
	defer rows.Close()

	var out []domain.BranchProductOverride
	for rows.Next() {
		var o domain.BranchProductOverride
		if err := rows.Scan(&o.TenantID, &o.BranchID, &o.ProductID, &o.IsAvailable, &o.PriceAmount, &o.UpdatedAt); err != nil {
			return nil, fmt.Errorf("catalog/repo/branch_override: list by branch scan: %w", err)
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// Upsert writes the branch's deviation for one product and returns the
// persisted row.
//
// ON CONFLICT DO UPDATE is legitimate here and is NOT the outbox anti-pattern
// the lint rules ban (ADR-DATA-002): this is current-state configuration, not
// an event log. The event that records the change is a separate immutable
// catalog_outbox row written in the same transaction.
func (r *BranchOverrideRepo) Upsert(ctx context.Context, tx pgx.Tx, o domain.BranchProductOverride) (domain.BranchProductOverride, error) {
	const q = `
		INSERT INTO branch_product_overrides (tenant_id, branch_id, product_id, is_available, price_amount, updated_at)
		VALUES ($1, $2, $3, $4, $5, NOW())
		ON CONFLICT (tenant_id, branch_id, product_id) DO UPDATE
		SET is_available = EXCLUDED.is_available,
		    price_amount = EXCLUDED.price_amount,
		    updated_at   = NOW()
		RETURNING ` + branchOverrideColumns

	row := tx.QueryRow(ctx, q, o.TenantID, o.BranchID, o.ProductID, o.IsAvailable, o.PriceAmount)
	var saved domain.BranchProductOverride
	if err := row.Scan(&saved.TenantID, &saved.BranchID, &saved.ProductID, &saved.IsAvailable, &saved.PriceAmount, &saved.UpdatedAt); err != nil {
		return domain.BranchProductOverride{}, fmt.Errorf("catalog/repo/branch_override: upsert: %w", err)
	}
	return saved, nil
}

// Delete removes the branch's deviation, returning the product to the tenant
// default. A missing row is ErrNotFound rather than a silent success: the
// caller asked to change something, and "nothing was there" is an answer the
// admin screen should be able to show.
func (r *BranchOverrideRepo) Delete(ctx context.Context, tx pgx.Tx, branchID, productID uuid.UUID) error {
	const q = `DELETE FROM branch_product_overrides WHERE branch_id = $1 AND product_id = $2`

	tag, err := tx.Exec(ctx, q, branchID, productID)
	if err != nil {
		return fmt.Errorf("catalog/repo/branch_override: delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// MapForBranch returns the branch's overrides for the listed products, keyed
// by product id.
//
// It backs the product LISTING path only. The pricing paths do not use it:
// they resolve the override inside their own SQL (storefrontMenuItemsCTE /
// PriceCatalogProducts) so that what a diner is shown and what the server
// charges come from one query, never from two that could disagree.
func (r *BranchOverrideRepo) MapForBranch(ctx context.Context, tx pgx.Tx, branchID uuid.UUID, productIDs []uuid.UUID) (map[uuid.UUID]domain.BranchProductOverride, error) {
	out := make(map[uuid.UUID]domain.BranchProductOverride, len(productIDs))
	if branchID == uuid.Nil || len(productIDs) == 0 {
		return out, nil
	}

	const q = `SELECT ` + branchOverrideColumns + `
		FROM branch_product_overrides
		WHERE branch_id = $1 AND product_id = ANY($2::uuid[])`

	rows, err := tx.Query(ctx, q, branchID, uuidStringSlice(productIDs))
	if err != nil {
		return nil, fmt.Errorf("catalog/repo/branch_override: map for branch: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var o domain.BranchProductOverride
		if err := rows.Scan(&o.TenantID, &o.BranchID, &o.ProductID, &o.IsAvailable, &o.PriceAmount, &o.UpdatedAt); err != nil {
			return nil, fmt.Errorf("catalog/repo/branch_override: map for branch scan: %w", err)
		}
		out[o.ProductID] = o
	}
	return out, rows.Err()
}
