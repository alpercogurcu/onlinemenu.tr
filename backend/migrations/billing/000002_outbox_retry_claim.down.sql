DROP INDEX IF EXISTS billing_outbox_dispatchable_idx;
CREATE INDEX IF NOT EXISTS billing_outbox_unprocessed_idx
    ON billing_outbox (created_at) WHERE processed_at IS NULL;
ALTER TABLE billing_outbox
    DROP COLUMN IF EXISTS retry_count,
    DROP COLUMN IF EXISTS next_retry_at,
    DROP COLUMN IF EXISTS last_error,
    DROP COLUMN IF EXISTS is_dead,
    DROP COLUMN IF EXISTS claimed_at;
