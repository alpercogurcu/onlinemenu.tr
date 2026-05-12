-- Migration: identity/000001_create_users
-- Creates users and branch_users tables for platform identity management.
-- Users are identified by their Keycloak subject (keycloak_sub) globally,
-- but are scoped to a tenant via tenant_id + RLS.

CREATE TABLE users (
    id            UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id     UUID        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    keycloak_sub  TEXT        NOT NULL,
    email         TEXT        NOT NULL,
    full_name     TEXT        NOT NULL DEFAULT '',
    is_active     BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- keycloak_sub is globally unique; a Keycloak user cannot belong to two tenants.
CREATE UNIQUE INDEX users_keycloak_sub_idx ON users (keycloak_sub);

-- Within a tenant, each email must be unique.
CREATE UNIQUE INDEX users_tenant_email_idx ON users (tenant_id, email);

CREATE INDEX users_tenant_idx ON users (tenant_id);

ALTER TABLE users ENABLE ROW LEVEL SECURITY;
ALTER TABLE users FORCE ROW LEVEL SECURITY;

CREATE POLICY users_read  ON users FOR SELECT TO app_runtime
    USING  (tenant_id = current_setting('app.tenant_id', TRUE)::uuid);

CREATE POLICY users_write ON users FOR ALL    TO app_runtime
    USING  (tenant_id = current_setting('app.tenant_id', TRUE)::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id', TRUE)::uuid);

-- ============================================================
-- branch_users
-- Maps users to branches with a specific role for branch-level authorization.
-- ============================================================
CREATE TABLE branch_users (
    id         UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id  UUID        NOT NULL REFERENCES tenants (id)  ON DELETE CASCADE,
    branch_id  UUID        NOT NULL REFERENCES branches (id) ON DELETE CASCADE,
    user_id    UUID        NOT NULL REFERENCES users (id)    ON DELETE CASCADE,
    role       TEXT        NOT NULL CHECK (role IN ('manager', 'cashier', 'waiter', 'kitchen')),
    is_active  BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE (branch_id, user_id)
);

CREATE INDEX branch_users_tenant_idx ON branch_users (tenant_id);
CREATE INDEX branch_users_user_idx   ON branch_users (user_id);

ALTER TABLE branch_users ENABLE ROW LEVEL SECURITY;
ALTER TABLE branch_users FORCE ROW LEVEL SECURITY;

CREATE POLICY branch_users_read  ON branch_users FOR SELECT TO app_runtime
    USING  (tenant_id = current_setting('app.tenant_id', TRUE)::uuid);

CREATE POLICY branch_users_write ON branch_users FOR ALL    TO app_runtime
    USING  (tenant_id = current_setting('app.tenant_id', TRUE)::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id', TRUE)::uuid);
