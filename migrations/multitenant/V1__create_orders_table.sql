-- V1: orders table for the multi-tenant migration demo.
--
-- This migration runs once per tenant against the tenant's own schema. The
-- target schema is resolved via the session search_path set in
-- etc/docker/postgres/multitenant-init.sql — there is no need to qualify
-- table names with a schema prefix here.

CREATE TABLE orders (
    id         BIGSERIAL PRIMARY KEY,
    customer   TEXT      NOT NULL,
    total      NUMERIC(12, 2) NOT NULL CHECK (total >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX orders_customer_idx ON orders (customer);
