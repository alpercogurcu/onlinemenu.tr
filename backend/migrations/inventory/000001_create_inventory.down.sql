-- Migration: inventory/000001_create_inventory (rollback)
DROP TABLE IF EXISTS inventory_transactions;
DROP TABLE IF EXISTS inventory_levels;
