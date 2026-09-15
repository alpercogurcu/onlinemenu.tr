# Deployment — Hazırlık Notları

> Oluşturma: 2026-09-15 · Güncelleme: 2026-09-15 (akşam) · Durum: **ilk kurulum TAMAMLANDI — stack canlı, mock fiscal (bkz. §10)**
> Kardeş kaynaklar: [backlog-pilot.md §1](backlog-pilot.md) (prod kurulumu açık maddeleri),
> `deploy/docker-compose.prod.yml` başlığı (ilk kurulum sırası), `deploy/.env.prod.example`,
> `deploy/keycloak/README.md`, [ADR-OPS-001](adr/OPS-001-backup-dr.md).
> Deploy otomasyonu: `deploy/scripts/*.sh` + kök `Taskfile.yml`deki `deploy:*` görevleri
> (`task --list` ile listelenir) — bkz. §6 ve "Günlük operasyon".
> Sırlar bu dosyaya **yazılmaz**; nerede durdukları §2 ve §6'da belirtilir.

## 1. Özet ve bağlam

- Ürün Token/Beko ÖKC sertifikasyonuna **Diver Street Food için geliştirilen uygulama** olarak
  bildirildi. Bu yüzden ilk canlı ortam `diverstreetfood.com` alt alan adlarında açılır.
  `onlinemenu.tr` ileride ürün markası için kullanılabilir; şimdilik dokunulmaz (bkz. §7).
- Token uyumluluk testi başlamadan önce **dışarıdan erişilebilir bir ortam** şart
  (sözleşme 4.6: test altyapısı, kimliklendirme ve erişim Firma'dan). Webhook ucu genel
  bir HTTPS adresinde olmalı. Bu deploy, sertifikasyon takviminin kritik yolunda.
- 2026-09-15 akşamı ilk kurulum yapıldı: dört alan adı canlı, smoke testi yeşil (§10).

## 2. Alan adları ve DNS

### diverstreetfood.com — Cenuta

| Alan | Değer |
|---|---|
| Kayıtçı / DNS | Cenuta (cPanel) |
| Name server | `ns52.dnsowner.com`, `ns53.dnsowner.com` |
| cPanel giriş | URL, kullanıcı adı ve şifre **`deploy/.env.cenuta.local`** dosyasında (git dışı, `.gitignore: .env.*.local`) |
| SOA | `noc.cenuta.com`, seri 2026091504 |
| SPF | `v=spf1 include:_spf.cenuta.com ~all` |
| MX | `0 diverstreetfood.com.` (posta Cenuta'da) |

Mevcut kayıtlar (2026-09-15 `dig` ile alındı):

| Kayıt | Tür | Hedef | Not |
|---|---|---|---|
| `diverstreetfood.com` | A | `213.238.183.121` | Cenuta paylaşımlı hosting (rDNS `static.cenuta.com`), addon domain kökü `/home/httpdjfq/diverstreetfood.com` (statik site). `curl` varsayılan UA'sına 403 döner (sunucu tarafı bot filtresi), tarayıcı UA'sıyla 200 — gerçek ziyaretçi etkilenmez. |
| `www.diverstreetfood.com` | CNAME | `diverstreetfood.com` | |
| `b2b.diverstreetfood.com` | A | `77.92.144.75` | b2b prod sunucusu (§3) |

Alt alan adları — **onaylandı** (2026-09-15, §7):

| Alt alan adı | Servis | Konteyner portu (nginx hedefi) | Host loopback portu (yalnız yerel teşhis) |
|---|---|---|---|
| `pos.diverstreetfood.com` | admin paneli (Next.js) | `onlinemenu-admin:3000` | `127.0.0.1:3002` |
| `menu.diverstreetfood.com` | misafir QR menüsü | `onlinemenu-menu:3001` | `127.0.0.1:3001` |
| `api.diverstreetfood.com` | Go API + Token webhook | `onlinemenu-api:8080` | `127.0.0.1:8080` |
| `auth.diverstreetfood.com` | Keycloak | `onlinemenu-keycloak:8080` | `127.0.0.1:8090` |

nginx **konteyner** portlarına (alias üzerinden, `onlinemenu-edge` ağı) gider; host loopback
portları yalnız sunucu içinden `curl` için vardır, nginx yapılandırmasında kullanılmaz.

Dikkat: `MENU_PUBLIC_URL` ve `KEYCLOAK_HOSTNAME` admin imajına **derleme anında** gömülür ve
basılan QR kodların tabanıdır. Alt alan adı bir kez seçilince değiştirmek yeniden build ve
QR yeniden basımı demektir; bu yüzden isimler deploy öncesi kesinleşmeli.

DNS kayıtları Cenuta cPanel **UAPI** ile eklendi (dört A kaydı → `77.92.144.75`, seri
`2026091505`); Zone Editor'a elle girilmedi, otomasyon script'i kullanıldı. TTL 300 ile
girildi, yayılma doğrulandıktan sonra 3600'e çıkarılabilir.

### onlinemenu.tr — Natro

| Alan | Değer |
|---|---|
| Name server | `ns1.natrohost.com`, `ns2.natrohost.com` |
| A | `85.159.66.93` (cizgi.net.tr), `www` Natro yönlendirme CNAME'i |

Karar (§7): şimdilik Cenuta'ya taşınmıyor.

## 3. Hedef sunucu

### Seçenek A — mevcut b2b sunucusu (öneri: pilot ve sertifikasyon için yeterli)

| Alan | Değer |
|---|---|
| Host | `srv.diverstreetfood.com`, `77.92.144.75` (rDNS `vulut.com`) |
| SSH | `ssh diverserver` (`~/.ssh/config`: root, port 25416, `~/.ssh/diverserver` anahtarı) |
| OS | Ubuntu 24.04.4 LTS, kernel 6.8 |
| Kaynak | 8 vCPU, 17 GiB RAM (swap yok), 175 GB disk (80 GB boş, %53 dolu) |
| Docker | 29.6.1, Compose v5.3.0 |
| Güvenlik | UFW (25416, 80, 443), fail2ban `sshd` jail (bkz. b2b DEPLOYMENT.md) |
| Üzerinde koşan | `/opt/b2b` (b2b stack: nginx, backend, frontend, postgres 15, redis, minio), `/opt/ashorial-demo` (WordPress + MariaDB) |
| Cron | 02:00 b2b DB yedeği, 03:00 refresh-token temizliği, 03:17 ve 15:17 certbot renew |

Kısıtlar:

- **80/443 portları `b2b_nginx` konteynerinde.** Onlinemenu'nün ters proxy'si bu nginx'e
  eklenmeli ya da nginx başka porta alınıp önüne ortak bir proxy konmalı (§4).
- Onlinemenu compose'u tüm servisleri `127.0.0.1`'e bağlıyor; b2b nginx'i ve Online Menu'nün
  dört dış servisi elle yaratılan `onlinemenu-edge` ağını paylaşır (§4). b2b'nin kendi ağı
  **kullanılmaz**: orada da `postgres`/`redis` servis adları var, Keycloak ilk denemede
  b2b'nin Postgres'ine bağlanmaya çalışıp parola hatasıyla düştü.
- Bellek: `free`'nin gösterdiği ~12 GiB "used" ile konteyner RSS toplamının (~1.2 GiB)
  uyuşmazlığı çözüldü — VMware balloon driver'ın raporladığı 11,7 GB, hosting sağlayıcısı
  tarafında hipervizör seviyesinde balon alınmış bellek (host'un kendisi görmüyor, misafir
  VM'e "used" olarak yansıyor). İmaj derlemesi sırasında balon kendiliğinden geri çekildi
  (kullanılabilir bellek 4,7 → 15,8 GiB), yani baskı altında bellek geri geliyor; yine de
  sağlayıcıya (vulut.com) bellek rezervasyonu için **ticket açılması önerilir** (henüz
  açılmadı). Bu arada **4 GB swap eklendi**
  (`swappiness=10`) ve `docker builder prune` ile **28 GB disk** boşaltıldı — Onlinemenu
  stack'inin (Postgres, Keycloak, NATS, Vault, MinIO, api, menu, admin) çekirdek profilde
  ~3 GiB, observability profiliyle ~5 GiB ihtiyacı için yeterli baş payı var.
- Host üzerindeki Docker dışı `apache2` süreçleri **çözüldü**: `ashorial_wp` konteynerine ait
  (`/opt/ashorial-demo` stack'i, WordPress + MariaDB) — 80/443'ü dinlemiyor, b2b_nginx ile
  çakışmıyor. Kurulum öncesi ek bir aksiyon gerekmiyor.

### Seçenek B — ayrı VPS

Token testinden pilota kadar b2b ile aynı makinede olmak sertifikasyon sırasında b2b'yi
etkileme riski taşır (nginx değişikliği, yeniden başlatma). Ayrı bir 4 vCPU / 8 GiB VPS bu
riski sıfırlar; maliyet ve yönetim yükü karşılığı. Karar kullanıcıya ait (§7).

## 4. Ters proxy ve TLS (uygulandı, 2026-09-15)

b2b'nin sertifika altyapısı Ağustos 2026 arızasından sonra sertleştirildi
(`--network host`, günde iki kez renew, loglama, parmak-izi bazlı reload). Yeni alan adları
aynı mekanizmaya eklendi; ek cron ya da yeni araç yok.

Uygulanan tasarım:

1. **Paylaşımlı kenar ağı:** `docker network create onlinemenu-edge` (elle, hiçbir compose sahibi
   değil). b2b nginx (`onlinemenu.b2b/docker-compose.prod.yml`) ve Online Menu'nün
   `api`/`admin`/`menu`/`keycloak` servisleri (`deploy/docker-compose.diverserver.yml`,
   `onlinemenu-*` alias'larıyla) bu ağa katılır. `compose down` ağı silmez; b2b nginx Online
   Menu'den bağımsız yeniden başlayabilir.
2. **Vhost dosyası Online Menu reposunda:** `deploy/nginx/diverserver.conf`, b2b nginx'e
   `/opt/onlinemenu/deploy/nginx:/etc/nginx/onlinemenu:ro` mount + `include` ile girer
   (`deploy/nginx/README.md`). Upstream'ler `resolver 127.0.0.11` + değişkenli `proxy_pass` ile
   verilir; Online Menu kapalıyken b2b nginx yine açılır (502 döner).
3. **Sertifika:** tek Let's Encrypt sertifikası, dört SAN, dizin
   `/etc/letsencrypt/live/pos.diverstreetfood.com/`, geçerlilik **2026-12-14**. Alma komutu
   README'de; b2b'nin `renew-ssl.sh` cron'u `live/*` glob'uyla bunu da yeniler (ek işlem yok).
   ACME doğrulaması, b2b'nin `:80` bloğu eşleşmeyen host'lar için varsayılan sunucu olduğu ve
   `/.well-known/acme-challenge/` webroot'unu servis ettiği için nginx değişikliği **öncesinde**
   yapılabildi.
4. **Rate limit:** `api.` vhost'u b2b'nin `zone=api` (10 r/s per IP, burst 40) bölgesini paylaşır.
   Tek restoran pilotu için yeterli; POS filosu tek NAT arkasında büyürse b2b `http{}` bloğuna
   ayrı bir `limit_req_zone` eklenmeli (b2b config'ine dokunmak gerektiği için karar bekliyor).
5. **Reload kuralı:** vhost dosyası değişince `docker exec b2b_nginx nginx -t && docker exec
   b2b_nginx nginx -s reload`; yalnız mount/ağ tanımı değişince nginx yeniden yaratılır
   (`up -d nginx`, ~2 sn b2b kesintisi — bugün iki kez yapıldı).

Bilinen kırılganlık: `ssl_session_cache shared:SSL:50m` beş server bloğunda aynı ad/boyutla
tekrarlanıyor; biri farklı boyutla değiştirilirse nginx hiç başlamaz.

## 5. b2b'den devralınan deploy yöntemi ve farklar

b2b'de (`onlinemenu.b2b/DEPLOYMENT.md`): sunucu git reposu **değil**; lokal makineden
`scripts/deploy-remote.sh` (`make deploy`) kodu `rsync --delete` ile `/opt/b2b`'ye gönderir
(`.env.prod`, `backups/`, `uploads/` korunur), sunucuda `update.sh` yedek + build + restart +
health check yapar, dışarıdan `/health` doğrulanır. Aynı desen burada da uygulanabilir:
`rsync` → `/opt/onlinemenu`, sonra `sops -d` + `docker compose up`.

Onlinemenu'de b2b'den farklı olan noktalar:

| Konu | b2b | onlinemenu | Yapılacak |
|---|---|---|---|
| Backend imajı | Sunucuda Dockerfile ile build | `ko` ile üretilip **registry'den pinli çekilir** (`API_IMAGE`, `latest` yasak) | **Karar (§7): registry yok.** Sunucuda `ko build --local` ile `ko.local/onlinemenu/api:<git-sha>` etiketi — `task deploy:build:api` (`deploy/scripts/build-api.sh`). |
| Frontend imajları | Sunucuda build | `menu` ve `admin` compose içinde kaynaktan derlenir (`MENU_PUBLIC_URL`, `KEYCLOAK_HOSTNAME` gömülü) | Sunucuda `task deploy:build:web` (`deploy/scripts/compose.sh build menu admin`); Next.js derlemesi 2-4 GiB RAM ister, §3 bellek notu |
| Sırlar | Düz `.env.prod` (gitignore) | `.env.prod.sops` (SOPS + age) commit edilir, deploy anında `sops -d` | SOPS/age kuruldu: lokal (macOS) `~/Library/Application Support/sops/age/keys.txt`, sunucu (Linux) `/root/.config/sops/age/keys.txt`, alıcı public key'ler `.sops.yaml`'da. `task deploy:secrets:encrypt` / `deploy:secrets:edit` / `deploy:secrets:updatekeys` — `.sops` uzantısından dosya tipi çıkarılamadığı ve `.sops.yaml`'ın `creation_rules` altındaki `input_type`/`output_type` alanları geçersiz olduğu için üçü de `--input-type dotenv` ile (encrypt/edit ayrıca `--output-type dotenv` ile) çağrılır; `updatekeys` alt komutu `--output-type` bayrağını **desteklemiyor**, verilirse sessizce dosya adı sanılıp hataya düşer. |
| Kimlik | JWT (kendi) | Keycloak 26 (realm import, SMTP zorunlu) | **Karar (§7): Cenuta posta hesabı.** `mail.diverstreetfood.com:587`, STARTTLS, gönderen `noreply@diverstreetfood.com` (SPF zaten Cenuta'da) |
| Runtime sırlar | env | Vault (yalnız Keycloak admin-client secret) | `vault operator init` + unseal **elle**, Shamir anahtarları çevrimdışı kasada. Reboot sonrası unseal de elle tekrarlanır (bkz. "Günlük operasyon") |
| Sağlık ucu | `/health` | `/healthz` ve `/readyz` | `task deploy:smoke` (`API_URL`, `ADMIN_URL`, `MENU_URL`) |
| DB | Postgres 15, tek rol | pgvector/pg17, `app_migrator` + `app_runtime`, FORCE RLS; roller **ilk açılışta** `init.prod.sh` ile | Şifreler ilk `up`'tan önce kesinleşmeli; sonradan değişmez |
| Yedek | cron + `backup-db.sh` | Compose içi `postgres-backup` servisi, 6 saat RPO, opsiyonel S3 | `BACKUP_S3_*` boş bırakılırsa yedek **yalnızca yerel** (named volume) kalır — tek host kaybında yedek de kaybolur; doldurulursa `mc` ile herhangi bir S3-uyumlu hedefe de yüklenir (bkz. `deploy/backup/README.md`) |
| Mali cihaz | yok | Prod compose `FISCAL_DEVICE_TYPE: beko_x30tr_cloud` **sabit** ve `TOKENX_API_URL/AUTH_URL` `:?` ile zorunlu; kod tarafında varsayılan `mock` (`cmd/api/main.go`) | **Karar (§7): mock ile kurulum, satış yapılmaz.** `deploy/docker-compose.diverserver.yml`'de `FISCAL_DEVICE_TYPE=mock` .env'den okunur; Token bilgileri gelince `.env.prod.sops` güncellenip prod compose'un sabit değeri (`beko_x30tr_cloud`) devreye alınır |

## 6. Kurulum sırası (gerçek script/task adlarıyla — 2026-09-15'te 1-11 ve 14 uygulandı)

Sırlar: `deploy/.env.prod.example` → `deploy/.env.prod` (doldur) → `task deploy:secrets:encrypt`
→ `deploy/.env.prod.sops` (commit) → düz metin otomatik silinir. Sunucuda
`deploy/scripts/compose.sh` her çalıştığında `sops -d` ile `deploy/.env` üretir.

1. **Sunucu hazırlığı** — `/opt/onlinemenu` dizini, ilk `task deploy:sync`
   (`deploy/scripts/sync.sh`, rsync + `deploy/.git-sha`). SOPS/age kurulumu **tamamlandı**:
   lokal (macOS) age anahtarı `~/Library/Application Support/sops/age/keys.txt`, sunucu
   (Linux) `/root/.config/sops/age/keys.txt` — sops age anahtarını platforma göre farklı
   varsayılan dizinde arar, ikisi de git dışı, ayrıca çevrimdışı kasada yedek; iki tarafın da
   public key'i `.sops.yaml`'da.
2. `deploy/.env.prod.example` → doldur → `task deploy:secrets:encrypt` → commit → `task deploy:sync`
   ile sunucuya gönder.
3. Sunucuda çekirdek servisler: `deploy/scripts/compose.sh up -d postgres redis nats vault`
   (henüz ayrı bir `deploy:` task'ı yok — ilk kurulumda elle, `ssh diverserver` ile).
4. Vault init + unseal (**elle**). Yapılan: `-key-shares=1 -key-threshold=1`, çıktı sunucuda
   `/root/.onlinemenu-vault-init.json` (root:600) — **parola kasasına taşınıp sunucudan
   silinmeli**; `secret/` kv-v2 mount'u açıldı; `onlinemenu-api` policy'si (yalnız
   `secret/keycloak/*` okuma); api token'ı orphan, TTL 1 yıl → **2027-09-15'te dolar**, api
   yenilemiyor (backlog). Token `.env.prod.sops`'ta `VAULT_TOKEN`.
5. `deploy/scripts/compose.sh up -d keycloak` → Keycloak sertleştirme:
   `deploy/scripts/keycloak-harden.sh` (dev client/kullanıcı temizliği, `onlinemenu-admin-api`
   client secret'ını Vault'a yazma, ilk yönetici kullanıcısını oluşturup `FIRST_ADMIN_SUB`
   basma — bkz. `deploy/keycloak/README.md`).
6. `task deploy:build:api` (sunucuda `ko build --local`).
7. `task deploy:build:web` (`menu` + `admin`, kaynaktan derleme).
8. `task deploy:migrate` (tüm modül migration'ları, `app_migrator`).
9. `task deploy:up` (tam stack: prod compose + `docker-compose.diverserver.yml`).
10. Ters proxy + sertifika (§4) — `deploy/nginx/diverserver.conf` + b2b nginx include.
11. `API_URL=… ADMIN_URL=… MENU_URL=… task deploy:smoke` (ya da tek seferde `task deploy:release`:
    sync → build:api → build:web → migrate → up → smoke, diverstreetfood alan adı
    varsayılanlarıyla).
12. Alertmanager: `deploy/alertmanager/alertmanager.yml` alıcı + `smtp_password` dosyası — **yapılmadı**
    (observability profili henüz açılmadı).
13. SEC-005 deploy-öncesi/sonrası sorguları (backlog-fiscal.md) — **yapılmadı**.
14. `task deploy:seed:first-tenant` (`FIRST_ADMIN_SUB` adım 5'ten, `FIRST_ADMIN_EMAIL`,
    `FIRST_ADMIN_NAME`, `TENANT_NAME`, `TENANT_SLUG`, `BRANCH_NAME`) — ilk tenant + tek şube +
    yönetici üyeliği (`deploy/postgres/seed-first-tenant.sql`, ürün/masa verisi yok, pilot
    işletme kendi girer). POS desktop'ı `api.diverstreetfood.com` adresine bağla.

## 7. Verilen kararlar (2026-09-15)

- [x] **Sunucu:** Seçenek A — mevcut b2b sunucusu (`ssh diverserver`, `/opt/onlinemenu`).
- [x] **Alt alan adları:** §2'deki dört isim onaylandı — `pos.`/`menu.`/`api.`/`auth.diverstreetfood.com`,
  DNS kayıtları cPanel UAPI ile eklendi.
- [x] **API imaj kaynağı:** registry yok — sunucuda `ko build --local` (`task deploy:build:api`),
  etiket = git kısa SHA.
- [x] **SMTP:** Cenuta posta hesabı — `mail.diverstreetfood.com:587`, STARTTLS,
  `noreply@diverstreetfood.com` (posta hesabı oluşturuldu, SPF zaten Cenuta'da).
- [x] **Token bilgileri gelmeden:** api mock fiscal ile ayağa kaldırılıyor
  (`docker-compose.diverserver.yml`'de `FISCAL_DEVICE_TYPE=mock`), sözleşme 4.6 gereği test
  altyapısı hazır olmalı. Token bilgileri gelince `.env.prod.sops` güncellenip mock override
  kaldırılır. **Mock ile pilot satış yapılmaz** (fiş kesilmez) — Token bilgileri gelene kadar.
- [x] **onlinemenu.tr name server'ları:** **taşınmıyor.** Natro'daki site ve posta kayıtları
  Cenuta'ya elle taşınmadan NS değişirse hepsi kesilir; ürün o alan adına taşınana kadar bir
  kazancı yok.

## 8. Gözlemler (deploy dışı, dikkat)

- `https://diverstreetfood.com` ana sitesindeki **403 yanlış alarm**: yalnız bot imzalı istemcilere
  (curl varsayılan UA) dönüyor, tarayıcı UA'sı 200 alıyor; Cenuta'nın sunucu tarafı filtresi.
  Sağlık kontrolü yazılırken `-A` ile tarayıcı UA verilmeli.
- b2b sunucusundaki 12 GiB "used" bellek ile konteyner toplamı uyuşmazlığı **açıklandı**
  (VMware balloon driver, §3) — gerçek kaynak sorunu değil, yine de sağlayıcı ticket'ı açık.
- b2b sunucusundaki Docker dışı `apache2` süreçleri **tanımlandı** (`/opt/ashorial-demo`, §3).
- b2b sertifikası 2026-11-28'e kadar geçerli, renew mekanizması çalışıyor.
- **MinIO başlatılmadı:** compose'daki `minio/mc` etiketi Docker Hub'dan, `postgres-backup`'ın
  indirdiği `mc` sürümü de dl.min.io'dan kaldırılmış (410 Gone). Uygulama MinIO'yu henüz
  kullanmadığı için `minio`/`minio-init` dışarıda bırakıldı; yedekleme entrypoint'i `mc`'yi
  artık yalnız S3 açıkken ve hata toleranslı indiriyor. Etiketler güncellenmeli (backlog).
- **Yedekleme koruması:** sidecar `tenants` tablosu boşken "RLS tuzağı" diye dump'ı reddeder;
  ilk tenant seed'inden önce çalışan ilk iki tur bu yüzden başarısız görünür (beklenen).
- **Mutfak ekranı akışı:** admin'in `kitchen-stream` route'u konteynerde `localhost:8081`'e
  düşüyor (compose'daki mevcut not) — KDS canlı akışı prod'da çalışmaz, admin tarafında düzeltme
  gerekiyor (backlog).
- **dev-seed.sql idempotency:** `memberships` unique index'i NULL `branch_id`'leri farklı saydığı
  için chain-wide üyelik `ON CONFLICT` ile yakalanmaz, her tekrar koşuda kopya satır ekler.
  Prod seed'i `WHERE NOT EXISTS` kullanır; dev-seed düzeltilmeli (backlog).
- **Vault storage yolu ve Redis healthcheck** compose'da hatalıydı (ilk kurulumda görüldü,
  yorumlu olarak düzeltildi): `/vault/data` → `/vault/file`, `$$REDIS_PASSWORD` → `REDISCLI_AUTH`.

## 9. Günlük operasyon

- **Release akışı:** `task deploy:release` (sync → build:api → build:web → migrate → up →
  smoke, tek komut) ya da §6'daki 1-14 adımlarını tek tek `task deploy:*` komutlarıyla.
- **Log izleme:** `task deploy:logs -- <servis>` (ör. `task deploy:logs -- api`); genel
  compose sarmalayıcısı için `task deploy:compose -- <komut>` (ör. `-- restart api`).
- **Servis durumu:** `task deploy:ps`.
- **Sır düzenleme:** `task deploy:secrets:edit` (`sops` ile `deploy/.env.prod.sops`'u yerinde
  şifreli düzenler); yeni bir age alıcısı eklenince `task deploy:secrets:updatekeys`. Düzenleme
  sonrası değişikliği sunucuya taşımak için `task deploy:sync` + ilgili `deploy:build:*`/`deploy:up`
  adımı tekrarlanır (compose.sh her çalıştığında `.env.prod.sops`'u yeniden çözer).
  **Sırlar bu dosyaya asla yazılmaz.**
  - `deploy:secrets:edit` ve `deploy:secrets:updatekeys` sunucuda değil **lokalde**
    çalıştırılır (age özel anahtarı ikisinde de var, ama commit lokalden yapılır).
- **Vault unseal (reboot sonrası, elle):** sunucu her yeniden başladığında Vault mühürlü
  (sealed) açılır — `deploy/scripts/compose.sh exec -T vault vault operator unseal
  -address=http://127.0.0.1:8200 <unseal_key>` (eşik 1; anahtar init çıktısında). Ardından
  api'yi yeniden başlat (`deploy/scripts/compose.sh restart api`), çünkü Vault'u yalnız
  açılışta okur. Otomatik unseal **yok** (kasıtlı).
- **Sertifika yenileme:** ayrı cron yok; b2b'nin `renew-ssl.sh` cron'u (03:17 ve 15:17)
  `/etc/letsencrypt/live/*` altındaki her sertifikayı, dolayısıyla `pos.diverstreetfood.com`
  sertifikasını da yeniler ve değişince nginx'i reload eder. Log: `/var/log/b2b-certbot.log`.
- **Yedekleme:** `postgres-backup` sidecar'ı compose içinde otomatik döngüyle çalışır (6 saatte
  bir, `deploy/backup/README.md`); `BACKUP_S3_*` bu sunucuda **boş** — yedekler yalnızca
  named volume'de yerel kalıyor, offsite kopya yok. Host kaybında yedek de kaybolur; S3 hedefi
  doldurulması ayrı bir karar (bkz. §5 "Yedek" satırı).

## 10. Canlı durum (2026-09-15 akşamı)

| Bileşen | Durum |
|---|---|
| `https://pos.diverstreetfood.com` | admin paneli, `/login` 200, Keycloak OIDC redirect kabul ediliyor |
| `https://menu.diverstreetfood.com` | misafir menüsü 200 (henüz menü verisi yok) |
| `https://api.diverstreetfood.com` | `/healthz`, `/readyz` OK; `FISCAL_DEVICE_TYPE=mock` |
| `https://auth.diverstreetfood.com` | Keycloak 26.2, realm `onlinemenu`, dev client/kullanıcılar silindi |
| Postgres / Redis / NATS / Vault / postgres-backup | sağlıklı; ilk yerel yedek alındı |
| MinIO, observability profili | **kapalı** (§8) |
| Tenant | `Diver Street Food` (slug `diverstreetfood`), şube `Ana Şube`, yönetici `admin@diverstreetfood.com` (manager, chain-wide) |

Kimlik bilgileri (repo dışı):
- **`deploy/.env.diverserver.local`** (git dışı, `.env.*.local`): Vault unseal anahtarı + root token,
  ilk yöneticinin geçici parolası, `admin@diverstreetfood.com` posta kutusu parolası. Sunucudaki
  `/root/.onlinemenu-*` dosyaları buraya taşınıp `shred` ile silindi (2026-09-15). Reboot sonrası
  unseal komutu dosyanın başında.
- `admin@diverstreetfood.com` posta kutusu Cenuta'da **açıldı** (Keycloak parola sıfırlama e-postaları
  artık ulaşır; webmail `https://mail.diverstreetfood.com`).
- Keycloak bootstrap admin (`kcadmin`), tüm servis parolaları: `deploy/.env.prod.sops`
  (`task deploy:secrets:edit`).
- Cenuta cPanel ve `noreply@` SMTP parolası: `deploy/.env.cenuta.local` (git dışı).
- Token webhook adresi (sözleşme/teknik formda bildirilecek):
  `https://api.diverstreetfood.com/webhooks/fiscal/tokenx/<TOKENX_WEBHOOK_SECRET>` — secret
  `.env.prod.sops`'ta, rota yalnız `TOKENX_WEBHOOK_SECRET` doluyken kayıtlı
  (`payment/http/webhook_handler.go`).

Sıradaki işler:
1. Token client-id/secret gelince `.env.prod.sops`'tan `FISCAL_DEVICE_TYPE=mock` satırını sil,
   `TOKENX_*` değerlerini doldur, `task deploy:sync` + `task deploy:up`; webhook adresi
   `https://api.diverstreetfood.com/...` (payment modülü webhook yolu) Token'a bildirilir.
2. Alertmanager SMTP + observability profili (§6 adım 12), SEC-005 sorguları (adım 13).
3. Offsite yedek (`BACKUP_S3_*`), MinIO etiketleri, Vault token yenileme stratejisi (AppRole),
   KDS akışı düzeltmesi, dev-seed idempotency, rate-limit zone kararı.
4. ~~b2b reposundaki değişiklikleri commit etmek~~ — yapıldı (`2bcdccb`, `feature/ui-ux-improvements` dalına push'landı; main'e merge edilmeli).

