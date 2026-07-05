// backend/loadtest/k6/pos_sale_flow.js
//
// Sprint-8 k6 yük testi — "500 aktif POS simülasyonu" (ROADMAP Faz 1).
//
// Bir VU = bir kasiyer terminali. Her iterasyon gerçekçi bir satış döngüsünü
// canlandırır:
//   1. Katalog okuma (kasiyer menüye bakıyor)
//   2. Adisyon (check) açma — masa ya da paket
//   3. 1-3 sipariş ekleme (müşteri sırayla ürün söylüyor / ek sipariş)
//   4. Nakit/kart ödeme kaydı (tam tutar — check kapanış kapısını geçer)
//   5. Adisyon kapama
//
// Adımlar arası bekleme (sleep) kasiyer/müşteri davranışını taklit eder —
// gerçek trafik olmayan "makine hızında" istek zincirlerinden kaçınmak için.
//
// Çalıştırma:
//   k6 run backend/loadtest/k6/pos_sale_flow.js                      (smoke: 25 VU, 2dk)
//   k6 run -e PROFILE=full backend/loadtest/k6/pos_sale_flow.js      (500 VU hedefli tam profil)
//
// Ortam değişkenleri: bkz. backend/loadtest/README.md.
import http from 'k6/http';
import { check, sleep } from 'k6';
import exec from 'k6/execution';

import {
  BASE_URL,
  BRANCH_ID,
  ADMIN_EMAIL,
  STANDARD_THRESHOLDS,
  devLogin,
  buildCashierTokenPool,
  ensureCatalog,
  authHeaders,
  idempotencyHeader,
  randomItem,
  randomIntBetween,
} from './lib/common.js';

const PROFILE = __ENV.PROFILE || 'smoke';

// Smoke: scriptin/senaryonun doğruluğunu kanıtlamak için küçük, hızlı koşu.
// Full: ROADMAP'in istediği "500 aktif POS" hedefine ramping-vus ile çıkar,
// 10dk sabit tutar, sonra iner — LOKALDE bilinçli olarak varsayılan DEĞİL
// (bkz. README "Bilinen sınırlar" — 500 VU tek node Postgres/Redis/NATS'a
// karşı yalnızca yeterli donanımda koşulmalı).
const SCENARIOS = {
  smoke: {
    executor: 'constant-vus',
    vus: 25,
    duration: '2m',
  },
  full: {
    executor: 'ramping-vus',
    startVUs: 0,
    stages: [
      { duration: '5m', target: 500 },
      { duration: '10m', target: 500 },
      { duration: '2m', target: 0 },
    ],
    gracefulRampDown: '30s',
  },
};

export const options = {
  scenarios: { cashiers: Object.assign({ exec: 'cashierFlow' }, SCENARIOS[PROFILE] || SCENARIOS.smoke) },
  thresholds: STANDARD_THRESHOLDS,
};

export function setup() {
  const adminToken = devLogin(ADMIN_EMAIL);
  const products = ensureCatalog(adminToken);
  if (products.length === 0) {
    throw new Error('setup: katalogda ürün yok ve oluşturulamadı — dev-seed ve migration durumunu kontrol edin');
  }
  const tokens = buildCashierTokenPool();
  return { tokens, products };
}

function pickToken(data) {
  // exec.vu.idInTest: test genelinde benzersiz, 1-tabanlı VU kimliği —
  // token havuzunu VU'lar arasında kararlı biçimde dağıtır (aynı VU her
  // zaman aynı "terminale" bağlanır).
  const idx = (exec.vu.idInTest - 1) % data.tokens.length;
  return data.tokens[idx];
}

function buildOrderItems(products, count) {
  const items = [];
  for (let i = 0; i < count; i++) {
    const p = randomItem(products);
    const qty = randomIntBetween(1, 3);
    items.push({
      product_id: p.id,
      product_name: p.name,
      product_price_amount: p.price_amount,
      product_currency: p.currency || 'TRY',
      tax_rate_bps: p.tax_rate_bps || 0,
      quantity: qty,
      unit_price_amount: p.price_amount,
      lineTotal: p.price_amount * qty,
    });
  }
  return items;
}

// cashierFlow tek bir kasiyer iterasyonu — pos_sale_flow.js tek başına
// çalıştığında varsayılan (default) fonksiyon, mixed_load.js tarafından da
// `exec: 'cashierFlow'` ile paylaşılan senaryo fonksiyonu olarak kullanılır.
export function cashierFlow(data) {
  const token = pickToken(data);
  const iterNo = exec.vu.iterationInScenario;
  const label = `LT-${exec.vu.idInTest}-${iterNo}`;

  // 1. Kasiyer menüye bakıyor (okuma).
  const listRes = http.get(`${BASE_URL}/api/v1/catalog/products`, {
    headers: authHeaders(token),
    tags: { type: 'read', name: 'ListProducts' },
  });
  check(listRes, { 'catalog list: 200': (r) => r.status === 200 });
  sleep(randomIntBetween(1, 3));

  // 2. Adisyon aç — masa (dine_in) ya da paket (takeaway) rastgele.
  const isTakeaway = Math.random() < 0.25;
  const channel = isTakeaway ? 'takeaway' : 'dine_in';
  const openRes = http.post(
    `${BASE_URL}/api/v1/pos/checks`,
    JSON.stringify({ branch_id: BRANCH_ID, table_label: isTakeaway ? `PAKET-${label}` : label }),
    { headers: authHeaders(token), tags: { type: 'write', name: 'OpenCheck' } }
  );
  const opened = check(openRes, { 'open check: 201': (r) => r.status === 201 });
  if (!opened) {
    sleep(1);
    return;
  }
  const checkId = JSON.parse(openRes.body).id;
  sleep(randomIntBetween(2, 5));

  // 3. 1-3 sipariş ekle; ödenecek toplamı biriktir.
  let amountDue = 0;
  const orderCount = randomIntBetween(1, 3);
  for (let i = 0; i < orderCount; i++) {
    const items = buildOrderItems(data.products, randomIntBetween(1, 3));
    const orderTotal = items.reduce((sum, it) => sum + it.lineTotal, 0);

    const orderRes = http.post(
      `${BASE_URL}/api/v1/pos/orders`,
      JSON.stringify({
        branch_id: BRANCH_ID,
        check_id: checkId,
        order_channel: channel,
        items: items.map(({ lineTotal, ...it }) => it),
      }),
      { headers: authHeaders(token, idempotencyHeader()), tags: { type: 'write', name: 'PlaceOrder' } }
    );
    const placed = check(orderRes, { 'place order: 201': (r) => r.status === 201 });
    if (placed) {
      amountDue += orderTotal;
    }
    sleep(randomIntBetween(3, 10));
  }

  if (amountDue === 0) {
    // Hiçbir sipariş kabul edilmediyse ödeme/kapama denemenin anlamı yok —
    // check açık kalır (gerçek hayatta da böyle: boş adisyon kapanmaz).
    return;
  }

  // 4. Ödeme öncesi kasiyer hesabı kapatmaya karar veriyor.
  sleep(randomIntBetween(3, 10));
  const method = Math.random() < 0.6 ? 'cash' : 'terminal';
  const payRes = http.post(
    `${BASE_URL}/api/v1/payments`,
    JSON.stringify({ branch_id: BRANCH_ID, check_id: checkId, method, amount_total: amountDue, currency: 'TRY' }),
    { headers: authHeaders(token, idempotencyHeader()), tags: { type: 'write', name: 'RegisterPayment' } }
  );
  const paid = check(payRes, { 'register payment: 201': (r) => r.status === 201 });
  if (!paid) {
    return;
  }
  sleep(randomIntBetween(1, 3));

  // 5. Adisyonu kapat.
  const closeRes = http.post(`${BASE_URL}/api/v1/pos/checks/${checkId}/close`, null, {
    headers: authHeaders(token, idempotencyHeader()),
    tags: { type: 'write', name: 'CloseCheck' },
  });
  check(closeRes, { 'close check: 200': (r) => r.status === 200 });
}

export default cashierFlow;
