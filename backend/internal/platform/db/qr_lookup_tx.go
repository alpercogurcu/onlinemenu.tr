package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ErrInvalidTokenHash is returned by WithQRTokenLookupTx when the supplied
// hash is not a lowercase hex SHA-256 digest.
//
// The check is not cosmetic. The hash is fed to a GUC that an RLS policy
// compares against storefront_qr_codes.token_hash; an empty string would make
// that comparison collapse to `token_hash = NULL` (NULLIF maps the empty string to NULL,
// so it stays falsy rather than matching), and anything else would be a caller
// that skipped hashing and is about to put a raw token into a database GUC.
// Rejecting before BeginTx means such a call never even opens a transaction.
var ErrInvalidTokenHash = errors.New("db: qr token hash must be a lowercase hex sha-256 digest")

// sha256HexLen is the length of a SHA-256 digest in lowercase hex.
const sha256HexLen = 64

// WithQRTokenLookupTx is the ONLY sanctioned way to resolve a storefront QR
// token to its tenant, and the only place app.storefront_qr_token_hash is ever
// set (ADR-ARCH-006 §4).
//
// Resolving a token is a bootstrap problem: the tenant is not known until the
// row is read, so no app.tenant_id can be set — and this function deliberately
// never sets one. Visibility instead comes from the qr_codes_read policy
// branch (migrations/storefront/000001), which unlocks exactly the single row
// whose token_hash equals the GUC. That is strictly narrower than the
// app.tenant_scope = 'all_tenants' door used by WithAllTenantsReadTx, which
// would expose every row of the table: a caller here can only see what it
// already had the token for.
//
// The transaction is read-only on purpose. Nothing about token resolution
// needs to write, and read-only makes "this bootstrap path cannot mutate
// anything" a database-enforced property rather than a code review promise.
// Every subsequent query must run in a normal WithTenantTx/WithTenantReadTx
// using the tenant_id this lookup returned.
func (p *Pool) WithQRTokenLookupTx(ctx context.Context, tokenHash string, fn func(pgx.Tx) error) error {
	if !isLowerHexSHA256(tokenHash) {
		return ErrInvalidTokenHash
	}

	tx, err := p.inner.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return fmt.Errorf("db: begin qr token lookup tx: %w", err)
	}
	// See WithTenantTx for why Rollback is deferred right after BeginTx.
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	// set_config(..., true) is the function form of SET LOCAL: transaction
	// scoped, which is what makes this safe under pgBouncer transaction-mode
	// pooling (ADR-SEC-001). The parameterized form also keeps the hash out of
	// the statement text, which SET LOCAL cannot do.
	if _, err = tx.Exec(ctx, "SELECT set_config('app.storefront_qr_token_hash', $1, true)", tokenHash); err != nil {
		return fmt.Errorf("db: set local storefront_qr_token_hash: %w", err)
	}

	if err = fn(tx); err != nil {
		return err
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("db: commit qr token lookup tx: %w", err)
	}

	return nil
}

func isLowerHexSHA256(s string) bool {
	if len(s) != sha256HexLen {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
