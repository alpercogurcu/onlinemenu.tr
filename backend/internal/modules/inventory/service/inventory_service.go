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

// InventoryService manages stock levels and transaction recording.
type InventoryService struct {
	db      *db.Pool
	lvlRepo *repo.InventoryLevelRepo
	txRepo  *repo.InventoryTransactionRepo
	logger  *zap.Logger
}

// Params groups fx-injected dependencies for NewInventoryService.
type Params struct {
	fx.In

	DB      *db.Pool
	LvlRepo *repo.InventoryLevelRepo
	TxRepo  *repo.InventoryTransactionRepo
	Logger  *zap.Logger
}

// NewInventoryService constructs an InventoryService for fx injection.
func NewInventoryService(p Params) *InventoryService {
	return &InventoryService{
		db:      p.DB,
		lvlRepo: p.LvlRepo,
		txRepo:  p.TxRepo,
		logger:  p.Logger,
	}
}

// RecordAdjustmentRequest carries the parameters for a stock adjustment.
type RecordAdjustmentRequest struct {
	BranchID      uuid.UUID
	ProductID     uuid.UUID
	Type          domain.TransactionType
	QuantityDelta float64
	ReferenceID   *uuid.UUID
	ReferenceType *string
	Notes         *string
	CreatedBy     *uuid.UUID
}

// RecordAdjustment records a stock movement and updates the materialized level atomically.
// For consumption/waste (negative delta), stock is clamped to zero rather than going negative.
func (s *InventoryService) RecordAdjustment(ctx context.Context, tenantID uuid.UUID, req RecordAdjustmentRequest) (domain.InventoryTransaction, domain.InventoryLevel, error) {
	if err := validateAdjustment(req); err != nil {
		return domain.InventoryTransaction{}, domain.InventoryLevel{}, err
	}

	var tx domain.InventoryTransaction
	var lvl domain.InventoryLevel

	err := s.db.WithTenantTx(ctx, tenantID, func(pgTx pgx.Tx) error {
		t := domain.InventoryTransaction{
			TenantID:      tenantID,
			BranchID:      req.BranchID,
			ProductID:     req.ProductID,
			Type:          req.Type,
			QuantityDelta: req.QuantityDelta,
			ReferenceID:   req.ReferenceID,
			ReferenceType: req.ReferenceType,
			Notes:         req.Notes,
			CreatedBy:     req.CreatedBy,
		}
		var err error
		tx, err = s.txRepo.Create(ctx, pgTx, t)
		if err != nil {
			return fmt.Errorf("record transaction: %w", err)
		}
		lvl, err = s.lvlRepo.AdjustQuantity(ctx, pgTx, tenantID, req.BranchID, req.ProductID, req.QuantityDelta)
		if err != nil {
			return fmt.Errorf("adjust level: %w", err)
		}
		return nil
	})
	if err != nil {
		return domain.InventoryTransaction{}, domain.InventoryLevel{}, fmt.Errorf("inventory/service: record adjustment: %w", err)
	}
	return tx, lvl, nil
}

// GetLevel returns the current stock level for a product in a branch.
// Returns pub.ErrNotFound if no level record exists (no stock ever recorded).
func (s *InventoryService) GetLevel(ctx context.Context, tenantID, branchID, productID uuid.UUID) (domain.InventoryLevel, error) {
	var lvl domain.InventoryLevel
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		lvl, err = s.lvlRepo.GetByProduct(ctx, tx, branchID, productID)
		return err
	})
	if err != nil {
		return domain.InventoryLevel{}, wrapErr(err, "inventory/service: get level: %w")
	}
	return lvl, nil
}

// ListLevelsByBranch returns all stock levels for a branch.
func (s *InventoryService) ListLevelsByBranch(ctx context.Context, tenantID, branchID uuid.UUID) ([]domain.InventoryLevel, error) {
	var levels []domain.InventoryLevel
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		levels, err = s.lvlRepo.ListByBranch(ctx, tx, branchID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("inventory/service: list levels: %w", err)
	}
	return levels, nil
}

// ListTransactionsByProduct returns recent transactions for a product in a branch.
func (s *InventoryService) ListTransactionsByProduct(ctx context.Context, tenantID, branchID, productID uuid.UUID, limit int) ([]domain.InventoryTransaction, error) {
	var txs []domain.InventoryTransaction
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		txs, err = s.txRepo.ListByProduct(ctx, tx, branchID, productID, limit)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("inventory/service: list transactions by product: %w", err)
	}
	return txs, nil
}

// ListTransactionsByBranch returns recent transactions for an entire branch.
func (s *InventoryService) ListTransactionsByBranch(ctx context.Context, tenantID, branchID uuid.UUID, limit int) ([]domain.InventoryTransaction, error) {
	var txs []domain.InventoryTransaction
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		txs, err = s.txRepo.ListByBranch(ctx, tx, branchID, limit)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("inventory/service: list transactions by branch: %w", err)
	}
	return txs, nil
}

// stockReaderAdapter satisfies pub.StockReader using InventoryService.
type stockReaderAdapter struct{ svc *InventoryService }

func NewStockReader(svc *InventoryService) *stockReaderAdapter {
	return &stockReaderAdapter{svc: svc}
}

func (a *stockReaderAdapter) GetLevel(ctx context.Context, tenantID, branchID, productID uuid.UUID) (pub.StockLevel, error) {
	lvl, err := a.svc.GetLevel(ctx, tenantID, branchID, productID)
	if err != nil {
		return pub.StockLevel{}, err
	}
	return pub.StockLevel{
		ProductID: lvl.ProductID,
		BranchID:  lvl.BranchID,
		Quantity:  lvl.Quantity,
		UpdatedAt: lvl.UpdatedAt,
	}, nil
}

func validateAdjustment(req RecordAdjustmentRequest) error {
	if !req.Type.Valid() {
		return &pub.ValidationError{Msg: fmt.Sprintf("invalid transaction type %q", req.Type)}
	}
	if req.QuantityDelta == 0 {
		return &pub.ValidationError{Msg: "quantity_delta must not be zero"}
	}
	switch req.Type {
	case domain.TransactionTypeRestock:
		if req.QuantityDelta <= 0 {
			return &pub.ValidationError{Msg: "restock must have positive quantity_delta"}
		}
	case domain.TransactionTypeConsumption, domain.TransactionTypeWaste:
		if req.QuantityDelta >= 0 {
			return &pub.ValidationError{Msg: string(req.Type) + " must have negative quantity_delta"}
		}
	case domain.TransactionTypeAdjustment:
		// adjustment allows any non-zero delta (already checked above)
	}
	return nil
}

func wrapErr(err error, format string) error {
	if errors.Is(err, repo.ErrNotFound) {
		return pub.ErrNotFound
	}
	return fmt.Errorf(format, err)
}
