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

// TestBranchRepo_CRUD_RoundTrip is ordinary correctness coverage for the
// previously-untested BranchRepo: create, get, list, update.
func TestBranchRepo_CRUD_RoundTrip(t *testing.T) {
	ctx := context.Background()
	r := repo.NewBranchRepo()

	tenant := createTenant(t, ctx)
	branch := createBranch(t, ctx, tenant.ID)

	err := sharedPool.WithTenantReadTx(ctx, tenant.ID, func(tx pgx.Tx) error {
		got, err := r.GetBranch(ctx, tx, tenant.ID, branch.ID)
		if err != nil {
			return err
		}
		assert.Equal(t, branch.Name, got.Name)
		assert.Equal(t, branch.Slug, got.Slug)
		assert.Equal(t, pub.OwnershipSube, got.OwnershipType)
		return nil
	})
	require.NoError(t, err)

	err = sharedPool.WithTenantReadTx(ctx, tenant.ID, func(tx pgx.Tx) error {
		list, err := r.ListBranches(ctx, tx, tenant.ID)
		if err != nil {
			return err
		}
		found := false
		for _, b := range list {
			if b.ID == branch.ID {
				found = true
			}
		}
		assert.True(t, found, "ListBranches must include the created branch")
		return nil
	})
	require.NoError(t, err)

	branch.Name = "Renamed Şube"
	branch.OwnershipType = pub.OwnershipFranchise
	var updated pub.Branch
	err = sharedPool.WithTenantTx(ctx, tenant.ID, func(tx pgx.Tx) error {
		var err error
		updated, err = r.UpdateBranch(ctx, tx, branch)
		return err
	})
	require.NoError(t, err)
	assert.Equal(t, "Renamed Şube", updated.Name)
	assert.Equal(t, pub.OwnershipFranchise, updated.OwnershipType)
}

// TestBranchRepo_UpdateBranch_UnknownID confirms updating a syntactically
// valid but non-existent branch id under the correct tenant reports
// ErrNotFound instead of a generic scan error.
func TestBranchRepo_UpdateBranch_UnknownID(t *testing.T) {
	ctx := context.Background()
	r := repo.NewBranchRepo()

	tenant := createTenant(t, ctx)
	ghost := pub.Branch{
		ID:            mustNewID(t),
		TenantID:      tenant.ID,
		Name:          "Ghost",
		OwnershipType: pub.OwnershipSube,
		OperationType: pub.OperationRestoran,
		IdentityType:  pub.IdentityKurumsal,
	}

	err := sharedPool.WithTenantTx(ctx, tenant.ID, func(tx pgx.Tx) error {
		_, err := r.UpdateBranch(ctx, tx, ghost)
		return err
	})
	require.ErrorIs(t, err, pub.ErrNotFound)
}

// TestBranchRepo_DuplicateSlugPerTenantRejected locks in
// branches_tenant_slug_idx: a slug must be unique within a tenant.
func TestBranchRepo_DuplicateSlugPerTenantRejected(t *testing.T) {
	ctx := context.Background()
	r := repo.NewBranchRepo()

	tenant := createTenant(t, ctx)
	first := createBranch(t, ctx, tenant.ID)

	dup := pub.Branch{
		TenantID:      tenant.ID,
		Name:          "Duplicate Slug Branch",
		Slug:          first.Slug,
		OwnershipType: pub.OwnershipSube,
		OperationType: pub.OperationRestoran,
		IdentityType:  pub.IdentityKurumsal,
		TaxNo:         "BTAXNO" + uniqueSuffix(),
	}

	err := sharedPool.WithTenantTx(ctx, tenant.ID, func(tx pgx.Tx) error {
		_, err := r.CreateBranch(ctx, tx, dup)
		return err
	})
	require.Error(t, err, "duplicate (tenant_id, slug) must be rejected by branches_tenant_slug_idx")
}

// TestBranchRepo_SameSlugAcrossDifferentTenants_Allowed proves the slug
// uniqueness index is scoped per-tenant, not global — two different tenants
// may each use e.g. "merkez-sube".
func TestBranchRepo_SameSlugAcrossDifferentTenants_Allowed(t *testing.T) {
	ctx := context.Background()
	r := repo.NewBranchRepo()

	tenantA := createTenant(t, ctx)
	tenantB := createTenant(t, ctx)

	slug := "shared-slug-" + uniqueSuffix()

	mk := func(tenantID pub.Tenant) error {
		return sharedPool.WithTenantTx(ctx, tenantID.ID, func(tx pgx.Tx) error {
			_, err := r.CreateBranch(ctx, tx, pub.Branch{
				TenantID:      tenantID.ID,
				Name:          "Şube",
				Slug:          slug,
				OwnershipType: pub.OwnershipSube,
				OperationType: pub.OperationRestoran,
				IdentityType:  pub.IdentityKurumsal,
				TaxNo:         "BTAXNO" + uniqueSuffix(),
			})
			return err
		})
	}

	require.NoError(t, mk(tenantA))
	require.NoError(t, mk(tenantB), "same slug under a different tenant must be allowed")
}

// TestBranchRepo_CrossTenantRead proves tenant B cannot see tenant A's
// branch, neither via ListBranches nor via a direct GetBranch by id
// (invariant class 1).
func TestBranchRepo_CrossTenantRead(t *testing.T) {
	ctx := context.Background()
	r := repo.NewBranchRepo()

	tenantA := createTenant(t, ctx)
	branchA := createBranch(t, ctx, tenantA.ID)
	tenantB := createTenant(t, ctx)

	err := sharedPool.WithTenantReadTx(ctx, tenantB.ID, func(tx pgx.Tx) error {
		list, err := r.ListBranches(ctx, tx, tenantB.ID)
		if err != nil {
			return err
		}
		for _, b := range list {
			assert.NotEqual(t, branchA.ID, b.ID, "tenant B's branch list must not contain tenant A's branch")
		}
		return nil
	})
	require.NoError(t, err)

	err = sharedPool.WithTenantReadTx(ctx, tenantB.ID, func(tx pgx.Tx) error {
		_, err := r.GetBranch(ctx, tx, tenantB.ID, branchA.ID)
		return err
	})
	require.ErrorIs(t, err, pub.ErrNotFound, "tenant B must not read tenant A's branch by id")
}

// TestBranchRepo_CrossTenantUpdate_ChildID is the b2b-shaped hole check
// (invariant class 2): tenant B, entirely within its own valid RLS context,
// attempts to update tenant A's branch by addressing it via the branch's own
// id. It must fail as ErrNotFound (existence not leaked), and tenant A's row
// must be unmodified.
func TestBranchRepo_CrossTenantUpdate_ChildID(t *testing.T) {
	ctx := context.Background()
	r := repo.NewBranchRepo()

	tenantA := createTenant(t, ctx)
	branchA := createBranch(t, ctx, tenantA.ID)
	tenantB := createTenant(t, ctx)

	attempt := branchA
	attempt.TenantID = tenantB.ID // attacker supplies their OWN tenant id
	attempt.Name = "Hijacked by tenant B"

	err := sharedPool.WithTenantTx(ctx, tenantB.ID, func(tx pgx.Tx) error {
		_, err := r.UpdateBranch(ctx, tx, attempt)
		return err
	})
	require.ErrorIs(t, err, pub.ErrNotFound)

	err = sharedPool.WithTenantReadTx(ctx, tenantA.ID, func(tx pgx.Tx) error {
		got, err := r.GetBranch(ctx, tx, tenantA.ID, branchA.ID)
		if err != nil {
			return err
		}
		assert.Equal(t, branchA.Name, got.Name, "tenant A's branch must survive the cross-tenant write attempt")
		return nil
	})
	require.NoError(t, err)
}
