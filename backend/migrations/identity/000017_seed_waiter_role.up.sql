-- Migration: identity/000017_seed_waiter_role
--
-- Seeds the "waiter" (garson) system role. The role UUID is forward-declared
-- in configs/opa/bundles/authz.rego ("waiter": 00000001-...-0008) and the
-- pos_table_read_actions rule already lists it, but no migration ever created
-- the role — so the rego grant was dead: no principal could hold the role, and
-- the floor plan a waiter is supposed to read was reachable only by
-- cashier/shift_manager/kitchen/bar. identity/000012 anticipated this seed
-- explicitly ("'waiter' is not seeded today ... listed forward-looking so a
-- future seed inherits the correct default").
--
-- Permission set is deliberately EXACTLY what authz.rego enforces for waiter
-- today: pos.table.read, nothing else. authz.rego grants waiter no catalog,
-- check, order or inventory action, so seeding any of those would create a
-- grant that OPA denies for its own holder — the same defect
-- permission_wiring_test.go already records for {inventory, read} on
-- kitchen/bar. Widening the role is a rego decision, not a seed decision.
--
-- Conventions follow 000006/000010: tenant_id IS NULL, is_system = TRUE, runs
-- as app_migrator. Every statement is idempotent, so re-running after a
-- partial failure is safe.

-- ============================================================
-- 1. System template role
-- ============================================================
-- branch_scoped is set literally rather than left to identity/000012's UPDATE:
-- that migration already ran and will never see this row. A waiter membership
-- must name a concrete branch (ADR-SEC-005) — the memberships_branch_scope_guard
-- trigger reads this column, so getting it wrong here would silently allow
-- chain-wide garson memberships.
INSERT INTO roles (id, tenant_id, branch_id, name, system_key, is_system, branch_scoped) VALUES
    ('00000001-0000-0000-0000-000000000008', NULL, NULL, 'Garson', 'waiter', TRUE, TRUE)
ON CONFLICT (system_key) DO NOTHING;

-- ============================================================
-- 2. Template permissions
-- ============================================================
-- Written as a literal (role_id, NULL, resource, action) tuple on purpose, not
-- as a SELECT over roles.system_key: the permission-wiring guard
-- (internal/platform/auth/permission_wiring_test.go) parses these tuples
-- straight out of the migration SQL to learn which roles hold which pair. A
-- SELECT form would be invisible to it and the grant would escape the guard
-- that exists to catch exactly that (same trap as 000014/000016).
--
-- ('tables', 'read') is the seed vocabulary for authz.rego's pos.table.read;
-- cashier and shift_manager already hold the identical pair (000006).
INSERT INTO role_permissions (role_id, tenant_id, resource, action) VALUES
    ('00000001-0000-0000-0000-000000000008', NULL, 'tables', 'read')
ON CONFLICT (role_id, resource, action) DO NOTHING;

-- ============================================================
-- 3. Backfill: clone the role into existing tenants
-- ============================================================
-- Unlike 000014/000016 — which backfilled a new permission onto role clones
-- that already existed — this is a brand-new role, so no tenant has a 'Garson'
-- clone yet and the permission backfill has nothing to join to until the role
-- rows exist. Hence two steps: role rows first, then their permissions.
--
-- Without this the seed is cosmetic for every tenant onboarded before today:
-- identity/events/subscriber.go's SeedTenantRoles only runs on tenant.created
-- and never re-runs. Same trap ADR-SEC-005 documents ("Klonlama — bu satır
-- olmadan düzeltme kozmetik kalır").
--
-- The tenant list is derived from `roles` rather than from `tenants` on
-- purpose: identity/000011 severed the cross-module FK, and an identity
-- migration reaching into the tenant module's table would re-introduce exactly
-- that coupling. Cloned chain-wide roles identify the already-seeded tenants,
-- which is the correct target set anyway.
--
-- Clone shape matches subscriber.go's qRoles verbatim: system_key NULL (only
-- templates keep it), is_system FALSE, branch_scoped copied from the template.
--
-- CAVEAT (inherited from SEC-005): a tenant that already has a custom role
-- literally named 'Garson' is skipped by the ON CONFLICT, and step 4's
-- name-join then attaches tables:read to that pre-existing custom role. The
-- permission-fingerprint query in ADR-SEC-005 § "Deploy öncesi denetim" finds
-- such rows; run it if any tenant is known to have hand-made role names.
INSERT INTO roles (tenant_id, branch_id, name, system_key, is_system, branch_scoped)
SELECT DISTINCT existing.tenant_id, NULL::uuid, tmpl.name, NULL::text, FALSE, tmpl.branch_scoped
FROM roles tmpl
CROSS JOIN roles existing
WHERE tmpl.tenant_id IS NULL
  AND tmpl.system_key = 'waiter'
  AND existing.tenant_id IS NOT NULL
  AND existing.branch_id IS NULL
ON CONFLICT (tenant_id, branch_id, name) DO NOTHING;

-- ============================================================
-- 4. Backfill: clone the permissions onto those role rows
-- ============================================================
-- The join is by NAME, matching how clones are created
-- (subscriber.go: `nr.name = sr.name`). A clone RENAMED by the tenant is not
-- matched — same documented limitation as 000016.
INSERT INTO role_permissions (role_id, tenant_id, resource, action)
SELECT nr.id, nr.tenant_id, v.resource, v.action
FROM roles sr
JOIN roles nr ON nr.tenant_id IS NOT NULL
             AND nr.branch_id IS NULL
             AND nr.name = sr.name
CROSS JOIN (VALUES ('tables', 'read')) AS v(resource, action)
WHERE sr.tenant_id IS NULL
  AND sr.system_key = 'waiter'
ON CONFLICT (role_id, resource, action) DO NOTHING;
