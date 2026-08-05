package repo

import "errors"

// ErrNotFound is returned when a requested row is not visible to the current
// RLS context — which, for this module, covers both "does not exist" and
// "belongs to another tenant". Callers must not distinguish the two.
var ErrNotFound = errors.New("storefront: not found")

// ErrInvalidTransition is returned by guarded status UPDATEs when the row's
// status no longer matches the expected value (0 rows affected).
var ErrInvalidTransition = errors.New("storefront: invalid status transition")

// ErrTableAlreadyHasCode is returned when an INSERT violates
// storefront_qr_codes_active_table_uidx: a table may have at most one active
// QR code. Rotating means revoking the old row first, in the same transaction.
var ErrTableAlreadyHasCode = errors.New("storefront: table already has an active qr code")

// isUniqueViolation reports whether err is a Postgres unique_violation
// (SQLSTATE 23505), mirroring pos/repo's helper.
func isUniqueViolation(err error) bool {
	var pgErr interface{ SQLState() string }
	if errors.As(err, &pgErr) {
		return pgErr.SQLState() == "23505"
	}
	return false
}
