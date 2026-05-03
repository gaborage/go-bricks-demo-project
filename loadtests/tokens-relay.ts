// tokens-relay.ts - JOSE Relay End-to-End Load Test
//
// Drives POST /api/v1/tokens/relay, which exercises the FULL JOSE pipeline
// in a single request without requiring k6 to perform any client-side crypto:
//
//   k6  --plaintext-->  /tokens/relay
//                       │
//                       ├─ outbound JOSETransport: sign + encrypt (tokens-our + tokens-peer)
//                       │
//                       │   POST /__sim/peer/tokens (in-process peer simulator)
//                       │   ├─ inbound JOSE middleware: decrypt + verify
//                       │   ├─ tokenize (HMAC, no DB)
//                       │   └─ outbound JOSE middleware: sign + encrypt (inverse policy)
//                       │
//                       └─ inbound JOSETransport: decrypt + verify, unwrap envelope
//                       │
//   k6  <--plaintext--  Token JSON
//
// That's 4 JOSE operations per request (2 seal + 2 unseal), so this is the
// most JOSE-heavy single call you can drive from a plain HTTP client. Use it
// to benchmark the framework's JOSE pipeline end-to-end.
//
// Note: the tokens module performs no DB I/O — tokenization is HMAC-SHA256.
// Differences against the products-* tests are dominated by RSA-OAEP / RSA-PSS
// / AES-GCM CPU cost, not query latency. Watch the Go runtime panels in the
// Application Overview dashboard for allocator pressure under sustained load.
//
// Usage:
//   k6 run loadtests/tokens-relay.ts
//   k6 run --vus 25 --duration 2m loadtests/tokens-relay.ts
//   K6_BASE_URL=http://prod.example.com:8080 k6 run loadtests/tokens-relay.ts

import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate, Trend, Counter } from 'k6/metrics';
import type { Options } from 'k6/options';
import { config, getURL, headers, loadProfiles } from './config.ts';
import type { RelayResponse } from './types/index.ts';

// Custom metrics — keep namespace consistent with the products-* tests.
const relayTokenSuccess = new Rate('relay_token_success');
const relayTokenDuration = new Trend('relay_token_duration');
const totalRequests = new Counter('total_requests');
// Status-code tracker. Tagged so k6's summary breaks down by code, e.g.
// `relay_status_code{code:429}: 567`. Critical for diagnosing failure modes
// (rate limiter rejections vs JOSE seal errors vs partner timeouts).
const relayStatusCode = new Counter('relay_status_code');
let firstFailureLogged = false;

// Test configuration. Sustained profile by default — JOSE is CPU-bound so the
// 10-minute hold at 50 VUs surfaces GC pressure better than a short ramp.
// Override with --vus/--duration on the CLI for ad-hoc runs.
export const options: Options = {
  stages: loadProfiles.sustained.stages,
  thresholds: {
    // Looser than the products-* baselines because every relay call performs
    // 4 RSA + 2 AES-GCM operations across two HTTP hops. Tune these against
    // your hardware after the first run.
    'http_req_duration': [
      'p(95)<800',
      'p(99)<1500',
    ],
    'http_req_failed': [
      'rate<0.005', // <0.5% — JOSE failures usually indicate a key/policy bug
    ],
    'http_req_duration{endpoint:tokens_relay}': ['p(95)<800'],
    'relay_token_success': ['rate>0.99'],
  },
  // No batching — each relay call is independent and we want the latency
  // distribution to reflect single-request cost, not pipelined throughput.
  discardResponseBodies: false,
};

// Test PANs — all Luhn-valid (the service rejects invalid PANs at
// internal/modules/tokens/service/tokenization_service.go:validPAN). Mixing
// networks exercises the IIN-detection switch but the JOSE cost is identical
// across PANs; this is mostly here so the cardinality of inputs isn't 1.
const TEST_PANS: string[] = [
  '4111111111111111', // Visa
  '4242424242424242', // Visa (Stripe test)
  '5555555555554444', // Mastercard
  '5105105105105100', // Mastercard
  '378282246310005',  // Amex
  '6011111111111117', // Discover
];

function pickPAN(): string {
  return TEST_PANS[Math.floor(Math.random() * TEST_PANS.length)];
}

// Main test function — one request per VU iteration, modest think time so
// the per-VU pacing isn't tail-latency-dominated.
export default function (): void {
  totalRequests.add(1);
  relay();
  // 0.2-0.7s think time. Shorter than the CRUD test because relay is
  // self-contained (no follow-up reads/updates simulating a user session).
  sleep(Math.random() * 0.5 + 0.2);
}

function relay(): void {
  const url = getURL('/tokens/relay');
  const body = JSON.stringify({ pan: pickPAN() });

  const response = http.post(url, body, {
    headers,
    tags: { endpoint: 'tokens_relay', operation: 'jose_roundtrip' },
  });

  // Tag the per-status-code counter. k6 uses 0 for transport-level failures
  // (connection refused, dial timeout) — keeping the bucket helps distinguish
  // server rejections from client-side issues.
  relayStatusCode.add(1, { code: String(response.status) });

  const success = check(response, {
    'relay: status is 200': (r) => r.status === 200,
    'relay: returns a token': (r) => {
      try {
        const parsed = JSON.parse(r.body as string) as RelayResponse;
        // The relay handler emits the standard {data: ...} envelope.
        const token = parsed.data?.token ?? parsed.token;
        return !!(
          token &&
          typeof token.token === 'string' &&
          token.token.startsWith('tok_') &&
          token.masked_pan &&
          token.last4 &&
          token.expires_at
        );
      } catch {
        return false;
      }
    },
  });

  // Log the first failure's status + body once so the operator can see exactly
  // what the server returned. Subsequent failures only bump the counter to
  // keep the test output readable.
  if (!success && !firstFailureLogged) {
    firstFailureLogged = true;
    console.error(
      `first failure: status=${response.status} body=${(response.body as string).slice(0, 300)}`,
    );
  }

  relayTokenSuccess.add(success ? 1 : 0);
  relayTokenDuration.add(response.timings.duration);
}

// Setup runs once before VUs start — verify the API and the JOSE plumbing
// before we burn time on a load run that would have failed on the first call.
export function setup(): void {
  console.log('🚀 Starting Tokens Relay Load Test (JOSE end-to-end)');
  console.log(`📊 Target: ${config.baseURL}${config.apiPrefix}/tokens/relay`);
  console.log('🔐 Each request triggers 4 JOSE ops (2 seal + 2 unseal)');
  console.log('');

  // Health check
  const healthURL = `${config.baseURL}${config.apiPrefix}/health`;
  const health = http.get(healthURL);
  if (health.status !== 200) {
    console.error('❌ Health check failed!');
    console.error(`   URL: ${healthURL}`);
    console.error(`   Status: ${health.status}`);
    throw new Error('API health check failed');
  }
  console.log('✅ Health check passed');

  // Smoke the relay endpoint once. If keystore wiring is broken or the peer
  // simulator route isn't registered, fail fast — don't waste a 12-minute run.
  const probe = http.post(
    getURL('/tokens/relay'),
    JSON.stringify({ pan: TEST_PANS[0] }),
    { headers },
  );
  if (probe.status !== 200) {
    console.error('❌ Relay probe failed — JOSE pipeline is not healthy');
    console.error(`   Status: ${probe.status}`);
    console.error(`   Body:   ${probe.body}`);
    throw new Error('Relay probe failed; aborting load test');
  }
  console.log('✅ Relay probe succeeded — JOSE pipeline healthy');
  console.log('');
}

export function teardown(): void {
  console.log('');
  console.log('✅ Tokens relay load test completed');
  console.log('');
  console.log('💡 Next steps:');
  console.log('   - Compare p95/p99 against products-crud.ts (create) for JOSE overhead');
  console.log('   - Check Go runtime panels for GC pressure (RSA/AES allocate per call)');
  console.log('   - Watch goroutine count — outbound httpclient + inbound handler each spawn work');
}

// Custom summary modeled on products-read-only.ts so output style matches.
export function handleSummary(data: any): { stdout: string } {
  const successRate = data.metrics.relay_token_success?.values?.rate || 0;
  const avgDuration = data.metrics.http_req_duration?.values?.avg || 0;
  const p95Duration = data.metrics.http_req_duration?.values['p(95)'] || 0;
  const p99Duration = data.metrics.http_req_duration?.values['p(99)'] || 0;
  const totalReqs = data.metrics.total_requests?.values?.count || 0;
  const failureRate = data.metrics.http_req_failed?.values?.rate || 0;

  const summary = `
═══════════════════════════════════════════════════════════
              TOKENS RELAY (JOSE) TEST SUMMARY
═══════════════════════════════════════════════════════════
Total Requests:          ${totalReqs}
Relay Success Rate:      ${(successRate * 100).toFixed(2)}%
HTTP Failure Rate:       ${(failureRate * 100).toFixed(2)}%
Avg Response Time:       ${avgDuration.toFixed(2)}ms
P95 Response Time:       ${p95Duration.toFixed(2)}ms
P99 Response Time:       ${p99Duration.toFixed(2)}ms
═══════════════════════════════════════════════════════════
`;

  console.log(summary);

  return {
    stdout: summary,
  };
}
