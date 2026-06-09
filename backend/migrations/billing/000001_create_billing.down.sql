SET LOCAL role = app_migrator;

DROP TABLE IF EXISTS billing_outbox;
DROP TABLE IF EXISTS invoice_items;
DROP TABLE IF EXISTS invoices;
