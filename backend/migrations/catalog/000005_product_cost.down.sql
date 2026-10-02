-- Migration: catalog/000005_product_cost (rollback)
ALTER TABLE branch_product_overrides DROP COLUMN IF EXISTS cost_amount;
ALTER TABLE products DROP COLUMN IF EXISTS cost_amount;
