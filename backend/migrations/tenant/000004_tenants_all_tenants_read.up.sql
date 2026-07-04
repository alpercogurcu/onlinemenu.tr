-- Migration: tenant/000004_tenants_all_tenants_read
--
-- Adds an explicit platform-scope SELECT branch to tenants, mirroring
-- identity/000008: sessions opened via platform/db.WithAllTenantsReadTx
-- (SET LOCAL app.tenant_scope = 'all_tenants') may read all tenant rows.
--
-- Why: identity's ListContextsForPerson joins tenants to resolve tenant
-- names for the login context picker. After 000008 removed the uuid.Nil
-- sentinel, cross-tenant membership rows resolved but tenant names came
-- back empty because tenants had no all_tenants branch. Read-only: the
-- write policy is unchanged and has no platform-scope branch.

CREATE POLICY tenants_all_scope_read ON tenants FOR SELECT TO app_runtime
    USING (current_setting('app.tenant_scope', TRUE) = 'all_tenants');
