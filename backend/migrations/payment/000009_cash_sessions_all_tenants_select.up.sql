-- Migration: payment/000009_cash_sessions_all_tenants_select
-- ADR-DATA-008 açıkları: StaleSessionWatch (service/stale_session_watch.go)
-- warns about branch cash sessions left 'opened' far past a normal shift, so
-- an operator can go check the till. Like the fiscal reconciler
-- (payment/000004's fiscal_submissions_all_tenants_select), the sweep scans
-- across every tenant in one pass under db.WithAllTenantsReadTx
-- (app.tenant_scope = 'all_tenants'), which the per-tenant tenant_isolation
-- policy on cash_sessions does not grant. This is read-only — the watch never
-- writes (no automatic close), so only a SELECT policy is added, unlike
-- fiscal_submissions' extra claim/UPDATE policy which that sweep needs and
-- this one does not.

SET LOCAL role = app_migrator;

CREATE POLICY cash_sessions_all_tenants_select ON cash_sessions
    FOR SELECT TO app_runtime
    USING (current_setting('app.tenant_scope', TRUE) = 'all_tenants');
