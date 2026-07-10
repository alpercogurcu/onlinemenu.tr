-- Migration: tenant/000005_fiscal_device_config
-- ADR-FISCAL-002 §4: registers the Beko X30TR Cloud adapter as a valid
-- branch_settings.fiscal_device_type value and adds fiscal_device_config, a
-- JSONB bag for adapter-specific settings (terminal defaults, basket mode
-- preference, operator routing). Vendor credentials (Token client-id/secret)
-- are NOT stored here — they live in Vault, addressed by a path convention
-- analogous to billing_integrators.vault_secret_path.
--
-- Depends on:
--   tenant/000001_create_tenants — branch_settings table

ALTER TABLE branch_settings
    DROP CONSTRAINT IF EXISTS branch_settings_fiscal_device_type_check;

ALTER TABLE branch_settings
    ADD CONSTRAINT branch_settings_fiscal_device_type_check
        CHECK (fiscal_device_type IN ('none', 'mock', 'efatura', 'okc', 'beko_x30tr_cloud'));

ALTER TABLE branch_settings
    ADD COLUMN IF NOT EXISTS fiscal_device_config JSONB NOT NULL DEFAULT '{}'::jsonb;
