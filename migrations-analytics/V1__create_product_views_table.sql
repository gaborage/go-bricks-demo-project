-- V1: Create product_views table for analytics
-- Tracks individual product view events in the analytics database
-- This demonstrates the go-bricks named databases feature

CREATE TABLE IF NOT EXISTS product_views (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    product_id UUID NOT NULL,
    viewed_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    user_agent VARCHAR(500),
    ip_address VARCHAR(45),  -- Supports IPv6
    session_id VARCHAR(100),
    referrer VARCHAR(500)
);

-- Index for efficient queries by product
CREATE INDEX IF NOT EXISTS idx_product_views_product_id ON product_views(product_id);

-- Index for time-based queries (e.g., views in last hour/day)
CREATE INDEX IF NOT EXISTS idx_product_views_viewed_at ON product_views(viewed_at DESC);

-- Composite index for product + time range queries
CREATE INDEX IF NOT EXISTS idx_product_views_product_time ON product_views(product_id, viewed_at DESC);
