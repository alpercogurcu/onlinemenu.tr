// Package repo provides persistence for the billing module.
package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"onlinemenu.tr/internal/modules/billing/domain"
)

// ErrNotFound is returned when an invoice row is not found.
var ErrNotFound = errors.New("billing/repo: not found")

// ErrDuplicateIdempotencyKey is returned when the idempotency key already exists.
var ErrDuplicateIdempotencyKey = errors.New("billing/repo: duplicate idempotency key")

// InvoiceRepo handles persistence for Invoice aggregates.
type InvoiceRepo struct{}

// NewInvoiceRepo constructs an InvoiceRepo for fx injection.
func NewInvoiceRepo() *InvoiceRepo { return &InvoiceRepo{} }

// Create inserts a new invoice and its items in the same transaction.
func (r *InvoiceRepo) Create(ctx context.Context, tx pgx.Tx, inv domain.Invoice) (domain.Invoice, error) {
	inv.ID = uuid.New()
	inv.Status = domain.InvoiceStatusDraft
	now := time.Now().UTC()
	inv.CreatedAt = now
	inv.UpdatedAt = now
	if inv.GibUUID == uuid.Nil {
		inv.GibUUID = uuid.New()
	}
	if inv.IssueDate.IsZero() {
		inv.IssueDate = now
	}
	if inv.Currency == "" {
		inv.Currency = "TRY"
	}

	_, err := tx.Exec(ctx, `
		INSERT INTO invoices (
			id, tenant_id, branch_id, invoice_type, status,
			check_id, payment_id, idempotency_key,
			invoice_number, gib_uuid, external_id,
			supplier_vkn, supplier_name, supplier_alias,
			customer_vkn, customer_name, customer_alias,
			amount_excluding_tax, tax_amount, amount_total, currency,
			issue_date, created_at, updated_at
		) VALUES (
			$1,$2,$3,$4,$5,
			$6,$7,$8,
			$9,$10,$11,
			$12,$13,$14,
			$15,$16,$17,
			$18,$19,$20,$21,
			$22,$23,$24
		)`,
		inv.ID, inv.TenantID, inv.BranchID, string(inv.InvoiceType), string(inv.Status),
		inv.CheckID, inv.PaymentID, inv.IdempotencyKey,
		inv.InvoiceNumber, inv.GibUUID, inv.ExternalID,
		inv.SupplierVKN, inv.SupplierName, inv.SupplierAlias,
		inv.CustomerVKN, inv.CustomerName, inv.CustomerAlias,
		inv.AmountExcludingTax, inv.TaxAmount, inv.AmountTotal, inv.Currency,
		inv.IssueDate.Format("2006-01-02"), inv.CreatedAt, inv.UpdatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return domain.Invoice{}, ErrDuplicateIdempotencyKey
		}
		return domain.Invoice{}, fmt.Errorf("billing/repo: create invoice: %w", err)
	}

	for i := range inv.Items {
		item := &inv.Items[i]
		item.ID = uuid.New()
		item.InvoiceID = inv.ID
		item.TenantID = inv.TenantID
		item.CreatedAt = now

		_, err := tx.Exec(ctx, `
			INSERT INTO invoice_items (
				id, invoice_id, tenant_id,
				product_id, product_name, quantity,
				unit_price_amount, tax_rate_bps,
				line_total, tax_amount, created_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
			item.ID, item.InvoiceID, item.TenantID,
			item.ProductID, item.ProductName, item.Quantity,
			item.UnitPriceAmount, item.TaxRateBPS,
			item.LineTotal, item.TaxAmount, item.CreatedAt,
		)
		if err != nil {
			return domain.Invoice{}, fmt.Errorf("billing/repo: create invoice item: %w", err)
		}
	}

	return inv, nil
}

// GetByID returns the invoice with its items.
func (r *InvoiceRepo) GetByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (domain.Invoice, error) {
	row := tx.QueryRow(ctx, `
		SELECT id, tenant_id, branch_id, invoice_type, status,
		       check_id, payment_id, idempotency_key,
		       invoice_number, gib_uuid, external_id,
		       supplier_vkn, supplier_name, supplier_alias,
		       customer_vkn, customer_name, customer_alias,
		       amount_excluding_tax, tax_amount, amount_total, currency,
		       issue_date, submitted_at, accepted_at, rejected_at, rejection_reason,
		       created_at, updated_at
		FROM invoices WHERE id = $1`, id)

	inv, err := scanInvoice(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Invoice{}, ErrNotFound
	}
	if err != nil {
		return domain.Invoice{}, err
	}

	items, err := r.listItems(ctx, tx, inv.ID)
	if err != nil {
		return domain.Invoice{}, err
	}
	inv.Items = items
	return inv, nil
}

// GetByIdempotencyKey returns the invoice matching the tenant-scoped key.
func (r *InvoiceRepo) GetByIdempotencyKey(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, key string) (domain.Invoice, error) {
	row := tx.QueryRow(ctx, `
		SELECT id, tenant_id, branch_id, invoice_type, status,
		       check_id, payment_id, idempotency_key,
		       invoice_number, gib_uuid, external_id,
		       supplier_vkn, supplier_name, supplier_alias,
		       customer_vkn, customer_name, customer_alias,
		       amount_excluding_tax, tax_amount, amount_total, currency,
		       issue_date, submitted_at, accepted_at, rejected_at, rejection_reason,
		       created_at, updated_at
		FROM invoices WHERE tenant_id = $1 AND idempotency_key = $2`, tenantID, key)

	inv, err := scanInvoice(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Invoice{}, ErrNotFound
	}
	return inv, err
}

// UpdateStatus persists status transitions and timestamps.
func (r *InvoiceRepo) UpdateStatus(ctx context.Context, tx pgx.Tx, id uuid.UUID, status domain.InvoiceStatus, externalID string, submittedAt, acceptedAt, rejectedAt *time.Time, rejectionReason string) error {
	_, err := tx.Exec(ctx, `
		UPDATE invoices SET
			status = $2, external_id = $3,
			submitted_at = $4, accepted_at = $5,
			rejected_at = $6, rejection_reason = $7,
			updated_at = now()
		WHERE id = $1`,
		id, string(status), externalID,
		submittedAt, acceptedAt, rejectedAt, rejectionReason,
	)
	if err != nil {
		return fmt.Errorf("billing/repo: update status: %w", err)
	}
	return nil
}

// SetInvoiceNumber sets the invoice_number after sequence allocation.
func (r *InvoiceRepo) SetInvoiceNumber(ctx context.Context, tx pgx.Tx, id uuid.UUID, number string) error {
	_, err := tx.Exec(ctx, `UPDATE invoices SET invoice_number = $2, updated_at = now() WHERE id = $1`, id, number)
	if err != nil {
		return fmt.Errorf("billing/repo: set invoice number: %w", err)
	}
	return nil
}

// List returns invoices for the given tenant, ordered by creation time descending.
func (r *InvoiceRepo) List(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, limit, offset int) ([]domain.Invoice, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := tx.Query(ctx, `
		SELECT id, tenant_id, branch_id, invoice_type, status,
		       check_id, payment_id, idempotency_key,
		       invoice_number, gib_uuid, external_id,
		       supplier_vkn, supplier_name, supplier_alias,
		       customer_vkn, customer_name, customer_alias,
		       amount_excluding_tax, tax_amount, amount_total, currency,
		       issue_date, submitted_at, accepted_at, rejected_at, rejection_reason,
		       created_at, updated_at
		FROM invoices WHERE tenant_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3`, tenantID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("billing/repo: list: %w", err)
	}
	defer rows.Close()

	var invoices []domain.Invoice
	for rows.Next() {
		inv, err := scanInvoice(rows)
		if err != nil {
			return nil, err
		}
		invoices = append(invoices, inv)
	}
	return invoices, rows.Err()
}

// NextInvoiceSequence returns the next integer to use for invoice_number generation.
// Uses a simple COUNT-based approach for Faz 1; replace with a sequence in Faz 2 if needed.
func (r *InvoiceRepo) NextInvoiceSequence(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, year int) (int, error) {
	var count int
	err := tx.QueryRow(ctx, `
		SELECT COUNT(*) FROM invoices
		WHERE tenant_id = $1 AND EXTRACT(YEAR FROM issue_date) = $2`,
		tenantID, year).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("billing/repo: next sequence: %w", err)
	}
	return count + 1, nil
}

func (r *InvoiceRepo) listItems(ctx context.Context, tx pgx.Tx, invoiceID uuid.UUID) ([]domain.InvoiceItem, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, invoice_id, tenant_id,
		       product_id, product_name, quantity,
		       unit_price_amount, tax_rate_bps, line_total, tax_amount, created_at
		FROM invoice_items WHERE invoice_id = $1 ORDER BY created_at`, invoiceID)
	if err != nil {
		return nil, fmt.Errorf("billing/repo: list items: %w", err)
	}
	defer rows.Close()

	var items []domain.InvoiceItem
	for rows.Next() {
		var item domain.InvoiceItem
		if err := rows.Scan(
			&item.ID, &item.InvoiceID, &item.TenantID,
			&item.ProductID, &item.ProductName, &item.Quantity,
			&item.UnitPriceAmount, &item.TaxRateBPS, &item.LineTotal, &item.TaxAmount, &item.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("billing/repo: scan item: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func scanInvoice(row interface {
	Scan(dest ...any) error
}) (domain.Invoice, error) {
	var inv domain.Invoice
	err := row.Scan(
		&inv.ID, &inv.TenantID, &inv.BranchID, &inv.InvoiceType, &inv.Status,
		&inv.CheckID, &inv.PaymentID, &inv.IdempotencyKey,
		&inv.InvoiceNumber, &inv.GibUUID, &inv.ExternalID,
		&inv.SupplierVKN, &inv.SupplierName, &inv.SupplierAlias,
		&inv.CustomerVKN, &inv.CustomerName, &inv.CustomerAlias,
		&inv.AmountExcludingTax, &inv.TaxAmount, &inv.AmountTotal, &inv.Currency,
		&inv.IssueDate, &inv.SubmittedAt, &inv.AcceptedAt, &inv.RejectedAt, &inv.RejectionReason,
		&inv.CreatedAt, &inv.UpdatedAt,
	)
	if err != nil {
		return domain.Invoice{}, fmt.Errorf("billing/repo: scan invoice: %w", err)
	}
	return inv, nil
}

// isUniqueViolation returns true for PostgreSQL unique constraint violations (code 23505).
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "23505") || strings.Contains(msg, "unique_violation") || strings.Contains(msg, "duplicate key")
}
