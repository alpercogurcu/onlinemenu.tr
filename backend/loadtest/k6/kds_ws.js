// backend/loadtest/k6/kds_ws.js
//
// KDS (mutfak ekranı) WebSocket senaryosu — backend/internal/modules/pos/ws.
// Şube başına 1-2 mutfak ekranı bağlantısını simüle eder ve iki şeyi ölçer:
//   1. Bağlantı/snapshot alımı başarı oranı (handshake + ilk frame).
//   2. order.placed olayının NATS → outbox dispatcher → JetStream → WS hub
//      zincirinden kasiyer ekranına düşme gecikmesi (custom Trend metric).
//
// Gecikme, aynı VU içinde WS mesajının OccurredAt alanı (sunucu saat
// damgası) ile mesajın k6 tarafında alındığı an arasındaki farktır — aynı
// host saatini kullandığından (localhost testi) çapraz-VU korelasyonuna
// gerek yoktur ve NATS→WS yayılma gecikmesini temiz biçimde izole eder.
// Snapshot satırları (seq=0) bu metriğe dahil edilmez: onlar tek bir NATS
// olayından değil, bağlantı anındaki DB durumundan üretilir.
//
// Çalıştırma:
//   k6 run backend/loadtest/k6/kds_ws.js
//   k6 run -e PROFILE=full -e KDS_COUNT=4 backend/loadtest/k6/kds_ws.js
import ws from 'k6/ws';
import { check } from 'k6';
import { Trend, Counter } from 'k6/metrics';

import { BASE_URL, BRANCH_ID, ADMIN_EMAIL, devLogin } from './lib/common.js';

const PROFILE = __ENV.PROFILE || 'smoke';
const KDS_COUNT = parseInt(__ENV.KDS_COUNT || '2', 10);

// Toplam bağlantı süresi, pos_sale_flow.js'in aynı PROFILE'daki toplam
// senaryo süresine denk gelir — mixed_load.js'te ikisi aynı anda koşar.
const DURATIONS = { smoke: '2m', full: '17m' };
const DURATION = DURATIONS[PROFILE] || DURATIONS.smoke;
const DURATION_MS = (() => {
  const m = /^(\d+)m$/.exec(DURATION);
  return m ? parseInt(m[1], 10) * 60 * 1000 : 120000;
})();

export const kdsEventLatency = new Trend('kds_event_latency_ms', true);
export const kdsSnapshotOrders = new Counter('kds_snapshot_orders_total');

export const options = {
  scenarios: {
    kitchen_displays: {
      executor: 'constant-vus',
      exec: 'kdsFlow',
      vus: KDS_COUNT,
      duration: DURATION,
    },
  },
  thresholds: {
    // 2 saniyelik outbox poll_interval (bkz. rapor: bilinen gecikme tabanı)
    // + JetStream + WS write timeout üzerine pay bırakan bir üst sınır.
    kds_event_latency_ms: ['p(95)<3000'],
    ws_connecting: ['p(95)<1000'],
  },
};

export function setup() {
  return { token: devLogin(ADMIN_EMAIL) };
}

export function kdsFlow(data) {
  const url = `${BASE_URL.replace('http', 'ws')}/api/v1/pos/ws/kitchen?branch_id=${BRANCH_ID}`;
  const res = ws.connect(url, { headers: { Authorization: `Bearer ${data.token}` } }, function (socket) {
    socket.on('message', (raw) => {
      const receivedAt = Date.now();
      let msg;
      try {
        msg = JSON.parse(raw);
      } catch (e) {
        return;
      }
      if (msg.type === 'snapshot') {
        kdsSnapshotOrders.add((msg.orders || []).length);
        return;
      }
      if (msg.type === 'order.placed' && msg.seq > 0 && msg.occurred_at) {
        const occurredAt = new Date(msg.occurred_at).getTime();
        kdsEventLatency.add(receivedAt - occurredAt);
      }
    });
    socket.on('error', (e) => console.error(`kds ws error: ${e}`));
    // Bağlantıyı senaryonun tüm süresi boyunca canlı tut; k6 ws iterasyonu
    // socket kapanana kadar bloke olur.
    socket.setTimeout(() => socket.close(), DURATION_MS);
  });
  check(res, { 'kds ws: handshake 101': (r) => r && r.status === 101 });
}

export default kdsFlow;
