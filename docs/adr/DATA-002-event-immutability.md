# ADR-DATA-002: Event Immutability Kuralı

**Durum:** Kabul Edildi
**Tarih:** 2026-04-19
**İlgili delta:** P0-6
**Kategori:** Veri / Event (DATA)

## Bağlam

Baseline'da "Consumer `ON CONFLICT (id) DO NOTHING`" vardı. Eksik olan: **publish edilmiş bir event'in payload'u değiştirilebilir mi?**

Cevap belirsiz kalırsa:
- Edge'de aynı event ID ile düzeltilmiş payload gelirse consumer `ON CONFLICT DO NOTHING` → eski kalır veya `DO UPDATE` → last-write-wins (tehlikeli).
- Event sourcing doktrininde event'ler **immutable** olmalı.

Bu kural netleşmezse farklı modüller farklı pattern benimser, sistem tutarsızlaşır.

## Karar

**Event'ler tamamen immutable'dır. Publish edilmiş bir event'in `event_id`, `payload`, `event_type`, `event_version` alanları asla değişmez.**

1. **Güncelleme = yeni event:**
   - ❌ Yanlış: `check.updated.v1` aynı `event_id` ile republish
   - ✅ Doğru: `check.item_added.v1`, `check.item_voided.v1`, `check.closed.v1` — her biri farklı ID

2. **Consumer deseni (tüm modüllerde sabit):**
   ```sql
   INSERT INTO <projection_table> ...
   ON CONFLICT (event_id) DO NOTHING;
   ```
   `ON CONFLICT DO UPDATE` **yasaktır.**

3. **Event şeması isimlendirme kuralı:**
   - Fiil + state change: `order.created.v1`, `order.paid.v1`, `shipment.received.v1`
   - Terminal event: şema dokümantasyonunda "bu event'ten sonra başka event yok" notu
   - Ara event: "aggregate lifecycle'ının ortasında"

4. **Tüm modül consumer'ları için idempotency contract testi:**
   - Aynı event iki kez deliver edilince side-effect bir kez çalışır.
   - `internal/platform/eventbus/idempotent_consumer_test.go`

5. **Edge senaryoları:**
   - Offline check açılır, online olunca sync: event ID aynı, içerik aynı, timestamp aynı. `ON CONFLICT DO NOTHING` idempotent.
   - Check iptal: `check.opened.v1` + `check.voided.v1` iki ayrı event.

6. **Event şema evrimi:**
   - Minor (yeni opsiyonel alan): aynı `vN`
   - Breaking: `vN+1` yeni dosya, `vN` consumer'lar migrate edilene kadar korunur

7. **Lint kuralı:** `UPDATE <module>_outbox SET payload = ...` **yasak.** Outbox tablosu write-once; lint ile engellenir.

## Sonuçlar

**İyi:**
- Event stream gerçek append-only log: rewind, replay, audit edilebilir.
- Consumer idempotency tek pattern, tüm modüllerde aynı.
- Debezium Faz 2'de gelince CDC kaynağı zaten immutable, uyumlu.
- Edge ↔ cloud sync conflict-free: aynı event ID = aynı içerik.

**Dikkat:**
- "Düzeltme" ihtiyacı = yeni event tipi gerekir; event taxonomy biraz şişer.
- İlk tasarımda event sayısı fazla görünebilir; bu doğrudur — state change granular olmalı.

**Risk:**
- Geliştirici "hızlı fix" olarak payload'u güncellemek isteyebilir. Lint + PR review guard.

## Değerlendirilen Alternatifler

- **Mutable event'ler (last-write-wins):** Reddedildi. Sıraya bağımlılık, replay imkânsız, CDC bozulur.
- **Event versioning per payload (aynı ID, farklı version):** Reddedildi. ID + version compound key karmaşası.
- **Snapshot-only:** Reddedildi. Audit/replay avantajları kaybedilir.
