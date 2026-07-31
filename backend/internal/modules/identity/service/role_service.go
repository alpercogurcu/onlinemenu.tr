package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/identity/domain"
	pub "onlinemenu.tr/internal/modules/identity/public"
	"onlinemenu.tr/internal/modules/identity/repo"
	"onlinemenu.tr/internal/platform/db"
)

const permCacheTTL = 60 * time.Second

// permCacheEntry is the serialisable form stored in Redis.
// domain.PermSet has unexported fields and cannot be marshalled directly.
type permCacheEntry struct {
	Permissions   []domain.Permission  `json:"permissions"`
	FieldPolicies []domain.FieldPolicy `json:"field_policies"`
}

// RoleService manages roles and resolves permission sets for a tenant.
type RoleService struct {
	db             *db.Pool
	roleRepo       *repo.RoleRepo
	permRepo       *repo.PermissionRepo
	membershipRepo *repo.MembershipRepo
	cache          *redis.Client
	logger         *zap.Logger
}

// RoleParams groups the fx-injected dependencies for NewRoleService.
type RoleParams struct {
	fx.In

	DB             *db.Pool
	RoleRepo       *repo.RoleRepo
	PermRepo       *repo.PermissionRepo
	MembershipRepo *repo.MembershipRepo
	Cache          *redis.Client
	Logger         *zap.Logger
}

// NewRoleService constructs a RoleService for fx injection.
func NewRoleService(p RoleParams) *RoleService {
	return &RoleService{
		db:             p.DB,
		roleRepo:       p.RoleRepo,
		permRepo:       p.PermRepo,
		membershipRepo: p.MembershipRepo,
		cache:          p.Cache,
		logger:         p.Logger,
	}
}

// ListForTenant returns all roles that are visible to a tenant: system roles and
// the tenant's own custom roles.
func (s *RoleService) ListForTenant(ctx context.Context, tenantID uuid.UUID) ([]domain.Role, error) {
	var roles []domain.Role
	err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		roles, err = s.roleRepo.ListForTenant(ctx, tx, tenantID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("identity/service/role: list for tenant: %w", err)
	}
	return roles, nil
}

// CreateTenantRole inserts a custom tenant-wide role. branchScoped marks the role
// as grantable only at a concrete branch (ADR-SEC-005); until this parameter
// existed every custom role was born chain-wide.
func (s *RoleService) CreateTenantRole(ctx context.Context, tenantID uuid.UUID, name string, branchScoped bool) (domain.Role, error) {
	if name == "" {
		return domain.Role{}, pub.ErrInvalid
	}
	r := domain.Role{
		TenantID:     &tenantID,
		Name:         name,
		BranchScoped: branchScoped,
	}
	var created domain.Role
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		created, err = s.roleRepo.Create(ctx, tx, r)
		return err
	})
	if err != nil {
		return domain.Role{}, fmt.Errorf("identity/service/role: create tenant role: %w", err)
	}
	return created, nil
}

// CreateBranchRole inserts a custom role owned by a single branch.
// branchScoped is accepted for symmetry but cannot lower the guarantee:
// RoleRepo.Create persists role.RequiresBranch(), which is already true for a
// branch-owned role.
func (s *RoleService) CreateBranchRole(ctx context.Context, tenantID, branchID uuid.UUID, name string, branchScoped bool) (domain.Role, error) {
	if name == "" {
		return domain.Role{}, pub.ErrInvalid
	}
	r := domain.Role{
		TenantID:     &tenantID,
		BranchID:     &branchID,
		Name:         name,
		BranchScoped: branchScoped,
	}
	var created domain.Role
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		created, err = s.roleRepo.Create(ctx, tx, r)
		return err
	})
	if err != nil {
		return domain.Role{}, fmt.Errorf("identity/service/role: create branch role: %w", err)
	}
	return created, nil
}

// Update rewrites a custom role's name and branch_scoped flag (PUT semantics:
// both fields are replaced). System roles are immutable templates.
//
// A FALSE -> TRUE transition is refused while chain-wide memberships of the role
// still exist. The memberships_branch_scope_guard trigger only fires on
// membership writes, so those rows would survive the flip as live chain-wide
// grants of a now branch-scoped role — the exact leak ADR-SEC-005 closes. The
// refusal carries the count (pub.BranchScopeConflictError, HTTP 409) so the
// admin can re-scope or terminate them and retry; silently flipping and logging
// a warning would leave the leak in place with nobody obliged to act on it.
//
// Read, count and write share one transaction: a concurrent CreateMembership
// between a separate count and update would slip through the check.
func (s *RoleService) Update(ctx context.Context, tenantID, roleID uuid.UUID, name string, branchScoped bool) (domain.Role, error) {
	if name == "" {
		return domain.Role{}, pub.ErrInvalid
	}

	var updated domain.Role
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		current, err := s.roleRepo.GetByID(ctx, tx, tenantID, roleID)
		if err != nil {
			return err
		}
		if current.IsSystem {
			return pub.ErrInvalid
		}

		next := current
		next.Name = name
		next.BranchScoped = branchScoped

		if next.RequiresBranch() && !current.RequiresBranch() {
			count, countErr := s.membershipRepo.CountChainWideForRole(ctx, tx, tenantID, roleID)
			if countErr != nil {
				return countErr
			}
			if count > 0 {
				return pub.BranchScopeConflictError{RoleID: roleID, ChainWideMemberships: count}
			}
		}

		updated, err = s.roleRepo.Update(ctx, tx, tenantID, next)
		return err
	})
	if err != nil {
		return domain.Role{}, wrapNotFound(err, "identity/service/role: update: %w")
	}
	return updated, nil
}

// Delete removes a custom role. System roles cannot be deleted.
func (s *RoleService) Delete(ctx context.Context, tenantID, roleID uuid.UUID) error {
	var role domain.Role
	if err := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		role, err = s.roleRepo.GetByID(ctx, tx, tenantID, roleID)
		return err
	}); err != nil {
		return wrapNotFound(err, "identity/service/role: delete — get role: %w")
	}
	if role.IsSystem {
		return pub.ErrInvalid
	}
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		return s.roleRepo.Delete(ctx, tx, tenantID, roleID)
	})
	if err != nil {
		return wrapNotFound(err, "identity/service/role: delete: %w")
	}
	return nil
}

// LoadPermSet builds the union PermSet for the given role IDs.
//
// All role cache keys are fetched in a single MGET pipeline. Misses are
// backfilled with one batched DB query and written back via a pipeline SET.
// Cache key per role: "authz:perms:{roleID}" (TTL 60s).
func (s *RoleService) LoadPermSet(ctx context.Context, roleIDs []uuid.UUID, tenantID uuid.UUID) (domain.PermSet, error) {
	if len(roleIDs) == 0 {
		return domain.NewPermSet(nil, nil), nil
	}

	keys := make([]string, len(roleIDs))
	for i, id := range roleIDs {
		keys[i] = fmt.Sprintf("authz:perms:%s", id)
	}

	vals, err := s.cache.MGet(ctx, keys...).Result()
	if err != nil {
		s.logger.Warn("identity/service/role: mget cache failed, fetching from db", zap.Error(err))
		vals = make([]interface{}, len(roleIDs))
	}

	hitEntries := make(map[uuid.UUID]permCacheEntry, len(roleIDs))
	var missIDs []uuid.UUID

	for i, val := range vals {
		if val == nil {
			missIDs = append(missIDs, roleIDs[i])
			continue
		}
		raw, ok := val.(string)
		if !ok {
			missIDs = append(missIDs, roleIDs[i])
			continue
		}
		var entry permCacheEntry
		if jsonErr := json.Unmarshal([]byte(raw), &entry); jsonErr != nil {
			s.logger.Warn("identity/service/role: corrupt cache entry, refetching",
				zap.String("role_id", roleIDs[i].String()),
			)
			missIDs = append(missIDs, roleIDs[i])
			continue
		}
		hitEntries[roleIDs[i]] = entry
	}

	if len(missIDs) > 0 {
		var allPerms []domain.Permission
		var allFields []domain.FieldPolicy
		if dbErr := s.db.WithTenantReadTx(ctx, tenantID, func(tx pgx.Tx) error {
			var err error
			allPerms, allFields, err = s.permRepo.LoadForRoles(ctx, tx, missIDs)
			return err
		}); dbErr != nil {
			return domain.PermSet{}, fmt.Errorf("identity/service/role: load perms for roles: %w", dbErr)
		}

		byRole := make(map[uuid.UUID]*permCacheEntry, len(missIDs))
		for _, id := range missIDs {
			byRole[id] = &permCacheEntry{}
		}
		for _, p := range allPerms {
			byRole[p.RoleID].Permissions = append(byRole[p.RoleID].Permissions, p)
		}
		for _, f := range allFields {
			byRole[f.RoleID].FieldPolicies = append(byRole[f.RoleID].FieldPolicies, f)
		}

		pipe := s.cache.Pipeline()
		for id, entry := range byRole {
			if entry.Permissions == nil && entry.FieldPolicies == nil {
				s.logger.Warn("identity/service/role: no permissions found for role",
					zap.String("role_id", id.String()),
				)
			}
			hitEntries[id] = *entry
			if encoded, marshalErr := json.Marshal(entry); marshalErr == nil {
				pipe.Set(ctx, fmt.Sprintf("authz:perms:%s", id), encoded, permCacheTTL)
			}
		}
		if _, pipeErr := pipe.Exec(ctx); pipeErr != nil {
			s.logger.Warn("identity/service/role: cache pipeline write failed", zap.Error(pipeErr))
		}
	}

	merged := domain.NewPermSet(nil, nil)
	for _, id := range roleIDs {
		entry := hitEntries[id]
		merged = merged.Merge(domain.NewPermSet(entry.Permissions, entry.FieldPolicies))
	}
	return merged, nil
}

// InvalidatePermCache removes the cached permission entry for a role.
// Called by the role.updated.v1 event subscriber.
func (s *RoleService) InvalidatePermCache(ctx context.Context, roleID uuid.UUID) error {
	key := fmt.Sprintf("authz:perms:%s", roleID)
	if err := s.cache.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("identity/service/role: invalidate perm cache: %w", err)
	}
	return nil
}
