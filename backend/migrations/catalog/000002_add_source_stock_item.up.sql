-- Migration: catalog/000002_add_source_stock_item
-- Adds the optional catalog.products -> inventory.stock_items link
-- (ADR-DATA-005 Faz 1): a sellable product that is fed by a stocked entity
-- (e.g. a franchise-sold finished good produced/shipped by inventory) points
-- at its source stock item. NULL for pure service/combo products that have no
-- stock backing.
--
-- Design decisions:
--   - No FK constraint: inventory.stock_items lives in a different module
--     (inventory) and migrations for each module run independently/in
--     isolation (each module has its own schema_migrations_<module> tracking
--     table and migration path — see repo/integration_test.go's per-module
--     migrate.New calls). A cross-module FK would require the inventory
--     migrations to have already run in the same database session in every
--     deployment order, which is not guaranteed (CLAUDE.md: cross-module DB
--     coupling is forbidden regardless of FK enforceability). Referential
--     integrity to stock_items is therefore an application-level concern
--     (catalog's ProductService validates the id via inventory's public
--     StockReader/service, not via SQL).
--   - Nullable: only products fed by a stocked entity set it; pure service or
--     combo products stay NULL (ADR-DATA-005 "Satılabilir mamul <-> stok
--     kalemi bağı").
--
-- ADR references: ADR-DATA-005

ALTER TABLE products ADD COLUMN source_stock_item_id UUID;

CREATE INDEX products_source_stock_item_idx ON products (source_stock_item_id)
    WHERE source_stock_item_id IS NOT NULL;

COMMENT ON COLUMN products.source_stock_item_id IS
    'Optional link to inventory.stock_items(id) (ADR-DATA-005). No FK: cross-module '
    'reference, migrations run independently per module. NULL for pure service/combo '
    'products with no stock backing.';
