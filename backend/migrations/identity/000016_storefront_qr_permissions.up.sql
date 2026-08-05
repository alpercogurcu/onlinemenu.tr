-- Migration: identity/000016_storefront_qr_permissions
--
-- ADR-ARCH-006 (QR dine-in online sipariş) adds a staff surface that the
-- seeded permission dictionary has no vocabulary for: /api/v1/storefront/
-- qr-codes. Without these rows the endpoints are unreachable by anyone except
-- the manager wildcard, and permission_wiring_test.go cannot see the grant at
-- all.
--
-- Vocabulary mirrors the pos table plan (identity/000006 `tables` read/manage):
--   storefront_qr:read   -> cashier + shift_manager. Counter staff must see
--                           which table already has a live code before
--                           printing another sticker.
--   storefront_qr:manage -> shift_manager only. Minting/revoking/rotating
--                           hands out or burns a printed secret; the three
--                           verbs are one action on purpose, so no role can
--                           end up able to mint but not revoke.
--
-- `manager` is not listed: it holds the ('*','*') wildcard row from 000006 and
-- is covered by authz.rego's has_role("manager") rule.
--
-- Both statements are idempotent (ON CONFLICT DO NOTHING), so re-running after
-- a partial failure is safe.

-- 1. System template rows (tenant_id IS NULL).
--
-- Written as literal (role_id, NULL, resource, action) tuples on purpose, not
-- as a SELECT over roles.system_key: the permission-wiring guard
-- (internal/platform/auth/permission_wiring_test.go) parses these tuples
-- straight out of the migration SQL to learn which roles hold which pair. A
-- SELECT form would be invisible to it, and the grant would silently escape
-- the guard that exists to catch exactly that (same trap as 000014).
--
-- '...0001' is cashier, '...0002' is shift_manager (identity/000006).
INSERT INTO role_permissions (role_id, tenant_id, resource, action) VALUES
    ('00000001-0000-0000-0000-000000000001', NULL, 'storefront_qr', 'read'),
    ('00000001-0000-0000-0000-000000000002', NULL, 'storefront_qr', 'read'),
    ('00000001-0000-0000-0000-000000000002', NULL, 'storefront_qr', 'manage')
ON CONFLICT (role_id, resource, action) DO NOTHING;

-- 2. Backfill tenant clones.
--
-- Without this the grant is cosmetic for every tenant onboarded before today:
-- they already have cloned 'Kasiyer'/'Vardiya Sorumlusu' roles and
-- SeedTenantRoles never runs again for them. Same trap ADR-SEC-005 documents
-- ("Klonlama — bu satır olmadan düzeltme kozmetik kalır") and the same
-- template as identity/000014 step 2.
--
-- The join is by NAME, matching how the clones were created
-- (identity/events/subscriber.go: `nr.name = sr.name`). Caveat, inherited from
-- SEC-005: a clone that was RENAMED by the tenant is not matched here. The
-- permission-fingerprint query in ADR-SEC-005 § "Deploy öncesi denetim" finds
-- those; it must be run if any tenant is known to have renamed system roles.
--
-- The per-role VALUES lists differ (cashier gets read only), so this is two
-- statements rather than one CROSS JOIN over both system keys.
INSERT INTO role_permissions (role_id, tenant_id, resource, action)
SELECT nr.id, nr.tenant_id, v.resource, v.action
FROM roles sr
JOIN roles nr ON nr.tenant_id IS NOT NULL
             AND nr.branch_id IS NULL
             AND nr.name = sr.name
CROSS JOIN (VALUES ('storefront_qr', 'read')) AS v(resource, action)
WHERE sr.tenant_id IS NULL
  AND sr.system_key = 'cashier'
ON CONFLICT (role_id, resource, action) DO NOTHING;

INSERT INTO role_permissions (role_id, tenant_id, resource, action)
SELECT nr.id, nr.tenant_id, v.resource, v.action
FROM roles sr
JOIN roles nr ON nr.tenant_id IS NOT NULL
             AND nr.branch_id IS NULL
             AND nr.name = sr.name
CROSS JOIN (VALUES ('storefront_qr', 'read'), ('storefront_qr', 'manage')) AS v(resource, action)
WHERE sr.tenant_id IS NULL
  AND sr.system_key = 'shift_manager'
ON CONFLICT (role_id, resource, action) DO NOTHING;
