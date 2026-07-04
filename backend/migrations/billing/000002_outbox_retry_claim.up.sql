-- billing_outbox: retry/dead-letter + claim kolonlari (ADR-DATA-001)
-- pos/payment outbox'lariyla ayni sema: dispatcher tek kod yolundan uc
-- tabloyu da isleyebilsin. billing_outbox bugune kadar dispatcher'a bagli
-- degildi (satirlar yazilip hic yayinlanmiyordu); bu migration semayi
-- esitler, dispatcher kaydini kod tarafinda tables listesi yapar.
ALTER TABLE billing_outbox
    ADD COLUMN IF NOT EXISTS retry_count   INT         NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS next_retry_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS last_error    TEXT,
    ADD COLUMN IF NOT EXISTS is_dead       BOOLEAN     NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS claimed_at    TIMESTAMPTZ;

DROP INDEX IF EXISTS billing_outbox_unprocessed_idx;
CREATE INDEX billing_outbox_dispatchable_idx ON billing_outbox (created_at)
    WHERE processed_at IS NULL AND is_dead = FALSE;
