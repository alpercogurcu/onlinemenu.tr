-- Migration: identity/000009_roles_all_tenants_read
--
-- Adds the explicit platform-scope SELECT branch to roles, mirroring 000008
-- (persons/memberships) and tenant/000004 (tenants): sessions opened via
-- platform/db.WithAllTenantsReadTx may read custom (tenant-scoped) roles of
-- any tenant. Needed so ListContextsForPerson can resolve custom role names
-- for the cross-tenant login context picker. System roles (tenant_id IS NULL)
-- were already globally readable. Read-only: roles_write is unchanged.

CREATE POLICY roles_all_scope_read ON roles FOR SELECT TO app_runtime
    USING (current_setting('app.tenant_scope', TRUE) = 'all_tenants');
