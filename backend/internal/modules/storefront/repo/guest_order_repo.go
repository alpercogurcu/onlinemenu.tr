package repo

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"onlinemenu.tr/internal/modules/storefront/domain"
)

// GuestOrderRepo manages storefront_guest_orders persistence — the binding
// between a placed pos order and the guest session that placed it.
type GuestOrderRepo struct{}

func NewGuestOrderRepo() *GuestOrderRepo { return &GuestOrderRepo{} }

const guestOrderColumns = `order_id, tenant_id, qr_code_id, guest_session_id, check_id`

// Link records that orderID was placed by guestSessionID. It must run in the
// same transaction as the order placement, so a successful order can never
// end up unreachable by the guest that placed it.
func (r *GuestOrderRepo) Link(ctx context.Context, tx pgx.Tx, l domain.GuestOrderLink) error {
	const q = `
		INSERT INTO storefront_guest_orders
		    (order_id, tenant_id, qr_code_id, guest_session_id, check_id)
		VALUES ($1, $2, $3, $4, $5)`

	if _, err := tx.Exec(ctx, q,
		l.OrderID, l.TenantID, l.QRCodeID, l.GuestSessionID, l.CheckID,
	); err != nil {
		return fmt.Errorf("storefront/repo/guest_order: link: %w", err)
	}
	return nil
}

// ListBySession returns the session's order links, newest first.
func (r *GuestOrderRepo) ListBySession(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID) ([]domain.GuestOrderLink, error) {
	const q = `
		SELECT ` + guestOrderColumns + `
		FROM storefront_guest_orders
		WHERE guest_session_id = $1
		ORDER BY created_at DESC`

	rows, err := tx.Query(ctx, q, sessionID)
	if err != nil {
		return nil, fmt.Errorf("storefront/repo/guest_order: list by session: %w", err)
	}
	defer rows.Close()

	var out []domain.GuestOrderLink
	for rows.Next() {
		l, err := scanGuestOrderLink(rows)
		if err != nil {
			return nil, fmt.Errorf("storefront/repo/guest_order: list by session scan: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// GetForSession returns the link for orderID only if it belongs to
// sessionID. The session id is part of the WHERE clause rather than something
// the caller compares afterwards: a guest must not be able to confirm another
// diner's order id even exists, so a mismatch is indistinguishable from a
// missing row (ErrNotFound → 404).
func (r *GuestOrderRepo) GetForSession(ctx context.Context, tx pgx.Tx, sessionID, orderID uuid.UUID) (domain.GuestOrderLink, error) {
	const q = `
		SELECT ` + guestOrderColumns + `
		FROM storefront_guest_orders
		WHERE order_id = $1 AND guest_session_id = $2`

	l, err := scanGuestOrderLink(tx.QueryRow(ctx, q, orderID, sessionID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.GuestOrderLink{}, ErrNotFound
		}
		return domain.GuestOrderLink{}, fmt.Errorf("storefront/repo/guest_order: get for session: %w", err)
	}
	return l, nil
}

func scanGuestOrderLink(s interface {
	Scan(...any) error
}) (domain.GuestOrderLink, error) {
	var l domain.GuestOrderLink
	if err := s.Scan(
		&l.OrderID, &l.TenantID, &l.QRCodeID, &l.GuestSessionID, &l.CheckID,
	); err != nil {
		return domain.GuestOrderLink{}, err
	}
	return l, nil
}
