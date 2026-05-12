#!/usr/bin/env bash
# scripts/capture-flyway-samples.sh
#
# Drives the Flyway CLI directly (NOT through go-bricks-migrate) with
# -outputType=json to capture structured output for each of the 7 scenarios
# documented in go-bricks-demo-project#32 / go-bricks#376. The captures feed
# the upstream Runner-contract refactor whose JSON-output parser needs real
# fixtures to design against.
#
# Why bypass go-bricks-migrate here?
#   go-bricks-migrate streams its own NDJSON progress envelope and redacts
#   Flyway stdout (migration/flyway.go:200). For parser-design fixtures we
#   need the raw Flyway JSON shape verbatim, so we shell out to flyway
#   ourselves.
#
# All scenarios target the `acme` tenant. Globex and initech are untouched.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
SAMPLES_DIR="$PROJECT_ROOT/samples/flyway-output"

# Repo-relative paths so they resolve the same on host (for trap cleanup of
# extra migration files) and inside the Flyway container (mounted at /work).
MIGRATIONS_REL="migrations/multitenant"
FLYWAY_CONF_REL="flyway/flyway-multitenant.conf"
MIGRATIONS_DIR="$PROJECT_ROOT/$MIGRATIONS_REL"
V1_PATH="$MIGRATIONS_DIR/V1__create_orders_table.sql"
EXTRA_V3="$MIGRATIONS_DIR/V3__add_orders_notes_column.sql"
EXTRA_V4_BROKEN="$MIGRATIONS_DIR/V4__intentionally_broken.sql"

FLYWAY_IMAGE="${FLYWAY_IMAGE:-flyway/flyway:11-alpine}"
POSTGRES_CONTAINER="${POSTGRES_CONTAINER:-go-bricks-postgres}"

TENANT_ROLE=acme
TENANT_PASSWORD=acme_pw
TENANT_SCHEMA=acme
PG_HOST="${PG_HOST:-postgres}"
PG_PORT="${PG_PORT:-5432}"
PG_DB="${PG_DB:-postgres}"

mkdir -p "$SAMPLES_DIR"

NETWORK="$(docker inspect -f '{{range $net,$_ := .NetworkSettings.Networks}}{{$net}} {{end}}' "$POSTGRES_CONTAINER" 2>/dev/null | awk '{print $1}')"
if [[ -z "$NETWORK" ]]; then
    echo "capture: postgres container '$POSTGRES_CONTAINER' not running. Run 'make docker-up'." >&2
    exit 1
fi

# --- helpers --------------------------------------------------------------

flyway() {
    # Pass through to the same Docker image the wrapper uses so version drift
    # between samples and the rest of the demo is impossible. Paths inside
    # the container are rooted at /work (the bind-mount target).
    docker run --rm \
        --network="$NETWORK" \
        -v "$PROJECT_ROOT":/work:ro \
        -w /work \
        -e DB_HOST="$PG_HOST" \
        -e DB_PORT="$PG_PORT" \
        -e DB_USER="$TENANT_ROLE" \
        -e DB_PASSWORD="$TENANT_PASSWORD" \
        -e DB_NAME="$PG_DB" \
        "$FLYWAY_IMAGE" \
        "-configFiles=/work/$FLYWAY_CONF_REL" \
        "-locations=filesystem:/work/$MIGRATIONS_REL" \
        -outputType=json \
        "$@"
}

reset_tenant_schema() {
    docker exec -i "$POSTGRES_CONTAINER" psql -U postgres -d postgres -v ON_ERROR_STOP=1 >/dev/null <<SQL
DROP SCHEMA IF EXISTS $TENANT_SCHEMA CASCADE;
CREATE SCHEMA $TENANT_SCHEMA AUTHORIZATION $TENANT_ROLE;
SQL
}

write_extra_migration() {
    local path="$1"
    local contents="$2"
    cat >"$path" <<EOF
$contents
EOF
}

# --- temp files we add/restore -------------------------------------------
V1_BACKUP="$(mktemp)"
cp "$V1_PATH" "$V1_BACKUP"

cleanup() {
    rm -f "$EXTRA_V3" "$EXTRA_V4_BROKEN"
    cp "$V1_BACKUP" "$V1_PATH"
    rm -f "$V1_BACKUP"
}
trap cleanup EXIT

# --- scenarios ------------------------------------------------------------

echo "[1/7] migrate-fresh: drop schema, run migrate"
reset_tenant_schema
flyway migrate | tee "$SAMPLES_DIR/migrate-fresh.json" >/dev/null

echo "[2/7] migrate-noop: re-run migrate against latest"
flyway migrate | tee "$SAMPLES_DIR/migrate-noop.json" >/dev/null

echo "[3/7] migrate-incremental: add V3, re-run migrate"
write_extra_migration "$EXTRA_V3" "ALTER TABLE orders ADD COLUMN notes TEXT;"
flyway migrate | tee "$SAMPLES_DIR/migrate-incremental.json" >/dev/null

# Keep V3 on disk while we capture migrate-failed — otherwise Flyway raises a
# VALIDATE_ERROR for the orphan history row before even reaching V4, and the
# sample would show a validation failure rather than the bad-SQL runtime
# failure the spike fixtures need.
echo "[4/7] migrate-failed: add broken V4, run migrate (expected failure)"
write_extra_migration "$EXTRA_V4_BROKEN" "ALTER TABLE orders DROP COLUMN does_not_exist;"
# Flyway -outputType=json emits structured output and may exit 0 even when
# migration fails; the failure detail lives inside the JSON envelope.
flyway migrate >"$SAMPLES_DIR/migrate-failed.json" 2>&1 || true
rm -f "$EXTRA_V3" "$EXTRA_V4_BROKEN"

# Repair so subsequent runs see a clean V1+V2 history (Flyway records the
# failed V4 plus the now-orphaned V3 in flyway_schema_history; repair clears
# both so info/validate samples reflect a healthy fleet).
flyway repair >/dev/null

echo "[5/7] info: run info against current state"
flyway info | tee "$SAMPLES_DIR/info.json" >/dev/null

echo "[6/7] validate-clean: validate against a clean fresh schema"
reset_tenant_schema
flyway migrate >/dev/null
flyway validate | tee "$SAMPLES_DIR/validate-clean.json" >/dev/null

echo "[7/7] validate-checksum-mismatch: modify V1, run validate (expected failure)"
# Append a no-op comment so the on-disk checksum drifts from the applied one.
# Flyway with -outputType=json exits 0 even when validation fails — the
# failure is reported inside the JSON envelope (validationSuccessful=false).
echo "-- intentional checksum drift for the validate-checksum-mismatch sample" >>"$V1_PATH"
flyway validate >"$SAMPLES_DIR/validate-checksum-mismatch.json" 2>&1 || true

echo
echo "Captured 7 samples in $SAMPLES_DIR"
ls -la "$SAMPLES_DIR" | grep -E '\.json$' || true
