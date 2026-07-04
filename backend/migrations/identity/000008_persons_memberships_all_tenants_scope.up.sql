-- Migration: identity/000008_persons_memberships_all_tenants_scope
--
-- Replaces the uuid.Nil ("00000000-0000-0000-0000-000000000000") RLS bypass
-- sentinel introduced in 000003 (persons_select, persons_update) and 000007
-- (memberships_read) with an explicit, named platform-scope GUC:
-- app.tenant_scope = 'all_tenants'.
--
-- Why: uuid.Nil compared as a literal tenant_id value is exactly the kind of
-- ambient "ambient god-mode" sentinel that regressed repeatedly in the sibling
-- b2b repo (docs/lessons-from-b2b.md item 6) — any code path that reached
-- WithTenantTx/WithTenantReadTx with a zero-value uuid.UUID (whether on
-- purpose or by an uninitialised-variable bug) silently got cross-tenant
-- visibility. app.tenant_scope is a distinct GUC set only by
-- platform/db.WithAllTenantsTx / WithAllTenantsReadTx, so this class of bug
-- can no longer trigger accidentally: WithTenantTx now rejects uuid.Nil
-- outright (see platform/db/tenant_tx.go, ErrNilTenant), and cross-tenant
-- reads require the caller to opt into a differently-named function.
--
-- persons_update additionally had `WITH CHECK (true)` — a latent
-- cross-tenant write hole regardless of the uuid.Nil bypass — which this
-- migration also closes: writes must match the same membership-subquery
-- visibility rule as reads. No code path needs cross-tenant person writes
-- (PersonService.Update always supplies a real tenantID), so persons_update
-- gets no all_tenants branch at all.

-- ============================================================
-- persons: SELECT gets the all_tenants branch, UPDATE loses WITH CHECK (true)
-- ============================================================

DROP POLICY IF EXISTS persons_select ON persons;
DROP POLICY IF EXISTS persons_update ON persons;

CREATE POLICY persons_select ON persons FOR SELECT TO app_runtime
    USING (
        current_setting('app.tenant_scope', TRUE) = 'all_tenants'
        OR id IN (
            SELECT person_id FROM memberships
            WHERE tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid
        )
    );

CREATE POLICY persons_update ON persons FOR UPDATE TO app_runtime
    USING (
        id IN (
            SELECT person_id FROM memberships
            WHERE tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid
        )
    )
    WITH CHECK (
        id IN (
            SELECT person_id FROM memberships
            WHERE tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid
        )
    );

-- ============================================================
-- memberships: SELECT gets the all_tenants branch (write policy is
-- unaffected — it never had a bypass and still requires app_migrator for
-- any platform-level write, per 000007's original note).
-- ============================================================

DROP POLICY IF EXISTS memberships_read ON memberships;

CREATE POLICY memberships_read ON memberships FOR SELECT TO app_runtime
    USING (
        current_setting('app.tenant_scope', TRUE) = 'all_tenants'
        OR tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid
    );
