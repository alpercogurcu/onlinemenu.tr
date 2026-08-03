package repo_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pub "onlinemenu.tr/internal/modules/tenant/public"
	"onlinemenu.tr/internal/modules/tenant/repo"
)

// TestIntegratorRepo_CRUD_RoundTrip is ordinary correctness coverage for the
// previously-untested IntegratorRepo.
func TestIntegratorRepo_CRUD_RoundTrip(t *testing.T) {
	ctx := context.Background()
	r := repo.NewIntegratorRepo()

	tenant := createTenant(t, ctx)
	integ := createIntegrator(t, ctx, tenant.ID, nil, pub.ProviderEDM)
	assert.Nil(t, integ.BranchID, "tenant-wide integrator must have a nil BranchID")

	err := sharedPool.WithTenantReadTx(ctx, tenant.ID, func(tx pgx.Tx) error {
		got, err := r.GetIntegrator(ctx, tx, tenant.ID, integ.ID)
		if err != nil {
			return err
		}
		assert.Equal(t, integ.DisplayName, got.DisplayName)
		assert.Equal(t, pub.ProviderEDM, got.Provider)

		list, err := r.ListIntegrators(ctx, tx, tenant.ID)
		if err != nil {
			return err
		}
		found := false
		for _, i := range list {
			if i.ID == integ.ID {
				found = true
			}
		}
		assert.True(t, found)
		return nil
	})
	require.NoError(t, err)

	integ.DisplayName = "EDM Yenilendi"
	integ.IsActive = false
	var updated pub.BillingIntegrator
	err = sharedPool.WithTenantTx(ctx, tenant.ID, func(tx pgx.Tx) error {
		var err error
		updated, err = r.UpdateIntegrator(ctx, tx, integ)
		return err
	})
	require.NoError(t, err)
	assert.Equal(t, "EDM Yenilendi", updated.DisplayName)
	assert.False(t, updated.IsActive)

	err = sharedPool.WithTenantTx(ctx, tenant.ID, func(tx pgx.Tx) error {
		return r.DeleteIntegrator(ctx, tx, tenant.ID, integ.ID)
	})
	require.NoError(t, err)

	err = sharedPool.WithTenantReadTx(ctx, tenant.ID, func(tx pgx.Tx) error {
		_, err := r.GetIntegrator(ctx, tx, tenant.ID, integ.ID)
		return err
	})
	require.ErrorIs(t, err, pub.ErrNotFound, "soft-deleted integrator must not be readable")
}

// TestIntegratorRepo_ConfigJSON_RoundTrip proves the config JSONB column
// preserves arbitrary key/value pairs through Create -> Get.
func TestIntegratorRepo_ConfigJSON_RoundTrip(t *testing.T) {
	ctx := context.Background()
	r := repo.NewIntegratorRepo()

	tenant := createTenant(t, ctx)
	f := pub.BillingIntegrator{
		TenantID:    tenant.ID,
		Provider:    pub.ProviderEDM,
		DisplayName: "EDM",
		Config:      map[string]any{"company_code": "ACME123", "gib_endpoint": "https://test.gib.gov.tr"},
		Environment: pub.EnvTest,
		IsActive:    true,
	}
	var created pub.BillingIntegrator
	err := sharedPool.WithTenantTx(ctx, tenant.ID, func(tx pgx.Tx) error {
		var err error
		created, err = r.CreateIntegrator(ctx, tx, f)
		return err
	})
	require.NoError(t, err)

	err = sharedPool.WithTenantReadTx(ctx, tenant.ID, func(tx pgx.Tx) error {
		got, err := r.GetIntegrator(ctx, tx, tenant.ID, created.ID)
		if err != nil {
			return err
		}
		assert.Equal(t, "ACME123", got.Config["company_code"])
		assert.Equal(t, "https://test.gib.gov.tr", got.Config["gib_endpoint"])
		return nil
	})
	require.NoError(t, err)
}

// TestIntegratorRepo_GetEffectiveIntegrator_BranchOverridesTenant proves the
// documented precedence: a branch-level record wins over the tenant-wide
// default for the same provider.
func TestIntegratorRepo_GetEffectiveIntegrator_BranchOverridesTenant(t *testing.T) {
	ctx := context.Background()
	r := repo.NewIntegratorRepo()

	tenant := createTenant(t, ctx)
	branch := createBranch(t, ctx, tenant.ID)

	tenantDefault := createIntegrator(t, ctx, tenant.ID, nil, pub.ProviderEDM)
	branchOverride := createIntegrator(t, ctx, tenant.ID, &branch.ID, pub.ProviderEDM)

	err := sharedPool.WithTenantReadTx(ctx, tenant.ID, func(tx pgx.Tx) error {
		got, err := r.GetEffectiveIntegrator(ctx, tx, tenant.ID, branch.ID, pub.ProviderEDM)
		if err != nil {
			return err
		}
		assert.Equal(t, branchOverride.ID, got.ID, "branch-level integrator must win over the tenant default")
		assert.NotEqual(t, tenantDefault.ID, got.ID)
		return nil
	})
	require.NoError(t, err)
}

// TestIntegratorRepo_GetEffectiveIntegrator_FallsBackToTenantDefault proves
// that a branch with no override resolves to the tenant-wide default.
func TestIntegratorRepo_GetEffectiveIntegrator_FallsBackToTenantDefault(t *testing.T) {
	ctx := context.Background()
	r := repo.NewIntegratorRepo()

	tenant := createTenant(t, ctx)
	branchWithoutOverride := createBranch(t, ctx, tenant.ID)
	tenantDefault := createIntegrator(t, ctx, tenant.ID, nil, pub.ProviderEDM)

	err := sharedPool.WithTenantReadTx(ctx, tenant.ID, func(tx pgx.Tx) error {
		got, err := r.GetEffectiveIntegrator(ctx, tx, tenant.ID, branchWithoutOverride.ID, pub.ProviderEDM)
		if err != nil {
			return err
		}
		assert.Equal(t, tenantDefault.ID, got.ID)
		return nil
	})
	require.NoError(t, err)
}

// TestIntegratorRepo_GetEffectiveIntegrator_NoActiveRecord_NotFound proves
// that a provider with no active tenant or branch record fails closed
// (ErrNotFound), rather than returning a zero-value integrator that callers
// might mistake for "configured but inactive".
func TestIntegratorRepo_GetEffectiveIntegrator_NoActiveRecord_NotFound(t *testing.T) {
	ctx := context.Background()
	r := repo.NewIntegratorRepo()

	tenant := createTenant(t, ctx)
	branch := createBranch(t, ctx, tenant.ID)

	err := sharedPool.WithTenantReadTx(ctx, tenant.ID, func(tx pgx.Tx) error {
		_, err := r.GetEffectiveIntegrator(ctx, tx, tenant.ID, branch.ID, pub.ProviderEDM)
		return err
	})
	require.ErrorIs(t, err, pub.ErrNotFound)
}

// TestIntegratorRepo_UniqueTenantLevelPerProvider locks in
// billing_int_tenant_provider_idx: at most one active tenant-wide (branch_id
// IS NULL) record per provider.
func TestIntegratorRepo_UniqueTenantLevelPerProvider(t *testing.T) {
	ctx := context.Background()
	r := repo.NewIntegratorRepo()

	tenant := createTenant(t, ctx)
	createIntegrator(t, ctx, tenant.ID, nil, pub.ProviderEDM)

	dup := pub.BillingIntegrator{
		TenantID: tenant.ID, Provider: pub.ProviderEDM, DisplayName: "Dup",
		Config: map[string]any{}, Environment: pub.EnvTest, IsActive: true,
	}
	err := sharedPool.WithTenantTx(ctx, tenant.ID, func(tx pgx.Tx) error {
		_, err := r.CreateIntegrator(ctx, tx, dup)
		return err
	})
	require.Error(t, err, "a second tenant-wide EDM integrator must be rejected")
}

// TestIntegratorRepo_UniqueBranchLevelPerProvider locks in
// billing_int_branch_provider_idx: at most one active record per (branch,
// provider) pair.
func TestIntegratorRepo_UniqueBranchLevelPerProvider(t *testing.T) {
	ctx := context.Background()
	r := repo.NewIntegratorRepo()

	tenant := createTenant(t, ctx)
	branch := createBranch(t, ctx, tenant.ID)
	createIntegrator(t, ctx, tenant.ID, &branch.ID, pub.ProviderEDM)

	dup := pub.BillingIntegrator{
		TenantID: tenant.ID, BranchID: &branch.ID, Provider: pub.ProviderEDM, DisplayName: "Dup",
		Config: map[string]any{}, Environment: pub.EnvTest, IsActive: true,
	}
	err := sharedPool.WithTenantTx(ctx, tenant.ID, func(tx pgx.Tx) error {
		_, err := r.CreateIntegrator(ctx, tx, dup)
		return err
	})
	require.Error(t, err, "a second branch-level EDM integrator for the same branch must be rejected")
}

// TestIntegratorRepo_CrossTenantRead proves tenant B cannot read tenant A's
// integrator (invariant class 1).
func TestIntegratorRepo_CrossTenantRead(t *testing.T) {
	ctx := context.Background()
	r := repo.NewIntegratorRepo()

	tenantA := createTenant(t, ctx)
	integA := createIntegrator(t, ctx, tenantA.ID, nil, pub.ProviderEDM)
	tenantB := createTenant(t, ctx)

	err := sharedPool.WithTenantReadTx(ctx, tenantB.ID, func(tx pgx.Tx) error {
		_, err := r.GetIntegrator(ctx, tx, tenantB.ID, integA.ID)
		return err
	})
	require.ErrorIs(t, err, pub.ErrNotFound)

	err = sharedPool.WithTenantReadTx(ctx, tenantB.ID, func(tx pgx.Tx) error {
		list, err := r.ListIntegrators(ctx, tx, tenantB.ID)
		if err != nil {
			return err
		}
		for _, i := range list {
			assert.NotEqual(t, integA.ID, i.ID)
		}
		return nil
	})
	require.NoError(t, err)
}

// TestIntegratorRepo_CrossTenantMutation_ChildID is invariant class 2:
// tenant B, using its own valid tenant id, cannot update or delete tenant
// A's integrator by supplying its id.
func TestIntegratorRepo_CrossTenantMutation_ChildID(t *testing.T) {
	ctx := context.Background()
	r := repo.NewIntegratorRepo()

	tenantA := createTenant(t, ctx)
	integA := createIntegrator(t, ctx, tenantA.ID, nil, pub.ProviderEDM)
	tenantB := createTenant(t, ctx)

	attempt := integA
	attempt.TenantID = tenantB.ID
	attempt.DisplayName = "Hijacked by B"
	err := sharedPool.WithTenantTx(ctx, tenantB.ID, func(tx pgx.Tx) error {
		_, err := r.UpdateIntegrator(ctx, tx, attempt)
		return err
	})
	require.ErrorIs(t, err, pub.ErrNotFound)

	err = sharedPool.WithTenantTx(ctx, tenantB.ID, func(tx pgx.Tx) error {
		return r.DeleteIntegrator(ctx, tx, tenantB.ID, integA.ID)
	})
	require.ErrorIs(t, err, pub.ErrNotFound)

	// tenant A's integrator must be unaffected.
	err = sharedPool.WithTenantReadTx(ctx, tenantA.ID, func(tx pgx.Tx) error {
		got, err := r.GetIntegrator(ctx, tx, tenantA.ID, integA.ID)
		if err != nil {
			return err
		}
		assert.Equal(t, integA.DisplayName, got.DisplayName)
		return nil
	})
	require.NoError(t, err)
}
