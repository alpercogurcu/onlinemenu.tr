// Package repo contains the database access layer for the catalog module.
// All functions accept a pgx.Tx — direct pool access is forbidden (ADR-SEC-001).
package repo

import "errors"

// ErrNotFound is returned when a requested catalog resource does not exist or
// is not visible to the current tenant (RLS hidden rows appear as not found).
var ErrNotFound = errors.New("catalog: not found")
