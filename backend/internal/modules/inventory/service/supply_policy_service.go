package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/inventory/domain"
	pub "onlinemenu.tr/internal/modules/inventory/public"
	"onlinemenu.tr/internal/modules/inventory/repo"
	"onlinemenu.tr/internal/platform/db"
)

// SupplyPolicyService manages supply_policies (ADR-DATA-007): the commercial
// procurement contract that determines how a branch sources a stock item —
// exclusively from HQ, from an approved supplier list, or freely.
type SupplyPolicyService struct {
	db        *db.Pool
	repo      *repo.SupplyPolicyRepo
	stockRepo *repo.StockItemRepo
	logger    *zap.Logger
}

// SupplyPolicyParams groups fx-injected dependencies for NewSupplyPolicyService.
type SupplyPolicyParams struct {
	fx.In

	DB        *db.Pool
	Repo      *repo.SupplyPolicyRepo
	StockRepo *repo.StockItemRepo
	Logger    *zap.Logger
}

// NewSupplyPolicyService constructs a SupplyPolicyService for fx injection.
func NewSupplyPolicyService(p SupplyPolicyParams) *SupplyPolicyService {
	return &SupplyPolicyService{db: p.DB, repo: p.Repo, stockRepo: p.StockRepo, logger: p.Logger}
}

// CreateSupplyPolicyRequest carries the parameters for creating a supply
// policy row. v1 only ever writes tenant-wide rules (ADR-DATA-007 Faz
// scoping): there is deliberately no BranchID field here — the resolver
// (domain.ResolvePolicy) already supports branch overrides so a later Faz
// can add branch-scoped writes with no schema or resolver change, only a
// new request field and route.
type CreateSupplyPolicyRequest struct {
	Scope               domain.SupplyScope
	StockItemID         *uuid.UUID
	Category            string
	Mode                domain.SupplyMode
	ApprovedSupplierIDs []uuid.UUID
	// EffectiveFrom defaults to time.Now() when zero.
	EffectiveFrom time.Time
	CreatedBy     *uuid.UUID
}

// Create generates a client-side UUIDv7 id and persists a new tenant-wide
// supply policy row. Policy changes are never an UPDATE (DATA-002
// immutability ruhu): calling Create again for the same scope/ref inserts a
// new row that supersedes the previous one from its EffectiveFrom onward.
func (s *SupplyPolicyService) Create(ctx context.Context, tenantID uuid.UUID, req CreateSupplyPolicyRequest) (domain.SupplyPolicy, error) {
	if err := validateSupplyPolicy(req); err != nil {
		return domain.SupplyPolicy{}, err
	}

	newID, err := uuid.NewV7()
	if err != nil {
		return domain.SupplyPolicy{}, fmt.Errorf("inventory/service: create supply policy: generate id: %w", err)
	}

	effectiveFrom := req.EffectiveFrom
	if effectiveFrom.IsZero() {
		effectiveFrom = time.Now()
	}

	var created domain.SupplyPolicy
	err = s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		created, err = s.repo.Create(ctx, tx, domain.SupplyPolicy{
			ID:                  newID,
			TenantID:            tenantID,
			BranchID:            nil, // v1: tenant-wide only
			Scope:               req.Scope,
			StockItemID:         req.StockItemID,
			Category:            req.Category,
			Mode:                req.Mode,
			ApprovedSupplierIDs: req.ApprovedSupplierIDs,
			EffectiveFrom:       effectiveFrom,
			CreatedBy:           req.CreatedBy,
		})
		return err
	})
	if err != nil {
		return domain.SupplyPolicy{}, fmt.Errorf("inventory/service: create supply policy: %w", err)
	}
	return created, nil
}

// List returns every supply policy row visible to the tenant (all branches,
// all scopes, full history) — used by the policy management listing
// endpoint, not by the resolver.
func (s *SupplyPolicyService) List(ctx context.Context, tenantID uuid.UUID) ([]domain.SupplyPolicy, error) {
	var policies []domain.SupplyPolicy
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		policies, err = s.repo.ListAll(ctx, tx)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("inventory/service: list supply policies: %w", err)
	}
	return policies, nil
}

// EffectivePolicyFor resolves the SupplyMode (and, when applicable, the
// approved supplier list) currently in effect for a stock item at a branch
// (ADR-DATA-007). This is the sole entry point other code (stock item
// visibility filtering here, and Wave B's purchase_receipts) should use to
// answer "how may this branch source this item" — never re-implement the
// priority order inline.
func (s *SupplyPolicyService) EffectivePolicyFor(ctx context.Context, tenantID, stockItemID, branchID uuid.UUID) (domain.SupplyMode, []uuid.UUID, error) {
	var (
		item     domain.StockItem
		policies []domain.SupplyPolicy
	)
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		item, err = s.stockRepo.GetByID(ctx, tx, stockItemID)
		if err != nil {
			return err
		}
		policies, err = s.repo.ListCandidates(ctx, tx, branchID)
		return err
	})
	if err != nil {
		return "", nil, wrapErr(err, "inventory/service: resolve effective supply policy: %w")
	}

	mode, approved := domain.ResolvePolicy(policies, item, branchID, time.Now())
	return mode, approved, nil
}

// supplyPolicyResolverAdapter satisfies pub.SupplyPolicyResolver using
// SupplyPolicyService, converting domain.SupplyMode to the cross-module
// pub.SupplyMode type.
type supplyPolicyResolverAdapter struct{ svc *SupplyPolicyService }

// NewSupplyPolicyResolver constructs the fx-provided pub.SupplyPolicyResolver
// implementation (Wave B's purchase_receipts consumes this).
func NewSupplyPolicyResolver(svc *SupplyPolicyService) *supplyPolicyResolverAdapter {
	return &supplyPolicyResolverAdapter{svc: svc}
}

func (a *supplyPolicyResolverAdapter) EffectivePolicyFor(ctx context.Context, tenantID, stockItemID, branchID uuid.UUID) (pub.SupplyMode, []uuid.UUID, error) {
	mode, approved, err := a.svc.EffectivePolicyFor(ctx, tenantID, stockItemID, branchID)
	if err != nil {
		return "", nil, err
	}
	return pub.SupplyMode(mode), approved, nil
}

func validateSupplyPolicy(req CreateSupplyPolicyRequest) error {
	if !req.Scope.Valid() {
		return &pub.ValidationError{Msg: fmt.Sprintf("invalid scope %q", req.Scope)}
	}
	if !req.Mode.Valid() {
		return &pub.ValidationError{Msg: fmt.Sprintf("invalid mode %q", req.Mode)}
	}
	switch req.Scope {
	case domain.SupplyScopeStockItem:
		if req.StockItemID == nil || *req.StockItemID == uuid.Nil {
			return &pub.ValidationError{Msg: "stock_item_id is required when scope=stock_item"}
		}
		if req.Category != "" {
			return &pub.ValidationError{Msg: "category must be empty when scope=stock_item"}
		}
	case domain.SupplyScopeCategory:
		if req.Category == "" {
			return &pub.ValidationError{Msg: "category is required when scope=category"}
		}
		if req.StockItemID != nil {
			return &pub.ValidationError{Msg: "stock_item_id must be empty when scope=category"}
		}
	case domain.SupplyScopeTenantDefault:
		if req.StockItemID != nil || req.Category != "" {
			return &pub.ValidationError{Msg: "stock_item_id and category must be empty when scope=tenant_default"}
		}
	}
	if req.Mode == domain.SupplyModeApprovedSuppliers && len(req.ApprovedSupplierIDs) == 0 {
		return &pub.ValidationError{Msg: "approved_supplier_ids is required when mode=approved_suppliers"}
	}
	return nil
}
