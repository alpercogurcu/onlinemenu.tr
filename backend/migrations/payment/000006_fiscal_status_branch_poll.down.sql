SET LOCAL role = app_migrator;

DROP INDEX IF EXISTS fiscal_submissions_branch_settled_idx;
DROP INDEX IF EXISTS fiscal_submissions_branch_inflight_idx;
