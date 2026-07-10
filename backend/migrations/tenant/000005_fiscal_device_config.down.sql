-- Migration: tenant/000005_fiscal_device_config (rollback)

ALTER TABLE branch_settings
    DROP COLUMN IF EXISTS fiscal_device_config;

ALTER TABLE branch_settings
    DROP CONSTRAINT IF EXISTS branch_settings_fiscal_device_type_check;

ALTER TABLE branch_settings
    ADD CONSTRAINT branch_settings_fiscal_device_type_check
        CHECK (fiscal_device_type IN ('none', 'mock', 'efatura', 'okc'));
