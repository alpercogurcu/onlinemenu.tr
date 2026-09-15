#!/usr/bin/env bash
# Lokal makineden repo'yu prod sunucusuna rsync ile gönderir (b2b'deki
# deploy-remote.sh deseni). Sunucu git reposu DEĞİLDİR.
#
#   task deploy:sync            (DEPLOY_HOST=diverserver, DEPLOY_DIR=/opt/onlinemenu)
#
# Korunanlar (gönderilmez, --delete ile sunucudan da silinmez): deploy/.env
# (sops çözümü), deploy/.api-image, deploy/.git-sha sunucu tarafında yeniden
# yazılır. .env.prod.sops GÖNDERİLİR (şifreli, git'te de o var).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DEPLOY_HOST="${DEPLOY_HOST:-diverserver}"
DEPLOY_DIR="${DEPLOY_DIR:-/opt/onlinemenu}"

SHA="$(git -C "$ROOT" rev-parse --short HEAD)"
if [[ -n "$(git -C "$ROOT" status --porcelain --untracked-files=no)" ]]; then
  SHA="${SHA}-dirty"
fi
echo "$SHA" > "$ROOT/deploy/.git-sha"

echo "rsync → ${DEPLOY_HOST}:${DEPLOY_DIR}  (@ ${SHA})"
rsync -az --delete --info=stats1 \
  --exclude '.git' \
  --exclude '.DS_Store' \
  --exclude '.claude' \
  --exclude '.superpowers' \
  --exclude '.playwright-mcp' \
  --exclude 'node_modules' \
  --exclude 'web/apps/*/.next' \
  --exclude 'web/apps/*/out' \
  --exclude 'web/apps/pos-desktop/build' \
  --exclude 'web/apps/pos-desktop/frontend/wailsjs' \
  --exclude 'backend/tmp' \
  --exclude 'backend/.env' \
  --exclude 'deploy/.env' \
  --exclude 'deploy/.env.prod' \
  --exclude 'deploy/.env.*.local' \
  --exclude 'deploy/.api-image' \
  --exclude 'deploy/alertmanager/smtp_password' \
  "$ROOT/" "${DEPLOY_HOST}:${DEPLOY_DIR}/"

ssh "$DEPLOY_HOST" "chmod +x ${DEPLOY_DIR}/deploy/scripts/*.sh ${DEPLOY_DIR}/deploy/smoke.sh"
echo "OK  sync tamam (${SHA})"
