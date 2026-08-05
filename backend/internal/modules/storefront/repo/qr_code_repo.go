package repo

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"onlinemenu.tr/internal/modules/storefront/domain"
)

// QRCodeRepo manages storefront_qr_codes persistence.
type QRCodeRepo struct{}

func NewQRCodeRepo() *QRCodeRepo { return &QRCodeRepo{} }

// qrCodeColumns is the single projection every query selects, in the exact
// order scanQRCode reads them. Shared rather than repeated because scanQRCode
// takes ...any: a drifting column list compiles fine and fails only at runtime.
const qrCodeColumns = `id, tenant_id, branch_id, table_id, table_label, token_hash, status,
	          created_by, revoked_at, revoked_by, created_at, updated_at`

// LookupByTokenHash resolves a QR code from its token hash during the
// pre-tenant bootstrap read.
//
// tx MUST come from db.WithQRTokenLookupTx — that is the only transaction in
// which the qr_codes_read policy's token-hash branch is armed. Passing a
// tenant-scoped tx would silently return ErrNotFound for every other tenant's
// code, which is exactly the failure mode this doc comment exists to prevent.
//
// The query still filters on token_hash rather than relying on RLS alone:
// defense in depth, and it keeps the unique index in play.
func (r *QRCodeRepo) LookupByTokenHash(ctx context.Context, tx pgx.Tx, tokenHash string) (domain.QRCode, error) {
	const q = `SELECT ` + qrCodeColumns + ` FROM storefront_qr_codes WHERE token_hash = $1`

	c, err := scanQRCode(tx.QueryRow(ctx, q, tokenHash))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.QRCode{}, ErrNotFound
		}
		return domain.QRCode{}, fmt.Errorf("storefront/repo/qr: lookup by token hash: %w", err)
	}
	return c, nil
}

// Create inserts a new active QR code. Only the token's hash is written — the
// raw token never reaches this layer.
func (r *QRCodeRepo) Create(ctx context.Context, tx pgx.Tx, c domain.QRCode) (domain.QRCode, error) {
	const q = `
		INSERT INTO storefront_qr_codes
		    (tenant_id, branch_id, table_id, table_label, token_hash, status, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING ` + qrCodeColumns

	status := c.Status
	if status == "" {
		status = domain.QRCodeStatusActive
	}

	created, err := scanQRCode(tx.QueryRow(ctx, q,
		c.TenantID, c.BranchID, c.TableID, c.TableLabel, c.TokenHash, string(status), c.CreatedBy,
	))
	if err != nil {
		if isUniqueViolation(err) {
			return domain.QRCode{}, ErrTableAlreadyHasCode
		}
		return domain.QRCode{}, fmt.Errorf("storefront/repo/qr: create: %w", err)
	}
	return created, nil
}

// GetByID returns a QR code visible to the current tenant context.
func (r *QRCodeRepo) GetByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (domain.QRCode, error) {
	const q = `SELECT ` + qrCodeColumns + ` FROM storefront_qr_codes WHERE id = $1`

	c, err := scanQRCode(tx.QueryRow(ctx, q, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.QRCode{}, ErrNotFound
		}
		return domain.QRCode{}, fmt.Errorf("storefront/repo/qr: get by id: %w", err)
	}
	return c, nil
}

// GetForUpdate locks the QR code row for the caller's transaction, so a
// check-then-revoke sequence cannot race another revoke/rotate.
func (r *QRCodeRepo) GetForUpdate(ctx context.Context, tx pgx.Tx, id uuid.UUID) (domain.QRCode, error) {
	const q = `SELECT ` + qrCodeColumns + ` FROM storefront_qr_codes WHERE id = $1 FOR UPDATE`

	c, err := scanQRCode(tx.QueryRow(ctx, q, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.QRCode{}, ErrNotFound
		}
		return domain.QRCode{}, fmt.Errorf("storefront/repo/qr: get for update: %w", err)
	}
	return c, nil
}

// ListByBranch returns a branch's QR codes, active first then newest first.
func (r *QRCodeRepo) ListByBranch(ctx context.Context, tx pgx.Tx, branchID uuid.UUID) ([]domain.QRCode, error) {
	const q = `
		SELECT ` + qrCodeColumns + `
		FROM storefront_qr_codes
		WHERE branch_id = $1
		ORDER BY CASE status WHEN 'active' THEN 0 ELSE 1 END, created_at DESC`

	rows, err := tx.Query(ctx, q, branchID)
	if err != nil {
		return nil, fmt.Errorf("storefront/repo/qr: list by branch: %w", err)
	}
	defer rows.Close()

	var out []domain.QRCode
	for rows.Next() {
		c, err := scanQRCode(rows)
		if err != nil {
			return nil, fmt.Errorf("storefront/repo/qr: list by branch scan: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Revoke transitions a QR code to revoked, guarded on it still being active.
// Returns ErrInvalidTransition when the row was already revoked (0 rows
// affected), so a double revoke is reported rather than silently succeeding.
func (r *QRCodeRepo) Revoke(ctx context.Context, tx pgx.Tx, id, revokedBy uuid.UUID) (domain.QRCode, error) {
	const q = `
		UPDATE storefront_qr_codes
		SET status = 'revoked', revoked_at = NOW(), revoked_by = $2, updated_at = NOW()
		WHERE id = $1 AND status = 'active'
		RETURNING ` + qrCodeColumns

	c, err := scanQRCode(tx.QueryRow(ctx, q, id, revokedBy))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.QRCode{}, ErrInvalidTransition
		}
		return domain.QRCode{}, fmt.Errorf("storefront/repo/qr: revoke: %w", err)
	}
	return c, nil
}

// scanQRCode reads one QR code row from any RowScanner (QueryRow or rows).
func scanQRCode(s interface {
	Scan(...any) error
}) (domain.QRCode, error) {
	var c domain.QRCode
	var status string
	if err := s.Scan(
		&c.ID, &c.TenantID, &c.BranchID, &c.TableID, &c.TableLabel, &c.TokenHash, &status,
		&c.CreatedBy, &c.RevokedAt, &c.RevokedBy, &c.CreatedAt, &c.UpdatedAt,
	); err != nil {
		return domain.QRCode{}, err
	}
	c.Status = domain.QRCodeStatus(status)
	return c, nil
}
