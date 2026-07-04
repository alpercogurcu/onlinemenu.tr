-- Migration: inventory/000007_transfer_price_and_costs
-- Adds the transfer price (ADR-DATA-006 eklenti / ADR-DATA-007 SS4) and
-- branch-local cost tracking (ADR-DATA-007 SS4/SS "Sube-Yerel Maliyet
-- Cozumlemesi") for the exclusive_hq policy path.
--
-- Design decisions:
--   - branch_transfer_order_items.unit_price/currency are NULL at request
--     time ("talep asamasinda fiyat yok" -- ADR-DATA-007): the requesting
--     branch never sets a price, only the source branch does, at approve
--     time. No CHECK ties unit_price to status here; the service layer
--     (TransferOrderService.Approve) is the sole writer of these two
--     columns, mirroring the existing approved_qty convention on this same
--     table (see migration 000005).
--   - shipment_items.unit_price/currency are copied from the linked BTO item
--     at shipment create time (ShipmentService.Create) and may be overridden
--     per shipment line; they are frozen there, matching the "sevkiyat
--     satirinda dondurulur" wording in ADR-DATA-006's eklenti note. A
--     shipment created without a BTO link (ad-hoc restock) simply leaves
--     these NULL.
--   - stock_levels.last_unit_cost/last_cost_currency/last_cost_source/
--     last_cost_at model the (warehouse, stock_item) branch-local cost of
--     ADR-DATA-007 SS "Sube-Yerel Maliyet Cozumlemesi". Faz 1 only ever
--     writes source='transfer' here (from shipment_items.unit_price on
--     receive, see ShipmentService.Receive); 'purchase_order' and
--     'purchase_receipt' sources are reserved for the Faz 2 purchase-side
--     cost cascade (ADR-DATA-007 SS3) and are listed in the CHECK now so the
--     column shape does not need to change again when that lands.
--   - All four new columns are nullable: an item with no known price/cost
--     yet (free/approved_suppliers policy items with no purchase recorded,
--     or a transfer never priced) must be representable, per ADR-DATA-007's
--     rejection of forcing a cost onto every row (that was the b2b
--     BranchStockTracking mistake in spirit -- a column that is opt-in by
--     construction, not a bolted-on flag).
--
-- ADR references: ADR-DATA-006, ADR-DATA-007, ADR-SEC-001, ADR-SEC-002

-- ============================================================
-- branch_transfer_order_items.unit_price / currency
-- ============================================================
ALTER TABLE branch_transfer_order_items
    ADD COLUMN unit_price NUMERIC(18,4) NULL,
    ADD COLUMN currency   CHAR(3)       NULL;

COMMENT ON COLUMN branch_transfer_order_items.unit_price IS
    'Imalathane/HQ -> franchise transfer (sale) price, set by the source '
    'branch at approve time (ADR-DATA-006 eklenti / ADR-DATA-007 SS4). NULL '
    'at request time -- the requesting branch never sets a price.';
COMMENT ON COLUMN branch_transfer_order_items.currency IS
    'ISO 4217 currency of unit_price. NULL until unit_price is set.';

-- ============================================================
-- shipment_items.unit_price / currency
-- ============================================================
ALTER TABLE shipment_items
    ADD COLUMN unit_price NUMERIC(18,4) NULL,
    ADD COLUMN currency   CHAR(3)       NULL;

COMMENT ON COLUMN shipment_items.unit_price IS
    'Copied from the linked branch_transfer_order_items.unit_price at '
    'shipment create time and may be overridden per line; frozen thereafter '
    '(ADR-DATA-006 eklenti). NULL for ad-hoc shipments with no BTO link or '
    'no priced BTO item.';
COMMENT ON COLUMN shipment_items.currency IS
    'ISO 4217 currency of unit_price. NULL until unit_price is set.';

-- ============================================================
-- stock_levels branch-local cost (ADR-DATA-007)
-- ============================================================
ALTER TABLE stock_levels
    ADD COLUMN last_unit_cost     NUMERIC(18,4) NULL,
    ADD COLUMN last_cost_currency CHAR(3)       NULL,
    ADD COLUMN last_cost_source   TEXT          NULL
        CHECK (last_cost_source IN ('transfer', 'purchase_order', 'purchase_receipt')),
    ADD COLUMN last_cost_at       TIMESTAMPTZ   NULL;

COMMENT ON COLUMN stock_levels.last_unit_cost IS
    'Branch-local cost of this (warehouse, stock_item) pair (ADR-DATA-007). '
    'Faz 1: written only from shipment_items.unit_price on shipment receive '
    '(source=''transfer''), for exclusive_hq-policy items with no local '
    'purchase. NULL when no cost has ever been recorded.';
COMMENT ON COLUMN stock_levels.last_cost_currency IS
    'ISO 4217 currency of last_unit_cost.';
COMMENT ON COLUMN stock_levels.last_cost_source IS
    'transfer (Faz 1, from a received shipment) | purchase_order | '
    'purchase_receipt (both Faz 2, ADR-DATA-007 SS3).';
COMMENT ON COLUMN stock_levels.last_cost_at IS
    'Timestamp the cost was last (re)established -- the "son alim" clock '
    'ADR-DATA-007''s cost resolution rule keys off.';
