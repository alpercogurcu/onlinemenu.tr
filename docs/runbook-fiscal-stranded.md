# Runbook — Takılı Kalmış Fiscal Kayıt (Stranded Pending Submission)

**Kapsam:** ÖKC'ye gönderilen bir satışın sonucu (webhook) hiç gelmediğinde adisyonun
kalıcı olarak `fiscal_pending` kilidinde kalması.

**Yetki:** Yalnızca `manager` rolü (`payment.fiscal_terminal.manage`).

---

## 1. Belirti

Kasiyer adisyonu kapatamıyor; POS "fiscal registration pending" (409,
`code: fiscal_pending`) döndürüyor ve bekleme dakikalar/saatler boyunca geçmiyor.

Arka plandaki durum:

- `payments.status = 'pending'`
- `fiscal_submissions.status = 'pending'` veya `'submitted'`, `completed_at IS NULL`
- POS'un `GET /api/v1/payments/fiscal-pending` yanıtında aynı ödeme sürekli
  `pending` listesinde, `age_seconds` büyüyor.

Neden kendiliğinden çözülmüyor:

- **Reconciler `AutoExpire` bilinçli kapalı** (ADR-FISCAL-002). Sonucun gelmemesi,
  cihazın fiş basmadığını **kanıtlamaz** — sadece bildirimin kaybolduğunu gösterebilir.
  Saate bakarak yasal olarak kaydedilmiş bir satışı `failed` yapmak yanlıştır. Reconciler
  bu yüzden yalnızca uyarır (`payment: fiscal result overdue...` log satırı).
- **`VoidSale` pending/submitted submission'ı reddeder.** İptal, worker hâlâ satışı
  gönderirken gelirse sonradan gelen `completed` sonucu sessizce düşer ve basılı fişin
  arkasında iptal edilmiş bir ödeme kalır.

Dolayısıyla çıkış **bilinçli olarak manuel**: kararı, cihaza fiilen bakmış bir insan verir.

---

## 2. Teşhis

### 2.1 Takılı kayıtları listele

```sql
SELECT s.id            AS submission_id,
       s.payment_id,
       s.tenant_id,
       s.branch_id,
       s.status,
       s.terminal_serial,
       s.adapter_type,
       s.retry_count,
       s.last_error,
       s.created_at,
       s.submitted_at,
       now() - s.created_at AS age,
       p.amount_total,
       p.check_id
FROM fiscal_submissions s
JOIN payments p ON p.id = s.payment_id
WHERE s.status IN ('pending', 'submitted')
  AND s.created_at < now() - interval '15 minutes'
ORDER BY s.created_at;
```

### 2.2 Karar öncesi zorunlu kontroller

Expire etmeden önce **sırayla** doğrula:

1. **Cihaz gerçekten fiş bastı mı?** Kasiyerden/şubeden teyit al: ÖKC'nin son fişleri,
   günlük raporu (X raporu) veya cihaz ekranındaki son işlem. Fiş basıldıysa **§5**'e git.
2. **Ağ/entegrasyon sorunu geçici mi?** `last_error` ve adapter loglarına bak; worker
   hâlâ deniyorsa (`status = 'pending'`, `next_retry_at` yaklaşıyor) bekle.
3. **Webhook gecikmiş olabilir mi?** Vendor tarafında basket açık mı (Token: "Get Open
   Baskets For Terminal")? Açıksa satış henüz tamamlanmamıştır; kapanmışsa sonuç yolda
   kaybolmuş olabilir.
4. **Basket TTL geçti mi?** Token X basket'leri en fazla ~14 gün yaşar. TTL geçtiyse
   satışın vendor tarafında tamamlanma ihtimali kalmamıştır — expire için en güvenli an.

> Şüphedeyken **expire etme**. Ödemeyi `failed` yapmak adisyonun bakiyesini yeniden açar;
> cihaz fişi basmışsa bu, muhasebe uyuşmazlığı üretir (§5).

---

## 3. Manuel expire

```
POST /api/v1/payments/fiscal/submissions/{submission_id}/expire
```

```bash
curl -i -X POST \
  "$API/api/v1/payments/fiscal/submissions/$SUBMISSION_ID/expire" \
  -H "Authorization: Bearer $MANAGER_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"reason":"cihaz kapaliydi, fis basilmadi (sube muduru teyit etti)"}'
```

`reason` alanı opsiyoneldir ama **doldurulmalıdır**: denetim izinin tek serbest metin
alanıdır (en fazla 500 karakter). Bu metin `failure_reason` olarak kasiyerin POS
ekranında da görünür — müşteri önünde okunabilecek bir ifade yazın.

### Yanıtlar

| Kod | Anlamı | Yapılacak |
|---|---|---|
| `204 No Content` | Expire uygulandı; ödeme `failed`. | §4'e devam et. |
| `409 Conflict` — `{"error":"...","code":"submission_not_expirable"}` | Submission zaten sonuçlanmış (`completed` / `failed` / `expired` / `voided`). | Yarışı gerçek sonuç kazandı. §2.1 ile son duruma bak; `completed` ise hiçbir şey yapma, satış geçerlidir. |
| `404 Not Found` | Submission id yok ya da başka bir tenant'a ait (RLS). | Id'yi doğrula. |
| `403 Forbidden` | Rol yetersiz. | Manager hesabıyla tekrar dene. |

Endpoint `Idempotency-Key` **istemez**: yapısı gereği idempotenttir — ikinci çağrı terminal
duruma çarpar ve 409 döner, dolayısıyla ödeme en fazla bir kez `failed` olur.

**Denetim izi:** işlem `fiscal_submissions.result_payload` içine
`{"source":"manual_operator_expire","expired_by":<person_id>,"expired_at":...,"operator_note":...,"status_at_call":...}`
yazar, `last_error` alanına operatör kimliğini içeren açıklama düşer ve uygulama logunda
`payment: fiscal submission manually expired by operator` satırı oluşur.

---

## 4. Expire sonrası operasyonel akış

Zincir (koddan doğrulanmıştır):

1. `fiscal_submissions.status = 'expired'`, `completed_at` damgalanır.
2. `payments.status = 'failed'` (`applyFailed`). **Fiş kaydı (`fiscal_receipts`) yazılmaz,
   `payment.completed` outbox event'i üretilmez.**
3. Ödeme artık `PendingTotalForCheck` toplamına girmez → adisyonun `fiscal_pending` kilidi
   kalkar.
4. Kasiyer adisyonu kapatmayı denediğinde:
   - Başka tahsilat yoksa: **409 `insufficient_payment`** — "para toplanmadı" mesajı.
     Yani kasiyer parayı **yeniden tahsil eder** (yeni bir satış kaydı açılır).
   - Toplanan diğer ödemeler tutarı karşılıyorsa adisyon normal şekilde kapanır.
5. Expire edilen ödeme, POS'un `fiscal-pending` yoklamasında 5 dakika boyunca
   `recently_settled` içinde `status: failed` ve `failure_reason` ile görünür; kasiyer
   başarısızlığı ekranda görür. Sonrasında listeden düşer.

> **Kasiyere söylenecek:** "Bu tahsilat mali olarak kaydedilemedi, iptal edildi. Ödemeyi
> yeniden alın." Fiziksel para zaten kasadaysa yeni satış kaydı fişle eşleşir.

### Geç gelen webhook

Expire'dan sonra gerçek sonuç gelirse `MarkResult`'ın kaynak-durum kapısı (`pending` /
`submitted`) devreye girer: kayıt `expired` olduğu için **geçiş uygulanmaz, sonuç sessizce
düşer** — fiş kaydı yazılmaz, ödeme `failed` kalır. Bu, tekrarlanan webhook teslimatları
için istenen davranıştır; ancak cihaz **gerçekten** fiş bastıysa §5 geçerlidir.

Bu durum logda **`WARN`** seviyesinde görünür (rutin duplicate'ler `DEBUG`'da kalır):

```
payment: fiscal result arrived after manual expire — receipt may exist on device, reconcile manually
  submission_id=... payment_id=... tenant_id=... branch_id=...
  submission_status=expired dropped_result_status=completed receipt_no=... vendor_ref=...
```

Bu satırı gördüğünde **doğrudan §5'e geç**: `receipt_no` / `vendor_ref` alanları cihazda
basılmış olabilecek fişin izidir. Bu uyarıya alarm bağlanmalıdır.

---

## 5. Cihaz fişi gerçekten bastıysa (muhasebe uyarısı)

Bu durumda sistemle yasal kayıt ayrışmıştır:

- ÖKC'de **geçerli bir mali fiş** vardır (Z raporuna ve GİB'e gider).
- Sistemde ödeme `failed`, `fiscal_receipts` satırı **yoktur**.

Yapılacaklar:

1. **Aynı satışı ikinci kez ÖKC'ye kaydettirme** — mükerrer mali fiş üretirsin. Adisyonu
   kapatmak için ikinci bir tahsilat gerekiyorsa, basılmış fişle çakışmayacak şekilde
   muhasebeyle önceden konuş.
2. Fiş numarası, Z no, tarih/saat ve tutarı kaydet; muhasebeye **yazılı** bildir.
3. Gerekirse ÖKC üzerinden fiş iptali (fiş iptali cihazda/vendor tarafında yapılır;
   sistemdeki `VoidSale` yalnızca `completed` submission'lar için çalışır, expire edilmiş
   kayıt için kullanılamaz).
4. Olayı bu runbook'a not düş: hangi cihaz, hangi şube, kaç kez. Tekrarlıyorsa sorun tek
   bir satış değil, cihaz/webhook entegrasyonudur.

---

## 6. Önleme

- Reconciler'ın `payment: fiscal result overdue` uyarısına **alarm bağla**; takılı kayıtlar
  kasiyer şikâyetiyle değil, uyarıyla fark edilmeli.
- Vendor'a "basket kayıtlı mı?" sorusunu soran adapter metodu geldiğinde, expire kararı
  saatten değil **vendor'un cevabından** çıkabilir; `ReconcilerConfig.AutoExpire` tam olarak
  o gün için duruyor. O güne kadar bu runbook tek yoldur.
- Şube başına haftalık takılı kayıt sayısını izle (§2.1 sorgusu).
