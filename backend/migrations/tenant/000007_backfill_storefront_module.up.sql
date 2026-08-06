-- Migration: tenant/000007_backfill_storefront_module
--
-- Problem: storefront (QR dine-in, ADR-ARCH-006) ships as a baseline feature,
-- not an opt-in purchase, but no tenant created before this migration has it
-- in enabled_modules (tenants.enabled_modules JSONB, ADR-ARCH-001's
-- billing/entitlement layer — "hangi modüller satın alındı"). The
-- forward-looking half of this fix lives in Go
-- (tenant/service.Service.Create's defaultEnabledModules): a tenant created
-- with no explicit module list from here on is born with storefront. That
-- default only fires when the caller passes an empty list, so — same trap
-- ADR-SEC-005 and identity/000017 document for role clones — it is cosmetic
-- for every tenant that already exists without this backfill.
--
-- NOTE (reported, not fixed here): deploy/dev-seed.sql inserts the local dev
-- tenant via raw SQL with an explicit enabled_modules list that omits
-- storefront. Neither this migration (it only UPDATEs existing rows) nor the
-- Go default (dev-seed bypasses the service layer entirely) reaches that
-- tenant on a fresh database, because dev-seed runs after migrations and
-- writes its own explicit list. Out of scope for an identity/tenant
-- migration — flagged for whoever owns that file.
--
-- Idempotent: the WHERE clause only touches rows that don't already have
-- storefront, so a re-run after a partial failure affects zero rows the
-- second time.
--
-- jsonb_typeof guard: enabled_modules is NOT NULL at the column level, but
-- that only rules out SQL NULL — a JSON null (or a non-array JSON value)
-- still satisfies it. `||` between a non-array JSONB and an array does not
-- produce the intended union (e.g. a JSON null becomes [null, "storefront"]),
-- so rows that are not already a JSON array are deliberately left untouched
-- rather than silently corrupted. None are expected to exist (every writer
-- in the codebase marshals a Go []string), but the guard costs nothing.
UPDATE tenants
SET enabled_modules = enabled_modules || '["storefront"]'::jsonb,
    updated_at = NOW()
WHERE jsonb_typeof(enabled_modules) = 'array'
  AND NOT (enabled_modules @> '["storefront"]'::jsonb);
