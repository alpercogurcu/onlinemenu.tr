-- Revert payment/000005: drop the DELETE grant added for the mapping replace.
REVOKE DELETE ON fiscal_section_mappings FROM app_runtime;
