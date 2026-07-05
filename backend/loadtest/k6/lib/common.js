// lib/common.js — pos_sale_flow.js, kds_ws.js ve mixed_load.js arasında
// paylaşılan sabitler ve yardımcı fonksiyonlar.
//
// Not: k6 goja runtime'ı Node modüllerini desteklemez (crypto, uuid paketi
// vb. yok) — bu yüzden basit bir uuidv4 üretici burada elle yazılmıştır.
// RFC 4122 uyumluluğu önemli değil; tek gereksinim Idempotency-Key ve
// table_label alanları için "aynı test koşusu içinde çakışmayan" değer
// üretmek.

import http from 'k6/http';
import { check } from 'k6';

// --- Ortam ------------------------------------------------------------
export const BASE_URL = __ENV.BASE_URL || 'http://localhost:8081';
export const BRANCH_ID = __ENV.BRANCH_ID || 'bbbbbbbb-0000-0000-0000-000000000001';
export const ADMIN_EMAIL = __ENV.ADMIN_EMAIL || 'admin@onlinemenu.tr';
// backend/loadtest/seed.sql tam olarak bu kadar kasiyer oluşturur — ikisini
// birlikte değiştirin.
export const CASHIER_COUNT = parseInt(__ENV.CASHIER_COUNT || '50', 10);

// --- uuidv4 -------------------------------------------------------------
export function uuidv4() {
  // Math.random tabanlı, kriptografik olarak güvenli değil — yük testi
  // için yeterli (yalnızca Idempotency-Key/table_label benzersizliği için
  // kullanılıyor, güvenlik amaçlı değil).
  return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, (c) => {
    const r = (Math.random() * 16) | 0;
    const v = c === 'x' ? r : (r & 0x3) | 0x8;
    return v.toString(16);
  });
}

// --- Auth -----------------------------------------------------------------

// devLogin, APP_ENV=dev'de yalnızca açık olan /dev/login uç noktasıyla bir
// CTX staff token'ı alır (cmd/api/main.go: devLoginHandler). Üretimde bu
// endpoint yok — k6 senaryoları yalnızca dev/staging ortamında çalışır.
export function devLogin(email) {
  const res = http.post(
    `${BASE_URL}/dev/login`,
    JSON.stringify({ email }),
    { headers: { 'Content-Type': 'application/json' }, tags: { type: 'setup' } }
  );
  if (res.status !== 200) {
    throw new Error(`dev/login failed for ${email}: ${res.status} ${res.body}`);
  }
  return JSON.parse(res.body).token;
}

// buildCashierTokenPool, backend/loadtest/seed.sql tarafından oluşturulan
// loadtest-cashier-{1..N}@onlinemenu.tr kullanıcıları için CTX token alır.
// setup() içinde bir kez çağrılır (VU başına değil) — 500 VU aynı N token
// havuzunu __VU % N ile paylaşır; bu, gerçek POS filosunda "500 farklı
// kasiyer" yerine "N terminal, çoklu vardiya" modelini temsil eder (bkz.
// README "Bilinen sınırlar").
export function buildCashierTokenPool() {
  const tokens = [];
  for (let i = 1; i <= CASHIER_COUNT; i++) {
    tokens.push(devLogin(`loadtest-cashier-${i}@onlinemenu.tr`));
  }
  return tokens;
}

export function authHeaders(token, extra) {
  return Object.assign({ Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' }, extra || {});
}

export function idempotencyHeader() {
  return { 'Idempotency-Key': uuidv4() };
}

// --- Katalog bootstrap ------------------------------------------------------

// SEED_MENU, dev-seed.sql'de ürün yoksa (temiz veritabanı) setup() sırasında
// oluşturulacak asgari menüdür. dev ortamında genelde en az bir ürün zaten
// var (bkz. README) — bu yalnızca sıfırdan bir ortamda senaryonun kendi
// kendine yeter (self-sufficient) olması içindir.
const SEED_MENU = [
  { category: 'Ana Yemekler', name: 'Adana Kebap', price: 18000, taxBps: 800 },
  { category: 'Ana Yemekler', name: 'Izgara Tavuk', price: 15000, taxBps: 800 },
  { category: 'Ana Yemekler', name: 'Lahmacun', price: 6000, taxBps: 800 },
  { category: 'İçecekler', name: 'Ayran', price: 2500, taxBps: 100 },
  { category: 'İçecekler', name: 'Kola', price: 3500, taxBps: 100 },
  { category: 'Tatlılar', name: 'Künefe', price: 12000, taxBps: 800 },
];

// ensureCatalog GET /catalog/products ile mevcut ürünleri kontrol eder;
// boşsa (ilk koşu / temiz DB) admin token'ıyla SEED_MENU'yü oluşturur.
// Idempotent: ürün adı bazında dedup yoktur (catalog.product.create'te unique
// constraint yok) — bu yüzden yalnızca liste boşken create çağrılır, ikinci
// bir k6 koşusunda ürünler zaten var olduğundan tekrar oluşturulmaz.
export function ensureCatalog(adminToken) {
  const listRes = http.get(`${BASE_URL}/api/v1/catalog/products`, {
    headers: authHeaders(adminToken),
    tags: { type: 'setup' },
  });
  check(listRes, { 'setup: list products OK': (r) => r.status === 200 });
  let products = JSON.parse(listRes.body || '[]').filter((p) => p.is_active);

  if (products.length > 0) {
    return products;
  }

  const categoryCache = {};
  for (const item of SEED_MENU) {
    if (!categoryCache[item.category]) {
      const catRes = http.post(
        `${BASE_URL}/api/v1/catalog/categories`,
        JSON.stringify({ name: item.category }),
        { headers: authHeaders(adminToken), tags: { type: 'setup' } }
      );
      check(catRes, { 'setup: create category OK': (r) => r.status === 201 });
      categoryCache[item.category] = JSON.parse(catRes.body).id;
    }

    const prodRes = http.post(
      `${BASE_URL}/api/v1/catalog/products`,
      JSON.stringify({
        category_id: categoryCache[item.category],
        name: item.name,
        price_amount: item.price,
        currency: 'TRY',
        tax_rate_bps: item.taxBps,
      }),
      { headers: authHeaders(adminToken), tags: { type: 'setup' } }
    );
    check(prodRes, { 'setup: create product OK': (r) => r.status === 201 });
    products.push(JSON.parse(prodRes.body));
  }
  return products;
}

export function randomItem(arr) {
  return arr[Math.floor(Math.random() * arr.length)];
}

export function randomIntBetween(min, max) {
  return Math.floor(Math.random() * (max - min + 1)) + min;
}

// --- Ortak threshold seti -----------------------------------------------
//
// Delta/ADR dokümanlarında POS hot path için sayısal bir p95 hedefi
// tanımlanmamış (bkz. rapor). Aşağıdaki değerler bu yük testi için makul
// bir başlangıç bütçesi olarak seçildi ve raporda gerekçelendirildi:
//   - okuma (katalog/liste): p95 < 200ms — kasiyer ekranının responsive
//     hissettirmesi için tipik POS UX beklentisi.
//   - yazma (check/order/payment): p95 < 500ms — fiscal adapter + RLS +
//     idempotency middleware zincirini içeren, tek satırlık DB yazması
//     olan bir işlem için makul üst sınır.
//   - hata oranı: %1 — 500 VU altında ara sıra ağ/timeout kaynaklı hata
//     toleransı, ama sistemsel bir hata sınıfını maskelemeyecek kadar sıkı.
export const STANDARD_THRESHOLDS = {
  http_req_failed: ['rate<0.01'],
  'http_req_duration{type:read}': ['p(95)<200'],
  'http_req_duration{type:write}': ['p(95)<500'],
};
