package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/storefront/domain"
	"onlinemenu.tr/internal/modules/storefront/repo"
	"onlinemenu.tr/internal/platform/auth"
	"onlinemenu.tr/internal/platform/db"
)

// OrderingGate answers "may guests place orders at this branch right now".
//
// It is the narrow interface the GUEST surface depends on (OrderService's
// placement check, the public menu's ordering_enabled flag): neither caller
// may reach the admin read/write methods through it, and tests stub one
// method instead of standing up a database.
type OrderingGate interface {
	OrderingEnabled(ctx context.Context, tenantID, branchID uuid.UUID) (bool, error)
}

// BranchSettingsService owns per-branch storefront settings.
//
// The settings row is created lazily: Get never writes, so a branch that was
// never touched has no row and behaves exactly as domain.DefaultBranchSettings
// says — which is how existing branches keep ordering enabled with no
// backfill migration.
type BranchSettingsService struct {
	db       *db.Pool
	settings *repo.BranchSettingsRepo
	logger   *zap.Logger
}

// BranchSettingsParams groups fx-injected dependencies.
type BranchSettingsParams struct {
	fx.In

	DB       *db.Pool
	Settings *repo.BranchSettingsRepo
	Logger   *zap.Logger
}

func NewBranchSettingsService(p BranchSettingsParams) *BranchSettingsService {
	return &BranchSettingsService{db: p.DB, settings: p.Settings, logger: p.Logger}
}

// Get returns the branch's settings for the admin surface, falling back to
// the defaults when no row exists. It never creates the row: a GET must stay
// side-effect free, and the defaults are the row's exact behaviour anyway.
func (s *BranchSettingsService) Get(ctx context.Context, tenantID uuid.UUID, principal auth.Principal, branchID uuid.UUID) (domain.BranchSettings, error) {
	if err := requireBranch(ctx, principal, branchID); err != nil {
		return domain.BranchSettings{}, err
	}

	var settings domain.BranchSettings
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		settings, err = s.settings.GetByBranch(ctx, tx, branchID)
		return err
	})
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return domain.DefaultBranchSettings(tenantID, branchID), nil
		}
		return domain.BranchSettings{}, fmt.Errorf("storefront/service/branch_settings: get: %w", err)
	}
	return settings, nil
}

// SetRequest is the input to BranchSettingsService.Set.
type SetRequest struct {
	BranchID        uuid.UUID
	OrderingEnabled bool
	UpdatedBy       uuid.UUID
}

// Set upserts the branch's settings. Branch scope is enforced the same way
// the QR endpoints enforce it (ADR-AUTH-001 layer 3 / SEC-005): a
// branch-scoped principal may only flip its own branch.
func (s *BranchSettingsService) Set(ctx context.Context, tenantID uuid.UUID, principal auth.Principal, req SetRequest) (domain.BranchSettings, error) {
	if err := requireBranch(ctx, principal, req.BranchID); err != nil {
		return domain.BranchSettings{}, err
	}

	var saved domain.BranchSettings
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		saved, err = s.settings.Upsert(ctx, tx, domain.BranchSettings{
			TenantID:        tenantID,
			BranchID:        req.BranchID,
			OrderingEnabled: req.OrderingEnabled,
			UpdatedBy:       req.UpdatedBy,
		})
		return err
	})
	if err != nil {
		return domain.BranchSettings{}, fmt.Errorf("storefront/service/branch_settings: set: %w", err)
	}
	return saved, nil
}

// OrderingEnabled is the guest-surface read (OrderingGate). Tenant and branch
// come from the signed guest session, never from the request, so there is no
// principal and no branch guard here — the session already pins both.
func (s *BranchSettingsService) OrderingEnabled(ctx context.Context, tenantID, branchID uuid.UUID) (bool, error) {
	var settings domain.BranchSettings
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		settings, err = s.settings.GetByBranch(ctx, tx, branchID)
		return err
	})
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return domain.DefaultBranchSettings(tenantID, branchID).OrderingEnabled, nil
		}
		return false, fmt.Errorf("storefront/service/branch_settings: ordering enabled: %w", err)
	}
	return settings.OrderingEnabled, nil
}
