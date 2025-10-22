// Type definitions for k6 load test configuration

/**
 * Main test configuration interface
 */
export interface TestConfig {
  baseURL: string;
  apiPrefix: string;
  thresholds: Record<string, string[]>;
  operationWeights: OperationWeights;
  testData: TestData;
}

/**
 * Operation weights for weighted random selection in CRUD tests
 */
export interface OperationWeights {
  list: number;
  get: number;
  create: number;
  update: number;
  delete: number;
}

/**
 * Test data configuration
 */
export interface TestData {
  maxPages: number;
  pageSize: number;
  sampleProducts: SampleProduct[];
}

/**
 * Sample product for creating test data
 */
export interface SampleProduct {
  name: string;
  description: string;
  price: number;
}

/**
 * Load profile with stages for ramping up/down virtual users
 */
export interface LoadProfile {
  stages: Stage[];
}

/**
 * Individual stage in a load profile
 */
export interface Stage {
  duration: string;
  target: number;
}

/**
 * HTTP headers type
 */
export interface Headers {
  'Content-Type': string;
  'Accept': string;
  [key: string]: string;
}
