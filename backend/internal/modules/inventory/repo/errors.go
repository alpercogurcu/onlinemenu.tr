// Package repo contains the database access layer for the inventory module.
// All functions accept a pgx.Tx — direct pool access is forbidden (ADR-SEC-001).
package repo

import "errors"

// ErrNotFound is returned when a requested inventory record does not exist or
// is not visible to the current tenant (RLS hidden rows appear as not found).
var ErrNotFound = errors.New("inventory: not found")
