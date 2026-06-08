package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/catalog/domain"
	"onlinemenu.tr/internal/modules/catalog/repo"
	"onlinemenu.tr/internal/platform/db"
)

// ProductService manages tenant-scoped product records.
type ProductService struct {
	db          *db.Pool
	productRepo *repo.ProductRepo
	logger      *zap.Logger
}

// ProductParams groups the fx-injected dependencies for NewProductService.
type ProductParams struct {
	fx.In

	DB          *db.Pool
	ProductRepo *repo.ProductRepo
	Logger      *zap.Logger
}

// NewProductService constructs a ProductService for fx injection.
func NewProductService(p ProductParams) *ProductService {
	return &ProductService{
		db:          p.DB,
		productRepo: p.ProductRepo,
		logger:      p.Logger,
	}
}

// List returns all products visible to the tenant.
func (s *ProductService) List(ctx context.Context, tenantID uuid.UUID) ([]domain.Product, error) {
	var products []domain.Product
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		products, err = s.productRepo.List(ctx, tx)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("catalog/service/product: list: %w", err)
	}
	return products, nil
}

// GetByID returns a single product by ID.
func (s *ProductService) GetByID(ctx context.Context, tenantID, productID uuid.UUID) (domain.Product, error) {
	var p domain.Product
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		p, err = s.productRepo.GetByID(ctx, tx, productID)
		return err
	})
	if err != nil {
		return domain.Product{}, wrapErr(err, "catalog/service/product: get by id: %w")
	}
	return p, nil
}

// ListByCategory returns products belonging to a specific category.
func (s *ProductService) ListByCategory(ctx context.Context, tenantID, categoryID uuid.UUID) ([]domain.Product, error) {
	var products []domain.Product
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		products, err = s.productRepo.ListByCategory(ctx, tx, categoryID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("catalog/service/product: list by category: %w", err)
	}
	return products, nil
}

// Create inserts a new product.
func (s *ProductService) Create(ctx context.Context, tenantID uuid.UUID, p domain.Product) (domain.Product, error) {
	p.TenantID = tenantID
	var created domain.Product
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		created, err = s.productRepo.Create(ctx, tx, p)
		return err
	})
	if err != nil {
		return domain.Product{}, fmt.Errorf("catalog/service/product: create: %w", err)
	}
	return created, nil
}

// Update modifies an existing product.
func (s *ProductService) Update(ctx context.Context, tenantID uuid.UUID, p domain.Product) (domain.Product, error) {
	var updated domain.Product
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		existing, err := s.productRepo.GetByID(ctx, tx, p.ID)
		if err != nil {
			return err
		}
		// Preserve tenant isolation — never allow cross-tenant writes via Update.
		p.TenantID = existing.TenantID
		updated, err = s.productRepo.Update(ctx, tx, p)
		return err
	})
	if err != nil {
		return domain.Product{}, wrapErr(err, "catalog/service/product: update: %w")
	}
	return updated, nil
}

// Delete soft-deletes a product (sets is_active=false).
func (s *ProductService) Delete(ctx context.Context, tenantID, productID uuid.UUID) error {
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		return s.productRepo.Delete(ctx, tx, productID)
	})
	if err != nil {
		return wrapErr(err, "catalog/service/product: delete: %w")
	}
	return nil
}
