-- Migration: identity/000007_memberships_rls_platform_bypass (rollback)
-- Restores memberships_read to its pre-000007 definition (000003's original:
-- tenant-scoped only, no platform-admin bypass branch).
--
-- By the time this runs in the down chain, 000008's down has already
-- restored memberships_read to exactly the shape this migration's up
-- created (uuid.Nil sentinel bypass branch), so this is a plain reversal of
-- that shape back to 000003's original policy body.

DROP POLICY IF EXISTS memberships_read ON memberships;

CREATE POLICY memberships_read ON memberships FOR SELECT TO app_runtime
    USING  (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);
