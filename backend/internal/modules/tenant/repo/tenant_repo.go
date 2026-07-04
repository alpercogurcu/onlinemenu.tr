// Package repo contains the database access layer for the tenant module.
// All functions accept a pgx.Tx from db.Pool.WithTenantTx — direct pool access is forbidden.
package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	pub "onlinemenu.tr/internal/modules/tenant/public"
)

// TenantRepo provides data access for the tenants table.
type TenantRepo struct{}

// NewTenantRepo constructs a TenantRepo for fx injection.
func NewTenantRepo() *TenantRepo {
	return &TenantRepo{}
}

// GetByID fetches a single tenant by primary key.
func (r *TenantRepo) GetByID(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) (pub.Tenant, error) {
	const q = `
		SELECT id, name, legal_name, trade_name, slug, plan, enabled_modules,
		       identity_type, tax_no, tax_office, mersis_no,
		       address, city, district, postal_code, country,
		       phone, contact_email, is_active
		FROM tenants
		WHERE id = $1 AND is_active = true`

	row := tx.QueryRow(ctx, q, tenantID)
	t, err := scanTenant(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return pub.Tenant{}, pub.ErrNotFound
		}
		return pub.Tenant{}, fmt.Errorf("tenant/repo: get by id: %w", err)
	}
	return t, nil
}

// Create inserts a new tenant row and returns the persisted record with generated fields.
func (r *TenantRepo) Create(ctx context.Context, tx pgx.Tx, t pub.Tenant) (pub.Tenant, error) {
	modulesJSON, err := json.Marshal(t.EnabledModules)
	if err != nil {
		return pub.Tenant{}, fmt.Errorf("tenant/repo: marshal enabled_modules: %w", err)
	}

	// The id is supplied by the caller so the INSERT can run inside
	// WithTenantTx(newID): the tenants RLS WITH CHECK (id = app.tenant_id)
	// then passes without any sentinel/bypass path.
	const q = `
		INSERT INTO tenants (
			id, name, legal_name, trade_name, slug, plan, enabled_modules,
			identity_type, tax_no, tax_office, mersis_no,
			address, city, district, postal_code, country,
			phone, contact_email, is_active
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7,
			$8, $9, $10, $11,
			$12, $13, $14, $15, $16,
			$17, $18, $19
		)
		RETURNING id, name, legal_name, trade_name, slug, plan, enabled_modules,
		          identity_type, tax_no, tax_office, mersis_no,
		          address, city, district, postal_code, country,
		          phone, contact_email, is_active`

	row := tx.QueryRow(ctx, q,
		t.ID, t.Name, t.LegalName, t.TradeName, t.Slug, string(t.Plan), string(modulesJSON),
		string(t.IdentityType), t.TaxNo, t.TaxOffice, t.MersisNo,
		t.Address.Line1, t.Address.City, t.Address.District, t.Address.PostalCode, countryOrDefault(t.Address.Country),
		t.Phone, t.ContactEmail, t.IsActive,
	)

	created, err := scanTenant(row)
	if err != nil {
		return pub.Tenant{}, fmt.Errorf("tenant/repo: create tenant: %w", err)
	}
	return created, nil
}

// Update persists changes to mutable tenant fields.
func (r *TenantRepo) Update(ctx context.Context, tx pgx.Tx, t pub.Tenant) (pub.Tenant, error) {
	modulesJSON, err := json.Marshal(t.EnabledModules)
	if err != nil {
		return pub.Tenant{}, fmt.Errorf("tenant/repo: marshal enabled_modules: %w", err)
	}

	const q = `
		UPDATE tenants SET
			name = $1, legal_name = $2, trade_name = $3, slug = $4, plan = $5,
			enabled_modules = $6, identity_type = $7, tax_no = $8, tax_office = $9,
			mersis_no = $10, address = $11, city = $12, district = $13,
			postal_code = $14, country = $15, phone = $16, contact_email = $17,
			is_active = $18, updated_at = NOW()
		WHERE id = $19
		RETURNING id, name, legal_name, trade_name, slug, plan, enabled_modules,
		          identity_type, tax_no, tax_office, mersis_no,
		          address, city, district, postal_code, country,
		          phone, contact_email, is_active`

	row := tx.QueryRow(ctx, q,
		t.Name, t.LegalName, t.TradeName, t.Slug, string(t.Plan),
		string(modulesJSON), string(t.IdentityType), t.TaxNo, t.TaxOffice,
		t.MersisNo, t.Address.Line1, t.Address.City, t.Address.District,
		t.Address.PostalCode, countryOrDefault(t.Address.Country), t.Phone, t.ContactEmail,
		t.IsActive, t.ID,
	)

	updated, err := scanTenant(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return pub.Tenant{}, pub.ErrNotFound
		}
		return pub.Tenant{}, fmt.Errorf("tenant/repo: update tenant: %w", err)
	}
	return updated, nil
}

// Deactivate marks a tenant as inactive without deleting the row.
func (r *TenantRepo) Deactivate(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
	const q = `UPDATE tenants SET is_active = false, updated_at = NOW() WHERE id = $1`
	ct, err := tx.Exec(ctx, q, tenantID)
	if err != nil {
		return fmt.Errorf("tenant/repo: deactivate tenant: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return pub.ErrNotFound
	}
	return nil
}

// IsModuleEnabled checks whether the given module name exists in the tenant's enabled_modules JSONB array.
func (r *TenantRepo) IsModuleEnabled(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, module string) (bool, error) {
	const q = `SELECT enabled_modules ? $1 FROM tenants WHERE id = $2 AND is_active = true`
	var enabled bool
	err := tx.QueryRow(ctx, q, module, tenantID).Scan(&enabled)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, pub.ErrNotFound
		}
		return false, fmt.Errorf("tenant/repo: is module enabled: %w", err)
	}
	return enabled, nil
}

// scanTenant maps a single tenants row into a pub.Tenant value.
func scanTenant(row pgx.Row) (pub.Tenant, error) {
	var (
		t              pub.Tenant
		modulesRaw     []byte
		identityType   string
		plan           string
		addressLine1   string
		city, district string
		postalCode     string
		country        string
	)

	err := row.Scan(
		&t.ID, &t.Name, &t.LegalName, &t.TradeName, &t.Slug, &plan, &modulesRaw,
		&identityType, &t.TaxNo, &t.TaxOffice, &t.MersisNo,
		&addressLine1, &city, &district, &postalCode, &country,
		&t.Phone, &t.ContactEmail, &t.IsActive,
	)
	if err != nil {
		return pub.Tenant{}, err
	}

	t.Plan = pub.Plan(plan)
	t.IdentityType = pub.IdentityType(identityType)
	t.Address = pub.Address{
		Line1:      addressLine1,
		City:       city,
		District:   district,
		PostalCode: postalCode,
		Country:    country,
	}

	if err := json.Unmarshal(modulesRaw, &t.EnabledModules); err != nil {
		return pub.Tenant{}, fmt.Errorf("unmarshal enabled_modules: %w", err)
	}
	if t.EnabledModules == nil {
		t.EnabledModules = []string{}
	}

	return t, nil
}

func countryOrDefault(c string) string {
	if c == "" {
		return "TR"
	}
	return c
}
