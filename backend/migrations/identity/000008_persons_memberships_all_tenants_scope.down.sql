-- Down migration for identity/000008_persons_memberships_all_tenants_scope.
-- Restores the pre-000008 policies exactly as created in 000003 and 000007
-- (uuid.Nil tenant sentinel, persons_update WITH CHECK (true)).

DROP POLICY IF EXISTS persons_select ON persons;
DROP POLICY IF EXISTS persons_update ON persons;

CREATE POLICY persons_select ON persons FOR SELECT TO app_runtime
    USING (
        current_setting('app.tenant_id', TRUE) = '00000000-0000-0000-0000-000000000000'
        OR id IN (
            SELECT person_id FROM memberships
            WHERE tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid
        )
    );

CREATE POLICY persons_update ON persons FOR UPDATE TO app_runtime
    USING (
        current_setting('app.tenant_id', TRUE) = '00000000-0000-0000-0000-000000000000'
        OR id IN (
            SELECT person_id FROM memberships
            WHERE tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid
        )
    )
    WITH CHECK (true);

DROP POLICY IF EXISTS memberships_read ON memberships;

CREATE POLICY memberships_read ON memberships FOR SELECT TO app_runtime
    USING (
        current_setting('app.tenant_id', TRUE) = '00000000-0000-0000-0000-000000000000'
        OR tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid
    );
