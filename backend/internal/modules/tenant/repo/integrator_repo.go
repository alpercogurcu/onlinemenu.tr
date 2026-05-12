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

// IntegratorRepo provides data access for the billing_integrators table.
type IntegratorRepo struct{}

// NewIntegratorRepo constructs an IntegratorRepo for fx injection.
func NewIntegratorRepo() *IntegratorRepo {
	return &IntegratorRepo{}
}

// ListIntegrators returns all non-deleted integrators for a tenant.
func (r *IntegratorRepo) ListIntegrators(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) ([]pub.BillingIntegrator, error) {
	const q = `
		SELECT id, tenant_id, branch_id, provider, display_name, config,
		       vault_secret_path, efatura_alias, environment, is_active, created_at
		FROM billing_integrators
		WHERE tenant_id = $1 AND deleted_at IS NULL
		ORDER BY created_at`

	rows, err := tx.Query(ctx, q, tenantID)
	if err != nil {
		return nil, fmt.Errorf("tenant/repo: list integrators: %w", err)
	}
	defer rows.Close()

	var integrators []pub.BillingIntegrator
	for rows.Next() {
		i, err := scanIntegrator(rows)
		if err != nil {
			return nil, fmt.Errorf("tenant/repo: scan integrator: %w", err)
		}
		integrators = append(integrators, i)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("tenant/repo: list integrators rows: %w", err)
	}
	if integrators == nil {
		integrators = []pub.BillingIntegrator{}
	}
	return integrators, nil
}

// GetIntegrator fetches a single non-deleted integrator by tenant and integrator IDs.
func (r *IntegratorRepo) GetIntegrator(ctx context.Context, tx pgx.Tx, tenantID, integratorID uuid.UUID) (pub.BillingIntegrator, error) {
	const q = `
		SELECT id, tenant_id, branch_id, provider, display_name, config,
		       vault_secret_path, efatura_alias, environment, is_active, created_at
		FROM billing_integrators
		WHERE tenant_id = $1 AND id = $2 AND deleted_at IS NULL`

	row := tx.QueryRow(ctx, q, tenantID, integratorID)
	i, err := scanIntegrator(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return pub.BillingIntegrator{}, pub.ErrNotFound
		}
		return pub.BillingIntegrator{}, fmt.Errorf("tenant/repo: get integrator: %w", err)
	}
	return i, nil
}

// GetEffectiveIntegrator returns the branch-level integrator when one exists; falls back to
// the tenant-wide record (branch_id IS NULL) for the same provider.
func (r *IntegratorRepo) GetEffectiveIntegrator(
	ctx context.Context, tx pgx.Tx,
	tenantID, branchID uuid.UUID,
	provider pub.BillingProvider,
) (pub.BillingIntegrator, error) {
	// Order by NULLS LAST so branch-specific rows sort before the tenant default.
	const q = `
		SELECT id, tenant_id, branch_id, provider, display_name, config,
		       vault_secret_path, efatura_alias, environment, is_active, created_at
		FROM billing_integrators
		WHERE tenant_id = $1
		  AND provider = $2
		  AND is_active = true
		  AND deleted_at IS NULL
		  AND (branch_id = $3 OR branch_id IS NULL)
		ORDER BY branch_id NULLS LAST
		LIMIT 1`

	row := tx.QueryRow(ctx, q, tenantID, string(provider), branchID)
	i, err := scanIntegrator(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return pub.BillingIntegrator{}, pub.ErrNotFound
		}
		return pub.BillingIntegrator{}, fmt.Errorf("tenant/repo: get effective integrator: %w", err)
	}
	return i, nil
}

// CreateIntegrator inserts a new billing integrator record.
func (r *IntegratorRepo) CreateIntegrator(ctx context.Context, tx pgx.Tx, i pub.BillingIntegrator) (pub.BillingIntegrator, error) {
	configJSON, err := json.Marshal(i.Config)
	if err != nil {
		return pub.BillingIntegrator{}, fmt.Errorf("tenant/repo: marshal integrator config: %w", err)
	}

	const q = `
		INSERT INTO billing_integrators (
			tenant_id, branch_id, provider, display_name, config,
			vault_secret_path, efatura_alias, environment, is_active
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, tenant_id, branch_id, provider, display_name, config,
		          vault_secret_path, efatura_alias, environment, is_active, created_at`

	row := tx.QueryRow(ctx, q,
		i.TenantID, i.BranchID, string(i.Provider), i.DisplayName, string(configJSON),
		i.VaultSecretPath, i.EfaturaAlias, string(i.Environment), i.IsActive,
	)

	created, err := scanIntegrator(row)
	if err != nil {
		return pub.BillingIntegrator{}, fmt.Errorf("tenant/repo: create integrator: %w", err)
	}
	return created, nil
}

// UpdateIntegrator persists changes to a billing integrator.
func (r *IntegratorRepo) UpdateIntegrator(ctx context.Context, tx pgx.Tx, i pub.BillingIntegrator) (pub.BillingIntegrator, error) {
	configJSON, err := json.Marshal(i.Config)
	if err != nil {
		return pub.BillingIntegrator{}, fmt.Errorf("tenant/repo: marshal integrator config: %w", err)
	}

	const q = `
		UPDATE billing_integrators SET
			branch_id = $1, provider = $2, display_name = $3, config = $4,
			vault_secret_path = $5, efatura_alias = $6, environment = $7,
			is_active = $8, updated_at = NOW()
		WHERE tenant_id = $9 AND id = $10 AND deleted_at IS NULL
		RETURNING id, tenant_id, branch_id, provider, display_name, config,
		          vault_secret_path, efatura_alias, environment, is_active, created_at`

	row := tx.QueryRow(ctx, q,
		i.BranchID, string(i.Provider), i.DisplayName, string(configJSON),
		i.VaultSecretPath, i.EfaturaAlias, string(i.Environment),
		i.IsActive, i.TenantID, i.ID,
	)

	updated, err := scanIntegrator(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return pub.BillingIntegrator{}, pub.ErrNotFound
		}
		return pub.BillingIntegrator{}, fmt.Errorf("tenant/repo: update integrator: %w", err)
	}
	return updated, nil
}

// DeleteIntegrator soft-deletes a billing integrator.
func (r *IntegratorRepo) DeleteIntegrator(ctx context.Context, tx pgx.Tx, tenantID, integratorID uuid.UUID) error {
	const q = `
		UPDATE billing_integrators
		SET deleted_at = NOW(), updated_at = NOW()
		WHERE tenant_id = $1 AND id = $2 AND deleted_at IS NULL`

	ct, err := tx.Exec(ctx, q, tenantID, integratorID)
	if err != nil {
		return fmt.Errorf("tenant/repo: delete integrator: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return pub.ErrNotFound
	}
	return nil
}

func scanIntegrator(row rowScanner) (pub.BillingIntegrator, error) {
	var (
		i           pub.BillingIntegrator
		branchID    *uuid.UUID
		provider    string
		environment string
		configRaw   []byte
	)

	err := row.Scan(
		&i.ID, &i.TenantID, &branchID, &provider, &i.DisplayName, &configRaw,
		&i.VaultSecretPath, &i.EfaturaAlias, &environment, &i.IsActive, &i.CreatedAt,
	)
	if err != nil {
		return pub.BillingIntegrator{}, err
	}

	i.BranchID = branchID
	i.Provider = pub.BillingProvider(provider)
	i.Environment = pub.IntegratorEnvironment(environment)

	if err := json.Unmarshal(configRaw, &i.Config); err != nil {
		return pub.BillingIntegrator{}, fmt.Errorf("unmarshal integrator config: %w", err)
	}
	if i.Config == nil {
		i.Config = map[string]any{}
	}

	return i, nil
}
