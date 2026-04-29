# ADR-SEC-004: Cihaz Kayıt ve Pairing Code Akışı

**Durum:** Taslak
**Tarih:** 2026-04-19
**İlgili delta:** P1-1
**Kategori:** Güvenlik (SEC)

## Bağlam

Baseline akışı:
> Yeni cihaz → Local Server'a bağlanır → Cloud'a cihaz kayıt isteği gönderir → Keycloak'ta client-credentials oluşturur.

Bu akışta **herhangi bir cihaz** kendini bir tenant'a kaydedebilir. POS tabletleri açık restoranlarda durur, çalınabilir. Fingerprint kolay sahtelenememe garantisi yoktu.

## Karar

1. **Pairing code akışı:**
   - Admin panelden (yetkili kullanıcı) "yeni cihaz ekle" → sistem 10 dakika ömürlü, 6-8 karakter alfanumerik code üretir.
   - Code `branch_id` + expiry ile bağlı.
   - Cihaz ilk açılışta bu code ile cloud'a başvurur → doğrulanırsa Keycloak client-credentials + cihaz token'ı alır.
   - Code tek kullanımlık, doğrulama sonrası invalidate edilir.

2. **Hardware fingerprint (platform-native):**
   - Windows: TPM-backed machine GUID
   - macOS: Secure Enclave keypair public key
   - Android: Keystore-backed key
   - Linux (edge local server): machine-id + primary NIC MAC kombinasyonu

3. **Token rotation:**
   - Access token: 1 saat ömürlü
   - Refresh token: 30 gün ömürlü, her kullanımda rotate olur

4. **Revocation:** Admin panel → Keycloak client disable + NATS `devices.<device_id>.command` üzerinden `device.wipe` → cihaz localStorage/SQLite kritik verilerini temizler.

5. **Şema değişikliği** (`devices` tablosuna eklenen kolonlar):
   ```sql
   ALTER TABLE devices ADD COLUMN pairing_code_hash TEXT;
   ALTER TABLE devices ADD COLUMN pairing_expires_at TIMESTAMPTZ;
   ALTER TABLE devices ADD COLUMN fingerprint_method TEXT; -- tpm|keystore|machine-id
   ALTER TABLE devices ADD COLUMN last_token_rotated_at TIMESTAMPTZ;
   ALTER TABLE devices ADD COLUMN revoked_at TIMESTAMPTZ;
   ALTER TABLE devices ADD COLUMN revoke_reason TEXT;
   ```

## İmplementasyon Detayları (Dolacak)

- Her platform için fingerprint extraction kodu (TPM, Secure Enclave, Keystore)
- Code formatı (okunabilirlik + güvenlik dengesi — QR code vs alfanumerik)
- Wipe komutunun kapsamı (hangi veri silinir, hangi kalır)
- Lost-stolen senaryosu operasyonel runbook

## Değerlendirilen Alternatifler

- **Otomatik cihaz kayıt (kod yok, ilk bağlantıda kaydet):** Reddedildi — güvenlik açığı.
- **SMS OTP ile cihaz onay:** Değerlendirilecek (Faz 2), ticari yük (SMS maliyeti) fayda-maliyet hesabıyla.
