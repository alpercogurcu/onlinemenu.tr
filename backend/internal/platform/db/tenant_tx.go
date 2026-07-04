package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrNilTenant is returned by WithTenantTx and WithTenantReadTx when called
// with uuid.Nil as the tenant ID.
//
// uuid.Nil must never be used as an ambient "no tenant filter" sentinel
// (docs/lessons-from-b2b.md item 6: b2b's `uuid.Nil` was ad-hoc god-mode that
// public/unauthenticated endpoints fell into by accident). Platform-level,
// cross-tenant access is a distinct, named, explicitly-authorized operation —
// see WithAllTenantsTx / WithAllTenantsReadTx.
var ErrNilTenant = errors.New("db: tenant id must not be uuid.Nil — use WithAllTenantsTx/WithAllTenantsReadTx for platform-level access")

// WithTenantTx begins a transaction, sets the RLS tenant context via SET LOCAL,
// then executes fn inside that transaction. It rolls back on any error.
//
// SET LOCAL is intentional: it scopes the GUC to the current transaction only,
// which is safe with pgBouncer transaction-mode pooling (ADR-SEC-001, ADR-SEC-002).
// Using bare SET (session-scoped) is forbidden because pgBouncer may reuse the
// connection for a different tenant.
//
// tenantID must not be uuid.Nil; see ErrNilTenant.
func (p *Pool) WithTenantTx(ctx context.Context, tenantID uuid.UUID, fn func(pgx.Tx) error) error {
	if tenantID == uuid.Nil {
		return ErrNilTenant
	}

	tx, err := p.inner.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.ReadCommitted,
		AccessMode: pgx.ReadWrite,
	})
	if err != nil {
		return fmt.Errorf("db: begin tenant tx: %w", err)
	}

	if _, err = tx.Exec(ctx, "SET LOCAL app.tenant_id = $1", tenantID.String()); err != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("db: set local tenant_id: %w", err)
	}

	if err = fn(tx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("db: commit tenant tx: %w", err)
	}

	return nil
}

// WithTenantReadTx is the read-only variant of WithTenantTx.
// Prefer this for all SELECT-only operations to allow routing to read replicas.
//
// tenantID must not be uuid.Nil; see ErrNilTenant.
func (p *Pool) WithTenantReadTx(ctx context.Context, tenantID uuid.UUID, fn func(pgx.Tx) error) error {
	if tenantID == uuid.Nil {
		return ErrNilTenant
	}

	tx, err := p.inner.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return fmt.Errorf("db: begin tenant read tx: %w", err)
	}

	if _, err = tx.Exec(ctx, "SET LOCAL app.tenant_id = $1", tenantID.String()); err != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("db: set local tenant_id (read): %w", err)
	}

	if err = fn(tx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("db: commit tenant read tx: %w", err)
	}

	return nil
}

// WithAllTenantsTx begins a transaction scoped to the platform-admin/system
// "all tenants" GUC (app.tenant_scope = 'all_tenants') instead of a specific
// tenant. It never sets app.tenant_id, so RLS policies that only check
// app.tenant_id continue to deny access by default; only policies that were
// explicitly written to check app.tenant_scope = 'all_tenants' grant
// cross-tenant visibility (see migrations/identity/000008 for the persons and
// memberships policies that do this).
//
// This is the single named, explicit replacement for the uuid.Nil "ambient
// bypass" sentinel that b2b regressed on repeatedly (docs/lessons-from-b2b.md
// item 6). Callers of this function are themselves responsible for verifying
// that the caller is actually a platform-admin/system actor — this function
// grants no authorization by itself, it only opens the RLS door that a
// correctly-written policy can then check against.
//
// Use WithAllTenantsReadTx for SELECT-only operations.
func (p *Pool) WithAllTenantsTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := p.inner.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.ReadCommitted,
		AccessMode: pgx.ReadWrite,
	})
	if err != nil {
		return fmt.Errorf("db: begin all-tenants tx: %w", err)
	}

	if _, err = tx.Exec(ctx, "SET LOCAL app.tenant_scope = 'all_tenants'"); err != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("db: set local tenant_scope: %w", err)
	}

	if err = fn(tx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("db: commit all-tenants tx: %w", err)
	}

	return nil
}

// WithAllTenantsReadTx is the read-only variant of WithAllTenantsTx.
// Prefer this for all SELECT-only platform-level/cross-tenant reads.
func (p *Pool) WithAllTenantsReadTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := p.inner.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return fmt.Errorf("db: begin all-tenants read tx: %w", err)
	}

	if _, err = tx.Exec(ctx, "SET LOCAL app.tenant_scope = 'all_tenants'"); err != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("db: set local tenant_scope (read): %w", err)
	}

	if err = fn(tx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("db: commit all-tenants read tx: %w", err)
	}

	return nil
}
