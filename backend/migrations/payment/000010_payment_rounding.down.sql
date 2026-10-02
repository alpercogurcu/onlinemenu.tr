-- Revert payment/000010_payment_rounding. Dropping the column makes rounded
-- checks read as underpaid by the conceded amount.
SET LOCAL role = app_migrator;

ALTER TABLE payments DROP COLUMN rounding_amount;
