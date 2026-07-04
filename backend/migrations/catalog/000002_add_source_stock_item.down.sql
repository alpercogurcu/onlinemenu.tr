-- Migration: catalog/000002_add_source_stock_item (rollback)
ALTER TABLE products DROP COLUMN IF EXISTS source_stock_item_id;
