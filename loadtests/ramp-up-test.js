// ramp-up-test.js - Gradual Load Increase Test
//
// This test gradually increases load to find the system's breaking point.
// It helps identify:
// - Maximum sustainable throughput
// - Resource exhaustion points (CPU, memory, DB connections)
// - Graceful degradation behavior
// - Connection pool bottlenecks
//
// The test increases load in stages and monitors for:
// - Increased latency (p95/p99)
// - Error rate increases
// - Throughput plateaus
//
// Usage:
//   k6 run loadtests/ramp-up-test.js
//   k6 run --out json=results.json loadtests/ramp-up-test.js

import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate, Trend, Counter } from 'k6/metrics';
import { config, getURL, getRandomProduct, getRandomPage, headers } from './config.js';

// Custom metrics
const errorRate = new Rate('errors');
const successRate = new Rate('success');
const requestDuration = new Trend('request_duration');
const requestsPerStage = new Counter('requests_per_stage');

// Test configuration - aggressive ramp-up to find limits
export const options = {
  stages: [
    { duration: '1m', target: 10 },    // Stage 1: Baseline
    { duration: '1m', target: 25 },    // Stage 2: Light load
    { duration: '1m', target: 50 },    // Stage 3: Moderate load
    { duration: '1m', target: 75 },    // Stage 4: Heavy load
    { duration: '1m', target: 100 },   // Stage 5: Very heavy load
    { duration: '2m', target: 100 },   // Stage 6: Sustained heavy
    { duration: '1m', target: 150 },   // Stage 7: Stress level
    { duration: '2m', target: 150 },   // Stage 8: Sustained stress
    { duration: '1m', target: 200 },   // Stage 9: Breaking point?
    { duration: '2m', target: 200 },   // Stage 10: Sustained breaking
    { duration: '2m', target: 0 },     // Ramp down
  ],
  thresholds: {
    // More lenient thresholds - we expect some degradation
    'http_req_duration': [
      'p(95)<1000',  // Allow up to 1s for p95
      'p(99)<2000',  // Allow up to 2s for p99
    ],
    'http_req_failed': [
      'rate<0.05',   // Allow up to 5% errors
    ],
    'success': ['rate>0.95'],  // Require 95% success rate
  },
  batch: 10,
};

// Operation weights - balanced mix
const OPERATION_WEIGHTS = {
  list: 40,
  get: 30,
  create: 20,
  update: 7,
  delete: 3,
};

const createdProductIDs = [];

export default function () {
  requestsPerStage.add(1);

  const rand = Math.random() * 100;
  let response;

  try {
    if (rand < OPERATION_WEIGHTS.list) {
      response = listProducts();
    } else if (rand < OPERATION_WEIGHTS.list + OPERATION_WEIGHTS.get) {
      response = getProduct();
    } else if (rand < OPERATION_WEIGHTS.list + OPERATION_WEIGHTS.get + OPERATION_WEIGHTS.create) {
      response = createProduct();
    } else if (rand < 100 - OPERATION_WEIGHTS.delete) {
      response = updateProduct();
    } else {
      response = deleteProduct();
    }

    if (response) {
      const isSuccess = response.status >= 200 && response.status < 300;
      successRate.add(isSuccess);
      errorRate.add(!isSuccess);
      requestDuration.add(response.timings.duration);
    }
  } catch (e) {
    errorRate.add(1);
    console.error(`Request failed: ${e.message}`);
  }

  // Variable think time - shorter under higher load (realistic behavior)
  const currentVUs = __VU;
  const thinkTime = currentVUs > 100 ? 0.3 : 0.8;
  sleep(Math.random() * thinkTime + 0.1);
}

function listProducts() {
  const page = getRandomPage();
  const pageSize = config.testData.pageSize;
  const url = getURL(`/products?page=${page}&pageSize=${pageSize}`);

  const response = http.get(url, {
    headers,
    tags: { endpoint: 'list_products', test: 'ramp-up' },
  });

  check(response, {
    'list: status ok': (r) => r.status === 200,
  });

  return response;
}

function getProduct() {
  let productID;

  if (createdProductIDs.length > 0) {
    productID = createdProductIDs[Math.floor(Math.random() * createdProductIDs.length)];
  } else {
    productID = `prod-${Math.floor(Math.random() * 100) + 1}`;
  }

  const url = getURL(`/products/${productID}`);

  const response = http.get(url, {
    headers,
    tags: { endpoint: 'get_product', test: 'ramp-up' },
  });

  check(response, {
    'get: status ok': (r) => r.status === 200 || r.status === 404,
  });

  return response;
}

function createProduct() {
  const product = getRandomProduct();
  const url = getURL('/products');

  const uniqueProduct = {
    name: `${product.name} ${Date.now()}-${__VU}`,
    description: product.description,
    price: product.price + (Math.random() * 10),
    imageURL: `https://example.com/products/${Date.now()}-${__VU}.jpg`,
  };

  const response = http.post(url, JSON.stringify(uniqueProduct), {
    headers,
    tags: { endpoint: 'create_product', test: 'ramp-up' },
  });

  check(response, {
    'create: status ok': (r) => r.status === 201,
  }) && storeProductID(response);

  return response;
}

function updateProduct() {
  if (createdProductIDs.length === 0) {
    return createProduct();
  }

  const productID = createdProductIDs[Math.floor(Math.random() * createdProductIDs.length)];
  const url = getURL(`/products/${productID}`);

  const updates = {
    price: Math.random() * 200 + 10,
    description: `Updated at ${Date.now()}`,
  };

  const response = http.put(url, JSON.stringify(updates), {
    headers,
    tags: { endpoint: 'update_product', test: 'ramp-up' },
  });

  check(response, {
    'update: status ok': (r) => r.status === 200 || r.status === 404,
  });

  return response;
}

function deleteProduct() {
  if (createdProductIDs.length < 10) {
    return createProduct();
  }

  const productID = createdProductIDs.shift();
  const url = getURL(`/products/${productID}`);

  const response = http.del(url, null, {
    headers,
    tags: { endpoint: 'delete_product', test: 'ramp-up' },
  });

  check(response, {
    'delete: status ok': (r) => r.status === 204 || r.status === 404,
  });

  return response;
}

function storeProductID(response) {
  try {
    const body = JSON.parse(response.body);
    // go-bricks wraps response in data object
    const product = body.data || body;
    if (product.id) {
      createdProductIDs.push(product.id);
      if (createdProductIDs.length > 150) {
        createdProductIDs.shift();
      }
    }
  } catch (e) {
    // Ignore parse errors
  }
}

export function setup() {
  console.log('🚀 Starting Ramp-Up Test');
  console.log(`📊 Target: ${config.baseURL}${config.apiPrefix}`);
  console.log('');
  console.log('📈 Load Profile:');
  console.log('   Stage  1 (1m):  →  10 VUs  (Baseline)');
  console.log('   Stage  2 (1m):  →  25 VUs  (Light load)');
  console.log('   Stage  3 (1m):  →  50 VUs  (Moderate)');
  console.log('   Stage  4 (1m):  →  75 VUs  (Heavy)');
  console.log('   Stage  5 (1m):  → 100 VUs  (Very heavy)');
  console.log('   Stage  6 (2m):  → 100 VUs  (Sustained)');
  console.log('   Stage  7 (1m):  → 150 VUs  (Stress)');
  console.log('   Stage  8 (2m):  → 150 VUs  (Sustained stress)');
  console.log('   Stage  9 (1m):  → 200 VUs  (Breaking point?)');
  console.log('   Stage 10 (2m):  → 200 VUs  (Sustained breaking)');
  console.log('   Stage 11 (2m):  →   0 VUs  (Ramp down)');
  console.log('');
  console.log('💡 Watch for:');
  console.log('   - Latency increases at each stage');
  console.log('   - Error rate changes');
  console.log('   - Connection pool exhaustion');
  console.log('   - Database slow query logs');
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
  console.log('');
}

export function teardown(data) {
  console.log('');
  console.log('✅ Ramp-up test completed');
  console.log('');
  console.log('📊 Analysis checklist:');
  console.log('   □ At what VU count did latency start increasing?');
  console.log('   □ Did error rates increase at higher loads?');
  console.log('   □ Check application logs for connection pool warnings');
  console.log('   □ Check database connection count (should not hit max)');
  console.log('   □ Review slow query logs');
  console.log('   □ Check memory/CPU usage on server');
  console.log('');
  console.log('💡 Configuration tuning recommendations:');
  console.log('   - If DB connection errors: increase database.pool.max.connections');
  console.log('   - If high latency: check slow query logs, add indexes');
  console.log('   - If timeout errors: increase server.timeout.read/write');
  console.log('   - If rate limit errors: increase app.rate.limit/burst');
}

export function handleSummary(data) {
  const avgDuration = data.metrics.http_req_duration?.values?.avg || 0;
  const p95Duration = data.metrics.http_req_duration?.values['p(95)'] || 0;
  const p99Duration = data.metrics.http_req_duration?.values['p(99)'] || 0;
  const errorRateValue = data.metrics.errors?.values?.rate || 0;
  const successRateValue = data.metrics.success?.values?.rate || 0;
  const totalReqs = data.metrics.http_reqs?.values?.count || 0;
  const reqRate = data.metrics.http_reqs?.values?.rate || 0;

  console.log('');
  console.log('═══════════════════════════════════════════════════════════');
  console.log('                   RAMP-UP TEST SUMMARY                     ');
  console.log('═══════════════════════════════════════════════════════════');
  console.log(`Total Requests:          ${totalReqs}`);
  console.log(`Requests/sec:            ${reqRate.toFixed(2)}`);
  console.log(`Success Rate:            ${(successRateValue * 100).toFixed(2)}%`);
  console.log(`Error Rate:              ${(errorRateValue * 100).toFixed(2)}%`);
  console.log(`Avg Response Time:       ${avgDuration.toFixed(2)}ms`);
  console.log(`P95 Response Time:       ${p95Duration.toFixed(2)}ms`);
  console.log(`P99 Response Time:       ${p99Duration.toFixed(2)}ms`);
  console.log('═══════════════════════════════════════════════════════════');

  // Determine test result
  const passed = successRateValue >= 0.95 && p95Duration < 1000;
  console.log('');
  console.log(passed ? '✅ TEST PASSED' : '❌ TEST FAILED - Review thresholds');
  console.log('');

  return {
    'stdout': data,
  };
}
