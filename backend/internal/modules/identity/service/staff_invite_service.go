package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/identity/domain"
	pub "onlinemenu.tr/internal/modules/identity/public"
	"onlinemenu.tr/internal/modules/identity/repo"
	tenantpub "onlinemenu.tr/internal/modules/tenant/public"
	"onlinemenu.tr/internal/platform/db"
	"onlinemenu.tr/internal/platform/keycloak"
)

// StaffInviteRequest is the input to StaffInviteService.Invite.
type StaffInviteRequest struct {
	FullName string
	Email    string
	BranchID *uuid.UUID
	RoleID   uuid.UUID
}

// StaffInviteResult reports the outcome of a staff invite (ADR-AUTH-003).
type StaffInviteResult struct {
	Person     domain.Person
	Membership domain.Membership

	// KeycloakUserCreated is true when a brand-new Keycloak user was created
	// for this invite, false when an existing user was found and reused —
	// either this tenant's own retry of a previously-successful invite, or a
	// person already working at another tenant (AUTH-002's single realm
	// makes email global; a retried DB-only failure looks identical to that
	// case here, which is the correct, ADR-mandated behaviour).
	KeycloakUserCreated bool

	// NotificationSent reports whether Keycloak accepted the password-setup
	// email request. False does NOT mean the invite failed: Person and
	// Membership are already committed by the time this is set. But it must
	// never be silently dropped (docs/lessons-from-b2b.md bans exactly this
	// class of swallowed failure) — the caller has to learn the account
	// exists but the person was not notified.
	NotificationSent bool

	// NotificationError carries the underlying failure reason when
	// NotificationSent is false (e.g. "realm has no SMTP configured"), so the
	// caller gets an actionable message instead of a bare boolean.
	NotificationError string
}

// StaffInviteService onboards a new staff member end-to-end: find-or-create
// the Keycloak user, then find-or-create the persons row, then grant the
// requested membership. See docs/adr/AUTH-003-keycloak-admin-api-staff-invite.md.
type StaffInviteService struct {
	db             *db.Pool
	personRepo     *repo.PersonRepo
	membershipRepo *repo.MembershipRepo
	roleRepo       *repo.RoleRepo
	admin          keycloak.AdminAPI
	tenantReader   tenantpub.TenantReader
	logger         *zap.Logger
}

// StaffInviteParams groups the fx-injected dependencies for NewStaffInviteService.
type StaffInviteParams struct {
	fx.In

	DB             *db.Pool
	PersonRepo     *repo.PersonRepo
	MembershipRepo *repo.MembershipRepo
	RoleRepo       *repo.RoleRepo
	Admin          keycloak.AdminAPI
	TenantReader   tenantpub.TenantReader
	Logger         *zap.Logger
}

// NewStaffInviteService constructs a StaffInviteService for fx injection.
func NewStaffInviteService(p StaffInviteParams) *StaffInviteService {
	return &StaffInviteService{
		db:             p.DB,
		personRepo:     p.PersonRepo,
		membershipRepo: p.MembershipRepo,
		roleRepo:       p.RoleRepo,
		admin:          p.Admin,
		tenantReader:   p.TenantReader,
		logger:         p.Logger,
	}
}

// Invite onboards a staff member for tenantID. Order is fixed by the ADR:
// Keycloak write first, then Postgres — and every step is idempotent by
// construction, so a caller retry (whether because the first attempt's DB
// write failed after Keycloak already succeeded, or because the caller
// simply doesn't know if its first request landed) converges on the same
// person + membership instead of creating duplicates or a second Keycloak
// user.
//
// This route is deliberately NOT behind platform/httpx.Idempotency
// (ADR-SEC-003 lists payment/invoice/check-close/order POSTs, not this one).
// That middleware would replay a cached response for a repeated
// Idempotency-Key, which is weaker here: two different managers racing an
// invite for the same email must both see a membership actually exist
// afterwards, not have the second one silently served the first one's cached
// body while their own request never touched the DB. The find-or-create keys
// used below (email, then keycloak_sub, then the membership quadruple) are
// already the idempotency boundary; no route-level cache adds anything.
func (s *StaffInviteService) Invite(ctx context.Context, tenantID uuid.UUID, req StaffInviteRequest) (StaffInviteResult, error) {
	fullName := strings.TrimSpace(req.FullName)
	email := strings.ToLower(strings.TrimSpace(req.Email))

	if fullName == "" {
		return StaffInviteResult{}, fmt.Errorf("%w: full_name is required", pub.ErrInvalidInput)
	}
	if email == "" || !strings.Contains(email, "@") {
		return StaffInviteResult{}, fmt.Errorf("%w: a valid email is required", pub.ErrInvalidInput)
	}
	if req.RoleID == uuid.Nil {
		return StaffInviteResult{}, fmt.Errorf("%w: role_id is required", pub.ErrInvalidInput)
	}

	// Read-only, tenant-scoped, and resolved BEFORE any Keycloak write: a bad
	// role or a branch-scoped role invited without a branch_id (ADR-SEC-005)
	// fails fast (422) without provisioning a Keycloak account nobody ends up
	// using. The memberships_branch_scope_guard trigger is the last line of
	// defence if this check is ever bypassed; this is the clean UX path
	// (mirrors MembershipService.Create).
	var role domain.Role
	if err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		role, err = s.roleRepo.GetByID(ctx, tx, tenantID, req.RoleID)
		return err
	}); err != nil {
		return StaffInviteResult{}, wrapNotFound(err, "identity/service/staff_invite: get role: %w")
	}
	if role.RequiresBranch() && req.BranchID == nil {
		return StaffInviteResult{}, fmt.Errorf("%w: role %q requires a branch_id", pub.ErrInvalidInput, role.Name)
	}

	// R2: a branch_id, if supplied, must actually exist in this tenant.
	// Validated here — before any Keycloak write — for the same reason as
	// the role check above: identity carries no FK to tenant's branches
	// table (module isolation), so a bogus id would otherwise surface much
	// later as an opaque failure instead of a clean 422.
	if err := validateBranch(ctx, s.tenantReader, tenantID, req.BranchID); err != nil {
		return StaffInviteResult{}, err
	}

	kcUser, created, err := s.findOrCreateKeycloakUser(ctx, email, fullName)
	if err != nil {
		return StaffInviteResult{}, err
	}

	// Person is platform-scope (WithAllTenantsTx): the same email may already
	// be a person at a different tenant (AUTH-002), and that row would be
	// invisible under a tenant-scoped read (persons_select only shows rows
	// with a membership in the CURRENT tenant — see identity/000008).
	// NOTE on reuse: when an existing person is found (FindOrCreateByKeycloakSub
	// returns a row that already existed), fullName/email from THIS request are
	// deliberately discarded — the existing row's values win. Overwriting them
	// would let one tenant's invite silently rewrite the identity of a person
	// who may be a member of a DIFFERENT tenant (AUTH-002's single realm makes
	// this a real case, not a hypothetical), which is a cross-tenant integrity
	// problem this module has no business creating. The caller can still see
	// this happened: the response's person.full_name/email reflect what was
	// actually kept, and KeycloakUserCreated=false is the signal that reuse (not
	// creation) occurred. This is an accepted product gap, not an oversight —
	// see the ADR's "davet kabul akışı" open item.
	var person domain.Person
	if err := s.db.WithAllTenantsTx(ctx, func(tx pgx.Tx) error {
		var err error
		person, err = s.personRepo.FindOrCreateByKeycloakSub(ctx, tx, domain.Person{
			KeycloakSub: kcUser.ID,
			Email:       email,
			FullName:    fullName,
		})
		return err
	}); err != nil {
		return StaffInviteResult{}, fmt.Errorf("identity/service/staff_invite: find or create person: %w", err)
	}

	// Membership is tenant-scope: this is the write that actually grants
	// access within tenantID.
	var membership domain.Membership
	if err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		membership, err = s.membershipRepo.FindOrCreate(ctx, tx, domain.Membership{
			PersonID: person.ID,
			TenantID: tenantID,
			BranchID: req.BranchID,
			RoleID:   req.RoleID,
			Status:   domain.MembershipActive,
		})
		if err != nil {
			return err
		}

		// R1: pull persons.email to Keycloak's current value when it has
		// drifted (a realm admin edited the account's email directly in
		// Keycloak after this person was provisioned — see
		// findOrCreateKeycloakUser's stale-email reuse branch). This must run
		// AFTER FindOrCreate above, not before: persons_update (identity
		// migration 000008) only allows a write when the target person
		// already holds a membership in the CURRENT tenant, and for a
		// brand-new invite to this tenant that only becomes true once the
		// membership just written above exists — which, inside this same
		// transaction, it now does.
		if kcUser.Email != "" && person.Email != kcUser.Email {
			updated := person
			updated.Email = kcUser.Email
			updated, err = s.personRepo.Update(ctx, tx, updated)
			if err != nil {
				return err
			}
			person = updated
		}
		return nil
	}); err != nil {
		return StaffInviteResult{}, fmt.Errorf("identity/service/staff_invite: find or create membership: %w", err)
	}

	result := StaffInviteResult{
		Person:              person,
		Membership:          membership,
		KeycloakUserCreated: created,
	}

	// Only a user THIS call created needs a password. On the reuse path the
	// person already has working credentials — under AUTH-002's single realm
	// they may be working at another tenant right now — and triggering
	// execute-actions on their account would send them an unrequested
	// "set your password" mail and, if the realm attaches UPDATE_PASSWORD as
	// a required action, force a reset that breaks their existing login.
	// One tenant's invite must not reach into an account another tenant
	// depends on; that is the same boundary the ADR draws when it forbids
	// deleting the Keycloak user to compensate a DB failure.
	//
	// Result contract: NotificationSent is true only when a mail was actually
	// sent; NotificationError is non-empty only when an attempt failed. The
	// reuse path leaves both zero — nothing was needed and nothing failed.
	if created {
		if err := s.admin.TriggerPasswordSetup(ctx, kcUser.ID); err != nil {
			// Person and membership are already committed. A failure here
			// (most commonly: the realm has no SMTP configured) must not be
			// swallowed (docs/lessons-from-b2b.md) — it is surfaced on the
			// result with its underlying reason, not raised as an error that
			// would tell the caller the whole invite failed when it did not.
			s.logger.Warn("identity/service/staff_invite: password-setup email not sent",
				zap.String("keycloak_user_id", kcUser.ID), zap.Error(err))
			result.NotificationError = err.Error()
		} else {
			result.NotificationSent = true
		}
	}

	return result, nil
}

// findOrCreateKeycloakUser implements the ADR's find-then-create-then-recover
// sequence, extended per R1: before ever creating a second Keycloak user for
// an email nothing currently matches, check whether persons already holds a
// row for this identity under a stale email — a realm admin changed that
// account's Keycloak email sometime after it was provisioned, so FindUserByEmail
// above no longer finds it under the email this invite was sent to. If the
// account behind that row's keycloak_sub still exists, it is reused instead
// of provisioning a second one, which is what used to 500 on
// persons_email_idx once the second account's row tried to insert with the
// same (still-stale) email.
//
// The returned bool is true only when a brand new Keycloak user was created
// by THIS call.
func (s *StaffInviteService) findOrCreateKeycloakUser(ctx context.Context, email, fullName string) (keycloak.User, bool, error) {
	kcUser, found, err := s.admin.FindUserByEmail(ctx, email)
	if err != nil {
		return keycloak.User{}, false, fmt.Errorf("identity/service/staff_invite: find keycloak user: %w", err)
	}
	if found {
		return kcUser, false, nil
	}

	reused, ok, err := s.reuseKeycloakUserByStaleEmail(ctx, email)
	if err != nil {
		return keycloak.User{}, false, err
	}
	if ok {
		return reused, false, nil
	}

	kcUser, err = s.admin.CreateUser(ctx, keycloak.CreateUserRequest{Email: email, FullName: fullName})
	if err == nil {
		return kcUser, true, nil
	}
	if !errors.Is(err, keycloak.ErrUserAlreadyExists) {
		return keycloak.User{}, false, fmt.Errorf("identity/service/staff_invite: create keycloak user: %w", err)
	}

	// Lost a race with a concurrent invite for the same email (this tenant's
	// own retry, or a different tenant — AUTH-002's single realm). Recover by
	// finding the user that just won; the ADR forbids ever attempting a
	// second create or deleting anything here.
	kcUser, found, err = s.admin.FindUserByEmail(ctx, email)
	if err != nil {
		return keycloak.User{}, false, fmt.Errorf("identity/service/staff_invite: re-find keycloak user after conflict: %w", err)
	}
	if !found {
		return keycloak.User{}, false, fmt.Errorf(
			"identity/service/staff_invite: keycloak reported a conflict for %q but no matching user can be found", email)
	}
	return kcUser, false, nil
}

// reuseKeycloakUserByStaleEmail implements R1's lookup: email did not resolve
// to any live Keycloak user, but a persons row might still exist for that
// same identity under this now-stale address. found is false (with a nil
// error) whenever no such reuse applies — either no persons row carries this
// email, or one does but its keycloak_sub no longer resolves to a live
// account (e.g. deleted directly in Keycloak) — and the caller falls through
// to its normal CreateUser path.
func (s *StaffInviteService) reuseKeycloakUserByStaleEmail(ctx context.Context, email string) (keycloak.User, bool, error) {
	var person domain.Person
	err := s.db.WithAllTenantsTx(ctx, func(tx pgx.Tx) error {
		var err error
		person, err = s.personRepo.GetByEmail(ctx, tx, email)
		return err
	})
	if err != nil {
		if errors.Is(err, pub.ErrNotFound) {
			return keycloak.User{}, false, nil
		}
		return keycloak.User{}, false, fmt.Errorf("identity/service/staff_invite: look up person by email: %w", err)
	}

	kcUser, found, err := s.admin.GetUserByID(ctx, person.KeycloakSub)
	if err != nil {
		return keycloak.User{}, false, fmt.Errorf("identity/service/staff_invite: get keycloak user by id: %w", err)
	}
	if !found {
		return keycloak.User{}, false, nil
	}
	return kcUser, true, nil
}
