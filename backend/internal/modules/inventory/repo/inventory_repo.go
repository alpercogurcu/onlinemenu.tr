package repo

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"onlinemenu.tr/internal/modules/inventory/domain"
)

// ErrNotFound is returned when a requested inventory record does not exist.
var ErrNotFound = errors.New("inventory: not found")

// ============================================================
// InventoryLevelRepo
// ============================================================

// InventoryLevelRepo manages inventory_levels persistence.
type InventoryLevelRepo struct{}

func NewInventoryLevelRepo() *InventoryLevelRepo { return &InventoryLevelRepo{} }

// Upsert creates or replaces the stock level for a product in a branch.
// Uses ON CONFLICT to atomically set the quantity and bump updated_at.
func (r *InventoryLevelRepo) Upsert(ctx context.Context, tx pgx.Tx, level domain.InventoryLevel) (domain.InventoryLevel, error) {
	const q = `
		INSERT INTO inventory_levels (tenant_id, branch_id, product_id, quantity)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (tenant_id, branch_id, product_id)
		DO UPDATE SET quantity = EXCLUDED.quantity, updated_at = NOW()
		RETURNING id, tenant_id, branch_id, product_id, quantity, updated_at`

	row := tx.QueryRow(ctx, q, level.TenantID, level.BranchID, level.ProductID, level.Quantity)
	return scanLevel(row)
}

// AdjustQuantity applies a signed delta to a product's stock level atomically.
// Creates the level record if it does not exist (initial delta becomes the quantity).
// Returns ErrNotFound if the resulting quantity would go below zero (CHECK constraint).
func (r *InventoryLevelRepo) AdjustQuantity(ctx context.Context, tx pgx.Tx, tenantID, branchID, productID uuid.UUID, delta float64) (domain.InventoryLevel, error) {
	const q = `
		INSERT INTO inventory_levels (tenant_id, branch_id, product_id, quantity)
		VALUES ($1, $2, $3, GREATEST(0, $4::numeric))
		ON CONFLICT (tenant_id, branch_id, product_id)
		DO UPDATE SET
			quantity   = GREATEST(0, inventory_levels.quantity + $4::numeric),
			updated_at = NOW()
		RETURNING id, tenant_id, branch_id, product_id, quantity, updated_at`

	row := tx.QueryRow(ctx, q, tenantID, branchID, productID, delta)
	return scanLevel(row)
}

// GetByProduct returns the current stock level for a product in a branch.
func (r *InventoryLevelRepo) GetByProduct(ctx context.Context, tx pgx.Tx, branchID, productID uuid.UUID) (domain.InventoryLevel, error) {
	const q = `
		SELECT id, tenant_id, branch_id, product_id, quantity, updated_at
		FROM inventory_levels
		WHERE branch_id = $1 AND product_id = $2`

	row := tx.QueryRow(ctx, q, branchID, productID)
	l, err := scanLevel(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.InventoryLevel{}, ErrNotFound
		}
		return domain.InventoryLevel{}, fmt.Errorf("inventory/repo: get level: %w", err)
	}
	return l, nil
}

// ListByBranch returns all stock levels for a branch.
func (r *InventoryLevelRepo) ListByBranch(ctx context.Context, tx pgx.Tx, branchID uuid.UUID) ([]domain.InventoryLevel, error) {
	const q = `
		SELECT id, tenant_id, branch_id, product_id, quantity, updated_at
		FROM inventory_levels
		WHERE branch_id = $1
		ORDER BY product_id`

	rows, err := tx.Query(ctx, q, branchID)
	if err != nil {
		return nil, fmt.Errorf("inventory/repo: list by branch: %w", err)
	}
	defer rows.Close()

	var levels []domain.InventoryLevel
	for rows.Next() {
		l, err := scanLevel(rows)
		if err != nil {
			return nil, fmt.Errorf("inventory/repo: list by branch scan: %w", err)
		}
		levels = append(levels, l)
	}
	return levels, rows.Err()
}

func scanLevel(s pgx.Row) (domain.InventoryLevel, error) {
	var l domain.InventoryLevel
	err := s.Scan(&l.ID, &l.TenantID, &l.BranchID, &l.ProductID, &l.Quantity, &l.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.InventoryLevel{}, ErrNotFound
		}
		return domain.InventoryLevel{}, fmt.Errorf("inventory/repo: scan level: %w", err)
	}
	return l, nil
}

// ============================================================
// InventoryTransactionRepo
// ============================================================

// InventoryTransactionRepo manages inventory_transactions persistence.
// Transactions are immutable: no Update or Delete methods (DATA-002).
type InventoryTransactionRepo struct{}

func NewInventoryTransactionRepo() *InventoryTransactionRepo { return &InventoryTransactionRepo{} }

// Create inserts a new transaction record. Returns the persisted transaction.
func (r *InventoryTransactionRepo) Create(ctx context.Context, tx pgx.Tx, t domain.InventoryTransaction) (domain.InventoryTransaction, error) {
	const q = `
		INSERT INTO inventory_transactions
		    (tenant_id, branch_id, product_id, type, quantity_delta,
		     reference_id, reference_type, notes, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		RETURNING id, tenant_id, branch_id, product_id, type, quantity_delta,
		          reference_id, reference_type, notes, created_by, created_at`

	row := tx.QueryRow(ctx, q,
		t.TenantID, t.BranchID, t.ProductID, string(t.Type), t.QuantityDelta,
		t.ReferenceID, t.ReferenceType, t.Notes, t.CreatedBy,
	)
	return scanTransaction(row)
}

// ListByProduct returns transactions for a product in a branch, newest first.
func (r *InventoryTransactionRepo) ListByProduct(ctx context.Context, tx pgx.Tx, branchID, productID uuid.UUID, limit int) ([]domain.InventoryTransaction, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	const q = `
		SELECT id, tenant_id, branch_id, product_id, type, quantity_delta,
		       reference_id, reference_type, notes, created_by, created_at
		FROM inventory_transactions
		WHERE branch_id = $1 AND product_id = $2
		ORDER BY created_at DESC
		LIMIT $3`

	rows, err := tx.Query(ctx, q, branchID, productID, limit)
	if err != nil {
		return nil, fmt.Errorf("inventory/repo: list transactions: %w", err)
	}
	defer rows.Close()

	var txs []domain.InventoryTransaction
	for rows.Next() {
		t, err := scanTransaction(rows)
		if err != nil {
			return nil, fmt.Errorf("inventory/repo: list transactions scan: %w", err)
		}
		txs = append(txs, t)
	}
	return txs, rows.Err()
}

// ListByBranch returns recent transactions for an entire branch, newest first.
func (r *InventoryTransactionRepo) ListByBranch(ctx context.Context, tx pgx.Tx, branchID uuid.UUID, limit int) ([]domain.InventoryTransaction, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	const q = `
		SELECT id, tenant_id, branch_id, product_id, type, quantity_delta,
		       reference_id, reference_type, notes, created_by, created_at
		FROM inventory_transactions
		WHERE branch_id = $1
		ORDER BY created_at DESC
		LIMIT $2`

	rows, err := tx.Query(ctx, q, branchID, limit)
	if err != nil {
		return nil, fmt.Errorf("inventory/repo: list branch transactions: %w", err)
	}
	defer rows.Close()

	var txs []domain.InventoryTransaction
	for rows.Next() {
		t, err := scanTransaction(rows)
		if err != nil {
			return nil, fmt.Errorf("inventory/repo: list branch transactions scan: %w", err)
		}
		txs = append(txs, t)
	}
	return txs, rows.Err()
}

func scanTransaction(s pgx.Row) (domain.InventoryTransaction, error) {
	var t domain.InventoryTransaction
	var txType string
	err := s.Scan(
		&t.ID, &t.TenantID, &t.BranchID, &t.ProductID,
		&txType, &t.QuantityDelta,
		&t.ReferenceID, &t.ReferenceType, &t.Notes, &t.CreatedBy,
		&t.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.InventoryTransaction{}, ErrNotFound
		}
		return domain.InventoryTransaction{}, fmt.Errorf("inventory/repo: scan transaction: %w", err)
	}
	t.Type = domain.TransactionType(txType)
	return t, nil
}
