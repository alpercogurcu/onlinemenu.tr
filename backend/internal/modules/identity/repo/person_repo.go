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

// GetByEmail resolves a person by email. Platform-scope (WithAllTenantsTx):
// the same email may belong to a person visible under a different tenant
// than the caller's current one (AUTH-002's single realm), so this must not
// be run under a tenant-scoped tx.
func (r *PersonRepo) GetByEmail(ctx context.Context, tx pgx.Tx, email string) (domain.Person, error) {
	const q = `
		SELECT id, keycloak_sub, email, full_name, COALESCE(phone, ''), created_at, updated_at
		FROM persons
		WHERE email = $1`

	row := tx.QueryRow(ctx, q, email)
	p, err := scanPerson(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Person{}, pub.ErrNotFound
		}
		return domain.Person{}, fmt.Errorf("identity/repo/person: get by email: %w", err)
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

// FindOrCreateByKeycloakSub returns the existing person for p.KeycloakSub, or
// creates one from p if none exists yet.
//
// This is the idempotency primitive the staff invite flow (ADR-AUTH-003)
// relies on: two concurrent invites for a brand-new email (or a single
// invite retried after its Keycloak write succeeded but nothing was
// committed to Postgres) must converge on exactly one persons row.
//
// ON CONFLICT DO NOTHING is used rather than catching a unique-violation
// error, because a raised error would abort the surrounding transaction —
// the caller could not then re-SELECT the existing row without starting a
// new transaction. DO NOTHING never raises, so the fallback SELECT below
// runs safely inside the same tx as the attempted INSERT.
func (r *PersonRepo) FindOrCreateByKeycloakSub(ctx context.Context, tx pgx.Tx, p domain.Person) (domain.Person, error) {
	const insertQ = `
		INSERT INTO persons (keycloak_sub, email, full_name, phone)
		VALUES ($1, $2, $3, NULLIF($4, ''))
		ON CONFLICT (keycloak_sub) DO NOTHING
		RETURNING id, keycloak_sub, email, full_name, COALESCE(phone, ''), created_at, updated_at`

	row := tx.QueryRow(ctx, insertQ, p.KeycloakSub, p.Email, p.FullName, p.Phone)
	created, err := scanPerson(row)
	if err == nil {
		return created, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.Person{}, fmt.Errorf("identity/repo/person: find or create: insert: %w", err)
	}

	// Conflict: another invite already created this person. Fetch it — no
	// abort occurred, so this SELECT runs in the same transaction.
	existing, err := r.GetByKeycloakSub(ctx, tx, p.KeycloakSub)
	if err != nil {
		return domain.Person{}, fmt.Errorf("identity/repo/person: find or create: refetch: %w", err)
	}
	return existing, nil
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
