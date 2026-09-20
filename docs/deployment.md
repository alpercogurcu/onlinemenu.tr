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
- **Mutfak ekranı akışı — DÜZELTİLDİ:** admin'in `kitchen-stream` route'u artık
  `API_CORE_ORIGIN`'i (server-only) `NEXT_PUBLIC_API_CORE_URL`'den önce okuyor
  (`web/apps/admin/src/lib/kitchen-ws-origin.ts`), konteyner içinde `localhost:8081`'e
  düşme sorunu giderildi. Açık kalan nokta: `deploy/admin/Dockerfile`'ın `runtime` aşaması
  build aşamasındaki `API_CORE_ORIGIN` ENV'ini miras almıyor — imaj yalnızca compose'un
  verdiği `environment:` değeriyle doğru çalışıyor, tek başına `docker run` ile değil.
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
- **Yedekleme:** `postgres-backup` sidecar'ı compose içinde 6 saatte bir yerel dump alır
  (`deploy/backup/README.md`, `BACKUP_S3_*` boş). **Offsite kopya:** root cron'u her gün 03:45'te
  `deploy/scripts/offsite-backup.sh` ile dump'ları b2b'nin kullandığı Drive hesabına
  (`rclone` `gdrive:onlinemenu-backups/`) kopyalar, 30 günden eski olanları Drive'dan siler;
  log `/var/log/onlinemenu-offsite.log`, elle tetikleme `task deploy:offsite-backup`
  (2026-09-15'te kuruldu, ilk kopya doğrulandı).

- **Gözlemlenebilirlik (`--profile observability`):** varsayılanda kapalı — prometheus,
  alertmanager, loki, tempo, otel-collector, grafana yalnızca profil açıkken ayağa kalkar.
  api bu profil kapalıyken de her dakika `lookup otel-collector: server misbehaving` uyarısı
  basar (collector yok); profil açılınca durur.
  1. `GRAFANA_PUBLIC_HOST` (ör. `grafana.diverstreetfood.com`) **profil açılmadan önce**
     `.env.prod.sops`'a girmeli: `docker-compose.prod.yml`'deki
     `GF_SERVER_ROOT_URL`/`GF_SERVER_DOMAIN` bunu `${GRAFANA_PUBLIC_HOST:?...}` ile zorunlu
     kılıyor ve bu, dosya ayrıştırma anında çözüldüğü için profil kapalıyken bile —
     `task deploy:up` dahil **her** compose çağrısını — bu değişken olmadan başarısız kılar
     (`GRAFANA_ADMIN_USER`/`PASSWORD` ile aynı desen). `task deploy:secrets:edit` →
     `GRAFANA_PUBLIC_HOST=grafana.diverstreetfood.com` ekle → `task deploy:sync`.
  2. `deploy/alertmanager/smtp_password` sunucuda **elle** üretilir (git dışı,
     `deploy/alertmanager/.gitignore`'da), `up --profile observability`'den ÖNCE var olmalı —
     yoksa Docker bind-mount kaynağını dizin sanıp orada boş bir klasör yaratır ve
     Alertmanager parolayı hiç okuyamaz:
     ```bash
     sops -d --input-type dotenv deploy/.env.prod.sops \
       | grep '^SMTP_PASSWORD=' | cut -d= -f2- > deploy/alertmanager/smtp_password
     chmod 600 deploy/alertmanager/smtp_password
     ```
     (`grep` başa çapalı — çapasız kullanılırsa birden çok satır eşleşip yanlış/boş parolayla
     sessiz SMTP auth hatası verir.) SMTP auth başarısız olursa ilk kontrol edilecek şey satır
     sonu karakteri (trailing newline) — bazı `sops`/`cut` kombinasyonları ekler, Alertmanager
     bunu parolanın parçası sanabilir.
  3. Profili aç: `deploy/scripts/compose.sh --profile observability up -d`.
  4. Grafana: `https://grafana.diverstreetfood.com`, giriş `GRAFANA_ADMIN_USER` /
     `GRAFANA_ADMIN_PASSWORD` (`.env.prod.sops`, `task deploy:secrets:edit` ile okunur).
  5. Alarm alıcısı: `admin@diverstreetfood.com` (`deploy/alertmanager/alertmanager.yml`,
     SMTP `mail.diverstreetfood.com:587` STARTTLS, gönderen `noreply@diverstreetfood.com`).
     Kurallar: `deploy/prometheus/rules.yml` (`OutboxBacklogHigh`, `FiscalSubmissionOverdue`,
     `OutboxMetricMissing`).
  6. **Konteyner logları (Loki) — çözüldü:** backend'in kendisi hâlâ OTLP log exporter'ı
     kullanmıyor (`backend/internal/platform/otel/otel.go` yalnızca Trace/Metric provider
     kaydediyor — bu backlog kalemi duruyor), ama `otel-collector` artık Docker'ın
     `json-file` sürücüsünün ürettiği konteyner stdout/stderr'ını doğrudan okuyor
     (`filelog/docker` receiver, `deploy/otelcol/config.yaml`). Bunun için:
     - `docker-compose.prod.yml`'deki `x-logging` (`&default-logging`) tüm servislere
       `logging: *default-logging` ile uygulanır — hem rotasyon (10m×3 dosya, önceden
       **hiç yoktu**, host'ta sınırsız büyüyordu) hem de her json satırına compose
       etiketlerini (`com.docker.compose.project/service/container-number`) `"attrs"`
       alanı olarak ekleten `labels:` seçeneği için.
     - `otel-collector` servisi `/var/lib/docker/containers`'ı **salt okunur** mount eder
       ve **`user: "0"`** ile (root) çalışır — dizin/dosya izinleri `root:root`,
       `drwx--x---` (0710): imajın varsayılan kullanıcısı (10001) hiçbir şey okuyamaz,
       ampirik doğrulandı (gerçek `dockerd`, macOS Docker Desktop).
     - Operatör zinciri (`container` parser → `filter` → `move`×2 → `add` → `remove`)
       `"attrs"` içindeki compose etiketlerini `resource["service.namespace"]` /
       `resource["service.name"]` / `resource["container.name"]`'a taşır (Loki 3.5'in
       varsayılan index-label listesindeki üçü de) ve `"attrs"` alanı olmayan (b2b/
       ashorial gibi yabancı) konteynerleri **düşürür** — yalnızca onlinemenu-prod
       servisleri Loki'ye ulaşır. Tüm zincir gerçek `otelcol-contrib:0.123.0` imajıyla,
       gerçek docker json-log formatıyla uçtan uca test edildi (bu repoda, Docker
       Desktop ile — sunucuda değil).
     - Container İSMİ (`onlinemenu-prod-api-1` gibi) json-log dosya YOLUNDAN
       çıkarılamıyor — plain Docker'da yol yalnızca 64 haneli container ID taşıyor
       (k8s'in `/var/log/pods/...` yolunun aksine); bu yüzden `container` operatörünün
       `add_metadata_from_filepath` özelliği burada **bilerek kapatıldı** (`false`) —
       açık bırakılsaydı her satırda "failed to detect a valid log path" hata gürültüsü
       üretirdi (ampirik doğrulandı).
     - **Bilinçli tercih edilmeyen yol:** `receiver_creator` + `docker_observer`
       (Docker API/`docker.sock` üzerinden container adı keşfi) kullanılmadı — hem
       log toplayıcıya root-eşdeğeri Docker API erişimi vermek daha büyük bir güvenlik
       kararı hem de otelcol-contrib'in resmi "hints" tabanlı otomatik `filelog` keşfi
       yalnızca `k8sobserver` için var, `docker_observer` için resmi/test edilmiş bir
       örnek yok (doğrulandı: `receiver/receivercreator` README'si, v0.123.0 tag).
     - **Bilinen sınır (offset):** offset yalnızca bellekte tutuluyor (`file_storage`
       extension yok) — collector her restart'ta `start_at: end`'e döner, restart
       anındaki birikmiş satırlar atlanır. Pilot ölçeği için kabul edilebilir.
     - **Bilinen sınır (16 KB satır bölünmesi, düzeltilmedi):** Docker'ın `json-file`
       sürücüsü tek bir stdout yazımı 16384 baytı geçerse onu, containerd/CRI'daki
       gibi bir "partial" işareti OLMADAN, birden fazla ayrı (ama her biri tek
       başına geçerli) JSON satırına bölüyor — bir `recombine` operatörü güvenilir
       eklenemedi, bölünen parça ile gerçek bir sonraki satır ayırt edilemiyor.
       Ampirik doğrulandı (gerçek `dockerd`: 20000 baytlık tek satır → 16384 +
       3616 baytlık iki ayrı geçerli JSON nesnesi). 16 KB'ı aşan tek satırlık loglar
       (ör. dev'de görülen uzun stack trace) Loki'de bölünmüş görünür; zap'ın tipik
       tek-satır JSON logları bu boyuta normalde ulaşmaz.
     - **dev ortamı etkisi (doğrulandı, sorun değil):** `deploy/otelcol/config.yaml`
       dev/prod ortak — `docker-compose.dev.yml` `/var/lib/docker/containers`'ı mount
       etmiyor. Bu durumda `filelog/docker` girişte **bir kez**
       `"no files match the configured criteria"` WARN'ı basıyor, sonra tekrar
       etmeden sessizce bekliyor (mount'suz/non-root senaryo ampirik test edildi —
       her poll'da tekrarlayan log gürültüsü YOK).
     - **⚠️ Devreye alırken dikkat:** `logging: *default-logging` artık **tüm** 17
       servise uygulanıyor — bir sonraki `task deploy:up` (ya da `compose.sh up -d`)
       her servisin `logging` config'ini değiştirdiği için Docker TÜMÜNÜ yeniden
       YARATACAK (in-place restart değil, recreate). Bu, o an **canlı olan pilot
       stack'i** (api/admin/menu/keycloak dahil) kısa süreliğine kesintiye uğratır —
       gece/düşük trafik saatinde yapılması önerilir, observability profiliyle
       aynı anda değil zorunlu olarak ama aynı deploy penceresinde beklenmeli.
     - **Doğrulama (profil açıldıktan sonra):** `deploy/scripts/compose.sh logs
       otel-collector | grep -i "started watching file"` çıktısında onlinemenu-prod
       konteynerlerinin json-log yolları görünmeli; `user: "0"`/izin varsayımı
       gerçek sunucuda (Ubuntu 24.04, bu depo dışı bir ortam) doğrulanmadı — yalnızca
       macOS Docker Desktop'ta gerçek `dockerd` ile doğrulandı. Yanlış çıkarsa
       collector sessizce hiçbir şey toplamaz (mount başarısız olmaz, sadece
       `Permission denied` ile dosya okunamaz) — bu grep adımı onu yakalar.

## 10. Canlı durum (2026-09-15 akşamı)

| Bileşen | Durum |
|---|---|
| `https://pos.diverstreetfood.com` | admin paneli, `/login` 200, Keycloak OIDC redirect kabul ediliyor |
| `https://menu.diverstreetfood.com` | misafir menüsü 200 (henüz menü verisi yok) |
| `https://api.diverstreetfood.com` | `/healthz`, `/readyz` OK; `FISCAL_DEVICE_TYPE=mock` |
| `https://auth.diverstreetfood.com` | Keycloak 26.2, realm `onlinemenu`, dev client/kullanıcılar silindi |
| Postgres / Redis / NATS / Vault / postgres-backup | sağlıklı; ilk yerel yedek alındı |
| MinIO | **kapalı** (§8, imaj etiketi) |
| Observability profili | **açık** (2026-09-15 gece): Prometheus, Alertmanager (alıcı admin@), Loki (docker log toplama, `service_name`/`container_name` etiketleri), Tempo (`/var/tempo`), otel-collector, Grafana `https://grafana.diverstreetfood.com` (kullanıcı `grafana-admin`, parola sops `GRAFANA_ADMIN_PASSWORD`) |
| Tenant | `Diver Street Food` (slug `diverstreetfood`), şube `Ana Şube`, yönetici `admin@diverstreetfood.com` (manager, chain-wide) — kalıcı parola belirlendi (`deploy/.env.diverserver.local`) |
| Test verisi | Katalog (2 kategori, 3 ürün) ve masa planı (Salon, 3 masa) **duruyor**; adisyon/sipariş/ödeme/kasa verisi `task deploy:reset-test-data` ile 2026-09-15 gece sıfırlandı |

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
- **Çevrimdışı kopya:** iki `.local` dosyası, iki age özel anahtarı (MacBook + sunucu) ve README,
  b2b yedeklerinin gittiği Google Drive hesabında `onlinemenu-secrets/` klasöründe (sunucudaki
  `rclone` `gdrive:` remote'u ile, 2026-09-15). Düz metin — Drive erişimi tüm prod sırları demektir.
- Token webhook adresi (sözleşme/teknik formda bildirilecek):
  `https://api.diverstreetfood.com/webhooks/fiscal/tokenx/<TOKENX_WEBHOOK_SECRET>` — secret
  `.env.prod.sops`'ta, rota yalnız `TOKENX_WEBHOOK_SECRET` doluyken kayıtlı
  (`payment/http/webhook_handler.go`).

Sıradaki işler:
1. Token client-id/secret gelince `.env.prod.sops`'tan `FISCAL_DEVICE_TYPE=mock` satırını sil,
   `TOKENX_*` değerlerini doldur, `task deploy:sync` + `task deploy:up`; webhook adresi
   `https://api.diverstreetfood.com/...` (payment modülü webhook yolu) Token'a bildirilir.
2. Alertmanager SMTP + observability profili (§6 adım 12), SEC-005 sorguları (adım 13).
3. ~~Offsite yedek~~ (Drive'a rclone ile kuruldu), MinIO etiketleri, Vault token yenileme stratejisi (AppRole),
   KDS akışı düzeltmesi, dev-seed idempotency, rate-limit zone kararı.
4. ~~b2b reposundaki değişiklikleri commit etmek~~ — yapıldı (`2bcdccb`, `feature/ui-ux-improvements` dalına push'landı; main'e merge edilmeli).

## 11. Prod kabul testleri (2026-09-15 gece)

Gerçek API (`api.diverstreetfood.com`, yönetici CTX token'ı) ve tarayıcı (Playwright) ile koşuldu;
script'ler geçici, repoya alınmadı.

**Geçenler:** Keycloak parola belirleme + SSO + bağlam seçimi; katalog/masa oluşturma; kasa
açılışı; adisyon → sipariş → ödemesiz kapatma 409 → nakit ödeme → mock fiscal fiş (~1 sn) →
kapanış → masa `cleaning`; gün sonu raporu (brüt, KDV kırılımı, ödeme yöntemi, kasa oturumu);
idempotent ödeme (aynı anahtar = tek ödeme); kısmi ödeme; KDS durum zinciri (accept →
preparing → ready → delivered, geçersiz geçiş 409); adisyon iptali; kasa hareketi + sayım
(fark raporlanıyor) + kapanış; admin arayüzünde adisyon listesi, masa durumu değiştirme,
mutfak ekranı canlı akışı; tam sayfa yenilemede oturum kalıcılığı (silent SSO).

**Bulunup düzeltilenler (aynı gece deploy edildi):**
- Kapalı/iptal adisyona sipariş ve ödeme kabul ediliyordu (201) → artık 409
  `check_not_open`; şube uyuşmazlığı 409 `check_branch_mismatch` (`pos/service/check_guard.go`,
  `payment/service/check_guard.go`; fx döngüsü için ayrı `CheckReadService`).
- Sayfa yenilemede admin oturumu sessizce düşüyordu → `prompt=none` silent SSO +
  `SessionGuard`; oturumsuz açılış `/login`'e yönlenir (`web/apps/admin/src/lib/session-restore.ts`).
- Mutfak ekranı canlı akışı konteynerde `localhost:8081`'e düşüyordu → `API_CORE_ORIGIN`
  önceliği (`web/apps/admin/src/lib/kitchen-ws-origin.ts`).
- Tempo volume sahipliği (`/tmp/tempo` → `/var/tempo`).

**Açık kalan küçükler:** breadcrumb "POS" bağlantısı `/pos` 404; `ErrTableBranchMismatch` 422 iken
`check_branch_mismatch` 409 (tutarsızlık); `OrderService.Accept/advance` kapalı adisyonun
siparişlerini hâlâ ilerletebiliyor; Keycloak `KC_CACHE=local` olduğu için konteyner yeniden
yaratılınca tüm oturumlar düşer (kullanıcılar yeniden giriş yapar); admin Dockerfile `runtime`
stage'i `API_CORE_ORIGIN` varsayılanı taşımıyor (compose sağlıyor).


## 12. Prod test hesapları ve kabul turu (2026-09-20)

**Bağlam / karar:** prod (`*.diverstreetfood.com`) şu an **test ortamı** olarak kullanılıyor.
b2b’den aktarılan gerçek katalog ve şube verisi (5 şube, 21 ürün, 20 şube fiyatı — `docs/b2b-import-plan.md` §10)
**duruyor**; hesaplar test hesabıdır. Gerçek personel davet edilmedi
(`deploy/b2b-staff.local.commands.sh` çalıştırılmadı), gerçek yönetici
`admin@diverstreetfood.com` hesabına **dokunulmadı**.

### 12.1 Kabul testi Keycloak istemcisi

`e2e-prod` — realm `onlinemenu`, **confidential**, yalnız **direct access grant**
(standard/implicit/service-account kapalı), `defaultClientScopes = [basic, onlinemenu-audience]`,
`optionalClientScopes = [onlinemenu-context-claims]`. Üretim kullanıcıları bu istemciyi kullanmaz;
yalnız kabul testleri parola akışıyla token alır. Secret **`deploy/.env.diverserver.local`**
(`E2E_PROD_CLIENT_SECRET`, git dışı).

Doğrulandı: `iss = https://auth.diverstreetfood.com/realms/onlinemenu`,
`aud = onlinemenu-backend` (API'nin `KEYCLOAK_AUDIENCE` değeriyle birebir), `azp = e2e-prod`.

> Test turu bittikten sonra bu istemci **silinebilir** (kabul turu dışında ihtiyaç yok).

### 12.2 Test hesapları

Parolalar **yalnız** `deploy/.env.diverserver.local` → `TEST_ACCOUNTS` bloğunda (git dışı).

> ⚠️ **Çevrimdışı kopya bayat:** §10'daki Google Drive `onlinemenu-secrets/`
> klasöründeki `.env.diverserver.local` kopyası bu bloktan **önceki** hâldir.
> Drive'dan geri yükleyen biri test hesaplarının parolalarını ve
> `E2E_PROD_CLIENT_SECRET`'ı bulamaz. Kopya tazelenmeli.
Tümü Keycloak `reset-password` ile **kalıcı** parolalıdır, required action yoktur
(e-posta akışına bağımlı değil). `admin+…` adresleri artı-adresleme ile gerçek
`admin@diverstreetfood.com` kutusuna düşer — davet postaları zararsızdır.

| E-posta | Rol | Şube | Nasıl oluşturuldu |
|---|---|---|---|
| `test.yonetici@diverstreetfood.com` | Yönetici | zincir geneli (branch NULL) | Keycloak Admin API + `persons`/`memberships` SQL |
| `admin+kasiyer.serdivan@diverstreetfood.com` | Kasiyer | Serdivan | `POST /v1/identity/{tid}/staff` |
| `admin+garson.serdivan@diverstreetfood.com` | Garson | Serdivan | aynı |
| `admin+mutfak.serdivan@diverstreetfood.com` | Mutfak | Serdivan | aynı |
| `admin+kasiyer.izmit@diverstreetfood.com` | Kasiyer | İzmit | aynı |
| `admin+garson.izmit@diverstreetfood.com` | Garson | İzmit | aynı |
| `admin+mutfak.izmit@diverstreetfood.com` | Mutfak | İzmit | aynı |
| `admin+kasiyer.adapazari@diverstreetfood.com` | Kasiyer | Adapazarı | aynı |
| `admin+kasiyer.kirkpinar@diverstreetfood.com` | Kasiyer | Kırkpınar | aynı |

Staff ucu 8/8 hesapta `201` döndü; `keycloak_user_created=true`, `notification_sent=true`
(SMTP çalışıyor, `notification_error` boş).

### 12.3 Şube kurulumu (test masaları)

| Şube | Bölge | Masa | QR |
|---|---|---|---|
| Serdivan | Salon (mevcut) | 3 (mevcut, dokunulmadı) | 3 yeni |
| İzmit | Salon (yeni) | Masa 1–6 (yeni) | 6 yeni |
| Adapazarı | Salon (yeni) | Masa 1–6 (yeni) | 6 yeni |
| Kırkpınar | Salon (yeni) | Masa 1–6 (yeni) | 6 yeni |
| İmalat Merkezi | — | masa yok (bilinçli) | — |

Hepsi POS/storefront REST uçlarıyla oluşturuldu (doğrudan SQL değil), yönetici CTX token'ıyla.
QR token'ları yalnız oluşturma yanıtında döner (DB'de `token_hash`); kabul turu için geçici
olarak saklandı, repoya yazılmadı.

### 12.4 Kabul testi paketi — `web/apps/admin/e2e-prod/`

Dev paketi (`web/apps/admin/e2e/`) **değiştirilmedi**. Prod için ayrı, kendi
Playwright yapılandırması olan bir paket eklendi:

```
set -a; . deploy/.env.diverserver.local; set +a
cd web/apps/admin
E2E_PROD=1 npx playwright test -c e2e-prod/playwright.config.ts
```

- `E2E_PROD=1` verilmeden **çalışmaz**: `e2e-prod/global-setup.ts` tüm koşuyu
  reddeder, `e2e-prod/fixtures/prod.ts` ayrıca import anında hata fırlatır
  (bir spec import'u unutsa bile koşu durur).
- Kimlik bilgileri yalnız ortamdan okunur; repoda hiçbir parola/QR token yok.
- Adres/ortam değişkenleri: `E2E_PROD_API_URL`, `E2E_PROD_BASE_URL`,
  `E2E_PROD_KEYCLOAK_URL`, `E2E_PROD_REALM`, `E2E_PROD_CLIENT_ID/SECRET`.
- Şube/ürün kimlikleri **sabit yazılmadı**; canlı API'den slug/ad ile çözülür.
- Ters proxy hız sınırı (bkz. bulgu B4) yüzünden her API çağrısı 503'te
  geri çekilerek yeniden denenir.

| Dosya | Senaryo |
|---|---|
| `a-branch-pricing.spec.ts` | (a) şube fiyat override'ı |
| `b-cross-branch.spec.ts` | (b) çapraz şube erişimi |
| `c-cash-day.spec.ts` | (c) Serdivan tam kasa günü (KDS arayüzü dahil) |
| `d-table-ops.spec.ts` | (d) masa taşıma / birleştirme / kalem taşıma |
| `e-guest-qr.spec.ts` | (e) misafir QR menüsü ve siparişi |
| `f-admin-ui.spec.ts` | (f) yönetim paneli ekranları |
| `g-cost-projection.spec.ts` | (g) maliyet alanı sızıntısı |

### 12.5 Senaryo sonuçları

Tam paket canlı prod'a karşı koşuldu: **23/23 geçti** (2026-09-20, ~22 sn).
Tüm senaryolar kendi verisini temizler; koşu sonunda prod'da açık adisyon,
açık kasa oturumu, kirli masa veya artık seçenek grubu kalmadı (DB ile doğrulandı).

| # | Senaryo | Sonuç |
|---|---|---|
| a | İzmit kasiyeri American Smash'i 490 TL görür; o fiyatla sipariş **201**, 470 TL ile **422 `price_mismatch`**; Serdivan 470 TL; İzmit/Kırkpınar 10'ar override, Adapazarı/Serdivan 0 | **GEÇTİ** |
| b | İzmit kasiyeri Serdivan adisyonunu tekil okumada **404**, Serdivan listesinde **403**, kendi listesinde göremiyor, çapraz ödeme reddediliyor | **GEÇTİ** |
| c | Kasa aç (ikinci açış 409, negatif 422) → masaya adisyon → seçeneksiz + seçenekli sipariş → KDS arayüzünden hazırla/hazır → kalem bazlı + kalan nakit (idempotent; anahtarsız 422; eksik ödemeyle kapanış 409) → mock ÖKC fişi → kapanış (kapalı adisyona sipariş 409) → masa `cleaning`→`empty` (kasiyer) → sayım farkı −25,00 TL → kasa kapanış → gün sonu raporu (şube kapsamlı) | **GEÇTİ** |
| d | Masa taşıma, adisyon birleştirme (`merged`, tutar hedefe), kalem taşıma, garson yetki sınırı | **GEÇTİ** |
| e | İzmit QR menüsü 490 TL, Serdivan 470 TL; misafir siparişi 201 ve tutarı sunucu belirliyor | **GEÇTİ** (geçici menü ile — bkz. bulgu B1) |
| f | Test yöneticisi Keycloak SSO ile panele giriyor; Şube Fiyatları'nda İzmit **10** "Şube fiyatı" rozeti / Serdivan **0**; Kullanıcılar sayfasında 8 `PRODTEST` personeli şube etiketiyle; Şubeler listesi **5** | **GEÇTİ** |
| g | `cost*` anahtarı hiçbir yanıtta yok (7 uç + kasiyer projeksiyonu) | **GEÇTİ** (sınırlı — bkz. bulgu B5) |

### 12.6 Bulgular (düzeltilmedi, yalnız raporlanıyor)

**B1 — Misafir QR menüsü prod'da boş; `docs/b2b-import-plan.md` §5 hatalı.**
Storefront menü read model'i `menu_items`'tan beslenir:
`backend/internal/modules/catalog/repo/storefront_menu_repo.go:104-123`
(`visible_items` CTE `FROM menu_items mi JOIN menus m …` ile başlar). Prod'da
hiç `menus` kaydı yok, dolayısıyla QR okutulduğunda oturum açılıyor ama
`GET /api/public/v1/menu` **`{"categories":[]}`** dönüyor (ampirik doğrulandı).
`docs/b2b-import-plan.md` §5'teki "`menus`/`menu_items` aktarılmaz … QR menüsü
menüsüz çözülür" satırı yanlıştır — şube fiyat override'ı menüyü ikame etmiyor,
yalnız menüdeki fiyatı değiştiriyor. Kabul testi (e) bunu kanıtlayabilmek için
geçici bir menü kurup sonunda pasifleştirir. **Karar gerekiyor:** pilot için
kalıcı bir tenant-geneli menü açılmalı, yoksa QR siparişi hiç çalışmaz.

**B2 — Personel daveti Keycloak'ta `lastName` yazmıyor; hesap "not fully set up" kalıyor.**
`backend/internal/platform/keycloak/client.go:322` `FirstName: req.FullName`
atıyor, `LastName` hiç set edilmiyor. Prod realm'inin kullanıcı profilinde
`lastName` `user` rolü için **zorunlu** (`GET /admin/realms/onlinemenu/users/profile`).
Sonuç: `POST /v1/identity/{tid}/staff` ile açılan her hesap eksik profilli
doğuyor; parola akışı (direct grant) `invalid_grant · "Account is not fully set up"`
ile reddediliyor, tarayıcı girişinde ise kullanıcı panele varmadan önce
"Hesap bilgilerini güncelle" formuna düşüyor. 8/8 test hesabında görüldü;
test için `lastName` elle dolduruldu. Öneri: ad/soyadı `full_name`'den ayırın
(`seed`/`keycloak-harden.sh` zaten `rsplit(" ", 1)` yapıyor) ya da realm'de
`lastName` zorunluluğunu kaldırın.

**B3 — Katalog listesinde `branch_id` opsiyonel; şube kapsamlı kullanıcıya tenant fiyatı dönüyor.**
`backend/internal/modules/catalog/http/branch_override_handler.go:145-154`:
parametre yoksa "tenant default". İzmit kasiyerinin CTX token'ı şubesini
kesin olarak taşıdığı hâlde `GET /api/v1/catalog/products` (parametresiz)
American Smash'i **470 TL** (`branch_price_overridden:false`) döndürüyor;
`?branch_id=<İzmit>` ile **490 TL**. Sipariş ucu katalogdan yeniden
fiyatlandırdığı için yanlış tahsilat oluşmuyor (422 `price_mismatch`), ama
parametreyi unutan bir POS/istemci ekranda yanlış fiyat gösterir ve kasiyer
siparişi geçiremez. Öneri: şube kapsamlı principal için varsayılanı kendi
şubesi yapmak.

**B4 — Ters proxy hız sınırı tek IP başına 10 r/s ve 429 değil 503 dönüyor.**
Direktif repoda: `deploy/nginx/diverserver.conf:152`
`limit_req zone=api burst=40 nodelay;`. **Hızı belirleyen zone tanımı ise
onlinemenu yığınında değil** — pilotun genel giriş kapısı kardeş b2b
yığınının ters proxy'sidir (`b2b_nginx` konteyneri; onlinemenu'nün kendi
nginx konteyneri **yok**) ve oran orada tanımlıdır:
`b2b_nginx:/etc/nginx/nginx.conf:38`
`limit_req_zone $binary_remote_addr zone=api:10m rate=10r/s`.
Yani bu sınırı değiştirmek **b2b'yi de etkiler**; değişiklik iki ürünün ortak
kararıdır. Kabul turu
sırasında ampirik olarak tetiklendi (nginx error log: `limiting requests,
excess: 40.140 by zone "api"`), API konteyneri sağlıklıyken istemci
`503 Service Temporarily Unavailable` aldı. İki sorun:
1. **Ölçek riski:** bir şubenin tüm POS cihazları tek NAT IP'sinin arkasındadır;
   gerçek bir servis saatinde 10 r/s şube başına değil **işletme başına**
   bütçedir. §10'daki "rate-limit zone kararı" açık maddesi bununla doğrulandı.
2. **Hatalı durum kodu:** 503 "arka uç çöktü" ile "hız sınırı" arasında ayrım
   bırakmıyor; ADR-OPS-003 anlamında doğru cevap 429'dur
   (menü uygulaması `rate_limited` kodunu zaten bekliyor,
   `web/apps/menu/src/lib/api.ts`). Kabul paketi bu yüzden 503'te geri çekilip yeniden deniyor —
   personel **ve** misafir uçlarında; tam paket koşusu ikisini de tetikledi.

**B5 — (g) senaryosunun kanıt gücü sınırlı.**
Yanıtlarda hiçbir `cost*` anahtarı yok (7 uç + kasiyer projeksiyonu denendi),
ancak b2b aktarımında `cost_price_tl` bilerek dışarıda bırakıldığı için
prod kataloğunda **hiç maliyet verisi yok**. Yani test "projeksiyon maliyeti
süzüyor"u değil "ortada maliyet yok"u doğruluyor. Maliyet alanı bir gün
kataloğa girerse bu testin yeniden koşulması gerekir.

**B6 — Adisyon kapanışından sonra masa `cleaning`'e geçişi asenkron.**
Kapanış/iptal yanıtı 200 döndükten hemen sonra masayı `empty` yapmak, durumu
`cleaning`'e çeken olay yolunu yarıştırıp masayı kirli bırakabiliyor
(2026-09-20 koşusunda İzmit "Masa 4" böyle kaldı). Kabul paketi teardown'da
plan gerçekten `empty` okuyana kadar tekrar deniyor; ürün tarafında POS
arayüzünün de aynı yarışa açık olup olmadığı incelenmeli.

### 12.7 Bu turda prod'a eklenenler ve geri alma

Hiçbir gerçek veri silinmedi/değiştirilmedi; aşağıdakilerin **tamamı** bu kabul
turunda eklendi ve test bitince kaldırılabilir.

| Ne | Nerede | Geri alma |
|---|---|---|
| `e2e-prod` Keycloak istemcisi | realm `onlinemenu` | Keycloak Admin API / konsol → client sil; `E2E_PROD_CLIENT_SECRET` satırını `.local`'dan çıkar |
| 9 test kullanıcısı | Keycloak + `persons` + `memberships` | Keycloak'ta kullanıcıları sil; SQL'de `persons.email LIKE 'admin+%'` ve `test.yonetici@%` satırları (+ `memberships`) |
| 3 bölge ("Salon") + 18 masa | İzmit/Adapazarı/Kırkpınar | `tables` → `table_zones` sırasıyla sil (FK `ON DELETE RESTRICT`) |
| 21 QR kodu | `storefront_qr_codes` | `POST /api/v1/storefront/qr-codes/{id}/revoke` ya da satır silme |
| `PRODTEST Menü` (+1 menü kalemi) | `menus` / `menu_items` | **Durum: bkz. aşağıdaki not** |
| `PRODTEST-*` adisyon/sipariş/ödeme/kasa oturumu kayıtları | POS/payment tabloları | Hepsi kapalı/iptal; `task deploy:reset-test-data` bu kümeyi sıfırlar |

**`PRODTEST Menü` durumu:** (e) senaryosunun teardown'u menüyü **pasifleştirir**,
yani QR menüsü bu turdan önceki hâline (boş) döner. Menü satırı pasif olarak
kataloğda kalır — silme ucu yok (`catalog` yalnızca create/update sunar) ve
tekrar koşuda yeniden kullanılır. Pilotun QR siparişini gerçekten açması için
**kalıcı ve gerçek** bir menü kaydı gerekir (bulgu B1); bu ürün kararıdır,
kabul testi bunu kendiliğinden yapmaz.
