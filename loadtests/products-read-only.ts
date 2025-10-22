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

import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate, Trend, Counter } from 'k6/metrics';
import type { Options } from 'k6/options';
import { config, getURL, getRandomPage, getSeededProductID, headers, loadProfiles } from './config.ts';
import type { ProductResponse, ProductListResponse, Product } from './types/index.ts';

// Custom metrics
const listProductsSuccess = new Rate('list_products_success');
const getProductSuccess = new Rate('get_product_success');
const listProductsDuration = new Trend('list_products_duration');
const getProductDuration = new Trend('get_product_duration');
const totalRequests = new Counter('total_requests');

// Test configuration - use sustained load profile
export const options: Options = {
  stages: loadProfiles.sustained.stages,
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
};

// Store product IDs discovered during list operations
const knownProductIDs: string[] = [];

// Main test function
export default function (): void {
  totalRequests.add(1);

  // 70% list, 30% get
  if (Math.random() < 0.7) {
    listProducts();
  } else {
    getProduct();
  }

  // Shorter think time for read-only workload (0.2-1 seconds)
  sleep(Math.random() * 0.8 + 0.2);
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

  if (knownProductIDs.length > 0) {
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

// Setup function
export function setup(): void {
  console.log('🚀 Starting Read-Only Load Test');
  console.log(`📊 Target: ${config.baseURL}${config.apiPrefix}`);
  console.log('📈 Operation Distribution:');
  console.log('   - List: 70%');
  console.log('   - Get:  30%');
  console.log('');
  console.log('💡 This test establishes baseline read performance');
  console.log('   Use results to compare against CRUD mix tests');
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
export function handleSummary(data: any): { stdout: string } {
  const listSuccess = data.metrics.list_products_success?.values?.rate || 0;
  const getSuccess = data.metrics.get_product_success?.values?.rate || 0;
  const avgDuration = data.metrics.http_req_duration?.values?.avg || 0;
  const p95Duration = data.metrics.http_req_duration?.values['p(95)'] || 0;
  const p99Duration = data.metrics.http_req_duration?.values['p(99)'] || 0;
  const totalReqs = data.metrics.total_requests?.values?.count || 0;

  const summary = `
═══════════════════════════════════════════════════════════
                   READ-ONLY TEST SUMMARY
═══════════════════════════════════════════════════════════
Total Requests:          ${totalReqs}
List Success Rate:       ${(listSuccess * 100).toFixed(2)}%
Get Success Rate:        ${(getSuccess * 100).toFixed(2)}%
Avg Response Time:       ${avgDuration.toFixed(2)}ms
P95 Response Time:       ${p95Duration.toFixed(2)}ms
P99 Response Time:       ${p99Duration.toFixed(2)}ms
═══════════════════════════════════════════════════════════
`;

  console.log(summary);

  return {
    stdout: summary,
  };
}
