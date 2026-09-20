package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/catalog/domain"
	pub "onlinemenu.tr/internal/modules/catalog/public"
	"onlinemenu.tr/internal/modules/catalog/repo"
	"onlinemenu.tr/internal/platform/db"
)

// branchOverrideEventType is the outbox event_type for every override change.
// It carries no module prefix: the dispatcher builds the NATS subject as
// "<module>.<eventType>.v1" (catalog.branch_override.changed.v1).
const branchOverrideEventType = "branch_override.changed"

// BranchOverrideService manages how one branch deviates from the tenant-wide
// catalog (ADR-DATA-009).
//
// It owns the WRITE side only. Reading the effective price is not here: that
// lives in the pricing SQL (StorefrontMenuService / storefrontMenuItemsCTE)
// so browsing, guest checkout and counter sale all resolve it identically.
type BranchOverrideService struct {
	db           *db.Pool
	overrideRepo *repo.BranchOverrideRepo
	productRepo  *repo.ProductRepo
	logger       *zap.Logger
}

// BranchOverrideParams groups fx-injected dependencies.
type BranchOverrideParams struct {
	fx.In

	DB           *db.Pool
	OverrideRepo *repo.BranchOverrideRepo
	ProductRepo  *repo.ProductRepo
	Logger       *zap.Logger
}

func NewBranchOverrideService(p BranchOverrideParams) *BranchOverrideService {
	return &BranchOverrideService{
		db:           p.DB,
		overrideRepo: p.OverrideRepo,
		productRepo:  p.ProductRepo,
		logger:       p.Logger,
	}
}

// ListByBranch returns every override configured for a branch. A branch with
// no deviations returns an empty list, not an error: "this branch sells the
// tenant catalog unchanged" is a valid, common answer.
func (s *BranchOverrideService) ListByBranch(ctx context.Context, tenantID, branchID uuid.UUID) ([]domain.BranchProductOverride, error) {
	var out []domain.BranchProductOverride
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		out, err = s.overrideRepo.ListByBranch(ctx, tx, branchID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("catalog/service/branch_override: list by branch: %w", err)
	}
	return out, nil
}

// Upsert writes a branch's deviation for one product.
//
// The product is read first, inside the same transaction: the FK would catch
// a non-existent id with a 500-shaped constraint error, and RLS would make
// another tenant's product look non-existent, so resolving it here turns both
// into the 404 the admin screen can act on.
func (s *BranchOverrideService) Upsert(ctx context.Context, tenantID uuid.UUID, o domain.BranchProductOverride) (domain.BranchProductOverride, error) {
	if o.BranchID == uuid.Nil || o.ProductID == uuid.Nil {
		return domain.BranchProductOverride{}, &pub.ValidationError{Msg: "branch_id and product_id are required"}
	}
	// A negative price is refused before the CHECK constraint sees it so the
	// caller gets 422 with a sentence, not a 500 with a constraint name.
	if o.PriceAmount != nil && *o.PriceAmount < 0 {
		return domain.BranchProductOverride{}, &pub.ValidationError{Msg: "price_amount must be >= 0"}
	}
	o.TenantID = tenantID

	var saved domain.BranchProductOverride
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		if _, err := s.productRepo.GetByID(ctx, tx, o.ProductID); err != nil {
			return err
		}
		var err error
		saved, err = s.overrideRepo.Upsert(ctx, tx, o)
		if err != nil {
			return err
		}
		return s.publishChange(ctx, tx, saved, false)
	})
	if err != nil {
		return domain.BranchProductOverride{}, wrapErr(err, "catalog/service/branch_override: upsert: %w")
	}
	return saved, nil
}

// Delete returns the product to the tenant default in this branch.
//
// The event it emits carries deleted=true rather than being omitted: a
// consumer that caches effective prices must learn that a branch price went
// away just as surely as it must learn that one appeared.
func (s *BranchOverrideService) Delete(ctx context.Context, tenantID, branchID, productID uuid.UUID) error {
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		if err := s.overrideRepo.Delete(ctx, tx, branchID, productID); err != nil {
			return err
		}
		return s.publishChange(ctx, tx, domain.BranchProductOverride{
			TenantID: tenantID, BranchID: branchID, ProductID: productID, IsAvailable: true,
		}, true)
	})
	if err != nil {
		return wrapErr(err, "catalog/service/branch_override: delete: %w")
	}
	return nil
}

// publishChange appends the immutable change event (ADR-DATA-001/002) inside
// the caller's transaction, so an override can never be persisted without the
// event that announces it.
//
// aggregate_id is the PRODUCT, not a synthetic override id: the table's key is
// composite, and a consumer rebuilding catalog state groups by product.
func (s *BranchOverrideService) publishChange(ctx context.Context, tx pgx.Tx, o domain.BranchProductOverride, deleted bool) error {
	return repo.InsertOutbox(ctx, tx, o.TenantID, "branch_product_override", o.ProductID.String(), branchOverrideEventType, map[string]any{
		"tenant_id":    o.TenantID,
		"branch_id":    o.BranchID,
		"product_id":   o.ProductID,
		"is_available": o.IsAvailable,
		"price_amount": o.PriceAmount,
		"deleted":      deleted,
	})
}

// ApplyToProducts folds a branch's overrides into a tenant product listing:
// unavailable products drop out, overridden prices replace the tenant price,
// and the remaining rows are flagged so the UI can say which price is
// branch-specific.
//
// branchID == uuid.Nil is the backwards-compatible path: every product is
// returned at the tenant price with the flag false, which is exactly what the
// endpoints returned before ADR-DATA-009.
func (s *BranchOverrideService) ApplyToProducts(ctx context.Context, tenantID, branchID uuid.UUID, products []domain.Product) ([]domain.BranchProduct, error) {
	out := make([]domain.BranchProduct, 0, len(products))
	if branchID == uuid.Nil || len(products) == 0 {
		for _, p := range products {
			out = append(out, domain.BranchProduct{Product: p})
		}
		return out, nil
	}

	ids := make([]uuid.UUID, 0, len(products))
	for _, p := range products {
		ids = append(ids, p.ID)
	}

	var overrides map[uuid.UUID]domain.BranchProductOverride
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		overrides, err = s.overrideRepo.MapForBranch(ctx, tx, branchID, ids)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("catalog/service/branch_override: apply to products: %w", err)
	}

	for _, p := range products {
		o, ok := overrides[p.ID]
		if ok && !o.IsAvailable {
			continue
		}
		bp := domain.BranchProduct{Product: p}
		if ok && o.PriceAmount != nil {
			bp.Product.PriceAmount = *o.PriceAmount
			bp.BranchPriceOverridden = true
		}
		out = append(out, bp)
	}
	return out, nil
}
