-- Revert pos/000014_branch_rounding.
ALTER TABLE pos_branch_settings
    DROP COLUMN rounding_max_per_check_minor,
    DROP COLUMN rounding_step_minor,
    DROP COLUMN rounding_card_enabled,
    DROP COLUMN rounding_cash_enabled;
