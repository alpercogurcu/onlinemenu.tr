-- Revert identity/000017_seed_waiter_role.
--
-- Removes the waiter template, every tenant clone of it, and their
-- tables:read grants. Deleting by role rather than by (resource, action) is
-- required here: unlike 000016's storefront_qr, the ('tables','read') pair is
-- also held by cashier and shift_manager (000006), so a delete keyed on the
-- pair alone would strip the counter roles' floor-plan access too.
--
-- WILL ABORT if any membership references the role: memberships_role_id_fkey
-- is ON DELETE RESTRICT. That is intentional — silently dropping a person's
-- role assignment during a rollback is worse than a failed migration. Revoke
-- the affected memberships first, then re-run. (identity/000010 carries the
-- same exposure for the warehouse role.)
--
-- Rolling this back leaves authz.rego's waiter branch in pos_table_read_actions
-- inert again, exactly as it was before this migration — no rego change needed.

-- 1. Permissions: template + clones (clones matched by the template's name,
--    the same join the up-migration and subscriber.go use).
DELETE FROM role_permissions rp
USING roles r
WHERE rp.role_id = r.id
  AND rp.resource = 'tables'
  AND rp.action = 'read'
  AND (
        (r.tenant_id IS NULL AND r.system_key = 'waiter')
        OR (
             r.tenant_id IS NOT NULL
             AND r.branch_id IS NULL
             AND r.name IN (
                 SELECT name FROM roles WHERE tenant_id IS NULL AND system_key = 'waiter'
             )
           )
      );

-- 2. Tenant clones.
--
-- Matched by name, mirroring the up-migration. Same SEC-005 caveat, and here it
-- cuts the other way: a tenant that had its OWN custom chain-wide role named
-- 'Garson' before this migration ran would be deleted by this statement. The
-- ON DELETE RESTRICT on memberships_role_id_fkey blocks that whenever the role
-- is actually assigned to someone; an unassigned same-named custom role is the
-- residual risk. Run the SEC-005 fingerprint query before rolling back if any
-- tenant is known to hand-name roles.
DELETE FROM roles
WHERE tenant_id IS NOT NULL
  AND branch_id IS NULL
  AND system_key IS NULL
  AND name IN (SELECT name FROM roles WHERE tenant_id IS NULL AND system_key = 'waiter');

-- 3. Template.
DELETE FROM roles WHERE id = '00000001-0000-0000-0000-000000000008' AND is_system = TRUE;
