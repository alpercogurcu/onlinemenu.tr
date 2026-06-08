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

// ModifierService manages modifier groups, modifiers, and their product assignments.
type ModifierService struct {
	db           *db.Pool
	groupRepo    *repo.ModifierGroupRepo
	modifierRepo *repo.ModifierRepo
	pmgRepo      *repo.ProductModifierGroupRepo
	logger       *zap.Logger
}

// ModifierParams groups fx-injected dependencies for NewModifierService.
type ModifierParams struct {
	fx.In

	DB           *db.Pool
	GroupRepo    *repo.ModifierGroupRepo
	ModifierRepo *repo.ModifierRepo
	PMGRepo      *repo.ProductModifierGroupRepo
	Logger       *zap.Logger
}

// NewModifierService constructs a ModifierService for fx injection.
func NewModifierService(p ModifierParams) *ModifierService {
	return &ModifierService{
		db:           p.DB,
		groupRepo:    p.GroupRepo,
		modifierRepo: p.ModifierRepo,
		pmgRepo:      p.PMGRepo,
		logger:       p.Logger,
	}
}

func (s *ModifierService) CreateGroup(ctx context.Context, tenantID uuid.UUID, g domain.ModifierGroup) (domain.ModifierGroup, error) {
	if err := validateModifierGroup(g); err != nil {
		return domain.ModifierGroup{}, err
	}
	g.TenantID = tenantID
	var created domain.ModifierGroup
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		created, err = s.groupRepo.Create(ctx, tx, g)
		return err
	})
	if err != nil {
		return domain.ModifierGroup{}, fmt.Errorf("catalog/service/modifier: create group: %w", err)
	}
	return created, nil
}

func (s *ModifierService) GetGroup(ctx context.Context, tenantID, groupID uuid.UUID) (domain.ModifierGroup, error) {
	var g domain.ModifierGroup
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		g, err = s.groupRepo.GetByID(ctx, tx, groupID)
		return err
	})
	if err != nil {
		return domain.ModifierGroup{}, wrapErr(err, "catalog/service/modifier: get group: %w")
	}
	return g, nil
}

func (s *ModifierService) ListGroups(ctx context.Context, tenantID uuid.UUID) ([]domain.ModifierGroup, error) {
	var groups []domain.ModifierGroup
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		groups, err = s.groupRepo.List(ctx, tx)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("catalog/service/modifier: list groups: %w", err)
	}
	return groups, nil
}

func (s *ModifierService) UpdateGroup(ctx context.Context, tenantID uuid.UUID, g domain.ModifierGroup) (domain.ModifierGroup, error) {
	if err := validateModifierGroup(g); err != nil {
		return domain.ModifierGroup{}, err
	}
	var updated domain.ModifierGroup
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		existing, err := s.groupRepo.GetByID(ctx, tx, g.ID)
		if err != nil {
			return err
		}
		existing.Name = g.Name
		existing.SelectionType = g.SelectionType
		existing.MinSelections = g.MinSelections
		existing.MaxSelections = g.MaxSelections
		existing.IsRequired = g.IsRequired
		existing.SortOrder = g.SortOrder
		updated, err = s.groupRepo.Update(ctx, tx, existing)
		return err
	})
	if err != nil {
		return domain.ModifierGroup{}, wrapErr(err, "catalog/service/modifier: update group: %w")
	}
	return updated, nil
}

func (s *ModifierService) DeleteGroup(ctx context.Context, tenantID, groupID uuid.UUID) error {
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		return s.groupRepo.Delete(ctx, tx, groupID)
	})
	if err != nil {
		return wrapErr(err, "catalog/service/modifier: delete group: %w")
	}
	return nil
}

func (s *ModifierService) CreateModifier(ctx context.Context, tenantID uuid.UUID, m domain.Modifier) (domain.Modifier, error) {
	if m.Name == "" {
		return domain.Modifier{}, &pub.ValidationError{Msg: "modifier name is required"}
	}
	m.TenantID = tenantID
	var created domain.Modifier
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		// Verify group belongs to this tenant before inserting.
		if _, err := s.groupRepo.GetByID(ctx, tx, m.GroupID); err != nil {
			return err
		}
		var err error
		created, err = s.modifierRepo.Create(ctx, tx, m)
		return err
	})
	if err != nil {
		return domain.Modifier{}, wrapErr(err, "catalog/service/modifier: create modifier: %w")
	}
	return created, nil
}

func (s *ModifierService) ListModifiers(ctx context.Context, tenantID, groupID uuid.UUID) ([]domain.Modifier, error) {
	var modifiers []domain.Modifier
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		modifiers, err = s.modifierRepo.ListByGroup(ctx, tx, groupID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("catalog/service/modifier: list modifiers: %w", err)
	}
	return modifiers, nil
}

func (s *ModifierService) UpdateModifier(ctx context.Context, tenantID uuid.UUID, m domain.Modifier) (domain.Modifier, error) {
	var updated domain.Modifier
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		existing, err := s.modifierRepo.GetByID(ctx, tx, m.ID)
		if err != nil {
			return err
		}
		existing.Name = m.Name
		existing.PriceDelta = m.PriceDelta
		existing.IsActive = m.IsActive
		existing.SortOrder = m.SortOrder
		updated, err = s.modifierRepo.Update(ctx, tx, existing)
		return err
	})
	if err != nil {
		return domain.Modifier{}, wrapErr(err, "catalog/service/modifier: update modifier: %w")
	}
	return updated, nil
}

func (s *ModifierService) DeleteModifier(ctx context.Context, tenantID, modifierID uuid.UUID) error {
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		return s.modifierRepo.Delete(ctx, tx, modifierID)
	})
	if err != nil {
		return wrapErr(err, "catalog/service/modifier: delete modifier: %w")
	}
	return nil
}

func (s *ModifierService) AssignGroup(ctx context.Context, tenantID, productID, groupID uuid.UUID, sortOrder int16) error {
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		return s.pmgRepo.Assign(ctx, tx, productID, groupID, tenantID, sortOrder)
	})
	if err != nil {
		return fmt.Errorf("catalog/service/modifier: assign group: %w", err)
	}
	return nil
}

func (s *ModifierService) RemoveGroup(ctx context.Context, tenantID, productID, groupID uuid.UUID) error {
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		return s.pmgRepo.Remove(ctx, tx, productID, groupID)
	})
	if err != nil {
		return fmt.Errorf("catalog/service/modifier: remove group: %w", err)
	}
	return nil
}

func (s *ModifierService) ListProductGroups(ctx context.Context, tenantID, productID uuid.UUID) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		ids, err = s.pmgRepo.ListByProduct(ctx, tx, productID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("catalog/service/modifier: list product groups: %w", err)
	}
	return ids, nil
}

// validateModifierGroup enforces SelectionType validity and min/max invariant.
func validateModifierGroup(g domain.ModifierGroup) error {
	if !g.SelectionType.Valid() {
		return &pub.ValidationError{Msg: fmt.Sprintf("invalid selection_type %q; must be single or multiple", g.SelectionType)}
	}
	if g.MinSelections < 0 {
		return &pub.ValidationError{Msg: "min_selections must be >= 0"}
	}
	if g.MaxSelections != nil && *g.MaxSelections < g.MinSelections {
		return &pub.ValidationError{Msg: "max_selections must be >= min_selections"}
	}
	return nil
}
