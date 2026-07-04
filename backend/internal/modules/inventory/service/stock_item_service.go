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
	"onlinemenu.tr/internal/platform/auth"
	"onlinemenu.tr/internal/platform/db"
)

// StockItemService manages the canonical stock item catalog (raw materials,
// packaging, intermediates, finished goods). See ADR-DATA-005.
type StockItemService struct {
	db               *db.Pool
	repo             *repo.StockItemRepo
	supplyPolicyRepo *repo.SupplyPolicyRepo
	logger           *zap.Logger
}

// StockItemParams groups fx-injected dependencies for NewStockItemService.
type StockItemParams struct {
	fx.In

	DB               *db.Pool
	Repo             *repo.StockItemRepo
	SupplyPolicyRepo *repo.SupplyPolicyRepo
	Logger           *zap.Logger
}

// NewStockItemService constructs a StockItemService for fx injection.
func NewStockItemService(p StockItemParams) *StockItemService {
	return &StockItemService{db: p.DB, repo: p.Repo, supplyPolicyRepo: p.SupplyPolicyRepo, logger: p.Logger}
}

// StockItemView pairs a stock item with its ADR-DATA-007 resolved supply
// mode and whether the acting principal's branch view of it must be
// restricted to the BTO-catalog-only projection (Restricted == true).
// Restricted is always false for a tenant-wide-scoped principal (OPA
// scope=="tenant", e.g. manager): visibility filtering is a branch-facing
// concern only (ADR-DATA-007 İlke, DATA-005 İlke 4 revizyonu).
//
// The HTTP layer, not this service, is responsible for actually omitting
// cost/supplier JSON fields for a restricted item — Restricted is a signal,
// never itself the enforcement (docs/lessons-from-b2b.md: field absence must
// happen at the DTO boundary).
type StockItemView struct {
	Item       domain.StockItem
	Mode       domain.SupplyMode
	Restricted bool
}

// CreateStockItemRequest carries the parameters for creating a stock item.
type CreateStockItemRequest struct {
	SKU           string
	Name          string
	Kind          domain.StockItemKind
	CanonicalUnit string
	Category      string
}

// Create generates a client-side UUIDv7 id and persists a new stock item.
func (s *StockItemService) Create(ctx context.Context, tenantID uuid.UUID, req CreateStockItemRequest) (domain.StockItem, error) {
	if err := validateStockItem(req); err != nil {
		return domain.StockItem{}, err
	}

	newID, err := uuid.NewV7()
	if err != nil {
		return domain.StockItem{}, fmt.Errorf("inventory/service: create stock item: generate id: %w", err)
	}

	var created domain.StockItem
	err = s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		created, err = s.repo.Create(ctx, tx, domain.StockItem{
			ID:            newID,
			TenantID:      tenantID,
			SKU:           req.SKU,
			Name:          req.Name,
			Kind:          req.Kind,
			CanonicalUnit: req.CanonicalUnit,
			Category:      req.Category,
			IsActive:      true,
		})
		return err
	})
	if err != nil {
		return domain.StockItem{}, fmt.Errorf("inventory/service: create stock item: %w", err)
	}
	return created, nil
}

// Get returns a stock item by id.
func (s *StockItemService) Get(ctx context.Context, tenantID, id uuid.UUID) (domain.StockItem, error) {
	var item domain.StockItem
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		item, err = s.repo.GetByID(ctx, tx, id)
		return err
	})
	if err != nil {
		return domain.StockItem{}, wrapErr(err, "inventory/service: get stock item: %w")
	}
	return item, nil
}

// List returns stock items, optionally filtered by kind, together with each
// item's ADR-DATA-007 resolved supply policy visibility for the acting
// principal (ADR-AUTH-001 layer 3 / DTO projection is layer 4, applied by
// the HTTP layer using StockItemView.Restricted).
//
// Visibility is keyed on the OPA-derived scope (auth.ScopeFromContext), not
// on the principal's role: a tenant-wide-scoped principal (scope=="tenant",
// e.g. manager) sees every item unrestricted. Any other scope resolves each
// item's policy against principal.BranchID; exclusive_hq items are marked
// Restricted so the HTTP layer renders only the BTO catalog fields for them.
func (s *StockItemService) List(ctx context.Context, tenantID uuid.UUID, kind domain.StockItemKind, principal auth.Principal) ([]StockItemView, error) {
	var (
		items    []domain.StockItem
		policies []domain.SupplyPolicy
	)
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		items, err = s.repo.List(ctx, tx, kind)
		if err != nil {
			return err
		}
		policies, err = s.supplyPolicyRepo.ListCandidates(ctx, tx, principal.BranchID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("inventory/service: list stock items: %w", err)
	}

	tenantWide := false
	if scope, ok := auth.ScopeFromContext(ctx); ok && scope == "tenant" {
		tenantWide = true
	}

	now := time.Now()
	out := make([]StockItemView, len(items))
	for i, item := range items {
		mode, _ := domain.ResolvePolicy(policies, item, principal.BranchID, now)
		out[i] = StockItemView{
			Item:       item,
			Mode:       mode,
			Restricted: !tenantWide && mode == domain.SupplyModeExclusiveHQ,
		}
	}
	return out, nil
}

// UpdateStockItemRequest carries the parameters for updating a stock item.
type UpdateStockItemRequest struct {
	ID            uuid.UUID
	SKU           string
	Name          string
	Kind          domain.StockItemKind
	CanonicalUnit string
	Category      string
	IsActive      bool
}

// Update persists mutable stock item field changes.
func (s *StockItemService) Update(ctx context.Context, tenantID uuid.UUID, req UpdateStockItemRequest) (domain.StockItem, error) {
	if err := validateStockItem(CreateStockItemRequest{
		SKU: req.SKU, Name: req.Name, Kind: req.Kind, CanonicalUnit: req.CanonicalUnit, Category: req.Category,
	}); err != nil {
		return domain.StockItem{}, err
	}

	var updated domain.StockItem
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		updated, err = s.repo.Update(ctx, tx, domain.StockItem{
			ID:            req.ID,
			SKU:           req.SKU,
			Name:          req.Name,
			Kind:          req.Kind,
			CanonicalUnit: req.CanonicalUnit,
			Category:      req.Category,
			IsActive:      req.IsActive,
		})
		return err
	})
	if err != nil {
		return domain.StockItem{}, wrapErr(err, "inventory/service: update stock item: %w")
	}
	return updated, nil
}

// Delete soft-deletes (deactivates) a stock item.
func (s *StockItemService) Delete(ctx context.Context, tenantID, id uuid.UUID) error {
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		return s.repo.Delete(ctx, tx, id)
	})
	if err != nil {
		return wrapErr(err, "inventory/service: delete stock item: %w")
	}
	return nil
}

func validateStockItem(req CreateStockItemRequest) error {
	if req.SKU == "" {
		return &pub.ValidationError{Msg: "sku is required"}
	}
	if req.Name == "" {
		return &pub.ValidationError{Msg: "name is required"}
	}
	if !req.Kind.Valid() {
		return &pub.ValidationError{Msg: fmt.Sprintf("invalid kind %q", req.Kind)}
	}
	if req.CanonicalUnit == "" {
		return &pub.ValidationError{Msg: "canonical_unit is required"}
	}
	return nil
}
