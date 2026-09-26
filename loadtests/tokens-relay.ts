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
// Built on relayScenario (tokens-common.ts), like the MLE and VTS Issuer
// scripts: the same fixtures, shape check, thresholds, PERF_* knobs and logging
// rule (a failure prints its status and error.code, never a body). The metric
// prefix stays `relay`, so relay_token_success, relay_token_duration and
// relay_status_code keep their names.
//
// Usage:
//   k6 run loadtests/tokens-relay.ts
//   k6 run --vus 25 --duration 2m loadtests/tokens-relay.ts
//   PERF_RATE=50 PERF_DURATION=60s k6 run loadtests/tokens-relay.ts
//   K6_BASE_URL=http://prod.example.com:8080 k6 run loadtests/tokens-relay.ts

import { relayScenario } from './tokens-common.ts';

const scenario = relayScenario({
  path: '/tokens/relay',
  endpoint: 'tokens_relay',
  metricPrefix: 'relay',
  title: 'TOKENS RELAY (NESTED JWE-OF-JWS)',
  banner: [
    'Each request triggers 4 JOSE ops (2 seal + 2 unseal) across two HTTP hops',
    'Wire shape: compact JWE(JWS) as application/jose, both directions',
  ],
});

export const options = scenario.options;
export const setup = scenario.setup;
export const handleSummary = scenario.handleSummary;
export default scenario.run;

export function teardown(): void {
  console.log('');
  console.log('✅ Tokens relay load test completed');
  console.log('');
  console.log('💡 Next steps:');
  console.log('   - Compare p95/p99 against products-crud.ts (create) for JOSE overhead');
  console.log('   - Check Go runtime panels for GC pressure (RSA/AES allocate per call)');
  console.log('   - Watch goroutine count — outbound httpclient + inbound handler each spawn work');
}
