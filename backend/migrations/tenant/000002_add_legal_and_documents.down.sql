-- Migration: tenant/000002_add_legal_and_documents (rollback)
-- Reverses the four sections of the up migration in strict reverse order.

-- ============================================================
-- SECTION 4 (reverse) — tenant_documents
-- ============================================================
DROP TABLE IF EXISTS tenant_documents;

-- ============================================================
-- SECTION 3 (reverse) — branch_settings: POS / billing / fiscal config
-- ============================================================

-- Restore the original fiscal_device_type check constraint from
-- tenant/000001 before dropping the columns added by this migration.
ALTER TABLE branch_settings
    DROP CONSTRAINT IF EXISTS branch_settings_fiscal_device_type_check;

ALTER TABLE branch_settings
    ADD CONSTRAINT branch_settings_fiscal_device_type_check
        CHECK (fiscal_device_type IN ('none', 'mock', 'efatura', 'okc'));

ALTER TABLE branch_settings
    DROP COLUMN IF EXISTS default_price_list_id,
    DROP COLUMN IF EXISTS fiscal_device_config,
    DROP COLUMN IF EXISTS billing_config,
    DROP COLUMN IF EXISTS pos_config,
    DROP COLUMN IF EXISTS pos_terminal_type,
    DROP COLUMN IF EXISTS currency;

-- ============================================================
-- SECTION 2 (reverse) — branches: drop new columns, restore timezone
-- ============================================================
DROP INDEX IF EXISTS branches_tenant_slug_idx;

ALTER TABLE branches
    DROP COLUMN IF EXISTS longitude,
    DROP COLUMN IF EXISTS latitude,
    DROP COLUMN IF EXISTS postal_code,
    DROP COLUMN IF EXISTS district,
    DROP COLUMN IF EXISTS city,
    DROP COLUMN IF EXISTS phone,
    DROP COLUMN IF EXISTS supply_rules,
    DROP COLUMN IF EXISTS operation_type,
    DROP COLUMN IF EXISTS ownership_type,
    DROP COLUMN IF EXISTS slug;

-- Restore the timezone column dropped by this migration's SECTION 2 (moved
-- to branch_settings.business_day_offset per ADR-DATA-003). Re-adding it
-- with the original NOT NULL DEFAULT satisfies existing rows immediately.
ALTER TABLE branches
    ADD COLUMN timezone TEXT NOT NULL DEFAULT 'Europe/Istanbul';

-- ============================================================
-- SECTION 1 (reverse) — tenants: legal identity fields
-- ============================================================
DROP INDEX IF EXISTS tenants_mersis_no_idx;
DROP INDEX IF EXISTS tenants_tax_no_idx;

ALTER TABLE tenants
    DROP COLUMN IF EXISTS contact_email,
    DROP COLUMN IF EXISTS website,
    DROP COLUMN IF EXISTS phone,
    DROP COLUMN IF EXISTS country,
    DROP COLUMN IF EXISTS postal_code,
    DROP COLUMN IF EXISTS district,
    DROP COLUMN IF EXISTS city,
    DROP COLUMN IF EXISTS address,
    DROP COLUMN IF EXISTS iban,
    DROP COLUMN IF EXISTS mersis_no,
    DROP COLUMN IF EXISTS tax_office,
    DROP COLUMN IF EXISTS tax_no,
    DROP COLUMN IF EXISTS identity_type,
    DROP COLUMN IF EXISTS trade_name,
    DROP COLUMN IF EXISTS legal_name;
