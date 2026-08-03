package service_test

// Integration tests for StaffInviteService (ADR-AUTH-003). These reuse the
// shared testcontainers Postgres harness declared in
// membership_service_test.go (sharedPool, tenantA, branchA, systemRoleID) —
// see that file's TestMain for the bootstrap/migration/fixture setup. Only
// Keycloak is faked (fakeAdminAPI below); no live Keycloak instance is
// required, per the task's "faked at an interface boundary" requirement.

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/identity/domain"
	pub "onlinemenu.tr/internal/modules/identity/public"
	"onlinemenu.tr/internal/modules/identity/repo"
	"onlinemenu.tr/internal/modules/identity/service"
	"onlinemenu.tr/internal/platform/keycloak"
)

// fakeAdminAPI is a hand-rolled keycloak.AdminAPI fake. It tracks call counts
// so tests can assert "never create a second Keycloak user" directly, not
// just infer it from the end state.
type fakeAdminAPI struct {
	mu sync.Mutex

	usersByEmail map[string]keycloak.User
	createCalls  int
	findCalls    int
	notifyCalls  int
	failNotify   bool
}

func newFakeAdminAPI() *fakeAdminAPI {
	return &fakeAdminAPI{usersByEmail: map[string]keycloak.User{}}
}

func (f *fakeAdminAPI) FindUserByEmail(_ context.Context, email string) (keycloak.User, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.findCalls++
	u, ok := f.usersByEmail[email]
	return u, ok, nil
}

func (f *fakeAdminAPI) CreateUser(_ context.Context, req keycloak.CreateUserRequest) (keycloak.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCalls++
	if _, ok := f.usersByEmail[req.Email]; ok {
		return keycloak.User{}, keycloak.ErrUserAlreadyExists
	}
	u := keycloak.User{ID: "kc-" + uuid.NewString(), Username: req.Email, Email: req.Email, Enabled: true}
	f.usersByEmail[req.Email] = u
	return u, nil
}

func (f *fakeAdminAPI) TriggerPasswordSetup(_ context.Context, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.notifyCalls++
	if f.failNotify {
		return errors.New("fake keycloak: SMTP not configured on this realm")
	}
	return nil
}

func newStaffInviteService(admin keycloak.AdminAPI) *service.StaffInviteService {
	return service.NewStaffInviteService(service.StaffInviteParams{
		DB:             sharedPool,
		PersonRepo:     repo.NewPersonRepo(),
		MembershipRepo: repo.NewMembershipRepo(),
		RoleRepo:       repo.NewRoleRepo(),
		Admin:          admin,
		Logger:         zap.NewNop(),
	})
}

// TestStaffInvite_FreshInvite_CreatesEverything covers the happy path: no
// prior Keycloak user, no prior person — a brand-new staff member.
func TestStaffInvite_FreshInvite_CreatesEverything(t *testing.T) {
	ctx := context.Background()
	admin := newFakeAdminAPI()
	svc := newStaffInviteService(admin)
	cashierRoleID := systemRoleID(t, "cashier")
	email := "fresh+" + uuid.NewString() + "@example.com"

	result, err := svc.Invite(ctx, tenantA, service.StaffInviteRequest{
		FullName: "Fresh Person",
		Email:    email,
		BranchID: &branchA,
		RoleID:   cashierRoleID,
	})
	require.NoError(t, err)

	assert.True(t, result.KeycloakUserCreated)
	assert.True(t, result.NotificationSent)
	assert.Empty(t, result.NotificationError)
	assert.Equal(t, email, result.Person.Email)
	assert.Equal(t, cashierRoleID, result.Membership.RoleID)
	assert.Equal(t, 1, admin.createCalls)
	assert.Equal(t, 1, admin.notifyCalls)

	// Committed, not just returned: re-read through the existing services.
	person, err := personSvc.GetByID(ctx, result.Person.ID)
	require.NoError(t, err)
	assert.Equal(t, email, person.Email)

	memberships, err := membershipSvc.List(ctx, tenantA, &result.Person.ID, nil)
	require.NoError(t, err)
	require.Len(t, memberships, 1)
	assert.Equal(t, cashierRoleID, memberships[0].RoleID)
}

// TestStaffInvite_RepeatInvite_IsIdempotent proves a caller retry of an
// already-fully-succeeded invite converges on the same person and membership
// rather than erroring or duplicating anything.
func TestStaffInvite_RepeatInvite_IsIdempotent(t *testing.T) {
	ctx := context.Background()
	admin := newFakeAdminAPI()
	svc := newStaffInviteService(admin)
	cashierRoleID := systemRoleID(t, "cashier")
	email := "repeat+" + uuid.NewString() + "@example.com"

	req := service.StaffInviteRequest{
		FullName: "Repeat Person", Email: email, BranchID: &branchA, RoleID: cashierRoleID,
	}

	first, err := svc.Invite(ctx, tenantA, req)
	require.NoError(t, err)
	require.True(t, first.KeycloakUserCreated)

	second, err := svc.Invite(ctx, tenantA, req)
	require.NoError(t, err)

	assert.False(t, second.KeycloakUserCreated, "retry must reuse the existing keycloak user")
	assert.Equal(t, first.Person.ID, second.Person.ID)
	assert.Equal(t, first.Membership.ID, second.Membership.ID)
	assert.Equal(t, 1, admin.createCalls, "exactly one keycloak user must ever be created across both attempts")

	memberships, err := membershipSvc.List(ctx, tenantA, &first.Person.ID, nil)
	require.NoError(t, err)
	require.Len(t, memberships, 1, "the retry must not create a second membership row")
}

// TestStaffInvite_EmailAlreadyInRealm_ReusesExistingUser covers the
// cross-tenant scenario the ADR names explicitly: a person already working
// at another tenant (or provisioned by some other means) shares a realm-wide
// email. The invite must reuse that Keycloak user, never create a second one.
func TestStaffInvite_EmailAlreadyInRealm_ReusesExistingUser(t *testing.T) {
	ctx := context.Background()
	admin := newFakeAdminAPI()
	email := "already-in-realm+" + uuid.NewString() + "@example.com"
	// Simulate a user that already exists in the realm before this tenant
	// ever calls Invite (e.g. onboarded by a different tenant).
	preexisting := keycloak.User{ID: "kc-preexisting-" + uuid.NewString(), Username: email, Email: email, Enabled: true}
	admin.usersByEmail[email] = preexisting

	svc := newStaffInviteService(admin)
	cashierRoleID := systemRoleID(t, "cashier")

	result, err := svc.Invite(ctx, tenantA, service.StaffInviteRequest{
		FullName: "Shared Person", Email: email, BranchID: &branchA, RoleID: cashierRoleID,
	})
	require.NoError(t, err)

	assert.False(t, result.KeycloakUserCreated, "an existing realm user must be reused, not recreated")
	assert.Equal(t, preexisting.ID, result.Person.KeycloakSub)
	assert.Equal(t, 0, admin.createCalls, "CreateUser must never be called when FindUserByEmail already found the user")
}

// TestStaffInvite_DBFailureAfterKeycloakWrite_RecoversOnRetry is the ADR's
// central partial-failure scenario: Keycloak's write succeeded and the
// person row committed, but the process failed (crashed, network error,
// whatever) before the membership was written — the two Postgres writes are
// deliberately two separate transactions (person is platform-scope, WithAllTenantsTx;
// membership is tenant-scope, WithTenantTx), so this is a real, reachable
// intermediate state, not a contrived one.
//
// The state is reproduced directly (seed the fake's Keycloak user + a
// committed person row, bypassing StaffInviteService) rather than by forcing
// an actual mid-flight DB error: this module deliberately carries no FK from
// memberships to the tenant module's branches table (identity/000011 — cross-
// module FKs are forbidden by the module isolation rule), so there is no
// syntactically-valid-but-doomed input left to use as a failure trigger.
//
// The retry (a normal Invite call) must complete by reusing both the
// Keycloak user and the person — never creating a second one of either —
// and only then writing the missing membership.
func TestStaffInvite_DBFailureAfterKeycloakWrite_RecoversOnRetry(t *testing.T) {
	ctx := context.Background()
	admin := newFakeAdminAPI()
	cashierRoleID := systemRoleID(t, "cashier")
	email := "recovers+" + uuid.NewString() + "@example.com"

	kcUser, err := admin.CreateUser(ctx, keycloak.CreateUserRequest{Email: email, FullName: "Recovering Person"})
	require.NoError(t, err)

	var seededPerson domain.Person
	err = sharedPool.WithAllTenantsTx(ctx, func(tx pgx.Tx) error {
		var err error
		seededPerson, err = repo.NewPersonRepo().FindOrCreateByKeycloakSub(ctx, tx, domain.Person{
			KeycloakSub: kcUser.ID, Email: email, FullName: "Recovering Person",
		})
		return err
	})
	require.NoError(t, err, "the prior attempt's person write must have committed")

	// Sanity check: no membership exists yet — the "crash" really did land
	// between the person write and the membership write.
	preRetryMemberships, err := membershipSvc.List(ctx, tenantA, &seededPerson.ID, nil)
	require.NoError(t, err)
	require.Empty(t, preRetryMemberships)

	svc := newStaffInviteService(admin)
	result, err := svc.Invite(ctx, tenantA, service.StaffInviteRequest{
		FullName: "Recovering Person", Email: email, BranchID: &branchA, RoleID: cashierRoleID,
	})
	require.NoError(t, err, "retry must complete using the already-provisioned keycloak user and person")

	assert.Equal(t, 1, admin.createCalls, "the keycloak user from the simulated prior attempt must not be recreated")
	assert.False(t, result.KeycloakUserCreated)
	assert.Equal(t, seededPerson.ID, result.Person.ID, "retry must reuse the person committed by the prior attempt")

	memberships, err := membershipSvc.List(ctx, tenantA, &result.Person.ID, nil)
	require.NoError(t, err)
	require.Len(t, memberships, 1)
	assert.Equal(t, branchA, *memberships[0].BranchID)
}

// TestStaffInvite_BranchScopedRoleWithoutBranch_ReturnsErrInvalidInput pins
// the d451cb2 lesson to this new endpoint: a caller-input rejection must map
// to pub.ErrInvalidInput (-> 422), not fall through to a 500. Cashier is
// branch_scoped (identity migration 000012 backfill).
func TestStaffInvite_BranchScopedRoleWithoutBranch_ReturnsErrInvalidInput(t *testing.T) {
	ctx := context.Background()
	admin := newFakeAdminAPI()
	svc := newStaffInviteService(admin)
	cashierRoleID := systemRoleID(t, "cashier")

	_, err := svc.Invite(ctx, tenantA, service.StaffInviteRequest{
		FullName: "No Branch", Email: "no-branch+" + uuid.NewString() + "@example.com",
		BranchID: nil, RoleID: cashierRoleID,
	})

	require.Error(t, err)
	assert.True(t, errors.Is(err, pub.ErrInvalidInput), "got %v", err)
	assert.Equal(t, 0, admin.createCalls, "no keycloak user should be provisioned for a rejected request")
}

// TestStaffInvite_MissingRequiredFields_ReturnsErrInvalidInput covers the
// cheap input-shape rejections (blank name, blank/malformed email, nil role)
// table-driven, matching this repo's testing convention.
func TestStaffInvite_MissingRequiredFields_ReturnsErrInvalidInput(t *testing.T) {
	ctx := context.Background()
	cashierRoleID := systemRoleID(t, "cashier")

	tests := map[string]service.StaffInviteRequest{
		"blank full name": {FullName: "   ", Email: "x@example.com", BranchID: &branchA, RoleID: cashierRoleID},
		"blank email":     {FullName: "X", Email: "   ", BranchID: &branchA, RoleID: cashierRoleID},
		"malformed email": {FullName: "X", Email: "not-an-email", BranchID: &branchA, RoleID: cashierRoleID},
		"nil role":        {FullName: "X", Email: "y@example.com", BranchID: &branchA, RoleID: uuid.Nil},
	}

	for name, req := range tests {
		t.Run(name, func(t *testing.T) {
			admin := newFakeAdminAPI()
			svc := newStaffInviteService(admin)
			_, err := svc.Invite(ctx, tenantA, req)
			require.Error(t, err)
			assert.True(t, errors.Is(err, pub.ErrInvalidInput), "got %v", err)
			assert.Equal(t, 0, admin.createCalls)
		})
	}
}

// TestStaffInvite_NotificationFailure_IsNotSwallowed proves an SMTP-less
// realm's failure to send the password-setup email is surfaced on the
// result (not raised as an error, and not silently dropped), while the
// person and membership it describes are still genuinely committed.
func TestStaffInvite_NotificationFailure_IsNotSwallowed(t *testing.T) {
	ctx := context.Background()
	admin := newFakeAdminAPI()
	admin.failNotify = true
	svc := newStaffInviteService(admin)
	cashierRoleID := systemRoleID(t, "cashier")
	email := "no-smtp+" + uuid.NewString() + "@example.com"

	result, err := svc.Invite(ctx, tenantA, service.StaffInviteRequest{
		FullName: "No SMTP", Email: email, BranchID: &branchA, RoleID: cashierRoleID,
	})
	require.NoError(t, err, "a notification failure must not fail the whole invite")

	assert.False(t, result.NotificationSent)
	assert.NotEmpty(t, result.NotificationError, "the reason must be legible, not a bare boolean")

	person, err := personSvc.GetByID(ctx, result.Person.ID)
	require.NoError(t, err, "the person must be committed even though the email failed")
	assert.Equal(t, email, person.Email)
}

// TestStaffInvite_UnknownRole_ReturnsNotFound is a light guard around the
// pre-Keycloak role lookup: a bogus role_id must fail before any Keycloak
// call is made.
func TestStaffInvite_UnknownRole_ReturnsNotFound(t *testing.T) {
	ctx := context.Background()
	admin := newFakeAdminAPI()
	svc := newStaffInviteService(admin)

	_, err := svc.Invite(ctx, tenantA, service.StaffInviteRequest{
		FullName: "X", Email: "unknown-role@example.com", BranchID: &branchA, RoleID: uuid.New(),
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, pub.ErrNotFound), "got %v", err)
	assert.Equal(t, 0, admin.findCalls, "keycloak must not be contacted before the role is validated")
}
