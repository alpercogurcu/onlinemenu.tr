#!/usr/bin/env bash
# Prod deploy smoke test (Task 7, Ops asgarisi).
#
# Her kontrol bağımsızdır ve tek başına başarısız olabilir; script'i ilk
# hatada durdurmuyoruz (set -e YOK) çünkü tüm servislerin durumunu tek
# çalıştırmada görmek istiyoruz — deploy sonrası "hangi servis düştü"
# sorusuna tek satırlık çıktı listesiyle cevap verir.
set -uo pipefail

API_URL="${API_URL:?API_URL gerekli (ör. https://api.example.com)}"
ADMIN_URL="${ADMIN_URL:?ADMIN_URL gerekli (ör. https://admin.example.com)}"
MENU_URL="${MENU_URL:?MENU_URL gerekli (ör. https://menu.example.com)}"

status=0

# Next.js konteynerleri `up -d` sonrası 20-40 sn boyunca 502 döner; ilk denemede
# FAIL basmak her yayında yanlış alarm üretiyordu. SMOKE_WAIT_SECONDS (varsayılan
# 90) boyunca 3 sn aralıkla yeniden dener.
check() {
  local name="$1" url="$2" deadline=$(( $(date +%s) + ${SMOKE_WAIT_SECONDS:-90} ))
  while :; do
    if curl -fsS --max-time 10 --connect-timeout 5 -o /dev/null "$url"; then
      echo "OK   ${name} (${url})"
      return
    fi
    if (( $(date +%s) >= deadline )); then
      echo "FAIL ${name} (${url})"
      status=1
      return
    fi
    sleep 3
  done
}

check "api /healthz"  "${API_URL%/}/healthz"
check "api /readyz"   "${API_URL%/}/readyz"
check "admin /"       "${ADMIN_URL%/}/"
check "menu /"        "${MENU_URL%/}/"

# Alertmanager — observability profiliyle opsiyonel, host'a port AÇMAZ (bkz.
# docker-compose.prod.yml), bu yüzden buradan da curl edilemez. Konteyner
# ayakta değilse (profil kapalı veya prod'da observability hiç kurulmadıysa)
# kontrol sessizce atlanır — bu bir FAIL değildir.
COMPOSE_FILE="${COMPOSE_FILE:-$(dirname "$0")/docker-compose.prod.yml}"
if command -v docker >/dev/null 2>&1 \
  && docker compose -f "${COMPOSE_FILE}" ps --status running --services 2>/dev/null | grep -qx alertmanager; then
  if docker compose -f "${COMPOSE_FILE}" exec -T alertmanager \
    wget -q --spider http://localhost:9093/-/ready; then
    echo "OK   alertmanager /-/ready (container-internal)"
  else
    echo "FAIL alertmanager /-/ready (container-internal)"
    status=1
  fi
fi

exit "${status}"
