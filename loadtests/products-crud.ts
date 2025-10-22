// products-crud.ts - Realistic CRUD Mix Load Test
//
// This test simulates realistic production traffic with a mix of:
// - 50% List operations (pagination)
// - 25% Get by ID operations
// - 15% Create operations
// - 7% Update operations
// - 3% Delete operations
//
// Usage:
//   k6 run loadtests/products-crud.ts
//   k6 run --vus 50 --duration 5m loadtests/products-crud.ts
//   K6_BASE_URL=http://prod.example.com:8080 k6 run loadtests/products-crud.ts

import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate, Trend } from 'k6/metrics';
import type { Options } from 'k6/options';
import {
  config,
  getURL,
  getRandomProduct,
  getRandomPage,
  getSeededProductID,
  headers,
  loadProfiles,
} from './config.ts';
import type { ProductResponse, ProductListResponse, CreateProductInput, UpdateProductInput } from './types/index.ts';

// Custom metrics
const listProductsRate = new Rate('list_products_success');
const getProductRate = new Rate('get_product_success');
const createProductRate = new Rate('create_product_success');
const updateProductRate = new Rate('update_product_success');
const deleteProductRate = new Rate('delete_product_success');

const listProductsDuration = new Trend('list_products_duration');
const getProductDuration = new Trend('get_product_duration');
const createProductDuration = new Trend('create_product_duration');
const updateProductDuration = new Trend('update_product_duration');
const deleteProductDuration = new Trend('delete_product_duration');

// Test configuration - use rampUp profile by default
export const options: Options = {
  stages: loadProfiles.rampUp.stages,
  thresholds: config.thresholds,
  // Batch multiple HTTP requests together for better performance
  batch: 10,
  // Don't throw errors on failed HTTP requests
  discardResponseBodies: false,
};

// Store created product IDs for use in update/delete operations
const createdProductIDs: string[] = [];

// Main test function - executed by each virtual user repeatedly
export default function (): void {
  // Determine which operation to perform based on weighted distribution
  const rand = Math.random() * 100;

  if (rand < config.operationWeights.list) {
    // LIST operation (50%)
    listProducts();
  } else if (rand < config.operationWeights.list + config.operationWeights.get) {
    // GET operation (25%)
    getProduct();
  } else if (rand < config.operationWeights.list + config.operationWeights.get + config.operationWeights.create) {
    // CREATE operation (15%)
    createProduct();
  } else if (rand < 100 - config.operationWeights.delete) {
    // UPDATE operation (7%)
    updateProduct();
  } else {
    // DELETE operation (3%)
    deleteProduct();
  }

  // Think time - simulate realistic user behavior (0.5-2 seconds between requests)
  sleep(Math.random() * 1.5 + 0.5);
}

function listProducts(): void {
  const page = getRandomPage();
  const pageSize = config.testData.pageSize;
  const url = getURL(`/products?page=${page}&pageSize=${pageSize}`);

  const response = http.get(url, {
    headers,
    tags: { endpoint: 'list_products' },
  });

  const success = check(response, {
    'list: status is 200': (r) => r.status === 200,
    'list: has products array': (r) => {
      try {
        const body = JSON.parse(r.body as string) as ProductListResponse;
        return Array.isArray(body.data?.products || body.products);
      } catch (e) {
        return false;
      }
    },
  });

  listProductsRate.add(success ? 1 : 0);
  listProductsDuration.add(response.timings.duration);
}

function getProduct(): void {
  // Try to get a product we created, otherwise use a random ID
  let productID: string;

  if (createdProductIDs.length > 0) {
    // Pick a random product ID from our created products
    productID = createdProductIDs[Math.floor(Math.random() * createdProductIDs.length)];
  } else {
    // Fallback to seeded product IDs from database migration
    productID = getSeededProductID();
  }

  const url = getURL(`/products/${productID}`);

  const response = http.get(url, {
    headers,
    tags: { endpoint: 'get_product' },
  });

  const success = check(response, {
    'get: status is 200 or 404': (r) => r.status === 200 || r.status === 404,
    'get: has valid response': (r) => {
      if (r.status === 404) return true;
      try {
        const body = JSON.parse(r.body as string) as ProductResponse;
        // go-bricks wraps response in data object
        const product = body.data || body;
        return !!(product.id && product.name && product.price !== undefined);
      } catch (e) {
        return false;
      }
    },
  });

  getProductRate.add(success ? 1 : 0);
  getProductDuration.add(response.timings.duration);
}

function createProduct(): void {
  const product = getRandomProduct();
  const url = getURL('/products');

  // Add some randomness to make each product unique
  const uniqueProduct: CreateProductInput = {
    name: `${product.name} ${Date.now()}`,
    description: product.description,
    price: product.price + (Math.random() * 10),
    imageURL: `https://example.com/products/${Date.now()}.jpg`,
  };

  const response = http.post(url, JSON.stringify(uniqueProduct), {
    headers,
    tags: { endpoint: 'create_product' },
  });

  const success = check(response, {
    'create: status is 201': (r) => r.status === 201,
    'create: returns product with ID': (r) => {
      try {
        const body = JSON.parse(r.body as string) as ProductResponse;
        // go-bricks wraps response in data object
        const product = body.data || body;
        if (product.id) {
          // Store the created product ID for later use
          createdProductIDs.push(product.id);
          // Limit array size to prevent memory issues
          if (createdProductIDs.length > 100) {
            createdProductIDs.shift();
          }
          return true;
        }
        return false;
      } catch (e) {
        return false;
      }
    },
  });

  createProductRate.add(success ? 1 : 0);
  createProductDuration.add(response.timings.duration);
}

function updateProduct(): void {
  // Only try to update if we have created products
  if (createdProductIDs.length === 0) {
    // Fallback to seeded product IDs from database migration
    const productID = getSeededProductID();
    performUpdate(productID);
    return;
  }

  const productID = createdProductIDs[Math.floor(Math.random() * createdProductIDs.length)];
  performUpdate(productID);
}

function performUpdate(productID: string): void {
  const url = getURL(`/products/${productID}`);

  const updates: UpdateProductInput = {
    name: `Updated Product ${Date.now()}`,
    price: Math.random() * 200 + 10,
    description: 'Updated during load test',
  };

  const response = http.put(url, JSON.stringify(updates), {
    headers,
    tags: { endpoint: 'update_product' },
  });

  const success = check(response, {
    'update: status is 200 or 404': (r) => r.status === 200 || r.status === 404,
    'update: returns updated product': (r) => {
      if (r.status === 404) return true;
      try {
        const body = JSON.parse(r.body as string) as ProductResponse;
        // go-bricks wraps response in data object
        const product = body.data || body;
        return product.id === productID;
      } catch (e) {
        return false;
      }
    },
  });

  updateProductRate.add(success ? 1 : 0);
  updateProductDuration.add(response.timings.duration);
}

function deleteProduct(): void {
  // Only delete if we have many created products (keep some for other operations)
  if (createdProductIDs.length < 10) {
    // Not enough products, create one instead
    createProduct();
    return;
  }

  // Remove and delete the oldest created product
  const productID = createdProductIDs.shift();
  if (!productID) return;

  const url = getURL(`/products/${productID}`);

  const response = http.del(url, null, {
    headers,
    tags: { endpoint: 'delete_product' },
  });

  const success = check(response, {
    'delete: status is 204 or 404': (r) => r.status === 204 || r.status === 404,
  });

  deleteProductRate.add(success ? 1 : 0);
  deleteProductDuration.add(response.timings.duration);
}

// Setup function - runs once before the test starts
export function setup(): void {
  console.log('🚀 Starting CRUD Mix Load Test');
  console.log(`📊 Target: ${config.baseURL}${config.apiPrefix}`);
  console.log('📈 Operation Distribution:');
  console.log(`   - List:   ${config.operationWeights.list}%`);
  console.log(`   - Get:    ${config.operationWeights.get}%`);
  console.log(`   - Create: ${config.operationWeights.create}%`);
  console.log(`   - Update: ${config.operationWeights.update}%`);
  console.log(`   - Delete: ${config.operationWeights.delete}%`);
  console.log('');

  // Verify the API is accessible
  const healthURL = `${config.baseURL}${config.apiPrefix}/health`;
  const response = http.get(healthURL);

  if (response.status !== 200) {
    console.error('❌ Health check failed! Is the API running?');
    console.error(`   URL: ${healthURL}`);
    console.error(`   Status: ${response.status}`);
    throw new Error('API health check failed');
  }

  console.log('✅ Health check passed');
  console.log('');
}

// Teardown function - runs once after the test completes
export function teardown(): void {
  console.log('');
  console.log('✅ Load test completed');
  console.log(`📦 Created ${createdProductIDs.length} products during test`);
}
