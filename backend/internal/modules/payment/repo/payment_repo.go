// Package repo provides persistence for the payment module.
package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"onlinemenu.tr/internal/modules/payment/domain"
)

// ErrNotFound is returned when a payment row is not found.
var ErrNotFound = errors.New("payment/repo: not found")

// ErrDuplicateIdempotencyKey is returned when the idempotency key already exists.
var ErrDuplicateIdempotencyKey = errors.New("payment/repo: duplicate idempotency key")

// PaymentRepo handles persistence for Payment and FiscalReceipt aggregates.
type PaymentRepo struct{}

func NewPaymentRepo() *PaymentRepo { return &PaymentRepo{} }

// Create inserts a new payment row and returns the persisted record.
func (r *PaymentRepo) Create(ctx context.Context, tx pgx.Tx, p domain.Payment) (domain.Payment, error) {
	p.ID = uuid.New()
	p.Status = domain.PaymentStatusPending
	p.CreatedAt = time.Now().UTC()

	_, err := tx.Exec(ctx, `
		INSERT INTO payments
			(id, tenant_id, branch_id, check_id, idempotency_key, method, status,
			 amount_total, currency, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
	`, p.ID, p.TenantID, p.BranchID, p.CheckID, p.IdempotencyKey,
		string(p.Method), string(p.Status), p.AmountTotal, p.Currency, p.CreatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return domain.Payment{}, ErrDuplicateIdempotencyKey
		}
		return domain.Payment{}, fmt.Errorf("payment/repo: create: %w", err)
	}
	return p, nil
}

// GetByID returns the payment with the given ID.
func (r *PaymentRepo) GetByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (domain.Payment, error) {
	row := tx.QueryRow(ctx, `
		SELECT id, tenant_id, branch_id, check_id, idempotency_key,
		       method, status, amount_total, currency, fiscal_receipt_id,
		       created_at, completed_at
		FROM payments WHERE id = $1
	`, id)
	p, err := scanPayment(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Payment{}, ErrNotFound
	}
	return p, err
}

// GetByIdempotencyKey returns the payment matching the tenant-scoped key.
func (r *PaymentRepo) GetByIdempotencyKey(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, key string) (domain.Payment, error) {
	row := tx.QueryRow(ctx, `
		SELECT id, tenant_id, branch_id, check_id, idempotency_key,
		       method, status, amount_total, currency, fiscal_receipt_id,
		       created_at, completed_at
		FROM payments WHERE tenant_id = $1 AND idempotency_key = $2
	`, tenantID, key)
	p, err := scanPayment(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Payment{}, ErrNotFound
	}
	return p, err
}

// Complete marks the payment as completed and links the fiscal receipt.
func (r *PaymentRepo) Complete(ctx context.Context, tx pgx.Tx, paymentID, receiptID uuid.UUID) error {
	now := time.Now().UTC()
	// status guard: double completion is already blocked one layer up by the
	// submission's MarkResult transition, but a completed/voided payment must
	// be structurally impossible to overwrite even if that layer regresses.
	tag, err := tx.Exec(ctx, `
		UPDATE payments
		SET status = 'completed', fiscal_receipt_id = $2, completed_at = $3
		WHERE id = $1 AND status = 'pending'
	`, paymentID, receiptID, now)
	if err != nil {
		return fmt.Errorf("payment/repo: complete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Fail marks a pending payment as failed after the fiscal registration was
// rejected. Only pending payments can fail — a completed one carries money and
// must be voided instead.
func (r *PaymentRepo) Fail(ctx context.Context, tx pgx.Tx, paymentID uuid.UUID) error {
	tag, err := tx.Exec(ctx, `
		UPDATE payments SET status = 'failed' WHERE id = $1 AND status = 'pending'
	`, paymentID)
	if err != nil {
		return fmt.Errorf("payment/repo: fail: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Void marks a payment as voided (fiş iptali). A void may cancel either a
// registration that never completed or one whose receipt was cancelled on the
// device afterwards, hence both source states.
func (r *PaymentRepo) Void(ctx context.Context, tx pgx.Tx, paymentID uuid.UUID) error {
	tag, err := tx.Exec(ctx, `
		UPDATE payments SET status = 'voided'
		WHERE id = $1 AND status IN ('pending', 'completed')
	`, paymentID)
	if err != nil {
		return fmt.Errorf("payment/repo: void: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// InsertFiscalReceipt persists a fiscal receipt and returns its ID.
// ZNo and VendorRef have no dedicated columns; they are folded into the
// receipt_data JSONB document under z_no / vendor_ref.
func (r *PaymentRepo) InsertFiscalReceipt(ctx context.Context, tx pgx.Tx, rec domain.FiscalReceipt) (uuid.UUID, error) {
	id := uuid.New()
	data, err := json.Marshal(mergeReceiptData(rec))
	if err != nil {
		return uuid.Nil, fmt.Errorf("payment/repo: marshal receipt data: %w", err)
	}
	// Pass as string so pgx sends it as text; Postgres coerces text → JSONB.
	// Passing []byte in simple-protocol mode would send hex-bytea, not valid JSON.
	_, err = tx.Exec(ctx, `
		INSERT INTO fiscal_receipts
			(id, tenant_id, payment_id, device_type, receipt_number, receipt_data, issued_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
	`, id, rec.TenantID, rec.PaymentID, rec.DeviceType, rec.ReceiptNumber, string(data), rec.IssuedAt)
	if err != nil {
		return uuid.Nil, fmt.Errorf("payment/repo: insert fiscal receipt: %w", err)
	}
	return id, nil
}

// ListByTenant returns payments for a tenant ordered by created_at desc.
func (r *PaymentRepo) ListByTenant(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, limit, offset int) ([]domain.Payment, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, tenant_id, branch_id, check_id, idempotency_key,
		       method, status, amount_total, currency, fiscal_receipt_id,
		       created_at, completed_at
		FROM payments
		WHERE tenant_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`, tenantID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("payment/repo: list by tenant: %w", err)
	}
	defer rows.Close()

	var payments []domain.Payment
	for rows.Next() {
		p, err := scanPayment(rows)
		if err != nil {
			return nil, fmt.Errorf("payment/repo: scan: %w", err)
		}
		payments = append(payments, p)
	}
	return payments, rows.Err()
}

// ListByCheck returns completed payments for a check, newest first. Used by
// POS to show already-recorded payments when a cashier reopens a check,
// guarding against double payment on the same check. Only completed payments
// are returned — pending/failed rows carry no money and would be misleading
// in that display, mirroring TotalPaidForCheck's status filter.
func (r *PaymentRepo) ListByCheck(ctx context.Context, tx pgx.Tx, tenantID, checkID uuid.UUID) ([]domain.Payment, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, tenant_id, branch_id, check_id, idempotency_key,
		       method, status, amount_total, currency, fiscal_receipt_id,
		       created_at, completed_at
		FROM payments
		WHERE tenant_id = $1 AND check_id = $2 AND status = 'completed'
		ORDER BY created_at DESC
	`, tenantID, checkID)
	if err != nil {
		return nil, fmt.Errorf("payment/repo: list by check: %w", err)
	}
	defer rows.Close()

	var payments []domain.Payment
	for rows.Next() {
		p, err := scanPayment(rows)
		if err != nil {
			return nil, fmt.Errorf("payment/repo: list by check scan: %w", err)
		}
		payments = append(payments, p)
	}
	return payments, rows.Err()
}

// TotalPaidForCheck returns the sum of completed payments for a check.
func (r *PaymentRepo) TotalPaidForCheck(ctx context.Context, tx pgx.Tx, tenantID, checkID uuid.UUID) (int64, error) {
	var total int64
	err := tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount_total), 0)
		FROM payments
		WHERE tenant_id = $1 AND check_id = $2 AND status = 'completed'
	`, tenantID, checkID).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("payment/repo: total paid for check: %w", err)
	}
	return total, nil
}

// PendingTotalForCheck returns the sum of payments for a check whose fiscal
// registration has not reached a terminal state yet.
//
// payments.status is the authoritative lifecycle here, not fiscal_submissions:
// RegisterSale writes the payment row and its submission row in one
// transaction, and every terminal fiscal outcome moves the payment out of
// 'pending' (OnFiscalResult -> Complete/Fail/Void). A join against
// fiscal_submissions could therefore only ever disagree when the worker left a
// payment stranded — a bug to fix at its source rather than to mask in a read
// that other modules depend on.
func (r *PaymentRepo) PendingTotalForCheck(ctx context.Context, tx pgx.Tx, tenantID, checkID uuid.UUID) (int64, error) {
	var total int64
	err := tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount_total), 0)
		FROM payments
		WHERE tenant_id = $1 AND check_id = $2 AND status = 'pending'
	`, tenantID, checkID).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("payment/repo: pending total for check: %w", err)
	}
	return total, nil
}

// InsertOutbox records a payment domain event within the caller's transaction.
func InsertOutbox(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, aggregateType, aggregateID, eventType string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("payment/repo: marshal outbox payload: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO payment_outbox (event_id, tenant_id, aggregate_type, aggregate_id, event_type, payload)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, uuid.New(), tenantID, aggregateType, aggregateID, eventType, string(data))
	if err != nil {
		return fmt.Errorf("payment/repo: insert outbox: %w", err)
	}
	return nil
}

// mergeReceiptData folds the ZNo/VendorRef fields into the receipt_data
// document. fiscal_receipts has no columns for them and the schema is
// deliberately kept stable, so the JSONB payload carries them instead. An
// explicit ReceiptData entry wins over the struct field: callers that already
// placed a value there are not silently overwritten.
func mergeReceiptData(rec domain.FiscalReceipt) map[string]any {
	data := make(map[string]any, len(rec.ReceiptData)+2)
	for k, v := range rec.ReceiptData {
		data[k] = v
	}
	if _, ok := data["z_no"]; !ok && rec.ZNo != "" {
		data["z_no"] = rec.ZNo
	}
	if _, ok := data["vendor_ref"]; !ok && rec.VendorRef != "" {
		data["vendor_ref"] = rec.VendorRef
	}
	return data
}

func scanPayment(row pgx.Row) (domain.Payment, error) {
	var p domain.Payment
	var method, status string
	err := row.Scan(
		&p.ID, &p.TenantID, &p.BranchID, &p.CheckID, &p.IdempotencyKey,
		&method, &status, &p.AmountTotal, &p.Currency, &p.FiscalReceiptID,
		&p.CreatedAt, &p.CompletedAt,
	)
	if err != nil {
		return domain.Payment{}, err
	}
	p.Method = domain.PaymentMethod(method)
	p.Status = domain.PaymentStatus(status)
	return p, nil
}

func isUniqueViolation(err error) bool {
	var pgErr interface{ SQLState() string }
	if errors.As(err, &pgErr) {
		return pgErr.SQLState() == "23505"
	}
	return false
}
