# ADR-ARCH-003: Arama Backend Seçimi (Typesense)

**Durum:** Kabul Edildi
**Tarih:** 2026-04-19
**İlgili delta:** v1'den kapanan açık soru
**Kategori:** Mimari (ARCH)

## Bağlam

Faz 2'de search backend gerekli: menü/ürün arama (POS hot path), müşteri arama (CRM), fatura arama (admin). Multi-tenant senaryoda ana soru: **tenant izolasyonu nasıl yapılır?**

İki aday: Meilisearch (filter-based izolasyon) ve Typesense (collection-per-tenant).

PostgreSQL full-text search değerlendirildi ama yetersiz: Türkçe lemmatization zayıf, POS autocomplete < 50ms gerekli, fuzzy matching sınırlı.

## Karar

**Typesense kullanılır.**

### Gerekçeler

1. **Collection-per-tenant native izolasyon:** Query'ye filter eklemek zorunda değilsin; yanlışlıkla tenant sızıntısı yapısal olarak daha zor.
2. **Autocomplete performansı:** Benchmark'larda Meilisearch'ten 1.5-2x hızlı; POS hot path'te anlamlı.
3. **Alias yönetimi:** Collection migration (re-index) için alias swap — downtime sıfır.
4. **Go client olgun:** `typesense/typesense-go` resmi, sürekli güncelleniyor.
5. **Türkçe:** `locale: "tr"` + custom synonym dosyası ile yeterli.

### Faz Dağılımı

- **Faz 0-1:** Kullanılmaz. Menü araması Postgres trigram index (`pg_trgm`) ile — basit, yeterli.
- **Faz 2:** Typesense cluster (3 node HA). Modül başına migration:
  1. Catalog (menü, ürün)
  2. Party (müşteri/tedarikçi)
  3. Invoice (fatura arama)

### Collection Stratejisi

```
Naming: {tenant_id}_catalog, {tenant_id}_customers
Alias:  current_catalog_{tenant_id} → collection pointer (re-index için)
Synonyms: configs/typesense/synonyms/{tenant_id}.json
```

### Türkçe Karakter Normalizasyonu (Faz 2 Başı Spike)

- "İstanbul" → "istanbul" araması (case + diacritic insensitive)
- "Ğ/ğ", "Ş/ş", "Ç/ç" normalizasyonu
- Turkish stop words
- Synonym: "cola" ↔ "kola" ↔ "coca cola"

## Sonuçlar

**İyi:**
- Native tenant izolasyonu (collection-per-tenant).
- POS hot path performansı.
- Zero-downtime re-index (alias swap).

**Dikkat:**
- Collection başına overhead — 10k tenant cluster sizing Faz 2'de planlanır.
- Türkçe synonym yönetimi manuel şimdilik.

**Risk:**
- Typesense ekibi küçük. Ticari sürdürülebilirlik izlenmeli; fallback plan: Meilisearch.

## Değerlendirilen Alternatifler

- **Meilisearch:** Reddedildi. Filter-based izolasyon daha kırılgan; autocomplete benchmark farkı.
- **Elasticsearch:** Reddedildi. Operasyonel yük yüksek, overkill.
- **Postgres pg_trgm + tsvector:** Faz 0-1 için kabul; Faz 2+ için yetersiz.
- **Algolia (SaaS):** Reddedildi. Maliyet + veri egemenliği (KVKK).
