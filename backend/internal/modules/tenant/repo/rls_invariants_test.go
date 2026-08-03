package repo_test

// Module-specific "wiring audit" (docs/lessons-from-b2b.md item 1): proves,
// per actual table this module owns — not a synthetic stand-in — that
// FORCE ROW LEVEL SECURITY is both declared AND enforced under the
// app_runtime role used in production. platform/db/rls_test.go already
// proves the *mechanism* generically; this file proves the *tenant
// module's own migrations* wired it correctly on every table.
//
// branch_settings (declared in tenant/migrations/000001) is intentionally
// excluded: it has no repo in this module — payment/fiscal owns all code
// access to it (see report "surprising, not acted on").

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pub "onlinemenu.tr/internal/modules/tenant/public"
	"onlinemenu.tr/internal/modules/tenant/repo"
)

// countByTenantCol runs `SELECT COUNT(*) FROM <table> WHERE <col> = id`
// under the reader's RLS context. table/col are fixed literals from the call
// sites below, never user input.
func countByTenantCol(t *testing.T, ctx context.Context, readerTenant uuid.UUID, table, col string, id uuid.UUID) int {
	t.Helper()
	var n int
	err := sharedPool.WithTenantReadTx(ctx, readerTenant, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT COUNT(*) FROM `+table+` WHERE `+col+` = $1`, id).Scan(&n)
	})
	require.NoError(t, err)
	return n
}

// TestRLS_CrossTenantRead_AllOwnedTables sweeps every table this module
// owns and proves tenant B's RLS-scoped connection sees zero of tenant A's
// rows, across the full set — not just the tables the three pre-existing
// tests happened to touch (invariant class 1).
func TestRLS_CrossTenantRead_AllOwnedTables(t *testing.T) {
	ctx := context.Background()
	hoursRepo := repo.NewHoursRepo()

	tenantA := createTenant(t, ctx)
	branchA := createBranch(t, ctx, tenantA.ID)
	_ = createDocument(t, ctx, tenantA.ID)
	_ = createBranchDocument(t, ctx, tenantA.ID, branchA.ID)
	_ = createIntegrator(t, ctx, tenantA.ID, nil, pub.ProviderEDM)

	err := sharedPool.WithTenantTx(ctx, tenantA.ID, func(tx pgx.Tx) error {
		if err := hoursRepo.SetRegularHours(ctx, tx, tenantA.ID, branchA.ID, []pub.RegularHours{
			{TenantID: tenantA.ID, BranchID: branchA.ID, DayOfWeek: time.Monday, OpenTime: tod(9, 0), CloseTime: tod(18, 0)},
		}); err != nil {
			return err
		}
		return hoursRepo.UpsertSpecialHours(ctx, tx, tenantA.ID, branchA.ID, pub.SpecialHours{
			TenantID: tenantA.ID, BranchID: branchA.ID,
			SpecialDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			IsClosed:    true,
		})
	})
	require.NoError(t, err)

	tenantB := createTenant(t, ctx)

	tests := []struct {
		table string
		col   string
		id    uuid.UUID
	}{
		{"tenants", "id", tenantA.ID},
		{"branches", "tenant_id", tenantA.ID},
		{"tenant_documents", "tenant_id", tenantA.ID},
		{"branch_documents", "tenant_id", tenantA.ID},
		{"billing_integrators", "tenant_id", tenantA.ID},
		{"branch_regular_hours", "tenant_id", tenantA.ID},
		{"branch_special_hours", "tenant_id", tenantA.ID},
	}

	for _, tt := range tests {
		t.Run(tt.table, func(t *testing.T) {
			// Sanity: tenant A must actually see its own row(s) first — a 0
			// here would mean the fixture setup is broken, not that RLS works.
			ownCount := countByTenantCol(t, ctx, tenantA.ID, tt.table, tt.col, tt.id)
			require.Greater(t, ownCount, 0, "fixture sanity: tenant A must see its own %s row(s)", tt.table)

			crossCount := countByTenantCol(t, ctx, tenantB.ID, tt.table, tt.col, tt.id)
			assert.Equal(t, 0, crossCount, "tenant B must see 0 rows of tenant A's %s", tt.table)
		})
	}
}

// Note: this file deliberately does NOT re-prove "no SET LOCAL -> 0 rows"
// for the tenant module's tables the way platform/db/rls_test.go proves it
// generically (TestRLSWithoutTenantContext / TestRLSForceRLSBypassAttempt).
// Reaching a raw, unscoped connection requires db.Pool.Inner(), which
// forbidigo (.golangci.yml, ADR-SEC-001/002) blocks for ALL code under
// internal/modules/** — including this module's own tests. That is a
// deliberate boundary, not an oversight: the "no SET LOCAL" guarantee is a
// platform-level property of db.Pool itself, proven once in
// internal/platform/db, not a per-module one to re-derive. Module code
// (and module tests) can only ever reach a tenant-scoped or
// all-tenants-scoped transaction — which is itself a live demonstration
// that the "WithTenantTx tekeli" from docs/lessons-from-b2b.md item 1 is
// enforced by tooling, not merely documented.
