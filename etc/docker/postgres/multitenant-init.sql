-- Multi-tenant schema-per-tenant bootstrap for the go-bricks-migrate demo.
--
-- This script is idempotent so re-running it via `make migrate-multitenant-init`
-- against a long-lived postgres container is safe.
--
-- For each demo tenant we create:
--   * a dedicated role with a password (used by go-bricks-migrate as the
--     per-tenant credential the Flyway subprocess connects with)
--   * a schema owned by that role
--   * a search_path on the role so Flyway and the application both default to
--     the tenant's schema without needing flyway.schemas to be set in the conf
--
-- The search_path mechanism is what makes schema-per-tenant work without a
-- framework patch: go-bricks (v0.31.0) only propagates DB_HOST/PORT/USER/
-- PASSWORD/NAME to the Flyway subprocess, so we route via role identity.

-- Tenant list source of truth: config.multitenant.yaml — keep this array in
-- sync with the multitenant.tenants block there (and with the TENANTS array
-- in scripts/multitenant-reset.sh).
DO $$
DECLARE
    tenant TEXT;
    tenant_roles CONSTANT TEXT[] := ARRAY['acme', 'globex', 'initech'];
BEGIN
    FOREACH tenant IN ARRAY tenant_roles LOOP
        -- Role. Password suffix "_pass" (not "_pw") keeps every derived password
        -- >= 8 bytes, satisfying go-bricks v0.49.0's minimum-length rule — the
        -- old "acme_pw" was 7 bytes and tripped ErrDatabasePasswordTooShort.
        -- Must stay in lockstep with the passwords in config.multitenant.yaml.
        IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = tenant) THEN
            EXECUTE format('CREATE ROLE %I LOGIN PASSWORD %L', tenant, tenant || '_pass');
        END IF;

        -- Schema (owned by the tenant role so DDL inside migrations doesn't
        -- need extra GRANTs)
        EXECUTE format('CREATE SCHEMA IF NOT EXISTS %I AUTHORIZATION %I', tenant, tenant);

        -- search_path: Flyway resolves the default schema via the session's
        -- current_schema(), which respects search_path. This is how
        -- flyway_schema_history ends up isolated per tenant.
        EXECUTE format('ALTER ROLE %I SET search_path TO %I', tenant, tenant);

        -- Allow the tenant role to connect to the shared database
        EXECUTE format('GRANT CONNECT ON DATABASE postgres TO %I', tenant);
    END LOOP;
END
$$;
