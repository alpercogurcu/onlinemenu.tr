-- Migration: payment/000004_fiscal_adapter_v2 (rollback)
-- Drops the tables introduced by the up migration (no FK dependency order
-- constraints between them — all references are bare UUIDs) and restores the
-- original, narrower payments.method/status CHECK constraints.

DROP TABLE IF EXISTS fiscal_section_mappings;
DROP TABLE IF EXISTS fiscal_device_sections;
DROP TABLE IF EXISTS fiscal_terminals;
DROP TABLE IF EXISTS fiscal_submissions;

ALTER TABLE payments
    DROP CONSTRAINT IF EXISTS payments_method_check,
    DROP CONSTRAINT IF EXISTS payments_status_check;

ALTER TABLE payments
    ADD CONSTRAINT payments_method_check
        CHECK (method IN ('cash', 'terminal')),
    ADD CONSTRAINT payments_status_check
        CHECK (status IN ('pending', 'completed', 'failed'));
