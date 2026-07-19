-- Migration: payment/000006_fiscal_status_branch_poll
-- Indexes for GET /api/v1/payments/fiscal-pending, the branch-scoped poll a
-- POS station runs every few seconds to see fiscal registrations started on
-- the OTHER stations of the same branch.
--
-- Existing fiscal_submissions indexes serve the dispatcher/reconciler, which
-- scan cross-tenant by created_at/submitted_at. Neither leads with
-- (tenant_id, branch_id), so the poll would seq-scan the table.
--
-- Depends on: payment/000004_fiscal_adapter_v2 — fiscal_submissions table

SET LOCAL role = app_migrator;

-- In-flight registrations for one branch. The predicate covers 'submitted'
-- as well as 'pending': from the polling station's point of view a sale
-- handed to the device but not yet confirmed is still "awaiting", and both
-- states must appear in the response's `pending` array.
CREATE INDEX IF NOT EXISTS fiscal_submissions_branch_inflight_idx
    ON fiscal_submissions (tenant_id, branch_id, created_at)
    WHERE status IN ('pending', 'submitted');

-- Recently settled window (last 5 minutes). fiscal_submissions is append-only,
-- so terminal rows accumulate without bound; without this index the poll's
-- `completed_at > now() - interval` predicate degrades linearly with table age
-- while being executed by every station, every few seconds.
CREATE INDEX IF NOT EXISTS fiscal_submissions_branch_settled_idx
    ON fiscal_submissions (tenant_id, branch_id, completed_at)
    WHERE completed_at IS NOT NULL;
