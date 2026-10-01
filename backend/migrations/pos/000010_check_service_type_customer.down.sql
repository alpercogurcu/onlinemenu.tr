-- Revert pos/000010_check_service_type_customer. Dropping the columns loses
-- which checks were takeaway/delivery and their customer contact details;
-- the rows themselves (and their table_label, which CheckService.Open filled
-- from customer_name for masasız checks) survive, so receipts and reports
-- keep rendering.
ALTER TABLE checks
    DROP COLUMN service_type,
    DROP COLUMN customer_name,
    DROP COLUMN customer_phone,
    DROP COLUMN customer_address;
