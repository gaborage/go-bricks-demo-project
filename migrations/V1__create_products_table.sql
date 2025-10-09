-- V1: Create products table
-- Flyway migration for products table

CREATE TABLE IF NOT EXISTS products (
    id UUID PRIMARY KEY,
    name VARCHAR(150) NOT NULL,
    description TEXT,
    price DECIMAL(10, 2) NOT NULL CHECK (price >= 0),
    image_url VARCHAR(500),
    created_date TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_date TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Create indexes for better performance
CREATE INDEX IF NOT EXISTS idx_products_created_date ON products(created_date DESC);
CREATE INDEX IF NOT EXISTS idx_products_name ON products(name);

-- Insert sample product data
INSERT INTO products (id, name, description, price, image_url, created_date, updated_date) VALUES
    (
        '550e8400-e29b-41d4-a716-446655440001',
        'Laptop Pro 15',
        'High-performance laptop with 15-inch display, 16GB RAM, and 512GB SSD',
        1299.99,
        'https://images.unsplash.com/photo-1496181133206-80ce9b88a853',
        CURRENT_TIMESTAMP,
        CURRENT_TIMESTAMP
    ),
    (
        '550e8400-e29b-41d4-a716-446655440002',
        'Wireless Mouse',
        'Ergonomic wireless mouse with precision tracking and long battery life',
        29.99,
        'https://images.unsplash.com/photo-1527814050087-3793815479db',
        CURRENT_TIMESTAMP,
        CURRENT_TIMESTAMP
    ),
    (
        '550e8400-e29b-41d4-a716-446655440003',
        'Mechanical Keyboard',
        'RGB backlit mechanical keyboard with Cherry MX switches',
        149.99,
        'https://images.unsplash.com/photo-1511467687858-23d96c32e4ae',
        CURRENT_TIMESTAMP,
        CURRENT_TIMESTAMP
    ),
    (
        '550e8400-e29b-41d4-a716-446655440004',
        '4K Monitor 27"',
        'Ultra HD 4K monitor with HDR support and 144Hz refresh rate',
        599.99,
        'https://images.unsplash.com/photo-1527443224154-c4a3942d3acf',
        CURRENT_TIMESTAMP,
        CURRENT_TIMESTAMP
    ),
    (
        '550e8400-e29b-41d4-a716-446655440005',
        'USB-C Hub',
        'Multi-port USB-C hub with HDMI, USB 3.0, and SD card reader',
        49.99,
        'https://images.unsplash.com/photo-1625948515291-69613efd103f',
        CURRENT_TIMESTAMP,
        CURRENT_TIMESTAMP
    ),
    (
        '550e8400-e29b-41d4-a716-446655440006',
        'Noise Cancelling Headphones',
        'Premium wireless headphones with active noise cancellation',
        349.99,
        'https://images.unsplash.com/photo-1545127398-14699f92334b',
        CURRENT_TIMESTAMP,
        CURRENT_TIMESTAMP
    )
ON CONFLICT (id) DO NOTHING;
