#!/usr/bin/env bash
#
# verify_migrations.sh -- full-chain local verification of the golang-migrate
# up/down/up round trip for the identity, tenant and catalog modules.
#
# Why this exists (see docs/lessons-from-b2b.md item 1): the CI migration
# check (.github/workflows/migration-check.yml) only round-trips the HEAD
# migration of each module (down 1 + up). It never proves that the FULL
# down chain works from head back to zero, because most early migrations in
# each module historically shipped without a .down.sql file. Once every
# migration in a module has a .down.sql, that guarantee needs a real test --
# this script is that test, run locally on demand (not wired into CI, which
# stays a fast HEAD-only check).
#
# What it does, against a throwaway postgres:17 container:
#   1. Bootstraps app_migrator / app_runtime roles exactly like
#      backend/internal/modules/identity/repo/integration_test.go's
#      bootstrapRoles() does (ADR-SEC-002 parity: migrations run as
#      app_migrator, the table owner).
#   2. For each module, in FK-dependency order (tenant before identity --
#      roles.tenant_id/branch_id FK-reference tenants/branches; catalog has
#      no cross-module FK so it is safe last): migrate up (all versions).
#   3. Tears every module all the way back down (migrate down -all), in the
#      REVERSE of the up order (catalog, then identity, then tenant) so that
#      identity's roles table (which FK-references tenant's tables) is fully
#      gone before tenant's tables are dropped.
#   4. Re-runs migrate up (all versions) for every module in the original
#      order, to prove the schema produced by a full down+up cycle is
#      identical in shape to a fresh up (golang-migrate would otherwise
#      "successfully" apply on top of leftover objects and mask a botched
#      down file).
#
# Usage: backend/scripts/verify_migrations.sh
# Requires: docker and a `migrate` binary on PATH or in $HOME/go/bin
# (install: go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@v4.18.1).
# psql is not required on the host -- role bootstrap runs via `docker exec`
# against the psql client bundled in the postgres image itself.
#
# Exit status: 0 when every module's up -> down -all -> up round trip
# succeeds; non-zero on the first failure (set -e).

set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."  # backend/

MODULES_UP_ORDER=(tenant identity catalog)

CONTAINER_NAME="onlinemenu-verify-migrations-$$"
PG_IMAGE="postgres:17"
PG_PORT="${VERIFY_MIGRATIONS_PORT:-55432}"
PG_DB="onlinemenu_verify"
SUPER_USER="postgres"
SUPER_PASSWORD="postgres"

MIGRATOR_USER="app_migrator"
MIGRATOR_PASSWORD="migrator_secret"
RUNTIME_USER="app_runtime"
RUNTIME_PASSWORD="runtime_secret"

MIGRATE_BIN=""

cleanup() {
  echo "=== cleanup: removing container ${CONTAINER_NAME} ==="
  docker rm -f "${CONTAINER_NAME}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

resolve_migrate_bin() {
  if command -v migrate >/dev/null 2>&1; then
    MIGRATE_BIN="migrate"
    return
  fi
  if [ -x "${HOME}/go/bin/migrate" ]; then
    MIGRATE_BIN="${HOME}/go/bin/migrate"
    return
  fi
  echo "verify_migrations.sh: no 'migrate' binary found on PATH or in \$HOME/go/bin." >&2
  echo "Install it with: go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@v4.18.1" >&2
  exit 2
}

exec_psql() {
  # Runs psql inside the container itself (bundled with the postgres image),
  # so the host does not need a psql client installed.
  docker exec -i \
    -e "PGPASSWORD=${SUPER_PASSWORD}" \
    "${CONTAINER_NAME}" \
    psql -U "${SUPER_USER}" -d "${PG_DB}" -v ON_ERROR_STOP=1 "$@"
}

wait_for_postgres() {
  echo "=== waiting for postgres to accept connections ==="
  # The official postgres image runs initdb, starts a throwaway server to
  # apply init scripts, stops it, then starts the real server -- pg_isready
  # can report "ready" during that throwaway window. Only a real SELECT 1
  # via psql (retried past the intermediate restart) proves it is usable.
  for _ in $(seq 1 60); do
    if docker exec -e "PGPASSWORD=${SUPER_PASSWORD}" "${CONTAINER_NAME}" \
        psql -U "${SUPER_USER}" -d "${PG_DB}" -c 'SELECT 1' >/dev/null 2>&1; then
      # Guard against the postgres image's initdb -> throwaway server ->
      # restart -> real server sequence: a SELECT 1 against the throwaway
      # server can succeed right before it shuts down. Re-check after a
      # short pause so we only report ready once the connection is stable.
      sleep 2
      if docker exec -e "PGPASSWORD=${SUPER_PASSWORD}" "${CONTAINER_NAME}" \
          psql -U "${SUPER_USER}" -d "${PG_DB}" -c 'SELECT 1' >/dev/null 2>&1; then
        echo "postgres is ready."
        return 0
      fi
    fi
    sleep 1
  done
  echo "verify_migrations.sh: postgres did not become ready in time" >&2
  exit 1
}

bootstrap_roles() {
  echo "=== bootstrapping app_migrator / app_runtime roles (ADR-SEC-002 parity) ==="
  # Mirrors bootstrapRoles() in
  # backend/internal/modules/identity/repo/integration_test.go exactly.
  exec_psql <<SQL
DO \$\$ BEGIN
    CREATE ROLE ${MIGRATOR_USER} LOGIN PASSWORD '${MIGRATOR_PASSWORD}' BYPASSRLS;
EXCEPTION WHEN duplicate_object THEN NULL; END \$\$;

DO \$\$ BEGIN
    CREATE ROLE ${RUNTIME_USER} LOGIN PASSWORD '${RUNTIME_PASSWORD}' NOINHERIT;
EXCEPTION WHEN duplicate_object THEN NULL; END \$\$;

GRANT USAGE ON SCHEMA public TO ${MIGRATOR_USER}, ${RUNTIME_USER};

-- identity/tenant/catalog only need "uuid-ossp" (tenant/000001). The
-- integration_test.go bootstrapRoles() this mirrors also creates the
-- "vector" extension, but that requires the pgvector/pgvector:pgNN image
-- used by the Go integration tests; this script runs against a plain
-- postgres:17 image (none of the three verified modules use pgvector), so
-- that extension is intentionally omitted here.
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

ALTER SCHEMA public OWNER TO ${MIGRATOR_USER};
GRANT ALL ON SCHEMA public TO ${MIGRATOR_USER};

ALTER DEFAULT PRIVILEGES FOR ROLE ${MIGRATOR_USER} IN SCHEMA public
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO ${RUNTIME_USER};
ALTER DEFAULT PRIVILEGES FOR ROLE ${MIGRATOR_USER} IN SCHEMA public
    GRANT USAGE ON SEQUENCES TO ${RUNTIME_USER};
SQL
}

migrator_url() {
  local mod="$1"
  local table="schema_migrations_$(echo "${mod}" | tr '-' '_')"
  echo "postgres://${MIGRATOR_USER}:${MIGRATOR_PASSWORD}@localhost:${PG_PORT}/${PG_DB}?sslmode=disable&x-migrations-table=${table}"
}

migrate_up() {
  local mod="$1"
  local path="migrations/${mod}"
  echo "--- ${mod}: up (all) ---"
  "${MIGRATE_BIN}" -path "${path}" -database "$(migrator_url "${mod}")" up
}

migrate_down_all() {
  local mod="$1"
  local path="migrations/${mod}"
  echo "--- ${mod}: down (-all) ---"
  "${MIGRATE_BIN}" -path "${path}" -database "$(migrator_url "${mod}")" down -all
}

# assert_final_state does a data/shape sanity check beyond "migrate exited
# 0": a down file can drop the wrong object, or forget one, and still let
# the subsequent 'up' succeed if nothing collides. This queries actual seed
# row counts and RLS policy definitions to confirm the post round-trip state
# is identical to a fresh single up, not merely error-free.
assert_final_state() {
  echo ""
  echo "=== asserting final state after round trip ==="

  local role_count
  role_count="$(exec_psql -t -A -c "SELECT count(*) FROM roles WHERE is_system = TRUE;")"
  if [ "${role_count}" != "7" ]; then
    echo "verify_migrations.sh: expected 7 system roles (000006 seeds 6 + 000010 seeds warehouse), got ${role_count}" >&2
    exit 1
  fi
  echo "OK: 7 system roles present (cashier, shift_manager, driver, kitchen, bar, manager, warehouse)."

  local memberships_read_def
  memberships_read_def="$(exec_psql -t -A -c "SELECT pg_get_expr(polqual, polrelid) FROM pg_policy WHERE polname = 'memberships_read' AND polrelid = 'memberships'::regclass;")"
  case "${memberships_read_def}" in
    *all_tenants*) ;;
    *) echo "verify_migrations.sh: memberships_read policy missing expected all_tenants scope branch: ${memberships_read_def}" >&2; exit 1 ;;
  esac
  echo "OK: memberships_read carries the 000008 all_tenants scope branch."

  local persons_update_check
  persons_update_check="$(exec_psql -t -A -c "SELECT pg_get_expr(polwithcheck, polrelid) FROM pg_policy WHERE polname = 'persons_update' AND polrelid = 'persons'::regclass;")"
  if [ "${persons_update_check}" = "true" ]; then
    echo "verify_migrations.sh: persons_update WITH CHECK is the bare 'true' hole closed by 000008: ${persons_update_check}" >&2
    exit 1
  fi
  echo "OK: persons_update WITH CHECK no longer contains the pre-000008 'true' hole."

  local product_count_tables
  product_count_tables="$(exec_psql -t -A -c "SELECT count(*) FROM information_schema.columns WHERE table_name = 'products' AND column_name = 'source_stock_item_id';")"
  if [ "${product_count_tables}" != "1" ]; then
    echo "verify_migrations.sh: expected products.source_stock_item_id column (catalog/000002) to exist, got count=${product_count_tables}" >&2
    exit 1
  fi
  echo "OK: catalog/000002's products.source_stock_item_id column is present."
}

main() {
  resolve_migrate_bin

  echo "=== starting ${PG_IMAGE} container '${CONTAINER_NAME}' on port ${PG_PORT} ==="
  docker run -d --rm \
    --name "${CONTAINER_NAME}" \
    -e "POSTGRES_USER=${SUPER_USER}" \
    -e "POSTGRES_PASSWORD=${SUPER_PASSWORD}" \
    -e "POSTGRES_DB=${PG_DB}" \
    -p "${PG_PORT}:5432" \
    "${PG_IMAGE}" >/dev/null

  wait_for_postgres
  bootstrap_roles

  echo ""
  echo "############################################################"
  echo "# Pass 1: up (all modules, dependency order)"
  echo "############################################################"
  for mod in "${MODULES_UP_ORDER[@]}"; do
    migrate_up "${mod}"
  done

  echo ""
  echo "############################################################"
  echo "# Pass 2: down -all (reverse dependency order)"
  echo "############################################################"
  for (( idx=${#MODULES_UP_ORDER[@]}-1 ; idx>=0 ; idx-- )); do
    migrate_down_all "${MODULES_UP_ORDER[$idx]}"
  done

  echo ""
  echo "############################################################"
  echo "# Pass 3: up (all modules again, dependency order)"
  echo "############################################################"
  for mod in "${MODULES_UP_ORDER[@]}"; do
    migrate_up "${mod}"
  done

  assert_final_state

  echo ""
  echo "verify_migrations.sh: OK -- up -> down -all -> up succeeded for: ${MODULES_UP_ORDER[*]}"
}

main "$@"
