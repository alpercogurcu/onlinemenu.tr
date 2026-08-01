-- Revert identity/000014_cashier_shift_permissions.
--
-- Removes `shifts:create` and `shifts:update` from the cashier system template
-- and from every tenant clone of it. `shifts:read` (seeded in 000006) is left
-- in place — this migration never granted it.
--
-- Rolling this back while payment/000007's cash session endpoints are deployed
-- leaves cashiers able to see an open session but unable to open or close one.

DELETE FROM role_permissions rp
USING roles r
WHERE rp.role_id = r.id
  AND rp.resource = 'shifts'
  AND rp.action IN ('create', 'update')
  AND (
        (r.tenant_id IS NULL AND r.system_key = 'cashier')
        OR (
             r.tenant_id IS NOT NULL
             AND r.branch_id IS NULL
             AND r.name = (SELECT name FROM roles WHERE tenant_id IS NULL AND system_key = 'cashier')
           )
      );
