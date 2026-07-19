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

// RoleRepo provides data access for the roles table.
type RoleRepo struct{}

// NewRoleRepo constructs a RoleRepo for fx injection.
func NewRoleRepo() *RoleRepo {
	return &RoleRepo{}
}

// GetByID fetches a single role by primary key.
// tenantID is used as a defense-in-depth WHERE clause; system roles (tenant_id IS NULL) are
// returned when tenantID is uuid.Nil or when the role's tenant_id matches.
func (r *RoleRepo) GetByID(ctx context.Context, tx pgx.Tx, tenantID, roleID uuid.UUID) (domain.Role, error) {
	const q = `
		SELECT id, tenant_id, branch_id, name, COALESCE(system_key, ''), is_system, branch_scoped, created_at
		FROM roles
		WHERE id = $1
		  AND (tenant_id IS NULL OR tenant_id = $2)`

	row := tx.QueryRow(ctx, q, roleID, tenantID)
	role, err := scanRole(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Role{}, pub.ErrNotFound
		}
		return domain.Role{}, fmt.Errorf("identity/repo/role: get by id: %w", err)
	}
	return role, nil
}

// ListForTenant returns all system roles plus the tenant's custom chain-wide roles.
func (r *RoleRepo) ListForTenant(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) ([]domain.Role, error) {
	const q = `
		SELECT id, tenant_id, branch_id, name, COALESCE(system_key, ''), is_system, branch_scoped, created_at
		FROM roles
		WHERE tenant_id IS NULL OR (tenant_id = $1 AND branch_id IS NULL)
		ORDER BY is_system DESC, name`

	rows, err := tx.Query(ctx, q, tenantID)
	if err != nil {
		return nil, fmt.Errorf("identity/repo/role: list for tenant: %w", err)
	}
	defer rows.Close()
	return collectRoles(rows)
}

// ListForBranch returns all system roles, the tenant's chain-wide roles, and branch-specific roles.
func (r *RoleRepo) ListForBranch(ctx context.Context, tx pgx.Tx, tenantID, branchID uuid.UUID) ([]domain.Role, error) {
	const q = `
		SELECT id, tenant_id, branch_id, name, COALESCE(system_key, ''), is_system, branch_scoped, created_at
		FROM roles
		WHERE tenant_id IS NULL
		   OR (tenant_id = $1 AND branch_id IS NULL)
		   OR (tenant_id = $1 AND branch_id = $2)
		ORDER BY is_system DESC, name`

	rows, err := tx.Query(ctx, q, tenantID, branchID)
	if err != nil {
		return nil, fmt.Errorf("identity/repo/role: list for branch: %w", err)
	}
	defer rows.Close()
	return collectRoles(rows)
}

// Create inserts a new custom role and returns the persisted record.
func (r *RoleRepo) Create(ctx context.Context, tx pgx.Tx, role domain.Role) (domain.Role, error) {
	const q = `
		INSERT INTO roles (tenant_id, branch_id, name, system_key, is_system, branch_scoped)
		VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6)
		RETURNING id, tenant_id, branch_id, name, COALESCE(system_key, ''), is_system, branch_scoped, created_at`

	row := tx.QueryRow(ctx, q, role.TenantID, role.BranchID, role.Name, role.SystemKey, role.IsSystem, role.RequiresBranch())
	created, err := scanRole(row)
	if err != nil {
		return domain.Role{}, fmt.Errorf("identity/repo/role: create: %w", err)
	}
	return created, nil
}

// Delete removes a custom role. Returns pub.ErrNotFound when the role does not exist,
// and a descriptive error when the caller attempts to delete a system role.
func (r *RoleRepo) Delete(ctx context.Context, tx pgx.Tx, tenantID, roleID uuid.UUID) error {
	const checkQ = `SELECT is_system FROM roles WHERE id = $1 AND tenant_id = $2`

	var isSystem bool
	if err := tx.QueryRow(ctx, checkQ, roleID, tenantID).Scan(&isSystem); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return pub.ErrNotFound
		}
		return fmt.Errorf("identity/repo/role: delete check: %w", err)
	}
	if isSystem {
		return fmt.Errorf("identity/repo/role: delete: system roles cannot be deleted")
	}

	const deleteQ = `DELETE FROM roles WHERE id = $1 AND tenant_id = $2`
	ct, err := tx.Exec(ctx, deleteQ, roleID, tenantID)
	if err != nil {
		return fmt.Errorf("identity/repo/role: delete: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return pub.ErrNotFound
	}
	return nil
}

func scanRole(row pgx.Row) (domain.Role, error) {
	var (
		role      domain.Role
		createdAt time.Time
	)
	err := row.Scan(
		&role.ID, &role.TenantID, &role.BranchID,
		&role.Name, &role.SystemKey, &role.IsSystem, &role.BranchScoped,
		&createdAt,
	)
	if err != nil {
		return domain.Role{}, err
	}
	role.CreatedAt = createdAt
	return role, nil
}

func collectRoles(rows pgx.Rows) ([]domain.Role, error) {
	var roles []domain.Role
	for rows.Next() {
		var (
			role      domain.Role
			createdAt time.Time
		)
		if err := rows.Scan(
			&role.ID, &role.TenantID, &role.BranchID,
			&role.Name, &role.SystemKey, &role.IsSystem, &role.BranchScoped,
			&createdAt,
		); err != nil {
			return nil, fmt.Errorf("identity/repo/role: scan: %w", err)
		}
		role.CreatedAt = createdAt
		roles = append(roles, role)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("identity/repo/role: rows: %w", err)
	}
	return roles, nil
}
