#!/usr/bin/env bash
# scripts/log-correlation-demo.sh
#
# Proof for three always-on go-bricks logging behaviors (go-bricks main
# 0eb5f27f, C73.29-C73.33), read off the JSON log of a real app instance:
#
#   C73.29 (ADR-167) — inbound paths render from the matched route template.
#          The access line's url.path and message print every path parameter as
#          REDACTED; http.route keeps the raw template; an unmatched request
#          prints the bare REDACTED; startup logs one [server.paths] line.
#   C73.31 — Panic recovered carries http.route, request_id and correlation_id
#          (the same value as the response's X-Request-ID).
#   C73.33 — a WARN logged through the request context, or
#          HandlerContext.EscalateSeverity, keeps the request's access line and
#          raises it to level=warn result_code=WARN; a plaintext POST to a JOSE
#          route writes an access line with status 415 at level=warn.
#
# The script builds nothing itself (the make target builds first). It boots its
# OWN app instance on APP_PORT with JSON logs written to a file, sends one
# request per behavior, checks the log with jq, and stops the instance. Each
# check prints PASS or FAIL; any FAIL exits 1.
#
# Routes used:
#   GET  /api/v1/legacy/products/<random uuid>            masked path on a module route (404)
#   GET  /api/v1/no-such-route/<random uuid>              unmatched path (404)
#   GET  /api/v1/__sim/observability/warn/<token>         handler WARN -> escalated access line
#   GET  /api/v1/__sim/observability/escalate/<token>     EscalateSeverity -> escalated access line
#   GET  /api/v1/__sim/observability/panic/<token>        Panic recovered correlation (500)
#   POST /api/v1/tokens  (plaintext JSON)                 JOSE 415 access line
# The /__sim/observability routes belong to the logproof module and exist only
# with CUSTOM_LOGPROOF_ENABLED=true AND APP_ENV=development; the script sets both
# for its own instance.
#
# Prerequisites:
#   make docker-up && make migrate   # postgres + broker the instance boots against
#   make generate-keys               # the keystore loads certs/ at boot
#
# Overrides (env): APP_BIN, APP_PORT (default 18090), API_BASE_PATH,
#                  STARTUP_TIMEOUT, LOG_DIR. Every other variable in the
#                  environment passes through to the instance unchanged.
#
# Trace ids: config.development.yaml leaves the OTLP export off, so no request
# has a valid span context and trace_id/span_id are absent from these lines by
# design. Boot with the OBSERVABILITY_* variables in CLAUDE.md's runtime tour to
# see them.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR/.."

APP_BIN="${APP_BIN:-./bin/go-bricks-demo-project}"
APP_PORT="${APP_PORT:-18090}"
API_BASE_PATH="${API_BASE_PATH:-/api/v1}"
STARTUP_TIMEOUT="${STARTUP_TIMEOUT:-60}"
BASE_URL="http://127.0.0.1:${APP_PORT}"

# Must match the framework's [server.paths] line and the logproof module.
PATHS_LINE="[server.paths] inbound paths render from the matched route template rule=masked_route_template listener=application token=REDACTED unmatched=REDACTED"
WARN_MESSAGE="logproof: handler WARN logged through the request context"
SIM="${API_BASE_PATH}/__sim/observability"

section() {
    printf '\n── %s ──────────────────────────\n' "$*"
}

fail() {
    echo "❌ $*" >&2
    exit 1
}

for tool in curl jq; do
    command -v "$tool" >/dev/null 2>&1 || fail "$tool is required but not installed"
done
[[ -x "$APP_BIN" ]] || fail "app binary '$APP_BIN' not found — run 'make build'"

LOG_DIR="${LOG_DIR:-$(mktemp -d "${TMPDIR:-/tmp}/log-correlation-demo.XXXXXX")}"
mkdir -p "$LOG_DIR"
APP_LOG="$LOG_DIR/app.log"
APP_PID=""

stop_app() {
    if [[ -n "$APP_PID" ]] && kill -0 "$APP_PID" 2>/dev/null; then
        kill -TERM "$APP_PID" 2>/dev/null || true
        for _ in $(seq 1 20); do
            kill -0 "$APP_PID" 2>/dev/null || return 0
            sleep 0.5
        done
        kill -KILL "$APP_PID" 2>/dev/null || true
    fi
}
trap stop_app EXIT

PASSED=0
FAILED=0

# check LABEL EXPECTED ACTUAL — exact comparison, one PASS/FAIL line.
check() {
    local label="$1" want="$2" got="$3"
    if [[ "$got" == "$want" ]]; then
        echo "PASS  $label"
        PASSED=$((PASSED + 1))
    else
        echo "FAIL  $label (want '$want', got '$got')"
        FAILED=$((FAILED + 1))
    fi
}

# absent LABEL NEEDLE — the needle appears nowhere in the app log.
absent() {
    local label="$1" needle="$2"
    if grep -qF -- "$needle" "$APP_LOG"; then
        echo "FAIL  $label ($(grep -cF -- "$needle" "$APP_LOG") line(s) carry it)"
        FAILED=$((FAILED + 1))
    else
        echo "PASS  $label"
        PASSED=$((PASSED + 1))
    fi
}

# jq_log FILTER [jq args...] — run FILTER over the log's JSON lines (non-JSON
# lines are skipped), slurped into one array.
jq_log() {
    local filter="$1"
    shift
    jq -Rn "$@" "[inputs | fromjson? | select(type == \"object\")] | $filter" "$APP_LOG"
}

# access_field REQUEST_ID FIELD — FIELD of the request's access line
# (log.type=action), "<none>" when there is none, "<many>" when there are more.
access_field() {
    jq_log '[.[] | select(.["log.type"] == "action" and .request_id == $rid)]
            | if length == 1 then (.[0][$f] // "<null>" | tostring)
              elif length == 0 then "<none>" else "<many>" end' \
        -r --arg rid "$1" --arg f "$2"
}

# line_field REQUEST_ID MESSAGE FIELD — FIELD of the one line with MESSAGE for
# the request.
line_field() {
    jq_log '[.[] | select(.request_id == $rid and .message == $m)]
            | if length == 1 then (.[0][$f] // "<null>" | tostring)
              elif length == 0 then "<none>" else "<many>" end' \
        -r --arg rid "$1" --arg m "$2" --arg f "$3"
}

# send METHOD PATH [curl args...] — sets STATUS and RID (the response's
# X-Request-ID). The access line (log.type=action) is written after the
# response, and a handler WARN for the same request id comes before it, so wait
# for the access line itself, not the first line carrying the id.
send() {
    local method="$1" path="$2"
    shift 2
    local hdr="$LOG_DIR/headers"
    STATUS="$(curl -sS -o /dev/null -D "$hdr" --max-time 10 -w '%{http_code}' \
        -X "$method" "$@" "$BASE_URL$path")" || STATUS="000"
    RID="$(grep -i '^x-request-id:' "$hdr" | head -n1 | cut -d: -f2- | tr -d ' \r')"
    if [[ -n "$RID" ]]; then
        for _ in $(seq 1 40); do
            [[ "$(jq_log 'any(.[]; .["log.type"] == "action" and .request_id == $rid)' \
                --arg rid "$RID")" == "true" ]] && break
            sleep 0.25
        done
    fi
}

uuid() {
    if command -v uuidgen >/dev/null 2>&1; then
        uuidgen | tr '[:upper:]' '[:lower:]'
    else
        cat /proc/sys/kernel/random/uuid
    fi
}

# --- 0. boot an instance --------------------------------------------------

section "0/5  Boot an app instance on :$APP_PORT"

rc=0
curl -s -o /dev/null --max-time 2 "$BASE_URL/" || rc=$?
(( rc == 7 )) || fail "port $APP_PORT is already in use — set APP_PORT to a free port"

# LOG_OUTPUT_FORMAT=json: config.yml's "auto" picks JSON for a non-TTY stdout
# already; forcing it keeps the proof independent of that rule. LOG_LEVEL=info:
# the [server.paths] line is INFO and C73.33's escalation is read at info.
env -u DEBUG \
    APP_ENV="${APP_ENV:-development}" \
    CORS_DEV_WILDCARD=true \
    CUSTOM_LOGPROOF_ENABLED=true \
    SERVER_PORT="$APP_PORT" \
    LOG_OUTPUT_FORMAT=json \
    LOG_LEVEL=info \
    "$APP_BIN" >"$APP_LOG" 2>&1 &
APP_PID=$!

deadline=$(( $(date +%s) + STARTUP_TIMEOUT ))
until curl -fsS -o /dev/null --max-time 2 "$BASE_URL$API_BASE_PATH/health" 2>/dev/null; do
    if ! kill -0 "$APP_PID" 2>/dev/null || (( $(date +%s) >= deadline )); then
        echo "--- last lines of $APP_LOG ---" >&2
        tail -n 20 "$APP_LOG" >&2 || true
        fail "the instance on :$APP_PORT did not become healthy — is the infrastructure up (make docker-up && make migrate)?"
    fi
    sleep 0.5
done
echo "instance  : :$APP_PORT (pid $APP_PID), log $APP_LOG"

# --- 1. masked paths (C73.29) ---------------------------------------------

section "1/5  Masked paths (C73.29)"

check "[server.paths] startup line" "1" \
    "$(jq_log '[.[] | select(.message == $m and .level == "info")] | length' --arg m "$PATHS_LINE")"

PRODUCT_ID="$(uuid)"
send GET "$API_BASE_PATH/legacy/products/$PRODUCT_ID"
check "legacy product by id answers 404" "404" "$STATUS"
check "access line url.path is masked" "$API_BASE_PATH/legacy/products/REDACTED" "$(access_field "$RID" url.path)"
check "access line http.route is the raw template" "$API_BASE_PATH/legacy/products/:id" "$(access_field "$RID" http.route)"
check "access line message is masked" "GET $API_BASE_PATH/legacy/products/REDACTED" \
    "$(access_field "$RID" message | sed 's/ completed in .*//')"

UNMATCHED_ID="$(uuid)"
send GET "$API_BASE_PATH/no-such-route/$UNMATCHED_ID"
check "unmatched path answers 404" "404" "$STATUS"
check "unmatched access line url.path is the bare token" "REDACTED" "$(access_field "$RID" url.path)"

# --- 2. handler WARN escalates the access line (C73.33) -------------------

section "2/5  Handler WARN through the request context (C73.33)"

WARN_TOKEN="4111$(printf '%06d%06d' $((RANDOM % 1000000)) $((RANDOM * 31 % 1000000)))"
send GET "$SIM/warn/$WARN_TOKEN"
check "warn route answers 200" "200" "$STATUS"
check "handler WARN logged for the request" "warn" "$(line_field "$RID" "$WARN_MESSAGE" level)"
check "access line kept, level=warn" "warn" "$(access_field "$RID" level)"
check "access line result_code=WARN" "WARN" "$(access_field "$RID" result_code)"
check "access line status 200" "200" "$(access_field "$RID" http.response.status_code)"
check "access line url.path masks the token" "$SIM/warn/REDACTED" "$(access_field "$RID" url.path)"

ESCALATE_TOKEN="4111$(printf '%06d%06d' $((RANDOM % 1000000)) $((RANDOM * 37 % 1000000)))"
send GET "$SIM/escalate/$ESCALATE_TOKEN"
check "escalate route answers 200" "200" "$STATUS"
check "EscalateSeverity: access line level=warn" "warn" "$(access_field "$RID" level)"
check "EscalateSeverity: access line result_code=WARN" "WARN" "$(access_field "$RID" result_code)"

# --- 3. Panic recovered correlation (C73.31) ------------------------------

section "3/5  Panic recovered correlation (C73.31)"

PANIC_TOKEN="4111$(printf '%06d%06d' $((RANDOM % 1000000)) $((RANDOM * 41 % 1000000)))"
send GET "$SIM/panic/$PANIC_TOKEN"
check "panic route answers 500" "500" "$STATUS"
check "response carries X-Request-ID" "yes" "$([[ -n "$RID" ]] && echo yes || echo no)"
check "Panic recovered http.route is the template" "$SIM/panic/:token" "$(line_field "$RID" "Panic recovered" http.route)"
check "Panic recovered request_id = X-Request-ID" "$RID" "$(line_field "$RID" "Panic recovered" request_id)"
check "Panic recovered correlation_id = X-Request-ID" "$RID" "$(line_field "$RID" "Panic recovered" correlation_id)"
check "panic access line url.path masks the token" "$SIM/panic/REDACTED" "$(access_field "$RID" url.path)"
# Not asserted: at this pin the access line of a recovered panic is written
# before the error handler answers 500, so it reads the uncommitted status.
echo "note  panic access line reads status=$(access_field "$RID" http.response.status_code) level=$(access_field "$RID" level) (client got $STATUS)"

# --- 4. JOSE plaintext 415 (C73.33) ---------------------------------------

section "4/5  Plaintext POST to a JOSE route (C73.33)"

send POST "$API_BASE_PATH/tokens" -H 'Content-Type: application/json' --data '{}'
check "plaintext POST answers 415" "415" "$STATUS"
check "access line kept, status 415" "415" "$(access_field "$RID" http.response.status_code)"
check "access line level=warn" "warn" "$(access_field "$RID" level)"
check "access line result_code=WARN" "WARN" "$(access_field "$RID" result_code)"
# The JOSE failure line is an ERROR, but it is no longer bound to the request
# context: it carries the request's correlation and leaves the access line at
# its status-derived level (warn above, not error).
check "jose failure line carries the request_id" "error" "$(line_field "$RID" "jose: operation failed" level)"
check "jose failure line http.route" "$API_BASE_PATH/tokens" "$(line_field "$RID" "jose: operation failed" http.route)"
check "jose failure line correlation_id = X-Request-ID" "$RID" "$(line_field "$RID" "jose: operation failed" correlation_id)"

# --- 5. no path value reached the log --------------------------------------

section "5/5  No path value in the log"

absent "product id absent from the log" "$PRODUCT_ID"
absent "unmatched path id absent from the log" "$UNMATCHED_ID"
absent "warn token absent from the log" "$WARN_TOKEN"
absent "escalate token absent from the log" "$ESCALATE_TOKEN"
absent "panic token absent from the log" "$PANIC_TOKEN"

section "Verdict"

echo "passed=$PASSED failed=$FAILED  log: $APP_LOG"
if (( FAILED != 0 )); then
    fail "$FAILED check(s) failed — the instance's log is kept in $APP_LOG"
fi
echo "✅ masked paths, correlated panic line and escalated access lines all hold"
