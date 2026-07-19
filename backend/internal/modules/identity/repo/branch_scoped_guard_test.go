package repo_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/modules/identity/domain"
	"onlinemenu.tr/internal/modules/identity/repo"
)

// These tests cover identity migration 000012 (ADR-SEC-005): a branch-scoped role
// may not be granted through a chain-wide (branch_id IS NULL) membership.
// Dropping the trigger or the branch_scoped column turns them red.

func newTestPerson(ctx context.Context, t *testing.T) domain.Person {
	t.Helper()
	pr := repo.NewPersonRepo()
	// NOTE: no require/assert inside the tx callback. testify's FailNow calls
	// runtime.Goexit, and db.WithTenantTx/WithAllTenantsTx roll back only on the
	// error return path (no deferred rollback) — a failed assertion there leaks
	// the pooled connection in "idle in transaction" and deadlocks the suite
	// once MaxConns is exhausted.
	var person domain.Person
	err := sharedPool.WithAllTenantsTx(ctx, func(tx pgx.Tx) error {
		var createErr error
		person, createErr = pr.Create(ctx, tx, domain.Person{
			KeycloakSub: "kc-sub-" + uuid.NewString(),
			Email:       "guard+" + uuid.NewString() + "@example.com",
			FullName:    "Guard Test",
		})
		return createErr
	})
	require.NoError(t, err)
	return person
}

func TestRoleRepo_BranchScopedFlagOnSystemTemplates(t *testing.T) {
	ctx := context.Background()
	r := repo.NewRoleRepo()

	want := map[string]bool{
		"cashier":       true,
		"shift_manager": true,
		"driver":        true,
		"kitchen":       true,
		"bar":           true,
		"warehouse":     true,
		"manager":       false,
	}

	var roles []domain.Role
	require.NoError(t, sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		var err error
		roles, err = r.ListForTenant(ctx, tx, tenantA)
		return err
	}))

	got := make(map[string]bool, len(want))
	for _, ro := range roles {
		if ro.SystemKey == "" {
			continue
		}
		if _, ok := want[ro.SystemKey]; ok {
			got[ro.SystemKey] = ro.BranchScoped
		}
	}
	assert.Equal(t, want, got)
}

func TestMembershipRepo_BranchScopedRoleRejectsNilBranch(t *testing.T) {
	ctx := context.Background()
	mr := repo.NewMembershipRepo()
	person := newTestPerson(ctx, t)

	branchScopedRoles := map[string]uuid.UUID{}
	for _, systemKey := range []string{"cashier", "kitchen", "warehouse"} {
		branchScopedRoles[systemKey] = systemRoleID(t, systemKey)
	}

	for systemKey, roleID := range branchScopedRoles {
		t.Run(systemKey, func(t *testing.T) {
			err := sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
				_, createErr := mr.Create(ctx, tx, domain.Membership{
					PersonID: person.ID,
					TenantID: tenantA,
					BranchID: nil,
					RoleID:   roleID,
					Status:   domain.MembershipActive,
				})
				return createErr
			})
			require.Error(t, err, "chain-wide membership for branch-scoped role must be rejected")

			var pgErr *pgconn.PgError
			require.ErrorAs(t, err, &pgErr)
			assert.Equal(t, "23514", pgErr.Code)
		})
	}
}

func TestMembershipRepo_ChainWideRoleAcceptsNilBranch(t *testing.T) {
	ctx := context.Background()
	mr := repo.NewMembershipRepo()
	person := newTestPerson(ctx, t)
	managerRoleID := systemRoleID(t, "manager")

	var created domain.Membership
	err := sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		var createErr error
		created, createErr = mr.Create(ctx, tx, domain.Membership{
			PersonID: person.ID,
			TenantID: tenantA,
			BranchID: nil,
			RoleID:   managerRoleID,
			Status:   domain.MembershipActive,
		})
		return createErr
	})
	require.NoError(t, err)
	assert.Nil(t, created.BranchID)
}

func TestMembershipRepo_UniqueNullsNotDistinct(t *testing.T) {
	ctx := context.Background()
	mr := repo.NewMembershipRepo()
	person := newTestPerson(ctx, t)
	managerRoleID := systemRoleID(t, "manager")

	m := domain.Membership{
		PersonID: person.ID,
		TenantID: tenantA,
		BranchID: nil,
		RoleID:   managerRoleID,
		Status:   domain.MembershipActive,
	}

	require.NoError(t, sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		_, createErr := mr.Create(ctx, tx, m)
		return createErr
	}))

	err := sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		_, createErr := mr.Create(ctx, tx, m)
		return createErr
	})
	require.Error(t, err, "duplicate chain-wide membership must violate memberships_unique")

	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	assert.Equal(t, "23505", pgErr.Code)
	assert.Equal(t, "memberships_unique", pgErr.ConstraintName)
}

// TestMembershipRepo_InvisibleRoleFailsClosed covers the guard's NOT FOUND branch.
// Asserting 23514 (not merely "an error") is what gives the test teeth: the
// BEFORE ROW trigger runs ahead of FK validation, so without migration 000012 the
// dangling case would surface as 23503 and the exists-but-invisible case would
// succeed outright — both turn this test red.
func TestMembershipRepo_InvisibleRoleFailsClosed(t *testing.T) {
	ctx := context.Background()
	mr := repo.NewMembershipRepo()
	rr := repo.NewRoleRepo()
	person := newTestPerson(ctx, t)

	// A real, chain-wide role owned by tenantB: the FK is satisfiable, but
	// roles_select RLS hides the row from a tenantA transaction.
	var foreignRole domain.Role
	require.NoError(t, sharedPool.WithTenantTx(ctx, tenantB, func(tx pgx.Tx) error {
		var createErr error
		foreignRole, createErr = rr.Create(ctx, tx, domain.Role{
			TenantID: &tenantB,
			Name:     "Yabancı Rol " + uuid.NewString(),
		})
		return createErr
	}))
	require.False(t, foreignRole.BranchScoped, "fixture must be chain-wide, so only invisibility can reject it")

	cases := map[string]uuid.UUID{
		"dangling":             uuid.New(),
		"exists_but_invisible": foreignRole.ID,
	}

	for name, roleID := range cases {
		t.Run(name, func(t *testing.T) {
			err := sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
				_, createErr := mr.Create(ctx, tx, domain.Membership{
					PersonID: person.ID,
					TenantID: tenantA,
					BranchID: nil,
					RoleID:   roleID,
					Status:   domain.MembershipActive,
				})
				return createErr
			})
			require.Error(t, err, "role invisible under tenantA RLS must fail closed")

			var pgErr *pgconn.PgError
			require.ErrorAs(t, err, &pgErr)
			assert.Equal(t, "23514", pgErr.Code,
				"must be the trigger's fail-closed reject, not an FK violation")
		})
	}
}
