// tokens-vts-issuer-relay.ts - VTS Issuer (JWS-of-JWE) Relay End-to-End Load Test
//
// Drives POST /api/v1/tokens/vts-issuer-relay, the Visa Token Service Issuer twin
// of tokens-relay.ts. k6 sends plaintext; the relay does the crypto:
//
//   k6  --plaintext-->  /tokens/vts-issuer-relay
//                       │
//                       ├─ outbound JOSETransport: encrypt (inner JWE, A256GCM,
//                       │  typ JOSE, millisecond iat), THEN sign the compact JWE
//                       │  (outer JWS, PS256, cty JWE) — application/jose
//                       │
//                       │   in-process VTS Issuer peer (the client's base
//                       │   transport — no /__sim/ route, no loopback hop)
//                       │   ├─ jose.Open: verify FIRST, then decrypt (manual —
//                       │   │  no jose: tag selects this mode)
//                       │   ├─ tokenize (HMAC, no DB)
//                       │   └─ jose.Seal back to us, answered as application/jose
//                       │
//                       └─ inbound JOSETransport: verify, then decrypt
//                       │
//   k6  <--plaintext--  Token JSON
//
// Two PS256 signatures, two verifications and two RSA-OAEP round trips per
// request — the same RSA operation count as tokens-relay.ts (which signs RS256)
// in the opposite nesting.
// The peer sits in the relay client's transport slot instead of behind a
// loopback HTTP call, so this script carries one HTTP hop fewer than the other
// two tokens scripts; keep that in mind when comparing their latencies. The
// client is named "visa-vts-issuer-peer-sim" (httpclient WithPeerName), so its
// outbound httpclient metrics carry that peer label for the length of the run.
//
// Usage:
//   k6 run loadtests/tokens-vts-issuer-relay.ts
//   k6 run --vus 1 --duration 30s loadtests/tokens-vts-issuer-relay.ts
//   K6_BASE_URL=http://localhost:8080 k6 run loadtests/tokens-vts-issuer-relay.ts

import { relayScenario } from './tokens-common.ts';

const scenario = relayScenario({
  path: '/tokens/vts-issuer-relay',
  endpoint: 'tokens_vts_issuer_relay',
  metricPrefix: 'vts_issuer_relay',
  title: 'TOKENS VTS ISSUER RELAY (JWS-OF-JWE)',
  banner: [
    'Each request: 2 RSA-OAEP encrypts + 2 decrypts, 2 PS256 signs + 2 verifies',
    'Wire shape: compact JWS(JWE) as application/jose, both directions, verify before decrypt',
  ],
});

export const options = scenario.options;
export const setup = scenario.setup;
export const handleSummary = scenario.handleSummary;
export default scenario.run;
