-- payments.rounding_amount: the cash-rounding concession (beşli yuvarlama,
-- docs/plans/2026-10-02-kasa-rapor-programi.md G.2) granted on the payment
-- that closes a check's remainder. amount_total stays what was actually
-- taken; the check is settled by amount_total + rounding_amount.
--
-- Every existing row is a payment without rounding, so DEFAULT 0 is exact and
-- no backfill is needed. Only downward rounding exists, hence >= 0.
SET LOCAL role = app_migrator;

ALTER TABLE payments
    ADD COLUMN rounding_amount BIGINT NOT NULL DEFAULT 0
        CONSTRAINT payments_rounding_amount_chk CHECK (rounding_amount >= 0);
