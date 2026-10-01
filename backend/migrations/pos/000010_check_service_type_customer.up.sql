-- checks.service_type + customer contact columns: a check is no longer only a
-- dine-in table session — takeaway ("gel al") and delivery ("paket") checks
-- carry who the sale is for instead of which table it sits on.
--
-- service_type defaults to 'dine_in' so every existing row (all of which were
-- opened against a table or as anonymous masasız sales) is backfilled with
-- the status quo; old clients that never send the field keep producing
-- exactly the rows they produced before. Like pax (pos/000005), the DEFAULT
-- never applies to new rows — CheckRepo.Create lists the column explicitly
-- and normalizes an empty Go string to 'dine_in' itself.
--
-- The customer columns are NOT NULL DEFAULT '' rather than nullable:
-- "unknown customer" and "no customer" need no distinction here (dine-in
-- checks simply have all three empty), and empty-string keeps every reader
-- free of a *string dance. The takeaway/delivery requiredness rules
-- (customer_name always, customer_phone for delivery) are service-layer
-- validation in CheckService.Open, not CHECK constraints — mirroring pax's
-- reasoning: repo-level tests construct rows directly and must stay able to.
ALTER TABLE checks
    ADD COLUMN service_type TEXT NOT NULL DEFAULT 'dine_in'
        CONSTRAINT checks_service_type_chk
        CHECK (service_type IN ('dine_in', 'takeaway', 'delivery')),
    ADD COLUMN customer_name TEXT NOT NULL DEFAULT '',
    ADD COLUMN customer_phone TEXT NOT NULL DEFAULT '',
    ADD COLUMN customer_address TEXT NOT NULL DEFAULT '';
