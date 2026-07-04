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
	"onlinemenu.tr/internal/platform/auth"
	"onlinemenu.tr/internal/platform/db"
)

// WarehouseService manages depo/imalat locations.
type WarehouseService struct {
	db     *db.Pool
	repo   *repo.WarehouseRepo
	logger *zap.Logger
}

// WarehouseParams groups fx-injected dependencies for NewWarehouseService.
type WarehouseParams struct {
	fx.In

	DB     *db.Pool
	Repo   *repo.WarehouseRepo
	Logger *zap.Logger
}

// NewWarehouseService constructs a WarehouseService for fx injection.
func NewWarehouseService(p WarehouseParams) *WarehouseService {
	return &WarehouseService{db: p.DB, repo: p.Repo, logger: p.Logger}
}

// CreateWarehouseRequest carries the parameters for creating a warehouse.
type CreateWarehouseRequest struct {
	BranchID      uuid.UUID
	Name          string
	WarehouseType domain.WarehouseType
}

// Create persists a new warehouse.
func (s *WarehouseService) Create(ctx context.Context, tenantID uuid.UUID, req CreateWarehouseRequest) (domain.Warehouse, error) {
	if err := validateWarehouse(req); err != nil {
		return domain.Warehouse{}, err
	}

	var created domain.Warehouse
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		created, err = s.repo.Create(ctx, tx, domain.Warehouse{
			TenantID:      tenantID,
			BranchID:      req.BranchID,
			Name:          req.Name,
			WarehouseType: req.WarehouseType,
			IsActive:      true,
		})
		return err
	})
	if err != nil {
		return domain.Warehouse{}, fmt.Errorf("inventory/service: create warehouse: %w", err)
	}
	return created, nil
}

// Get returns a warehouse by id.
func (s *WarehouseService) Get(ctx context.Context, tenantID, id uuid.UUID) (domain.Warehouse, error) {
	var w domain.Warehouse
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		w, err = s.repo.GetByID(ctx, tx, id)
		return err
	})
	if err != nil {
		return domain.Warehouse{}, wrapErr(err, "inventory/service: get warehouse: %w")
	}
	return w, nil
}

// List returns warehouses, optionally filtered by branch.
func (s *WarehouseService) List(ctx context.Context, tenantID, branchID uuid.UUID) ([]domain.Warehouse, error) {
	var out []domain.Warehouse
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		out, err = s.repo.List(ctx, tx, branchID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("inventory/service: list warehouses: %w", err)
	}
	return out, nil
}

// UpdateWarehouseRequest carries the parameters for updating a warehouse.
type UpdateWarehouseRequest struct {
	ID            uuid.UUID
	BranchID      uuid.UUID
	Name          string
	WarehouseType domain.WarehouseType
	IsActive      bool
}

// Update persists mutable warehouse field changes. The acting principal must
// belong to the warehouse's PERSISTED branch (not the branch_id in the
// request body — a request could otherwise name a branch the caller
// legitimately belongs to while targeting a warehouse that actually lives in
// a different branch; ADR-AUTH-001 layer 3 / security sprint).
func (s *WarehouseService) Update(ctx context.Context, tenantID uuid.UUID, principal auth.Principal, req UpdateWarehouseRequest) (domain.Warehouse, error) {
	if err := validateWarehouse(CreateWarehouseRequest{BranchID: req.BranchID, Name: req.Name, WarehouseType: req.WarehouseType}); err != nil {
		return domain.Warehouse{}, err
	}

	var updated domain.Warehouse
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		existing, err := s.repo.GetByID(ctx, tx, req.ID)
		if err != nil {
			return err
		}
		if err := requireBranch(ctx, principal, existing.BranchID); err != nil {
			return err
		}
		updated, err = s.repo.Update(ctx, tx, domain.Warehouse{
			ID:            req.ID,
			BranchID:      req.BranchID,
			Name:          req.Name,
			WarehouseType: req.WarehouseType,
			IsActive:      req.IsActive,
		})
		return err
	})
	if err != nil {
		return domain.Warehouse{}, wrapErr(err, "inventory/service: update warehouse: %w")
	}
	return updated, nil
}

// Delete soft-deletes (deactivates) a warehouse. The acting principal must
// belong to the warehouse's persisted branch (see Update).
func (s *WarehouseService) Delete(ctx context.Context, tenantID uuid.UUID, principal auth.Principal, id uuid.UUID) error {
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		existing, err := s.repo.GetByID(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := requireBranch(ctx, principal, existing.BranchID); err != nil {
			return err
		}
		return s.repo.Delete(ctx, tx, id)
	})
	if err != nil {
		return wrapErr(err, "inventory/service: delete warehouse: %w")
	}
	return nil
}

func validateWarehouse(req CreateWarehouseRequest) error {
	if req.BranchID == uuid.Nil {
		return &pub.ValidationError{Msg: "branch_id is required"}
	}
	if req.Name == "" {
		return &pub.ValidationError{Msg: "name is required"}
	}
	if !req.WarehouseType.Valid() {
		return &pub.ValidationError{Msg: fmt.Sprintf("invalid warehouse_type %q", req.WarehouseType)}
	}
	return nil
}
