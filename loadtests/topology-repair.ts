// topology-repair.ts - Exchange Loss Under Load (topology self-repair)
//
// go-bricks v0.67.0 (#1776/#1779, ADR-113 amendment) made every pooled publisher
// a driver of the topology redeclare pass. When an operator deletes an exchange
// under a live app, the next publish takes the broker's 404 on the publisher's
// channel, the client opens a replacement, and that NEW channel wakes the
// registry, which replays every exchange, queue and binding it declared over its
// own connection. No restart. Before v0.67.0 every later publish failed with
// ErrPublishRetriesExhausted until the process restarted.
//
// This test deletes both AMQP exchanges midway through steady load and reports
// what the repair costs:
//
//   products          constant-arrival POST /products -> outbox relay -> product-events
//   payments          constant-arrival POST /payments/authorize -> sealed publish
//                     -> payment-events -> payments.authorized (consumer)
//                                       -> payments.authorized.tap (no consumer)
//   delete_exchanges  one iteration at TOPO_DELETE_AT: DELETE both exchanges via
//                     the management API, then poll until both are back and
//                     payment-events is re-bound to both queues
//   tap_drain         one VU draining the tap via the management API, counting
//                     the distinct orders that arrived
//
// The report compares the 202s from /payments/authorize with the distinct orders
// that reached the tap. The difference is "lost in repair window". The pass is
// not atomic (exchanges, then queues, then bindings) and the typed publisher sets
// no Mandatory flag, so a publish that lands after payment-events is back but
// before its bindings are is broker-acked, unroutable and dropped, while the
// caller already got 202. That is the documented ack-and-drop window, so it is a
// REPORTED number, never a failed threshold. Thresholds cover HTTP error rate and
// latency only.
//
// Why drain the tap instead of reading its depth: the tap is capped at
// x-max-length 100 (drop-head), so its depth stops counting at 100, and the
// management API reports depth on the ~5s stats emission tick.
// POST /api/queues/.../get reads the queue itself, immediately. The tap stands in
// for payments.authorized, whose consumer removes each delivery at once; the pass
// re-binds both queues one declare apart. As a cross-check the broker's own
// message_stats.publish counters for BOTH queues (same ~5s tick) are read in
// setup and again in teardown, after the drain tail.
//
// Why product-events too: it is the outbox relay's exchange and has NO bound
// queue in this demo, so product events are unroutable by design. Its repair
// proves the relay's publishes are confirmed again, not that they are delivered.
// The payments path carries the end-to-end delivery measurement. Any source's new
// channel replays the WHOLE topology, so whichever publish trips the 404 first
// repairs both exchanges.
//
// SECURITY: payment bodies carry the documented network test PANs the tokens
// load tests already use (TEST_PANS) and are never logged. App responses are
// discarded (responseType 'none') and no line prints a request. Do NOT add
// --http-debug: it prints those bodies and the management API Authorization
// header. Broker credentials come from RABBIT_USER / RABBIT_PASS (defaults: the
// broker's default dev user), and they travel only in that header, never in a
// URL or a log line. Like scripts/lib/rabbitmq-mgmt.sh, the script refuses
// plaintext http:// to a non-loopback RABBIT_MGMT.
//
// Destructive on purpose: it deletes two exchanges on the broker it points at,
// so it is NOT part of `make loadtest-all`. Run it against a demo broker only.
//
// Usage:
//   make loadtest-topology-repair            # maps APP_URL onto K6_BASE_URL
//   k6 run loadtests/topology-repair.ts
//   RABBIT_MGMT=http://localhost:15673 k6 run loadtests/topology-repair.ts
//   TOPO_PAYMENT_RATE=20 TOPO_DURATION=3m TOPO_DELETE_AT=90s k6 run loadtests/topology-repair.ts
//
// Knobs (env): K6_BASE_URL, RABBIT_MGMT, RABBIT_USER, RABBIT_PASS, RABBIT_VHOST,
//   TOPO_PRODUCT_RATE (5/s), TOPO_PAYMENT_RATE (5/s), TOPO_DURATION (120s),
//   TOPO_DELETE_AT (60s), TOPO_DISRUPTION (15s), TOPO_REPAIR_TIMEOUT (30s),
//   TOPO_DRAIN_TAIL (20s), PERF_SUMMARY_FILE (full summary JSON, see config.ts)

import http from 'k6/http';
import type { RefinedResponse } from 'k6/http';
import { check, sleep } from 'k6';
import encoding from 'k6/encoding';
import exec from 'k6/execution';
import { Counter, Gauge, Rate } from 'k6/metrics';
import type { Options } from 'k6/options';
import { config, getURL, getRandomProduct, headers, summaryOutputs } from './config.ts';
import type { CreateProductInput } from './types/index.ts';
// Card numbers: the published, Luhn-valid TEST_PANS set every tokens load test
// draws from, kept in one place. The payments boundary accepts any 13-19 digit
// string. DEMO DATA ONLY: never put a real card number through this test.
import { pickPAN } from './tokens-common.ts';

// ---------------------------------------------------------------------------
// Knobs
// ---------------------------------------------------------------------------

/** Parse a positive-integer env value; fallback on unset/zero/malformed input. */
function positiveIntOr(value: string | undefined, fallback: number): number {
  const n = parseInt(value || '', 10);
  return Number.isFinite(n) && n > 0 ? n : fallback;
}

/** Parse a k6-style duration ("90s", "2m", "90") to whole seconds; fallback on bad input. */
function secondsOr(value: string | undefined, fallback: number): number {
  if (!value) return fallback;
  const m = /^(\d+)(s|m)?$/.exec(value.trim());
  if (!m) return fallback;
  return parseInt(m[1], 10) * (m[2] === 'm' ? 60 : 1);
}

const PRODUCT_RATE = positiveIntOr(__ENV.TOPO_PRODUCT_RATE, 5);
const PAYMENT_RATE = positiveIntOr(__ENV.TOPO_PAYMENT_RATE, 5);
const LOAD_S = secondsOr(__ENV.TOPO_DURATION, 120);
const DELETE_AT_S = secondsOr(__ENV.TOPO_DELETE_AT, 60);
const DISRUPTION_S = secondsOr(__ENV.TOPO_DISRUPTION, 15);
const REPAIR_TIMEOUT_S = secondsOr(__ENV.TOPO_REPAIR_TIMEOUT, 30);
// Longer than two ~5s stats ticks, so teardown's message_stats read has settled.
const DRAIN_TAIL_S = secondsOr(__ENV.TOPO_DRAIN_TAIL, 20);

if (DELETE_AT_S <= 0 || DELETE_AT_S >= LOAD_S) {
  throw new Error(`TOPO_DELETE_AT (${DELETE_AT_S}s) must fall inside TOPO_DURATION (${LOAD_S}s)`);
}

// ---------------------------------------------------------------------------
// Topology. Must match internal/modules/payments/module.go,
// internal/modules/products/module.go and outbox.defaultexchange.
// ---------------------------------------------------------------------------

const PRODUCT_EXCHANGE = 'product-events';
const PAYMENT_EXCHANGE = 'payment-events';
const PAYMENT_ROUTING_KEY = 'payment.authorized';
const PAYMENT_QUEUE = 'payments.authorized';
const TAP_QUEUE = 'payments.authorized.tap';
const TAP_MAX_LENGTH = 100; // tapMaxLength in payments/module.go (drop-head)
const DRAIN_BATCH = 250;

// ---------------------------------------------------------------------------
// RabbitMQ management API
// ---------------------------------------------------------------------------

const RABBIT_MGMT = (__ENV.RABBIT_MGMT || 'http://localhost:15672').replace(/\/+$/, '');
const RABBIT_VHOST = __ENV.RABBIT_VHOST || '%2F'; // default vhost "/" percent-encoded

// Same rule as guard_mgmt_endpoint in scripts/lib/rabbitmq-mgmt.sh: every call
// carries broker credentials, so plaintext HTTP is accepted only when the request
// cannot leave the host. Runs in the init context, before any request.
function guardMgmtEndpoint(url: string): void {
  if (url.startsWith('https://')) return;
  if (url.startsWith('http://')) {
    const host = url.slice('http://'.length).split('/')[0];
    if (/^(localhost|127\.0\.0\.1|\[::1\])(:[0-9]+)?$/.test(host)) return;
    throw new Error(
      `RABBIT_MGMT would send broker credentials in the clear to non-loopback host '${host}' — use https://, or a loopback host`,
    );
  }
  throw new Error('RABBIT_MGMT must start with https://, or http:// for a loopback host');
}
guardMgmtEndpoint(RABBIT_MGMT);

const MGMT_HEADERS: Record<string, string> = {
  'Content-Type': 'application/json',
  Authorization: `Basic ${encoding.b64encode(`${__ENV.RABBIT_USER || 'guest'}:${__ENV.RABBIT_PASS || 'guest'}`)}`,
};

function mgmt(method: string, path: string, body: object | null, expected: number[]): RefinedResponse<'text'> {
  return http.request(method, `${RABBIT_MGMT}${path}`, body === null ? null : JSON.stringify(body), {
    headers: MGMT_HEADERS,
    responseType: 'text',
    // A 404 while polling for a deleted exchange is the expected answer, not an
    // http_req_failed sample.
    responseCallback: http.expectedStatuses(...expected),
    tags: { endpoint: 'rabbitmq_mgmt' },
  });
}

interface QueueBinding {
  source: string;
  routing_key: string;
}

interface QueueInfo {
  message_stats?: { publish?: number };
}

interface TapMessage {
  payload: string;
  payload_encoding: string; // "string" or "base64" with encoding=auto
  message_count: number; // messages left in the queue after this one
}

// The signed payload of a sealed payment.authorized body: orderId, amount and
// currency stay clear, card is a compact JWE.
interface SealedPaymentDocument {
  orderId?: unknown;
  amount?: unknown;
  card?: unknown;
}

function exchangeExists(name: string): boolean {
  return mgmt('GET', `/api/exchanges/${RABBIT_VHOST}/${name}`, null, [200, 404]).status === 200;
}

/** Both payment queues carry a payment-events binding on payment.authorized again. */
function paymentBindingsBack(): boolean {
  return [PAYMENT_QUEUE, TAP_QUEUE].every((queue) => {
    const res = mgmt('GET', `/api/queues/${RABBIT_VHOST}/${queue}/bindings`, null, [200, 404]);
    if (res.status !== 200) return false;
    const bindings = JSON.parse(res.body as string) as QueueBinding[];
    return bindings.some((b) => b.source === PAYMENT_EXCHANGE && b.routing_key === PAYMENT_ROUTING_KEY);
  });
}

/** A queue's cumulative message_stats.publish, or null when the broker reports no stats. */
function routedCount(queue: string): number | null {
  const res = mgmt('GET', `/api/queues/${RABBIT_VHOST}/${queue}`, null, [200]);
  if (res.status !== 200) return null;
  const info = JSON.parse(res.body as string) as QueueInfo;
  return info.message_stats ? info.message_stats.publish || 0 : null;
}

// ---------------------------------------------------------------------------
// Metrics
// ---------------------------------------------------------------------------

const productsCreated = new Counter('products_created');
const productsNotCreated = new Counter('products_not_created');
const paymentsAccepted = new Counter('payments_accepted');
const paymentsNotAccepted = new Counter('payments_not_accepted');
// Success rate per phase, so the report shows the error burst is confined to
// the disruption window rather than smeared over the run.
const paymentOkBaseline = new Rate('payment_ok_baseline');
const paymentOkDisruption = new Rate('payment_ok_disruption');
const paymentOkRecovered = new Rate('payment_ok_recovered');

// Milliseconds from the DELETE to each piece being back; -1 when it never came back.
const productExchangeRepairMs = new Gauge('product_events_repair_ms');
const paymentExchangeRepairMs = new Gauge('payment_events_repair_ms');
const paymentBindingsRepairMs = new Gauge('payment_bindings_repair_ms');
const topologyFinalOk = new Gauge('topology_final_ok');

const tapArrived = new Counter('tap_arrived'); // distinct orders of THIS run
const tapDuplicates = new Counter('tap_duplicates'); // same order seen again
const tapForeign = new Counter('tap_foreign'); // not a sealed body of this run
const tapUnsealed = new Counter('tap_unsealed'); // card was not a compact JWE
const tapOverflowRisk = new Counter('tap_overflow_risk'); // depth reached x-max-length
const drainErrors = new Counter('tap_drain_errors');
// message_stats.publish deltas, read by teardown after the drain tail.
const tapRoutedStats = new Gauge('tap_routed_stats');
const queueRoutedStats = new Gauge('queue_routed_stats');

// ---------------------------------------------------------------------------
// Options
// ---------------------------------------------------------------------------

export const options: Options = {
  scenarios: {
    products: {
      executor: 'constant-arrival-rate',
      exec: 'createProduct',
      rate: PRODUCT_RATE,
      timeUnit: '1s',
      duration: `${LOAD_S}s`,
      preAllocatedVUs: 10,
      maxVUs: 50,
      gracefulStop: '5s',
    },
    payments: {
      executor: 'constant-arrival-rate',
      exec: 'authorizePayment',
      rate: PAYMENT_RATE,
      timeUnit: '1s',
      duration: `${LOAD_S}s`,
      preAllocatedVUs: 10,
      maxVUs: 50,
      gracefulStop: '5s',
    },
    delete_exchanges: {
      executor: 'shared-iterations',
      exec: 'deleteExchanges',
      vus: 1,
      iterations: 1,
      startTime: `${DELETE_AT_S}s`,
      maxDuration: `${REPAIR_TIMEOUT_S + 10}s`,
    },
    tap_drain: {
      executor: 'constant-vus',
      exec: 'drainTap',
      vus: 1, // exactly one: the distinct-order Set lives in this VU
      duration: `${LOAD_S + DRAIN_TAIL_S}s`,
      gracefulStop: '10s',
    },
  },
  // HTTP error rate and latency of the app calls only. The management API calls
  // are tagged scenario:delete_exchanges / tap_drain and stay out of these.
  thresholds: {
    'http_req_failed{scenario:products}': ['rate<0.01'],
    // The publish that takes the 404 retries on the new channel; a small burst of
    // failures inside the repair window is tolerated, a lasting outage is not.
    'http_req_failed{scenario:payments}': ['rate<0.02'],
    'http_req_duration{scenario:products}': ['p(95)<500', 'p(99)<1000'],
    'http_req_duration{scenario:payments}': ['p(95)<800', 'p(99)<2000'],
  },
  summaryTrendStats: ['avg', 'min', 'med', 'max', 'p(90)', 'p(95)', 'p(99)'],
};

interface SetupData {
  runAmount: number;
  tapRoutedBaseline: number | null;
  queueRoutedBaseline: number | null;
}

// ---------------------------------------------------------------------------
// Setup: the app and the broker must be healthy before we break anything
// ---------------------------------------------------------------------------

export function setup(): SetupData {
  console.log('🚀 Starting Topology Repair Load Test (exchange loss under load)');
  console.log(`📊 Target: ${config.baseURL}${config.apiPrefix} (products ${PRODUCT_RATE}/s, payments ${PAYMENT_RATE}/s, ${LOAD_S}s)`);
  console.log(`💥 Deletes ${PRODUCT_EXCHANGE} and ${PAYMENT_EXCHANGE} at t=${DELETE_AT_S}s via ${RABBIT_MGMT}`);
  console.log('');

  const health = http.get(getURL('/health'));
  if (health.status !== 200) {
    throw new Error(`API health check failed (HTTP ${health.status}) — start the app with 'make run'`);
  }

  const overview = mgmt('GET', '/api/overview', null, [200]);
  if (overview.status !== 200) {
    throw new Error(
      `RabbitMQ management API not reachable at ${RABBIT_MGMT} (HTTP ${overview.status}) — check RABBIT_MGMT, RABBIT_USER and RABBIT_PASS`,
    );
  }

  for (const exchange of [PRODUCT_EXCHANGE, PAYMENT_EXCHANGE]) {
    if (!exchangeExists(exchange)) {
      throw new Error(`exchange '${exchange}' is missing before the test started — restart the app so it declares its topology`);
    }
  }
  if (!paymentBindingsBack()) {
    throw new Error(`${PAYMENT_QUEUE} / ${TAP_QUEUE} are not bound to ${PAYMENT_EXCHANGE} — restart the app so it declares its topology`);
  }

  // Start from an empty tap so every message the drainer sees was published by
  // this run (the amount marker below double-checks that).
  const purge = mgmt('DELETE', `/api/queues/${RABBIT_VHOST}/${TAP_QUEUE}/contents`, null, [200, 204]);
  if (purge.status !== 200 && purge.status !== 204) {
    throw new Error(`could not purge '${TAP_QUEUE}' (HTTP ${purge.status})`);
  }

  // Every payment in this run carries this amount (minor units), and the drainer
  // counts only bodies whose clear amount matches.
  const runAmount = 10000 + Math.floor(Math.random() * 90000);
  const tapRoutedBaseline = routedCount(TAP_QUEUE);
  const queueRoutedBaseline = routedCount(PAYMENT_QUEUE);

  console.log(`✅ App healthy, both exchanges bound, '${TAP_QUEUE}' purged`);
  console.log(`🏷️  Run marker: amount=${runAmount} (minor units) on every payment`);
  console.log('');
  return { runAmount, tapRoutedBaseline, queueRoutedBaseline };
}

// ---------------------------------------------------------------------------
// Load: products (outbox -> product-events) and payments (sealed -> payment-events)
// ---------------------------------------------------------------------------

export function createProduct(): void {
  const sample = getRandomProduct();
  const product: CreateProductInput = {
    name: `${sample.name} topology-repair ${Date.now()}`,
    description: sample.description,
    price: sample.price,
  };

  const res = http.post(getURL('/products'), JSON.stringify(product), {
    headers,
    responseType: 'none',
    tags: { endpoint: 'create_product' },
  });

  const created = res.status === 201;
  (created ? productsCreated : productsNotCreated).add(1);
  check(res, { 'create: status is 201': () => created });
}

let firstPaymentFailureLogged = false;

export function authorizePayment(data: SetupData): void {
  const body = JSON.stringify({
    amount: data.runAmount,
    currency: 'USD',
    card: { pan: pickPAN(), expMonth: 12, expYear: 2030, holder: 'LOAD TEST' },
  });

  // responseType 'none': the response is never read, so it cannot be logged. The
  // 202 alone is the claim this test checks against the tap.
  const res = http.post(getURL('/payments/authorize'), body, {
    headers,
    responseType: 'none',
    tags: { endpoint: 'payments_authorize' },
  });

  const accepted = res.status === 202;
  (accepted ? paymentsAccepted : paymentsNotAccepted).add(1);
  check(res, { 'authorize: status is 202': () => accepted });

  // Phase relative to the payments scenario start, which is also the zero the
  // delete_exchanges startTime counts from.
  const elapsedS = (Date.now() - exec.scenario.startTime) / 1000;
  let phase = 'baseline';
  if (elapsedS >= DELETE_AT_S + DISRUPTION_S) {
    phase = 'recovered';
    paymentOkRecovered.add(accepted);
  } else if (elapsedS >= DELETE_AT_S) {
    phase = 'disruption';
    paymentOkDisruption.add(accepted);
  } else {
    paymentOkBaseline.add(accepted);
  }

  // Status and phase only, once per VU: the body holds a card number.
  if (!accepted && !firstPaymentFailureLogged) {
    firstPaymentFailureLogged = true;
    console.warn(`payments: first non-202 on this VU: status=${res.status} phase=${phase} t=${elapsedS.toFixed(1)}s`);
  }
}

// ---------------------------------------------------------------------------
// Disruption: delete both exchanges, then time the repair
// ---------------------------------------------------------------------------

export function deleteExchanges(): void {
  const deletedAt = Date.now();
  for (const exchange of [PRODUCT_EXCHANGE, PAYMENT_EXCHANGE]) {
    const res = mgmt('DELETE', `/api/exchanges/${RABBIT_VHOST}/${exchange}`, null, [204]);
    check(res, { [`delete ${exchange}: status is 204`]: (r) => r.status === 204 });
    if (res.status !== 204) {
      console.error(`❌ DELETE ${exchange} returned HTTP ${res.status} — no repair to measure for it`);
    }
  }
  console.log(`💥 Deleted ${PRODUCT_EXCHANGE} and ${PAYMENT_EXCHANGE}; the next publish takes the 404`);

  // Nothing repairs until a publish trips the 404, so these times include the
  // wait for the next publish (~1/rate), plus the pass itself.
  let productBack = -1;
  let paymentBack = -1;
  let bindingsBack = -1;
  const deadline = deletedAt + REPAIR_TIMEOUT_S * 1000;
  while (Date.now() < deadline && (productBack < 0 || paymentBack < 0 || bindingsBack < 0)) {
    if (productBack < 0 && exchangeExists(PRODUCT_EXCHANGE)) productBack = Date.now() - deletedAt;
    if (paymentBack < 0 && exchangeExists(PAYMENT_EXCHANGE)) paymentBack = Date.now() - deletedAt;
    if (paymentBack >= 0 && bindingsBack < 0 && paymentBindingsBack()) bindingsBack = Date.now() - deletedAt;
    sleep(0.1);
  }

  productExchangeRepairMs.add(productBack);
  paymentExchangeRepairMs.add(paymentBack);
  paymentBindingsRepairMs.add(bindingsBack);

  const repaired = productBack >= 0 && paymentBack >= 0 && bindingsBack >= 0;
  check(null, { 'topology repaired without a restart': () => repaired });
  if (repaired) {
    console.log(
      `🔧 Repaired: ${PRODUCT_EXCHANGE} +${productBack}ms, ${PAYMENT_EXCHANGE} +${paymentBack}ms, its bindings +${bindingsBack}ms`,
    );
  } else {
    console.error(
      `❌ Not repaired within ${REPAIR_TIMEOUT_S}s — is the app on go-bricks >= v0.67.0? Restart it (make run) to re-declare the topology`,
    );
  }
}

// ---------------------------------------------------------------------------
// Delivery: drain the tap and count distinct orders
// ---------------------------------------------------------------------------

// One drainer VU, so one Set. A publish retried after its confirm was lost with
// the old channel arrives twice under the same orderId; counting distinct orders
// keeps those duplicates from hiding a loss.
const seenOrders = new Set<string>();
let overflowWarned = false;

function countTapMessage(message: TapMessage, runAmount: number): void {
  const raw = message.payload_encoding === 'base64' ? encoding.b64decode(message.payload, 'std', 's') : message.payload;
  const segments = raw.split('.');
  let doc: SealedPaymentDocument | null = null;
  if (segments.length === 3) {
    try {
      doc = JSON.parse(encoding.b64decode(segments[1], 'rawurl', 's')) as SealedPaymentDocument;
    } catch {
      doc = null;
    }
  }
  if (doc === null || doc.amount !== runAmount || typeof doc.orderId !== 'string') {
    tapForeign.add(1);
    return;
  }
  // The Subject must still be a 5-segment compact JWE under load.
  if (typeof doc.card !== 'string' || doc.card.split('.').length !== 5) {
    tapUnsealed.add(1);
  }
  if (seenOrders.has(doc.orderId)) {
    tapDuplicates.add(1);
    return;
  }
  seenOrders.add(doc.orderId);
  tapArrived.add(1);
}

export function drainTap(data: SetupData): void {
  const res = mgmt(
    'POST',
    `/api/queues/${RABBIT_VHOST}/${TAP_QUEUE}/get`,
    { count: DRAIN_BATCH, ackmode: 'ack_requeue_false', encoding: 'auto' },
    [200],
  );
  if (res.status !== 200) {
    drainErrors.add(1);
    sleep(1);
    return;
  }

  const messages = JSON.parse(res.body as string) as TapMessage[];
  for (const message of messages) {
    countTapMessage(message, data.runAmount);
  }

  // Depth at the moment of this GET. At x-max-length the broker drops the oldest
  // message, and a dropped message would read as lost.
  if (messages.length > 0 && messages.length + messages[messages.length - 1].message_count >= TAP_MAX_LENGTH) {
    tapOverflowRisk.add(1);
    if (!overflowWarned) {
      overflowWarned = true;
      console.warn(`⚠️  '${TAP_QUEUE}' reached x-max-length ${TAP_MAX_LENGTH}; lower TOPO_PAYMENT_RATE for an exact count`);
    }
  }

  if (messages.length < DRAIN_BATCH) sleep(1);
}

// ---------------------------------------------------------------------------
// Teardown: final topology state and the broker's own count
// ---------------------------------------------------------------------------

export function teardown(data: SetupData): void {
  const productBack = exchangeExists(PRODUCT_EXCHANGE);
  const paymentBack = exchangeExists(PAYMENT_EXCHANGE);
  const bound = paymentBack && paymentBindingsBack();
  topologyFinalOk.add(productBack && paymentBack && bound ? 1 : 0);

  // A queue that never received a message may report no stats at all, so a null
  // baseline counts from zero. A null final means the broker keeps no stats.
  const tapFinal = routedCount(TAP_QUEUE);
  if (tapFinal !== null) tapRoutedStats.add(tapFinal - (data.tapRoutedBaseline || 0));
  const queueFinal = routedCount(PAYMENT_QUEUE);
  if (queueFinal !== null) queueRoutedStats.add(queueFinal - (data.queueRoutedBaseline || 0));

  console.log('');
  if (productBack && paymentBack && bound) {
    console.log('✅ Final state: both exchanges present and payment-events bound to both queues');
  } else {
    console.error(
      `❌ Final state: ${PRODUCT_EXCHANGE}=${productBack ? 'present' : 'MISSING'}, ${PAYMENT_EXCHANGE}=${paymentBack ? 'present' : 'MISSING'}, bindings=${bound ? 'present' : 'MISSING'} — restart the app (make run) to re-declare the topology`,
    );
  }
}

// ---------------------------------------------------------------------------
// Summary
// ---------------------------------------------------------------------------

function pct(n: number, d: number): string {
  return d > 0 ? `${((n / d) * 100).toFixed(2)}%` : 'n/a';
}

function rateText(rate: number | undefined): string {
  return rate === undefined ? 'n/a' : `${(rate * 100).toFixed(2)}%`;
}

function msText(ms: number | undefined): string {
  if (ms === undefined) return 'n/a';
  return ms < 0 ? `NOT BACK within ${REPAIR_TIMEOUT_S}s` : `${ms}ms`;
}

export function handleSummary(data: any): Record<string, string> {
  const m = data.metrics;
  const count = (name: string): number => m[name]?.values?.count || 0;
  const gauge = (name: string): number | undefined => m[name]?.values?.value;
  const rate = (name: string): number | undefined => m[name]?.values?.rate;
  const trend = (name: string, stat: string): string => {
    const v = m[name]?.values?.[stat];
    return v === undefined ? 'n/a' : `${v.toFixed(2)}ms`;
  };

  const created = count('products_created');
  const notCreated = count('products_not_created');
  const accepted = count('payments_accepted');
  const notAccepted = count('payments_not_accepted');
  const arrived = count('tap_arrived');

  // A 202 whose order never reached the tap is lost. A request that did NOT get a
  // 202 may still have been published (its outcome is unknown), and if so its
  // order is among `arrived`. So the loss is exact when every request got a 202,
  // and bounded above by the non-202 count otherwise.
  const lostMin = Math.max(0, accepted - arrived);
  const lostMax = Math.max(0, accepted - arrived + notAccepted);
  const lostText = lostMin === lostMax
    ? `${lostMin}   (exact: every request got a 202)`
    : `${lostMin}..${lostMax}   (upper bound if all ${notAccepted} non-202 publishes still landed)`;
  const tapRouted = gauge('tap_routed_stats');
  const queueRouted = gauge('queue_routed_stats');
  const finalOk = gauge('topology_final_ok');

  const lossNote = lostMax > 0
    ? `  A loss is the documented ack-and-drop window, not a test failure: the
  redeclare pass runs exchanges, then queues, then bindings, and the typed
  publisher sets no Mandatory flag. A publish that lands between the two is
  acked by the broker and dropped as unroutable, after the caller got 202.`
    : `  No 202 was lost this run. The ack-and-drop window still exists (exchanges
  are re-declared before bindings); no publish happened to land inside it.`;

  const warnings: string[] = [];
  if (count('tap_overflow_risk') > 0) warnings.push(`⚠️  The tap reached x-max-length ${TAP_MAX_LENGTH}: dropped messages inflate the loss. Lower TOPO_PAYMENT_RATE.`);
  if (count('tap_drain_errors') > 0) warnings.push(`⚠️  ${count('tap_drain_errors')} drain calls failed: the arrived count may be short.`);
  if (count('tap_unsealed') > 0) warnings.push(`❌ ${count('tap_unsealed')} tap bodies carried a card that was NOT a compact JWE. The Subject crossed the broker unsealed.`);

  const summary = `
═══════════════════════════════════════════════════════════
          TOPOLOGY REPAIR (EXCHANGE LOSS) TEST SUMMARY
═══════════════════════════════════════════════════════════
HTTP
  POST /products             201: ${created} / ${created + notCreated}   (${pct(created, created + notCreated)})
  POST /payments/authorize   202: ${accepted} / ${accepted + notAccepted}   (${pct(accepted, accepted + notAccepted)})
    success by phase         baseline ${rateText(rate('payment_ok_baseline'))} | disruption ${rateText(rate('payment_ok_disruption'))} (first ${DISRUPTION_S}s after the delete) | recovered ${rateText(rate('payment_ok_recovered'))}
  Payments p95 / p99         ${trend('http_req_duration{scenario:payments}', 'p(95)')} / ${trend('http_req_duration{scenario:payments}', 'p(99)')}
  Products p95 / p99         ${trend('http_req_duration{scenario:products}', 'p(95)')} / ${trend('http_req_duration{scenario:products}', 'p(99)')}

TOPOLOGY REPAIR (both exchanges deleted at t=${DELETE_AT_S}s, no restart)
  ${PRODUCT_EXCHANGE} back after          ${msText(gauge('product_events_repair_ms'))}
  ${PAYMENT_EXCHANGE} back after          ${msText(gauge('payment_events_repair_ms'))}
  ${PAYMENT_EXCHANGE} re-bound after      ${msText(gauge('payment_bindings_repair_ms'))}   (${PAYMENT_QUEUE} + tap)
  Final state                     ${finalOk === undefined ? 'n/a' : finalOk === 1 ? 'all present' : 'MISSING PIECES (see teardown log)'}

END-TO-END DELIVERY (${TAP_QUEUE})
  202 Accepted                    ${accepted}
  Distinct orders on the tap      ${arrived}
  Lost in repair window           ${lostText}
  Duplicates on the tap           ${count('tap_duplicates')}   (a retried publish whose first confirm died with the old channel)
  Foreign bodies skipped          ${count('tap_foreign')}
  Routed to the tap (stats)       ${tapRouted === undefined ? 'n/a' : tapRouted}
  Routed to the queue (stats)     ${queueRouted === undefined ? 'n/a' : queueRouted}   (${PAYMENT_QUEUE})
    message_stats.publish deltas on the ~5s stats tick. They count duplicates and
    any payment published outside this run, and the two should match.

${lossNote}
${warnings.length > 0 ? `\n${warnings.join('\n')}\n` : ''}═══════════════════════════════════════════════════════════
`;

  // No console.log: the returned stdout (summaryOutputs) prints the box once.

  // The loss is computed here, across all VUs, so it is not a k6 metric. Put it
  // in the machine-readable summary too (PERF_SUMMARY_FILE).
  const report = {
    ...data,
    topology_repair: {
      payments_accepted: accepted,
      payments_not_accepted: notAccepted,
      tap_distinct_orders: arrived,
      lost_in_repair_window_min: lostMin,
      lost_in_repair_window_max: lostMax,
      tap_routed_stats: tapRouted === undefined ? null : tapRouted,
      queue_routed_stats: queueRouted === undefined ? null : queueRouted,
    },
  };
  return summaryOutputs(report, summary);
}
