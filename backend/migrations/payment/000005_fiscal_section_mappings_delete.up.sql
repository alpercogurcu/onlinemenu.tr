-- Migration: payment/000005_fiscal_section_mappings_delete
-- The admin API replaces a branch's category→section mappings wholesale in a
-- single transaction (PUT /api/v1/fiscal/section-mappings), which needs DELETE.
-- payment/000004 granted only SELECT, INSERT, UPDATE on this table, so the
-- replace would fail at runtime with "permission denied for table
-- fiscal_section_mappings".
--
-- This gap is invisible to the repo integration tests: their bootstrap issues
-- ALTER DEFAULT PRIVILEGES ... GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES
-- TO app_runtime, so app_runtime holds DELETE there regardless of this file.
-- The grant is asserted by inspection against migrations/, not by a green test.
--
-- fiscal_device_sections already carries DELETE (payment/000004) for the same
-- full-sync reason; RLS needs no change — fiscal_section_mappings' FOR ALL
-- policy's USING clause already governs DELETE.
--
-- Depends on:
--   payment/000004_fiscal_adapter_v2 — creates fiscal_section_mappings

GRANT DELETE ON fiscal_section_mappings TO app_runtime;
