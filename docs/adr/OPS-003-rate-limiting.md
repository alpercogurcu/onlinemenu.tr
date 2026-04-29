# ADR-OPS-003: Rate Limiting Stratejisi

**Durum:** Taslak
**Tarih:** 2026-04-19
**İlgili delta:** P2-3
**Kategori:** Operasyonel (OPS)

## Bağlam

Multi-tenant'ta "noisy neighbor" kaçınılmaz. Baseline'da Redis var ama platform katmanında rate limiter yoktu. Bir tenant'ın aşırı kullanımı diğer tenant'ların performansını etkiler; POS hot path'te kabul edilemez.

## Karar

### Dört Seviyeli Rate Limit

| Seviye | Kapsam | Uygulama Yeri |
|---|---|---|
| **Global** | IP bazlı DDoS koruma | Traefik / edge proxy |
| **Tenant** | Plan'a göre (starter/pro/enterprise) | Platform middleware |
| **Endpoint** | Expensive endpoint'ler (rapor, CSV export, batch) | Platform middleware |
| **Device** | POS tablet başına burst limit | Platform middleware |

### Algoritma

Sliding window log — `go-redis` + Lua script:
```
key: ratelimit:{level}:{identifier}:{window_start}
value: request count
TTL: window size
```

### Limit Örnekleri (İmplementasyona kadar placeholder)

| Plan | Dakika başına istek |
|---|---|
| starter | 300 |
| pro | 1.000 |
| enterprise | 10.000 |
| device (POS) | 120 burst |

### Response

HTTP 429 + RFC 7807 problem detail + `Retry-After` header:
```json
{
  "type": "https://errors.onlinemenu.tr/rate-limit-exceeded",
  "title": "Rate Limit Exceeded",
  "status": 429,
  "detail": "Tenant plan limiti aşıldı. Retry-After: 30 saniye."
}
```

### Muafiyetler

- Internal tool endpoint'leri (`/v1/ops/*`) — tenant rate limit'in dışında
- Webhook receiver (`/v1/webhooks/*`) — IP bazlı rate limit, tenant bazlı değil
- Health check (`/health`) — rate limit yok

## İmplementasyon Detayları (Dolacak)

- Plan limits konfigürasyon kaynağı (DB vs config file — DB önerilen, tenant güncellemesi anlık)
- Burst vs sustained rate ayrımı (token bucket?)
- Rate limit header'ları (`X-RateLimit-Limit`, `X-RateLimit-Remaining`, `X-RateLimit-Reset`)
- Prometheus metric: `http_rate_limit_hits_total{level, tenant_plan}`
- Alerting: tenant sürekli 429 alıyorsa → plan upgrade önerisi bildirimi

## Değerlendirilen Alternatifler

- **Traefik'te her şeyi yönet:** Reddedildi. Tenant-bazlı ve device-bazlı context bilgisi application katmanında daha kolay.
- **Token bucket:** Değerlendirildi. Sliding window log daha adil; burst'u da kontrol eder.
- **Fixed window:** Reddedildi. Window başında spike mümkün.
