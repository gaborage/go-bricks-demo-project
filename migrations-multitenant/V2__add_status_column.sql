-- V2: incremental change to demonstrate the migrate-incremental scenario.
--
-- Adds an order workflow status with a CHECK constraint. Applied per tenant
-- on top of V1.

ALTER TABLE orders
    ADD COLUMN status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'paid', 'shipped', 'cancelled'));

CREATE INDEX orders_status_idx ON orders (status);
