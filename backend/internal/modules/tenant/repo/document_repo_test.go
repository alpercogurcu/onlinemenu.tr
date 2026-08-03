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

// ---------------------------------------------------------------------------
// tenant_documents
// ---------------------------------------------------------------------------

// TestDocumentRepo_TenantDocuments_CRUD_RoundTrip covers create/list/get/
// update-status/soft-delete for the previously-untested DocumentRepo.
func TestDocumentRepo_TenantDocuments_CRUD_RoundTrip(t *testing.T) {
	ctx := context.Background()
	r := repo.NewDocumentRepo()

	tenant := createTenant(t, ctx)
	doc := createDocument(t, ctx, tenant.ID)
	assert.Equal(t, pub.DocStatusPending, doc.Status)

	err := sharedPool.WithTenantReadTx(ctx, tenant.ID, func(tx pgx.Tx) error {
		got, err := r.GetDocument(ctx, tx, tenant.ID, doc.ID)
		if err != nil {
			return err
		}
		assert.Equal(t, doc.FileKey, got.FileKey)

		list, err := r.ListDocuments(ctx, tx, tenant.ID)
		if err != nil {
			return err
		}
		found := false
		for _, d := range list {
			if d.ID == doc.ID {
				found = true
			}
		}
		assert.True(t, found)
		return nil
	})
	require.NoError(t, err)

	err = sharedPool.WithTenantTx(ctx, tenant.ID, func(tx pgx.Tx) error {
		return r.UpdateDocumentStatus(ctx, tx, tenant.ID, doc.ID, pub.DocStatusVerified, "")
	})
	require.NoError(t, err)

	err = sharedPool.WithTenantReadTx(ctx, tenant.ID, func(tx pgx.Tx) error {
		got, err := r.GetDocument(ctx, tx, tenant.ID, doc.ID)
		if err != nil {
			return err
		}
		assert.Equal(t, pub.DocStatusVerified, got.Status)
		return nil
	})
	require.NoError(t, err)

	// Soft delete: the document disappears from both Get and List.
	err = sharedPool.WithTenantTx(ctx, tenant.ID, func(tx pgx.Tx) error {
		return r.DeleteDocument(ctx, tx, tenant.ID, doc.ID)
	})
	require.NoError(t, err)

	err = sharedPool.WithTenantReadTx(ctx, tenant.ID, func(tx pgx.Tx) error {
		_, err := r.GetDocument(ctx, tx, tenant.ID, doc.ID)
		return err
	})
	require.ErrorIs(t, err, pub.ErrNotFound)

	err = sharedPool.WithTenantReadTx(ctx, tenant.ID, func(tx pgx.Tx) error {
		list, err := r.ListDocuments(ctx, tx, tenant.ID)
		if err != nil {
			return err
		}
		for _, d := range list {
			assert.NotEqual(t, doc.ID, d.ID, "soft-deleted document must not appear in ListDocuments")
		}
		return nil
	})
	require.NoError(t, err)
}

// TestDocumentRepo_DeleteDocument_AlreadyDeleted_NotFound proves the soft
// delete is not idempotent-success: deleting an already-deleted document
// reports ErrNotFound, matching the "deleted_at IS NULL" guard in the WHERE
// clause rather than silently succeeding a second time.
func TestDocumentRepo_DeleteDocument_AlreadyDeleted_NotFound(t *testing.T) {
	ctx := context.Background()
	r := repo.NewDocumentRepo()

	tenant := createTenant(t, ctx)
	doc := createDocument(t, ctx, tenant.ID)

	del := func() error {
		return sharedPool.WithTenantTx(ctx, tenant.ID, func(tx pgx.Tx) error {
			return r.DeleteDocument(ctx, tx, tenant.ID, doc.ID)
		})
	}
	require.NoError(t, del())
	require.ErrorIs(t, del(), pub.ErrNotFound)
}

// TestDocumentRepo_TenantDocuments_CrossTenantRead proves tenant B cannot
// read tenant A's document via GetDocument or ListDocuments (invariant class 1).
func TestDocumentRepo_TenantDocuments_CrossTenantRead(t *testing.T) {
	ctx := context.Background()
	r := repo.NewDocumentRepo()

	tenantA := createTenant(t, ctx)
	docA := createDocument(t, ctx, tenantA.ID)
	tenantB := createTenant(t, ctx)

	err := sharedPool.WithTenantReadTx(ctx, tenantB.ID, func(tx pgx.Tx) error {
		_, err := r.GetDocument(ctx, tx, tenantB.ID, docA.ID)
		return err
	})
	require.ErrorIs(t, err, pub.ErrNotFound)

	err = sharedPool.WithTenantReadTx(ctx, tenantB.ID, func(tx pgx.Tx) error {
		list, err := r.ListDocuments(ctx, tx, tenantB.ID)
		if err != nil {
			return err
		}
		for _, d := range list {
			assert.NotEqual(t, docA.ID, d.ID)
		}
		return nil
	})
	require.NoError(t, err)
}

// TestDocumentRepo_TenantDocuments_CrossTenantMutation_ChildID is the
// b2b-shaped hole check (invariant class 2) for tenant_documents: tenant B
// tries to change status / delete tenant A's document by its own id, from
// entirely within tenant B's own valid RLS context.
func TestDocumentRepo_TenantDocuments_CrossTenantMutation_ChildID(t *testing.T) {
	ctx := context.Background()
	r := repo.NewDocumentRepo()

	tenantA := createTenant(t, ctx)
	docA := createDocument(t, ctx, tenantA.ID)
	tenantB := createTenant(t, ctx)

	err := sharedPool.WithTenantTx(ctx, tenantB.ID, func(tx pgx.Tx) error {
		return r.UpdateDocumentStatus(ctx, tx, tenantB.ID, docA.ID, pub.DocStatusRejected, "hijack")
	})
	require.ErrorIs(t, err, pub.ErrNotFound)

	err = sharedPool.WithTenantTx(ctx, tenantB.ID, func(tx pgx.Tx) error {
		return r.DeleteDocument(ctx, tx, tenantB.ID, docA.ID)
	})
	require.ErrorIs(t, err, pub.ErrNotFound)

	// tenant A's document must be unaffected by either attempt.
	err = sharedPool.WithTenantReadTx(ctx, tenantA.ID, func(tx pgx.Tx) error {
		got, err := r.GetDocument(ctx, tx, tenantA.ID, docA.ID)
		if err != nil {
			return err
		}
		assert.Equal(t, pub.DocStatusPending, got.Status, "tenant A's document status must be untouched")
		return nil
	})
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// branch_documents
// ---------------------------------------------------------------------------

// TestDocumentRepo_BranchDocuments_CRUD_RoundTrip mirrors the tenant_documents
// round trip for the branch-scoped table.
func TestDocumentRepo_BranchDocuments_CRUD_RoundTrip(t *testing.T) {
	ctx := context.Background()
	r := repo.NewDocumentRepo()

	tenant := createTenant(t, ctx)
	branch := createBranch(t, ctx, tenant.ID)
	doc := createBranchDocument(t, ctx, tenant.ID, branch.ID)

	err := sharedPool.WithTenantReadTx(ctx, tenant.ID, func(tx pgx.Tx) error {
		list, err := r.ListBranchDocuments(ctx, tx, tenant.ID, branch.ID)
		if err != nil {
			return err
		}
		found := false
		for _, d := range list {
			if d.ID == doc.ID {
				found = true
			}
		}
		assert.True(t, found)
		return nil
	})
	require.NoError(t, err)

	err = sharedPool.WithTenantTx(ctx, tenant.ID, func(tx pgx.Tx) error {
		return r.UpdateBranchDocumentStatus(ctx, tx, tenant.ID, branch.ID, doc.ID, pub.DocStatusVerified, "")
	})
	require.NoError(t, err)

	err = sharedPool.WithTenantTx(ctx, tenant.ID, func(tx pgx.Tx) error {
		return r.DeleteBranchDocument(ctx, tx, tenant.ID, branch.ID, doc.ID)
	})
	require.NoError(t, err)

	err = sharedPool.WithTenantReadTx(ctx, tenant.ID, func(tx pgx.Tx) error {
		list, err := r.ListBranchDocuments(ctx, tx, tenant.ID, branch.ID)
		if err != nil {
			return err
		}
		for _, d := range list {
			assert.NotEqual(t, doc.ID, d.ID, "soft-deleted branch document must not appear in the list")
		}
		return nil
	})
	require.NoError(t, err)
}

// TestDocumentRepo_BranchDocuments_CrossBranchWithinSameTenant proves that
// even within the SAME tenant, a branch document cannot be mutated by
// supplying a different, sibling branch id — the repo comment on
// UpdateBranchDocumentStatus/DeleteBranchDocument says branchID "is required
// to prevent cross-branch mutations within the same tenant"; this is the
// regression test for that claim.
func TestDocumentRepo_BranchDocuments_CrossBranchWithinSameTenant(t *testing.T) {
	ctx := context.Background()
	r := repo.NewDocumentRepo()

	tenant := createTenant(t, ctx)
	branch1 := createBranch(t, ctx, tenant.ID)
	branch2 := createBranch(t, ctx, tenant.ID)
	doc := createBranchDocument(t, ctx, tenant.ID, branch1.ID)

	err := sharedPool.WithTenantTx(ctx, tenant.ID, func(tx pgx.Tx) error {
		return r.UpdateBranchDocumentStatus(ctx, tx, tenant.ID, branch2.ID, doc.ID, pub.DocStatusVerified, "")
	})
	require.ErrorIs(t, err, pub.ErrNotFound, "wrong sibling branch id must not be able to update the document")

	err = sharedPool.WithTenantTx(ctx, tenant.ID, func(tx pgx.Tx) error {
		return r.DeleteBranchDocument(ctx, tx, tenant.ID, branch2.ID, doc.ID)
	})
	require.ErrorIs(t, err, pub.ErrNotFound, "wrong sibling branch id must not be able to delete the document")

	// The document must still exist, untouched, under its real branch.
	err = sharedPool.WithTenantReadTx(ctx, tenant.ID, func(tx pgx.Tx) error {
		list, err := r.ListBranchDocuments(ctx, tx, tenant.ID, branch1.ID)
		if err != nil {
			return err
		}
		found := false
		for _, d := range list {
			if d.ID == doc.ID {
				found = true
				assert.Equal(t, pub.DocStatusPending, d.Status)
			}
		}
		assert.True(t, found, "document must still exist under its real branch")
		return nil
	})
	require.NoError(t, err)
}

// TestDocumentRepo_BranchDocuments_CrossTenantMutation_ChildID is invariant
// class 2 for branch_documents: tenant B, using its own valid tenant id,
// tries to mutate tenant A's branch document by supplying A's real branch id
// and document id.
func TestDocumentRepo_BranchDocuments_CrossTenantMutation_ChildID(t *testing.T) {
	ctx := context.Background()
	r := repo.NewDocumentRepo()

	tenantA := createTenant(t, ctx)
	branchA := createBranch(t, ctx, tenantA.ID)
	docA := createBranchDocument(t, ctx, tenantA.ID, branchA.ID)
	tenantB := createTenant(t, ctx)

	err := sharedPool.WithTenantTx(ctx, tenantB.ID, func(tx pgx.Tx) error {
		return r.UpdateBranchDocumentStatus(ctx, tx, tenantB.ID, branchA.ID, docA.ID, pub.DocStatusRejected, "hijack")
	})
	require.ErrorIs(t, err, pub.ErrNotFound)

	err = sharedPool.WithTenantTx(ctx, tenantB.ID, func(tx pgx.Tx) error {
		return r.DeleteBranchDocument(ctx, tx, tenantB.ID, branchA.ID, docA.ID)
	})
	require.ErrorIs(t, err, pub.ErrNotFound)
}
