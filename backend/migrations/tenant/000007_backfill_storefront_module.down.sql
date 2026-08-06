-- Revert tenant/000007_backfill_storefront_module.
--
-- Removes the "storefront" entry from enabled_modules for every tenant that
-- currently has it. This is a best-effort inverse, not a precise one: a
-- tenant that already had storefront enabled BEFORE the up-migration ran
-- (e.g. via a future onboarding flow using the Go default, or a manual
-- admin update) is indistinguishable from one this migration touched, and
-- will also lose it here. Matches the same "backfill can't be perfectly
-- reversed" caveat identity/000012 and 000017 document for their own
-- backfills.
UPDATE tenants
SET enabled_modules = enabled_modules - 'storefront',
    updated_at = NOW()
WHERE jsonb_typeof(enabled_modules) = 'array'
  AND enabled_modules @> '["storefront"]'::jsonb;
