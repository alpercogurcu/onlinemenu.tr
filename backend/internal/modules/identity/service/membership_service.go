package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/identity/domain"
	pub "onlinemenu.tr/internal/modules/identity/public"
	"onlinemenu.tr/internal/modules/identity/repo"
	"onlinemenu.tr/internal/platform/db"
)

// MembershipService manages person-to-role bindings within a tenant.
type MembershipService struct {
	db             *db.Pool
	membershipRepo *repo.MembershipRepo
	roleRepo       *repo.RoleRepo
	logger         *zap.Logger
}

// MembershipParams groups the fx-injected dependencies for NewMembershipService.
type MembershipParams struct {
	fx.In

	DB             *db.Pool
	MembershipRepo *repo.MembershipRepo
	RoleRepo       *repo.RoleRepo
	Logger         *zap.Logger
}

// NewMembershipService constructs a MembershipService for fx injection.
func NewMembershipService(p MembershipParams) *MembershipService {
	return &MembershipService{
		db:             p.DB,
		membershipRepo: p.MembershipRepo,
		roleRepo:       p.RoleRepo,
		logger:         p.Logger,
	}
}

// ListDetails returns memberships for the given tenant, optionally filtered by
// personID and/or branchID, with the person and role display fields joined in
// (see domain.MembershipDetail) — the shape the admin user list renders.
func (s *MembershipService) ListDetails(
	ctx context.Context,
	tenantID uuid.UUID,
	personID *uuid.UUID,
	branchID *uuid.UUID,
) ([]domain.MembershipDetail, error) {
	var details []domain.MembershipDetail
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		details, err = s.membershipRepo.ListDetailsForTenant(ctx, tx, tenantID, personID, branchID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("identity/service/membership: list details: %w", err)
	}
	return details, nil
}

// ListContexts returns the lightweight context summaries for all active memberships
// belonging to the person identified by keycloakSub.
// This is a platform-scope read: the memberships-by-person view spans tenants.
//
// A Keycloak subject that has authenticated but has no corresponding persons row
// (no staff onboarding has happened for them yet) is NOT an error: it returns an
// empty context list. A persons row grants no access on its own — memberships do
// — so "no person yet" and "person with zero memberships" are behaviourally the
// same thing from this endpoint's point of view, and both must yield 200 with an
// empty list rather than surfacing pub.ErrNotFound as a caller-facing error.
func (s *MembershipService) ListContexts(ctx context.Context, keycloakSub string, personSvc *PersonService) ([]domain.ContextItem, error) {
	person, err := personSvc.GetByKeycloakSub(ctx, keycloakSub)
	if err != nil {
		if errors.Is(err, pub.ErrNotFound) {
			return []domain.ContextItem{}, nil
		}
		return nil, fmt.Errorf("identity/service/membership: list contexts — resolve person: %w", err)
	}

	var items []domain.ContextItem
	// WithAllTenantsReadTx scopes the query to the cross-tenant view of
	// memberships (app.tenant_scope = 'all_tenants'), the named platform-scope
	// path — see db.WithAllTenantsReadTx and docs/lessons-from-b2b.md item 6.
	err = s.db.WithAllTenantsReadTx(ctx, func(tx pgx.Tx) error {
		var err error
		items, err = s.membershipRepo.ListContextsForPerson(ctx, tx, person.ID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("identity/service/membership: list contexts: %w", err)
	}
	return items, nil
}

// ListContextsByPerson returns context summaries for the person identified by personID
// without an extra keycloak sub resolution round-trip. Used when the caller already has
// a context token (CTX) and personID is known.
func (s *MembershipService) ListContextsByPerson(ctx context.Context, personID uuid.UUID) ([]domain.ContextItem, error) {
	var items []domain.ContextItem
	err := s.db.WithAllTenantsReadTx(ctx, func(tx pgx.Tx) error {
		var err error
		items, err = s.membershipRepo.ListContextsForPerson(ctx, tx, personID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("identity/service/membership: list contexts by person: %w", err)
	}
	return items, nil
}

// Create binds a person to a role within a tenant (and optionally a branch).
// Branch-scoped roles require a non-nil branchID. System roles (nil TenantID)
// are always valid targets; no additional tenant-ownership check is needed.
func (s *MembershipService) Create(ctx context.Context, tenantID, personID uuid.UUID, branchID *uuid.UUID, roleID uuid.UUID) (domain.Membership, error) {
	var role domain.Role
	if err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		role, err = s.roleRepo.GetByID(ctx, tx, tenantID, roleID)
		return err
	}); err != nil {
		return domain.Membership{}, wrapNotFound(err, "identity/service/membership: create — get role: %w")
	}

	// Early, clean 400. The memberships_branch_scope_guard trigger (identity
	// migration 000012) is the last line of defence; this is the UX path.
	if role.RequiresBranch() && branchID == nil {
		return domain.Membership{}, pub.ErrInvalid
	}

	m := domain.Membership{
		PersonID: personID,
		TenantID: tenantID,
		BranchID: branchID,
		RoleID:   roleID,
		Status:   domain.MembershipActive,
	}

	var created domain.Membership
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		created, err = s.membershipRepo.Create(ctx, tx, m)
		return err
	})
	if err != nil {
		return domain.Membership{}, fmt.Errorf("identity/service/membership: create: %w", err)
	}
	return created, nil
}

// UpdateStatus transitions a membership to a new lifecycle status.
func (s *MembershipService) UpdateStatus(ctx context.Context, tenantID, membershipID uuid.UUID, status domain.MembershipStatus) error {
	if !validMembershipStatus(status) {
		return pub.ErrInvalid
	}
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		return s.membershipRepo.UpdateStatus(ctx, tx, tenantID, membershipID, status)
	})
	if err != nil {
		return wrapNotFound(err, "identity/service/membership: update status: %w")
	}
	return nil
}

// ActiveRoleIDsAt returns all active role IDs a person holds at the given branch
// within a tenant. Chain-wide memberships (branch_id IS NULL) are included.
// This satisfies pub.MembershipResolver and is used when building context tokens.
func (s *MembershipService) ActiveRoleIDsAt(ctx context.Context, tenantID, personID, branchID uuid.UUID) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		ids, err = s.membershipRepo.ActiveRoleIDsAt(ctx, tx, tenantID, personID, branchID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("identity/service/membership: active role ids at: %w", err)
	}
	return ids, nil
}

func validMembershipStatus(s domain.MembershipStatus) bool {
	switch s {
	case domain.MembershipActive, domain.MembershipSuspended, domain.MembershipTerminated:
		return true
	}
	return false
}
