package repo

import "errors"

// isUniqueViolation reports whether err is a Postgres unique_violation
// (SQLSTATE 23505), mirroring pos/repo's and storefront/repo's helper.
func isUniqueViolation(err error) bool {
	var pgErr interface{ SQLState() string }
	if errors.As(err, &pgErr) {
		return pgErr.SQLState() == "23505"
	}
	return false
}
