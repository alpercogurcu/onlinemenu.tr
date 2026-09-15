#!/usr/bin/env bash
# Sunucu üzerinde docker compose sarmalayıcısı.
#   - deploy/.env.prod.sops → deploy/.env (sops -d, age anahtarı sunucuda)
#   - prod compose + sunucuya özgü katman (diverserver) birlikte
#   - API_IMAGE: build-api.sh'ın yazdığı deploy/.api-image dosyasından (varsa)
#
# Kullanım (sunucuda, repo kökünde):
#   deploy/scripts/compose.sh up -d
#   deploy/scripts/compose.sh ps
#   deploy/scripts/compose.sh logs -f api
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DEPLOY="$ROOT/deploy"
OVERLAY="${COMPOSE_OVERLAY:-$DEPLOY/docker-compose.diverserver.yml}"

if [[ ! -f "$DEPLOY/.env.prod.sops" ]]; then
  echo "HATA: $DEPLOY/.env.prod.sops yok — önce 'task deploy:secrets:encrypt' ile üretip sync edin." >&2
  exit 1
fi

# Düz metin .env yalnızca compose'un okuması için üretilir; root:600.
umask 077
sops -d --input-type dotenv --output-type dotenv "$DEPLOY/.env.prod.sops" > "$DEPLOY/.env"

if [[ -f "$DEPLOY/.api-image" ]]; then
  export API_IMAGE="$(cat "$DEPLOY/.api-image")"
  export API_IMAGE_TAG="${API_IMAGE##*:}"
fi

exec docker compose \
  --project-directory "$DEPLOY" \
  --env-file "$DEPLOY/.env" \
  -f "$DEPLOY/docker-compose.prod.yml" \
  -f "$OVERLAY" \
  "$@"
