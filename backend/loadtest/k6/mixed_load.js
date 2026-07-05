// backend/loadtest/k6/mixed_load.js
//
// Karma profil: pos_sale_flow.js'in kasiyer senaryosu + kds_ws.js'in mutfak
// ekranı bağlantıları aynı k6 koşusunda, iki ayrı "scenario" olarak çalışır.
// Bu, ROADMAP'in "500 aktif POS simülasyonu" maddesinin tam karşılığıdır:
// kasiyer trafiği + arka planda sürekli açık KDS WebSocket bağlantıları.
//
// Çalıştırma:
//   k6 run backend/loadtest/k6/mixed_load.js                 (smoke: 25 kasiyer + 2 KDS, 2dk)
//   k6 run -e PROFILE=full backend/loadtest/k6/mixed_load.js (full: 500 kasiyer ramp + N KDS, ~17dk)
//
// PROFILE=full'ü LOKALDE tam parametreleriyle çalıştırmadık — bkz.
// backend/loadtest/README.md "500-VU tam koşu için ortam önerisi".
import { cashierFlow } from './pos_sale_flow.js';
import { kdsFlow, kdsEventLatency, kdsSnapshotOrders } from './kds_ws.js';
import {
  ADMIN_EMAIL,
  STANDARD_THRESHOLDS,
  devLogin,
  buildCashierTokenPool,
  ensureCatalog,
} from './lib/common.js';

// Re-export: k6, `exec: 'cashierFlow'` / `exec: 'kdsFlow'` referanslarını bu
// dosyanın (ana script) export'ları arasında arar.
export { cashierFlow, kdsFlow };
// Metrikleri de re-export ediyoruz ki k6 çıktı özetinde görünsünler (bir
// metric yalnızca import edildiği + kullanıldığı dosyada aktif sayılır).
export { kdsEventLatency, kdsSnapshotOrders };

const PROFILE = __ENV.PROFILE || 'smoke';
const KDS_COUNT = parseInt(__ENV.KDS_COUNT || '2', 10);

const CASHIER_SCENARIOS = {
  smoke: { executor: 'constant-vus', vus: 25, duration: '2m' },
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
// KDS bağlantıları, kasiyer senaryosunun toplam süresi boyunca açık kalır.
const KDS_DURATION = { smoke: '2m', full: '17m' };

export const options = {
  scenarios: {
    cashiers: Object.assign({ exec: 'cashierFlow' }, CASHIER_SCENARIOS[PROFILE] || CASHIER_SCENARIOS.smoke),
    kitchen_displays: {
      executor: 'constant-vus',
      exec: 'kdsFlow',
      vus: KDS_COUNT,
      duration: KDS_DURATION[PROFILE] || KDS_DURATION.smoke,
    },
  },
  thresholds: Object.assign({}, STANDARD_THRESHOLDS, {
    kds_event_latency_ms: ['p(95)<3000'],
  }),
};

export function setup() {
  const adminToken = devLogin(ADMIN_EMAIL);
  const products = ensureCatalog(adminToken);
  if (products.length === 0) {
    throw new Error('setup: katalogda ürün yok ve oluşturulamadı — dev-seed ve migration durumunu kontrol edin');
  }
  const tokens = buildCashierTokenPool();
  return { tokens, products, token: adminToken };
}
