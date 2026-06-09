// Package repo provides persistence for the party module.
package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"onlinemenu.tr/internal/modules/party/domain"
)

// ErrNotFound is returned when a party is not found.
var ErrNotFound = errors.New("party/repo: not found")

// PartyRepo handles persistence for Party aggregates.
type PartyRepo struct{}

// NewPartyRepo constructs a PartyRepo.
func NewPartyRepo() *PartyRepo { return &PartyRepo{} }

// Create inserts a new party row.
func (r *PartyRepo) Create(ctx context.Context, tx pgx.Tx, p domain.Party) (domain.Party, error) {
	p.ID = uuid.New()
	now := time.Now().UTC()
	p.CreatedAt = now
	p.UpdatedAt = now
	if p.Currency == "" {
		p.Currency = "TRY"
	}
	if !p.IsActive {
		p.IsActive = true
	}

	_, err := tx.Exec(ctx, `
		INSERT INTO parties (
			id, tenant_id, party_type, name, short_name,
			tax_no, tax_office, gib_alias,
			phone, email, website,
			address_line, city, district, postal_code,
			payment_terms_days, credit_limit_amount, currency,
			is_active, notes, created_at, updated_at
		) VALUES (
			$1,$2,$3,$4,$5,
			$6,$7,$8,
			$9,$10,$11,
			$12,$13,$14,$15,
			$16,$17,$18,
			$19,$20,$21,$22
		)`,
		p.ID, p.TenantID, string(p.PartyType), p.Name, p.ShortName,
		p.TaxNo, p.TaxOffice, p.GibAlias,
		p.Phone, p.Email, p.Website,
		p.AddressLine, p.City, p.District, p.PostalCode,
		p.PaymentTermsDays, p.CreditLimitAmount, p.Currency,
		p.IsActive, p.Notes, p.CreatedAt, p.UpdatedAt,
	)
	if err != nil {
		return domain.Party{}, fmt.Errorf("party/repo: create: %w", err)
	}
	return p, nil
}

// GetByID returns the party with the given ID (with contacts).
func (r *PartyRepo) GetByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (domain.Party, error) {
	row := tx.QueryRow(ctx, `
		SELECT id, tenant_id, party_type, name, short_name,
		       tax_no, tax_office, gib_alias,
		       phone, email, website,
		       address_line, city, district, postal_code,
		       payment_terms_days, credit_limit_amount, currency,
		       is_active, notes, created_at, updated_at
		FROM parties WHERE id = $1`, id)

	p, err := scanParty(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Party{}, ErrNotFound
	}
	if err != nil {
		return domain.Party{}, err
	}

	contacts, err := r.listContacts(ctx, tx, p.ID)
	if err != nil {
		return domain.Party{}, err
	}
	p.Contacts = contacts
	return p, nil
}

// Update modifies mutable fields of an existing party.
func (r *PartyRepo) Update(ctx context.Context, tx pgx.Tx, p domain.Party) (domain.Party, error) {
	p.UpdatedAt = time.Now().UTC()

	tag, err := tx.Exec(ctx, `
		UPDATE parties SET
			party_type = $2, name = $3, short_name = $4,
			tax_no = $5, tax_office = $6, gib_alias = $7,
			phone = $8, email = $9, website = $10,
			address_line = $11, city = $12, district = $13, postal_code = $14,
			payment_terms_days = $15, credit_limit_amount = $16, currency = $17,
			is_active = $18, notes = $19, updated_at = $20
		WHERE id = $1`,
		p.ID, string(p.PartyType), p.Name, p.ShortName,
		p.TaxNo, p.TaxOffice, p.GibAlias,
		p.Phone, p.Email, p.Website,
		p.AddressLine, p.City, p.District, p.PostalCode,
		p.PaymentTermsDays, p.CreditLimitAmount, p.Currency,
		p.IsActive, p.Notes, p.UpdatedAt,
	)
	if err != nil {
		return domain.Party{}, fmt.Errorf("party/repo: update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.Party{}, ErrNotFound
	}
	return p, nil
}

// List returns parties for the given tenant, optionally filtered by type.
// partyType="" returns all types.
func (r *PartyRepo) List(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, partyType domain.PartyType, limit, offset int) ([]domain.Party, error) {
	if limit <= 0 {
		limit = 50
	}
	var (
		query string
		args  []any
	)
	if partyType == "" {
		query = `SELECT id, tenant_id, party_type, name, short_name,
		                tax_no, tax_office, gib_alias,
		                phone, email, website,
		                address_line, city, district, postal_code,
		                payment_terms_days, credit_limit_amount, currency,
		                is_active, notes, created_at, updated_at
		         FROM parties WHERE tenant_id = $1
		         ORDER BY name LIMIT $2 OFFSET $3`
		args = []any{tenantID, limit, offset}
	} else {
		query = `SELECT id, tenant_id, party_type, name, short_name,
		                tax_no, tax_office, gib_alias,
		                phone, email, website,
		                address_line, city, district, postal_code,
		                payment_terms_days, credit_limit_amount, currency,
		                is_active, notes, created_at, updated_at
		         FROM parties WHERE tenant_id = $1 AND (party_type = $2 OR party_type = 'both')
		         ORDER BY name LIMIT $3 OFFSET $4`
		args = []any{tenantID, string(partyType), limit, offset}
	}

	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("party/repo: list: %w", err)
	}
	defer rows.Close()

	var parties []domain.Party
	for rows.Next() {
		p, err := scanParty(rows)
		if err != nil {
			return nil, err
		}
		parties = append(parties, p)
	}
	return parties, rows.Err()
}

// AddContact inserts a contact for the given party.
func (r *PartyRepo) AddContact(ctx context.Context, tx pgx.Tx, c domain.Contact) (domain.Contact, error) {
	c.ID = uuid.New()
	c.CreatedAt = time.Now().UTC()

	_, err := tx.Exec(ctx, `
		INSERT INTO party_contacts (id, tenant_id, party_id, name, role, phone, email, is_primary, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		c.ID, c.TenantID, c.PartyID, c.Name, c.Role, c.Phone, c.Email, c.IsPrimary, c.CreatedAt,
	)
	if err != nil {
		return domain.Contact{}, fmt.Errorf("party/repo: add contact: %w", err)
	}
	return c, nil
}

// DeleteContact removes a specific contact.
func (r *PartyRepo) DeleteContact(ctx context.Context, tx pgx.Tx, contactID uuid.UUID) error {
	_, err := tx.Exec(ctx, `DELETE FROM party_contacts WHERE id = $1`, contactID)
	if err != nil {
		return fmt.Errorf("party/repo: delete contact: %w", err)
	}
	return nil
}

func (r *PartyRepo) listContacts(ctx context.Context, tx pgx.Tx, partyID uuid.UUID) ([]domain.Contact, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, tenant_id, party_id, name, role, phone, email, is_primary, created_at
		FROM party_contacts WHERE party_id = $1 ORDER BY is_primary DESC, created_at`, partyID)
	if err != nil {
		return nil, fmt.Errorf("party/repo: list contacts: %w", err)
	}
	defer rows.Close()

	var contacts []domain.Contact
	for rows.Next() {
		var c domain.Contact
		if err := rows.Scan(&c.ID, &c.TenantID, &c.PartyID, &c.Name, &c.Role, &c.Phone, &c.Email, &c.IsPrimary, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("party/repo: scan contact: %w", err)
		}
		contacts = append(contacts, c)
	}
	return contacts, rows.Err()
}

func scanParty(row interface{ Scan(dest ...any) error }) (domain.Party, error) {
	var p domain.Party
	err := row.Scan(
		&p.ID, &p.TenantID, &p.PartyType, &p.Name, &p.ShortName,
		&p.TaxNo, &p.TaxOffice, &p.GibAlias,
		&p.Phone, &p.Email, &p.Website,
		&p.AddressLine, &p.City, &p.District, &p.PostalCode,
		&p.PaymentTermsDays, &p.CreditLimitAmount, &p.Currency,
		&p.IsActive, &p.Notes, &p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		return domain.Party{}, fmt.Errorf("party/repo: scan party: %w", err)
	}
	return p, nil
}

// SearchByName returns parties whose name contains the query string (case-insensitive).
func (r *PartyRepo) SearchByName(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, query string, limit int) ([]domain.Party, error) {
	if limit <= 0 {
		limit = 20
	}
	pattern := "%" + strings.ToLower(query) + "%"
	rows, err := tx.Query(ctx, `
		SELECT id, tenant_id, party_type, name, short_name,
		       tax_no, tax_office, gib_alias,
		       phone, email, website,
		       address_line, city, district, postal_code,
		       payment_terms_days, credit_limit_amount, currency,
		       is_active, notes, created_at, updated_at
		FROM parties
		WHERE tenant_id = $1 AND LOWER(name) LIKE $2
		ORDER BY name LIMIT $3`, tenantID, pattern, limit)
	if err != nil {
		return nil, fmt.Errorf("party/repo: search: %w", err)
	}
	defer rows.Close()

	var parties []domain.Party
	for rows.Next() {
		p, err := scanParty(rows)
		if err != nil {
			return nil, err
		}
		parties = append(parties, p)
	}
	return parties, rows.Err()
}
