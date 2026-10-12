// k6 Load Testing Configuration
// This file contains shared configuration for all load tests

import { sleep } from 'k6';
import crypto from 'k6/crypto';
import http from 'k6/http';
import type { RefinedParams, RefinedResponse, RequestBody, ResponseType } from 'k6/http';
import { Counter } from 'k6/metrics';
import type { TestConfig, LoadProfile, SampleProduct, Headers } from './types/index.ts';
import type { Options, Scenario, Threshold } from 'k6/options';

export const config: TestConfig = {
  // Base URL for API - override with K6_BASE_URL environment variable
  baseURL: __ENV.K6_BASE_URL || 'http://localhost:8080',

  // API path prefix, the app's server.path.base - override with K6_API_PREFIX
  // (leading slash, no trailing slash). `??`, not `||`: an explicitly empty
  // K6_API_PREFIX= means the API is served at the URL root, with no prefix.
  // Every API URL is built from it, through getURL() below.
  apiPrefix: __ENV.K6_API_PREFIX ?? '/api/v1',

  // Performance thresholds - these define what "passing" means
  // Adjust these based on your SLAs and performance requirements
  thresholds: {
    // HTTP request duration thresholds
    'http_req_duration': [
      'p(95)<500',   // 95% of requests should be below 500ms
      'p(99)<1000',  // 99% of requests should be below 1s
    ],
    // HTTP request failure rate threshold
    'http_req_failed': [
      'rate<0.01',   // Error rate should be less than 1%
    ],
    // Specific endpoint thresholds (can be customized per test)
    'http_req_duration{endpoint:list_products}': ['p(95)<400'],
    'http_req_duration{endpoint:get_product}': ['p(95)<300'],
    'http_req_duration{endpoint:create_product}': ['p(95)<600'],
    'http_req_duration{endpoint:update_product}': ['p(95)<600'],
    'http_req_duration{endpoint:delete_product}': ['p(95)<400'],
  },

  // Realistic CRUD operation weights (percentage)
  // These simulate typical production usage patterns
  operationWeights: {
    list: 50,      // 50% - List/search operations (most common)
    get: 25,       // 25% - Get by ID operations
    create: 15,    // 15% - Create new products
    update: 7,     // 7% - Update existing products
    delete: 3,     // 3% - Delete operations (least common)
  },

  // Test data configuration
  testData: {
    // Number of pages to randomly access during list operations
    maxPages: 10,
    pageSize: 10,

    // Sample product data for creates
    sampleProducts: [
      { name: 'Test Product', description: 'Load test product', price: 99.99 },
      { name: 'Demo Item', description: 'Benchmark test item', price: 149.99 },
      { name: 'Sample Product', description: 'Performance test product', price: 79.99 },
      { name: 'Load Test Widget', description: 'Widget for testing', price: 29.99 },
      { name: 'Benchmark Gadget', description: 'Gadget for benchmarking', price: 199.99 },
    ],
  },
};

/**
 * Helper function to construct full URL from path
 */
export function getURL(path: string): string {
  return `${config.baseURL}${config.apiPrefix}${path}`;
}

/**
 * Helper function to get random test product
 */
export function getRandomProduct(): SampleProduct {
  const products = config.testData.sampleProducts;
  return products[Math.floor(Math.random() * products.length)];
}

/**
 * Helper function to get random page number
 */
export function getRandomPage(): number {
  return Math.floor(Math.random() * config.testData.maxPages) + 1;
}

/**
 * Helper function to get a random seeded product ID
 * Returns one of the 6 UUIDs from the initial database seed data (V1 migration)
 * Use this as a fallback when no products have been created yet during load tests
 */
export function getSeededProductID(): string {
  const seededIDs: string[] = [
    '550e8400-e29b-41d4-a716-446655440001',
    '550e8400-e29b-41d4-a716-446655440002',
    '550e8400-e29b-41d4-a716-446655440003',
    '550e8400-e29b-41d4-a716-446655440004',
    '550e8400-e29b-41d4-a716-446655440005',
    '550e8400-e29b-41d4-a716-446655440006',
  ];
  return seededIDs[Math.floor(Math.random() * seededIDs.length)];
}

// Common request headers
export const headers: Headers = {
  'Content-Type': 'application/json',
  'Accept': 'application/json',
};

// Load profile configurations for different test scenarios
export const loadProfiles: Record<string, LoadProfile> = {
  // Smoke test - minimal load to verify everything works
  smoke: {
    stages: [
      { duration: '30s', target: 1 },   // 1 user for 30s
    ],
  },

  // Ramp-up test - gradually increase load to find limits
  rampUp: {
    stages: [
      { duration: '2m', target: 10 },   // Ramp up to 10 users over 2 min
      { duration: '2m', target: 25 },   // Ramp up to 25 users
      { duration: '2m', target: 50 },   // Ramp up to 50 users
      { duration: '2m', target: 100 },  // Ramp up to 100 users
      { duration: '5m', target: 100 },  // Stay at 100 users for 5 min
      { duration: '2m', target: 0 },    // Ramp down to 0
    ],
  },

  // Spike test - sudden burst of traffic
  spike: {
    stages: [
      { duration: '1m', target: 10 },   // Baseline load
      { duration: '10s', target: 200 }, // Spike to 200 users
      { duration: '1m', target: 200 },  // Hold spike
      { duration: '10s', target: 10 },  // Drop back
      { duration: '1m', target: 10 },   // Recovery period
    ],
  },

  // Sustained load test - constant load over time
  sustained: {
    stages: [
      { duration: '1m', target: 50 },   // Ramp up to 50 users
      { duration: '10m', target: 50 },  // Hold at 50 users for 10 min
      { duration: '1m', target: 0 },    // Ramp down
    ],
  },

  // Stress test - push beyond normal limits
  stress: {
    stages: [
      { duration: '2m', target: 50 },   // Warm up
      { duration: '5m', target: 100 },  // Approaching limits
      { duration: '5m', target: 200 },  // Beyond limits
      { duration: '5m', target: 300 },  // Much beyond limits
      { duration: '2m', target: 0 },    // Recovery
    ],
  },
};

/**
 * Type for check functions
 */
export type CheckFunction = (r: any) => boolean;

/**
 * Common check functions
 */
export function createChecks(): Record<string, CheckFunction> {
  return {
    'status is 200': (r) => r.status === 200,
    'status is 201': (r) => r.status === 201,
    'status is 204': (r) => r.status === 204,
    'response time < 1s': (r) => r.timings.duration < 1000,
    'response has body': (r) => r.body && r.body.length > 0,
  };
}

// ============================================================================
// Controlled-load hardening (non-breaking, opt-in via environment variables)
// ============================================================================
// Default `make loadtest-*` behavior is UNCHANGED. When PERF_RATE is set, steady
// tests switch to an open-model constant-arrival-rate scenario — a controlled,
// repeatable, device-safe offered load that is ideal for version-to-version A/B
// comparison (hold a fixed rps comfortably below the measured saturation
// ceiling so the signal reflects the framework, not queue collapse). Knobs use
// a PERF_ prefix to avoid colliding with k6's reserved K6_VUS/K6_DURATION/etc.
// option env-vars (which would override script `scenarios`):
//   PERF_RATE      offered requests/sec (0 or unset -> keep native VU stages)
//   PERF_DURATION  steady-state duration (default 60s)
//   PERF_STEPS     "rate:duration,..." staircase instead of PERF_RATE, e.g.
//                  "50:3m,100:3m,150:3m": one ramping-arrival-rate scenario that
//                  holds each rate for its duration, with a 15s linear ramp
//                  between steps. Needs PERF_RATE unset or 0.
//   PERF_PREALLOC  preallocated VUs reused for keep-alive connections (def 300)
//   PERF_MAXVUS    hard VU ceiling (default = PERF_PREALLOC)
// The perf-comparison knobs (abort guards, run id, seed ids, summary markers)
// are documented with their helpers further down.

/** Parse a positive-integer env value; fallback on unset/zero/malformed input (NaN-safe). */
function positiveIntOr(value: string | undefined, fallback: number): number {
  const n = parseInt(value || '', 10);
  return Number.isFinite(n) && n > 0 ? n : fallback;
}

/** One PERF_STEPS step: an offered rate (iterations/s) held for a k6 duration. */
export interface PerfStep {
  rate: number;
  duration: string;
}

/**
 * Linear ramp between two PERF_STEPS steps; part of the staircase's length.
 * Reported as perfMeta.stepRampSeconds, so a reader can time the steps.
 */
export const PERF_STEP_RAMP_SECONDS = 15;
const PERF_STEP_RAMP = `${PERF_STEP_RAMP_SECONDS}s`;

/** A k6 duration such as "30s", "3m" or "1m30s", and not all zeros. */
function isK6Duration(value: string): boolean {
  return /^(\d+(ms|s|m|h))+$/.test(value) && /[1-9]/.test(value);
}

/**
 * PERF_STEPS parsed into steps, or null when unset. A malformed value throws at
 * init: a perf run must not silently fall back to another load shape.
 */
export function perfSteps(): PerfStep[] | null {
  const raw = (__ENV.PERF_STEPS || '').trim();
  if (raw === '') return null;
  return raw.split(',').map((part) => {
    const m = /^\s*([1-9]\d*)\s*:\s*(\S+)\s*$/.exec(part);
    if (!m || !isK6Duration(m[2])) {
      throw new Error(`PERF_STEPS: "${part}" is not <rate>:<duration> (for example 50:3m)`);
    }
    return { rate: parseInt(m[1], 10), duration: m[2] };
  });
}

// The PERF_STEPS value behind each staircase scenario, so perfMeta reports
// steps only for a run that really executes one.
const staircaseSteps = new WeakMap<object, string>();

/** The PERF_STEPS staircase as one ramping-arrival-rate scenario. */
function staircaseScenario(steps: PerfStep[]): Scenario {
  const prealloc = positiveIntOr(__ENV.PERF_PREALLOC, 300);
  const stages: { target: number; duration: string }[] = [];
  steps.forEach((step, i) => {
    if (i > 0) stages.push({ target: step.rate, duration: PERF_STEP_RAMP });
    stages.push({ target: step.rate, duration: step.duration });
  });
  const scenario = {
    executor: 'ramping-arrival-rate',
    startRate: steps[0].rate,
    timeUnit: '1s',
    preAllocatedVUs: prealloc,
    maxVUs: positiveIntOr(__ENV.PERF_MAXVUS, prealloc),
    stages,
    gracefulStop: '5s',
  } as Scenario;
  staircaseSteps.set(scenario, steps.map((step) => `${step.rate}:${step.duration}`).join(','));
  return scenario;
}

/**
 * The controlled scenario a steady test runs: the PERF_STEPS staircase, else a
 * constant-arrival-rate scenario when PERF_RATE is set, else null (native VU
 * stages).
 */
export function resolveScenario(): Scenario | null {
  const steps = perfSteps();
  const rate = positiveIntOr(__ENV.PERF_RATE, 0);
  if (steps) {
    if (rate > 0) {
      throw new Error('PERF_RATE and PERF_STEPS are exclusive: set PERF_RATE=0 or unset it to run the staircase');
    }
    return staircaseScenario(steps);
  }
  if (rate <= 0) return null;
  const prealloc = positiveIntOr(__ENV.PERF_PREALLOC, 300);
  return {
    executor: 'constant-arrival-rate',
    rate,
    timeUnit: '1s',
    duration: __ENV.PERF_DURATION || '60s',
    preAllocatedVUs: prealloc,
    maxVUs: positiveIntOr(__ENV.PERF_MAXVUS, prealloc),
    gracefulStop: '5s',
  } as Scenario;
}

// Spike variant: a controlled ramping-arrival-rate burst (baseline -> peak ->
// baseline) when PERF_SPIKE_PEAK or PERF_RATE is set; else the native VU stages.
// Keeps the spike SHAPE while bounding the offered load for device safety.
//   PERF_SPIKE_PEAK / PERF_RATE   peak rps of the burst
//   PERF_SPIKE_BASE               baseline rps (default 300)
//   PERF_SPIKE_BASE_DUR / _RAMP_DUR / _HOLD_DUR / _RECOVERY_DUR (durations)
export function resolveSpikeScenario(): Scenario | null {
  // NOTE: an explicit PERF_SPIKE_PEAK=0 (truthy string "0") deliberately
  // short-circuits the OR-chain and overrides PERF_RATE -> native spike mode.
  const peak = positiveIntOr(__ENV.PERF_SPIKE_PEAK || __ENV.PERF_RATE, 0);
  if (peak <= 0) return null;
  const base = positiveIntOr(__ENV.PERF_SPIKE_BASE, 300);
  const prealloc = positiveIntOr(__ENV.PERF_PREALLOC, 600);
  return {
    executor: 'ramping-arrival-rate',
    startRate: base,
    timeUnit: '1s',
    preAllocatedVUs: prealloc,
    maxVUs: parseInt(__ENV.PERF_MAXVUS || String(prealloc), 10),
    stages: [
      { target: base, duration: __ENV.PERF_SPIKE_BASE_DUR || '20s' },
      { target: peak, duration: __ENV.PERF_SPIKE_RAMP_DUR || '5s' },
      { target: peak, duration: __ENV.PERF_SPIKE_HOLD_DUR || '20s' },
      { target: base, duration: '5s' },
      { target: base, duration: __ENV.PERF_SPIKE_RECOVERY_DUR || '15s' },
    ],
    gracefulStop: '5s',
  } as Scenario;
}

/** Parse a k6-style duration ("20s"/"2m") to seconds; fallback on bad input. */
function durationToSeconds(value: string | undefined, fallback: number): number {
  if (!value) return fallback;
  const m = /^(\d+)(s|m)?$/.exec(value.trim());
  if (!m) return fallback;
  return parseInt(m[1], 10) * (m[2] === 'm' ? 60 : 1);
}

// Cumulative phase boundaries (seconds) for the spike test's per-phase error
// metrics. Tracks the native VU stages by default, or the controlled spike
// stages when PERF_SPIKE_PEAK/PERF_RATE is set, so baseline/spike/recovery labels
// stay correct in BOTH modes.
export function spikePhaseBoundaries(): { baselineEnd: number; rampEnd: number; holdEnd: number; dropEnd: number } {
  // Derived from resolveSpikeScenario() so the two can never disagree on what
  // counts as "controlled" (incl. the explicit PERF_SPIKE_PEAK=0 override).
  const controlled = resolveSpikeScenario() !== null;
  if (!controlled) {
    return { baselineEnd: 120, rampEnd: 150, holdEnd: 210, dropEnd: 240 };
  }
  const base = durationToSeconds(__ENV.PERF_SPIKE_BASE_DUR, 20);
  const ramp = durationToSeconds(__ENV.PERF_SPIKE_RAMP_DUR, 5);
  const hold = durationToSeconds(__ENV.PERF_SPIKE_HOLD_DUR, 20);
  const drop = 5;
  return {
    baselineEnd: base,
    rampEnd: base + ramp,
    holdEnd: base + ramp + hold,
    dropEnd: base + ramp + hold + drop,
  };
}

// In controlled A/B mode the offered load is driven open-loop by the
// arrival-rate scenario, so per-iteration think-time is suppressed — otherwise
// think-time would make the scenario VU-bound and the offered rate
// unreachable. Each script passes `controlled` from ITS OWN resolved scenario
// (`myScenario !== null`) rather than a shared global check, so e.g.
// PERF_SPIKE_PEAK set alone never mutes think-time in the steady tests, and an
// explicit PERF_SPIKE_PEAK=0 keeps a native spike run's pacing fully native.
// In default mode the native think-time is preserved (realistic closed-loop
// user simulation), so `make loadtest-*` is unchanged.
export function maybeSleep(seconds: number, controlled: boolean): void {
  if (controlled) return;
  sleep(seconds);
}

// ============================================================================
// Perf-comparison knobs (opt-in; with none set every script runs as before)
// ============================================================================
// For comparing framework versions under an open-model load, e.g. from a
// Kubernetes Job whose only output channel is its log:
//   PERF_READ_MIX         get | list | mixed: products-read-only.ts operation mix
//                         (mixed is the default 70% list / 30% get)
//   PERF_SEED_ROWS        N: gets pick a uniform random row of the deterministic
//                         seed (scripts/perf-reset-products.sh) instead of
//                         discovered or fixture ids. Writes never touch seed rows.
//   PERF_ABORT_ON_429     1: abort the run on the first 429 (rate limiter)
//   PERF_ABORT_ERROR_RATE fraction: abort once http_req_failed reaches it
//   PERF_ABORT_P95_MS     ms: abort once http_req_duration p(95) reaches it
//                         All three use abortOnFail with delayAbortEval 60s.
//   PERF_RUN_ID           id: tags every request and metric run_id=<id>
//   PERF_HARNESS_TAG      recorded in perfMeta only (e.g. the scripts' git tag)
//   PERF_SUMMARY_STDOUT   1: print the summary JSON (with perfMeta) to stdout
//                         between ---PERF-SUMMARY-BEGIN--- and
//                         ---PERF-SUMMARY-END---, after the text summary
// With PERF_RATE or PERF_STEPS set, dropped_iterations and http_reqs{status:429}
// carry a count<1 threshold, so both always appear in the summary data (k6 omits
// a metric that recorded nothing) and either one fails the run's thresholds.
// In that mode the perf_http_5xx counter also counts every response with a
// status of 500 or above, made through perfHttp below, and carries a
// count>=0 threshold so it appears even at 0. A runner judges server errors
// from it: http_req_failed also counts the 404s products-crud.ts accepts.

/** A boolean knob: unset, 0 or false is off; 1 or true is on; anything else throws. */
function perfFlag(name: string): boolean {
  const raw = (__ENV[name] || '').trim().toLowerCase();
  if (raw === '' || raw === '0' || raw === 'false') return false;
  if (raw === '1' || raw === 'true') return true;
  throw new Error(`${name} must be 1 or 0, got "${__ENV[name]}"`);
}

/** A numeric knob, or null when unset; a value outside `valid` throws. */
function perfNumber(name: string, valid: (n: number) => boolean, expected: string): number | null {
  const raw = (__ENV[name] || '').trim();
  if (raw === '') return null;
  const n = Number(raw);
  if (!Number.isFinite(n) || !valid(n)) {
    throw new Error(`${name} must be ${expected}, got "${raw}"`);
  }
  return n;
}

/** PERF_SEED_ROWS, or null when unset. */
export function perfSeedRows(): number | null {
  return perfNumber('PERF_SEED_ROWS', (n) => Number.isInteger(n) && n > 0, 'a positive integer');
}

/**
 * The id of seed row g: md5('perf-seed-' || g)::uuid, exactly as
 * scripts/perf-reset-products.sh writes it.
 */
export function seedProductID(g: number): string {
  const h = crypto.md5(`perf-seed-${g}`, 'hex');
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`;
}

/** The id of a seed row picked uniformly from 1..rows. */
export function randomSeedProductID(rows: number): string {
  return seedProductID(1 + Math.floor(Math.random() * rows));
}

/** products-read-only.ts operation mix. */
export type ReadMix = 'get' | 'list' | 'mixed';

/** PERF_READ_MIX, or null when unset (the script's default mix). */
export function resolveReadMix(): ReadMix | null {
  const raw = (__ENV.PERF_READ_MIX || '').trim();
  if (raw === '') return null;
  if (raw === 'get' || raw === 'list' || raw === 'mixed') return raw;
  throw new Error(`PERF_READ_MIX must be get, list or mixed, got "${raw}"`);
}

/** PERF_ABORT_ERROR_RATE, or null when unset. */
function perfAbortErrorRate(): number | null {
  return perfNumber('PERF_ABORT_ERROR_RATE', (n) => n > 0 && n <= 1, 'a fraction in (0, 1]');
}

/** PERF_ABORT_P95_MS, or null when unset. */
function perfAbortP95Ms(): number | null {
  return perfNumber('PERF_ABORT_P95_MS', (n) => n > 0, 'a positive number of milliseconds');
}

/** Abort guard: fail fast, but only judge after the first minute. */
function abortThreshold(expression: string): Threshold {
  return { threshold: expression, abortOnFail: true, delayAbortEval: '60s' };
}

/** The thresholds the perf knobs add, keyed by metric; empty when none is set. */
function perfThresholds(): Record<string, Threshold[]> {
  const extra: Record<string, Threshold[]> = {};
  const controlled = positiveIntOr(__ENV.PERF_RATE, 0) > 0 || perfSteps() !== null;
  if (controlled) {
    extra['dropped_iterations'] = ['count<1'];
    // Never fails: it only keeps the counter in the summary at 0.
    extra[PERF_HTTP_5XX] = ['count>=0'];
  }
  if (perfFlag('PERF_ABORT_ON_429')) {
    extra['http_reqs{status:429}'] = [abortThreshold('count<1')];
  } else if (controlled) {
    extra['http_reqs{status:429}'] = ['count<1'];
  }
  const errorRate = perfAbortErrorRate();
  if (errorRate !== null) {
    extra['http_req_failed'] = [abortThreshold(`rate<${errorRate}`)];
  }
  const p95Ms = perfAbortP95Ms();
  if (p95Ms !== null) {
    extra['http_req_duration'] = [abortThreshold(`p(95)<${p95Ms}`)];
  }
  return extra;
}

/** Responses with a status of 500 or above, counted under PERF_RATE or PERF_STEPS. */
export const PERF_HTTP_5XX = 'perf_http_5xx';

// Registered only under a controlled load (PERF_RATE or PERF_STEPS set, the
// same condition that adds its threshold), so a default run records no new
// metric. Created here, at module scope, because k6 only allows custom
// metrics in the init context.
const serverErrors: Counter | null =
  positiveIntOr(__ENV.PERF_RATE, 0) > 0 || (__ENV.PERF_STEPS || '').trim() !== ''
    ? new Counter(PERF_HTTP_5XX)
    : null;

function countServerError<RT extends ResponseType | undefined>(response: RefinedResponse<RT>): RefinedResponse<RT> {
  if (serverErrors !== null && response.status >= 500) {
    serverErrors.add(1);
  }
  return response;
}

/**
 * k6/http's get, post, put and del, plus expectedStatuses, with every response
 * counted into perf_http_5xx when that counter is registered. Otherwise each
 * call is exactly the k6/http call. The perf-aware scripts import it as
 * `http`, so no request site changes.
 */
export const perfHttp = {
  get<RT extends ResponseType | undefined>(
    url: Parameters<typeof http.get>[0],
    params?: RefinedParams<RT> | null,
  ): RefinedResponse<RT> {
    return countServerError(http.get(url, params));
  },
  post<RT extends ResponseType | undefined>(
    url: Parameters<typeof http.post>[0],
    body?: RequestBody | null,
    params?: RefinedParams<RT> | null,
  ): RefinedResponse<RT> {
    return countServerError(http.post(url, body, params));
  },
  put<RT extends ResponseType | undefined>(
    url: Parameters<typeof http.put>[0],
    body?: RequestBody | null,
    params?: RefinedParams<RT> | null,
  ): RefinedResponse<RT> {
    return countServerError(http.put(url, body, params));
  },
  del<RT extends ResponseType | undefined>(
    url: Parameters<typeof http.del>[0],
    body?: RequestBody | null,
    params?: RefinedParams<RT> | null,
  ): RefinedResponse<RT> {
    return countServerError(http.del(url, body, params));
  },
  expectedStatuses: http.expectedStatuses,
};

/**
 * A script's options plus whatever the perf knobs add: their thresholds,
 * appended after the script's own, and the run_id tag. With no knob set it
 * returns `options` itself, so `k6 inspect` output is unchanged.
 */
export function withPerfKnobs(options: Options): Options {
  // Validated here, at init, rather than first read in handleSummary.
  perfFlag('PERF_SUMMARY_STDOUT');
  const extra = perfThresholds();
  const names = Object.keys(extra);
  const runID = (__ENV.PERF_RUN_ID || '').trim();
  if (names.length === 0 && runID === '') return options;

  const thresholds: Record<string, Threshold[]> = { ...(options.thresholds || {}) };
  names.forEach((name) => {
    thresholds[name] = (thresholds[name] || []).concat(extra[name]);
  });
  return {
    ...options,
    ...(names.length > 0 ? { thresholds } : {}),
    ...(runID !== '' ? { tags: { ...(options.tags || {}), run_id: runID } } : {}),
  };
}

/** What setup() returns so the summary can stamp the run's start. */
export interface PerfSetupData {
  perfStartedAt: string;
}

/** setup()'s return value: the run's start, read back from handleSummary's setup_data. */
export function perfSetupData(): PerfSetupData {
  return { perfStartedAt: new Date().toISOString() };
}

/** What a script tells summaryOutputs about its run, for perfMeta. */
export interface PerfSummaryContext {
  /** The script's file name, e.g. 'products-read-only.ts'. */
  script: string;
  /** True when the script's options went through withPerfKnobs, so its abort guards apply. */
  perfKnobs?: boolean;
  /**
   * The scenario a perf knob selected (PERF_RATE, PERF_STEPS, the controlled
   * spike), or null when the run uses the script's own load shape.
   */
  scenario?: Scenario | null;
  /** The script's PERF_READ_MIX, when it reads one. */
  readMix?: ReadMix | null;
  /** The script's PERF_SEED_ROWS, when it reads one. */
  seedRows?: number | null;
}

/**
 * Run identity and the load the run actually executed, added to the summary
 * JSON as `perfMeta`. Load-shape fields come from the selected scenario and
 * abort fields from the guards the script applied, not from the environment
 * alone, so a knob the script ignores reads as null (or false).
 */
export interface PerfMeta {
  runId: string | null;
  script: string | null;
  /**
   * The selected scenario's executor: constant-arrival-rate (PERF_RATE),
   * ramping-arrival-rate (PERF_STEPS, or the controlled spike), or null for the
   * script's own load shape.
   */
  executor: string | null;
  readMix: ReadMix | null;
  /** The constant arrival rate, or null for any other load shape. */
  rate: number | null;
  /** The staircase as rate:duration steps, or null when none runs. */
  steps: string | null;
  /** The linear ramp between two staircase steps, in seconds, or null when no staircase runs. */
  stepRampSeconds: number | null;
  seedRows: number | null;
  /** null without a selected scenario: native VU mode has no arrival-rate VU pool. */
  preAllocatedVUs: number | null;
  maxVUs: number | null;
  baseUrl: string;
  apiPrefix: string;
  /** The abort guards the script applied (false/null for a script without withPerfKnobs). */
  abortOn429: boolean;
  abortErrorRate: number | null;
  abortP95Ms: number | null;
  harnessTag: string | null;
  /** From setup(); null for a script whose setup does not return perfSetupData(). */
  startedAt: string | null;
  endedAt: string;
}

function perfMeta(data: unknown, ctx: PerfSummaryContext | undefined): PerfMeta {
  const setupData = (data as { setup_data?: { perfStartedAt?: unknown } } | null)?.setup_data;
  const scenario = ctx?.scenario || null;
  const shape = (scenario || {}) as { executor?: string; rate?: number; preAllocatedVUs?: number; maxVUs?: number };
  const knobs = ctx?.perfKnobs === true;
  const steps = scenario ? staircaseSteps.get(scenario) ?? null : null;
  return {
    runId: (__ENV.PERF_RUN_ID || '').trim() || null,
    script: ctx?.script ?? null,
    executor: shape.executor ?? null,
    readMix: ctx?.readMix ?? null,
    rate: shape.executor === 'constant-arrival-rate' ? shape.rate ?? null : null,
    steps,
    stepRampSeconds: steps !== null ? PERF_STEP_RAMP_SECONDS : null,
    seedRows: ctx?.seedRows ?? null,
    preAllocatedVUs: shape.preAllocatedVUs ?? null,
    maxVUs: shape.maxVUs ?? null,
    baseUrl: config.baseURL,
    apiPrefix: config.apiPrefix,
    abortOn429: knobs && perfFlag('PERF_ABORT_ON_429'),
    abortErrorRate: knobs ? perfAbortErrorRate() : null,
    abortP95Ms: knobs ? perfAbortP95Ms() : null,
    harnessTag: (__ENV.PERF_HARNESS_TAG || '').trim() || null,
    startedAt: typeof setupData?.perfStartedAt === 'string' ? setupData.perfStartedAt : null,
    endedAt: new Date().toISOString(),
  };
}

/** Lines that frame the summary JSON on stdout under PERF_SUMMARY_STDOUT=1. */
export const PERF_SUMMARY_BEGIN = '---PERF-SUMMARY-BEGIN---';
export const PERF_SUMMARY_END = '---PERF-SUMMARY-END---';

// handleSummary helper: always emit the human-readable stdout text. With
// PERF_SUMMARY_FILE set, ADDITIONALLY write the full k6 summary JSON, plus a
// top-level `perfMeta`, to that file, so a version A/B can diff
// machine-readable metrics across runs. With PERF_SUMMARY_STDOUT=1, also print
// that JSON as one line between the marker lines, after the text summary, for
// a runner that can only read the log.
export function summaryOutputs(data: unknown, stdout: string, ctx?: PerfSummaryContext): Record<string, string> {
  const outputs: Record<string, string> = { stdout };
  const file = __ENV.PERF_SUMMARY_FILE;
  const markers = perfFlag('PERF_SUMMARY_STDOUT');
  if (!file && !markers) return outputs;

  const json = JSON.stringify({ ...(data as Record<string, unknown>), perfMeta: perfMeta(data, ctx) });
  if (file) {
    outputs[file] = json;
  }
  if (markers) {
    const text = stdout.endsWith('\n') ? stdout : `${stdout}\n`;
    outputs.stdout = `${text}${PERF_SUMMARY_BEGIN}\n${json}\n${PERF_SUMMARY_END}\n`;
  }
  return outputs;
}

// Trend stats every products summary reads. k6's default summaryTrendStats stops
// at p(95), so without this list values['p(99)'] is absent from handleSummary's
// data and a `|| 0` fallback printed "P99 Response Time: 0.00ms".
export const summaryTrendStats: string[] = ['avg', 'min', 'med', 'max', 'p(90)', 'p(95)', 'p(99)'];

/** Shape of one metric in handleSummary data, as far as the summaries read it. */
export interface SummaryMetric {
  values?: Record<string, number | undefined>;
}

/**
 * A Rate metric as a percentage, or "n/a (none ran)" when the metric recorded no
 * sample. k6 omits a metric that never received a value, or keeps it at rate 0
 * with no passes and no fails when a threshold names it, and rendering either
 * as 0.00% reads as "every request failed".
 */
export function formatRate(metric: SummaryMetric | undefined): string {
  const values = metric?.values;
  const rate = values?.rate;
  if (rate === undefined || (values?.passes === 0 && values?.fails === 0)) {
    return 'n/a (none ran)';
  }
  return `${(rate * 100).toFixed(2)}%`;
}

/** A trend stat in milliseconds, or "n/a" when the run recorded none. */
export function formatMs(value: number | undefined): string {
  return value === undefined ? 'n/a' : `${value.toFixed(2)}ms`;
}
