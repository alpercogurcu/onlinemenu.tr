-- order_items.seat_no: which guest (kuver) at the table a line was ordered
-- for, so the cashier can later split the bill per person (alman usulü). 0
-- means "no seat assigned" — every pre-existing row, every guest (QR) order
-- (a diner at the table has no waiter numbering the seats) and every staff
-- order whose client never sends the field stays 0, which is also why the
-- column is NOT NULL DEFAULT 0 rather than nullable: "unassigned" needs no
-- distinction from "unknown", and 0 keeps every reader free of a *int dance
-- (same reasoning as pos/000010's customer columns).
--
-- Unlike pax (pos/000005), a CHECK constraint IS added here: seat_no is pure
-- client input with no service-layer default of its own, repo tests construct
-- items with the zero value (which the range allows), and 0..99 mirrors the
-- domain.MaxSeatNo bound OrderService.Place enforces — the constraint is the
-- backstop for a write path that skips the service.
ALTER TABLE order_items
    ADD COLUMN seat_no SMALLINT NOT NULL DEFAULT 0
        CONSTRAINT order_items_seat_no_chk
        CHECK (seat_no BETWEEN 0 AND 99);
