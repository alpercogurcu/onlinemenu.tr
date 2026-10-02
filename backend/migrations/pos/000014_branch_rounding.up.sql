-- Per-branch cash-rounding policy (beşli yuvarlama, kasa-rapor-programi G.2).
-- The branch grants permission and a ceiling; the cashier applies rounding
-- per payment. Defaults keep rounding off, so branches without a row — and
-- every existing row — behave exactly as before (lazy-row contract of
-- pos/000012).
ALTER TABLE pos_branch_settings
    ADD COLUMN rounding_cash_enabled BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN rounding_card_enabled BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN rounding_step_minor INT NOT NULL DEFAULT 500
        CONSTRAINT pos_branch_settings_rounding_step_chk
        CHECK (rounding_step_minor IN (50, 100, 500, 1000)),
    ADD COLUMN rounding_max_per_check_minor INT NOT NULL DEFAULT 1000
        CONSTRAINT pos_branch_settings_rounding_max_chk
        CHECK (rounding_max_per_check_minor >= 0);
