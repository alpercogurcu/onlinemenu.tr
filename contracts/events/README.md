# Event Sözleşmeleri

Bu dizin, NATS JetStream üzerinden yayınlanan domain event'lerinin JSON Schema tanımlarını içerir.

## Kurallar (ADR-DATA-002)

- Event'ler **immutable**'dır. Yayınlandıktan sonra payload değiştirilemez.
- Güncelleme = yeni event. (ör. `tenant.updated.v1`)
- Consumer'lar `ON CONFLICT (event_id) DO NOTHING` ile idempotent olmalıdır.
- `UPDATE <module>_outbox SET payload = ...` **yasaktır**.

## Dizin Yapısı

```
contracts/events/
├── tenant/
│   ├── tenant.created.v1.json
│   └── branch.created.v1.json
└── README.md
```

## Şema Versiyonlama

- Breaking change → versiyon artır: `tenant.created.v2.json`
- Additive change → aynı versiyon, eski consumer'lar `additionalProperties: false` kuralı gereği yeni alanları yok sayar.

## NATS Subject Formatı

```
<module>.<event_name>.<version>
```

Örnekler:
- `tenant.created.v1`
- `tenant.branch.created.v1`
- `pos.order.placed.v1`
