-- POS module: checks (adisyon), orders, order_items, pos_outbox
-- All tables enforce RLS. Cross-module references are bare UUIDs (no FK).

-- checks: a dine-in table session that accumulates orders
CREATE TABLE checks (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID        NOT NULL,
    branch_id   UUID        NOT NULL,
    table_label TEXT        NOT NULL DEFAULT '',
    status      TEXT        NOT NULL DEFAULT 'open'
                            CHECK (status IN ('open', 'closed', 'cancelled')),
    opened_by   UUID        NOT NULL,
    closed_by   UUID,
    note        TEXT        NOT NULL DEFAULT '',
    opened_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    closed_at   TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE checks ENABLE ROW LEVEL SECURITY;
ALTER TABLE checks FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON checks
    USING      (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

GRANT SELECT, INSERT, UPDATE ON checks TO app_runtime;

CREATE INDEX checks_tenant_branch_status_idx ON checks (tenant_id, branch_id, status);
CREATE INDEX checks_opened_at_idx ON checks (tenant_id, opened_at DESC);

-- orders: a single "ticket" for kitchen — belongs to all channels
-- check_id is NULL for takeaway/delivery orders
CREATE TABLE orders (
    id                      UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id               UUID        NOT NULL,
    branch_id               UUID        NOT NULL,
    check_id                UUID        REFERENCES checks (id) ON DELETE RESTRICT,
    order_channel           TEXT        NOT NULL
                                        CHECK (order_channel IN ('dine_in', 'takeaway', 'delivery')),
    delivery_integrator_id  UUID,
    status                  TEXT        NOT NULL DEFAULT 'pending'
                                        CHECK (status IN (
                                            'pending', 'accepted', 'rejected',
                                            'preparing', 'ready', 'delivered', 'cancelled'
                                        )),
    accept_deadline_at      TIMESTAMPTZ,
    accepted_at             TIMESTAMPTZ,
    accepted_by             UUID,
    rejected_at             TIMESTAMPTZ,
    rejected_by             UUID,
    rejection_reason        TEXT        NOT NULL DEFAULT '',
    note                    TEXT        NOT NULL DEFAULT '',
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE orders ENABLE ROW LEVEL SECURITY;
ALTER TABLE orders FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON orders
    USING      (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

GRANT SELECT, INSERT, UPDATE ON orders TO app_runtime;

CREATE INDEX orders_tenant_branch_status_idx ON orders (tenant_id, branch_id, status);
CREATE INDEX orders_check_id_idx ON orders (check_id) WHERE check_id IS NOT NULL;
CREATE INDEX orders_created_at_idx ON orders (tenant_id, created_at DESC);

-- order_items: line items per order; product data snapshotted at order time
CREATE TABLE order_items (
    id                  UUID    PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID    NOT NULL,
    order_id            UUID    NOT NULL REFERENCES orders (id) ON DELETE RESTRICT,
    product_id          UUID    NOT NULL,
    product_name        TEXT    NOT NULL,
    product_price_amount BIGINT NOT NULL,
    product_currency    TEXT    NOT NULL DEFAULT 'TRY',
    tax_rate_bps        INT     NOT NULL DEFAULT 0,
    quantity            INT     NOT NULL CHECK (quantity > 0),
    unit_price_amount   BIGINT  NOT NULL,
    note                TEXT    NOT NULL DEFAULT '',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE order_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE order_items FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON order_items
    USING      (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

GRANT SELECT, INSERT ON order_items TO app_runtime;

CREATE INDEX order_items_order_id_idx ON order_items (order_id);
CREATE INDEX order_items_product_id_idx ON order_items (product_id);

-- pos_outbox: transactional outbox for POS domain events (ADR-DATA-001)
CREATE TABLE pos_outbox (
    event_id        UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID        NOT NULL,
    aggregate_type  TEXT        NOT NULL,
    aggregate_id    UUID        NOT NULL,
    event_type      TEXT        NOT NULL,
    payload         JSONB       NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processed_at    TIMESTAMPTZ
);

ALTER TABLE pos_outbox ENABLE ROW LEVEL SECURITY;
ALTER TABLE pos_outbox FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON pos_outbox
    USING      (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', TRUE), '')::uuid);

GRANT SELECT, INSERT, UPDATE ON pos_outbox TO app_runtime;

CREATE INDEX pos_outbox_unprocessed_idx ON pos_outbox (created_at)
    WHERE processed_at IS NULL;
