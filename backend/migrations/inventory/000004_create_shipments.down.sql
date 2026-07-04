-- Migration: inventory/000004_create_shipments (rollback)
DROP TABLE IF EXISTS shipment_items;
DROP TABLE IF EXISTS shipments;
