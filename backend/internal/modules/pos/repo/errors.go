package repo

import "errors"

var ErrNotFound = errors.New("pos: not found")

// ErrInvalidTransition is returned by guarded status UPDATEs when the row's
// status no longer matches the expected value (0 rows affected). Combined
// with a preceding SELECT ... FOR UPDATE it is a defense-in-depth check —
// the row lock already prevents concurrent status changes within the same
// transaction — rather than the primary concurrency mechanism.
var ErrInvalidTransition = errors.New("pos: invalid status transition")

// ErrTableOccupied is returned by CheckRepo.Create when the insert violates
// checks_open_table_id_uidx (at most one OPEN check per table_id). This is
// the defense-in-depth backstop for CheckService.Open's row lock
// (TableRepo.GetTableForUpdate): it only fires when a table's status was
// manually reset to "empty"/"reserved" (TableService.SetStatus) while an
// open check still referenced it — a data state the lock's own
// empty/reserved precondition cannot see, since it only checks the table
// row, not whether some other still-open check already claims it. Without
// this mapping the unique violation would surface as an unmapped 500.
var ErrTableOccupied = errors.New("pos: table already has an open check")

// ErrOpenedByKindMismatch is returned by CheckRepo.Create when opened_by and
// opened_by_kind disagree: 'staff' requires a person, 'guest_qr' forbids one.
// The DB already enforces this (checks_opened_by_kind_chk), but a raw SQLSTATE
// 23514 surfaces as an unmapped 500 with no hint about which of the two fields
// the caller got wrong — this turns a wrong call into a readable error.
var ErrOpenedByKindMismatch = errors.New("pos: opened_by must be set for staff checks and nil for guest checks")

// isUniqueViolation reports whether err is a Postgres unique_violation
// (SQLSTATE 23505), mirroring payment/repo's isUniqueViolation helper.
func isUniqueViolation(err error) bool {
	var pgErr interface{ SQLState() string }
	if errors.As(err, &pgErr) {
		return pgErr.SQLState() == "23505"
	}
	return false
}
