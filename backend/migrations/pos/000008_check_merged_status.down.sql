-- Revert pos/000008_check_merged_status.
--
-- Rolling back with merged checks already in the table would violate the
-- narrowed CHECK, so those rows are first demoted to 'cancelled' — the status
-- the pre-000008 schema would have forced them into anyway. That loses the
-- "birleştirildi, iptal edilmedi" distinction in the day-end report; it is the
-- only reversible option that does not delete a persisted adisyon.
UPDATE checks SET status = 'cancelled', closed_at = COALESCE(closed_at, updated_at)
WHERE status = 'merged';

DROP INDEX IF EXISTS checks_merged_into_check_id_idx;

ALTER TABLE checks DROP CONSTRAINT checks_merged_into_chk;

ALTER TABLE checks DROP COLUMN merged_into_check_id;

ALTER TABLE checks DROP CONSTRAINT checks_status_check;

ALTER TABLE checks ADD CONSTRAINT checks_status_check
    CHECK (status IN ('open', 'closed', 'cancelled'));

REVOKE UPDATE ON order_items FROM app_runtime;
