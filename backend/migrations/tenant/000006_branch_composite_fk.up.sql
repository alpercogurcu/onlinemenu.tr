-- Migration: tenant/000006_branch_composite_fk
-- Closes a cross-tenant write hole: branch_documents.branch_id,
-- billing_integrators.branch_id, branch_regular_hours.branch_id and
-- branch_special_hours.branch_id were plain FKs to branches(id) — "does this
-- branch exist", never "does it belong to the tenant_id on this row". RLS
-- only checks the tenant_id of the row being written, so any tenant in its
-- own valid RLS context could attach documents/hours/integrators to another
-- tenant's real branch by supplying that branch's UUID.
--
-- Fix: composite foreign keys so Postgres itself rejects a mismatched
-- (branch_id, tenant_id) pair. Requires a UNIQUE (id, tenant_id) on branches
-- to serve as the FK target (redundant with the PK, but composite FKs must
-- reference a unique constraint covering all referenced columns).
--
-- MATCH SIMPLE (the default — no MATCH clause below): a composite FK check
-- is skipped entirely when ANY referencing column is NULL. billing_integrators
-- .branch_id is nullable (NULL = tenant-wide integrator, no branch override —
-- see 000003's comment), so a NULL branch_id always skips the branch/tenant
-- check, which is exactly the desired "no branch to validate" behaviour.
-- MATCH FULL would NOT work here: it requires all referencing columns to be
-- either all NULL or all non-NULL, and tenant_id on billing_integrators is
-- NOT NULL, so every tenant-wide row (NULL branch_id, non-NULL tenant_id)
-- would violate MATCH FULL's mixed-null rule. branch_documents,
-- branch_regular_hours and branch_special_hours all have branch_id NOT NULL,
-- so MATCH SIMPLE vs FULL makes no difference for them, but MATCH SIMPLE is
-- used uniformly for consistency.
--
-- Pre-migration data check (2026-08-03, onlinemenu-dev-postgres-1, dev DB):
-- zero mismatched rows found across all four child tables (see report) and
-- the tables were in fact empty (0 rows in branch_documents,
-- billing_integrators, branch_regular_hours, branch_special_hours). No data
-- was deleted or modified by this migration. If a mismatched row existed in
-- some other environment, the ADD CONSTRAINT statements below would fail
-- outright with a foreign_key_violation — that failure IS the deliberate
-- guard: it stops the migration rather than silently dropping rows, and
-- forces a manual decision (reassign or delete) before this can proceed.
--
-- Depends on:
--   tenant/000001_create_tenants — branches, tenants
--   tenant/000003_branch_details_hours_integrators — branch_documents,
--     billing_integrators, branch_regular_hours, branch_special_hours

-- ============================================================
-- branches: composite unique target for the child FKs below
-- ============================================================
ALTER TABLE branches
    ADD CONSTRAINT branches_id_tenant_id_key UNIQUE (id, tenant_id);

-- ============================================================
-- branch_documents
-- ============================================================
ALTER TABLE branch_documents
    DROP CONSTRAINT branch_documents_branch_id_fkey;

ALTER TABLE branch_documents
    ADD CONSTRAINT branch_documents_branch_tenant_fkey
        FOREIGN KEY (branch_id, tenant_id) REFERENCES branches (id, tenant_id) ON DELETE CASCADE;

-- ============================================================
-- billing_integrators (branch_id nullable — see MATCH SIMPLE note above)
-- ============================================================
ALTER TABLE billing_integrators
    DROP CONSTRAINT billing_integrators_branch_id_fkey;

ALTER TABLE billing_integrators
    ADD CONSTRAINT billing_integrators_branch_tenant_fkey
        FOREIGN KEY (branch_id, tenant_id) REFERENCES branches (id, tenant_id) ON DELETE CASCADE;

-- ============================================================
-- branch_regular_hours
-- ============================================================
ALTER TABLE branch_regular_hours
    DROP CONSTRAINT branch_regular_hours_branch_id_fkey;

ALTER TABLE branch_regular_hours
    ADD CONSTRAINT branch_regular_hours_branch_tenant_fkey
        FOREIGN KEY (branch_id, tenant_id) REFERENCES branches (id, tenant_id) ON DELETE CASCADE;

-- Tenant-scope the slot-uniqueness constraint explicitly. Once the FK above
-- is in place, branch_id alone already determines a single tenant, so this
-- alone no longer enables the cross-tenant "slot squatting" DoS described in
-- the defect report — but making the tenant scope explicit in the constraint
-- itself removes any doubt for future readers and matches the pattern used
-- by every other tenant-scoped uniqueness rule in this schema.
ALTER TABLE branch_regular_hours
    DROP CONSTRAINT branch_regular_hours_unique;

ALTER TABLE branch_regular_hours
    ADD CONSTRAINT branch_regular_hours_unique UNIQUE (tenant_id, branch_id, day_of_week, sort_order);

-- ============================================================
-- branch_special_hours
-- ============================================================
ALTER TABLE branch_special_hours
    DROP CONSTRAINT branch_special_hours_branch_id_fkey;

ALTER TABLE branch_special_hours
    ADD CONSTRAINT branch_special_hours_branch_tenant_fkey
        FOREIGN KEY (branch_id, tenant_id) REFERENCES branches (id, tenant_id) ON DELETE CASCADE;

ALTER TABLE branch_special_hours
    DROP CONSTRAINT branch_special_hours_unique;

ALTER TABLE branch_special_hours
    ADD CONSTRAINT branch_special_hours_unique UNIQUE (tenant_id, branch_id, special_date);
