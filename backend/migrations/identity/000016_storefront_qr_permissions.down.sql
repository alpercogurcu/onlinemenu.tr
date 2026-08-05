-- Revert identity/000016_storefront_qr_permissions.
--
-- Removes `storefront_qr:read` / `storefront_qr:manage` from the cashier and
-- shift_manager system templates and from every tenant clone of them. No other
-- migration grants this resource, so deleting by resource name is exact.
--
-- Rolling this back while the storefront admin routes are deployed leaves
-- /api/v1/storefront/qr-codes reachable by the manager wildcard only: staff
-- cannot print or retire a table QR code, but existing printed codes keep
-- working (guest sessions never consult role_permissions).

DELETE FROM role_permissions rp
USING roles r
WHERE rp.role_id = r.id
  AND rp.resource = 'storefront_qr'
  AND (
        (r.tenant_id IS NULL AND r.system_key IN ('cashier', 'shift_manager'))
        OR (
             r.tenant_id IS NOT NULL
             AND r.branch_id IS NULL
             AND r.name IN (
                 SELECT name FROM roles
                 WHERE tenant_id IS NULL AND system_key IN ('cashier', 'shift_manager')
             )
           )
      );
