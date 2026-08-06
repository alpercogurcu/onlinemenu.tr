-- Migration: identity/000018_backfill_warehouse_role
--
-- Problem: identity/000010 seeded the "warehouse" (Depo) system role template,
-- but two things never happened:
--   (a) no migration cloned it into tenants that already existed on the day
--       000010 shipped;
--   (b) identity/events/subscriber.go's systemRoleKeys never listed
--       "warehouse" (its own NOTE said so explicitly), so tenants onboarded
--       AFTER 000010 never got a clone either.
-- Net effect: today, zero tenants can hold the warehouse role — it is a
-- template with no clones anywhere. This migration fixes (a); the sibling Go
-- change (removing "warehouse" from subscriber.go's NOTE and adding it to
-- systemRoleKeys) fixes (b). Both are required together — this migration alone
-- would only cover tenants that exist right now, not future ones.
--
-- Same two-step shape as identity/000017's backfill (role clone, then
-- permission clone joined by name), except 000017 seeded a brand-new template
-- in the same migration while this one backfills clones of a template that
-- already existed (000010) and is left untouched — this migration inserts no
-- ('warehouse', ...) rows into the template scope (tenant_id IS NULL), only
-- into existing tenants' scopes.
--
-- Permission set: NOT hand-listed here. The clone step SELECTs whatever
-- role_permissions rows the template actually holds (mirrors
-- subscriber.go's qPerms), so this migration cannot drift from 000010 if that
-- migration's grant list is ever revisited by a later migration. This is also
-- why no permission_wiring_test.go registry change is needed: the guard's
-- parser only reads literal (role_id, NULL, resource, action) VALUES tuples
-- out of migration SQL, and this migration contains none — it is a pure
-- SELECT-join clone, structurally invisible to that parser, exactly like
-- 000017's own step 3/4 precedent and subscriber.go's runtime clone path.
--
-- Idempotent: every statement is ON CONFLICT DO NOTHING, safe to re-run after
-- a partial failure.

-- ============================================================
-- 1. Backfill: clone the warehouse role into existing tenants
-- ============================================================
-- Tenant list derived from `roles` (not `tenants`) on purpose — identity/000011
-- severed the cross-module FK; reaching into the tenant module's table from an
-- identity migration would re-introduce that coupling. Any tenant with at
-- least one chain-wide role clone (branch_id IS NULL) is already a seeded
-- tenant and the correct backfill target.
--
-- Clone shape matches subscriber.go's qRoles verbatim: system_key NULL (only
-- the template keeps it), is_system FALSE, branch_scoped copied from the
-- template (identity/000012 already set warehouse's template branch_scoped =
-- TRUE, so every clone correctly requires a concrete branch on membership).
--
-- CAVEAT (inherited from SEC-005, same as 000017): a tenant that already has a
-- custom role literally named 'Depo' is skipped by the ON CONFLICT, and step 2
-- then attaches the warehouse permission set to that pre-existing custom role.
-- Run the SEC-005 permission-fingerprint query if any tenant is known to hand-
-- name roles.
INSERT INTO roles (tenant_id, branch_id, name, system_key, is_system, branch_scoped)
SELECT DISTINCT existing.tenant_id, NULL::uuid, tmpl.name, NULL::text, FALSE, tmpl.branch_scoped
FROM roles tmpl
CROSS JOIN roles existing
WHERE tmpl.tenant_id IS NULL
  AND tmpl.system_key = 'warehouse'
  AND existing.tenant_id IS NOT NULL
  AND existing.branch_id IS NULL
ON CONFLICT (tenant_id, branch_id, name) DO NOTHING;

-- ============================================================
-- 2. Backfill: clone the template's permissions onto those role rows
-- ============================================================
-- Joined by NAME, matching how clones are created (subscriber.go:
-- `nr.name = sr.name`). A clone RENAMED by the tenant is not matched — same
-- documented limitation as 000016/000017.
INSERT INTO role_permissions (role_id, tenant_id, resource, action)
SELECT nr.id, nr.tenant_id, rp.resource, rp.action
FROM role_permissions rp
JOIN roles sr ON sr.id = rp.role_id AND sr.tenant_id IS NULL AND sr.system_key = 'warehouse'
JOIN roles nr ON nr.tenant_id IS NOT NULL
             AND nr.branch_id IS NULL
             AND nr.name = sr.name
ON CONFLICT (role_id, resource, action) DO NOTHING;
