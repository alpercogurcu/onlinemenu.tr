package repo

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"onlinemenu.tr/internal/modules/identity/domain"
)

// PermissionRepo provides data access for role_permissions and role_field_policies.
type PermissionRepo struct{}

// NewPermissionRepo constructs a PermissionRepo for fx injection.
func NewPermissionRepo() *PermissionRepo {
	return &PermissionRepo{}
}

// LoadForRoles fetches all permissions and field policies for the given role IDs.
// System role rows (tenant_id IS NULL) are visible via RLS policy without additional filtering.
func (r *PermissionRepo) LoadForRoles(ctx context.Context, tx pgx.Tx, roleIDs []uuid.UUID) ([]domain.Permission, []domain.FieldPolicy, error) {
	perms, err := r.loadPermissions(ctx, tx, roleIDs)
	if err != nil {
		return nil, nil, err
	}
	policies, err := r.loadFieldPolicies(ctx, tx, roleIDs)
	if err != nil {
		return nil, nil, err
	}
	return perms, policies, nil
}

// UpsertPermission inserts a permission row; if the (role_id, resource, action) combination
// already exists the row is left unchanged (idempotent).
// tenant_id is derived from the parent role row so RLS is consistent.
func (r *PermissionRepo) UpsertPermission(ctx context.Context, tx pgx.Tx, p domain.Permission) error {
	const q = `
		INSERT INTO role_permissions (role_id, tenant_id, resource, action)
		SELECT $1, r.tenant_id, $2, $3
		FROM roles r
		WHERE r.id = $1
		ON CONFLICT (role_id, resource, action) DO NOTHING`

	if _, err := tx.Exec(ctx, q, p.RoleID, p.Resource, p.Action); err != nil {
		return fmt.Errorf("identity/repo/permission: upsert permission: %w", err)
	}
	return nil
}

// DeletePermission removes a specific (role_id, resource, action) permission row.
func (r *PermissionRepo) DeletePermission(ctx context.Context, tx pgx.Tx, roleID uuid.UUID, resource, action string) error {
	const q = `DELETE FROM role_permissions WHERE role_id = $1 AND resource = $2 AND action = $3`
	if _, err := tx.Exec(ctx, q, roleID, resource, action); err != nil {
		return fmt.Errorf("identity/repo/permission: delete permission: %w", err)
	}
	return nil
}

// UpsertFieldPolicy inserts a field policy row; if the (role_id, resource, field) combination
// already exists the row is left unchanged (idempotent).
// tenant_id is derived from the parent role row so RLS is consistent.
func (r *PermissionRepo) UpsertFieldPolicy(ctx context.Context, tx pgx.Tx, fp domain.FieldPolicy) error {
	const q = `
		INSERT INTO role_field_policies (role_id, tenant_id, resource, field)
		SELECT $1, r.tenant_id, $2, $3
		FROM roles r
		WHERE r.id = $1
		ON CONFLICT (role_id, resource, field) DO NOTHING`

	if _, err := tx.Exec(ctx, q, fp.RoleID, fp.Resource, fp.Field); err != nil {
		return fmt.Errorf("identity/repo/permission: upsert field policy: %w", err)
	}
	return nil
}

// DeleteFieldPolicy removes a specific (role_id, resource, field) field policy row.
func (r *PermissionRepo) DeleteFieldPolicy(ctx context.Context, tx pgx.Tx, roleID uuid.UUID, resource, field string) error {
	const q = `DELETE FROM role_field_policies WHERE role_id = $1 AND resource = $2 AND field = $3`
	if _, err := tx.Exec(ctx, q, roleID, resource, field); err != nil {
		return fmt.Errorf("identity/repo/permission: delete field policy: %w", err)
	}
	return nil
}

func (r *PermissionRepo) loadPermissions(ctx context.Context, tx pgx.Tx, roleIDs []uuid.UUID) ([]domain.Permission, error) {
	if len(roleIDs) == 0 {
		return nil, nil
	}
	q := fmt.Sprintf(`
		SELECT role_id, resource, action
		FROM role_permissions
		WHERE role_id IN (%s)`, uuidInClause(len(roleIDs)))

	rows, err := tx.Query(ctx, q, uuidArgs(roleIDs)...)
	if err != nil {
		return nil, fmt.Errorf("identity/repo/permission: load permissions: %w", err)
	}
	defer rows.Close()

	var perms []domain.Permission
	for rows.Next() {
		var p domain.Permission
		if err := rows.Scan(&p.RoleID, &p.Resource, &p.Action); err != nil {
			return nil, fmt.Errorf("identity/repo/permission: scan permission: %w", err)
		}
		perms = append(perms, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("identity/repo/permission: permissions rows: %w", err)
	}
	return perms, nil
}

func (r *PermissionRepo) loadFieldPolicies(ctx context.Context, tx pgx.Tx, roleIDs []uuid.UUID) ([]domain.FieldPolicy, error) {
	if len(roleIDs) == 0 {
		return nil, nil
	}
	q := fmt.Sprintf(`
		SELECT role_id, resource, field
		FROM role_field_policies
		WHERE role_id IN (%s)`, uuidInClause(len(roleIDs)))

	rows, err := tx.Query(ctx, q, uuidArgs(roleIDs)...)
	if err != nil {
		return nil, fmt.Errorf("identity/repo/permission: load field policies: %w", err)
	}
	defer rows.Close()

	var policies []domain.FieldPolicy
	for rows.Next() {
		var fp domain.FieldPolicy
		if err := rows.Scan(&fp.RoleID, &fp.Resource, &fp.Field); err != nil {
			return nil, fmt.Errorf("identity/repo/permission: scan field policy: %w", err)
		}
		policies = append(policies, fp)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("identity/repo/permission: field policies rows: %w", err)
	}
	return policies, nil
}

// uuidInClause returns "$1,$2,...$n" for use in an IN (...) expression.
// pgBouncer simple-protocol mode can't encode []uuid.UUID for ANY($1),
// but individual UUID parameters work correctly.
func uuidInClause(n int) string {
	placeholders := make([]string, n)
	for i := range n {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
	}
	return strings.Join(placeholders, ",")
}

func uuidArgs(ids []uuid.UUID) []any {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return args
}
