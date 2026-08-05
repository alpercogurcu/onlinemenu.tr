package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hashToken mirrors storefront/service.HashQRToken (lowercase hex SHA-256).
// Duplicated rather than imported: platform must not depend on a module.
func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func insertQRCode(t *testing.T, ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, tokenHash string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO storefront_qr_codes (tenant_id, branch_id, table_id, token_hash, created_by)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id`,
		tenantID, uuid.New(), uuid.New(), tokenHash, uuid.New(),
	).Scan(&id)
	require.NoError(t, err, "insertQRCode")
	return id
}

func countAll(t *testing.T, ctx context.Context, tx pgx.Tx, table string) int {
	t.Helper()
	var n int
	require.NoError(t, tx.QueryRow(ctx, `SELECT COUNT(*) FROM `+table).Scan(&n))
	return n
}

// TestRLSQRTokenLookupIsRowScoped is the acceptance criterion for the one RLS
// exception this codebase grants (ADR-ARCH-006 §4): resolving a QR token has
// to read storefront_qr_codes before any tenant is known.
//
// What must hold is that the exception is ROW-scoped, not table-scoped and not
// database-scoped: inside WithQRTokenLookupTx the caller sees exactly the one
// row whose token hash it already possessed, sees nothing on any other table,
// and cannot write anything at all. Every sub-test below pins one of those.
func TestRLSQRTokenLookupIsRowScoped(t *testing.T) {
	startContainer(t)

	ctx := context.Background()
	tenantA := uuid.New()
	tenantB := uuid.New()

	rawA := "raw-token-tenant-a-" + uuid.NewString()
	rawB := "raw-token-tenant-b-" + uuid.NewString()
	hashA := hashToken(rawA)
	hashB := hashToken(rawB)

	var qrA uuid.UUID
	require.NoError(t, runtimePool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		qrA = insertQRCode(t, ctx, tx, tenantA, hashA)
		// Seed one row on each stub table so a later "count == 0" cannot pass
		// merely because the table happens to be empty.
		_, err := tx.Exec(ctx, `INSERT INTO products (tenant_id) VALUES ($1)`, tenantA)
		require.NoError(t, err)
		_, err = tx.Exec(ctx, `INSERT INTO orders (tenant_id) VALUES ($1)`, tenantA)
		require.NoError(t, err)
		_, err = tx.Exec(ctx, `INSERT INTO checks (tenant_id) VALUES ($1)`, tenantA)
		require.NoError(t, err)
		return nil
	}))

	var qrB uuid.UUID
	require.NoError(t, runtimePool.WithTenantTx(ctx, tenantB, func(tx pgx.Tx) error {
		qrB = insertQRCode(t, ctx, tx, tenantB, hashB)
		return nil
	}))

	t.Run("sees exactly one row across all tenants", func(t *testing.T) {
		err := runtimePool.WithQRTokenLookupTx(ctx, hashA, func(tx pgx.Tx) error {
			// Deliberately WHERE-less: the policy, not the query, is what
			// must narrow this to a single row.
			assert.Equal(t, 1, countAll(t, ctx, tx, "storefront_qr_codes"),
				"the token-hash policy branch must unlock exactly one row")

			var gotID, gotTenant uuid.UUID
			require.NoError(t, tx.QueryRow(ctx,
				`SELECT id, tenant_id FROM storefront_qr_codes`,
			).Scan(&gotID, &gotTenant))
			assert.Equal(t, qrA, gotID)
			assert.Equal(t, tenantA, gotTenant)
			return nil
		})
		require.NoError(t, err)
	})

	t.Run("other tenants qr row stays invisible", func(t *testing.T) {
		err := runtimePool.WithQRTokenLookupTx(ctx, hashA, func(tx pgx.Tx) error {
			var n int
			require.NoError(t, tx.QueryRow(ctx,
				`SELECT COUNT(*) FROM storefront_qr_codes WHERE id = $1`, qrB,
			).Scan(&n))
			assert.Equal(t, 0, n, "holding tenant A's token must not reveal tenant B's code")
			return nil
		})
		require.NoError(t, err)
	})

	t.Run("no other table becomes readable", func(t *testing.T) {
		err := runtimePool.WithQRTokenLookupTx(ctx, hashA, func(tx pgx.Tx) error {
			// app.tenant_id is never set by this path, so tenant_isolation
			// evaluates to NULL and denies — the exception is scoped to the
			// one table with the token-hash policy branch, nothing else.
			assert.Equal(t, 0, countAll(t, ctx, tx, "products"))
			assert.Equal(t, 0, countAll(t, ctx, tx, "orders"))
			assert.Equal(t, 0, countAll(t, ctx, tx, "checks"))
			assert.Equal(t, 0, countAll(t, ctx, tx, "test_items"))
			assert.Equal(t, 0, countAll(t, ctx, tx, "platform_items"),
				"the all_tenants door must stay shut on this path")
			return nil
		})
		require.NoError(t, err)
	})

	t.Run("transaction is read only", func(t *testing.T) {
		var writeErr error
		// The write error is returned from fn rather than swallowed: a failed
		// statement already aborts the Postgres transaction, so returning nil
		// here would only turn this into a confusing commit failure.
		err := runtimePool.WithQRTokenLookupTx(ctx, hashA, func(tx pgx.Tx) error {
			_, writeErr = tx.Exec(ctx,
				`INSERT INTO storefront_qr_codes (tenant_id, branch_id, table_id, token_hash, created_by)
				 VALUES ($1, $2, $3, $4, $5)`,
				tenantA, uuid.New(), uuid.New(), hashToken("smuggled"), uuid.New())
			return writeErr
		})
		require.Error(t, writeErr, "the bootstrap transaction must not be able to write")
		require.ErrorIs(t, err, writeErr)

		// 25006 = read_only_sql_transaction. Asserting the exact SQLSTATE
		// pins WHY the write failed: an RLS WITH CHECK denial would also
		// error, but would mean the read-only access mode had been dropped.
		var pgErr interface{ SQLState() string }
		require.ErrorAs(t, writeErr, &pgErr)
		assert.Equal(t, "25006", pgErr.SQLState())

		// The failed write must leave nothing behind.
		require.NoError(t, runtimePool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
			assert.Equal(t, 1, countAll(t, ctx, tx, "storefront_qr_codes"))
			return nil
		}))
	})

	t.Run("unknown hash yields no rows rather than an error", func(t *testing.T) {
		err := runtimePool.WithQRTokenLookupTx(ctx, hashToken("never-issued"), func(tx pgx.Tx) error {
			assert.Equal(t, 0, countAll(t, ctx, tx, "storefront_qr_codes"))
			return nil
		})
		require.NoError(t, err)
	})

	t.Run("malformed hash is rejected before a transaction is opened", func(t *testing.T) {
		tests := []struct {
			name string
			hash string
		}{
			{name: "empty", hash: ""},
			{name: "too short", hash: "abc123"},
			{name: "uppercase hex", hash: strings.ToUpper(hashA)},
			{name: "right length wrong alphabet", hash: strings.Repeat("z", 64)},
			{name: "raw token instead of hash", hash: rawA},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				called := false
				err := runtimePool.WithQRTokenLookupTx(ctx, tt.hash, func(pgx.Tx) error {
					called = true
					return nil
				})
				require.ErrorIs(t, err, ErrInvalidTokenHash)
				assert.False(t, called, "fn must not run when the hash is rejected")
			})
		}
	})

	t.Run("ordinary tenant read is unaffected", func(t *testing.T) {
		err := runtimePool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
			assert.Equal(t, 1, countAll(t, ctx, tx, "storefront_qr_codes"),
				"a normal tenant read must still see only its own codes")

			var n int
			require.NoError(t, tx.QueryRow(ctx,
				`SELECT COUNT(*) FROM storefront_qr_codes WHERE id = $1`, qrB,
			).Scan(&n))
			assert.Equal(t, 0, n, "tenant A must never see tenant B's qr code")
			return nil
		})
		require.NoError(t, err)
	})

	t.Run("token hash guc does not leak past the transaction", func(t *testing.T) {
		// set_config(..., true) is transaction scoped. If it ever regressed to
		// session scope, a pgBouncer-pooled connection could carry tenant A's
		// hash into an unrelated later request (ADR-SEC-001).
		require.NoError(t, runtimePool.WithQRTokenLookupTx(ctx, hashA, func(pgx.Tx) error { return nil }))

		conn, err := runtimePool.inner.Acquire(ctx)
		require.NoError(t, err)
		defer conn.Release()

		var n int
		require.NoError(t, conn.QueryRow(ctx, `SELECT COUNT(*) FROM storefront_qr_codes`).Scan(&n))
		assert.Equal(t, 0, n, "no GUC must survive the lookup transaction")
	})
}
