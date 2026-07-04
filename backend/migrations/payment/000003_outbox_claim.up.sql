-- payment_outbox: claim kolonu (ADR-DATA-001)
-- Dispatcher artik satirlari kisa bir "claim" tx'inde isaretleyip publish'i
-- transaction DISINDA yapiyor. claimed_at, bir worker'in satiri ne zaman
-- rezerve ettigini tutar; crash sonrasi stale-reclaim icin kullanilir.
-- Not: payment_outbox_dispatchable_idx (created_at, WHERE processed_at IS NULL
-- AND is_dead = FALSE) degismiyor; claim sorgusu bu index'i oldugu gibi kullanir.
ALTER TABLE payment_outbox
    ADD COLUMN IF NOT EXISTS claimed_at TIMESTAMPTZ;
