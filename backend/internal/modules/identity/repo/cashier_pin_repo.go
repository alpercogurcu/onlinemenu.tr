package repo

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	pub "onlinemenu.tr/internal/modules/identity/public"
)

// CashierPinRepo handles persistence for cashier_pins (ADR-DATA-008 PIN
// akışı). It is the ONLY code in this repository that ever selects
// pin_salt/pin_hash — no other repo, no HTTP handler, joins against this
// table's hash columns.
type CashierPinRepo struct{}

// NewCashierPinRepo constructs a CashierPinRepo for fx injection.
func NewCashierPinRepo() *CashierPinRepo { return &CashierPinRepo{} }

// Upsert sets (or replaces) personID's PIN for tenantID. Called only from
// the trusted "join" moment (see service/pin.go SetOwnPin) — the caller is
// responsible for ensuring personID is the acting principal's own ID.
func (r *CashierPinRepo) Upsert(ctx context.Context, tx pgx.Tx, tenantID, personID uuid.UUID, salt, hash []byte) error {
	const q = `
		INSERT INTO cashier_pins (tenant_id, person_id, pin_salt, pin_hash, updated_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (tenant_id, person_id)
		DO UPDATE SET pin_salt = EXCLUDED.pin_salt, pin_hash = EXCLUDED.pin_hash, updated_at = now()`
	if _, err := tx.Exec(ctx, q, tenantID, personID, salt, hash); err != nil {
		return fmt.Errorf("identity/repo/cashier_pin: upsert: %w", err)
	}
	return nil
}

// GetHash returns the stored salt/hash for (tenantID, personID). Returns
// pub.ErrNotFound if the person has never set a PIN (or it was reset) — the
// caller (service/pin.go VerifyPin) treats this identically to a wrong PIN,
// this method itself makes no such judgement.
func (r *CashierPinRepo) GetHash(ctx context.Context, tx pgx.Tx, tenantID, personID uuid.UUID) (salt, hash []byte, err error) {
	const q = `SELECT pin_salt, pin_hash FROM cashier_pins WHERE tenant_id = $1 AND person_id = $2`
	row := tx.QueryRow(ctx, q, tenantID, personID)
	if err := row.Scan(&salt, &hash); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, pub.ErrNotFound
		}
		return nil, nil, fmt.Errorf("identity/repo/cashier_pin: get hash: %w", err)
	}
	return salt, hash, nil
}

// Delete removes personID's PIN row for tenantID (manager reset). It is not
// an error for the row to already be absent — resetting an unset PIN is a
// no-op, not a conflict; the caller must never be able to tell from this
// method's result whether a PIN existed.
func (r *CashierPinRepo) Delete(ctx context.Context, tx pgx.Tx, tenantID, personID uuid.UUID) error {
	const q = `DELETE FROM cashier_pins WHERE tenant_id = $1 AND person_id = $2`
	if _, err := tx.Exec(ctx, q, tenantID, personID); err != nil {
		return fmt.Errorf("identity/repo/cashier_pin: delete: %w", err)
	}
	return nil
}
