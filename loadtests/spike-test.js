// spike-test.js - Traffic Spike Load Test
//
// This test simulates sudden traffic spikes to verify:
// - System resilience under sudden load increases
// - Auto-scaling behavior (if applicable)
// - Connection pool behavior under bursts
// - Rate limiting effectiveness
// - Recovery after spike ends
//
// Spike pattern:
// 1. Baseline load (10 VUs)
// 2. Sudden spike to 300 VUs for 1 minute
// 3. Return to baseline
// 4. Recovery monitoring
//
// Usage:
//   k6 run loadtests/spike-test.js

import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate, Trend, Counter, Gauge } from 'k6/metrics';
import { config, getURL, getRandomProduct, getRandomPage, headers } from './config.js';

// Custom metrics
const spikeErrors = new Rate('spike_errors');
const baselineErrors = new Rate('baseline_errors');
const recoveryErrors = new Rate('recovery_errors');
const currentStage = new Gauge('current_stage');
const requestLatency = new Trend('request_latency');

// Test configuration - spike profile
export const options = {
  stages: [
    { duration: '2m', target: 10 },    // Baseline: establish normal behavior
    { duration: '30s', target: 300 },  // Spike: sudden jump to 300 VUs
    { duration: '1m', target: 300 },   // Hold: maintain spike
    { duration: '30s', target: 10 },   // Drop: sudden return to baseline
    { duration: '2m', target: 10 },    // Recovery: monitor system recovery
  ],
  thresholds: {
    // Allow higher error rates during spike
    'http_req_duration': [
      'p(95)<1500',  // Allow higher latency during spike
      'p(99)<3000',
    ],
    'http_req_failed': [
      'rate<0.10',   // Allow up to 10% errors during spike
    ],
    // Baseline should maintain good performance
    'baseline_errors': ['rate<0.01'],
    // Recovery should return to normal
    'recovery_errors': ['rate<0.02'],
  },
  batch: 10,
};

// Track which stage we're in
let stageStartTime = Date.now();
let currentStageIndex = 0;
const stages = ['baseline', 'spike_ramp', 'spike_hold', 'spike_drop', 'recovery'];

const createdProductIDs = [];

export default function () {
  // Determine current stage based on elapsed time
  const elapsed = (Date.now() - stageStartTime) / 1000;
  const stage = getCurrentStage(elapsed);
  currentStage.add(stages.indexOf(stage));

  // Execute mixed operations
  const response = executeRandomOperation();

  if (response) {
    const isError = response.status >= 400;

    // Record errors by stage
    if (stage === 'baseline') {
      baselineErrors.add(isError);
    } else if (stage.startsWith('spike')) {
      spikeErrors.add(isError);
    } else if (stage === 'recovery') {
      recoveryErrors.add(isError);
    }

    requestLatency.add(response.timings.duration, { stage });
  }

  // Adaptive think time based on stage
  const thinkTime = stage.startsWith('spike') ? 0.1 : 0.5;
  sleep(Math.random() * thinkTime);
}

function getCurrentStage(elapsed) {
  if (elapsed < 120) return 'baseline';        // 0-2m
  if (elapsed < 150) return 'spike_ramp';      // 2m-2.5m
  if (elapsed < 210) return 'spike_hold';      // 2.5m-3.5m
  if (elapsed < 240) return 'spike_drop';      // 3.5m-4m
  return 'recovery';                           // 4m+
}

function executeRandomOperation() {
  const rand = Math.random() * 100;

  // During spike, favor reads (simulates cache stampede scenario)
  const weights = getCurrentStage((Date.now() - stageStartTime) / 1000).startsWith('spike')
    ? { list: 50, get: 35, create: 10, update: 3, delete: 2 }
    : { list: 40, get: 30, create: 20, update: 7, delete: 3 };

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
    console.error(`Operation failed: ${e.message}`);
    return null;
  }
}

function listProducts() {
  const page = getRandomPage();
  const pageSize = config.testData.pageSize;
  const url = getURL(`/products?page=${page}&pageSize=${pageSize}`);

  const response = http.get(url, {
    headers,
    tags: { endpoint: 'list_products', test: 'spike' },
    timeout: '5s',  // Shorter timeout during spike test
  });

  check(response, {
    'list: status ok': (r) => r.status === 200,
    'list: not timeout': (r) => r.status !== 0,
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
    tags: { endpoint: 'get_product', test: 'spike' },
    timeout: '5s',
  });

  check(response, {
    'get: status ok': (r) => r.status === 200 || r.status === 404,
    'get: not timeout': (r) => r.status !== 0,
  });

  return response;
}

function createProduct() {
  const product = getRandomProduct();
  const url = getURL('/products');

  const uniqueProduct = {
    name: `${product.name} ${Date.now()}-${__VU}`,
    description: product.description,
    price: product.price,
    imageURL: `https://example.com/products/${Date.now()}.jpg`,
  };

  const response = http.post(url, JSON.stringify(uniqueProduct), {
    headers,
    tags: { endpoint: 'create_product', test: 'spike' },
    timeout: '5s',
  });

  const success = check(response, {
    'create: status ok': (r) => r.status === 201,
    'create: not timeout': (r) => r.status !== 0,
  });

  if (success) {
    try {
      const body = JSON.parse(response.body);
      // go-bricks wraps response in data object
      const product = body.data || body;
      if (product.id) {
        createdProductIDs.push(product.id);
        if (createdProductIDs.length > 100) {
          createdProductIDs.shift();
        }
      }
    } catch (e) {
      // Ignore parse errors
    }
  }

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
  };

  const response = http.put(url, JSON.stringify(updates), {
    headers,
    tags: { endpoint: 'update_product', test: 'spike' },
    timeout: '5s',
  });

  check(response, {
    'update: status ok': (r) => r.status === 200 || r.status === 404,
    'update: not timeout': (r) => r.status !== 0,
  });

  return response;
}

function deleteProduct() {
  if (createdProductIDs.length < 5) {
    return createProduct();
  }

  const productID = createdProductIDs.shift();
  const url = getURL(`/products/${productID}`);

  const response = http.del(url, null, {
    headers,
    tags: { endpoint: 'delete_product', test: 'spike' },
    timeout: '5s',
  });

  check(response, {
    'delete: status ok': (r) => r.status === 204 || r.status === 404,
    'delete: not timeout': (r) => r.status !== 0,
  });

  return response;
}

export function setup() {
  console.log('🚀 Starting Spike Test');
  console.log(`📊 Target: ${config.baseURL}${config.apiPrefix}`);
  console.log('');
  console.log('📈 Spike Profile:');
  console.log('   Phase 1 (2m):    10 VUs → Baseline');
  console.log('   Phase 2 (30s): → 300 VUs → 🚨 SPIKE!');
  console.log('   Phase 3 (1m):   300 VUs → Hold spike');
  console.log('   Phase 4 (30s): →  10 VUs → Drop back');
  console.log('   Phase 5 (2m):    10 VUs → Recovery monitoring');
  console.log('');
  console.log('💡 This test validates:');
  console.log('   ✓ System handles sudden traffic increases');
  console.log('   ✓ Graceful degradation under extreme load');
  console.log('   ✓ Rate limiting protects backend');
  console.log('   ✓ Quick recovery after spike ends');
  console.log('   ✓ No resource leaks during/after spike');
  console.log('');
  console.log('⚠️  Expected behavior:');
  console.log('   - Some errors during spike are acceptable');
  console.log('   - Latency will increase during spike');
  console.log('   - System should recover quickly after spike');
  console.log('   - No cascading failures');
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
  stageStartTime = Date.now();
  console.log('');
  console.log('⏱️  Test duration: ~6 minutes');
  console.log('');
}

export function teardown(data) {
  console.log('');
  console.log('✅ Spike test completed');
  console.log('');
  console.log('📊 Analysis checklist:');
  console.log('   □ Compare baseline vs spike error rates');
  console.log('   □ Check recovery time (how quickly errors dropped)');
  console.log('   □ Review application logs for connection errors');
  console.log('   □ Check if rate limiting triggered during spike');
  console.log('   □ Verify no resource leaks (memory, connections)');
  console.log('   □ Check database connection pool behavior');
  console.log('');
  console.log('💡 Red flags to watch for:');
  console.log('   🚩 Error rate stays high after spike ends (slow recovery)');
  console.log('   🚩 Memory/CPU doesn\'t return to baseline (resource leak)');
  console.log('   🚩 Database connections stay maxed out (connection leak)');
  console.log('   🚩 Cascading failures (errors in baseline after spike)');
}

export function handleSummary(data) {
  const p95Duration = data.metrics.http_req_duration?.values['p(95)'] || 0;
  const p99Duration = data.metrics.http_req_duration?.values['p(99)'] || 0;
  const totalErrors = data.metrics.http_req_failed?.values?.rate || 0;
  const baselineErrorRate = data.metrics.baseline_errors?.values?.rate || 0;
  const spikeErrorRate = data.metrics.spike_errors?.values?.rate || 0;
  const recoveryErrorRate = data.metrics.recovery_errors?.values?.rate || 0;
  const totalReqs = data.metrics.http_reqs?.values?.count || 0;

  console.log('');
  console.log('═══════════════════════════════════════════════════════════');
  console.log('                     SPIKE TEST SUMMARY                     ');
  console.log('═══════════════════════════════════════════════════════════');
  console.log(`Total Requests:          ${totalReqs}`);
  console.log(`Overall Error Rate:      ${(totalErrors * 100).toFixed(2)}%`);
  console.log('');
  console.log('Error Rates by Phase:');
  console.log(`  Baseline:              ${(baselineErrorRate * 100).toFixed(2)}%`);
  console.log(`  Spike:                 ${(spikeErrorRate * 100).toFixed(2)}%`);
  console.log(`  Recovery:              ${(recoveryErrorRate * 100).toFixed(2)}%`);
  console.log('');
  console.log('Response Times:');
  console.log(`  P95:                   ${p95Duration.toFixed(2)}ms`);
  console.log(`  P99:                   ${p99Duration.toFixed(2)}ms`);
  console.log('═══════════════════════════════════════════════════════════');

  // Evaluate results
  const goodRecovery = recoveryErrorRate < baselineErrorRate * 2;
  const acceptableSpike = spikeErrorRate < 0.15;

  console.log('');
  console.log('Resilience Assessment:');
  console.log(`  Spike handling:        ${acceptableSpike ? '✅ Good' : '❌ Poor - too many errors'}`);
  console.log(`  Recovery speed:        ${goodRecovery ? '✅ Good' : '❌ Poor - slow recovery'}`);
  console.log('');

  return {
    'stdout': data,
  };
}
