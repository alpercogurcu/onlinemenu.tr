// Package service implements party business logic.
package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/party/domain"
	"onlinemenu.tr/internal/modules/party/repo"
	"onlinemenu.tr/internal/platform/db"
)

// PartyService orchestrates party CRUD operations.
type PartyService struct {
	db        *db.Pool
	partyRepo *repo.PartyRepo
	logger    *zap.Logger
}

// Params groups fx-injected dependencies.
type Params struct {
	fx.In

	DB        *db.Pool
	PartyRepo *repo.PartyRepo
	Logger    *zap.Logger
}

// New constructs a PartyService.
func New(p Params) *PartyService {
	return &PartyService{
		db:        p.DB,
		partyRepo: p.PartyRepo,
		logger:    p.Logger,
	}
}

// ErrNotFound is returned when a party cannot be found.
var ErrNotFound = errors.New("party/service: not found")

// ErrInvalidInput is returned when request validation fails.
var ErrInvalidInput = errors.New("party/service: invalid input")

// CreateParty creates a new trading partner.
func (s *PartyService) CreateParty(ctx context.Context, tenantID uuid.UUID, p domain.Party) (domain.Party, error) {
	if p.Name == "" {
		return domain.Party{}, fmt.Errorf("%w: name required", ErrInvalidInput)
	}
	if !p.PartyType.Valid() {
		return domain.Party{}, fmt.Errorf("%w: invalid party_type %q", ErrInvalidInput, p.PartyType)
	}
	p.TenantID = tenantID

	var created domain.Party
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var txErr error
		created, txErr = s.partyRepo.Create(ctx, tx, p)
		return txErr
	})
	return created, err
}

// GetParty returns a party by ID.
func (s *PartyService) GetParty(ctx context.Context, tenantID, partyID uuid.UUID) (domain.Party, error) {
	var p domain.Party
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var txErr error
		p, txErr = s.partyRepo.GetByID(ctx, tx, partyID)
		return txErr
	})
	if errors.Is(err, repo.ErrNotFound) {
		return domain.Party{}, ErrNotFound
	}
	return p, err
}

// UpdateParty modifies a party's data.
func (s *PartyService) UpdateParty(ctx context.Context, tenantID uuid.UUID, p domain.Party) (domain.Party, error) {
	if p.Name == "" {
		return domain.Party{}, fmt.Errorf("%w: name required", ErrInvalidInput)
	}
	if !p.PartyType.Valid() {
		return domain.Party{}, fmt.Errorf("%w: invalid party_type %q", ErrInvalidInput, p.PartyType)
	}
	p.TenantID = tenantID

	var updated domain.Party
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var txErr error
		updated, txErr = s.partyRepo.Update(ctx, tx, p)
		return txErr
	})
	if errors.Is(err, repo.ErrNotFound) {
		return domain.Party{}, ErrNotFound
	}
	return updated, err
}

// ListParties returns parties for a tenant with optional type filter.
func (s *PartyService) ListParties(ctx context.Context, tenantID uuid.UUID, partyType domain.PartyType, limit, offset int) ([]domain.Party, error) {
	var parties []domain.Party
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var txErr error
		parties, txErr = s.partyRepo.List(ctx, tx, tenantID, partyType, limit, offset)
		return txErr
	})
	return parties, err
}

// SearchParties returns parties matching the given name query.
func (s *PartyService) SearchParties(ctx context.Context, tenantID uuid.UUID, query string, limit int) ([]domain.Party, error) {
	if query == "" {
		return nil, fmt.Errorf("%w: search query required", ErrInvalidInput)
	}
	var parties []domain.Party
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var txErr error
		parties, txErr = s.partyRepo.SearchByName(ctx, tx, tenantID, query, limit)
		return txErr
	})
	return parties, err
}

// AddContact adds a contact person to a party.
func (s *PartyService) AddContact(ctx context.Context, tenantID uuid.UUID, c domain.Contact) (domain.Contact, error) {
	if c.Name == "" {
		return domain.Contact{}, fmt.Errorf("%w: contact name required", ErrInvalidInput)
	}
	c.TenantID = tenantID

	var created domain.Contact
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var txErr error
		created, txErr = s.partyRepo.AddContact(ctx, tx, c)
		return txErr
	})
	return created, err
}

// DeleteContact removes a contact from a party.
func (s *PartyService) DeleteContact(ctx context.Context, tenantID uuid.UUID, contactID uuid.UUID) error {
	return s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		return s.partyRepo.DeleteContact(ctx, tx, contactID)
	})
}
