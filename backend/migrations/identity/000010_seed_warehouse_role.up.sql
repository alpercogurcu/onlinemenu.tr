-- Migration: identity/000010_seed_warehouse_role
-- Seeds the "warehouse" system role (depo/imalat staff, ADR-DATA-005 İlke 4).
-- The role UUID is forward-declared in configs/opa/bundles/authz.rego
-- ("warehouse": 00000001-...-0007); until this seed existed no principal
-- could hold the role, so the rego grants were dead. Same conventions as
-- 000006: tenant_id IS NULL, is_system = TRUE, runs as app_migrator.

INSERT INTO roles (id, tenant_id, branch_id, name, system_key, is_system) VALUES
    ('00000001-0000-0000-0000-000000000007', NULL, NULL, 'Depo', 'warehouse', TRUE)
ON CONFLICT (system_key) DO NOTHING;

-- Controller-level permissions: full inventory surface, read-only catalog
-- (stock items reference sellable products via source_stock_item_id).
INSERT INTO role_permissions (role_id, tenant_id, resource, action) VALUES
    ('00000001-0000-0000-0000-000000000007', NULL, 'stock_items',     'read'),
    ('00000001-0000-0000-0000-000000000007', NULL, 'stock_items',     'create'),
    ('00000001-0000-0000-0000-000000000007', NULL, 'stock_items',     'update'),
    ('00000001-0000-0000-0000-000000000007', NULL, 'warehouses',      'read'),
    ('00000001-0000-0000-0000-000000000007', NULL, 'warehouses',      'update'),
    ('00000001-0000-0000-0000-000000000007', NULL, 'stock_levels',    'read'),
    ('00000001-0000-0000-0000-000000000007', NULL, 'stock_movements', 'read'),
    ('00000001-0000-0000-0000-000000000007', NULL, 'stock_movements', 'create'),
    ('00000001-0000-0000-0000-000000000007', NULL, 'transfer_orders', 'read'),
    ('00000001-0000-0000-0000-000000000007', NULL, 'transfer_orders', 'create'),
    ('00000001-0000-0000-0000-000000000007', NULL, 'transfer_orders', 'update'),
    ('00000001-0000-0000-0000-000000000007', NULL, 'shipments',       'read'),
    ('00000001-0000-0000-0000-000000000007', NULL, 'shipments',       'create'),
    ('00000001-0000-0000-0000-000000000007', NULL, 'shipments',       'update'),
    ('00000001-0000-0000-0000-000000000007', NULL, 'catalog',         'read')
ON CONFLICT DO NOTHING;
