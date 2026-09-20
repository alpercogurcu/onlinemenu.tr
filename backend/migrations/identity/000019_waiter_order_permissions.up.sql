-- Migration: identity/000019_waiter_order_permissions
--
-- Product decision: a waiter (garson) takes orders at the table. Until now the
-- role held pos.table.read and nothing else (identity/000017), so a waiter could
-- see the floor plan but not open an adisyon, browse the catalog or place an
-- order.
--
-- New grants (template + every tenant clone):
--   checks:read     pos.check.read      list / read adisyons of the own branch
--   checks:create   pos.check.open      open an adisyon
--   orders:read     pos.order.read      read orders back
--   orders:create   pos.order.place     place an order
--   catalog:read    catalog.*.read      browse products, variants, menus
--
-- Deliberately NOT granted: checks:update / orders:update (close, cancel,
-- transfer, merge, accept, reject, advance), payment:*, shifts:*, reports:*,
-- storefront_qr:*, any catalog write. Those stay with the counter roles.
--
-- The rego half lives in configs/opa/bundles/authz.rego (pos_waiter_actions and
-- the waiter entry of catalog_read_actions). Both halves ship together: a seed
-- without the rule is a dead grant (ADR-SEC-005), a rule without the seed makes
-- the role's role_permissions lie about what OPA lets it do.
-- permission_wiring_test.go classifies these pairs already (cashier holds them
-- too) and its waiter test proves every seeded pair is really allowed.
--
-- Template rows are literal (role_id, NULL, resource, action) tuples so the
-- wiring guard can parse them (same requirement as 000014/000016/000017).
-- Idempotent throughout.

-- ============================================================
-- 1. Template permissions
-- ============================================================
INSERT INTO role_permissions (role_id, tenant_id, resource, action) VALUES
    ('00000001-0000-0000-0000-000000000008', NULL, 'checks',  'read'),
    ('00000001-0000-0000-0000-000000000008', NULL, 'checks',  'create'),
    ('00000001-0000-0000-0000-000000000008', NULL, 'orders',  'read'),
    ('00000001-0000-0000-0000-000000000008', NULL, 'orders',  'create'),
    ('00000001-0000-0000-0000-000000000008', NULL, 'catalog', 'read')
ON CONFLICT (role_id, resource, action) DO NOTHING;

-- ============================================================
-- 2. Backfill: copy the template's grants onto existing tenant clones
-- ============================================================
-- Without this the seed is cosmetic for every tenant onboarded before today:
-- subscriber.go clones permissions only on tenant.created. Clones are matched
-- by NAME (subscriber.go: nr.name = sr.name), exactly as 000017 step 4 does; a
-- clone the tenant RENAMED is not matched — same documented limitation. The
-- source is the template's own rows, so this statement cannot drift from
-- step 1.
INSERT INTO role_permissions (role_id, tenant_id, resource, action)
SELECT nr.id, nr.tenant_id, rp.resource, rp.action
FROM roles sr
JOIN role_permissions rp ON rp.role_id = sr.id
JOIN roles nr ON nr.tenant_id IS NOT NULL
             AND nr.branch_id IS NULL
             AND nr.name = sr.name
WHERE sr.tenant_id IS NULL
  AND sr.system_key = 'waiter'
ON CONFLICT (role_id, resource, action) DO NOTHING;
