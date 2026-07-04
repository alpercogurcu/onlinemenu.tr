-- Migration: identity/000005_create_role_field_policies (rollback)
-- Drops the role_field_policies table; its indexes and RLS policies
-- (role_field_policies_select, role_field_policies_write) are owned by the
-- table and dropped automatically with it. The 000006/000010 seed rows in
-- this table have already been deleted by their own down migrations by the
-- time this runs, but the table drop would remove them regardless.

DROP TABLE IF EXISTS role_field_policies;
