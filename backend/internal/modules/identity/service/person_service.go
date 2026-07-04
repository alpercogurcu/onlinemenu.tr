package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/identity/domain"
	pub "onlinemenu.tr/internal/modules/identity/public"
	"onlinemenu.tr/internal/modules/identity/repo"
	"onlinemenu.tr/internal/platform/db"
)

// PersonService manages platform-level person records.
// Person read operations use db.WithAllTenantsTx/WithAllTenantsReadTx (the
// app.tenant_scope = 'all_tenants' RLS path) because persons are not owned by
// any single tenant — they exist at platform scope. This replaces the former
// uuid.Nil "tenant" sentinel (docs/lessons-from-b2b.md item 6): uuid.Nil is no
// longer a valid WithTenantTx argument (see db.ErrNilTenant), so platform-level
// access must go through this explicitly-named, separately-authorized path.
type PersonService struct {
	db         *db.Pool
	personRepo *repo.PersonRepo
	logger     *zap.Logger
}

// PersonParams groups the fx-injected dependencies for NewPersonService.
type PersonParams struct {
	fx.In

	DB         *db.Pool
	PersonRepo *repo.PersonRepo
	Logger     *zap.Logger
}

// NewPersonService constructs a PersonService for fx injection.
func NewPersonService(p PersonParams) *PersonService {
	return &PersonService{
		db:         p.DB,
		personRepo: p.PersonRepo,
		logger:     p.Logger,
	}
}

// GetByKeycloakSub resolves a Keycloak subject claim to a platform Person.
// This is a platform-scope read: the persons table is cross-tenant.
func (s *PersonService) GetByKeycloakSub(ctx context.Context, sub string) (domain.Person, error) {
	var person domain.Person
	err := s.db.WithAllTenantsReadTx(ctx, func(tx pgx.Tx) error {
		var err error
		person, err = s.personRepo.GetByKeycloakSub(ctx, tx, sub)
		return err
	})
	if err != nil {
		return domain.Person{}, wrapNotFound(err, "identity/service/person: get by keycloak sub: %w")
	}
	return person, nil
}

// GetByID returns the Person for the given personID.
// This is a platform-scope read: the persons table is cross-tenant.
func (s *PersonService) GetByID(ctx context.Context, personID uuid.UUID) (domain.Person, error) {
	var person domain.Person
	err := s.db.WithAllTenantsReadTx(ctx, func(tx pgx.Tx) error {
		var err error
		person, err = s.personRepo.GetByID(ctx, tx, personID)
		return err
	})
	if err != nil {
		return domain.Person{}, wrapNotFound(err, "identity/service/person: get by id: %w")
	}
	return person, nil
}

// Create inserts a new Person at platform scope.
// This is a platform-scope write: the person has no tenant yet.
func (s *PersonService) Create(ctx context.Context, p domain.Person) (domain.Person, error) {
	var created domain.Person
	err := s.db.WithAllTenantsTx(ctx, func(tx pgx.Tx) error {
		var err error
		created, err = s.personRepo.Create(ctx, tx, p)
		return err
	})
	if err != nil {
		return domain.Person{}, fmt.Errorf("identity/service/person: create: %w", err)
	}
	return created, nil
}

// Update modifies only fullName and phone for an existing person.
// A read-modify-write is performed inside a single transaction so the email
// and other fields that this operation does not own are never overwritten.
func (s *PersonService) Update(ctx context.Context, tenantID, personID uuid.UUID, fullName, phone string) (domain.Person, error) {
	var updated domain.Person
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		current, err := s.personRepo.GetByID(ctx, tx, personID)
		if err != nil {
			return err
		}
		current.FullName = fullName
		current.Phone = phone
		updated, err = s.personRepo.Update(ctx, tx, current)
		return err
	})
	if err != nil {
		return domain.Person{}, wrapNotFound(err, "identity/service/person: update: %w")
	}
	return updated, nil
}

// wrapNotFound returns pub.ErrNotFound and pub.ErrInvalid unwrapped so callers
// can use errors.Is. All other errors are wrapped with the supplied format string.
func wrapNotFound(err error, format string) error {
	if errors.Is(err, pub.ErrNotFound) || errors.Is(err, pub.ErrInvalid) {
		return err
	}
	return fmt.Errorf(format, err)
}
