-- ADR-ARCH-006: QR dine-in orders arrive without a staff principal, so a check
-- may now be opened by a guest.
--
-- opened_by becomes nullable and gains an explicit opened_by_kind discriminator
-- instead of a sentinel/magic "system person" UUID: a sentinel would make every
-- read path silently treat an unauthenticated guest as a real staff member.
-- The paired CHECK keeps the two columns from drifting — 'staff' requires a
-- person, 'guest_qr' forbids one.
ALTER TABLE checks ADD COLUMN opened_by_kind TEXT NOT NULL DEFAULT 'staff'
    CHECK (opened_by_kind IN ('staff', 'guest_qr'));

ALTER TABLE checks ALTER COLUMN opened_by DROP NOT NULL;

ALTER TABLE checks ADD CONSTRAINT checks_opened_by_kind_chk
    CHECK ((opened_by_kind = 'staff') = (opened_by IS NOT NULL));

-- source answers "who created this row", order_channel answers "how is it
-- fulfilled". They are orthogonal and must not be conflated: a QR order is
-- order_channel='dine_in' AND source='online_qr'.
ALTER TABLE orders ADD COLUMN source TEXT NOT NULL DEFAULT 'pos'
    CHECK (source IN ('pos', 'online_qr'));

ALTER TABLE checks ADD COLUMN source TEXT NOT NULL DEFAULT 'pos'
    CHECK (source IN ('pos', 'online_qr'));

-- Partial index: 'pos' is the overwhelming majority, so only the online rows
-- are worth indexing for storefront/reporting lookups.
CREATE INDEX orders_source_idx ON orders (tenant_id, source) WHERE source <> 'pos';
