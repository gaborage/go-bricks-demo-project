-- V2: Seed additional products for load testing
-- This migration adds more sample data to make load testing more realistic
-- Creates 100 additional products to test pagination and query performance

-- Generate 100 test products with realistic data
INSERT INTO products (id, name, description, price, image_url, created_date, updated_date)
SELECT
    gen_random_uuid() AS id,
    'Product ' || generate_series AS name,
    'Load test product ' || generate_series || ' - This is a sample product created for load testing purposes. It includes a description to simulate realistic data volumes.' AS description,
    ROUND((RANDOM() * 1000 + 10)::numeric, 2) AS price,
    'https://images.unsplash.com/photo-' || (1500000000000 + (generate_series * 1000))::text AS image_url,
    CURRENT_TIMESTAMP - (RANDOM() * INTERVAL '30 days') AS created_date,
    CURRENT_TIMESTAMP - (RANDOM() * INTERVAL '15 days') AS updated_date
FROM generate_series(1, 100);

-- Add additional indexes for load testing scenarios
CREATE INDEX IF NOT EXISTS idx_products_price ON products(price);
CREATE INDEX IF NOT EXISTS idx_products_updated_date ON products(updated_date DESC);

-- Analyze table to update statistics for query planner
ANALYZE products;

-- Display summary
DO $$
DECLARE
    product_count INTEGER;
BEGIN
    SELECT COUNT(*) INTO product_count FROM products;
    RAISE NOTICE 'Load test data seeded successfully';
    RAISE NOTICE 'Total products in database: %', product_count;
END $$;
