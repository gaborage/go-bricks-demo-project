// k6 Load Testing Configuration
// This file contains shared configuration for all load tests

import { sleep } from 'k6';
import type { TestConfig, LoadProfile, SampleProduct, Headers } from './types/index.ts';
import type { Scenario } from 'k6/options';

export const config: TestConfig = {
  // Base URL for API - override with K6_BASE_URL environment variable
  baseURL: __ENV.K6_BASE_URL || 'http://localhost:8080',

  // API path prefix
  apiPrefix: '/api/v1',

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
//   PERF_PREALLOC  preallocated VUs reused for keep-alive connections (def 300)
//   PERF_MAXVUS    hard VU ceiling (default = PERF_PREALLOC)

/** Steady constant-arrival-rate scenario when PERF_RATE is set, else null. */
export function resolveScenario(): Scenario | null {
  const rate = parseInt(__ENV.PERF_RATE || '0', 10);
  if (rate <= 0) return null;
  const prealloc = parseInt(__ENV.PERF_PREALLOC || '300', 10);
  return {
    executor: 'constant-arrival-rate',
    rate,
    timeUnit: '1s',
    duration: __ENV.PERF_DURATION || '60s',
    preAllocatedVUs: prealloc,
    maxVUs: parseInt(__ENV.PERF_MAXVUS || String(prealloc), 10),
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
  const peak = parseInt(__ENV.PERF_SPIKE_PEAK || __ENV.PERF_RATE || '0', 10);
  if (peak <= 0) return null;
  const base = parseInt(__ENV.PERF_SPIKE_BASE || '300', 10);
  const prealloc = parseInt(__ENV.PERF_PREALLOC || '600', 10);
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
  const controlled = parseInt(__ENV.PERF_SPIKE_PEAK || __ENV.PERF_RATE || '0', 10) > 0;
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

// In controlled A/B mode (PERF_RATE or PERF_SPIKE_PEAK set) the offered load is
// driven open-loop by the arrival-rate scenario, so per-iteration think-time is
// suppressed — otherwise think-time would make the scenario VU-bound and the
// offered rate unreachable. In default mode the native think-time is preserved
// (realistic closed-loop user simulation), so `make loadtest-*` is unchanged.
export function controlledMode(): boolean {
  return parseInt(__ENV.PERF_RATE || '0', 10) > 0 || parseInt(__ENV.PERF_SPIKE_PEAK || '0', 10) > 0;
}

/** sleep() in default mode; no-op in controlled A/B mode. */
export function maybeSleep(seconds: number): void {
  if (controlledMode()) return;
  sleep(seconds);
}

// handleSummary helper: always emit the human-readable stdout text; ADDITIONALLY
// write the full k6 summary JSON to PERF_SUMMARY_FILE when set, so a version A/B
// can diff machine-readable metrics across runs.
export function summaryOutputs(data: unknown, stdout: string): Record<string, string> {
  const outputs: Record<string, string> = { stdout };
  const file = __ENV.PERF_SUMMARY_FILE;
  if (file) {
    outputs[file] = JSON.stringify(data);
  }
  return outputs;
}
