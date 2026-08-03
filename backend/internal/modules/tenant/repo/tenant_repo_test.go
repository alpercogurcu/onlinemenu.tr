package repo_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pub "onlinemenu.tr/internal/modules/tenant/public"
	"onlinemenu.tr/internal/modules/tenant/repo"
)

// TestTenantRepo_CRUD_RoundTrip exercises Create/GetByID/Update/Deactivate
// as ordinary correctness coverage for the previously-untested TenantRepo.
func TestTenantRepo_CRUD_RoundTrip(t *testing.T) {
	ctx := context.Background()
	r := repo.NewTenantRepo()

	tenant := createTenant(t, ctx)

	// Get reflects what was written.
	err := sharedPool.WithTenantReadTx(ctx, tenant.ID, func(tx pgx.Tx) error {
		got, err := r.GetByID(ctx, tx, tenant.ID)
		if err != nil {
			return err
		}
		assert.Equal(t, tenant.Slug, got.Slug)
		assert.Equal(t, tenant.TaxNo, got.TaxNo)
		assert.Equal(t, pub.PlanStarter, got.Plan)
		assert.True(t, got.IsActive)
		return nil
	})
	require.NoError(t, err)

	// Update persists mutable fields.
	tenant.Name = "Renamed İşletme"
	tenant.Plan = pub.PlanPro
	var updated pub.Tenant
	err = sharedPool.WithTenantTx(ctx, tenant.ID, func(tx pgx.Tx) error {
		var err error
		updated, err = r.Update(ctx, tx, tenant)
		return err
	})
	require.NoError(t, err)
	assert.Equal(t, "Renamed İşletme", updated.Name)
	assert.Equal(t, pub.PlanPro, updated.Plan)

	err = sharedPool.WithTenantReadTx(ctx, tenant.ID, func(tx pgx.Tx) error {
		got, err := r.GetByID(ctx, tx, tenant.ID)
		if err != nil {
			return err
		}
		assert.Equal(t, "Renamed İşletme", got.Name)
		assert.Equal(t, pub.PlanPro, got.Plan)
		return nil
	})
	require.NoError(t, err)

	// Deactivate: GetByID filters is_active = true, so the tenant becomes
	// invisible through the normal read path afterwards.
	err = sharedPool.WithTenantTx(ctx, tenant.ID, func(tx pgx.Tx) error {
		return r.Deactivate(ctx, tx, tenant.ID)
	})
	require.NoError(t, err)

	err = sharedPool.WithTenantReadTx(ctx, tenant.ID, func(tx pgx.Tx) error {
		_, err := r.GetByID(ctx, tx, tenant.ID)
		return err
	})
	require.ErrorIs(t, err, pub.ErrNotFound)
}

// TestTenantRepo_Deactivate_UnknownID confirms deactivating a non-existent
// tenant id (but a syntactically valid, non-nil one) reports ErrNotFound
// rather than silently succeeding.
func TestTenantRepo_Deactivate_UnknownID(t *testing.T) {
	ctx := context.Background()
	r := repo.NewTenantRepo()

	ghost := mustNewID(t)
	err := sharedPool.WithTenantTx(ctx, ghost, func(tx pgx.Tx) error {
		return r.Deactivate(ctx, tx, ghost)
	})
	require.ErrorIs(t, err, pub.ErrNotFound)
}

// TestTenantRepo_Create_DuplicateSlugRejected locks in the slug uniqueness
// constraint (tenants_slug_idx) used for subdomain routing.
func TestTenantRepo_Create_DuplicateSlugRejected(t *testing.T) {
	ctx := context.Background()
	r := repo.NewTenantRepo()

	first := createTenant(t, ctx)

	dupID := mustNewID(t)
	dup := pub.Tenant{
		ID:             dupID,
		Name:           "Duplicate Slug Co",
		Slug:           first.Slug, // collision
		Plan:           pub.PlanStarter,
		EnabledModules: []string{"pos"},
		IdentityType:   pub.IdentityKurumsal,
		TaxNo:          "TAXNO" + uniqueSuffix(),
		MersisNo:       "MERSIS" + uniqueSuffix(),
		IsActive:       true,
	}

	err := sharedPool.WithTenantTx(ctx, dupID, func(tx pgx.Tx) error {
		_, err := r.Create(ctx, tx, dup)
		return err
	})
	require.Error(t, err, "duplicate slug must be rejected by tenants_slug_idx")
}

// TestTenantRepo_CrossTenantRead proves tenant B's RLS-scoped read cannot see
// tenant A's row via GetByID, even by exact primary key (invariant class 1).
func TestTenantRepo_CrossTenantRead(t *testing.T) {
	ctx := context.Background()
	r := repo.NewTenantRepo()

	tenantA := createTenant(t, ctx)
	tenantB := createTenant(t, ctx)

	err := sharedPool.WithTenantReadTx(ctx, tenantB.ID, func(tx pgx.Tx) error {
		_, err := r.GetByID(ctx, tx, tenantA.ID)
		return err
	})
	require.ErrorIs(t, err, pub.ErrNotFound, "tenant B must not read tenant A's row by id")
}

// TestTenantRepo_CrossTenantUpdate_NotFoundNotForbidden proves that tenant B,
// operating entirely under its own (valid, non-nil) RLS context, cannot
// mutate tenant A's row by addressing it via A's id — and that the failure
// mode is ErrNotFound, not a distinguishable "forbidden" response, so
// existence is not leaked (invariant class 2, applied to the tenant's own
// row rather than a child entity).
func TestTenantRepo_CrossTenantUpdate_NotFoundNotForbidden(t *testing.T) {
	ctx := context.Background()
	r := repo.NewTenantRepo()

	tenantA := createTenant(t, ctx)
	tenantB := createTenant(t, ctx)

	attempt := tenantA
	attempt.Name = "Hijacked by B"

	err := sharedPool.WithTenantTx(ctx, tenantB.ID, func(tx pgx.Tx) error {
		_, err := r.Update(ctx, tx, attempt)
		return err
	})
	require.ErrorIs(t, err, pub.ErrNotFound)

	// tenant A's row must be untouched.
	err = sharedPool.WithTenantReadTx(ctx, tenantA.ID, func(tx pgx.Tx) error {
		got, err := r.GetByID(ctx, tx, tenantA.ID)
		if err != nil {
			return err
		}
		assert.Equal(t, tenantA.Name, got.Name, "tenant A's name must survive the cross-tenant write attempt")
		return nil
	})
	require.NoError(t, err)
}

// TestTenantRepo_IsModuleEnabled covers the JSONB `?` containment query used
// by other modules to gate module-scoped features.
func TestTenantRepo_IsModuleEnabled(t *testing.T) {
	ctx := context.Background()
	r := repo.NewTenantRepo()

	tenant := createTenant(t, ctx) // EnabledModules: []string{"pos"}

	err := sharedPool.WithTenantReadTx(ctx, tenant.ID, func(tx pgx.Tx) error {
		enabled, err := r.IsModuleEnabled(ctx, tx, tenant.ID, "pos")
		if err != nil {
			return err
		}
		assert.True(t, enabled, "pos must be enabled per fixture")

		enabled, err = r.IsModuleEnabled(ctx, tx, tenant.ID, "catalog")
		if err != nil {
			return err
		}
		assert.False(t, enabled, "catalog was never enabled")
		return nil
	})
	require.NoError(t, err)
}

// TestTenantRepo_IsModuleEnabled_UnknownTenant proves the lookup fails closed
// (ErrNotFound) rather than reporting "enabled=false" for a tenant id that
// does not resolve under the active RLS context — an important distinction
// for callers deciding whether to treat the result as authoritative.
func TestTenantRepo_IsModuleEnabled_UnknownTenant(t *testing.T) {
	ctx := context.Background()
	r := repo.NewTenantRepo()

	tenantA := createTenant(t, ctx)
	ghost := mustNewID(t)

	err := sharedPool.WithTenantReadTx(ctx, tenantA.ID, func(tx pgx.Tx) error {
		_, err := r.IsModuleEnabled(ctx, tx, ghost, "pos")
		return err
	})
	require.True(t, errors.Is(err, pub.ErrNotFound), "expected ErrNotFound, got: %v", err)
}
