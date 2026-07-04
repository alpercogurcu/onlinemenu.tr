ALTER TABLE payment_outbox
    DROP COLUMN IF EXISTS claimed_at;
