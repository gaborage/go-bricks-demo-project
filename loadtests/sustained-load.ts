// sustained-load.ts - Sustained Load Test
//
// This test maintains constant load over an extended period to:
// - Identify memory leaks
// - Detect connection leaks
// - Monitor system stability over time
// - Validate resource cleanup
// - Check for gradual performance degradation
//
// Runs at constant 50 VUs for 15 minutes
//
// Usage:
//   k6 run loadtests/sustained-load.ts
//   k6 run --duration 30m loadtests/sustained-load.ts  # Extended duration

import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate, Trend, Counter, Gauge } from 'k6/metrics';
import type { Options } from 'k6/options';
import type { RefinedResponse, ResponseType } from 'k6/http';
import { config, getURL, getRandomProduct, getRandomPage, getSeededProductID, headers } from './config.ts';
import type { ProductResponse, CreateProductInput, UpdateProductInput } from './types/index.ts';

// Custom metrics to detect degradation over time
const errorRate = new Rate('errors');
const successRate = new Rate('success');
const memoryLeakIndicator = new Trend('response_time_trend');  // Should stay flat
const connectionIssues = new Counter('connection_errors');
const timeouts = new Counter('timeout_errors');
const activeConnections = new Gauge('active_connections_estimate');

// Interface for metrics window tracking
interface MetricsWindow {
  count: number;
  sumDuration: number;
  errors: number;
}

interface MetricsWindows {
  first5min: MetricsWindow;
  middle5min: MetricsWindow;
  last5min: MetricsWindow;
}

// Track metrics over time windows
const metricsWindow: MetricsWindows = {
  first5min: { count: 0, sumDuration: 0, errors: 0 },
  middle5min: { count: 0, sumDuration: 0, errors: 0 },
  last5min: { count: 0, sumDuration: 0, errors: 0 },
};

// Test configuration - sustained load
export const options: Options = {
  stages: [
    { duration: '1m', target: 50 },   // Ramp up
    { duration: '15m', target: 50 },  // Sustained load
    { duration: '1m', target: 0 },    // Ramp down
  ],
  thresholds: {
    'http_req_duration': [
      'p(95)<500',   // p95 should stay under 500ms
      'p(99)<1000',  // p99 should stay under 1s
      'avg<350',     // Average should stay low
    ],
    'http_req_failed': [
      'rate<0.01',   // Less than 1% errors
    ],
    'success': ['rate>0.99'],  // 99% success rate
    'connection_errors': ['count<10'],  // Very few connection errors
    'timeout_errors': ['count<5'],  // Almost no timeouts
  },
  batch: 10,
};

const createdProductIDs: string[] = [];
const startTime = Date.now();

interface OperationWeights {
  list: number;
  get: number;
  create: number;
  update: number;
  delete: number;
}

export default function (): void {
  const elapsed = (Date.now() - startTime) / 1000;

  // Execute mixed operations
  const response = executeRandomOperation();

  if (response) {
    const isSuccess = response.status >= 200 && response.status < 400;
    const duration = response.timings.duration;

    successRate.add(isSuccess ? 1 : 0);
    errorRate.add(!isSuccess ? 1 : 0);
    memoryLeakIndicator.add(duration);

    // Track connection and timeout errors specifically
    if (response.status === 0 || (response as any).error_code === 1050) {
      connectionIssues.add(1);
    }
    if (response.status === 0 && (response as any).error?.includes('timeout')) {
      timeouts.add(1);
    }

    // Collect metrics by time window for degradation analysis
    recordMetricsByWindow(elapsed, duration, !isSuccess);
  }

  // Estimate active connections (rough approximation)
  activeConnections.add(__VU);

  // Realistic think time
  sleep(Math.random() * 0.8 + 0.3);
}

function recordMetricsByWindow(elapsed: number, duration: number, isError: boolean): void {
  // First 5 minutes (after 1min ramp-up)
  if (elapsed >= 60 && elapsed < 360) {
    metricsWindow.first5min.count++;
    metricsWindow.first5min.sumDuration += duration;
    if (isError) metricsWindow.first5min.errors++;
  }
  // Middle 5 minutes (6-11 min)
  else if (elapsed >= 360 && elapsed < 660) {
    metricsWindow.middle5min.count++;
    metricsWindow.middle5min.sumDuration += duration;
    if (isError) metricsWindow.middle5min.errors++;
  }
  // Last 5 minutes (11-16 min)
  else if (elapsed >= 660 && elapsed < 960) {
    metricsWindow.last5min.count++;
    metricsWindow.last5min.sumDuration += duration;
    if (isError) metricsWindow.last5min.errors++;
  }
}

function executeRandomOperation(): RefinedResponse<ResponseType | undefined> | null {
  const rand = Math.random() * 100;

  // Realistic production mix
  const weights: OperationWeights = {
    list: 45,
    get: 30,
    create: 15,
    update: 7,
    delete: 3,
  };

  try {
    if (rand < weights.list) {
      return listProducts();
    } else if (rand < weights.list + weights.get) {
      return getProduct();
    } else if (rand < weights.list + weights.get + weights.create) {
      return createProduct();
    } else if (rand < 100 - weights.delete) {
      return updateProduct();
    } else {
      return deleteProduct();
    }
  } catch (e) {
    console.error(`Operation failed: ${(e as Error).message}`);
    return null;
  }
}

function listProducts(): RefinedResponse<ResponseType | undefined> {
  const page = getRandomPage();
  const pageSize = config.testData.pageSize;
  const url = getURL(`/products?page=${page}&pageSize=${pageSize}`);

  const response = http.get(url, {
    headers,
    tags: { endpoint: 'list_products', test: 'sustained' },
  });

  check(response, {
    'list: status ok': (r) => r.status === 200,
  });

  return response;
}

function getProduct(): RefinedResponse<ResponseType | undefined> {
  let productID: string;

  if (createdProductIDs.length > 0) {
    productID = createdProductIDs[Math.floor(Math.random() * createdProductIDs.length)];
  } else {
    // Fallback to seeded product IDs from database migration
    productID = getSeededProductID();
  }

  const url = getURL(`/products/${productID}`);

  const response = http.get(url, {
    headers,
    tags: { endpoint: 'get_product', test: 'sustained' },
  });

  check(response, {
    'get: status ok': (r) => r.status === 200 || r.status === 404,
  });

  return response;
}

function createProduct(): RefinedResponse<ResponseType | undefined> {
  const product = getRandomProduct();
  const url = getURL('/products');

  const uniqueProduct: CreateProductInput = {
    name: `${product.name} ${Date.now()}-${__VU}-${__ITER}`,
    description: product.description,
    price: product.price + (Math.random() * 10),
    imageURL: `https://example.com/products/${Date.now()}-${__VU}.jpg`,
  };

  const response = http.post(url, JSON.stringify(uniqueProduct), {
    headers,
    tags: { endpoint: 'create_product', test: 'sustained' },
  });

  const success = check(response, {
    'create: status ok': (r) => r.status === 201,
  });

  if (success) {
    try {
      const body = JSON.parse(response.body as string) as ProductResponse;
      // go-bricks wraps response in data object
      const product = body.data || body;
      if (product.id) {
        createdProductIDs.push(product.id);
        // Keep array size reasonable
        if (createdProductIDs.length > 200) {
          createdProductIDs.shift();
        }
      }
    } catch (e) {
      // Ignore parse errors
    }
  }

  return response;
}

function updateProduct(): RefinedResponse<ResponseType | undefined> {
  if (createdProductIDs.length === 0) {
    return createProduct();
  }

  const productID = createdProductIDs[Math.floor(Math.random() * createdProductIDs.length)];
  const url = getURL(`/products/${productID}`);

  const updates: UpdateProductInput = {
    price: Math.random() * 200 + 10,
    description: `Updated at ${Date.now()}`,
  };

  const response = http.put(url, JSON.stringify(updates), {
    headers,
    tags: { endpoint: 'update_product', test: 'sustained' },
  });

  check(response, {
    'update: status ok': (r) => r.status === 200 || r.status === 404,
  });

  return response;
}

function deleteProduct(): RefinedResponse<ResponseType | undefined> {
  if (createdProductIDs.length < 20) {
    return createProduct();
  }

  const productID = createdProductIDs.shift();
  const url = getURL(`/products/${productID}`);

  const response = http.del(url, null, {
    headers,
    tags: { endpoint: 'delete_product', test: 'sustained' },
  });

  check(response, {
    'delete: status ok': (r) => r.status === 204 || r.status === 404,
  });

  return response;
}

export function setup(): void {
  console.log('🚀 Starting Sustained Load Test');
  console.log(`📊 Target: ${config.baseURL}${config.apiPrefix}`);
  console.log('');
  console.log('📈 Load Profile:');
  console.log('   Phase 1 (1m):  →  50 VUs  (Ramp up)');
  console.log('   Phase 2 (15m):    50 VUs  (Sustained load)');
  console.log('   Phase 3 (1m):  →   0 VUs  (Ramp down)');
  console.log('');
  console.log('💡 This test validates:');
  console.log('   ✓ No memory leaks over time');
  console.log('   ✓ No connection leaks');
  console.log('   ✓ Stable performance (no degradation)');
  console.log('   ✓ Proper resource cleanup');
  console.log('   ✓ Database connection pool stability');
  console.log('');
  console.log('🔍 Watch server metrics:');
  console.log('   - Memory usage should stay flat');
  console.log('   - CPU usage should stay constant');
  console.log('   - DB connections should stay under max');
  console.log('   - No error log growth');
  console.log('');

  // Health check
  const healthURL = `${config.baseURL}${config.apiPrefix}/health`;
  const response = http.get(healthURL);

  if (response.status !== 200) {
    console.error('❌ Health check failed!');
    console.error(`   URL: ${healthURL}`);
    console.error(`   Status: ${response.status}`);
    throw new Error('API health check failed');
  }

  console.log('✅ Health check passed');
  console.log('');
  console.log('⏱️  Test duration: ~17 minutes');
  console.log('⚠️  Run system monitoring during this test!');
  console.log('');
}

export function teardown(): void {
  // Calculate average response times by window
  const first5minAvg = metricsWindow.first5min.count > 0
    ? metricsWindow.first5min.sumDuration / metricsWindow.first5min.count
    : 0;
  const middle5minAvg = metricsWindow.middle5min.count > 0
    ? metricsWindow.middle5min.sumDuration / metricsWindow.middle5min.count
    : 0;
  const last5minAvg = metricsWindow.last5min.count > 0
    ? metricsWindow.last5min.sumDuration / metricsWindow.last5min.count
    : 0;

  const first5minErrRate = metricsWindow.first5min.count > 0
    ? metricsWindow.first5min.errors / metricsWindow.first5min.count
    : 0;
  const last5minErrRate = metricsWindow.last5min.count > 0
    ? metricsWindow.last5min.errors / metricsWindow.last5min.count
    : 0;

  console.log('');
  console.log('✅ Sustained load test completed');
  console.log('');
  console.log('📊 Degradation Analysis:');
  console.log('═══════════════════════════════════════════════════════════');
  console.log('Average Response Time by Period:');
  console.log(`  First 5 minutes:       ${first5minAvg.toFixed(2)}ms`);
  console.log(`  Middle 5 minutes:      ${middle5minAvg.toFixed(2)}ms`);
  console.log(`  Last 5 minutes:        ${last5minAvg.toFixed(2)}ms`);
  console.log('');
  console.log('Error Rate by Period:');
  console.log(`  First 5 minutes:       ${(first5minErrRate * 100).toFixed(2)}%`);
  console.log(`  Last 5 minutes:        ${(last5minErrRate * 100).toFixed(2)}%`);
  console.log('═══════════════════════════════════════════════════════════');

  // Detect degradation
  const degradationThreshold = 1.2; // 20% increase
  const hasResponseTimeDegradation = last5minAvg > first5minAvg * degradationThreshold;
  const hasErrorRateIncrease = last5minErrRate > first5minErrRate * 1.5;

  console.log('');
  console.log('Health Assessment:');
  console.log(`  Response time:         ${hasResponseTimeDegradation ? '⚠️  DEGRADED' : '✅ STABLE'}`);
  console.log(`  Error rate:            ${hasErrorRateIncrease ? '⚠️  INCREASED' : '✅ STABLE'}`);

  if (hasResponseTimeDegradation || hasErrorRateIncrease) {
    console.log('');
    console.log('⚠️  WARNING: Performance degradation detected!');
    console.log('');
    console.log('🔍 Investigate:');
    console.log('   □ Check for memory leaks (memory usage growth)');
    console.log('   □ Check for connection leaks (open connections count)');
    console.log('   □ Review application logs for errors');
    console.log('   □ Check database slow query logs');
    console.log('   □ Monitor system resources (CPU, RAM, disk I/O)');
  } else {
    console.log('');
    console.log('✅ No degradation detected - system is stable!');
  }

  console.log('');
  console.log(`📦 Created ${createdProductIDs.length} products remaining in memory`);
}

export function handleSummary(data: any): { stdout: string } {
  const avgDuration = data.metrics.http_req_duration?.values?.avg || 0;
  const p95Duration = data.metrics.http_req_duration?.values['p(95)'] || 0;
  const p99Duration = data.metrics.http_req_duration?.values['p(99)'] || 0;
  const errorRateValue = data.metrics.errors?.values?.rate || 0;
  const totalReqs = data.metrics.http_reqs?.values?.count || 0;
  const reqRate = data.metrics.http_reqs?.values?.rate || 0;
  const connErrors = data.metrics.connection_errors?.values?.count || 0;
  const timeoutErrors = data.metrics.timeout_errors?.values?.count || 0;

  const summary = `
═══════════════════════════════════════════════════════════
                 SUSTAINED LOAD TEST SUMMARY
═══════════════════════════════════════════════════════════
Total Requests:          ${totalReqs}
Requests/sec:            ${reqRate.toFixed(2)}
Error Rate:              ${(errorRateValue * 100).toFixed(2)}%
Connection Errors:       ${connErrors}
Timeout Errors:          ${timeoutErrors}

Response Times:
  Average:               ${avgDuration.toFixed(2)}ms
  P95:                   ${p95Duration.toFixed(2)}ms
  P99:                   ${p99Duration.toFixed(2)}ms
═══════════════════════════════════════════════════════════
`;

  const passed = errorRateValue < 0.01 && p95Duration < 500 && connErrors < 10;
  const status = passed ? '✅ TEST PASSED' : '❌ TEST FAILED - Review thresholds and logs';

  console.log(summary);
  console.log(status);
  console.log('');

  return {
    stdout: summary + '\n' + status + '\n',
  };
}
