package repo_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pub "onlinemenu.tr/internal/modules/tenant/public"
	"onlinemenu.tr/internal/modules/tenant/repo"
)

// TestHoursRepo_RegularHours_ReplaceSemantics is ordinary correctness
// coverage for the previously-untested HoursRepo: SetRegularHours is a full
// weekly replace, not a merge/upsert.
func TestHoursRepo_RegularHours_ReplaceSemantics(t *testing.T) {
	ctx := context.Background()
	r := repo.NewHoursRepo()

	tenant := createTenant(t, ctx)
	branch := createBranch(t, ctx, tenant.ID)

	// Split lunch/dinner service on Monday.
	first := []pub.RegularHours{
		{TenantID: tenant.ID, BranchID: branch.ID, DayOfWeek: time.Monday, OpenTime: tod(12, 0), CloseTime: tod(15, 0), SortOrder: 0},
		{TenantID: tenant.ID, BranchID: branch.ID, DayOfWeek: time.Monday, OpenTime: tod(18, 0), CloseTime: tod(23, 0), SortOrder: 1},
	}
	err := sharedPool.WithTenantTx(ctx, tenant.ID, func(tx pgx.Tx) error {
		return r.SetRegularHours(ctx, tx, tenant.ID, branch.ID, first)
	})
	require.NoError(t, err)

	err = sharedPool.WithTenantReadTx(ctx, tenant.ID, func(tx pgx.Tx) error {
		got, err := r.GetRegularHours(ctx, tx, tenant.ID, branch.ID)
		if err != nil {
			return err
		}
		require.Len(t, got, 2)
		assert.Equal(t, time.Monday, got[0].DayOfWeek)
		assert.Equal(t, 12, got[0].OpenTime.Hour)
		assert.Equal(t, 18, got[1].OpenTime.Hour)
		return nil
	})
	require.NoError(t, err)

	// Replace with a single Tuesday closed-all-day entry — Monday's slots
	// must be gone, not merged.
	second := []pub.RegularHours{
		{TenantID: tenant.ID, BranchID: branch.ID, DayOfWeek: time.Tuesday, IsClosed: true, SortOrder: 0},
	}
	err = sharedPool.WithTenantTx(ctx, tenant.ID, func(tx pgx.Tx) error {
		return r.SetRegularHours(ctx, tx, tenant.ID, branch.ID, second)
	})
	require.NoError(t, err)

	err = sharedPool.WithTenantReadTx(ctx, tenant.ID, func(tx pgx.Tx) error {
		got, err := r.GetRegularHours(ctx, tx, tenant.ID, branch.ID)
		if err != nil {
			return err
		}
		require.Len(t, got, 1, "SetRegularHours must fully replace the previous week, not merge")
		assert.Equal(t, time.Tuesday, got[0].DayOfWeek)
		assert.True(t, got[0].IsClosed)
		return nil
	})
	require.NoError(t, err)

	// Replacing with an empty slice clears the schedule entirely.
	err = sharedPool.WithTenantTx(ctx, tenant.ID, func(tx pgx.Tx) error {
		return r.SetRegularHours(ctx, tx, tenant.ID, branch.ID, nil)
	})
	require.NoError(t, err)

	err = sharedPool.WithTenantReadTx(ctx, tenant.ID, func(tx pgx.Tx) error {
		got, err := r.GetRegularHours(ctx, tx, tenant.ID, branch.ID)
		if err != nil {
			return err
		}
		assert.Empty(t, got)
		return nil
	})
	require.NoError(t, err)
}

// TestHoursRepo_SpecialHours_UpsertAndDelete covers the ON CONFLICT DO UPDATE
// upsert path and soft-delete-free hard delete for special hours.
func TestHoursRepo_SpecialHours_UpsertAndDelete(t *testing.T) {
	ctx := context.Background()
	r := repo.NewHoursRepo()

	tenant := createTenant(t, ctx)
	branch := createBranch(t, ctx, tenant.ID)
	date := time.Date(2026, 4, 23, 0, 0, 0, 0, time.UTC) // Ulusal Egemenlik Bayramı

	sh := pub.SpecialHours{
		TenantID: tenant.ID, BranchID: branch.ID, SpecialDate: date,
		Name: "Resmi Tatil", IsClosed: true,
	}
	err := sharedPool.WithTenantTx(ctx, tenant.ID, func(tx pgx.Tx) error {
		return r.UpsertSpecialHours(ctx, tx, tenant.ID, branch.ID, sh)
	})
	require.NoError(t, err)

	err = sharedPool.WithTenantReadTx(ctx, tenant.ID, func(tx pgx.Tx) error {
		got, err := r.GetSpecialHours(ctx, tx, tenant.ID, branch.ID)
		if err != nil {
			return err
		}
		require.Len(t, got, 1)
		assert.Equal(t, "Resmi Tatil", got[0].Name)
		assert.True(t, got[0].IsClosed)
		return nil
	})
	require.NoError(t, err)

	// Upsert again on the SAME date with different content — must update in
	// place (branch_special_hours_unique on (branch_id, special_date)), not
	// duplicate.
	sh.Name = "Kısaltılmış Mesai"
	sh.IsClosed = false
	sh.OpenTime = tod(10, 0)
	sh.CloseTime = tod(16, 0)
	err = sharedPool.WithTenantTx(ctx, tenant.ID, func(tx pgx.Tx) error {
		return r.UpsertSpecialHours(ctx, tx, tenant.ID, branch.ID, sh)
	})
	require.NoError(t, err)

	err = sharedPool.WithTenantReadTx(ctx, tenant.ID, func(tx pgx.Tx) error {
		got, err := r.GetSpecialHours(ctx, tx, tenant.ID, branch.ID)
		if err != nil {
			return err
		}
		require.Len(t, got, 1, "upsert on the same date must update, not duplicate")
		assert.Equal(t, "Kısaltılmış Mesai", got[0].Name)
		assert.False(t, got[0].IsClosed)
		return nil
	})
	require.NoError(t, err)

	err = sharedPool.WithTenantTx(ctx, tenant.ID, func(tx pgx.Tx) error {
		return r.DeleteSpecialHours(ctx, tx, tenant.ID, branch.ID, date)
	})
	require.NoError(t, err)

	err = sharedPool.WithTenantTx(ctx, tenant.ID, func(tx pgx.Tx) error {
		return r.DeleteSpecialHours(ctx, tx, tenant.ID, branch.ID, date)
	})
	require.ErrorIs(t, err, pub.ErrNotFound, "deleting an already-deleted special-hours row must be ErrNotFound")
}

// TestHoursRepo_IsOpenAt_SpecialOverridesRegular proves the precedence order
// documented on IsOpenAt: an exact-date special_hours record wins over the
// weekday's regular_hours, even when they disagree.
func TestHoursRepo_IsOpenAt_SpecialOverridesRegular(t *testing.T) {
	ctx := context.Background()
	r := repo.NewHoursRepo()

	tenant := createTenant(t, ctx)
	branch := createBranch(t, ctx, tenant.ID)

	// Regular Monday hours: open 09:00-22:00.
	monday := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC) // a Monday
	require.Equal(t, time.Monday, monday.Weekday())

	err := sharedPool.WithTenantTx(ctx, tenant.ID, func(tx pgx.Tx) error {
		return r.SetRegularHours(ctx, tx, tenant.ID, branch.ID, []pub.RegularHours{
			{TenantID: tenant.ID, BranchID: branch.ID, DayOfWeek: time.Monday, OpenTime: tod(9, 0), CloseTime: tod(22, 0)},
		})
	})
	require.NoError(t, err)

	err = sharedPool.WithTenantReadTx(ctx, tenant.ID, func(tx pgx.Tx) error {
		open, err := r.IsOpenAt(ctx, tx, tenant.ID, branch.ID, monday)
		if err != nil {
			return err
		}
		assert.True(t, open, "12:00 on a regular Monday must be open per regular_hours")
		return nil
	})
	require.NoError(t, err)

	// Special-hours override: this specific Monday is a holiday, closed all day.
	err = sharedPool.WithTenantTx(ctx, tenant.ID, func(tx pgx.Tx) error {
		return r.UpsertSpecialHours(ctx, tx, tenant.ID, branch.ID, pub.SpecialHours{
			TenantID: tenant.ID, BranchID: branch.ID,
			SpecialDate: time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC),
			Name:        "Özel Kapalı Gün", IsClosed: true,
		})
	})
	require.NoError(t, err)

	err = sharedPool.WithTenantReadTx(ctx, tenant.ID, func(tx pgx.Tx) error {
		open, err := r.IsOpenAt(ctx, tx, tenant.ID, branch.ID, monday)
		if err != nil {
			return err
		}
		assert.False(t, open, "special_hours override must take precedence over regular_hours")
		return nil
	})
	require.NoError(t, err)
}

// TestHoursRepo_CrossTenantRead proves tenant B's reads for tenant A's branch
// hours come back empty, not erroring and not leaking rows (invariant class 1).
func TestHoursRepo_CrossTenantRead(t *testing.T) {
	ctx := context.Background()
	r := repo.NewHoursRepo()

	tenantA := createTenant(t, ctx)
	branchA := createBranch(t, ctx, tenantA.ID)
	tenantB := createTenant(t, ctx)

	err := sharedPool.WithTenantTx(ctx, tenantA.ID, func(tx pgx.Tx) error {
		return r.SetRegularHours(ctx, tx, tenantA.ID, branchA.ID, []pub.RegularHours{
			{TenantID: tenantA.ID, BranchID: branchA.ID, DayOfWeek: time.Monday, OpenTime: tod(9, 0), CloseTime: tod(22, 0)},
		})
	})
	require.NoError(t, err)

	// tenant B queries using tenant A's real branch id under its OWN RLS
	// context — RLS must hide every row, yielding an empty slice, not tenant
	// A's schedule.
	err = sharedPool.WithTenantReadTx(ctx, tenantB.ID, func(tx pgx.Tx) error {
		got, err := r.GetRegularHours(ctx, tx, tenantB.ID, branchA.ID)
		if err != nil {
			return err
		}
		assert.Empty(t, got, "tenant B must not see tenant A's regular hours")
		return nil
	})
	require.NoError(t, err)
}

// TestHoursRepo_CrossTenantDelete_ChildID is invariant class 2 for special
// hours: tenant B, using its own valid tenant id, cannot delete tenant A's
// special-hours row by supplying A's real branch id.
func TestHoursRepo_CrossTenantDelete_ChildID(t *testing.T) {
	ctx := context.Background()
	r := repo.NewHoursRepo()

	tenantA := createTenant(t, ctx)
	branchA := createBranch(t, ctx, tenantA.ID)
	tenantB := createTenant(t, ctx)
	date := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	err := sharedPool.WithTenantTx(ctx, tenantA.ID, func(tx pgx.Tx) error {
		return r.UpsertSpecialHours(ctx, tx, tenantA.ID, branchA.ID, pub.SpecialHours{
			TenantID: tenantA.ID, BranchID: branchA.ID, SpecialDate: date, IsClosed: true,
		})
	})
	require.NoError(t, err)

	err = sharedPool.WithTenantTx(ctx, tenantB.ID, func(tx pgx.Tx) error {
		return r.DeleteSpecialHours(ctx, tx, tenantB.ID, branchA.ID, date)
	})
	require.ErrorIs(t, err, pub.ErrNotFound)

	// tenant A's row must survive.
	err = sharedPool.WithTenantReadTx(ctx, tenantA.ID, func(tx pgx.Tx) error {
		got, err := r.GetSpecialHours(ctx, tx, tenantA.ID, branchA.ID)
		if err != nil {
			return err
		}
		assert.Len(t, got, 1, "tenant A's special-hours row must survive the cross-tenant delete attempt")
		return nil
	})
	require.NoError(t, err)
}
