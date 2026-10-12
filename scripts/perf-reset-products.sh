#!/usr/bin/env bash
# scripts/perf-reset-products.sh
#
# Puts the products table back into a known state between perf runs: the rows
# the V1/V2 migrations created, plus N deterministic seed rows, and nothing
# else. A perf comparison then reads the same data in every session and can
# gate on one exact count: <rows the migrations created> + N.
#
# The script only generates SQL. It never sees a password: --print-sql writes
# the psql script to stdout for a runner that has its own connection (for
# example a Kubernetes Job with the database secret in its environment), and
# --local pipes the same script into psql inside the docker-compose postgres
# container, which connects over its local socket.
#
# What the SQL does, in one transaction:
#   1. Refuses to run when the watermark would delete rows it must keep (see
#      "Guards" below).
#   2. Deletes every product whose created_date is later than the watermark:
#      everything a load test created after the seed.
#   3. Inserts the seed rows g = 1..N that are missing, ON CONFLICT DO NOTHING:
#        id           = md5('perf-seed-' || g)::uuid
#        name         = 'Seed Product ' || g
#        created_date = 2025-01-01T00:00:00Z + g minutes (updated_date the same)
#      and a description, price and image URL that also depend only on g.
#      loadtests/config.ts (seedProductID) derives the same ids, so a k6 run
#      with PERF_SEED_ROWS=N reads only rows that exist.
#      With --drop-seed it deletes the seed rows g = 1..N instead.
#   4. With --clear-outbox, deletes the outbox rows already published. Pending
#      and failed rows stay: they are still owed to the broker.
# Then, outside the transaction, a best-effort ANALYZE products (a role that
# may not analyze the table is skipped silently; last_analyze shows whether it
# ran), and one line of counts:
#   COUNT products=<n> seed=<n> outbox_pending=<n> last_analyze=<ts|null>
# seed counts the rows with a seed id for g = 1..N; last_analyze is the later
# of last_analyze and last_autoanalyze, in UTC.
#
# Re-running with the same arguments changes nothing and prints the same
# counts (apart from last_analyze).
#
# Guards (the SQL raises an error, and nothing is deleted):
#   - The last seed row (2025-01-01 + N minutes) must be older than the
#     watermark. Otherwise every reset would delete seed rows and insert them
#     again.
#   - No row from the migrations may be newer than the watermark: neither the
#     six fixed-id V1 rows nor a V2 'Product <n>' row. Both are stamped with the
#     time the migrations ran, so a watermark from before that time (say a
#     fixed date chosen before the database was migrated) would delete them and
#     the count gate could never pass again. Take the watermark after the
#     migrations and after the first seed.
#
# Why load tests cannot break the count: loadtests/products-crud.ts deletes
# only rows its own VU created, updates fall back to the six V1 rows (their
# content changes, their count and created_date do not), and seed ids are only
# ever read. Every row a test creates is stamped with the current time, so the
# next reset deletes it.
#
# Usage:
#   scripts/perf-reset-products.sh --print-sql --rows N --watermark TS [--clear-outbox] [--drop-seed]
#   scripts/perf-reset-products.sh --local     --rows N --watermark TS [--clear-outbox] [--drop-seed]
#
#   --print-sql     write the psql script to stdout (run it with psql -X)
#   --local         run it in the docker-compose postgres (make docker-up && make migrate)
#   --rows N        seed rows, g = 1..N
#   --watermark TS  ISO-8601 with an explicit zone, e.g. 2026-10-01T00:00:00Z
#   --clear-outbox  also delete published gobricks_outbox rows
#   --drop-seed     delete the seed rows instead of inserting them
#
# Environment (--local only):
#   POSTGRES_CONTAINER  default go-bricks-postgres
#   POSTGRES_DB         default postgres
#   POSTGRES_USER       default postgres
#
# Exit codes: 0 done, 1 error (including a guard), 2 usage.
#
# Example, a local first seed:
#   make docker-up && make migrate
#   scripts/perf-reset-products.sh --local --rows 250000 --watermark "$(date -u +%Y-%m-%dT%H:%M:%SZ)"

set -euo pipefail

POSTGRES_CONTAINER="${POSTGRES_CONTAINER:-go-bricks-postgres}"
POSTGRES_DB="${POSTGRES_DB:-postgres}"
POSTGRES_USER="${POSTGRES_USER:-postgres}"

# ISO-8601 date and time with an explicit zone; nothing else may reach the SQL.
WATERMARK_RE='^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}(:[0-9]{2}(\.[0-9]{1,6})?)?(Z|[+-][0-9]{2}(:?[0-9]{2})?)$'
COUNT_RE='^COUNT products=[0-9]+ seed=[0-9]+ outbox_pending=[0-9]+ last_analyze=([0-9TZ:-]+|null)$'

usage() {
    sed -n '/^# Usage:/,/^# Exit codes:/p' "$0" | sed 's/^# \{0,1\}//'
}

usage_error() {
    echo "perf-reset-products: $*" >&2
    echo "Run with --help for usage." >&2
    exit 2
}

mode=""
rows=""
watermark=""
clear_outbox=0
drop_seed=0

set_mode() {
    [[ -z "$mode" ]] || usage_error "--print-sql and --local are exclusive"
    mode=$1
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --print-sql) set_mode print ;;
        --local) set_mode local ;;
        --rows)
            [[ $# -ge 2 ]] || usage_error "--rows needs a value"
            rows=$2
            shift
            ;;
        --watermark)
            [[ $# -ge 2 ]] || usage_error "--watermark needs a value"
            watermark=$2
            shift
            ;;
        --clear-outbox) clear_outbox=1 ;;
        --drop-seed) drop_seed=1 ;;
        -h | --help)
            usage
            exit 0
            ;;
        *) usage_error "unknown argument: $1" ;;
    esac
    shift
done

[[ -n "$mode" ]] || usage_error "one of --print-sql or --local is required"
[[ "$rows" =~ ^[1-9][0-9]{0,8}$ ]] || usage_error "--rows must be a positive integer, got '$rows'"
[[ "$watermark" =~ $WATERMARK_RE ]] ||
    usage_error "--watermark must be ISO-8601 with a zone (e.g. 2026-10-01T00:00:00Z), got '$watermark'"

# The psql script. $rows and $watermark are validated above, and both reach SQL
# only through psql variables (:rows, :'watermark').
emit_sql() {
    cat <<SQL
-- Generated by scripts/perf-reset-products.sh: rows=$rows watermark=$watermark clear_outbox=$clear_outbox drop_seed=$drop_seed
-- Run with psql -X. The script sets its own output options and prints one line.
\\set QUIET on
\\set ON_ERROR_STOP on
\\pset format unaligned
\\pset tuples_only on
\\set rows $rows
\\set watermark '$watermark'
SQL

    cat <<'SQL'
SET client_min_messages = error;
SET lock_timeout = '30s';
SET perf_reset.rows = :'rows';
SET perf_reset.watermark = :'watermark';

BEGIN;

DO $guard$
DECLARE
    wm        timestamptz := current_setting('perf_reset.watermark')::timestamptz;
    last_seed timestamptz := timestamptz '2025-01-01 00:00:00+00'
                             + current_setting('perf_reset.rows')::bigint * interval '1 minute';
BEGIN
SQL

    if [[ $drop_seed -eq 0 ]]; then
        cat <<'SQL'
    IF last_seed >= wm THEN
        RAISE EXCEPTION 'watermark % is not later than the last seed row (%): pick a later watermark or fewer rows', wm, last_seed;
    END IF;
SQL
    fi

    cat <<'SQL'
    IF EXISTS (
        SELECT 1 FROM products
        WHERE created_date > wm
          AND (id IN ('550e8400-e29b-41d4-a716-446655440001', '550e8400-e29b-41d4-a716-446655440002',
                      '550e8400-e29b-41d4-a716-446655440003', '550e8400-e29b-41d4-a716-446655440004',
                      '550e8400-e29b-41d4-a716-446655440005', '550e8400-e29b-41d4-a716-446655440006')
               OR name ~ '^Product [0-9]+$')
    ) THEN
        RAISE EXCEPTION 'rows created by the V1/V2 migrations are newer than watermark %: the reset would delete them; take the watermark after the migrations ran', wm;
    END IF;
END
$guard$;

-- Everything a load test created after the seed.
DELETE FROM products WHERE created_date > :'watermark'::timestamptz;
SQL

    if [[ $drop_seed -eq 1 ]]; then
        cat <<'SQL'

DELETE FROM products p
USING generate_series(1, :rows) AS g
WHERE p.id = md5('perf-seed-' || g)::uuid;
SQL
    else
        cat <<'SQL'

INSERT INTO products (id, name, description, price, image_url, created_date, updated_date)
SELECT md5('perf-seed-' || g)::uuid,
       'Seed Product ' || g,
       'Deterministic perf seed row ' || g,
       round((g % 99900 + 100) / 100.0, 2),
       'https://example.com/perf-seed/' || g || '.jpg',
       timestamptz '2025-01-01 00:00:00+00' + g * interval '1 minute',
       timestamptz '2025-01-01 00:00:00+00' + g * interval '1 minute'
FROM generate_series(1, :rows) AS g
ON CONFLICT (id) DO NOTHING;
SQL
    fi

    if [[ $clear_outbox -eq 1 ]]; then
        cat <<'SQL'

DELETE FROM gobricks_outbox WHERE status = 'published';
SQL
    fi

    cat <<'SQL'

COMMIT;

-- Best-effort: a role that may not analyze the table gets a (suppressed)
-- warning, and a lock held elsewhere skips it; autovacuum analyzes later.
DO $analyze$
BEGIN
    ANALYZE products;
EXCEPTION WHEN insufficient_privilege OR lock_not_available THEN
    NULL;
END
$analyze$;

SELECT format('COUNT products=%s seed=%s outbox_pending=%s last_analyze=%s',
    (SELECT count(*) FROM products),
    (SELECT count(*) FROM products p
       JOIN generate_series(1, :rows) AS g ON p.id = md5('perf-seed-' || g)::uuid),
    (SELECT count(*) FROM gobricks_outbox WHERE status = 'pending'),
    coalesce((SELECT to_char(greatest(last_analyze, last_autoanalyze) AT TIME ZONE 'UTC',
                             'YYYY-MM-DD"T"HH24:MI:SS"Z"')
              FROM pg_stat_user_tables WHERE relid = 'products'::regclass), 'null'));
SQL
}

if [[ "$mode" == print ]]; then
    emit_sql
    exit 0
fi

if [[ "$(docker inspect -f '{{.State.Running}}' "$POSTGRES_CONTAINER" 2>/dev/null)" != true ]]; then
    echo "perf-reset-products: container '$POSTGRES_CONTAINER' is not running. Run 'make docker-up && make migrate'." >&2
    exit 1
fi

if ! out=$(emit_sql | docker exec -i "$POSTGRES_CONTAINER" psql -X -U "$POSTGRES_USER" -d "$POSTGRES_DB"); then
    echo "perf-reset-products: the reset failed; nothing was changed unless the error came after COMMIT." >&2
    exit 1
fi
if [[ ! "$out" =~ $COUNT_RE ]]; then
    echo "perf-reset-products: unexpected psql output (expected one COUNT line)." >&2
    exit 1
fi
echo "$out"
