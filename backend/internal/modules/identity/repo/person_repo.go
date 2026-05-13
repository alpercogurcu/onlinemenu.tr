// Package repo contains the database access layer for the identity module.
// All functions accept a pgx.Tx — direct pool access is forbidden (ADR-SEC-001).
package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"onlinemenu.tr/internal/modules/identity/domain"
	pub "onlinemenu.tr/internal/modules/identity/public"
)

// PersonRepo provides data access for the persons table.
type PersonRepo struct{}

// NewPersonRepo constructs a PersonRepo for fx injection.
func NewPersonRepo() *PersonRepo {
	return &PersonRepo{}
}

// GetByID fetches a single person by primary key.
func (r *PersonRepo) GetByID(ctx context.Context, tx pgx.Tx, personID uuid.UUID) (domain.Person, error) {
	const q = `
		SELECT id, keycloak_sub, email, full_name, COALESCE(phone, ''), created_at, updated_at
		FROM persons
		WHERE id = $1`

	row := tx.QueryRow(ctx, q, personID)
	p, err := scanPerson(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Person{}, pub.ErrNotFound
		}
		return domain.Person{}, fmt.Errorf("identity/repo/person: get by id: %w", err)
	}
	return p, nil
}

// GetByKeycloakSub resolves a Keycloak subject claim to a Person.
func (r *PersonRepo) GetByKeycloakSub(ctx context.Context, tx pgx.Tx, sub string) (domain.Person, error) {
	const q = `
		SELECT id, keycloak_sub, email, full_name, COALESCE(phone, ''), created_at, updated_at
		FROM persons
		WHERE keycloak_sub = $1`

	row := tx.QueryRow(ctx, q, sub)
	p, err := scanPerson(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Person{}, pub.ErrNotFound
		}
		return domain.Person{}, fmt.Errorf("identity/repo/person: get by keycloak sub: %w", err)
	}
	return p, nil
}

// Create inserts a new person row and returns the persisted record.
func (r *PersonRepo) Create(ctx context.Context, tx pgx.Tx, p domain.Person) (domain.Person, error) {
	const q = `
		INSERT INTO persons (keycloak_sub, email, full_name, phone)
		VALUES ($1, $2, $3, NULLIF($4, ''))
		RETURNING id, keycloak_sub, email, full_name, COALESCE(phone, ''), created_at, updated_at`

	row := tx.QueryRow(ctx, q, p.KeycloakSub, p.Email, p.FullName, p.Phone)
	created, err := scanPerson(row)
	if err != nil {
		return domain.Person{}, fmt.Errorf("identity/repo/person: create: %w", err)
	}
	return created, nil
}

// Update persists changes to mutable person fields.
func (r *PersonRepo) Update(ctx context.Context, tx pgx.Tx, p domain.Person) (domain.Person, error) {
	const q = `
		UPDATE persons
		SET email = $1, full_name = $2, phone = NULLIF($3, ''), updated_at = NOW()
		WHERE id = $4
		RETURNING id, keycloak_sub, email, full_name, COALESCE(phone, ''), created_at, updated_at`

	row := tx.QueryRow(ctx, q, p.Email, p.FullName, p.Phone, p.ID)
	updated, err := scanPerson(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Person{}, pub.ErrNotFound
		}
		return domain.Person{}, fmt.Errorf("identity/repo/person: update: %w", err)
	}
	return updated, nil
}

func scanPerson(row pgx.Row) (domain.Person, error) {
	var (
		p         domain.Person
		createdAt time.Time
		updatedAt time.Time
	)
	err := row.Scan(
		&p.ID, &p.KeycloakSub, &p.Email, &p.FullName, &p.Phone,
		&createdAt, &updatedAt,
	)
	if err != nil {
		return domain.Person{}, err
	}
	p.CreatedAt = createdAt
	p.UpdatedAt = updatedAt
	return p, nil
}
