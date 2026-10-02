package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/pos/domain"
	"onlinemenu.tr/internal/modules/pos/repo"
	"onlinemenu.tr/internal/platform/auth"
	"onlinemenu.tr/internal/platform/db"
)

// ErrInvalidWaiterCategoryLayout is returned by BranchSettingsService.Set when
// waiter_category_layout is present but not one of top/side. HTTP maps it to 422.
var ErrInvalidWaiterCategoryLayout = errors.New("pos/service/branch_settings: invalid waiter_category_layout")

// ErrInvalidOrderFlow is returned by BranchSettingsService.Set when order_flow
// is present but not one of full/simple. HTTP maps it to 422.
var ErrInvalidOrderFlow = errors.New("pos/service/branch_settings: invalid order_flow")

// ErrInvalidRounding is returned by BranchSettingsService.Set when a rounding
// field is present but out of range (a step outside 50/100/500/1000 kuruş, or
// a negative per-check ceiling). HTTP maps it to 422.
var ErrInvalidRounding = errors.New("pos/service/branch_settings: invalid rounding setting")

// maxRoundingPerCheckMinor caps the per-check rounding ceiling at ₺100. The
// column is an INT, and rounding is a kuruş-level courtesy: a ceiling above
// this is a typo, not a policy.
const maxRoundingPerCheckMinor = 10000

// BranchSettingsService owns per-branch POS preferences (pos_branch_settings).
//
// The settings row is created lazily: Get never writes, so a branch that was
// never touched has no row and behaves exactly as domain.DefaultBranchSettings
// says — which is how existing branches keep today's behaviour (top/full) with
// no backfill migration. The order/check write paths do NOT come through this
// service: they read the flow via BranchSettingsRepo.OrderFlowByBranch inside
// their own write transaction, so the verdict and the write it gates commit or
// roll back together.
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

// Get returns the branch's settings, falling back to the defaults when no row
// exists. It never creates the row: a GET must stay side-effect free, and the
// defaults are the row's exact behaviour anyway. Branch scope is enforced per
// SEC-005 (requireBranch): the waiter/cashier/kitchen screens read their own
// branch, the chain manager (OPA tenant scope) reads any.
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
		return domain.BranchSettings{}, fmt.Errorf("pos/service/branch_settings: get: %w", err)
	}
	return settings, nil
}

// SetBranchSettingsRequest is the input to BranchSettingsService.Set. Nil
// pointer fields mean "not supplied — keep the current value (or the default
// when this PUT creates the row)".
type SetBranchSettingsRequest struct {
	BranchID             uuid.UUID
	WaiterCategoryLayout *domain.WaiterCategoryLayout
	OrderFlow            *domain.OrderFlow
	RoundingCashEnabled  *bool
	RoundingCardEnabled  *bool
	RoundingStepMinor    *int64
	RoundingMaxPerCheck  *int64
	UpdatedBy            uuid.UUID
}

// Set upserts the branch's settings. The branch guard runs before value
// validation so a branch-forbidden caller gets 403 and learns nothing about
// which values the endpoint would have taken (same ordering rationale as
// requireBranch's doc comment).
func (s *BranchSettingsService) Set(ctx context.Context, tenantID uuid.UUID, principal auth.Principal, req SetBranchSettingsRequest) (domain.BranchSettings, error) {
	if err := requireBranch(ctx, principal, req.BranchID); err != nil {
		return domain.BranchSettings{}, err
	}
	if req.WaiterCategoryLayout != nil && !req.WaiterCategoryLayout.Valid() {
		return domain.BranchSettings{}, fmt.Errorf("%q: %w", *req.WaiterCategoryLayout, ErrInvalidWaiterCategoryLayout)
	}
	if req.OrderFlow != nil && !req.OrderFlow.Valid() {
		return domain.BranchSettings{}, fmt.Errorf("%q: %w", *req.OrderFlow, ErrInvalidOrderFlow)
	}
	if req.RoundingStepMinor != nil && !domain.ValidRoundingStep(*req.RoundingStepMinor) {
		return domain.BranchSettings{}, fmt.Errorf("rounding_step_minor %d: %w", *req.RoundingStepMinor, ErrInvalidRounding)
	}
	if req.RoundingMaxPerCheck != nil && (*req.RoundingMaxPerCheck < 0 || *req.RoundingMaxPerCheck > maxRoundingPerCheckMinor) {
		return domain.BranchSettings{}, fmt.Errorf("rounding_max_per_check_minor %d: %w", *req.RoundingMaxPerCheck, ErrInvalidRounding)
	}

	var saved domain.BranchSettings
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		saved, err = s.settings.Upsert(ctx, tx, repo.BranchSettingsPatch{
			TenantID:             tenantID,
			BranchID:             req.BranchID,
			WaiterCategoryLayout: req.WaiterCategoryLayout,
			OrderFlow:            req.OrderFlow,
			RoundingCashEnabled:  req.RoundingCashEnabled,
			RoundingCardEnabled:  req.RoundingCardEnabled,
			RoundingStepMinor:    req.RoundingStepMinor,
			RoundingMaxPerCheck:  req.RoundingMaxPerCheck,
			UpdatedBy:            req.UpdatedBy,
		})
		return err
	})
	if err != nil {
		return domain.BranchSettings{}, fmt.Errorf("pos/service/branch_settings: set: %w", err)
	}
	return saved, nil
}
