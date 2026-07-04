-- Migration: tenant/000003_branch_details_hours_integrators (rollback)
-- Reverses the five sections of the up migration in strict reverse order.
-- Sections 2-5 create independent tables that only FK-reference
-- branches(id)/tenants(id) (not the new branches columns from section 1),
-- so dropping them before altering branches in section 1 is safe either way;
-- this file still follows reverse-creation order for clarity.

-- ============================================================
-- SECTION 5 (reverse) — branch_special_hours
-- ============================================================
DROP TABLE IF EXISTS branch_special_hours;

-- ============================================================
-- SECTION 4 (reverse) — branch_regular_hours
-- ============================================================
DROP TABLE IF EXISTS branch_regular_hours;

-- ============================================================
-- SECTION 3 (reverse) — billing_integrators
-- ============================================================
DROP TABLE IF EXISTS billing_integrators;

-- ============================================================
-- SECTION 2 (reverse) — branch_documents
-- ============================================================
DROP TABLE IF EXISTS branch_documents;

-- ============================================================
-- SECTION 1 (reverse) — branches: franchise legal identity + IBAN
-- ============================================================
DROP INDEX IF EXISTS branches_tax_no_idx;

ALTER TABLE branches
    DROP COLUMN IF EXISTS tax_office,
    DROP COLUMN IF EXISTS tax_no,
    DROP COLUMN IF EXISTS identity_type,
    DROP COLUMN IF EXISTS legal_name,
    DROP COLUMN IF EXISTS iban;
