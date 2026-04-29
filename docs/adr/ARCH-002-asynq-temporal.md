# ADR-ARCH-002: asynq ve Temporal Sorumluluk Ayrımı

**Durum:** Kabul Edildi
**Tarih:** 2026-04-19
**İlgili delta:** v1'den kapanan açık soru
**Kategori:** Mimari (ARCH)

## Bağlam

Stack'te hem asynq (Redis-backed, kısa süreli jobs) hem Temporal (workflow engine, Faz 3'te aktif) var. İki araç ne zaman ne için kullanılacağı net değilse:
- Geliştirici/AI her iş için hangisini kullanacağına kafa yorar.
- Sınır bulanıklaşır; bazı işler her ikisinde de çalışır → tutarsızlık.

## Karar

### Net Sorumluluk Sınırı

| Araç | Kullanım | Örnekler |
|---|---|---|
| **asynq** | Fire-and-forget, < 5 dakika, dış servis bağımlılığı düşük, retry basit | Email gönderme, webhook retry, push notification, CSV export |
| **Temporal** | State-ful, uzun süreli, karmaşık retry + compensation, human task | MRP planlama, gün sonu ÖKC akışı, fatura retry zinciri, sevkiyat kararı |

**Kritik istisna:** Outbox dispatcher **asynq'e konmaz** (ADR-DATA-001). Outbox = ayrı goroutine, app binary içinde.

### Karar Ağacı

Bir iş için araç seçimi:

1. **"Süresi 5 dakikayı geçer mi?"** — Evet → **Temporal**
2. **"Dış servis hatasında saatlerce retry gerektirir mi?"** — Evet → **Temporal**
3. **"Çok adımlı + ara durumda compensation gerektirir mi?"** — Evet → **Temporal**
4. **"Bu bir domain event dispatch mi?"** — Evet → **Outbox dispatcher** (asynq değil)
5. **Varsayılan:** **asynq**

### Faz Dağılımı

- **Faz 0-1:** Sadece asynq aktif. Temporal docker-compose'da ayakta ama worker (`cmd/worker`) başlatılmaz. Temporal UI erişilebilir.
- **Faz 3:** Temporal worker'ları devreye girer. MRP, gün sonu ÖKC, fatura retry akışları Temporal workflow olarak yazılır.

### Gri Alan Kuralı

"Biraz uzun ama 5 dakikayı geçmez" tipi işler için → **Temporal tercih edilir** (kaçak büyüme riskine karşı).

## Sonuçlar

**İyi:**
- Net karar kuralı; araç seçimi 10 saniyede çözülür.
- Faz 3'e kadar Temporal operasyonel yükü yok (worker kapalı).

**Dikkat:**
- Faz 2-3 geçişinde bazı asynq job'ları Temporal'a taşınabilir. Net sınır bu taşımayı kolaylaştırır.
- Temporal docker-compose'da ama kullanılmıyor → küçük overhead. Kabul edilebilir.

**Risk:**
- Gri alan işler için tutarsız kararlar. Karar ağacı + code review guard.

## Değerlendirilen Alternatifler

- **Yalnız asynq:** Reddedildi. MRP, gün sonu ÖKC gibi stateful workflow'lar asynq'te cehennem.
- **Yalnız Temporal:** Reddedildi. Email gönderme gibi basit iş için Temporal overhead fazla.
- **River (Go-native job queue):** Değerlendirildi, reddedildi. asynq daha olgun, Redis ekosistemi ile uyumlu.
