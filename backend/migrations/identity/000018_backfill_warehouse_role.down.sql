-- Revert identity/000018_backfill_warehouse_role.
--
-- Removes only what this migration added: the per-tenant "Depo" role clones
-- and their permission grants. The warehouse TEMPLATE (tenant_id IS NULL,
-- id = '00000001-0000-0000-0000-000000000007') and its own permissions are
-- owned by identity/000010 and are deliberately left untouched here — this
-- migration never inserted a tenant_id IS NULL row.
--
-- WILL ABORT if any membership references a cloned role: memberships_role_id_fkey
-- is ON DELETE RESTRICT. That is intentional — silently dropping a person's
-- role assignment during a rollback is worse than a failed migration. Revoke
-- the affected memberships first, then re-run. (Same exposure 000010 and
-- 000017's down carry for their own roles.)

-- 1. Permission clones: only the (resource, action) pairs that actually came
--    from the warehouse template, on tenant clones of it (matched by name,
--    same join the up-migration uses). Restricted to the template's own pairs
--    — not "every row this clone happens to hold" — so a grant a tenant added
--    to its own Depo clone by hand survives the rollback untouched.
DELETE FROM role_permissions rp
USING roles r
WHERE rp.role_id = r.id
  AND r.tenant_id IS NOT NULL
  AND r.branch_id IS NULL
  AND r.system_key IS NULL
  AND r.name IN (SELECT name FROM roles WHERE tenant_id IS NULL AND system_key = 'warehouse')
  AND (rp.resource, rp.action) IN (
      SELECT rp2.resource, rp2.action
      FROM role_permissions rp2
      JOIN roles tmpl ON tmpl.id = rp2.role_id
      WHERE tmpl.tenant_id IS NULL AND tmpl.system_key = 'warehouse'
  );

-- 2. Role clones themselves.
--
-- Matched by name, mirroring the up-migration. Same SEC-005 caveat as
-- 000017's down: a tenant that had its OWN custom chain-wide role named
-- 'Depo' before this migration ran would be deleted by this statement. The
-- ON DELETE RESTRICT on memberships_role_id_fkey blocks that whenever the role
-- is actually assigned to someone; an unassigned same-named custom role is the
-- residual risk. Run the SEC-005 fingerprint query before rolling back if any
-- tenant is known to hand-name roles.
DELETE FROM roles
WHERE tenant_id IS NOT NULL
  AND branch_id IS NULL
  AND system_key IS NULL
  AND name IN (SELECT name FROM roles WHERE tenant_id IS NULL AND system_key = 'warehouse');
