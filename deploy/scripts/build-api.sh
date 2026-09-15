#!/usr/bin/env bash
# Sunucu üzerinde api imajını ko ile yerel Docker daemon'a derler (registry
# yok — docs/deployment.md §7 kararı). Etiket = git kısa SHA (rsync ile
# gelen ağaçta .git yok; SHA sync.sh'ın yazdığı deploy/.git-sha dosyasından).
#
# Çıktı: deploy/.api-image  →  ko.local/onlinemenu/api:<sha>
# compose.sh bu dosyayı okuyup API_IMAGE olarak dışa aktarır.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
export PATH="$PATH:/usr/local/go/bin:/root/go/bin"

SHA="$(cat "$ROOT/deploy/.git-sha" 2>/dev/null || date +%Y%m%d%H%M%S)"
REPO="ko.local/onlinemenu/api"

cd "$ROOT/backend"
echo "ko build → $REPO:$SHA"
KO_DOCKER_REPO="$REPO" ko build --local --bare --tags "$SHA" --platform=linux/amd64 ./cmd/api >/dev/null

IMAGE="$REPO:$SHA"
docker image inspect "$IMAGE" >/dev/null
echo "$IMAGE" > "$ROOT/deploy/.api-image"
echo "OK  $IMAGE  (deploy/.api-image güncellendi)"
