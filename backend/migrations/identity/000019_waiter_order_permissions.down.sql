-- Revert identity/000019_waiter_order_permissions.
--
-- Removes the order-taking grants from the waiter template and its tenant
-- clones, leaving only the tables:read grant identity/000017 gave it. Keyed on
-- the waiter role (template + clones matched by the template's name), never on
-- the (resource, action) pair alone: cashier and shift_manager hold the very
-- same pairs and must keep them.
--
-- Rolling back also requires reverting authz.rego's pos_waiter_actions and the
-- waiter entry of catalog_read_actions; otherwise OPA keeps allowing what
-- role_permissions no longer records.

DELETE FROM role_permissions rp
USING roles r
WHERE rp.role_id = r.id
  AND (rp.resource, rp.action) IN (
        ('checks', 'read'), ('checks', 'create'),
        ('orders', 'read'), ('orders', 'create'),
        ('catalog', 'read')
      )
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
