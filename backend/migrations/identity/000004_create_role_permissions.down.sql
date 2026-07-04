-- Migration: identity/000004_create_role_permissions (rollback)
-- Drops the role_permissions table; its indexes and RLS policies
-- (role_permissions_select, role_permissions_write) are owned by the table
-- and dropped automatically with it. The 000006/000010 seed rows in this
-- table have already been deleted by their own down migrations by the time
-- this runs, but the table drop would remove them regardless.

DROP TABLE IF EXISTS role_permissions;
