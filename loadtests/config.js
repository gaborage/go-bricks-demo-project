// k6 Load Testing Configuration
// This file contains shared configuration for all load tests

export const config = {
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

// Helper function to construct full URL
export function getURL(path) {
  return `${config.baseURL}${config.apiPrefix}${path}`;
}

// Helper function to get random test product
export function getRandomProduct() {
  const products = config.testData.sampleProducts;
  return products[Math.floor(Math.random() * products.length)];
}

// Helper function to get random page number
export function getRandomPage() {
  return Math.floor(Math.random() * config.testData.maxPages) + 1;
}

// Common request headers
export const headers = {
  'Content-Type': 'application/json',
  'Accept': 'application/json',
};

// Load profile configurations for different test scenarios
export const loadProfiles = {
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

// Export common checks
export function createChecks() {
  return {
    'status is 200': (r) => r.status === 200,
    'status is 201': (r) => r.status === 201,
    'status is 204': (r) => r.status === 204,
    'response time < 1s': (r) => r.timings.duration < 1000,
    'response has body': (r) => r.body && r.body.length > 0,
  };
}
