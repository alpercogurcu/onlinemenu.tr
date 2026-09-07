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
	getByIDCalls int
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

// GetUserByID implements keycloak.AdminAPI, resolving by scanning
// usersByEmail's values so it stays consistent with tests that seed a user
// directly into that map (bypassing CreateUser).
func (f *fakeAdminAPI) GetUserByID(_ context.Context, id string) (keycloak.User, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getByIDCalls++
	for _, u := range f.usersByEmail {
		if u.ID == id {
			return u, true, nil
		}
	}
	return keycloak.User{}, false, nil
}

// changeEmail simulates a realm admin editing a user's email directly in
// Keycloak — independent of anything persons knows about — which is the
// drift scenario R1 fixes.
func (f *fakeAdminAPI) changeEmail(oldEmail, newEmail string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.usersByEmail[oldEmail]
	if !ok {
		panic("fakeAdminAPI.changeEmail: no such user: " + oldEmail)
	}
	delete(f.usersByEmail, oldEmail)
	u.Email = newEmail
	u.Username = newEmail
	f.usersByEmail[newEmail] = u
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

	memberships, err := membershipSvc.ListDetails(ctx, tenantA, &result.Person.ID, nil)
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

	memberships, err := membershipSvc.ListDetails(ctx, tenantA, &first.Person.ID, nil)
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

	// The account already has working credentials — very possibly in daily use
	// at the tenant that provisioned it. Triggering execute-actions on it
	// would send an unrequested password-setup mail and, with UPDATE_PASSWORD
	// attached as a required action, force a reset that breaks that tenant's
	// login. One tenant's invite must not reach into another's account.
	assert.Equal(t, 0, admin.notifyCalls,
		"password setup must not be triggered for a reused account")
	assert.False(t, result.NotificationSent, "no mail was sent, so this must not claim one was")
	assert.Empty(t, result.NotificationError, "nothing was attempted, so nothing failed")
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
	preRetryMemberships, err := membershipSvc.ListDetails(ctx, tenantA, &seededPerson.ID, nil)
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

	memberships, err := membershipSvc.ListDetails(ctx, tenantA, &result.Person.ID, nil)
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

// TestStaffInvite_KeycloakEmailChangedSincePriorInvite_ReusesUserAndSyncsEmail
// pins R1: a realm admin edits a user's email directly in Keycloak sometime
// after that person was onboarded. Inviting them again under the STALE email
// their persons row still carries must not create a second Keycloak user —
// which used to 500 on persons_email_idx — it must reuse the existing
// account and pull persons.email to Keycloak's current value.
func TestStaffInvite_KeycloakEmailChangedSincePriorInvite_ReusesUserAndSyncsEmail(t *testing.T) {
	ctx := context.Background()
	admin := newFakeAdminAPI()
	svc := newStaffInviteService(admin)
	cashierRoleID := systemRoleID(t, "cashier")

	oldEmail := "drift-old+" + uuid.NewString() + "@example.com"
	newEmail := "drift-new+" + uuid.NewString() + "@example.com"

	first, err := svc.Invite(ctx, tenantA, service.StaffInviteRequest{
		FullName: "Drift Person", Email: oldEmail, BranchID: &branchA, RoleID: cashierRoleID,
	})
	require.NoError(t, err)
	require.True(t, first.KeycloakUserCreated)

	admin.changeEmail(oldEmail, newEmail)

	second, err := svc.Invite(ctx, tenantA, service.StaffInviteRequest{
		FullName: "Drift Person", Email: oldEmail, BranchID: &branchA, RoleID: cashierRoleID,
	})
	require.NoError(t, err, "must reuse the existing keycloak account, not fail on persons_email_idx")

	assert.False(t, second.KeycloakUserCreated, "the drifted account must be reused, not recreated")
	assert.Equal(t, 1, admin.createCalls, "only the first invite may create a keycloak user")
	assert.Equal(t, first.Person.ID, second.Person.ID)
	assert.Equal(t, newEmail, second.Person.Email, "persons.email must be pulled to keycloak's current value")

	person, err := personSvc.GetByID(ctx, second.Person.ID)
	require.NoError(t, err)
	assert.Equal(t, newEmail, person.Email, "the sync must be committed, not just reflected in the response")
}
