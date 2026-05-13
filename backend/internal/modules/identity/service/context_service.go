package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/fx"
	"go.uber.org/zap"

	pub "onlinemenu.tr/internal/modules/identity/public"
	"onlinemenu.tr/internal/modules/identity/repo"
	"onlinemenu.tr/internal/platform/auth"
	"onlinemenu.tr/internal/platform/db"
)

// ContextService issues platform-signed context tokens after validating the
// caller's membership and active role set.
type ContextService struct {
	db             *db.Pool
	membershipRepo *repo.MembershipRepo
	signer         *auth.ContextTokenSigner
	logger         *zap.Logger
}

// ContextParams groups the fx-injected dependencies for NewContextService.
type ContextParams struct {
	fx.In

	DB             *db.Pool
	MembershipRepo *repo.MembershipRepo
	Signer         *auth.ContextTokenSigner
	Logger         *zap.Logger
}

// NewContextService constructs a ContextService for fx injection.
func NewContextService(p ContextParams) *ContextService {
	return &ContextService{
		db:             p.DB,
		membershipRepo: p.MembershipRepo,
		signer:         p.Signer,
		logger:         p.Logger,
	}
}

// SelectContext issues a staff context token for the given membershipID.
//
// The method verifies that the membership belongs to the caller (keycloakSub),
// is currently active, and then collects all active role IDs at the same
// tenant+branch before signing the token.
//
// For chain-wide memberships (BranchID == nil) the token encodes uuid.Nil as
// the branch — downstream middleware treats uuid.Nil as "no specific branch".
func (s *ContextService) SelectContext(
	ctx context.Context,
	keycloakSub string,
	membershipID uuid.UUID,
	personSvc *PersonService,
	membershipSvc *MembershipService,
) (string, error) {
	person, err := personSvc.GetByKeycloakSub(ctx, keycloakSub)
	if err != nil {
		return "", fmt.Errorf("identity/service/context: select context — resolve person: %w", err)
	}

	// Resolve tenantID and branchID for the requested membershipID.
	// ListContextsForPerson is the only cross-tenant read path available;
	// GetByID requires a known tenantID which we don't have yet.
	// Only active memberships are returned by the query, so a missing match
	// means the membership is inactive, terminated, or belongs to a different person.
	var tenantID uuid.UUID
	var branchID uuid.UUID
	var found bool
	err = s.db.WithTenantReadTx(ctx, uuid.Nil, func(tx pgx.Tx) error {
		items, err := s.membershipRepo.ListContextsForPerson(ctx, tx, person.ID)
		if err != nil {
			return err
		}
		for _, item := range items {
			if item.MembershipID == membershipID {
				tenantID = item.TenantID
				if item.BranchID != nil {
					branchID = *item.BranchID
				}
				found = true
				break
			}
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("identity/service/context: select context — list contexts: %w", err)
	}
	if !found {
		return "", pub.ErrInvalid
	}

	var roleIDs []uuid.UUID
	err = s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		// branchID is uuid.Nil for chain-wide memberships; ActiveRoleIDsAt
		// includes branch_id IS NULL rows so chain-wide roles are collected correctly.
		roleIDs, err = s.membershipRepo.ActiveRoleIDsAt(ctx, tx, tenantID, person.ID, branchID)
		return err
	})
	if err != nil {
		return "", fmt.Errorf("identity/service/context: select context — active role ids: %w", err)
	}

	token, err := s.signer.IssueStaff(person.ID, tenantID, branchID, roleIDs)
	if err != nil {
		return "", fmt.Errorf("identity/service/context: select context — issue staff token: %w", err)
	}

	s.logger.Info("identity/service/context: staff context selected",
		zap.String("person_id", person.ID.String()),
		zap.String("tenant_id", tenantID.String()),
		zap.String("membership_id", membershipID.String()),
		zap.String("branch_id", branchID.String()),
	)
	return token, nil
}

// SelectCustomerContext issues a customer context token for the person identified
// by keycloakSub. Customer tokens carry no tenant or branch scope.
func (s *ContextService) SelectCustomerContext(
	ctx context.Context,
	keycloakSub string,
	personSvc *PersonService,
) (string, error) {
	person, err := personSvc.GetByKeycloakSub(ctx, keycloakSub)
	if err != nil {
		return "", fmt.Errorf("identity/service/context: select customer context — resolve person: %w", err)
	}

	token, err := s.signer.IssueCustomer(person.ID)
	if err != nil {
		return "", fmt.Errorf("identity/service/context: select customer context — issue customer token: %w", err)
	}

	s.logger.Info("identity/service/context: customer context selected",
		zap.String("person_id", person.ID.String()),
	)
	return token, nil
}
