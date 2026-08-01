# ADR-AUTH-003: Keycloak Admin API ile Personel Daveti

**Durum:** ✅ Kabul edildi
**Tarih:** 2026-08-01
**Kategori:** Yetkilendirme (AUTH)
**İlgili:** AUTH-001 (4 katmanlı authorization), AUTH-002 (Keycloak tek realm), SEC-002 (FORCE RLS),
SEC-005 (şube-kapsamlı roller), `docs/backlog-pilot.md` madde 2

---

## Bağlam

Bugün sisteme yeni personel eklenemiyor. `persons` tablosuna INSERT eden tek kod yolu devre dışı bir
uçta (`POST /v1/identity/persons`, `routes.go`'da yorum satırı) ve ilk girişte otomatik provizyon yok.
`hr-core.CreateEmployee` ve `memberships` ikisi de var olan bir `person_id` istiyor. Zincir baştan
kopuk — pilotu tek başına imkânsız kılan madde bu.

Değerlendirilen ve elenen yol: `/me/contexts`'in doğrulanmış Keycloak claim'lerinden person'ı
upsert etmesi. **Uygulanabilir değil** — `platform/auth/keycloak_verifier.go` yalnızca `sub`
çıkarıyor (`return &KeycloakClaims{Sub: sub}, nil`), `Principal`'da e-posta/ad alanı yok, realm
`profile`/`email` scope'larını bilinçli tanımlamıyor (`deploy/keycloak/README.md`); buna karşılık
`persons.email` **NOT NULL** ve düz **UNIQUE**. Hiç görülmemiş bir özne için oraya yazılacak meşru
bir değer yok.

---

## Karar

Personel, **yönetici tarafından admin panelden davet edilir**. Backend, Keycloak Admin API'sine
kullanıcı yaratma yetkisiyle bağlanır.

**Akış:**
1. Yönetici ad + e-posta + şube + rol girer.
2. Backend Keycloak'ta e-postayla arar; yoksa kullanıcıyı yaratır, varsa **mevcut olanı kullanır**.
3. Dönen Keycloak kullanıcı id'si `persons.keycloak_sub` olur; `persons` ve `memberships` yazılır.
4. Keycloak parola belirleme (execute-actions) e-postasını gönderir.
5. Personel ilk girişinde `/me/contexts` bağlamını görür ve doğrudan çalışır.

E-posta **davet formundan** geldiği için token'ın şekli değişmiyor: **ADR-AUTH-001 ve realm'in scope
tanımı dokunulmadan kalır.** Bu, seçilen yolun elenen alternatife göre asıl avantajıdır.

### Servis hesabı ve yetki sınırı

Confidential bir client + `client_credentials` grant. Servis hesabına **yalnızca** şu
`realm-management` client rolleri verilir:

| Rol | Neden |
|---|---|
| `create-user` | Kullanıcı yaratmak |
| `query-users` | E-postayla var olanı bulmak (idempotency için şart) |
| `manage-users` | Parola belirleme aksiyonunu tetiklemek |

**Açıkça verilmeyenler:** `manage-realm`, `manage-clients`, `manage-authorization`, `manage-identity-providers`.
Realm yapılandırmasını değiştirebilen bir servis hesabı, uygulama sunucusu ele geçirildiğinde kimlik
katmanının tamamını devreder; bu iş için gerekli değildir.

Client secret **Vault'tan** gelir (runtime sırrı). `.env`'e yazılmaz; `os.Getenv` modül kodunda
zaten yasak, config `fx.Provide` ile enjekte edilir. İstemci `backend/internal/platform/keycloak/`
altında yaşar; modüller Keycloak Admin API'sini doğrudan çağırmaz.

### Çok-kiracılıkta e-posta çakışması

Realm tektir (AUTH-002). Aynı e-posta iki tenant'ta çalışabilir — gerçek bir senaryo (muhasebeci,
bölge müdürü). Bu yüzden **var olan kullanıcı yeniden kullanılır, ikinci Keycloak kullanıcısı
yaratılmaz**; yalnızca yeni bir `memberships` satırı eklenir.

Sonuçları:
- Tenant A, davet ettiği e-postanın sistemde zaten var olduğunu **dolaylı olarak öğrenebilir**.
  Kabul edilen, düşük etkili bir bilgi sızıntısı: davet eden zaten o e-postayı biliyor.
- Tenant A yalnızca **kendi verisine** erişim vermiş olur; çapraz tenant yükselme yok.
- Davet edilen kişi onay vermeden bağlam listesinde yeni bir tenant görür. MVP'de otomatik kabul
  ediliyor; **davet kabul akışı** ürünleşme iş listesine yazıldı.

### Kısmi başarısızlık

Keycloak ve PostgreSQL iki ayrı sistem; aralarında dağıtık transaction yok. Sıra **önce Keycloak,
sonra DB** olarak sabittir ve davet **e-posta bazında idempotent** olmalıdır:

- Keycloak yazıldı, DB düştü → yeniden denemede arama var olan kullanıcıyı bulur, ikinci kullanıcı
  doğmaz, DB yazımı tamamlanır.
- **Keycloak kullanıcısı DB hatasında asla silinmez.** Başka bir tenant o kullanıcıya bağlı olabilir;
  telafi amaçlı silme, ilgisiz bir tenant'ın personelini sisteme kapatır.
- Sıra tersine çevrilemez: önce DB yazıp sonra Keycloak'ı denemek, `keycloak_sub` bilinmeden
  `persons` satırı yazmayı gerektirirdi.

### İzin

Davet ucu **tenant-kapsamlıdır** (`/v1/identity/{tenantID}/staff`), platform-admin değil.
`POST /persons` ve `GET /persons/{personID}` devre dışı kalmaya devam eder — onlar cross-tenant
platform yüzeyi ve ayrı bir iştir.

İzin adı seed ile kod **birlikte** gider; `role_permissions`'a yazılıp kodda karşılığı olmayan bir
izin, `permission_wiring` testini kırar (`docs/backlog-pilot.md` madde 7). Rol ataması SEC-005'e
tabidir: `branch_scoped` bir rol için `branch_id` zorunludur ve DB trigger'ı bunu son savunma olarak
zorlar.

---

## Değerlendirilen alternatifler

- **Realm'e `email`/`profile` scope'u ekleyip ilk girişte otomatik provizyon.** Reddedildi:
  personel önce girip boş ekran görmek, yönetici rol atadıktan sonra tekrar girmek zorunda kalırdı.
  Ayrıca ADR-AUTH-001'in belgelenmiş token şeklini değiştirirdi.
- **`persons.email`'i nullable yapmak + `persons_update` RLS'ine `all_tenants` dalı açmak.**
  Reddedildi: SEC-002'nin koruduğu invaryantı gereksiz yere zayıflatıyor. Zaten gereksizdi —
  `INSERT ... ON CONFLICT (keycloak_sub) DO NOTHING` + `SELECT` UPDATE'i hiç devreye sokmuyor
  (`persons_insert` `WITH CHECK (true)`, `persons_select`'te `all_tenants` dalı var).
- **Keycloak kullanıcısını admin panelin tarayıcıdan doğrudan yaratması.** Reddedildi: admin
  token'ının tarayıcıya inmesi gerekirdi.

---

## Sonuçlar

**Olumlu**
- Zincir uçtan uca kapanıyor; yönetici personeli ekliyor, personel ilk girişte çalışıyor.
- Token şekli ve realm scope'ları değişmiyor; AUTH-001 dokunulmuyor.
- Aynı kişinin birden çok tenant'ta çalışması doğal olarak destekleniyor.

**Olumsuz / maliyet**
- Backend artık kimlik sağlayıcısında **yazma** yetkisine sahip; ele geçirilmesi hâlinde kullanıcı
  yaratabilir. Yetki listesi bilinçli olarak dar tutuldu, ama sıfır değil.
- Yeni bir dış bağımlılık: Keycloak erişilemezse personel daveti çalışmaz (satış etkilenmez).
- Vault'ta yönetilecek bir sır daha.
- Kısmi başarısızlık telafisi elle akıl yürütme gerektiriyor; dağıtık transaction yok.

---

## Açık işler

- **Davet kabul akışı** — kişi, kendisini ekleyen tenant'ı onaylamadan bağlam listesinde görüyor.
- Davetin **geri alınması** (membership `terminated`, Keycloak kullanıcısı silinmez).
- Keycloak Admin API için hız sınırı ve hata bütçesi; SMTP yapılandırması olmayan ortamda parola
  belirleme e-postası sessizce düşmemeli (`docs/lessons-from-b2b.md`: sessiz hata yasağı).
