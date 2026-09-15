#!/usr/bin/env bash
# Runs deploy/postgres/seed-first-tenant.sql on the server against the prod
# Postgres (app_migrator, 127.0.0.1:5433 — same DSN pattern as migrate.sh) to
# bootstrap the first pilot tenant + branch + manager membership. Idempotent
# (see the .sql file's header); safe to re-run with the same inputs.
#
#   FIRST_ADMIN_SUB=<keycloak-user-uuid> FIRST_ADMIN_EMAIL=... FIRST_ADMIN_NAME=... \
#   TENANT_NAME=... TENANT_SLUG=... BRANCH_NAME=... \
#   deploy/scripts/seed-first-tenant.sh
#
# or via: task deploy:seed:first-tenant FIRST_ADMIN_SUB=... FIRST_ADMIN_EMAIL=... ...
#
# FIRST_ADMIN_SUB is the Keycloak user id printed by
# deploy/scripts/keycloak-harden.sh when it creates the first admin user.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

: "${FIRST_ADMIN_SUB:?FIRST_ADMIN_SUB required (Keycloak user UUID)}"
: "${FIRST_ADMIN_EMAIL:?FIRST_ADMIN_EMAIL required}"
: "${FIRST_ADMIN_NAME:?FIRST_ADMIN_NAME required}"
: "${TENANT_NAME:?TENANT_NAME required}"
: "${TENANT_SLUG:?TENANT_SLUG required}"
: "${BRANCH_NAME:?BRANCH_NAME required}"

umask 077
# Reads one key from the sops-encrypted dotenv and prints it URL-encoded.
# Why: the value is embedded in a connection URL; base64-style passwords can
# contain "/" or "@", which would split the URL authority. Parsing in python
# also strips dotenv quoting instead of a fragile eval/export.
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

PW="$(env_url APP_MIGRATOR_PASSWORD)"; : "${PW:?APP_MIGRATOR_PASSWORD .env.prod.sops içinde yok}"
DB="$(env_url POSTGRES_DB)"; DB="${DB:-onlinemenu}"
DSN="postgres://app_migrator:${PW}@127.0.0.1:5433/${DB}?sslmode=disable"
SQL_FILE="$ROOT/deploy/postgres/seed-first-tenant.sql"

PSQL_VARS=(
  -v "tenant_name=${TENANT_NAME}"
  -v "tenant_slug=${TENANT_SLUG}"
  -v "branch_name=${BRANCH_NAME}"
  -v "admin_email=${FIRST_ADMIN_EMAIL}"
  -v "admin_name=${FIRST_ADMIN_NAME}"
  -v "keycloak_sub=${FIRST_ADMIN_SUB}"
)

if command -v psql >/dev/null 2>&1; then
  psql "$DSN" -v ON_ERROR_STOP=1 "${PSQL_VARS[@]}" -f "$SQL_FILE"
else
  echo "psql bulunamadı — dockerized psql'e (--network host) düşülüyor"
  docker run --rm --network host \
    -v "$SQL_FILE:/seed-first-tenant.sql:ro" \
    postgres:17-alpine \
    psql "$DSN" -v ON_ERROR_STOP=1 "${PSQL_VARS[@]}" -f /seed-first-tenant.sql
fi

echo "OK  first tenant seed uygulandı (slug=${TENANT_SLUG})"
