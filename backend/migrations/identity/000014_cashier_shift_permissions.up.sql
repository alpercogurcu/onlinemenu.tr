-- Migration: identity/000014_cashier_shift_permissions
--
-- Problem: 000006 seeds `shifts` create/update to shift_manager only; cashier
-- holds `shifts:read` alone. With ADR-DATA-008's cash session wired to that
-- vocabulary (payment/000007), a cashier can SEE the open session but cannot
-- open one, record a cash in/out, submit a closing count, or close it.
--
-- That does not match how a till works: the cashier counts their own drawer.
-- Requiring a shift_manager for every open/close makes the feature unusable in
-- a single-till restaurant, which is exactly the pilot shape ADR-DATA-008
-- commits to.
--
-- Fix: grant cashier `shifts:create` and `shifts:update`.
--
-- Manager approval of a NON-ZERO difference (açık/fazla) is a separate control
-- and is deliberately NOT introduced here — that belongs to the unwired
-- `checks:approve` vocabulary and needs its own design. Today the audit trail
-- (who opened, who counted, who closed, every movement with actor+timestamp)
-- is the control; four-eyes on the difference is a follow-up.
--
-- Both statements below are idempotent (ON CONFLICT DO NOTHING), so re-running
-- after a partial failure is safe.

-- 1. System template rows (tenant_id IS NULL).
--
-- Written as literal (role_id, NULL, resource, action) tuples on purpose, not
-- as a SELECT over roles.system_key: the permission-wiring guard
-- (internal/platform/auth/permission_wiring_test.go) parses these tuples
-- straight out of the migration SQL to learn which roles hold which pair. A
-- SELECT form would be invisible to it, and the grant would silently escape
-- the guard that exists to catch exactly that.
--
-- '...0001' is the cashier system role (identity/000006).
INSERT INTO role_permissions (role_id, tenant_id, resource, action) VALUES
    ('00000001-0000-0000-0000-000000000001', NULL, 'shifts', 'create'),
    ('00000001-0000-0000-0000-000000000001', NULL, 'shifts', 'update')
ON CONFLICT (role_id, resource, action) DO NOTHING;

-- 2. Backfill tenant clones.
--
-- Without this the fix is cosmetic: every tenant onboarded before today already
-- has a cloned 'Kasiyer' role and SeedTenantRoles never runs again for them.
-- This is the same trap ADR-SEC-005 documents ("Klonlama — bu satır olmadan
-- düzeltme kozmetik kalır").
--
-- The join is by NAME, matching how the clones were created in the first place
-- (identity/events/subscriber.go: `nr.name = sr.name`). Caveat, inherited from
-- SEC-005: a clone that was RENAMED by the tenant is not matched here. The
-- permission-fingerprint query in ADR-SEC-005 § "Deploy öncesi denetim" finds
-- those; it must be run if any tenant is known to have renamed system roles.
INSERT INTO role_permissions (role_id, tenant_id, resource, action)
SELECT nr.id, nr.tenant_id, v.resource, v.action
FROM roles sr
JOIN roles nr ON nr.tenant_id IS NOT NULL
             AND nr.branch_id IS NULL
             AND nr.name = sr.name
CROSS JOIN (VALUES ('shifts', 'create'), ('shifts', 'update')) AS v(resource, action)
WHERE sr.tenant_id IS NULL
  AND sr.system_key = 'cashier'
ON CONFLICT (role_id, resource, action) DO NOTHING;
