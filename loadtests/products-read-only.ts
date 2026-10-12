// products-read-only.ts - Read-Only Baseline Load Test
//
// This test focuses purely on read operations to establish baseline performance
// for the most common operations:
// - 70% List operations (pagination)
// - 30% Get by ID operations
//
// Use this test to:
// - Establish baseline read performance
// - Test database connection pooling under read load
// - Identify caching opportunities
// - Measure query optimization impact
//
// Usage:
//   k6 run loadtests/products-read-only.ts
//   k6 run --vus 100 --duration 5m loadtests/products-read-only.ts
//   PERF_RATE=150 PERF_READ_MIX=get PERF_SEED_ROWS=250000 k6 run loadtests/products-read-only.ts
//
// PERF_READ_MIX (get | list | mixed) and PERF_SEED_ROWS are opt-in perf knobs;
// see config.ts. Gets pick seed ids only with PERF_SEED_ROWS set, which needs
// the seed from scripts/perf-reset-products.sh in the database.

import http from 'k6/http';
import { check } from 'k6';
import { Rate, Trend, Counter } from 'k6/metrics';
import type { Options } from 'k6/options';
import {
  config,
  getURL,
  getRandomPage,
  getSeededProductID,
  headers,
  loadProfiles,
  resolveScenario,
  resolveReadMix,
  perfSeedRows,
  randomSeedProductID,
  perfSetupData,
  withPerfKnobs,
  summaryOutputs,
  summaryTrendStats,
  formatRate,
  maybeSleep,
} from './config.ts';
import type { PerfSetupData } from './config.ts';
import type { ProductResponse, ProductListResponse, Product } from './types/index.ts';

// Custom metrics
const listProductsSuccess = new Rate('list_products_success');
const getProductSuccess = new Rate('get_product_success');
const listProductsDuration = new Trend('list_products_duration');
const getProductDuration = new Trend('get_product_duration');
const totalRequests = new Counter('total_requests');

// Opt-in perf knobs (config.ts): null when unset, which keeps the default
// 70/30 mix and the discovered/fixture ids for gets.
const readMix = resolveReadMix();
const seedRows = perfSeedRows();

// Test configuration - sustained VU stages by default; controlled
// constant-arrival-rate when PERF_RATE is set, or a staircase when PERF_STEPS
// is (see resolveScenario in config.ts).
const readScenario = resolveScenario();
export const options: Options = withPerfKnobs({
  ...(readScenario
    ? { scenarios: { steady: readScenario } }
    : { stages: loadProfiles.sustained.stages }),
  thresholds: {
    // Stricter thresholds for read-only operations
    'http_req_duration': [
      'p(95)<400',   // 95% of requests should be below 400ms
      'p(99)<800',   // 99% of requests should be below 800ms
      'avg<300',     // Average should be below 300ms
    ],
    'http_req_failed': [
      'rate<0.001',  // Less than 0.1% error rate
    ],
    'list_products_success': ['rate>0.99'],
    'get_product_success': ['rate>0.99'],
  },
  batch: 15,
  // Adds p(99), which the summary prints, to k6's default trend stats.
  summaryTrendStats,
});

// Store product IDs discovered during list operations
const knownProductIDs: string[] = [];

// Main test function
export default function (): void {
  totalRequests.add(1);

  if (pickList()) {
    listProducts();
  } else {
    getProduct();
  }

  // Shorter think time for read-only workload (0.2-1 seconds); suppressed in
  // controlled A/B mode so the arrival-rate scenario drives the offered load.
  maybeSleep(Math.random() * 0.8 + 0.2, readScenario !== null);
}

// 70% list, 30% get by default and under PERF_READ_MIX=mixed; one operation
// only under get or list.
function pickList(): boolean {
  if (readMix === 'get') return false;
  if (readMix === 'list') return true;
  return Math.random() < 0.7;
}

function listProducts(): void {
  const page = getRandomPage();
  const pageSize = config.testData.pageSize;
  const url = getURL(`/products?page=${page}&pageSize=${pageSize}`);

  const response = http.get(url, {
    headers,
    tags: { endpoint: 'list_products', operation: 'read' },
  });

  const success = check(response, {
    'list: status is 200': (r) => r.status === 200,
    'list: response time < 500ms': (r) => r.timings.duration < 500,
    'list: has products array': (r) => {
      try {
        const body = JSON.parse(r.body as string) as ProductListResponse;
        // go-bricks wraps response in data object
        const products = body.data?.products || body.products;
        // Store product IDs for use in get operations
        if (Array.isArray(products)) {
          products.forEach((p: Product) => {
            if (p.id && knownProductIDs.indexOf(p.id) === -1) {
              knownProductIDs.push(p.id);
              // Limit array size
              if (knownProductIDs.length > 200) {
                knownProductIDs.shift();
              }
            }
          });
          return true;
        }
        return false;
      } catch (e) {
        return false;
      }
    },
    'list: has pagination info': (r) => {
      try {
        const body = JSON.parse(r.body as string) as ProductListResponse;
        // go-bricks wraps response in data object
        const data = body.data || body;
        return data.page !== undefined && data.pageSize !== undefined && data.total !== undefined;
      } catch (e) {
        return false;
      }
    },
  });

  listProductsSuccess.add(success ? 1 : 0);
  listProductsDuration.add(response.timings.duration);
}

function getProduct(): void {
  let productID: string;

  if (seedRows !== null) {
    // A uniform pick from the deterministic seed (PERF_SEED_ROWS).
    productID = randomSeedProductID(seedRows);
  } else if (knownProductIDs.length > 0) {
    // Use a known product ID from list operations
    productID = knownProductIDs[Math.floor(Math.random() * knownProductIDs.length)];
  } else {
    // Fallback to seeded product IDs from database migration
    productID = getSeededProductID();
  }

  const url = getURL(`/products/${productID}`);

  const response = http.get(url, {
    headers,
    tags: { endpoint: 'get_product', operation: 'read' },
  });

  const success = check(response, {
    'get: status is 200': (r) => r.status === 200,
    'get: response time < 400ms': (r) => r.timings.duration < 400,
    'get: has valid product': (r) => {
      try {
        const body = JSON.parse(r.body as string) as ProductResponse;
        // go-bricks wraps response in data object
        const product = body.data || body;
        return !!(product.id && product.name && product.price !== undefined);
      } catch (e) {
        return false;
      }
    },
    'get: has timestamps': (r) => {
      try {
        const body = JSON.parse(r.body as string) as ProductResponse;
        // go-bricks wraps response in data object
        const product = body.data || body;
        return !!(product.createdDate && product.updatedDate);
      } catch (e) {
        return false;
      }
    },
  });

  getProductSuccess.add(success ? 1 : 0);
  getProductDuration.add(response.timings.duration);
}

// Setup function. Its return value carries the run's start time to
// handleSummary (perfMeta.startedAt); the default function ignores it.
export function setup(): PerfSetupData {
  console.log('🚀 Starting Read-Only Load Test');
  console.log(`📊 Target: ${config.baseURL}${config.apiPrefix}`);
  console.log('📈 Operation Distribution:');
  if (readMix === 'get' || readMix === 'list') {
    console.log(`   - ${readMix === 'get' ? 'Get:  ' : 'List: '}100% (PERF_READ_MIX=${readMix})`);
  } else {
    console.log('   - List: 70%');
    console.log('   - Get:  30%');
  }
  if (seedRows !== null) {
    console.log(`   - Gets pick from ${seedRows} seed rows (PERF_SEED_ROWS)`);
  }
  console.log('');
  console.log('💡 This test establishes baseline read performance');
  console.log('   Use results to compare against CRUD mix tests');
  console.log('');

  // Health check
  const healthURL = getURL('/health');
  const response = http.get(healthURL);

  if (response.status !== 200) {
    console.error('❌ Health check failed!');
    console.error(`   URL: ${healthURL}`);
    console.error(`   Status: ${response.status}`);
    throw new Error('API health check failed');
  }

  console.log('✅ Health check passed');
  console.log('');

  // Seed-id gets never read knownProductIDs, so skip the warm-up lists: they
  // would only add requests outside the configured mix.
  if (seedRows !== null) {
    return perfSetupData();
  }

  // Warm up - do a few list requests to populate knownProductIDs
  console.log('🔥 Warming up - fetching initial product IDs...');
  for (let i = 1; i <= 3; i++) {
    const url = getURL(`/products?page=${i}&pageSize=10`);
    const resp = http.get(url, { headers });
    if (resp.status === 200) {
      try {
        const body = JSON.parse(resp.body as string) as ProductListResponse;
        // go-bricks wraps response in data object
        const products = body.data?.products || body.products;
        if (Array.isArray(products)) {
          products.forEach((p: Product) => {
            if (p.id) knownProductIDs.push(p.id);
          });
        }
      } catch (e) {
        // Ignore parse errors during warmup
      }
    }
  }

  console.log(`✅ Warmed up with ${knownProductIDs.length} product IDs`);
  console.log('');
  return perfSetupData();
}

// Teardown function
export function teardown(): void {
  console.log('');
  console.log('✅ Read-only load test completed');
  console.log(`📊 Discovered ${knownProductIDs.length} unique product IDs`);
  console.log('');
  console.log('💡 Next steps:');
  console.log('   - Compare these results with products-crud.ts results');
  console.log('   - Check if read performance degrades under write load');
  console.log('   - Consider caching strategies for frequently accessed products');
}

// Custom summary
export function handleSummary(data: any): Record<string, string> {
  const avgDuration = data.metrics.http_req_duration?.values?.avg || 0;
  const p95Duration = data.metrics.http_req_duration?.values['p(95)'] || 0;
  const p99Duration = data.metrics.http_req_duration?.values['p(99)'] || 0;
  const totalReqs = data.metrics.total_requests?.values?.count || 0;

  const summary = `
═══════════════════════════════════════════════════════════
                   READ-ONLY TEST SUMMARY
═══════════════════════════════════════════════════════════
Total Requests:          ${totalReqs}
List Success Rate:       ${formatRate(data.metrics.list_products_success)}
Get Success Rate:        ${formatRate(data.metrics.get_product_success)}
Avg Response Time:       ${avgDuration.toFixed(2)}ms
P95 Response Time:       ${p95Duration.toFixed(2)}ms
P99 Response Time:       ${p99Duration.toFixed(2)}ms
═══════════════════════════════════════════════════════════
`;

  console.log(summary);

  return summaryOutputs(data, summary, {
    script: 'products-read-only.ts',
    scenario: readScenario,
    readMix,
    seedRows,
  });
}
