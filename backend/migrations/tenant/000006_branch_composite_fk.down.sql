-- Migration: tenant/000006_branch_composite_fk (rollback)
-- Restores the original single-column branch_id FKs and unique constraints.
-- Reverse order of the up migration.

-- ============================================================
-- branch_special_hours
-- ============================================================
ALTER TABLE branch_special_hours
    DROP CONSTRAINT branch_special_hours_unique;

ALTER TABLE branch_special_hours
    ADD CONSTRAINT branch_special_hours_unique UNIQUE (branch_id, special_date);

ALTER TABLE branch_special_hours
    DROP CONSTRAINT branch_special_hours_branch_tenant_fkey;

ALTER TABLE branch_special_hours
    ADD CONSTRAINT branch_special_hours_branch_id_fkey
        FOREIGN KEY (branch_id) REFERENCES branches (id) ON DELETE CASCADE;

-- ============================================================
-- branch_regular_hours
-- ============================================================
ALTER TABLE branch_regular_hours
    DROP CONSTRAINT branch_regular_hours_unique;

ALTER TABLE branch_regular_hours
    ADD CONSTRAINT branch_regular_hours_unique UNIQUE (branch_id, day_of_week, sort_order);

ALTER TABLE branch_regular_hours
    DROP CONSTRAINT branch_regular_hours_branch_tenant_fkey;

ALTER TABLE branch_regular_hours
    ADD CONSTRAINT branch_regular_hours_branch_id_fkey
        FOREIGN KEY (branch_id) REFERENCES branches (id) ON DELETE CASCADE;

-- ============================================================
-- billing_integrators
-- ============================================================
ALTER TABLE billing_integrators
    DROP CONSTRAINT billing_integrators_branch_tenant_fkey;

ALTER TABLE billing_integrators
    ADD CONSTRAINT billing_integrators_branch_id_fkey
        FOREIGN KEY (branch_id) REFERENCES branches (id) ON DELETE CASCADE;

-- ============================================================
-- branch_documents
-- ============================================================
ALTER TABLE branch_documents
    DROP CONSTRAINT branch_documents_branch_tenant_fkey;

ALTER TABLE branch_documents
    ADD CONSTRAINT branch_documents_branch_id_fkey
        FOREIGN KEY (branch_id) REFERENCES branches (id) ON DELETE CASCADE;

-- ============================================================
-- branches
-- ============================================================
ALTER TABLE branches
    DROP CONSTRAINT branches_id_tenant_id_key;
