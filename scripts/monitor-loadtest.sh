#!/usr/bin/env bash
# scripts/monitor-loadtest.sh
#
# Samples the running app's goroutines, heap and database connections into a
# CSV while k6 drives load, so scripts/analyze-loadtest-results.sh can judge a
# run against loadtests/thresholds.yaml (peaks, leaks across the sustained
# test, recovery after the spike).
#
# Usage:
#   scripts/monitor-loadtest.sh <output.csv> [interval_seconds]   # default 10
#   make loadtest-monitor        # writes loadtest-results/metrics-<timestamp>.csv
# Stop it with Ctrl+C (or SIGTERM); the CSV is complete after every row.
#
# One row per interval:
#   timestamp,elapsed_s,phase,goroutines,heap_alloc_mb,rss_mb,db_active,db_idle,db_total
#
# Where each column comes from (a source that does not answer leaves its
# columns empty rather than stopping the monitor):
#   goroutines     GET $APP_URL/_sys/info  -> .data.goroutines
#   heap_alloc_mb  GET $APP_URL/_sys/gc    -> .data.mem_before (runtime Alloc;
#                  a GET only reads MemStats, it never forces a GC)
#   rss_mb         ps on the process listening on APP_URL's port (host process
#                  only; empty when the app runs in a container)
#   db_*           pg_stat_activity client backends on PG_DB, read with psql
#                  inside PG_CONTAINER (the host needs no psql)
#   phase          the first line of PHASE_FILE, written by
#                  scripts/run-loadtest-all-monitored.sh before each test;
#                  "manual" when the file does not exist
#
# The two /_sys endpoints are go-bricks debug endpoints: off by default,
# served at the URL root (not under /api/v1), and loopback-only. Start the app
# with them on:
#   DEBUG_ENABLED=true DEBUG_ALLOWEDIPS=127.0.0.1,::1 \
#   DEBUG_ENDPOINTS_INFO=true DEBUG_ENDPOINTS_GC=true make run
#
# Overrides (env): APP_URL (default http://localhost:8080), APP_PID,
#                  PG_CONTAINER (default go-bricks-postgres), PG_USER, PG_DB,
#                  PHASE_FILE (default <output.csv>.phase)
#
# Requires: curl, jq, docker (for the db_* columns).

set -euo pipefail

usage() {
    echo "usage: $0 <output.csv> [interval_seconds]" >&2
    exit 2
}

[[ $# -ge 1 && $# -le 2 ]] || usage
OUT="$1"
INTERVAL="${2:-10}"
[[ "$INTERVAL" =~ ^[1-9][0-9]*$ ]] || { echo "interval must be a positive integer (seconds), got '$INTERVAL'" >&2; exit 2; }

APP_URL="${APP_URL:-http://localhost:8080}"
PG_CONTAINER="${PG_CONTAINER:-go-bricks-postgres}"
PG_USER="${PG_USER:-postgres}"
PG_DB="${PG_DB:-postgres}"
PHASE_FILE="${PHASE_FILE:-$OUT.phase}"

for tool in curl jq; do
    command -v "$tool" >/dev/null 2>&1 || { echo "❌ $tool is required" >&2; exit 2; }
done

# --- sources ----------------------------------------------------------------

# debug_json <path> — the JSON body of a /_sys debug endpoint, or nothing.
debug_json() {
    curl -fsS --max-time 3 "$APP_URL/_sys/$1" 2>/dev/null || true
}

goroutines() {
    debug_json info | jq -r '.data.goroutines // empty' 2>/dev/null || true
}

heap_alloc_mb() {
    debug_json gc | jq -r '(.data.mem_before // empty) / 1048576 | . * 10 | round / 10' 2>/dev/null || true
}

# app_pid — APP_PID, else the process listening on APP_URL's port.
app_pid() {
    if [[ -n "${APP_PID:-}" ]]; then
        echo "$APP_PID"
        return
    fi
    local port="${APP_URL##*:}"
    port="${port%%/*}"
    [[ "$port" =~ ^[0-9]+$ ]] || return 0
    command -v lsof >/dev/null 2>&1 || return 0
    lsof -nP -tiTCP:"$port" -sTCP:LISTEN 2>/dev/null | head -n 1 || true
}

rss_mb() {
    local pid kb
    pid="$(app_pid)"
    [[ -n "$pid" ]] || return 0
    kb="$(ps -o rss= -p "$pid" 2>/dev/null | tr -d ' ' || true)"
    [[ "$kb" =~ ^[0-9]+$ ]] || return 0
    awk -v kb="$kb" 'BEGIN { printf "%.1f", kb / 1024 }'
}

# db_connections — "active,idle,total" client backends on PG_DB, or ",,".
db_connections() {
    local sql out
    sql="SELECT count(*) FILTER (WHERE state = 'active') || ',' ||
                count(*) FILTER (WHERE state = 'idle') || ',' || count(*)
           FROM pg_stat_activity
          WHERE datname = '$PG_DB' AND backend_type = 'client backend'
            AND pid <> pg_backend_pid()"
    out="$(docker exec "$PG_CONTAINER" psql -U "$PG_USER" -d "$PG_DB" -tAc "$sql" 2>/dev/null || true)"
    if [[ "$out" =~ ^[0-9]+,[0-9]+,[0-9]+$ ]]; then
        echo "$out"
    else
        echo ",,"
    fi
}

current_phase() {
    local phase=""
    [[ -f "$PHASE_FILE" ]] && phase="$(head -n 1 "$PHASE_FILE" 2>/dev/null || true)"
    phase="${phase//[^A-Za-z0-9_-]/}"
    echo "${phase:-manual}"
}

# --- preflight --------------------------------------------------------------

mkdir -p "$(dirname "$OUT")"

echo "📊 Load test monitor"
echo "   app      : $APP_URL"
echo "   output   : $OUT (every ${INTERVAL}s)"
[[ -n "$(goroutines)" ]] || echo "   ⚠️  $APP_URL/_sys/info does not answer: goroutines stay empty (enable DEBUG_ENDPOINTS_INFO, see header)"
[[ -n "$(heap_alloc_mb)" ]] || echo "   ⚠️  $APP_URL/_sys/gc does not answer: heap_alloc_mb stays empty (enable DEBUG_ENDPOINTS_GC, see header)"
[[ -n "$(rss_mb)" ]] || echo "   ⚠️  no host process found for $APP_URL: rss_mb stays empty (set APP_PID)"
[[ "$(db_connections)" != ",," ]] || echo "   ⚠️  cannot read pg_stat_activity in container $PG_CONTAINER: db_* stay empty (set PG_CONTAINER)"

if [[ ! -s "$OUT" ]]; then
    echo "timestamp,elapsed_s,phase,goroutines,heap_alloc_mb,rss_mb,db_active,db_idle,db_total" >"$OUT"
fi

ROWS=0
SLEEP_PID=""
START="$(date +%s)"
finish() {
    [[ -n "$SLEEP_PID" ]] && kill "$SLEEP_PID" 2>/dev/null
    echo ""
    echo "✅ Monitor stopped after $ROWS sample(s): $OUT"
    exit 0
}
trap finish INT TERM

# --- sample loop ------------------------------------------------------------

while true; do
    now="$(date +%s)"
    printf '%s,%s,%s,%s,%s,%s,%s\n' \
        "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
        "$((now - START))" \
        "$(current_phase)" \
        "$(goroutines)" \
        "$(heap_alloc_mb)" \
        "$(rss_mb)" \
        "$(db_connections)" >>"$OUT"
    ROWS=$((ROWS + 1))
    # sleep in the background so INT/TERM interrupt the wait at once
    sleep "$INTERVAL" &
    SLEEP_PID=$!
    wait "$SLEEP_PID" || true
done
