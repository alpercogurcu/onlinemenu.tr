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

// MenuService manages menus and their product assignments.
type MenuService struct {
	db           *db.Pool
	menuRepo     *repo.MenuRepo
	menuItemRepo *repo.MenuItemRepo
	logger       *zap.Logger
}

// MenuParams groups fx-injected dependencies for NewMenuService.
type MenuParams struct {
	fx.In

	DB           *db.Pool
	MenuRepo     *repo.MenuRepo
	MenuItemRepo *repo.MenuItemRepo
	Logger       *zap.Logger
}

// NewMenuService constructs a MenuService for fx injection.
func NewMenuService(p MenuParams) *MenuService {
	return &MenuService{
		db:           p.DB,
		menuRepo:     p.MenuRepo,
		menuItemRepo: p.MenuItemRepo,
		logger:       p.Logger,
	}
}

func (s *MenuService) Create(ctx context.Context, tenantID uuid.UUID, m domain.Menu) (domain.Menu, error) {
	m.TenantID = tenantID
	var created domain.Menu
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		created, err = s.menuRepo.Create(ctx, tx, m)
		return err
	})
	if err != nil {
		return domain.Menu{}, fmt.Errorf("catalog/service/menu: create: %w", err)
	}
	return created, nil
}

func (s *MenuService) GetByID(ctx context.Context, tenantID, menuID uuid.UUID) (domain.Menu, error) {
	var m domain.Menu
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		m, err = s.menuRepo.GetByID(ctx, tx, menuID)
		return err
	})
	if err != nil {
		return domain.Menu{}, wrapErr(err, "catalog/service/menu: get by id: %w")
	}
	return m, nil
}

func (s *MenuService) List(ctx context.Context, tenantID uuid.UUID) ([]domain.Menu, error) {
	var menus []domain.Menu
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		menus, err = s.menuRepo.List(ctx, tx)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("catalog/service/menu: list: %w", err)
	}
	return menus, nil
}

func (s *MenuService) Update(ctx context.Context, tenantID uuid.UUID, m domain.Menu) (domain.Menu, error) {
	var updated domain.Menu
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		existing, err := s.menuRepo.GetByID(ctx, tx, m.ID)
		if err != nil {
			return err
		}
		existing.Name = m.Name
		existing.Description = m.Description
		existing.IsActive = m.IsActive
		existing.ValidFrom = m.ValidFrom
		existing.ValidUntil = m.ValidUntil
		existing.SortOrder = m.SortOrder
		updated, err = s.menuRepo.Update(ctx, tx, existing)
		return err
	})
	if err != nil {
		return domain.Menu{}, wrapErr(err, "catalog/service/menu: update: %w")
	}
	return updated, nil
}

func (s *MenuService) AddItem(ctx context.Context, tenantID uuid.UUID, item domain.MenuItem) error {
	item.TenantID = tenantID
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		return s.menuItemRepo.AddItem(ctx, tx, item)
	})
	if err != nil {
		return fmt.Errorf("catalog/service/menu: add item: %w", err)
	}
	return nil
}

func (s *MenuService) RemoveItem(ctx context.Context, tenantID, menuID, productID uuid.UUID) error {
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		return s.menuItemRepo.RemoveItem(ctx, tx, menuID, productID)
	})
	if err != nil {
		return fmt.Errorf("catalog/service/menu: remove item: %w", err)
	}
	return nil
}

func (s *MenuService) ListItems(ctx context.Context, tenantID, menuID uuid.UUID) ([]domain.MenuItem, error) {
	var items []domain.MenuItem
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		items, err = s.menuItemRepo.ListByMenu(ctx, tx, menuID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("catalog/service/menu: list items: %w", err)
	}
	return items, nil
}
