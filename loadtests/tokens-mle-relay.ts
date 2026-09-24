// tokens-mle-relay.ts - Visa MLE (bare-JWE) Relay End-to-End Load Test
//
// Drives POST /api/v1/tokens/mle-relay, the Visa Message Level Encryption twin of
// tokens-relay.ts. k6 sends plaintext; the relay does the crypto:
//
//   k6  --plaintext-->  /tokens/mle-relay
//                       │
//                       ├─ outbound JOSETransport: encrypt only (bare JWE, A128GCM,
//                       │  typ JOSE, millisecond iat), wrapped as {"encData": ...}
//                       │
//                       │   POST /__sim/peer/mle (in-process MLE peer simulator)
//                       │   ├─ jose.Open (bare, manual — no jose: tag selects it)
//                       │   ├─ tokenize (HMAC, no DB)
//                       │   └─ jose.Seal back to us, answered as {"encData": ...}
//                       │
//                       └─ inbound JOSETransport: unwrap encData by shape, decrypt
//                       │
//   k6  <--plaintext--  Token JSON
//
// Two RSA-OAEP unwraps and no signatures per request — compare its latency with
// tokens-relay.ts (nested JWE-of-JWS, which adds two RSA signatures) to see what
// the signature layer costs. The client is named "visa-mle-peer-sim"
// (httpclient WithPeerName), so its outbound httpclient metrics carry that peer
// label for the length of the run.
//
// Usage:
//   k6 run loadtests/tokens-mle-relay.ts
//   k6 run --vus 1 --duration 30s loadtests/tokens-mle-relay.ts
//   K6_BASE_URL=http://localhost:8080 k6 run loadtests/tokens-mle-relay.ts

import { relayScenario } from './tokens-common.ts';

const scenario = relayScenario({
  path: '/tokens/mle-relay',
  endpoint: 'tokens_mle_relay',
  metricPrefix: 'mle_relay',
  title: 'TOKENS MLE RELAY (BARE JWE)',
  banner: [
    'Each request: 2 RSA-OAEP encrypts + 2 decrypts, no signatures',
    'Wire shape: {"encData":"<compact JWE>"} as application/json, both directions',
  ],
});

export const options = scenario.options;
export const setup = scenario.setup;
export const handleSummary = scenario.handleSummary;
export default scenario.run;
