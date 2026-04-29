package db

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// WithTenantTx begins a transaction, sets the RLS tenant context via SET LOCAL,
// then executes fn inside that transaction. It rolls back on any error.
//
// SET LOCAL is intentional: it scopes the GUC to the current transaction only,
// which is safe with pgBouncer transaction-mode pooling (ADR-SEC-001, ADR-SEC-002).
// Using bare SET (session-scoped) is forbidden because pgBouncer may reuse the
// connection for a different tenant.
func (p *Pool) WithTenantTx(ctx context.Context, tenantID uuid.UUID, fn func(pgx.Tx) error) error {
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
func (p *Pool) WithTenantReadTx(ctx context.Context, tenantID uuid.UUID, fn func(pgx.Tx) error) error {
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
