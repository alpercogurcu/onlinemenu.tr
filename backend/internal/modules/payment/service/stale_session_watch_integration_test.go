package service_test

// Integration test for StaleSessionWatch's production wiring
// (dbStaleSessionStore): unlike stale_session_watch_test.go's fake-backed unit
// tests, this exercises the real cross-tenant read through
// db.WithAllTenantsReadTx against sharedPool, which is exactly the path that
// needs migration/000009's cash_sessions_all_tenants_select RLS policy. A
// missing or wrong policy would make ListOpenOlderThan silently return zero
// rows (RLS denies, not errors), which the unit tests above cannot catch.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"onlinemenu.tr/internal/modules/payment/repo"
	"onlinemenu.tr/internal/modules/payment/service"
	"onlinemenu.tr/internal/platform/auth"
)

// TestStaleSessionWatch_Integration_WarnsAboutRealOldSession opens a real
// session, backdates it past MaxAge, and drives one sweep of the production
// watch (fake clock, real Postgres) — it must warn exactly once, and never
// touch the session's status (no automatic close).
func TestStaleSessionWatch_Integration_WarnsAboutRealOldSession(t *testing.T) {
	requireDB(t)
	ctx := context.Background()

	branch := uuid.New()
	sessions := repo.NewCashSessionRepo()
	// Deliberately tenantA (this package's shared fixture tenant, not a
	// fresh random one): the later raw SQL backdate below runs under
	// WithTenantTx(tenantA, ...), and FORCE RLS means an UPDATE against a
	// row owned by a different tenant silently matches zero rows.
	manager := auth.Principal{
		PersonID: uuid.New(),
		Ctx:      auth.ContextStaff,
		TenantID: tenantA,
		BranchID: branch,
	}
	opened, err := newCashSessionService().Open(ctx, manager,
		service.OpenCashSessionRequest{BranchID: branch, OpeningCountedAmount: 0})
	require.NoError(t, err)

	backdated := time.Now().UTC().Add(-25 * time.Hour)
	err = sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE cash_sessions SET opened_at = $1 WHERE id = $2`, backdated, opened.Session.ID)
		return err
	})
	require.NoError(t, err)

	core, observed := observer.New(zapcore.DebugLevel)
	w := service.NewStaleSessionWatch(service.StaleSessionWatchParams{
		DB:       sharedPool,
		Sessions: sessions,
		Logger:   zap.New(core),
		Config:   service.StaleSessionWatchConfig{MaxAge: 20 * time.Hour},
	})

	stats, err := w.RunOnce(ctx)
	require.NoError(t, err)
	require.GreaterOrEqual(t, stats.Scanned, 1,
		"the sweep scanned nothing; ListOpenOlderThan runs under WithAllTenantsReadTx and needs "+
			"the cash_sessions_all_tenants_select RLS policy (migration/000009)")
	assert.GreaterOrEqual(t, stats.Warned, 1)

	logs := observed.FilterMessage("payment: cash session open past max age").All()
	assert.NotEmpty(t, logs, "the stale session must produce a Warn log")

	// No automatic close, ever: the session's status must be untouched.
	var status string
	err = sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT status FROM cash_sessions WHERE id = $1`, opened.Session.ID).Scan(&status)
	})
	require.NoError(t, err)
	assert.Equal(t, "opened", status, "StaleSessionWatch must never close a session on its own")
}
