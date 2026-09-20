-- docs/pos-ux-spec.md §3c (adisyon birleştirme): the source check of a merge
-- needs a status of its own.
--
-- Why not 'cancelled': the day-end report (pos/repo/report_repo.go
-- statusTotals) counts every cancelled check in the window as
-- CancelledCheckCount/CancelledAmount, and the admin adisyon table renders it
-- with the "İptal" badge. Every table merge would then read as a cancelled
-- sale — a figure the shift manager reconciles against the till.
--
-- 'merged' means "this check's orders were moved onto another check"; the
-- money is not lost, it is billed on the target.
--
-- closed_at stays NULL for a merged check, deliberately. CheckRepo.UpdateStatus
-- only stamps it for 'closed'/'cancelled', and every report query filters on
-- `c.closed_at >= from AND c.closed_at < to`. A NULL therefore keeps merged
-- checks out of the report window structurally, not merely because the current
-- WHERE clauses also happen to list the two statuses explicitly.
--
-- checks_open_table_id_uidx is partial on status = 'open', so a merged source
-- check releases its table slot without any index change.
ALTER TABLE checks DROP CONSTRAINT checks_status_check;

ALTER TABLE checks ADD CONSTRAINT checks_status_check
    CHECK (status IN ('open', 'closed', 'cancelled', 'merged'));

-- merged_into_check_id records where the orders went, so "bu adisyona ne oldu"
-- is answerable from the row itself rather than only from the outbox event.
-- Module-internal FK (checks -> checks), same as checks.table_id.
ALTER TABLE checks ADD COLUMN merged_into_check_id UUID REFERENCES checks (id) ON DELETE RESTRICT;

ALTER TABLE checks ADD CONSTRAINT checks_merged_into_chk
    CHECK ((status = 'merged') = (merged_into_check_id IS NOT NULL));

CREATE INDEX checks_merged_into_check_id_idx ON checks (merged_into_check_id)
    WHERE merged_into_check_id IS NOT NULL;

-- Kalem taşıma (docs/pos-ux-spec.md §3c move-items) re-points an order_item at
-- another order. pos/000001 granted app_runtime only SELECT+INSERT on this
-- table — order items were immutable once written — so the UPDATE is added
-- here rather than silently failing at runtime with a permission error.
-- DELETE is deliberately NOT granted: a moved line keeps its row and its id,
-- which is what makes the move auditable.
GRANT UPDATE ON order_items TO app_runtime;
