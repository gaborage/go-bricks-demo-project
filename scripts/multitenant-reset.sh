#!/usr/bin/env bash
# scripts/multitenant-reset.sh
#
# Drops and recreates every tenant's schema in the demo postgres container.
# Useful between demo runs or when experimenting with broken migrations.
#
# The per-tenant role and search_path stay intact — only the schema contents
# (tables + flyway_schema_history) are wiped.

set -euo pipefail

POSTGRES_CONTAINER="${POSTGRES_CONTAINER:-go-bricks-postgres}"

# Tenant list source of truth: config.multitenant.yaml — keep this array in
# sync with the multitenant.tenants block there (and with the tenant_roles
# array in etc/docker/postgres/multitenant-init.sql).
TENANTS=(acme globex initech)

if ! docker ps --format '{{.Names}}' | grep -qx "$POSTGRES_CONTAINER"; then
    echo "multitenant-reset: container '$POSTGRES_CONTAINER' is not running. Run 'make docker-up'." >&2
    exit 1
fi

# One psql round-trip per tenant so a failure on tenant N surfaces the
# offending tenant directly in the error, instead of bundling six
# statements into one invocation.
for tenant in "${TENANTS[@]}"; do
    docker exec -i "$POSTGRES_CONTAINER" psql -U postgres -d postgres -v ON_ERROR_STOP=1 >/dev/null <<SQL
DROP SCHEMA IF EXISTS ${tenant} CASCADE;
CREATE SCHEMA ${tenant} AUTHORIZATION ${tenant};
SQL
done
echo "✅ Reset schemas: ${TENANTS[*]}"
