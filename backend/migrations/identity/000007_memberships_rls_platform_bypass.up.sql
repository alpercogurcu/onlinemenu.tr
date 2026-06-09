-- Migration: identity/000007_memberships_rls_platform_bypass
-- Adds a platform-admin bypass to the memberships SELECT policy, consistent
-- with the persons SELECT policy (added in 000003).
--
-- When app.tenant_id = uuid.Nil (00000000-...) the runtime role is acting as
-- a platform-level reader (e.g. ContextService.SelectContext listing all
-- active memberships across tenants). Without this bypass the cross-tenant
-- ListContextsForPerson query always returns empty rows.
--
-- The write policy is intentionally NOT modified: platform-level writes to
-- memberships must always go through the migrator role (BYPASSRLS).

DROP POLICY IF EXISTS memberships_read ON memberships;

CREATE POLICY memberships_read ON memberships FOR SELECT TO app_runtime
    USING (
        current_setting('app.tenant_id', TRUE) = '00000000-0000-0000-0000-000000000000'
        OR tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid
    );
