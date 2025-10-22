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
