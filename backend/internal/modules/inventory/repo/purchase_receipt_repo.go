package repo

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"onlinemenu.tr/internal/modules/inventory/domain"
)

// PurchaseReceiptRepo manages purchase_receipts persistence (ADR-DATA-007
// karar 3). Receipts are immutable: no Update method (a correction is a new
// receipt), mirroring StockMovementRepo and SupplyPolicyRepo.
type PurchaseReceiptRepo struct{}

// NewPurchaseReceiptRepo constructs a PurchaseReceiptRepo for fx injection.
func NewPurchaseReceiptRepo() *PurchaseReceiptRepo { return &PurchaseReceiptRepo{} }

// Create inserts a new purchase receipt. The caller is responsible for
// generating receipt.ID (client-side UUIDv7; mirrors supply_policies'/
// stock_items' convention).
func (r *PurchaseReceiptRepo) Create(ctx context.Context, tx pgx.Tx, receipt domain.PurchaseReceipt) (domain.PurchaseReceipt, error) {
	const q = `
		INSERT INTO purchase_receipts (
			id, tenant_id, warehouse_id, supplier_party_id, supplier_name,
			receipt_no, receipt_date, total, currency, note, created_by
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		RETURNING id, tenant_id, warehouse_id, supplier_party_id, COALESCE(supplier_name, ''),
			COALESCE(receipt_no, ''), receipt_date, total, currency, COALESCE(note, ''),
			created_by, created_at`

	row := tx.QueryRow(ctx, q,
		receipt.ID, receipt.TenantID, receipt.WarehouseID, receipt.SupplierPartyID,
		emptyToNil(receipt.SupplierName), emptyToNil(receipt.ReceiptNo), receipt.ReceiptDate,
		receipt.Total, receipt.Currency, emptyToNil(receipt.Note), receipt.CreatedBy,
	)
	return scanPurchaseReceipt(row)
}

// GetByID fetches a single purchase receipt by primary key within the RLS
// tenant context.
func (r *PurchaseReceiptRepo) GetByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (domain.PurchaseReceipt, error) {
	const q = `
		SELECT id, tenant_id, warehouse_id, supplier_party_id, COALESCE(supplier_name, ''),
			COALESCE(receipt_no, ''), receipt_date, total, currency, COALESCE(note, ''),
			created_by, created_at
		FROM purchase_receipts WHERE id = $1`

	row := tx.QueryRow(ctx, q, id)
	rcpt, err := scanPurchaseReceipt(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.PurchaseReceipt{}, ErrNotFound
		}
		return domain.PurchaseReceipt{}, fmt.Errorf("inventory/repo/purchase_receipt: get by id: %w", err)
	}
	return rcpt, nil
}

// ListByWarehouse returns purchase receipts recorded against a warehouse,
// newest first.
func (r *PurchaseReceiptRepo) ListByWarehouse(ctx context.Context, tx pgx.Tx, warehouseID uuid.UUID) ([]domain.PurchaseReceipt, error) {
	const q = `
		SELECT id, tenant_id, warehouse_id, supplier_party_id, COALESCE(supplier_name, ''),
			COALESCE(receipt_no, ''), receipt_date, total, currency, COALESCE(note, ''),
			created_by, created_at
		FROM purchase_receipts
		WHERE warehouse_id = $1
		ORDER BY created_at DESC`

	rows, err := tx.Query(ctx, q, warehouseID)
	if err != nil {
		return nil, fmt.Errorf("inventory/repo/purchase_receipt: list by warehouse: %w", err)
	}
	defer rows.Close()

	var out []domain.PurchaseReceipt
	for rows.Next() {
		rcpt, err := scanPurchaseReceipt(rows)
		if err != nil {
			return nil, fmt.Errorf("inventory/repo/purchase_receipt: list by warehouse scan: %w", err)
		}
		out = append(out, rcpt)
	}
	return out, rows.Err()
}

func scanPurchaseReceipt(s pgx.Row) (domain.PurchaseReceipt, error) {
	var rcpt domain.PurchaseReceipt
	err := s.Scan(
		&rcpt.ID, &rcpt.TenantID, &rcpt.WarehouseID, &rcpt.SupplierPartyID, &rcpt.SupplierName,
		&rcpt.ReceiptNo, &rcpt.ReceiptDate, &rcpt.Total, &rcpt.Currency, &rcpt.Note,
		&rcpt.CreatedBy, &rcpt.CreatedAt,
	)
	if err != nil {
		return domain.PurchaseReceipt{}, err
	}
	return rcpt, nil
}

// ============================================================
// PurchaseReceiptItemRepo
// ============================================================

// PurchaseReceiptItemRepo manages purchase_receipt_items persistence.
type PurchaseReceiptItemRepo struct{}

// NewPurchaseReceiptItemRepo constructs a PurchaseReceiptItemRepo for fx injection.
func NewPurchaseReceiptItemRepo() *PurchaseReceiptItemRepo { return &PurchaseReceiptItemRepo{} }

// Add inserts a purchase receipt line item. The caller is responsible for
// generating item.ID (client-side UUIDv7).
func (r *PurchaseReceiptItemRepo) Add(ctx context.Context, tx pgx.Tx, item domain.PurchaseReceiptItem) (domain.PurchaseReceiptItem, error) {
	const q = `
		INSERT INTO purchase_receipt_items (id, tenant_id, receipt_id, stock_item_id, quantity, unit, unit_price, line_total, brand)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		RETURNING id, tenant_id, receipt_id, stock_item_id, quantity, unit, unit_price, line_total, COALESCE(brand, '')`

	row := tx.QueryRow(ctx, q, item.ID, item.TenantID, item.ReceiptID, item.StockItemID,
		item.Quantity, item.Unit, item.UnitPrice, item.LineTotal, emptyToNil(item.Brand))
	return scanPurchaseReceiptItem(row)
}

// ListByReceipt returns all line items for a purchase receipt.
func (r *PurchaseReceiptItemRepo) ListByReceipt(ctx context.Context, tx pgx.Tx, receiptID uuid.UUID) ([]domain.PurchaseReceiptItem, error) {
	const q = `
		SELECT id, tenant_id, receipt_id, stock_item_id, quantity, unit, unit_price, line_total, COALESCE(brand, '')
		FROM purchase_receipt_items
		WHERE receipt_id = $1
		ORDER BY id`

	rows, err := tx.Query(ctx, q, receiptID)
	if err != nil {
		return nil, fmt.Errorf("inventory/repo/purchase_receipt_item: list by receipt: %w", err)
	}
	defer rows.Close()

	var out []domain.PurchaseReceiptItem
	for rows.Next() {
		item, err := scanPurchaseReceiptItem(rows)
		if err != nil {
			return nil, fmt.Errorf("inventory/repo/purchase_receipt_item: list by receipt scan: %w", err)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func scanPurchaseReceiptItem(s pgx.Row) (domain.PurchaseReceiptItem, error) {
	var item domain.PurchaseReceiptItem
	err := s.Scan(&item.ID, &item.TenantID, &item.ReceiptID, &item.StockItemID,
		&item.Quantity, &item.Unit, &item.UnitPrice, &item.LineTotal, &item.Brand)
	if err != nil {
		return domain.PurchaseReceiptItem{}, err
	}
	return item, nil
}
