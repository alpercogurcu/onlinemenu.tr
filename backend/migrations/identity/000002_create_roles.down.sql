-- Migration: identity/000002_create_roles (rollback)
-- Drops the roles table; its two indexes (roles_tenant_idx, roles_branch_idx)
-- and RLS policies (roles_select, roles_write) are owned by the table and
-- dropped automatically with it.
--
-- By the time this runs in the down chain, memberships (000003),
-- role_permissions (000004) and role_field_policies (000005) — all of which
-- FK-reference roles(id) — plus the 000006/000010 seed rows and the 000009
-- roles_all_scope_read policy have already been reversed by their own down
-- migrations, so no dependent-object error occurs here.

DROP TABLE IF EXISTS roles;
