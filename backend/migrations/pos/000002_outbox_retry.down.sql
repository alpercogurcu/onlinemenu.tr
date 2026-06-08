DROP INDEX IF EXISTS pos_outbox_dispatchable_idx;
CREATE INDEX pos_outbox_unprocessed_idx ON pos_outbox (created_at) WHERE processed_at IS NULL;

ALTER TABLE pos_outbox
    DROP COLUMN IF EXISTS is_dead,
    DROP COLUMN IF EXISTS last_error,
    DROP COLUMN IF EXISTS next_retry_at,
    DROP COLUMN IF EXISTS retry_count;
