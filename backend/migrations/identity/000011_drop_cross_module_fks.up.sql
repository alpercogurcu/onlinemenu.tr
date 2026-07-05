-- Migration: identity/000011_drop_cross_module_fks
-- Modül izolasyonu — cross-module FK yasak; bütünlük RLS + servis katmanı +
-- OPS-002 offboarding ile korunur (karar: task #14, 2026-07-05).
--
-- 000002 (roles) and 000003 (memberships) shipped with inline FK constraints
-- from identity's tables to the tenant module's tenants/branches tables.
-- That is a cross-module DB coupling forbidden by CLAUDE.md's module
-- isolation rule (see catalog/000002's rationale: migrations for each
-- module run independently, in their own schema_migrations_<module> chain,
-- so a cross-module FK is not guaranteed to be satisfiable in every
-- deployment order regardless of whether it happens to hold today).
--
-- Constraints dropped here (identity -> tenant, all cross-module):
--   roles.tenant_id             -> tenants(id)   ON DELETE CASCADE
--   roles.branch_id             -> branches(id)  ON DELETE CASCADE
--   memberships.tenant_id       -> tenants(id)   ON DELETE CASCADE
--   memberships.branch_id       -> branches(id)  ON DELETE CASCADE
--   role_permissions.tenant_id  -> tenants(id)   ON DELETE CASCADE
--   role_field_policies.tenant_id -> tenants(id) ON DELETE CASCADE
--
-- Constraints left untouched (identity -> identity, module-internal, correct):
--   memberships.person_id -> persons(id)
--   memberships.role_id   -> roles(id)
--   role_permissions.role_id -> roles(id)
--   role_field_policies.role_id -> roles(id)
--
-- Behavioral note: these were ON DELETE CASCADE, so deleting a tenant/branch
-- row previously cascaded into identity's roles/memberships automatically.
-- After this migration that cascade no longer happens at the DB level;
-- tenant offboarding (ADR-OPS-002) and any branch-deletion flow must
-- explicitly clean up (or the service layer must otherwise account for)
-- identity rows referencing the deleted tenant_id/branch_id. This is an
-- intentional trade-off of the module isolation rule, not an oversight.
--
-- 000011 keeps identity/000002-000005's original migration order/history
-- intact (immutability of shipped migrations) and only removes the
-- constraint objects; the deployment order note in
-- backend/scripts/verify_migrations.sh (tenant before identity) still
-- applies to fresh installs because 000002/000003 create the FK inline
-- before this migration drops it later in the same module's chain.

ALTER TABLE roles               DROP CONSTRAINT IF EXISTS roles_tenant_id_fkey;
ALTER TABLE roles               DROP CONSTRAINT IF EXISTS roles_branch_id_fkey;
ALTER TABLE memberships         DROP CONSTRAINT IF EXISTS memberships_tenant_id_fkey;
ALTER TABLE memberships         DROP CONSTRAINT IF EXISTS memberships_branch_id_fkey;
ALTER TABLE role_permissions    DROP CONSTRAINT IF EXISTS role_permissions_tenant_id_fkey;
ALTER TABLE role_field_policies DROP CONSTRAINT IF EXISTS role_field_policies_tenant_id_fkey;
