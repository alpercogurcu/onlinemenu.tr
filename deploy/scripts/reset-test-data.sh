#!/usr/bin/env bash
# Wipes one tenant's TRANSACTIONAL data (checks, orders, payments, fiscal
# receipts/submissions, cash sessions, guest orders, outbox rows) and puts its
# tables back to "empty". Catalog, floor plan, staff, roles and settings stay.
#
# Why: the pilot tenant is exercised with end-to-end test sales before go-live;
# the reports must start from zero on day one. There is no application-level
# "delete sale" (immutability, ADR-DATA-002), so this is a one-off operator
# tool run as app_migrator (BYPASSRLS) through the loopback Postgres port.
#
# Refuses to run unless CONFIRM=yes-wipe-<slug> matches TENANT_SLUG exactly.
#
#   TENANT_SLUG=diverstreetfood CONFIRM=yes-wipe-diverstreetfood deploy/scripts/reset-test-data.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
: "${TENANT_SLUG:?TENANT_SLUG required}"
[[ "${CONFIRM:-}" == "yes-wipe-${TENANT_SLUG}" ]] || { echo "refusing: set CONFIRM=yes-wipe-${TENANT_SLUG}" >&2; exit 1; }

umask 077
env_url() {
  sops -d --input-type dotenv --output-type dotenv "$ROOT/deploy/.env.prod.sops" \
    | python3 -c '
import sys, urllib.parse
key = sys.argv[1]
for line in sys.stdin:
    line = line.strip()
    if not line or line.startswith("#") or "=" not in line:
        continue
    k, v = line.split("=", 1)
    if k.strip() == key:
        v = v.strip()
        if len(v) >= 2 and v[0] == v[-1] and v[0] in "\"\x27":
            v = v[1:-1]
        print(urllib.parse.quote(v, safe=""))
        break
' "$1"
}
PW="$(env_url APP_MIGRATOR_PASSWORD)"; : "${PW:?}"
DB="$(env_url POSTGRES_DB)"; DB="${DB:-onlinemenu}"
DSN="postgres://app_migrator:${PW}@127.0.0.1:5433/${DB}?sslmode=disable"

SQL=$(cat <<'EOSQL'
DO $$
DECLARE
    v_tenant UUID;
    v_slug   TEXT := current_setting('reset.slug');
    r RECORD;
BEGIN
    SELECT id INTO v_tenant FROM tenants WHERE slug = v_slug;
    IF v_tenant IS NULL THEN RAISE EXCEPTION 'tenant % not found', v_slug; END IF;

    -- Child-first order; every table below carries tenant_id (FORCE RLS
    -- schema rule), so a single predicate is enough and nothing crosses tenants.
    FOR r IN SELECT unnest(ARRAY[
        'cash_movements', 'cash_session_participants', 'cashier_pins', 'cash_sessions',
        'fiscal_receipts', 'fiscal_submissions', 'payments',
        'storefront_guest_orders', 'order_items', 'orders', 'checks',
        'payment_outbox', 'pos_outbox'
    ]) AS t LOOP
        IF to_regclass(r.t) IS NULL THEN
            RAISE NOTICE 'skip %: table does not exist', r.t;
            CONTINUE;
        END IF;
        EXECUTE format('DELETE FROM %I WHERE tenant_id = $1', r.t) USING v_tenant;
        RAISE NOTICE 'wiped %', r.t;
    END LOOP;

    UPDATE tables SET status = 'empty', updated_at = NOW()
    WHERE tenant_id = v_tenant AND status <> 'empty';
    RAISE NOTICE 'tables reset to empty for tenant %', v_tenant;
END
$$;
EOSQL
)

run_psql() {
  if command -v psql >/dev/null 2>&1; then
    psql "$DSN" -v ON_ERROR_STOP=1 "$@"
  else
    docker run --rm -i --network host postgres:17-alpine psql "$DSN" -v ON_ERROR_STOP=1 "$@"
  fi
}

printf "SELECT set_config('reset.slug', %s, false);\n%s\n" "'$TENANT_SLUG'" "$SQL" | run_psql -f -
echo "OK  transactional data wiped for tenant '$TENANT_SLUG'"
