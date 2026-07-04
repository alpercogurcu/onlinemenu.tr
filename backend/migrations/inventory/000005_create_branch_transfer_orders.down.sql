-- Migration: inventory/000005_create_branch_transfer_orders (rollback)
ALTER TABLE shipments DROP COLUMN IF EXISTS transfer_order_id;
DROP TABLE IF EXISTS branch_transfer_order_items;
DROP TABLE IF EXISTS branch_transfer_orders;
