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

// BranchRepo provides data access for the branches table.
type BranchRepo struct{}

// NewBranchRepo constructs a BranchRepo for fx injection.
func NewBranchRepo() *BranchRepo {
	return &BranchRepo{}
}

// GetBranch fetches a single branch by tenant and branch IDs.
func (r *BranchRepo) GetBranch(ctx context.Context, tx pgx.Tx, tenantID, branchID uuid.UUID) (pub.Branch, error) {
	const q = `
		SELECT id, tenant_id, name, slug, ownership_type, operation_type, supply_rules,
		       phone, address, city, district, postal_code,
		       iban, legal_name, identity_type, tax_no, tax_office, is_active
		FROM branches
		WHERE tenant_id = $1 AND id = $2`

	row := tx.QueryRow(ctx, q, tenantID, branchID)
	b, err := scanBranch(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return pub.Branch{}, pub.ErrNotFound
		}
		return pub.Branch{}, fmt.Errorf("tenant/repo: get branch: %w", err)
	}
	return b, nil
}

// ListBranches returns all active branches belonging to a tenant.
func (r *BranchRepo) ListBranches(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) ([]pub.Branch, error) {
	const q = `
		SELECT id, tenant_id, name, slug, ownership_type, operation_type, supply_rules,
		       phone, address, city, district, postal_code,
		       iban, legal_name, identity_type, tax_no, tax_office, is_active
		FROM branches
		WHERE tenant_id = $1 AND is_active = true
		ORDER BY name`

	rows, err := tx.Query(ctx, q, tenantID)
	if err != nil {
		return nil, fmt.Errorf("tenant/repo: list branches: %w", err)
	}
	defer rows.Close()

	var branches []pub.Branch
	for rows.Next() {
		b, err := scanBranch(rows)
		if err != nil {
			return nil, fmt.Errorf("tenant/repo: scan branch: %w", err)
		}
		branches = append(branches, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("tenant/repo: list branches rows: %w", err)
	}
	if branches == nil {
		branches = []pub.Branch{}
	}
	return branches, nil
}

// CreateBranch inserts a new branch and returns the persisted record.
func (r *BranchRepo) CreateBranch(ctx context.Context, tx pgx.Tx, b pub.Branch) (pub.Branch, error) {
	supplyJSON, err := json.Marshal(b.SupplyRules)
	if err != nil {
		return pub.Branch{}, fmt.Errorf("tenant/repo: marshal supply_rules: %w", err)
	}

	const q = `
		INSERT INTO branches (
			tenant_id, name, slug, ownership_type, operation_type, supply_rules,
			phone, address, city, district, postal_code,
			iban, legal_name, identity_type, tax_no, tax_office, is_active
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10, $11,
			$12, $13, $14, $15, $16, $17
		)
		RETURNING id, tenant_id, name, slug, ownership_type, operation_type, supply_rules,
		          phone, address, city, district, postal_code,
		          iban, legal_name, identity_type, tax_no, tax_office, is_active`

	row := tx.QueryRow(ctx, q,
		b.TenantID, b.Name, b.Slug, string(b.OwnershipType), string(b.OperationType), string(supplyJSON),
		b.Phone, b.Address.Line1, b.Address.City, b.Address.District, b.Address.PostalCode,
		b.IBAN, b.LegalName, string(b.IdentityType), b.TaxNo, b.TaxOffice, b.IsActive,
	)

	created, err := scanBranch(row)
	if err != nil {
		return pub.Branch{}, fmt.Errorf("tenant/repo: create branch: %w", err)
	}
	return created, nil
}

// UpdateBranch persists changes to mutable branch fields.
func (r *BranchRepo) UpdateBranch(ctx context.Context, tx pgx.Tx, b pub.Branch) (pub.Branch, error) {
	supplyJSON, err := json.Marshal(b.SupplyRules)
	if err != nil {
		return pub.Branch{}, fmt.Errorf("tenant/repo: marshal supply_rules: %w", err)
	}

	const q = `
		UPDATE branches SET
			name = $1, slug = $2, ownership_type = $3, operation_type = $4,
			supply_rules = $5, phone = $6, address = $7, city = $8,
			district = $9, postal_code = $10, iban = $11, legal_name = $12,
			identity_type = $13, tax_no = $14, tax_office = $15, is_active = $16,
			updated_at = NOW()
		WHERE tenant_id = $17 AND id = $18
		RETURNING id, tenant_id, name, slug, ownership_type, operation_type, supply_rules,
		          phone, address, city, district, postal_code,
		          iban, legal_name, identity_type, tax_no, tax_office, is_active`

	row := tx.QueryRow(ctx, q,
		b.Name, b.Slug, string(b.OwnershipType), string(b.OperationType),
		string(supplyJSON), b.Phone, b.Address.Line1, b.Address.City,
		b.Address.District, b.Address.PostalCode, b.IBAN, b.LegalName,
		string(b.IdentityType), b.TaxNo, b.TaxOffice, b.IsActive,
		b.TenantID, b.ID,
	)

	updated, err := scanBranch(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return pub.Branch{}, pub.ErrNotFound
		}
		return pub.Branch{}, fmt.Errorf("tenant/repo: update branch: %w", err)
	}
	return updated, nil
}

// rowScanner abstracts over pgx.Row and pgx.Rows so scanBranch works for both.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanBranch(row rowScanner) (pub.Branch, error) {
	var (
		b                              pub.Branch
		supplyRaw                      []byte
		ownershipType, operationType   string
		identityType                   string
		addressLine1                   string
		city, district, postalCode     string
	)

	err := row.Scan(
		&b.ID, &b.TenantID, &b.Name, &b.Slug, &ownershipType, &operationType, &supplyRaw,
		&b.Phone, &addressLine1, &city, &district, &postalCode,
		&b.IBAN, &b.LegalName, &identityType, &b.TaxNo, &b.TaxOffice, &b.IsActive,
	)
	if err != nil {
		return pub.Branch{}, err
	}

	b.OwnershipType = pub.OwnershipType(ownershipType)
	b.OperationType = pub.OperationType(operationType)
	b.IdentityType = pub.IdentityType(identityType)
	b.Address = pub.Address{
		Line1:      addressLine1,
		City:       city,
		District:   district,
		PostalCode: postalCode,
	}

	if err := json.Unmarshal(supplyRaw, &b.SupplyRules); err != nil {
		return pub.Branch{}, fmt.Errorf("unmarshal supply_rules: %w", err)
	}
	if b.SupplyRules == nil {
		b.SupplyRules = []pub.SupplyRule{}
	}

	return b, nil
}
