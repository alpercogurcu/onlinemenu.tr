package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"onlinemenu.tr/internal/modules/inventory/domain"
)

// TransferOrderRepo manages branch_transfer_orders persistence.
type TransferOrderRepo struct{}

// NewTransferOrderRepo constructs a TransferOrderRepo for fx injection.
func NewTransferOrderRepo() *TransferOrderRepo { return &TransferOrderRepo{} }

// Create inserts a new branch transfer order (status defaults to draft).
func (r *TransferOrderRepo) Create(ctx context.Context, tx pgx.Tx, o domain.BranchTransferOrder) (domain.BranchTransferOrder, error) {
	const q = `
		INSERT INTO branch_transfer_orders
		    (tenant_id, requesting_branch_id, source_branch_id, status, priority,
		     requested_delivery_date, note, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING id, tenant_id, requesting_branch_id, source_branch_id, status, priority,
		          requested_delivery_date, COALESCE(note, ''), created_by,
		          submitted_at, approved_at, approved_by, created_at, updated_at`

	row := tx.QueryRow(ctx, q,
		o.TenantID, o.RequestingBranchID, o.SourceBranchID, string(o.Status), string(o.Priority),
		o.RequestedDeliveryDate, emptyToNil(o.Note), o.CreatedBy,
	)
	return scanTransferOrder(row)
}

// GetByID fetches a single BTO by primary key within the RLS tenant context.
func (r *TransferOrderRepo) GetByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (domain.BranchTransferOrder, error) {
	const q = `
		SELECT id, tenant_id, requesting_branch_id, source_branch_id, status, priority,
		       requested_delivery_date, COALESCE(note, ''), created_by,
		       submitted_at, approved_at, approved_by, created_at, updated_at
		FROM branch_transfer_orders WHERE id = $1`

	row := tx.QueryRow(ctx, q, id)
	o, err := scanTransferOrder(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.BranchTransferOrder{}, ErrNotFound
		}
		return domain.BranchTransferOrder{}, fmt.Errorf("inventory/repo/transfer_order: get by id: %w", err)
	}
	return o, nil
}

// ListByRequestingBranch returns BTOs requested by a branch, newest first.
func (r *TransferOrderRepo) ListByRequestingBranch(ctx context.Context, tx pgx.Tx, branchID uuid.UUID) ([]domain.BranchTransferOrder, error) {
	const q = `
		SELECT id, tenant_id, requesting_branch_id, source_branch_id, status, priority,
		       requested_delivery_date, COALESCE(note, ''), created_by,
		       submitted_at, approved_at, approved_by, created_at, updated_at
		FROM branch_transfer_orders
		WHERE requesting_branch_id = $1
		ORDER BY created_at DESC`

	rows, err := tx.Query(ctx, q, branchID)
	if err != nil {
		return nil, fmt.Errorf("inventory/repo/transfer_order: list by requesting branch: %w", err)
	}
	defer rows.Close()

	var out []domain.BranchTransferOrder
	for rows.Next() {
		o, err := scanTransferOrder(rows)
		if err != nil {
			return nil, fmt.Errorf("inventory/repo/transfer_order: list by requesting branch scan: %w", err)
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// ListBySourceBranch returns BTOs to be fulfilled by a source branch, newest first.
func (r *TransferOrderRepo) ListBySourceBranch(ctx context.Context, tx pgx.Tx, branchID uuid.UUID) ([]domain.BranchTransferOrder, error) {
	const q = `
		SELECT id, tenant_id, requesting_branch_id, source_branch_id, status, priority,
		       requested_delivery_date, COALESCE(note, ''), created_by,
		       submitted_at, approved_at, approved_by, created_at, updated_at
		FROM branch_transfer_orders
		WHERE source_branch_id = $1
		ORDER BY created_at DESC`

	rows, err := tx.Query(ctx, q, branchID)
	if err != nil {
		return nil, fmt.Errorf("inventory/repo/transfer_order: list by source branch: %w", err)
	}
	defer rows.Close()

	var out []domain.BranchTransferOrder
	for rows.Next() {
		o, err := scanTransferOrder(rows)
		if err != nil {
			return nil, fmt.Errorf("inventory/repo/transfer_order: list by source branch scan: %w", err)
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// UpdateStatus persists a BTO status transition. submittedAt/approvedAt/approvedBy
// are applied via COALESCE so unrelated transitions do not clear prior values.
func (r *TransferOrderRepo) UpdateStatus(ctx context.Context, tx pgx.Tx, id uuid.UUID, status domain.BTOStatus, submittedAt, approvedAt *time.Time, approvedBy *uuid.UUID) (domain.BranchTransferOrder, error) {
	const q = `
		UPDATE branch_transfer_orders SET
			status = $1,
			submitted_at = COALESCE($2, submitted_at),
			approved_at  = COALESCE($3, approved_at),
			approved_by  = COALESCE($4, approved_by),
			updated_at = NOW()
		WHERE id = $5
		RETURNING id, tenant_id, requesting_branch_id, source_branch_id, status, priority,
		          requested_delivery_date, COALESCE(note, ''), created_by,
		          submitted_at, approved_at, approved_by, created_at, updated_at`

	row := tx.QueryRow(ctx, q, string(status), submittedAt, approvedAt, approvedBy, id)
	updated, err := scanTransferOrder(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.BranchTransferOrder{}, ErrNotFound
		}
		return domain.BranchTransferOrder{}, fmt.Errorf("inventory/repo/transfer_order: update status: %w", err)
	}
	return updated, nil
}

func scanTransferOrder(s pgx.Row) (domain.BranchTransferOrder, error) {
	var o domain.BranchTransferOrder
	var status, priority string
	err := s.Scan(
		&o.ID, &o.TenantID, &o.RequestingBranchID, &o.SourceBranchID, &status, &priority,
		&o.RequestedDeliveryDate, &o.Note, &o.CreatedBy,
		&o.SubmittedAt, &o.ApprovedAt, &o.ApprovedBy, &o.CreatedAt, &o.UpdatedAt,
	)
	if err != nil {
		return domain.BranchTransferOrder{}, err
	}
	o.Status = domain.BTOStatus(status)
	o.Priority = domain.Priority(priority)
	return o, nil
}

// ============================================================
// TransferOrderItemRepo
// ============================================================

// TransferOrderItemRepo manages branch_transfer_order_items persistence.
type TransferOrderItemRepo struct{}

// NewTransferOrderItemRepo constructs a TransferOrderItemRepo for fx injection.
func NewTransferOrderItemRepo() *TransferOrderItemRepo { return &TransferOrderItemRepo{} }

// Add inserts a BTO line item.
func (r *TransferOrderItemRepo) Add(ctx context.Context, tx pgx.Tx, item domain.BranchTransferOrderItem) (domain.BranchTransferOrderItem, error) {
	const q = `
		INSERT INTO branch_transfer_order_items
		    (tenant_id, transfer_order_id, stock_item_id, requested_qty, approved_qty, unit, note)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		RETURNING id, tenant_id, transfer_order_id, stock_item_id, requested_qty, approved_qty,
		          shipped_qty, received_qty, unit, COALESCE(note, '')`

	row := tx.QueryRow(ctx, q, item.TenantID, item.TransferOrderID, item.StockItemID,
		item.RequestedQty, item.ApprovedQty, item.Unit, emptyToNil(item.Note))
	return scanTransferOrderItem(row)
}

// ListByTransferOrder returns all line items for a BTO.
func (r *TransferOrderItemRepo) ListByTransferOrder(ctx context.Context, tx pgx.Tx, transferOrderID uuid.UUID) ([]domain.BranchTransferOrderItem, error) {
	const q = `
		SELECT id, tenant_id, transfer_order_id, stock_item_id, requested_qty, approved_qty,
		       shipped_qty, received_qty, unit, COALESCE(note, '')
		FROM branch_transfer_order_items
		WHERE transfer_order_id = $1
		ORDER BY stock_item_id`

	rows, err := tx.Query(ctx, q, transferOrderID)
	if err != nil {
		return nil, fmt.Errorf("inventory/repo/transfer_order_item: list: %w", err)
	}
	defer rows.Close()

	var out []domain.BranchTransferOrderItem
	for rows.Next() {
		item, err := scanTransferOrderItem(rows)
		if err != nil {
			return nil, fmt.Errorf("inventory/repo/transfer_order_item: list scan: %w", err)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// SetApprovedQty sets the approved quantity for a BTO line item (partial approval).
func (r *TransferOrderItemRepo) SetApprovedQty(ctx context.Context, tx pgx.Tx, id uuid.UUID, qty float64) error {
	const q = `UPDATE branch_transfer_order_items SET approved_qty = $1 WHERE id = $2`
	tag, err := tx.Exec(ctx, q, qty, id)
	if err != nil {
		return fmt.Errorf("inventory/repo/transfer_order_item: set approved qty: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// AddShippedQty increments shipped_qty by delta (denormalized from Shipment;
// ADR-DATA-006). Called only by the service-layer shipment-event path.
func (r *TransferOrderItemRepo) AddShippedQty(ctx context.Context, tx pgx.Tx, transferOrderID, stockItemID uuid.UUID, delta float64) error {
	const q = `
		UPDATE branch_transfer_order_items
		SET shipped_qty = shipped_qty + $1
		WHERE transfer_order_id = $2 AND stock_item_id = $3`
	tag, err := tx.Exec(ctx, q, delta, transferOrderID, stockItemID)
	if err != nil {
		return fmt.Errorf("inventory/repo/transfer_order_item: add shipped qty: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// AddReceivedQty increments received_qty by delta (denormalized from Shipment;
// ADR-DATA-006). Called only by the service-layer shipment-event path.
func (r *TransferOrderItemRepo) AddReceivedQty(ctx context.Context, tx pgx.Tx, transferOrderID, stockItemID uuid.UUID, delta float64) error {
	const q = `
		UPDATE branch_transfer_order_items
		SET received_qty = received_qty + $1
		WHERE transfer_order_id = $2 AND stock_item_id = $3`
	tag, err := tx.Exec(ctx, q, delta, transferOrderID, stockItemID)
	if err != nil {
		return fmt.Errorf("inventory/repo/transfer_order_item: add received qty: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// AllReceived reports whether every line item of the BTO has received_qty >=
// approved_qty (or requested_qty if never approved) — used to auto-close.
func (r *TransferOrderItemRepo) AllReceived(ctx context.Context, tx pgx.Tx, transferOrderID uuid.UUID) (bool, error) {
	const q = `
		SELECT COUNT(*) = 0
		FROM branch_transfer_order_items
		WHERE transfer_order_id = $1
		  AND received_qty < COALESCE(approved_qty, requested_qty)`

	var allReceived bool
	if err := tx.QueryRow(ctx, q, transferOrderID).Scan(&allReceived); err != nil {
		return false, fmt.Errorf("inventory/repo/transfer_order_item: all received: %w", err)
	}
	return allReceived, nil
}

func scanTransferOrderItem(s pgx.Row) (domain.BranchTransferOrderItem, error) {
	var item domain.BranchTransferOrderItem
	err := s.Scan(&item.ID, &item.TenantID, &item.TransferOrderID, &item.StockItemID,
		&item.RequestedQty, &item.ApprovedQty, &item.ShippedQty, &item.ReceivedQty,
		&item.Unit, &item.Note)
	if err != nil {
		return domain.BranchTransferOrderItem{}, err
	}
	return item, nil
}
