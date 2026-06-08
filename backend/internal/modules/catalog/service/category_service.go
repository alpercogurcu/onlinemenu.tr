package service

import (
	"context"
	"errors"
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

// CategoryService manages tenant-scoped category records.
type CategoryService struct {
	db           *db.Pool
	categoryRepo *repo.CategoryRepo
	logger       *zap.Logger
}

// CategoryParams groups the fx-injected dependencies for NewCategoryService.
type CategoryParams struct {
	fx.In

	DB           *db.Pool
	CategoryRepo *repo.CategoryRepo
	Logger       *zap.Logger
}

// NewCategoryService constructs a CategoryService for fx injection.
func NewCategoryService(p CategoryParams) *CategoryService {
	return &CategoryService{
		db:           p.DB,
		categoryRepo: p.CategoryRepo,
		logger:       p.Logger,
	}
}

// List returns all categories visible to the tenant.
func (s *CategoryService) List(ctx context.Context, tenantID uuid.UUID) ([]domain.Category, error) {
	var cats []domain.Category
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		cats, err = s.categoryRepo.List(ctx, tx)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("catalog/service/category: list: %w", err)
	}
	return cats, nil
}

// GetByID returns a single category by ID.
func (s *CategoryService) GetByID(ctx context.Context, tenantID, categoryID uuid.UUID) (domain.Category, error) {
	var cat domain.Category
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		cat, err = s.categoryRepo.GetByID(ctx, tx, categoryID)
		return err
	})
	if err != nil {
		return domain.Category{}, wrapErr(err, "catalog/service/category: get by id: %w")
	}
	return cat, nil
}

// Create inserts a new category.
func (s *CategoryService) Create(ctx context.Context, tenantID uuid.UUID, c domain.Category) (domain.Category, error) {
	c.TenantID = tenantID
	var created domain.Category
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		created, err = s.categoryRepo.Create(ctx, tx, c)
		return err
	})
	if err != nil {
		return domain.Category{}, fmt.Errorf("catalog/service/category: create: %w", err)
	}
	return created, nil
}

// Update modifies an existing category.
func (s *CategoryService) Update(ctx context.Context, tenantID uuid.UUID, c domain.Category) (domain.Category, error) {
	var updated domain.Category
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		existing, err := s.categoryRepo.GetByID(ctx, tx, c.ID)
		if err != nil {
			return err
		}
		existing.Name = c.Name
		existing.Description = c.Description
		existing.ImageKey = c.ImageKey
		existing.IsActive = c.IsActive
		existing.SortOrder = c.SortOrder
		updated, err = s.categoryRepo.Update(ctx, tx, existing)
		return err
	})
	if err != nil {
		return domain.Category{}, wrapErr(err, "catalog/service/category: update: %w")
	}
	return updated, nil
}

func wrapErr(err error, format string) error {
	if errors.Is(err, repo.ErrNotFound) {
		return pub.ErrNotFound
	}
	return fmt.Errorf(format, err)
}
