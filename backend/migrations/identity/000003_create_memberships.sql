-- Migration: identity/000003_create_memberships
-- Links a person to a tenant+branch with a specific role.
-- One person can hold multiple roles at the same branch (permissions are union-ed).
-- branch_id IS NULL = chain-wide membership (e.g. chain owner, chain auditor).
--
-- After creating memberships, this file also attaches the persons SELECT/UPDATE RLS
-- policies that were deferred from 000001 because they reference this table.

CREATE TABLE memberships (
    id         UUID             PRIMARY KEY DEFAULT gen_random_uuid(),
    person_id  UUID             NOT NULL REFERENCES persons(id)  ON DELETE CASCADE,
    tenant_id  UUID             NOT NULL REFERENCES tenants(id)  ON DELETE CASCADE,
    branch_id  UUID             REFERENCES branches(id)          ON DELETE CASCADE,
    role_id    UUID             NOT NULL REFERENCES roles(id)    ON DELETE RESTRICT,
    status     TEXT             NOT NULL DEFAULT 'active'
                   CHECK (status IN ('active', 'suspended', 'terminated')),
    created_at TIMESTAMPTZ      NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ      NOT NULL DEFAULT NOW(),

    -- A person can hold each (tenant, branch, role) combination exactly once.
    CONSTRAINT memberships_unique UNIQUE (person_id, tenant_id, branch_id, role_id)
);

CREATE INDEX memberships_tenant_person_idx ON memberships (tenant_id, person_id);
CREATE INDEX memberships_person_idx        ON memberships (person_id);
CREATE INDEX memberships_branch_idx        ON memberships (branch_id);
CREATE INDEX memberships_role_idx          ON memberships (role_id);

ALTER TABLE memberships ENABLE ROW LEVEL SECURITY;
ALTER TABLE memberships FORCE ROW LEVEL SECURITY;

CREATE POLICY memberships_read ON memberships FOR SELECT TO app_runtime
    USING  (tenant_id = current_setting('app.tenant_id', TRUE)::uuid);

CREATE POLICY memberships_write ON memberships FOR ALL TO app_runtime
    USING  (tenant_id = current_setting('app.tenant_id', TRUE)::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id', TRUE)::uuid);

-- ============================================================
-- Deferred persons RLS (requires memberships to exist — see 000001)
-- ============================================================

-- Platform-admin bypass: app.tenant_id = uuid.Nil (bootstrap pattern, ADR-SEC-002).
CREATE POLICY persons_select ON persons FOR SELECT TO app_runtime
    USING (
        current_setting('app.tenant_id', TRUE) = '00000000-0000-0000-0000-000000000000'
        OR id IN (
            SELECT person_id FROM memberships
            WHERE tenant_id = current_setting('app.tenant_id', TRUE)::uuid
        )
    );

CREATE POLICY persons_update ON persons FOR UPDATE TO app_runtime
    USING (
        current_setting('app.tenant_id', TRUE) = '00000000-0000-0000-0000-000000000000'
        OR id IN (
            SELECT person_id FROM memberships
            WHERE tenant_id = current_setting('app.tenant_id', TRUE)::uuid
        )
    )
    WITH CHECK (true);
