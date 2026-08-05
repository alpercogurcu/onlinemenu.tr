-- storefront_guest_orders is dropped first: it carries the module's only
-- intra-module FK (qr_code_id -> storefront_qr_codes).
DROP TABLE IF EXISTS storefront_guest_orders;
DROP TABLE IF EXISTS storefront_qr_codes;
