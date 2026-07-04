package service

import (
	"context"
	"errors"
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

// InventoryService manages warehouse-scoped stock levels and movement recording.
type InventoryService struct {
	db      *db.Pool
	lvlRepo *repo.StockLevelRepo
	mvRepo  *repo.StockMovementRepo
	logger  *zap.Logger
}

// Params groups fx-injected dependencies for NewInventoryService.
type Params struct {
	fx.In

	DB      *db.Pool
	LvlRepo *repo.StockLevelRepo
	MvRepo  *repo.StockMovementRepo
	Logger  *zap.Logger
}

// NewInventoryService constructs an InventoryService for fx injection.
func NewInventoryService(p Params) *InventoryService {
	return &InventoryService{
		db:      p.DB,
		lvlRepo: p.LvlRepo,
		mvRepo:  p.MvRepo,
		logger:  p.Logger,
	}
}

// RecordMovementRequest carries the parameters for recording a stock movement.
type RecordMovementRequest struct {
	WarehouseID   uuid.UUID
	StockItemID   uuid.UUID
	Type          domain.MovementType
	Quantity      float64 // positive magnitude, except for MovementTypeAdjust which may be signed
	Unit          string
	ReferenceID   *uuid.UUID
	ReferenceType *string
	Notes         *string
	CreatedBy     *uuid.UUID
}

// RecordMovement records a stock movement and updates the materialized level
// atomically. in/out/adjust/transfer affect on_hand; reserve/release affect
// reserved only (ADR-DATA-005 / migrations/inventory/000003).
func (s *InventoryService) RecordMovement(ctx context.Context, tenantID uuid.UUID, req RecordMovementRequest) (domain.StockMovement, domain.StockLevel, error) {
	if err := validateMovement(req); err != nil {
		return domain.StockMovement{}, domain.StockLevel{}, err
	}

	var mv domain.StockMovement
	var lvl domain.StockLevel

	err := s.db.WithTenantTx(ctx, tenantID, func(pgTx pgx.Tx) error {
		m := domain.StockMovement{
			TenantID:      tenantID,
			WarehouseID:   req.WarehouseID,
			StockItemID:   req.StockItemID,
			Type:          req.Type,
			Quantity:      req.Quantity,
			ReferenceID:   req.ReferenceID,
			ReferenceType: req.ReferenceType,
			Notes:         req.Notes,
			CreatedBy:     req.CreatedBy,
		}
		var err error
		mv, err = s.mvRepo.Create(ctx, pgTx, m)
		if err != nil {
			return fmt.Errorf("record movement: %w", err)
		}

		delta := signedDelta(req.Type, req.Quantity)
		if req.Type.AffectsOnHand() {
			lvl, err = s.lvlRepo.AdjustOnHand(ctx, pgTx, tenantID, req.WarehouseID, req.StockItemID, delta, req.Unit)
		} else {
			lvl, err = s.lvlRepo.AdjustReserved(ctx, pgTx, tenantID, req.WarehouseID, req.StockItemID, delta, req.Unit)
		}
		if err != nil {
			return fmt.Errorf("adjust level: %w", err)
		}
		return nil
	})
	if err != nil {
		return domain.StockMovement{}, domain.StockLevel{}, fmt.Errorf("inventory/service: record movement: %w", err)
	}
	return mv, lvl, nil
}

// signedDelta converts a movement's (type, magnitude) into a signed delta
// applied to the affected column. in/release/reserve increase their column;
// out decreases it; adjust is already signed; transfer decreases the source
// warehouse's on_hand (Faz 1 callers only ever use in/out for warehouse-to-
// warehouse moves via shipments — see ADR-DATA-006 — so `transfer` behaves
// like `out` here and is reserved for future direct-transfer call sites).
func signedDelta(t domain.MovementType, quantity float64) float64 {
	switch t {
	case domain.MovementTypeIn, domain.MovementTypeRelease:
		return quantity
	case domain.MovementTypeOut, domain.MovementTypeReserve, domain.MovementTypeTransfer:
		return -quantity
	case domain.MovementTypeAdjust:
		return quantity
	default:
		return quantity
	}
}

// GetLevel returns the current stock level for a stock item in a warehouse.
// Returns pub.ErrNotFound if no level record exists (no stock ever recorded).
func (s *InventoryService) GetLevel(ctx context.Context, tenantID, warehouseID, stockItemID uuid.UUID) (domain.StockLevel, error) {
	var lvl domain.StockLevel
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		lvl, err = s.lvlRepo.GetByStockItem(ctx, tx, warehouseID, stockItemID)
		return err
	})
	if err != nil {
		return domain.StockLevel{}, wrapErr(err, "inventory/service: get level: %w")
	}
	return lvl, nil
}

// ListLevelsByWarehouse returns all stock levels for a warehouse.
func (s *InventoryService) ListLevelsByWarehouse(ctx context.Context, tenantID, warehouseID uuid.UUID) ([]domain.StockLevel, error) {
	var levels []domain.StockLevel
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		levels, err = s.lvlRepo.ListByWarehouse(ctx, tx, warehouseID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("inventory/service: list levels: %w", err)
	}
	return levels, nil
}

// ListMovementsByStockItem returns recent movements for a stock item in a warehouse.
func (s *InventoryService) ListMovementsByStockItem(ctx context.Context, tenantID, warehouseID, stockItemID uuid.UUID, limit int) ([]domain.StockMovement, error) {
	var mvs []domain.StockMovement
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		mvs, err = s.mvRepo.ListByStockItem(ctx, tx, warehouseID, stockItemID, limit)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("inventory/service: list movements by stock item: %w", err)
	}
	return mvs, nil
}

// ListMovementsByWarehouse returns recent movements for an entire warehouse.
func (s *InventoryService) ListMovementsByWarehouse(ctx context.Context, tenantID, warehouseID uuid.UUID, limit int) ([]domain.StockMovement, error) {
	var mvs []domain.StockMovement
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		mvs, err = s.mvRepo.ListByWarehouse(ctx, tx, warehouseID, limit)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("inventory/service: list movements by warehouse: %w", err)
	}
	return mvs, nil
}

// stockReaderAdapter satisfies pub.StockReader using InventoryService.
type stockReaderAdapter struct{ svc *InventoryService }

func NewStockReader(svc *InventoryService) *stockReaderAdapter {
	return &stockReaderAdapter{svc: svc}
}

func (a *stockReaderAdapter) GetLevel(ctx context.Context, tenantID, warehouseID, stockItemID uuid.UUID) (pub.StockLevel, error) {
	lvl, err := a.svc.GetLevel(ctx, tenantID, warehouseID, stockItemID)
	if err != nil {
		return pub.StockLevel{}, err
	}
	return pub.StockLevel{
		StockItemID: lvl.StockItemID,
		WarehouseID: lvl.WarehouseID,
		OnHand:      lvl.OnHand,
		Available:   lvl.Available,
		UpdatedAt:   lvl.UpdatedAt,
	}, nil
}

func validateMovement(req RecordMovementRequest) error {
	if !req.Type.Valid() {
		return &pub.ValidationError{Msg: fmt.Sprintf("invalid movement type %q", req.Type)}
	}
	if req.WarehouseID == uuid.Nil {
		return &pub.ValidationError{Msg: "warehouse_id is required"}
	}
	if req.StockItemID == uuid.Nil {
		return &pub.ValidationError{Msg: "stock_item_id is required"}
	}
	if req.Quantity == 0 {
		return &pub.ValidationError{Msg: "quantity must not be zero"}
	}
	if req.Type != domain.MovementTypeAdjust && req.Quantity < 0 {
		return &pub.ValidationError{Msg: string(req.Type) + " requires a positive quantity magnitude"}
	}
	return nil
}

func wrapErr(err error, format string) error {
	if errors.Is(err, repo.ErrNotFound) {
		return pub.ErrNotFound
	}
	return fmt.Errorf(format, err)
}
