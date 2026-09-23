package service

import (
	"context"
	"fmt"
	"strings"

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
	menuRepo    *repo.MenuRepo
	menuItems   *repo.MenuItemRepo
	logger      *zap.Logger
}

// ProductParams groups the fx-injected dependencies for NewProductService.
type ProductParams struct {
	fx.In

	DB           *db.Pool
	ProductRepo  *repo.ProductRepo
	MenuRepo     *repo.MenuRepo
	MenuItemRepo *repo.MenuItemRepo
	Logger       *zap.Logger
}

// NewProductService constructs a ProductService for fx injection.
func NewProductService(p ProductParams) *ProductService {
	return &ProductService{
		db:          p.DB,
		productRepo: p.ProductRepo,
		menuRepo:    p.MenuRepo,
		menuItems:   p.MenuItemRepo,
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

// ListByCategory returns the active products of a category; includeInactive
// adds the deactivated ones (admin views).
func (s *ProductService) ListByCategory(ctx context.Context, tenantID, categoryID uuid.UUID, includeInactive bool) ([]domain.Product, error) {
	var products []domain.Product
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		products, err = s.productRepo.ListByCategory(ctx, tx, categoryID, includeInactive)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("catalog/service/product: list by category: %w", err)
	}
	return products, nil
}

// Create inserts a new product.
func (s *ProductService) Create(ctx context.Context, tenantID uuid.UUID, p domain.Product) (domain.Product, error) {
	name, err := requireName(p.Name)
	if err != nil {
		return domain.Product{}, err
	}
	p.Name = name
	p.TenantID = tenantID
	var created domain.Product
	err = s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		created, err = s.productRepo.Create(ctx, tx, p)
		if err != nil {
			return err
		}
		created.MenuMembership, err = s.addToSoleActiveMenu(ctx, tx, created)
		return err
	})
	if err != nil {
		return domain.Product{}, fmt.Errorf("catalog/service/product: create: %w", err)
	}
	return created, nil
}

// addToSoleActiveMenu makes a new product visible to guests when the tenant
// has exactly one active menu. With zero or several active menus the choice is
// the operator's, so nothing is added.
func (s *ProductService) addToSoleActiveMenu(ctx context.Context, tx pgx.Tx, p domain.Product) (string, error) {
	menus, err := s.menuRepo.List(ctx, tx)
	if err != nil {
		return "", err
	}
	var active []domain.Menu
	for _, m := range menus {
		if m.IsActive {
			active = append(active, m)
		}
	}
	if len(active) != 1 {
		return domain.MenuMembershipManual, nil
	}
	err = s.menuItems.AddItem(ctx, tx, domain.MenuItem{
		MenuID:    active[0].ID,
		ProductID: p.ID,
		TenantID:  p.TenantID,
		IsActive:  true,
		SortOrder: p.SortOrder,
	})
	if err != nil {
		return "", err
	}
	return domain.MenuMembershipAuto, nil
}

// Update modifies an existing product.
func (s *ProductService) Update(ctx context.Context, tenantID uuid.UUID, p domain.Product) (domain.Product, error) {
	name, err := requireName(p.Name)
	if err != nil {
		return domain.Product{}, err
	}
	p.Name = name
	var updated domain.Product
	err = s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		existing, err := s.productRepo.GetByID(ctx, tx, p.ID)
		if err != nil {
			return err
		}
		// Preserve tenant isolation — never allow cross-tenant writes via Update.
		p.TenantID = existing.TenantID
		// The PUT contract (http/handler.go updateProduct) carries no sku/
		// image_key/barcode fields, but repo.Update writes every column
		// unconditionally — without this, any PUT from any client silently
		// blanks these out. Restore them from the row that's already there.
		p.SKU = existing.SKU
		p.ImageKey = existing.ImageKey
		p.Barcode = existing.Barcode
		// currency is CHAR(3) NOT NULL; an empty/whitespace value from the
		// request body must not be written as-is (it would persist as
		// "   "), so fall back to the existing value, and to "TRY" if even
		// that is somehow blank.
		if strings.TrimSpace(p.Currency) == "" {
			p.Currency = existing.Currency
			if strings.TrimSpace(p.Currency) == "" {
				p.Currency = "TRY"
			}
		}
		updated, err = s.productRepo.Update(ctx, tx, p)
		return err
	})
	if err != nil {
		return domain.Product{}, wrapErr(err, "catalog/service/product: update: %w")
	}
	return updated, nil
}

// Delete soft-deletes a product (sets is_active=false) and, in the same
// transaction, drops its menu items, modifier-group links, branch overrides and
// channel availability. The products row is kept for order history; the links
// are cleared because a deleted product has no reactivation flow that would
// need them, and stale rows would otherwise resurface with old prices/menus if
// the product were ever switched back on.
func (s *ProductService) Delete(ctx context.Context, tenantID, productID uuid.UUID) error {
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		if err := s.productRepo.Delete(ctx, tx, productID); err != nil {
			return err
		}
		return s.productRepo.PurgeReferences(ctx, tx, productID)
	})
	if err != nil {
		return wrapErr(err, "catalog/service/product: delete: %w")
	}
	return nil
}
