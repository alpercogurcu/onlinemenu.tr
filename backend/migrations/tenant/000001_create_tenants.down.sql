-- Migration: tenant/000001_create_tenants (rollback)
-- Drops tables in reverse FK-dependency order: branch_settings (child of
-- branches), branches (child of tenants), tenants (root). Indexes and RLS
-- policies are owned by their tables and dropped automatically with them.
--
-- The "uuid-ossp" extension created in the up migration is intentionally
-- left in place (not dropped here): extensions are cluster/database-wide
-- objects that may be shared by other schemas, roles, or modules outside
-- this migration's ownership; dropping a shared, non-module-owned resource
-- from a module-scoped down migration is unsafe.
--
-- By the time this runs in the down chain, tenant/000003's down and
-- tenant/000002's down have already dropped the additional tables they
-- introduced (branch_special_hours, branch_regular_hours,
-- billing_integrators, branch_documents, tenant_documents) and reverted the
-- ALTER TABLE column additions on tenants/branches/branch_settings, so only
-- the three base tables created here remain to be dropped.

DROP TABLE IF EXISTS branch_settings;
DROP TABLE IF EXISTS branches;
DROP TABLE IF EXISTS tenants;
