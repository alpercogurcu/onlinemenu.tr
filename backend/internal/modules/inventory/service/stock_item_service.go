package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/inventory/domain"
	pub "onlinemenu.tr/internal/modules/inventory/public"
	"onlinemenu.tr/internal/modules/inventory/repo"
	"onlinemenu.tr/internal/platform/db"
)

// StockItemService manages the canonical stock item catalog (raw materials,
// packaging, intermediates, finished goods). See ADR-DATA-005.
type StockItemService struct {
	db     *db.Pool
	repo   *repo.StockItemRepo
	logger *zap.Logger
}

// StockItemParams groups fx-injected dependencies for NewStockItemService.
type StockItemParams struct {
	fx.In

	DB     *db.Pool
	Repo   *repo.StockItemRepo
	Logger *zap.Logger
}

// NewStockItemService constructs a StockItemService for fx injection.
func NewStockItemService(p StockItemParams) *StockItemService {
	return &StockItemService{db: p.DB, repo: p.Repo, logger: p.Logger}
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

// List returns stock items, optionally filtered by kind.
func (s *StockItemService) List(ctx context.Context, tenantID uuid.UUID, kind domain.StockItemKind) ([]domain.StockItem, error) {
	var items []domain.StockItem
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		items, err = s.repo.List(ctx, tx, kind)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("inventory/service: list stock items: %w", err)
	}
	return items, nil
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
