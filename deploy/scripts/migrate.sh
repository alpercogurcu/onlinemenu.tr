#!/usr/bin/env bash
# Sunucu üzerinde tüm modül migration'larını uygular (app_migrator rolü).
# Postgres yalnız 127.0.0.1:5433'e açık (docker-compose.diverserver.yml);
# DSN sops ile çözülen .env'den türetilir. "verify" ile RLS kapsama denetimi.
#
#   deploy/scripts/migrate.sh up
#   deploy/scripts/migrate.sh verify
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
export PATH="$PATH:/usr/local/go/bin:/root/go/bin"

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

cd "$ROOT/backend"
DATABASE_URL="pgx5://app_migrator:${PW}@127.0.0.1:5433/${DB}?sslmode=disable" \
  go run ./cmd/migrate "${1:-up}" "${@:2}"
