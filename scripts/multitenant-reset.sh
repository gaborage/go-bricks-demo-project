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
TENANTS=(acme globex initech)

if ! docker ps --format '{{.Names}}' | grep -qx "$POSTGRES_CONTAINER"; then
    echo "multitenant-reset: container '$POSTGRES_CONTAINER' is not running. Run 'make docker-up'." >&2
    exit 1
fi

SQL=""
for tenant in "${TENANTS[@]}"; do
    SQL+="DROP SCHEMA IF EXISTS ${tenant} CASCADE;"
    SQL+="CREATE SCHEMA ${tenant} AUTHORIZATION ${tenant};"
done

docker exec -i "$POSTGRES_CONTAINER" psql -U postgres -d postgres -v ON_ERROR_STOP=1 -c "$SQL" >/dev/null
echo "✅ Reset schemas: ${TENANTS[*]}"
