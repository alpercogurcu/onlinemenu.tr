-- Migration: inventory/000002_create_stock_items_warehouses (rollback)
DROP TABLE IF EXISTS unit_conversions;
DROP TABLE IF EXISTS stock_items;
DROP TABLE IF EXISTS warehouses;
