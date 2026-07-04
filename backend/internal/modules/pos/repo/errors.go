package repo

import "errors"

var ErrNotFound = errors.New("pos: not found")

// ErrInvalidTransition is returned by guarded status UPDATEs when the row's
// status no longer matches the expected value (0 rows affected). Combined
// with a preceding SELECT ... FOR UPDATE it is a defense-in-depth check —
// the row lock already prevents concurrent status changes within the same
// transaction — rather than the primary concurrency mechanism.
var ErrInvalidTransition = errors.New("pos: invalid status transition")
