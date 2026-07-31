package repo_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/identity/domain"
	pub "onlinemenu.tr/internal/modules/identity/public"
	"onlinemenu.tr/internal/modules/identity/repo"
	"onlinemenu.tr/internal/modules/identity/service"
)

// These tests cover identity migration 000013 (ADR-SEC-005 § "000013 eki") and
// the branch_scoped role API. Dropping the migration or the service-level
// FALSE -> TRUE refusal turns them red.

// newForeignRole creates a chain-wide custom role owned by tenantB.
func newForeignRole(ctx context.Context, t *testing.T) domain.Role {
	t.Helper()
	rr := repo.NewRoleRepo()
	var role domain.Role
	// No require/assert inside the tx callback — see newTestPerson.
	err := sharedPool.WithTenantTx(ctx, tenantB, func(tx pgx.Tx) error {
		var createErr error
		role, createErr = rr.Create(ctx, tx, domain.Role{
			TenantID: &tenantB,
			Name:     "Yabancı Rol " + uuid.NewString(),
		})
		return createErr
	})
	require.NoError(t, err)
	return role
}

// TestMembershipRepo_ConcreteBranchRejectsForeignTenantRole is the test that
// actually gates migration 000013.
//
// On 000012 alone this case SUCCEEDS: the guard returns early whenever
// NEW.branch_id IS NOT NULL, so the role row is never read, and the role_id FK
// is validated by the system (RLS-exempt) — a membership at tenantA's branch
// pointing at tenantB's role is accepted at the DB layer. 000013 moves the
// lookup ahead of the early return, so the row is now rejected.
//
// Note that the nil-branch variant of this test would prove nothing: it is
// already green on 000012 via the invisible-role fail-closed branch
// (TestMembershipRepo_InvisibleRoleFailsClosed covers it). The concrete
// branch_id is the whole point here.
func TestMembershipRepo_ConcreteBranchRejectsForeignTenantRole(t *testing.T) {
	ctx := context.Background()
	mr := repo.NewMembershipRepo()
	person := newTestPerson(ctx, t)
	foreignRole := newForeignRole(ctx, t)

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
					BranchID: &branchA,
					RoleID:   roleID,
					Status:   domain.MembershipActive,
				})
				return createErr
			})
			require.Error(t, err, "a concrete-branch membership must not escape the role/tenant guard")

			var pgErr *pgconn.PgError
			require.ErrorAs(t, err, &pgErr)
			assert.Equal(t, "23514", pgErr.Code,
				"must be the guard's reject, not an FK violation (23503) — the FK bypasses RLS")
		})
	}
}

// TestMembershipRepo_ConcreteBranchAcceptsOwnRole is the negative control: the
// unconditional lookup added in 000013 must not break the ordinary path.
func TestMembershipRepo_ConcreteBranchAcceptsOwnRole(t *testing.T) {
	ctx := context.Background()
	mr := repo.NewMembershipRepo()
	person := newTestPerson(ctx, t)
	cashierRoleID := systemRoleID(t, "cashier")

	var created domain.Membership
	err := sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		var createErr error
		created, createErr = mr.Create(ctx, tx, domain.Membership{
			PersonID: person.ID,
			TenantID: tenantA,
			BranchID: &branchA,
			RoleID:   cashierRoleID,
			Status:   domain.MembershipActive,
		})
		return createErr
	})
	require.NoError(t, err, "a branch-scoped system role at a concrete branch is the normal case")
	assert.Equal(t, &branchA, created.BranchID)
}

// newRoleService builds a RoleService against the shared test pool.
// The redis client is never reached on the Create/Update paths under test
// (only LoadPermSet and InvalidatePermCache touch the cache), so an unconnected
// client is deliberate rather than a missing fixture.
func newRoleService(t *testing.T) *service.RoleService {
	t.Helper()
	cache := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { _ = cache.Close() })
	return service.NewRoleService(service.RoleParams{
		DB:             sharedPool,
		RoleRepo:       repo.NewRoleRepo(),
		PermRepo:       repo.NewPermissionRepo(),
		MembershipRepo: repo.NewMembershipRepo(),
		Cache:          cache,
		Logger:         zap.NewNop(),
	})
}

func TestRoleService_CreateCarriesBranchScoped(t *testing.T) {
	ctx := context.Background()
	svc := newRoleService(t)

	t.Run("tenant_role_chain_wide", func(t *testing.T) {
		created, err := svc.CreateTenantRole(ctx, tenantA, "Zincir Rolü "+uuid.NewString(), false)
		require.NoError(t, err)
		assert.False(t, created.BranchScoped)
	})

	t.Run("tenant_role_branch_scoped", func(t *testing.T) {
		created, err := svc.CreateTenantRole(ctx, tenantA, "Şube Rolü "+uuid.NewString(), true)
		require.NoError(t, err)
		assert.True(t, created.BranchScoped,
			"the flag must reach the roles row — before this it was always false")
	})

	t.Run("branch_owned_role_is_forced_true", func(t *testing.T) {
		created, err := svc.CreateBranchRole(ctx, tenantA, branchA, "Şube Sahibi "+uuid.NewString(), false)
		require.NoError(t, err)
		assert.True(t, created.BranchScoped,
			"RequiresBranch() must win: a branch-owned role can never be granted chain-wide")
	})
}

func TestRoleService_UpdateBranchScoped(t *testing.T) {
	ctx := context.Background()
	svc := newRoleService(t)

	t.Run("false_to_true_without_memberships_succeeds", func(t *testing.T) {
		created, err := svc.CreateTenantRole(ctx, tenantA, "Yükseltilecek "+uuid.NewString(), false)
		require.NoError(t, err)

		updated, err := svc.Update(ctx, tenantA, created.ID, created.Name, true)
		require.NoError(t, err)
		assert.True(t, updated.BranchScoped)
	})

	t.Run("true_to_false_is_permissive_and_allowed", func(t *testing.T) {
		created, err := svc.CreateTenantRole(ctx, tenantA, "Gevşetilecek "+uuid.NewString(), true)
		require.NoError(t, err)

		updated, err := svc.Update(ctx, tenantA, created.ID, created.Name, false)
		require.NoError(t, err)
		assert.False(t, updated.BranchScoped)
	})

	t.Run("branch_owned_role_cannot_be_loosened", func(t *testing.T) {
		created, err := svc.CreateBranchRole(ctx, tenantA, branchA, "Şube Sabit "+uuid.NewString(), true)
		require.NoError(t, err)

		updated, err := svc.Update(ctx, tenantA, created.ID, created.Name, false)
		require.NoError(t, err)
		assert.True(t, updated.BranchScoped,
			"branch ownership outranks the request body")
	})

	t.Run("system_role_is_immutable", func(t *testing.T) {
		// A fresh context, not the outer ctx: systemRoleID opens its own
		// context, and contextcheck rejects mixing an inherited context with a
		// call chain that starts a new one. Behaviourally identical here — the
		// outer ctx carries no deadline or values.
		roleID := systemRoleID(t, "cashier")
		_, err := svc.Update(context.Background(), tenantA, roleID, "Ele Geçirilmiş Kasiyer", false)
		require.ErrorIs(t, err, pub.ErrInvalid)
	})
}

// TestRoleService_UpdateRefusesFlipWithChainWideMemberships pins the decision
// documented in ADR-SEC-005: the FALSE -> TRUE flip is refused, not warned
// about. The trigger fires on membership writes only, so the pre-existing
// chain-wide rows would survive the flip as live chain-wide grants of a
// now branch-scoped role.
func TestRoleService_UpdateRefusesFlipWithChainWideMemberships(t *testing.T) {
	ctx := context.Background()
	svc := newRoleService(t)
	mr := repo.NewMembershipRepo()
	person := newTestPerson(ctx, t)

	role, err := svc.CreateTenantRole(ctx, tenantA, "İhlalli Rol "+uuid.NewString(), false)
	require.NoError(t, err)

	var membership domain.Membership
	require.NoError(t, sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		var createErr error
		membership, createErr = mr.Create(ctx, tx, domain.Membership{
			PersonID: person.ID,
			TenantID: tenantA,
			BranchID: nil,
			RoleID:   role.ID,
			Status:   domain.MembershipActive,
		})
		return createErr
	}))

	_, err = svc.Update(ctx, tenantA, role.ID, role.Name, true)
	require.Error(t, err, "the flip must be refused while a chain-wide grant is live")

	var conflict pub.BranchScopeConflictError
	require.ErrorAs(t, err, &conflict, "the caller needs the count, not a bare 400")
	assert.Equal(t, role.ID, conflict.RoleID)
	assert.Equal(t, 1, conflict.ChainWideMemberships)
	require.ErrorIs(t, err, pub.ErrConflict, "must map to HTTP 409")

	// The refusal must not have written a partial update.
	roles, err := svc.ListForTenant(ctx, tenantA)
	require.NoError(t, err)
	for _, r := range roles {
		if r.ID == role.ID {
			assert.False(t, r.BranchScoped, "refused flip must leave the row untouched")
		}
	}

	// Terminating the offending membership clears the conflict: the refusal is a
	// solvable state, not a dead end.
	require.NoError(t, sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		return mr.UpdateStatus(ctx, tx, tenantA, membership.ID, domain.MembershipTerminated)
	}))

	updated, err := svc.Update(ctx, tenantA, role.ID, role.Name, true)
	require.NoError(t, err, "terminated rows are history and must not block the flip")
	assert.True(t, updated.BranchScoped)
}

// TestMigration000013_DownUpIsSymmetric drives golang-migrate one step down and
// back up. It proves the down file is executable (a broken down migration is
// otherwise only discovered during a production rollback) and that re-applying
// the up file restores the guard.
//
// The rollback is global and briefly weakens the guard for the whole database,
// so the up step is re-applied before any assertion runs — no other test can
// observe the 000012 behaviour. (This package uses no t.Parallel(), so tests do
// not overlap in the first place.)
func TestMigration000013_DownUpIsSymmetric(t *testing.T) {
	ctx := context.Background()
	require.NotEmpty(t, sharedMigratorDSN, "TestMain must have run migrations")

	src := "file://" + filepath.Join(migrationsBase(), "identity")
	dsn := sharedMigratorDSN + "&x-migrations-table=schema_migrations_identity"

	mg, err := migrate.New(src, dsn)
	require.NoError(t, err)
	defer mg.Close()

	require.NoError(t, mg.Steps(-1), "000013 down must be executable")

	// Restore before asserting, so a failed assertion cannot leave the schema
	// rolled back for whatever runs next.
	upErr := mg.Steps(1)
	require.NoError(t, upErr, "000013 up must re-apply cleanly")

	// The guard is back: repeat the case that only 000013 rejects.
	person := newTestPerson(ctx, t)
	foreignRole := newForeignRole(ctx, t)
	err = sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		_, createErr := repo.NewMembershipRepo().Create(ctx, tx, domain.Membership{
			PersonID: person.ID,
			TenantID: tenantA,
			BranchID: &branchA,
			RoleID:   foreignRole.ID,
			Status:   domain.MembershipActive,
		})
		return createErr
	})
	require.Error(t, err)
	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	assert.Equal(t, "23514", pgErr.Code)
}

// TestMembershipRepo_CountChainWideForRole pins the status filter the refusal
// depends on: suspended counts (reversible), terminated does not (history).
func TestMembershipRepo_CountChainWideForRole(t *testing.T) {
	ctx := context.Background()
	svc := newRoleService(t)
	mr := repo.NewMembershipRepo()

	role, err := svc.CreateTenantRole(ctx, tenantA, "Sayım Rolü "+uuid.NewString(), false)
	require.NoError(t, err)

	// One chain-wide grant per status, plus one at a concrete branch which must
	// never be counted.
	statuses := []domain.MembershipStatus{
		domain.MembershipActive,
		domain.MembershipSuspended,
		domain.MembershipTerminated,
	}
	for _, status := range statuses {
		person := newTestPerson(ctx, t)
		require.NoError(t, sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
			_, createErr := mr.Create(ctx, tx, domain.Membership{
				PersonID: person.ID,
				TenantID: tenantA,
				BranchID: nil,
				RoleID:   role.ID,
				Status:   status,
			})
			return createErr
		}))
	}
	branchPerson := newTestPerson(ctx, t)
	require.NoError(t, sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		_, createErr := mr.Create(ctx, tx, domain.Membership{
			PersonID: branchPerson.ID,
			TenantID: tenantA,
			BranchID: &branchA,
			RoleID:   role.ID,
			Status:   domain.MembershipActive,
		})
		return createErr
	}))

	var count int
	require.NoError(t, sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		var countErr error
		count, countErr = mr.CountChainWideForRole(ctx, tx, tenantA, role.ID)
		return countErr
	}))
	assert.Equal(t, 2, count, "active + suspended chain-wide; terminated and branch-bound excluded")
}
