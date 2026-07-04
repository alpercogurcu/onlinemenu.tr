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

// ShipmentRepo manages shipments persistence.
type ShipmentRepo struct{}

// NewShipmentRepo constructs a ShipmentRepo for fx injection.
func NewShipmentRepo() *ShipmentRepo { return &ShipmentRepo{} }

// Create inserts a new shipment (status defaults to draft).
func (r *ShipmentRepo) Create(ctx context.Context, tx pgx.Tx, s domain.Shipment) (domain.Shipment, error) {
	const q = `
		INSERT INTO shipments
		    (tenant_id, from_warehouse_id, to_branch_id, transfer_order_id, status, priority, note, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING id, tenant_id, from_warehouse_id, to_branch_id, transfer_order_id, status, priority,
		          COALESCE(note, ''), created_by, shipped_at, received_at, created_at, updated_at`

	row := tx.QueryRow(ctx, q,
		s.TenantID, s.FromWarehouseID, s.ToBranchID, s.TransferOrderID,
		string(s.Status), string(s.Priority), emptyToNil(s.Note), s.CreatedBy,
	)
	return scanShipment(row)
}

// GetByID fetches a single shipment by primary key within the RLS tenant context.
func (r *ShipmentRepo) GetByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (domain.Shipment, error) {
	const q = `
		SELECT id, tenant_id, from_warehouse_id, to_branch_id, transfer_order_id, status, priority,
		       COALESCE(note, ''), created_by, shipped_at, received_at, created_at, updated_at
		FROM shipments WHERE id = $1`

	row := tx.QueryRow(ctx, q, id)
	s, err := scanShipment(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Shipment{}, ErrNotFound
		}
		return domain.Shipment{}, fmt.Errorf("inventory/repo/shipment: get by id: %w", err)
	}
	return s, nil
}

// ListByWarehouse returns shipments originating from a warehouse, newest first.
func (r *ShipmentRepo) ListByWarehouse(ctx context.Context, tx pgx.Tx, warehouseID uuid.UUID) ([]domain.Shipment, error) {
	const q = `
		SELECT id, tenant_id, from_warehouse_id, to_branch_id, transfer_order_id, status, priority,
		       COALESCE(note, ''), created_by, shipped_at, received_at, created_at, updated_at
		FROM shipments
		WHERE from_warehouse_id = $1
		ORDER BY created_at DESC`

	rows, err := tx.Query(ctx, q, warehouseID)
	if err != nil {
		return nil, fmt.Errorf("inventory/repo/shipment: list by warehouse: %w", err)
	}
	defer rows.Close()

	var out []domain.Shipment
	for rows.Next() {
		s, err := scanShipment(rows)
		if err != nil {
			return nil, fmt.Errorf("inventory/repo/shipment: list by warehouse scan: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ListByTransferOrder returns all shipments linked to a BTO.
func (r *ShipmentRepo) ListByTransferOrder(ctx context.Context, tx pgx.Tx, transferOrderID uuid.UUID) ([]domain.Shipment, error) {
	const q = `
		SELECT id, tenant_id, from_warehouse_id, to_branch_id, transfer_order_id, status, priority,
		       COALESCE(note, ''), created_by, shipped_at, received_at, created_at, updated_at
		FROM shipments
		WHERE transfer_order_id = $1
		ORDER BY created_at`

	rows, err := tx.Query(ctx, q, transferOrderID)
	if err != nil {
		return nil, fmt.Errorf("inventory/repo/shipment: list by transfer order: %w", err)
	}
	defer rows.Close()

	var out []domain.Shipment
	for rows.Next() {
		s, err := scanShipment(rows)
		if err != nil {
			return nil, fmt.Errorf("inventory/repo/shipment: list by transfer order scan: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// UpdateStatus persists a shipment's status transition. shippedAt/receivedAt
// are set by the caller according to the target status (nil otherwise).
func (r *ShipmentRepo) UpdateStatus(ctx context.Context, tx pgx.Tx, id uuid.UUID, status domain.ShipmentStatus, shippedAt, receivedAt *time.Time) (domain.Shipment, error) {
	const q = `
		UPDATE shipments SET
			status = $1,
			shipped_at  = COALESCE($2, shipped_at),
			received_at = COALESCE($3, received_at),
			updated_at = NOW()
		WHERE id = $4
		RETURNING id, tenant_id, from_warehouse_id, to_branch_id, transfer_order_id, status, priority,
		          COALESCE(note, ''), created_by, shipped_at, received_at, created_at, updated_at`

	row := tx.QueryRow(ctx, q, string(status), shippedAt, receivedAt, id)
	updated, err := scanShipment(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Shipment{}, ErrNotFound
		}
		return domain.Shipment{}, fmt.Errorf("inventory/repo/shipment: update status: %w", err)
	}
	return updated, nil
}

func scanShipment(s pgx.Row) (domain.Shipment, error) {
	var sh domain.Shipment
	var status, priority string
	err := s.Scan(
		&sh.ID, &sh.TenantID, &sh.FromWarehouseID, &sh.ToBranchID, &sh.TransferOrderID,
		&status, &priority, &sh.Note, &sh.CreatedBy, &sh.ShippedAt, &sh.ReceivedAt,
		&sh.CreatedAt, &sh.UpdatedAt,
	)
	if err != nil {
		return domain.Shipment{}, err
	}
	sh.Status = domain.ShipmentStatus(status)
	sh.Priority = domain.Priority(priority)
	return sh, nil
}

// ============================================================
// ShipmentItemRepo
// ============================================================

// ShipmentItemRepo manages shipment_items persistence.
type ShipmentItemRepo struct{}

// NewShipmentItemRepo constructs a ShipmentItemRepo for fx injection.
func NewShipmentItemRepo() *ShipmentItemRepo { return &ShipmentItemRepo{} }

// Add inserts a shipment line item. UnitPrice/Currency are copied from the
// linked BTO item (or overridden) by ShipmentService.Create and frozen from
// then on (ADR-DATA-006 eklenti).
func (r *ShipmentItemRepo) Add(ctx context.Context, tx pgx.Tx, item domain.ShipmentItem) (domain.ShipmentItem, error) {
	const q = `
		INSERT INTO shipment_items (shipment_id, tenant_id, stock_item_id, requested_qty, shipped_qty, received_qty, unit, unit_price, currency)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		RETURNING shipment_id, tenant_id, stock_item_id, requested_qty, shipped_qty, received_qty, unit, unit_price, currency`

	row := tx.QueryRow(ctx, q, item.ShipmentID, item.TenantID, item.StockItemID,
		item.RequestedQty, item.ShippedQty, item.ReceivedQty, item.Unit, item.UnitPrice, item.Currency)
	return scanShipmentItem(row)
}

// ListByShipment returns all line items for a shipment.
func (r *ShipmentItemRepo) ListByShipment(ctx context.Context, tx pgx.Tx, shipmentID uuid.UUID) ([]domain.ShipmentItem, error) {
	const q = `
		SELECT shipment_id, tenant_id, stock_item_id, requested_qty, shipped_qty, received_qty, unit, unit_price, currency
		FROM shipment_items
		WHERE shipment_id = $1
		ORDER BY stock_item_id`

	rows, err := tx.Query(ctx, q, shipmentID)
	if err != nil {
		return nil, fmt.Errorf("inventory/repo/shipment_item: list by shipment: %w", err)
	}
	defer rows.Close()

	var out []domain.ShipmentItem
	for rows.Next() {
		item, err := scanShipmentItem(rows)
		if err != nil {
			return nil, fmt.Errorf("inventory/repo/shipment_item: list by shipment scan: %w", err)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// SetShippedQty sets the shipped_qty for a line item (called when a shipment
// transitions to in_transit).
func (r *ShipmentItemRepo) SetShippedQty(ctx context.Context, tx pgx.Tx, shipmentID, stockItemID uuid.UUID, qty float64) error {
	const q = `UPDATE shipment_items SET shipped_qty = $1 WHERE shipment_id = $2 AND stock_item_id = $3`
	tag, err := tx.Exec(ctx, q, qty, shipmentID, stockItemID)
	if err != nil {
		return fmt.Errorf("inventory/repo/shipment_item: set shipped qty: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetReceivedQty sets the received_qty for a line item (called when a
// shipment transitions to received).
func (r *ShipmentItemRepo) SetReceivedQty(ctx context.Context, tx pgx.Tx, shipmentID, stockItemID uuid.UUID, qty float64) error {
	const q = `UPDATE shipment_items SET received_qty = $1 WHERE shipment_id = $2 AND stock_item_id = $3`
	tag, err := tx.Exec(ctx, q, qty, shipmentID, stockItemID)
	if err != nil {
		return fmt.Errorf("inventory/repo/shipment_item: set received qty: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func scanShipmentItem(s pgx.Row) (domain.ShipmentItem, error) {
	var item domain.ShipmentItem
	err := s.Scan(&item.ShipmentID, &item.TenantID, &item.StockItemID,
		&item.RequestedQty, &item.ShippedQty, &item.ReceivedQty, &item.Unit,
		&item.UnitPrice, &item.Currency)
	if err != nil {
		return domain.ShipmentItem{}, err
	}
	return item, nil
}
