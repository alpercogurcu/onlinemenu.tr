DROP INDEX IF EXISTS payment_outbox_dispatchable_idx;
CREATE INDEX payment_outbox_unprocessed_idx ON payment_outbox (created_at) WHERE processed_at IS NULL;

ALTER TABLE payment_outbox
    DROP COLUMN IF EXISTS is_dead,
    DROP COLUMN IF EXISTS last_error,
    DROP COLUMN IF EXISTS next_retry_at,
    DROP COLUMN IF EXISTS retry_count;
