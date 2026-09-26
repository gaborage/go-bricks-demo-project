// tokens-common.ts - shared fixtures and scenario factory for the tokens relays
//
// Every tokens relay endpoint takes the same plaintext {"pan": ...} body and
// answers the same {data: {token: ...}} envelope; only the JOSE wire shape
// between the relay and its in-process peer simulator differs. This module holds
// what they share so each relay script is a thin, declarative spec:
//
//   - TEST_PANS / pickPAN: the published network test numbers (no real card data)
//   - hasTokenShape: the response-shape assertion every relay makes
//   - errorCode: the ONLY part of a failed response a script may print
//   - relayScenario: options + default + setup + handleSummary for one relay
//
// Logging rule: request bodies carry a PAN, so no script built on this module
// prints a request or response body. Failures report the HTTP status plus the
// envelope's error.code (or k6's transport error) and nothing else.

import http from 'k6/http';
import type { RefinedResponse, ResponseType } from 'k6/http';
import { check } from 'k6';
import { Rate, Trend, Counter } from 'k6/metrics';
import type { Options } from 'k6/options';
import { config, getURL, headers, loadProfiles, maybeSleep, resolveScenario, summaryOutputs } from './config.ts';
import type { RelayResponse } from './types/index.ts';

// Test PANs — all Luhn-valid (the service rejects invalid PANs at
// internal/modules/tokens/service/tokenization_service.go:validPAN). Mixing
// networks exercises the IIN-detection switch but the JOSE cost is identical
// across PANs; this is mostly here so the cardinality of inputs isn't 1.
export const TEST_PANS: string[] = [
  '4111111111111111', // Visa
  '4242424242424242', // Visa (Stripe test)
  '5555555555554444', // Mastercard
  '5105105105105100', // Mastercard
  '378282246310005',  // Amex
  '6011111111111117', // Discover
];

export function pickPAN(): string {
  return TEST_PANS[Math.floor(Math.random() * TEST_PANS.length)];
}

/**
 * Asserts the relay's response shape for the PAN that was sent: a tok_ token, a
 * masked PAN that is asterisks plus the last four digits (so the full PAN never
 * comes back), and last4 matching the input.
 */
export function hasTokenShape(body: string | null, pan: string): boolean {
  if (!body) return false;
  try {
    const token = (JSON.parse(body) as RelayResponse).data?.token;
    return !!(
      token &&
      typeof token.token === 'string' &&
      token.token.startsWith('tok_') &&
      token.last4 === pan.slice(-4) &&
      typeof token.masked_pan === 'string' &&
      token.masked_pan.length === pan.length &&
      /^\*+\d{4}$/.test(token.masked_pan) &&
      token.masked_pan.endsWith(token.last4) &&
      typeof token.network === 'string' &&
      token.network.length > 0 &&
      !!token.expires_at
    );
  } catch {
    return false;
  }
}

/**
 * Names a failed response without printing its body: the envelope's error.code
 * when there is one, k6's transport error when the request never got an answer.
 */
export function errorCode(response: RefinedResponse<ResponseType | undefined>): string {
  if (response.status === 0) {
    return response.error || 'transport error';
  }
  try {
    const parsed = JSON.parse(response.body as string) as { error?: { code?: string } };
    return parsed.error?.code || 'no error code';
  } catch {
    return 'non-JSON body';
  }
}

/** What distinguishes one relay endpoint from another. */
export interface RelaySpec {
  /** Route under the API prefix, e.g. '/tokens/mle-relay'. */
  path: string;
  /** k6 `endpoint` tag value; also scopes the per-endpoint latency threshold. */
  endpoint: string;
  /** Prefix for this script's custom metrics (letters, digits, underscore). */
  metricPrefix: string;
  /** Title printed in the summary box. */
  title: string;
  /** One line per JOSE step, printed by setup(). */
  banner: string[];
}

/** The four exports a k6 script needs, built from a RelaySpec. */
export interface RelayScenario {
  options: Options;
  run: () => void;
  setup: () => void;
  handleSummary: (data: any) => Record<string, string>;
}

/**
 * Builds a relay load test. Must be called from a script's init context (top
 * level), because k6 only allows custom metrics to be created there.
 */
export function relayScenario(spec: RelaySpec): RelayScenario {
  const success = new Rate(`${spec.metricPrefix}_token_success`);
  const duration = new Trend(`${spec.metricPrefix}_token_duration`);
  // Tagged so k6's summary breaks down by code, e.g. `{code:429}` for rate
  // limiter rejections vs `{code:500}` for a JOSE or partner failure.
  const statusCode = new Counter(`${spec.metricPrefix}_status_code`);
  const totalRequests = new Counter('total_requests');
  let firstFailureLogged = false;

  // Sustained VU stages by default — JOSE is CPU-bound, so the 10-minute hold at
  // 50 VUs surfaces GC pressure. PERF_RATE switches to a controlled
  // constant-arrival-rate scenario (see resolveScenario in config.ts).
  const steady = resolveScenario();
  const options: Options = {
    ...(steady ? { scenarios: { steady } } : { stages: loadProfiles.sustained.stages }),
    thresholds: {
      // One budget for every relay: each call is 2 seal + 2 open. Tune against
      // your hardware after the first run.
      'http_req_duration': ['p(95)<800', 'p(99)<1500'],
      'http_req_failed': ['rate<0.005'],
      [`http_req_duration{endpoint:${spec.endpoint}}`]: ['p(95)<800'],
      [`${spec.metricPrefix}_token_success`]: ['rate>0.99'],
    },
    // p(99) is not a default summary stat; the summary box below prints it.
    summaryTrendStats: ['avg', 'min', 'med', 'max', 'p(90)', 'p(95)', 'p(99)'],
    discardResponseBodies: false,
  };

  function run(): void {
    totalRequests.add(1);
    const pan = pickPAN();
    const response = http.post(getURL(spec.path), JSON.stringify({ pan }), {
      headers,
      tags: { endpoint: spec.endpoint, operation: 'jose_roundtrip' },
    });
    statusCode.add(1, { code: String(response.status) });

    const ok = check(response, {
      [`${spec.endpoint}: status is 200`]: (r) => r.status === 200,
      [`${spec.endpoint}: returns a token for the PAN sent`]: (r) => hasTokenShape(r.body as string, pan),
    });

    if (!ok && !firstFailureLogged) {
      firstFailureLogged = true;
      console.error(`first failure: status=${response.status} code=${errorCode(response)}`);
    }

    success.add(ok ? 1 : 0);
    duration.add(response.timings.duration);
    // Suppressed in controlled mode so the arrival rate drives the offered load.
    maybeSleep(Math.random() * 0.5 + 0.2, steady !== null);
  }

  // Fail fast: a broken keystore or an unregistered simulator would otherwise
  // burn a 12-minute run on errors.
  function setup(): void {
    console.log(`🚀 Starting ${spec.title} load test`);
    console.log(`📊 Target: ${config.baseURL}${config.apiPrefix}${spec.path}`);
    spec.banner.forEach((line) => console.log(`🔐 ${line}`));

    const health = http.get(`${config.baseURL}${config.apiPrefix}/health`);
    if (health.status !== 200) {
      throw new Error(`API health check failed (status ${health.status})`);
    }

    const pan = TEST_PANS[0];
    const probe = http.post(getURL(spec.path), JSON.stringify({ pan }), { headers });
    if (probe.status !== 200 || !hasTokenShape(probe.body as string, pan)) {
      throw new Error(`${spec.path} probe failed: status=${probe.status} code=${errorCode(probe)}`);
    }
    console.log('✅ Probe succeeded — JOSE pipeline healthy');
  }

  function handleSummary(data: any): Record<string, string> {
    const rate = data.metrics[`${spec.metricPrefix}_token_success`]?.values?.rate || 0;
    const httpDuration = data.metrics.http_req_duration?.values || {};
    const summary = `
═══════════════════════════════════════════════════════════
  ${spec.title} — TEST SUMMARY
═══════════════════════════════════════════════════════════
Total Requests:          ${data.metrics.total_requests?.values?.count || 0}
Token Success Rate:      ${(rate * 100).toFixed(2)}%
HTTP Failure Rate:       ${((data.metrics.http_req_failed?.values?.rate || 0) * 100).toFixed(2)}%
Avg Response Time:       ${(httpDuration.avg || 0).toFixed(2)}ms
P95 Response Time:       ${(httpDuration['p(95)'] || 0).toFixed(2)}ms
P99 Response Time:       ${(httpDuration['p(99)'] || 0).toFixed(2)}ms
═══════════════════════════════════════════════════════════
`;
    return summaryOutputs(data, summary);
  }

  return { options, run, setup, handleSummary };
}
