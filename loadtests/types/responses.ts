// Type definitions for API responses

/**
 * Product entity as returned by the API
 */
export interface Product {
  id: string;
  name: string;
  description: string;
  price: number;
  imageURL?: string;
  createdDate: string;
  updatedDate: string;
}

/**
 * Single product response from the API
 * go-bricks wraps responses in a data object, but sometimes returns directly
 */
export interface ProductResponse {
  data?: Product;
  // Fallback for direct product response (no wrapper)
  id?: string;
  name?: string;
  description?: string;
  price?: number;
  imageURL?: string;
  createdDate?: string;
  updatedDate?: string;
  [key: string]: any;
}

/**
 * Product list response from the API
 * go-bricks wraps paginated responses in a data object
 */
export interface ProductListResponse {
  // Wrapped response (go-bricks pattern)
  data?: {
    products: Product[];
    page: number;
    pageSize: number;
    total: number;
  };
  // Direct response (fallback)
  products?: Product[];
  page?: number;
  pageSize?: number;
  total?: number;
  [key: string]: any;
}

/**
 * Product creation input
 */
export interface CreateProductInput {
  name: string;
  description: string;
  price: number;
  imageURL?: string;
}

/**
 * Product update input
 */
export interface UpdateProductInput {
  name?: string;
  description?: string;
  price?: number;
  imageURL?: string;
}

/**
 * Consumer counters in the messaging_stats block of a GET /api/v1/ready 200.
 * The four consumer keys arrived in go-bricks v0.65.0 (#1684/#1686); the
 * block carries publisher counters too, which the smoke does not read.
 */
export interface MessagingReadyStats {
  status?: string;
  declared_consumers?: number;
  subscribed_consumers?: number;
  consumer_max_fail_streak?: number;
  consumer_resubscribes?: number;
  [key: string]: any;
}

/**
 * GET /api/v1/ready body. A 200 renders every kind's status plus its
 * <kind>_stats; a 503 renders only {status, <blocking kind>, error}.
 * Unwrapped: /ready is not an APIResponse route.
 */
export interface ReadyResponse {
  status: string;
  messaging?: string;
  messaging_stats?: MessagingReadyStats;
  [key: string]: any;
}

/**
 * Tokenization result returned by the tokens module.
 * Mirrors internal/modules/tokens/domain.Token.
 */
export interface Token {
  token: string;
  masked_pan: string;
  network: string;
  last4: string;
  expires_at: string;
}

/**
 * Plaintext response from POST /api/v1/tokens/relay.
 * The relay handler unwraps the partner's JOSE response and re-emits it as
 * a standard go-bricks APIResponse envelope.
 */
export interface RelayResponse {
  data?: {
    token: Token;
  };
  // Fallback for direct shape if the envelope is ever bypassed
  token?: Token;
  [key: string]: any;
}
