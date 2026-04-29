# ADR-DATA-003: Timezone ve Business Day Hesaplama

**Durum:** Taslak
**Tarih:** 2026-04-19
**İlgili delta:** P1-3
**Kategori:** Veri / Event (DATA)

## Bağlam

"Bugünün satışları", "gün sonu raporu", "kasa kapatma" — hepsi "işletme günü" kavramına bağlı.

- Franchise zincir yurtdışı şube açabilir (Türkiye → Dubai).
- Bar/club sabah 04:00'e kadar açık — gece 03:00'daki satış "dün" mü "bugün" mü?
- Tenant-level rapor farklı timezone'daki şubeleri nasıl gruplar?

Baseline'da `timezone: Europe/Istanbul` var ama `business_day_cutoff` kavramı yoktu.

## Karar

1. **Saklama:** Tüm DB zamanları `TIMESTAMPTZ` + UTC. Hiçbir yerde "yerel saat" saklanmaz.

2. **Hesaplama:** Business day daima **branch timezone'una göre**. Tenant-level agregasyon şubelerin kendi business day'ini hesaplar, sonra tenant rapor timezone'unda gruplar.

3. **Cutoff desteği:**
   ```sql
   ALTER TABLE branch_settings ADD COLUMN timezone TEXT NOT NULL DEFAULT 'Europe/Istanbul';
   ALTER TABLE branch_settings ADD COLUMN business_day_cutoff INTERVAL NOT NULL DEFAULT '00:00:00';
   -- Bar/club için: '05:00:00' (sabah 05:00'e kadar "dünün" günü sayılır)
   ```

4. **Platform helper:** `internal/platform/timex/business_day.go`
   ```go
   // BranchBusinessDay, t zamanının branch timezone + cutoff'a göre hangi
   // business day'e düştüğünü döndürür.
   func BranchBusinessDay(t time.Time, branch BranchRef) civil.Date

   func BranchBusinessDayRange(day civil.Date, branch BranchRef) (start, end time.Time)
   ```
   `civil.Date` kullanımı: UTC confusion'dan kaçınmak için `cloud.google.com/go/civil`.

5. **Test zorunluluğu (Faz 0'da):** DST geçişleri, cutoff dönüşü, zincir farklı timezone senaryoları için unit testler.

6. **API contract:** Client API'ye zaman parametresi gönderirken branch-local olarak gönderir, backend UTC'ye çevirir.

## İmplementasyon Detayları (Dolacak)

- Temporal workflow'larda business_day kullanımı (gün kapatma retry'ları Faz 3)
- Raporlarda timezone normalizasyonu (farklı şubelerin günlük karşılaştırması)
- DST olmayan zone'lar için edge case'ler (Türkiye şu an DST kullanmıyor, ama yasa değişebilir)

## Değerlendirilen Alternatifler

- **Tenant timezone (şube değil):** Reddedildi. Farklı timezone'da franchise şube varsa yanlış.
- **UTC gün sınırları:** Reddedildi. "Gece 03:00 satışı bugün mü?" sorusu yanıtsız kalır.
