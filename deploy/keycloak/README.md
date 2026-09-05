# Keycloak Realm — `onlinemenu`

Config-as-code Keycloak realm tanımı (ADR-AUTH-002: tek realm stratejisi).
Bu klasör docker-compose'da Keycloak'a `--import-realm` ile bağlanır.

## Dosyalar

| Dosya | Amaç |
|---|---|
| `realm-onlinemenu.json` | İçe aktarılabilir realm: client'lar, client scope'lar, mapper'lar, DEV seed kullanıcı |

## Başlıca Kavram: Keycloak `sub` DIŞINDA bir şey taşımaz

> **En önemli mimari nokta.** Backend, Keycloak access token'ından yetki bağlamı için **yalnızca `sub`** claim'ini okur (`backend/internal/platform/auth/keycloak_verifier.go` → `KeycloakClaims{Sub}`).
>
> `tenant_id`, `branch_ids`, `roles` **JWT'den DEĞİL, veritabanından** çözülür:
>
> ```
> Keycloak JWT (sub)  →  auth.Middleware  →  KeycloakVerifier  →  Principal{KeycloakSub}
>       →  GET /v1/identity/me/contexts          (persons.keycloak_sub → memberships)
>       →  POST /v1/identity/auth/context        (membership seçimi)
>       →  platform-signed CTX token             (tid/bid/rids)
>       →  Principal{TenantID, BranchID, RoleIDs}
> ```
>
> Bu, ADR-AUTH-001'in dört katmanlı modelidir. ADR-AUTH-002 başlangıçta JWT
> claim mapper'larını öngörmüştü; ancak uygulama DB-tabanlı iki aşamalı akışa
> evrildi. `onlinemenu-context-claims` scope'undaki `tenant_id`/`branch_ids`/`roles`
> mapper'ları **forward-looking / non-authoritative**'dır — backend onları
> tüketmez ve **yetki kaynağı değildir**. Gerçek client'larda (admin-panel,
> pos-desktop) bu scope **optional**'dır (varsayılan olarak token'a girmez).

## Client'lar

| clientId | Tip | Akış | Redirect | Not |
|---|---|---|---|---|
| `admin-panel` | public | Authorization Code + **PKCE (S256)** | `http://localhost:3000/*` | Next.js admin paneli (Wave 2) |
| `pos-desktop` | public | Authorization Code + **PKCE (S256)** | `http://127.0.0.1/callback`, `http://127.0.0.1:*/callback` | Wails masaüstü, RFC 8252 loopback (Wave 3) |
| `onlinemenu-dev-cli` | public | **Direct Access Grants (password)** | — | ⚠️ **DEV/TEST ONLY** — üretime alınmaz |
| `onlinemenu-dev-shortlived` | public | Direct Access Grants, `access.token.lifespan=1s` | — | ⚠️ **DEV/TEST ONLY** — expired-token testi için |

> Gerçek client'lar (`admin-panel`, `pos-desktop`) **PKCE-saf**tır:
> `directAccessGrantsEnabled=false`. Password grant yalnızca dev/test client'larında
> açıktır. Üretim realm'inde dev client'lar ve seed kullanıcı **bulunmamalıdır**.

## Client Scope'lar

| Scope | Atama | İçerik | Not |
|---|---|---|---|
| `basic` | default (tüm client'lar) | `sub`, `auth_time` mapper'ları | Realm import'unda `clientScopes` tanımlıysa Keycloak yerleşik `basic` scope'unu **otomatik oluşturmaz**; bu yüzden açıkça tanımlanır. Backend `sub`'a dayanır. |
| `onlinemenu-audience` | default (tüm client'lar) | `aud` = `onlinemenu-backend` (audience mapper) | Verifier `aud` doğrular (`KEYCLOAK_AUDIENCE=onlinemenu-backend`). Keycloak varsayılanı client'ı `azp`'ye koyar, `aud`'a değil — bu mapper olmadan token reddedilir. |
| `onlinemenu-context-claims` | admin/pos: **optional**; dev-cli: default | `tenant_id`, `branch_ids`, `roles` | **Non-authoritative**, backend tüketmez (yukarıdaki uyarı). |

> **Wave 2 notu — standart OIDC scope'ları:** Realm import'unda `clientScopes`
> tanımlandığında Keycloak yerleşik scope'ları (`profile`, `email`, `web-origins`,
> `roles`, `offline_access`) **otomatik oluşturmaz**. Bu realm yalnızca backend'in
> ihtiyaç duyduğu scope'ları (`basic`→`sub`, `onlinemenu-audience`→`aud`) tanımlar.
> Client dalgaları (admin-panel/pos-desktop) `web-origins` (CORS) ve `profile`/`email`
> claim'lerine ihtiyaç duyduğunda bu standart scope'lar realm'e **açıkça eklenmelidir**
> — aksi halde `web-origins` CORS mapper'ı çalışmaz. Tanımsız scope referansları
> import sırasında sessizce yok sayıldığı için client'lara eklenmemiştir.

### Backend ortam değişkenleri (üretim/staging)

```
KEYCLOAK_ISSUER_URL=http://<keycloak-host>:8090/realms/onlinemenu
KEYCLOAK_AUDIENCE=onlinemenu-backend
# opsiyonel: KEYCLOAK_JWKS_URL (varsayılan: ISSUER_URL + /protocol/openid-connect/certs)
```

## Realm İçe Aktarma (Import)

docker-compose (`deploy/docker-compose.dev.yml`) Keycloak servisinde:

```yaml
command: start-dev --import-realm
volumes:
  - ./keycloak:/opt/keycloak/data/import:ro
```

Elle çalıştırmak için:

```bash
task infra:up -- --profile auth      # (veya) docker compose --profile auth up keycloak
```

Keycloak Admin Console: <http://localhost:8090> (admin/admin — dev).
Realm well-known: <http://localhost:8090/realms/onlinemenu/.well-known/openid-configuration>

## DEV Seed Kullanıcı

| Alan | Değer |
|---|---|
| username | `dev-cashier` |
| password | `Passw0rd!` |
| email | `cashier@dev.onlinemenu.tr` |
| attributes | `tenant_id`, `branch_ids` (context-claims mapper'larını besler) |
| realm role | `cashier` |

> ⚠️ Yalnızca dev/test içindir. Token'ın `sub` değeri (Keycloak user id),
> DB köprüsünün çalışması için `persons.keycloak_sub` ile eşleşmelidir.
> Dev seed'de (`backend/deploy/dev-seed.sql`) `keycloak_sub` sabit bir placeholder
> (`dev-admin-sub`) kullanır; gerçek Keycloak login'i test etmek için ilgili
> person satırının `keycloak_sub`'ını token'daki `sub` ile eşleyin.

## Yeni Tenant Kullanıcısı Ekleme (Akış)

1. **Keycloak'ta kullanıcı oluştur** (Admin API veya Console): username/email + parola.
2. **Platform backend** (tenant oluşturulduğunda) Keycloak grubu/rolünü hazırlar
   ve `persons` tablosuna `keycloak_sub = <keycloak user id>` ile kişi yazar.
3. **Membership** tanımla: `memberships(person_id, tenant_id, branch_id, role_id, status='active')`.
   Kişinin bir tenant+şubedeki rollerini bu tablo belirler — **JWT değil**.
4. Kullanıcı login olur → `sub` taşıyan token → `/me/contexts` → `/auth/context`
   → CTX token → yetkili istekler.

> `tenant_id`/`branch_ids` user attribute'larını Keycloak'ta doldurmak **opsiyoneldir**
> ve yalnızca `onlinemenu-context-claims` (non-authoritative) scope'unu besler.
> Yetki için zorunlu olan **membership** kaydıdır.

## Uçtan Uca Test (gerçek Keycloak ile)

`backend/internal/e2e/keycloaklogin/login_integration_test.go` — testcontainers
ile gerçek Keycloak + Postgres ayağa kaldırır, bu realm'i import eder ve tüm
zinciri doğrular. `keycloak_integration` build tag'i ile korunur (yavaştır):

```bash
cd backend
go test -tags keycloak_integration ./internal/e2e/keycloaklogin/...
```

Kanıtladıkları:
- Gerçek Keycloak JWT → Verifier → `Principal.KeycloakSub` (`sub` eşleşmesi)
- `aud` = `onlinemenu-backend` doğrulaması (audience mapper çalışıyor)
- DB köprüsü: `SelectContext` → CTX token → `tenant_id`/`branch_id`/`roles`
- Yanlış audience reddi
- Süresi dolmuş token reddi

> **CI önerisi:** Bu test normal `go test ./...` içinde çalışmaz (build tag).
> CI'da ayrı bir job/step olarak (Docker gerektirir) koşturulmalı:
> `go test -tags keycloak_integration ./internal/e2e/keycloaklogin/...`

---

## Personel Daveti — Admin API Servis Hesabı (ADR-AUTH-003)

`POST /v1/identity/{tenantID}/staff` yöneticiye personel davet ettiriyor; backend
bunun için Keycloak Admin API'sine **yazma** yetkisiyle bağlanıyor. Bu bölüm o
bağlantının kurulması için gereken her şeyi listeler — koddaki değerler burada
belgelenmezse dağıtım sessizce çalışmaz.

### 1. Realm'de confidential client

`onlinemenu-admin-api` (ad serbest) adında bir client açın:

- **Client authentication:** ON (confidential)
- **Service accounts roles:** ON — `client_credentials` grant bunu gerektirir
- **Standard flow / Direct access grants:** OFF (bu client hiç kullanıcı adına oturum açmaz)

### 2. Servis hesabına verilecek roller — yalnız üçü

`realm-management` client'ından, servis hesabı kullanıcısına:

| Rol | Neden |
|---|---|
| `create-user` | Kullanıcı yaratmak |
| `query-users` | E-postayla var olanı bulmak — idempotency bunun üzerine kurulu |
| `manage-users` | Parola belirleme (execute-actions) e-postasını tetiklemek |

**Vermeyin:** `manage-realm`, `manage-clients`, `manage-authorization`,
`manage-identity-providers`. Ele geçirilen bir uygulama sunucusu kimlik
katmanının tamamını devredebilmemeli; bu iş için gerekli değiller.

### 3. Vault'taki sır — tam yol

Backend client secret'ı **Vault'tan** okur (`.env`'den değil):

```
mount : secret
path  : keycloak/admin-client
key   : client_secret
```

```bash
vault kv put secret/keycloak/admin-client client_secret='<client secret>'
```

### 4. Ortam değişkenleri

| Değişken | Zorunlu | Varsayılan | Not |
|---|---|---|---|
| `KEYCLOAK_ADMIN_CLIENT_ID` | — | *(boş)* | **Boşsa özellik tamamen kapalıdır**: config sıfır değerle döner, davet ucu çalışmaz. Üretimde doldurulmalı |
| `KEYCLOAK_ADMIN_BASE_URL` | evet* | — | `KEYCLOAK_ADMIN_CLIENT_ID` doluysa zorunlu; eksikse süreç başlangıçta durur |
| `KEYCLOAK_REALM` | hayır | `onlinemenu` | |

### 5. SMTP — artık şablonda var; realm import env'den okur

`realm-onlinemenu.json`'daki `smtpServer` bloğu `${SMTP_HOST}`, `${SMTP_PORT}`,
`${SMTP_FROM}`, `${SMTP_USER}`, `${SMTP_PASSWORD}`, `${SMTP_STARTTLS}`
placeholder'larını taşır. Bunlar Keycloak'ın **kendi** config placeholder
mekanizmasıdır (`docs/guides/server/importExport.adoc` — `${VAR_NAME}`
biçimi; `${ENV_VAR:fallback}` de desteklenir) — Spring/Helm'deki `${env.X}`
biçimi **değildir**, Keycloak öyle bir söz dizimini tanımıyor. `command:
start --import-realm` ile başlarken bu placeholder'lar container'ın ortam
değişkenlerinden çözülür; ayrı bir Admin Console adımı **gerekmez**.

`docker-compose.prod.yml`'daki `keycloak` servisi `SMTP_*` değişkenlerini
`.env.prod`'dan devralır (bkz. `deploy/.env.prod.example`). Değerler boş
bırakılırsa placeholder boş string'e çözülür — import patlamaz, sadece SMTP
devre dışı kalır (aşağıdaki davranış geçerli olur).

Realm'de SMTP yapılandırılmamışsa (veya `.env.prod`'da boş bırakılmışsa)
parola belirleme e-postası gönderilemez. Davet **başarılı sayılır** (person
ve membership zaten yazılmıştır, geri alınacak bir şey yoktur) ama yanıt
`notification_sent: false` ve `notification_error: "<gerçek hata>"` taşır,
ayrıca Warn seviyesinde loglanır.

Personel giriş yapamayacağı için üretimde SMTP yapılandırması **fiilen
zorunludur**; aksi halde her davet elle parola belirlemeyi gerektirir.

### 6. Dört yerde wiring — atlanırsa `go build` uyarmaz

`keycloak.Module` şu **dört** dosyada birden kayıtlı olmalı:
`cmd/api/main.go`, `cmd/api-core/main.go` ve bunların `main_test.go`'ları
(ikisi fx modül listesini `fx.ValidateApp` için bağımsız olarak yeniden bildirir).

Biri atlanırsa `go build ./...` **yeşil kalır**, DI grafiği ise çözülemez olur;
yalnız `go test ./cmd/...` yakalar. Yeni bir platform modülü eklerken aynı tuzak
geçerli.
