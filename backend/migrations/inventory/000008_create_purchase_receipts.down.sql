-- Migration: inventory/000008_create_purchase_receipts (rollback)
DROP TABLE IF EXISTS purchase_receipt_items;
DROP TABLE IF EXISTS purchase_receipts;
