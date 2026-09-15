#!/usr/bin/env bash
# Keycloak prod sertleştirme — Keycloak'ın İLK açılışından sonra, sunucuda
# çalıştırılır. Host'tan 127.0.0.1:8090'a (compose'un yayımladığı port) konuşur.
#
# NEDEN BU SCRIPT VAR: realm import'u TEK SEFERLİKTİR. Keycloak açılışta
# "import-at-startup" stratejisini IGNORE_EXISTING ile uygular; realm bir kez
# yaratıldıktan sonra realm-onlinemenu.json bir daha okunmaz. Bu yüzden buradaki
# silmeler kalıcıdır ve dev artefaktları (password-grant client'ları, seed
# kullanıcı) prod realm'inde bırakılmaz (deploy/keycloak/README.md: "Üretim
# realm'inde dev client'lar ve seed kullanıcı bulunmamalıdır").
#
# İdempotenttir: yoksa atlar, varsa dokunmaz. Tekrar çalıştırmak güvenlidir.
#
#   deploy/scripts/keycloak-harden.sh
#   FIRST_ADMIN_EMAIL=ad@ornek.com FIRST_ADMIN_NAME="Ad Soyad" \
#     deploy/scripts/keycloak-harden.sh
#
# Ortam değişkenleri (hepsi opsiyonel):
#   KC_URL                            Keycloak taban adresi (vars. http://127.0.0.1:8090)
#   KEYCLOAK_REALM                    vars. onlinemenu
#   KEYCLOAK_ADMIN_CLIENT_SECRET_OUT  secret'ı Vault yerine bu dosyaya yaz (umask 077)
#   VAULT_TOKEN                       verilmezse /root/.onlinemenu-vault-init.json'daki
#                                     root_token okunur (.env'deki token api'nindir,
#                                     onlinemenu-api policy'si yalnız READ yetkili)
#   VAULT_INIT_FILE                   yukarıdaki dosyanın yolu (vars. /root/.onlinemenu-vault-init.json)
#   FIRST_ADMIN_EMAIL                 verilmezse ilk yönetici adımı ATLANIR
#   FIRST_ADMIN_NAME                  "Ad Soyad" (son kelime soyad sayılır)
#   FIRST_ADMIN_TEMP_PASSWORD         verilmezse üretilir ve yalnız stderr'e yazılır
#
# Çıktı sözleşmesi: stdout'a YALNIZCA "FIRST_ADMIN_SUB=<uuid>" yazılır (seed SQL
# bu değeri persons.keycloak_sub olarak kullanır). Diğer her şey stderr'e gider.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DEPLOY="$ROOT/deploy"
KC_URL="${KC_URL:-http://127.0.0.1:8090}"
REALM="${KEYCLOAK_REALM:-onlinemenu}"

log() { printf '%s\n' "$*" >&2; }
die() { printf 'HATA: %s\n' "$*" >&2; exit 1; }

for cmd in curl python3 sops; do
  command -v "$cmd" >/dev/null 2>&1 || die "$cmd bulunamadı."
done

TMP="$(mktemp -d)"
cleanup() { rm -rf "$TMP"; }
trap cleanup EXIT
umask 077

BODY="$TMP/body.json"

# -----------------------------------------------------------------------------
# .env.prod.sops — bootstrap admin kimliği (compose.sh ile aynı kaynak)
# -----------------------------------------------------------------------------
[[ -f "$DEPLOY/.env.prod.sops" ]] || die "$DEPLOY/.env.prod.sops yok."
# .sops uzantısından dosya tipi çıkarılamıyor — tip açıkça verilmeli
# (compose.sh / migrate.sh / seed-first-tenant.sh ile aynı biçim).
sops -d --input-type dotenv --output-type dotenv "$DEPLOY/.env.prod.sops" > "$TMP/env"

env_val() {
  python3 - "$TMP/env" "$1" <<'PY'
import sys
path, key = sys.argv[1], sys.argv[2]
for line in open(path, encoding="utf-8"):
    line = line.strip()
    if not line or line.startswith("#") or "=" not in line:
        continue
    k, v = line.split("=", 1)
    if k.strip() == key:
        v = v.strip()
        if len(v) >= 2 and v[0] == v[-1] and v[0] in "\"'":
            v = v[1:-1]
        print(v)
        break
PY
}

ADMIN_USER="$(env_val KEYCLOAK_BOOTSTRAP_ADMIN_USERNAME)"
ADMIN_PASS="$(env_val KEYCLOAK_BOOTSTRAP_ADMIN_PASSWORD)"
[[ -n "$ADMIN_USER" && -n "$ADMIN_PASS" ]] ||
  die "KEYCLOAK_BOOTSTRAP_ADMIN_USERNAME/PASSWORD .env.prod.sops içinde yok."

# -----------------------------------------------------------------------------
# JSON / URL yardımcıları (jq yok, python3 var)
# -----------------------------------------------------------------------------
json_get() { # stdin: object — json_get <alan>
  python3 -c 'import json,sys
try: d = json.load(sys.stdin)
except ValueError: sys.exit(0)
print(d.get(sys.argv[1], "") if isinstance(d, dict) else "")' "$1"
}

json_first_id() { # stdin: array — ilk elemanın id'si, yoksa boş
  python3 -c 'import json,sys
try: d = json.load(sys.stdin)
except ValueError: sys.exit(0)
print(d[0]["id"] if isinstance(d, list) and d else "")'
}

urlenc() { python3 -c 'import sys,urllib.parse;print(urllib.parse.quote(sys.argv[1], safe=""))' "$1"; }

# Vault init çıktısı: "vault operator init -format=json" sonucunun saklandığı
# dosya (yalnız bu sunucuda, root:600). VAULT_TOKEN verilmediğinde root token
# buradan okunur — .env'deki token api'nindir ve yazma yetkisi yoktur.
VAULT_INIT_FILE="${VAULT_INIT_FILE:-/root/.onlinemenu-vault-init.json}"
vault_root_token() {
  [[ -r "$VAULT_INIT_FILE" ]] || return 1
  python3 -c 'import json,sys
try: d = json.load(open(sys.argv[1], encoding="utf-8"))
except Exception: sys.exit(1)
t = d.get("root_token", "")
if not t: sys.exit(1)
print(t)' "$VAULT_INIT_FILE"
}

# -----------------------------------------------------------------------------
# Admin REST API
#
# Not: realm'in sslRequired="external" ayarı HTTP'yi yalnız özel/loopback
# adresler için serbest bırakır. Bu script host'tan konuştuğu için istek
# Keycloak'a docker köprüsünün özel IP'sinden ulaşır — HTTP kabul edilir.
# -----------------------------------------------------------------------------
# Token dosyada tutulur, değişkende değil: api() komut ikamesi (subshell)
# içinde çağrılıyor — orada tazelenen bir değişken dışarı sızmaz.
TOKEN_FILE="$TMP/token"
kc_login() {
  printf 'client_id=admin-cli&grant_type=password&username=%s&password=%s' \
    "$(urlenc "$ADMIN_USER")" "$(urlenc "$ADMIN_PASS")" > "$TMP/login"
  { curl -sS -X POST \
      -H 'Content-Type: application/x-www-form-urlencoded' \
      --data-binary "@$TMP/login" \
      "$KC_URL/realms/master/protocol/openid-connect/token" || true
  } | json_get access_token > "$TOKEN_FILE"
  [[ -s "$TOKEN_FILE" ]] || die "master realm'den admin token alınamadı ($KC_URL)."
}

_api_raw() {
  local method="$1" path="$2" data="${3:-}"
  local args=(-sS -o "$BODY" -w '%{http_code}' -X "$method"
    -H "Authorization: Bearer $(cat "$TOKEN_FILE")" -H 'Accept: application/json')
  if [[ -n "$data" ]]; then
    printf '%s' "$data" > "$TMP/req.json"
    args+=(-H 'Content-Type: application/json' --data-binary "@$TMP/req.json")
  fi
  curl "${args[@]}" "$KC_URL$path"
}

api() { # api <METHOD> <PATH> [JSON] — HTTP kodunu yazar, gövde $BODY'de
  local code
  code="$(_api_raw "$@")"
  # Admin token'ı kısa ömürlüdür; uzun adımlardan (Vault exec) sonra tazelenir.
  if [[ "$code" == "401" ]]; then
    kc_login
    code="$(_api_raw "$@")"
  fi
  printf '%s' "$code"
}

kc_login
code="$(api GET "/admin/realms/$REALM")"
[[ "$code" == "200" ]] || die "'$REALM' realm'i okunamadı (HTTP $code) — Keycloak açık ve import bitmiş mi?"
log "Keycloak: $KC_URL, realm: $REALM"

# -----------------------------------------------------------------------------
# a/b) Dev artefaktlarını sil
# -----------------------------------------------------------------------------
client_uuid() { # clientId -> iç id (yoksa boş)
  local code
  code="$(api GET "/admin/realms/$REALM/clients?clientId=$(urlenc "$1")")"
  [[ "$code" == "200" ]] || die "client sorgusu başarısız ($1, HTTP $code)"
  json_first_id < "$BODY"
}

for dev_client in onlinemenu-dev-cli onlinemenu-dev-shortlived; do
  uuid="$(client_uuid "$dev_client")"
  if [[ -z "$uuid" ]]; then
    log "atla   client $dev_client (yok)"
    continue
  fi
  code="$(api DELETE "/admin/realms/$REALM/clients/$uuid")"
  [[ "$code" == "204" ]] || die "client silinemedi ($dev_client, HTTP $code)"
  log "SİLİNDİ client $dev_client"
done

code="$(api GET "/admin/realms/$REALM/users?username=dev-cashier&exact=true")"
[[ "$code" == "200" ]] || die "dev-cashier sorgusu başarısız (HTTP $code)"
dev_user="$(json_first_id < "$BODY")"
if [[ -z "$dev_user" ]]; then
  log "atla   kullanıcı dev-cashier (yok)"
else
  code="$(api DELETE "/admin/realms/$REALM/users/$dev_user")"
  [[ "$code" == "204" ]] || die "dev-cashier silinemedi (HTTP $code)"
  log "SİLİNDİ kullanıcı dev-cashier"
fi

# -----------------------------------------------------------------------------
# c) onlinemenu-admin-api client secret'ı — ASLA stdout'a yazılmaz
# -----------------------------------------------------------------------------
admin_api_uuid="$(client_uuid onlinemenu-admin-api)"
[[ -n "$admin_api_uuid" ]] || die "onlinemenu-admin-api client'ı realm'de yok — import eksik."

code="$(api GET "/admin/realms/$REALM/clients/$admin_api_uuid/client-secret")"
[[ "$code" == "200" ]] || die "client secret okunamadı (HTTP $code)"
SECRET="$(json_get value < "$BODY")"
[[ -n "$SECRET" ]] || die "client secret boş döndü."

if [[ -n "${KEYCLOAK_ADMIN_CLIENT_SECRET_OUT:-}" ]]; then
  printf '%s' "$SECRET" > "$KEYCLOAK_ADMIN_CLIENT_SECRET_OUT"
  chmod 600 "$KEYCLOAK_ADMIN_CLIENT_SECRET_OUT"
  log "YAZILDI client secret -> $KEYCLOAK_ADMIN_CLIENT_SECRET_OUT (0600)"
else
  # .env'deki VAULT_TOKEN api'nin token'ıdır: onlinemenu-api policy'si yalnız
  # READ yetkili, bu path'e YAZAMAZ. Yazma için root token gerekir; sunucuda
  # /root/.onlinemenu-vault-init.json (root:600) içinde durur.
  VAULT_TOKEN="${VAULT_TOKEN:-$(vault_root_token || true)}"
  [[ -n "$VAULT_TOKEN" ]] ||
    die "VAULT_TOKEN gerekli (ya da KEYCLOAK_ADMIN_CLIENT_SECRET_OUT verin)."
  compose="$DEPLOY/scripts/compose.sh"
  [[ -x "$compose" ]] || die "$compose çalıştırılabilir değil."

  vault_exec() {
    "$compose" exec -T \
      -e VAULT_ADDR=http://127.0.0.1:8200 \
      -e VAULT_TOKEN="$VAULT_TOKEN" \
      vault "$@"
  }

  # Vault "server" modunda açılır: restart sonrası SEALED'dir ve KV mount'u
  # KENDİLİĞİNDEN GELMEZ. İkisini de önden söylemezsek "kv put" anlaşılmaz bir
  # hata verir.
  # Çıktılar önce değişkene alınır: "vault status" sealed'da 2 döner ve boru
  # hattında pipefail ile karışır.
  vault_status="$(vault_exec vault status 2>/dev/null || true)"
  if ! grep -Eq '^Sealed[[:space:]]+false' <<<"$vault_status"; then
    die "Vault sealed (veya erişilemiyor). Önce: compose.sh exec vault vault operator unseal <key>"
  fi
  vault_mounts="$(vault_exec vault secrets list -format=json 2>/dev/null || true)"
  if ! python3 -c 'import json,sys
try: d = json.load(sys.stdin)
except ValueError: sys.exit(1)
sys.exit(0 if "secret/" in d else 1)' <<<"$vault_mounts"; then
    die "Vault'ta 'secret/' KV mount'u yok ('vault server' modu KV mount'u kendiliğinden açmaz). Önce:
  deploy/scripts/compose.sh exec -T -e VAULT_ADDR=http://127.0.0.1:8200 -e VAULT_TOKEN=... \\
    vault vault secrets enable -path=secret kv-v2"
  fi

  # "client_secret=-" değeri stdin'den okur (Vault kv-builder) — secret argv'ye,
  # yani host'un süreç listesine düşmez.
  printf '%s' "$SECRET" | vault_exec vault kv put \
    secret/keycloak/admin-client client_secret=- >/dev/null
  log "YAZILDI client secret -> Vault secret/keycloak/admin-client (key: client_secret)"
fi
unset SECRET

# -----------------------------------------------------------------------------
# c2) admin-panel prod adresi — realm İMPORT EDİLMİŞ OLSA BİLE düzeltir
#
# realm-onlinemenu.json'daki "${ADMIN_PUBLIC_URL:...}" placeholder'ı yalnızca
# realm'in ilk import'unda çözülür. Keycloak ADMIN_PUBLIC_URL tanımlanmadan
# bir kez açıldıysa client localhost'ta kalır ve prod login'i sessizce
# "Invalid redirect_uri" verir. Bu adım eksik girdileri EKLER, hiçbir şeyi
# silmez (localhost girdileri dev'de kullanışlı, zarar vermiyor).
# -----------------------------------------------------------------------------
if [[ -n "${ADMIN_PUBLIC_URL:-}" ]]; then
  admin_panel_uuid="$(client_uuid admin-panel)"
  [[ -n "$admin_panel_uuid" ]] || die "admin-panel client'ı realm'de yok — import eksik."
  code="$(api GET "/admin/realms/$REALM/clients/$admin_panel_uuid")"
  [[ "$code" == "200" ]] || die "admin-panel client'ı okunamadı (HTTP $code)"

  # Gövde stdin'den DEĞİL dosya yolundan okunur: "python3 -" programı zaten
  # stdin'den (heredoc) alıyor.
  merged="$(python3 - "${ADMIN_PUBLIC_URL%/}" "$BODY" <<'PY'
import json, sys
base = sys.argv[1]
with open(sys.argv[2], encoding="utf-8") as fh:
    c = json.load(fh)
changed = False
for field, wanted in (("redirectUris", base + "/*"), ("webOrigins", base)):
    values = c.get(field) or []
    if wanted not in values:
        c[field] = values + [wanted]
        changed = True
print(json.dumps(c) if changed else "")
PY
)"
  if [[ -z "$merged" ]]; then
    log "atla   admin-panel redirect/webOrigin ($ADMIN_PUBLIC_URL zaten kayıtlı)"
  else
    code="$(api PUT "/admin/realms/$REALM/clients/$admin_panel_uuid" "$merged")"
    [[ "$code" == "204" ]] || die "admin-panel güncellenemedi (HTTP $code): $(cat "$BODY")"
    log "EKLENDİ admin-panel redirect '$ADMIN_PUBLIC_URL/*' + webOrigin '$ADMIN_PUBLIC_URL'"
  fi
else
  log "atla   admin-panel adres denetimi (ADMIN_PUBLIC_URL verilmedi)"
fi

# -----------------------------------------------------------------------------
# d) İlk yönetici kullanıcı
# -----------------------------------------------------------------------------
if [[ -z "${FIRST_ADMIN_EMAIL:-}" ]]; then
  log "atla   ilk yönetici (FIRST_ADMIN_EMAIL verilmedi). Sonra:"
  log "       FIRST_ADMIN_EMAIL=... FIRST_ADMIN_NAME='Ad Soyad' $0"
  exit 0
fi

code="$(api GET "/admin/realms/$REALM/users?username=$(urlenc "$FIRST_ADMIN_EMAIL")&exact=true")"
[[ "$code" == "200" ]] || die "kullanıcı sorgusu başarısız (HTTP $code)"
first_admin="$(json_first_id < "$BODY")"

if [[ -n "$first_admin" ]]; then
  # Var olan kullanıcıya DOKUNULMAZ: parolayı sıfırlamak ya da UPDATE_PASSWORD'ü
  # yeniden eklemek, parolasını çoktan belirlemiş yöneticiyi kilitler.
  log "atla   kullanıcı $FIRST_ADMIN_EMAIL (zaten var, değiştirilmedi)"
else
  TEMP_PASS="${FIRST_ADMIN_TEMP_PASSWORD:-}"
  generated=false
  if [[ -z "$TEMP_PASS" ]]; then
    command -v openssl >/dev/null 2>&1 || die "FIRST_ADMIN_TEMP_PASSWORD verilmedi ve openssl yok."
    TEMP_PASS="$(openssl rand -base64 12)"
    generated=true
  fi

  # Parola argv'den DEĞİL ortamdan geçirilir: argv süreç listesinde görünür.
  payload="$(KC_TEMP_PASS="$TEMP_PASS" python3 - "$FIRST_ADMIN_EMAIL" "${FIRST_ADMIN_NAME:-}" <<'PY'
import json, os, sys
email, full_name, password = sys.argv[1], sys.argv[2].strip(), os.environ["KC_TEMP_PASS"]
first, last = full_name, ""
if " " in full_name:
    first, last = full_name.rsplit(" ", 1)
print(json.dumps({
    "username": email,
    "email": email,
    "emailVerified": True,
    "enabled": True,
    "firstName": first,
    "lastName": last,
    "requiredActions": ["UPDATE_PASSWORD"],
    "credentials": [{"type": "password", "value": password, "temporary": True}],
}))
PY
)"

  code="$(api POST "/admin/realms/$REALM/users" "$payload")"
  [[ "$code" == "201" ]] || die "kullanıcı oluşturulamadı (HTTP $code): $(cat "$BODY")"
  log "OLUŞTU kullanıcı $FIRST_ADMIN_EMAIL (requiredActions: UPDATE_PASSWORD)"
  if [[ "$generated" == true ]]; then
    log "GEÇİCİ PAROLA (bir kez gösterilir, güvenli iletin): $TEMP_PASS"
  fi
  unset TEMP_PASS payload

  code="$(api GET "/admin/realms/$REALM/users?username=$(urlenc "$FIRST_ADMIN_EMAIL")&exact=true")"
  [[ "$code" == "200" ]] || die "oluşturulan kullanıcı okunamadı (HTTP $code)"
  first_admin="$(json_first_id < "$BODY")"
  [[ -n "$first_admin" ]] || die "oluşturulan kullanıcının id'si bulunamadı."
fi

printf 'FIRST_ADMIN_SUB=%s\n' "$first_admin"
