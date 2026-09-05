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

check() {
  local name="$1" url="$2"
  if curl -fsS -o /dev/null "$url"; then
    echo "OK   ${name} (${url})"
  else
    echo "FAIL ${name} (${url})"
    status=1
  fi
}

check "api /healthz"  "${API_URL%/}/healthz"
check "api /readyz"   "${API_URL%/}/readyz"
check "admin /"       "${ADMIN_URL%/}/"
check "menu /"        "${MENU_URL%/}/"

exit "${status}"
