#!/usr/bin/env bash
# scripts/tenant-family-demo.sh
#
# Proof for the HTTP tenant features go-bricks offers only with multitenant
# enabled, read off a real instance of cmd/tenant-family-demo (the demo's
# multi-tenant binary; cmd/api stays single-tenant):
#
#   ADR-164 — a tenant-owned route family (server.TenantOwnedGroup) under
#          /api/v1/partner/v2: it skips the tenant seat, authenticates the
#          bearer JWT, and takes the tenant from its "tenant" claim. Client
#          faults are bodyless (DenyBodyless): 401 with WWW-Authenticate when
#          the token is missing, 403 otherwise. The module adds an existence
#          check to auth.TenantClaim, which checks form only, so a claim naming
#          a tenant the deployment does not hold is 403 too.
#   ADR-170 — a tenant read from the request path is written only when the
#          tenant store holds it; an unknown one is 400 "Invalid tenant".
#   ADR-161 — a missing or malformed tenant is 400 "Invalid tenant", with one
#          [server.tenant] WARN carrying http.route, request_id and
#          correlation_id, and a masked path.
#   Tenant-ID grammar ^[a-z0-9-]{1,64}$ — an id outside it is refused before
#          any lookup.
#
# The resolver order is [path, header]: on a path route the path wins over
# X-Tenant-ID. Header tenants are checked for FORM only, not existence, so an
# unknown but well-formed X-Tenant-ID on a header route answers 200. That is the
# framework's behavior at this pin, shown here as it is.
#
# The script builds nothing itself (the make target builds first). It boots its
# OWN instance on APP_PORT with JSON logs written to a file, sends one request
# per behavior, checks responses and the log with jq, and stops the instance.
# Each check prints PASS or FAIL; any FAIL exits 1.
#
# Prerequisites: none. No handler dials a database, so neither docker nor
# migrations nor keys are needed.
#
# Overrides (env): APP_BIN, APP_PORT (default 18091), STARTUP_TIMEOUT, LOG_DIR.
# The binary reads cmd/tenant-family-demo/config.yaml (relative to the repository
# root), and of the environment only SERVER_PORT, LOG_LEVEL, LOG_OUTPUT_FORMAT
# and APP_ENV. The script pins APP_ENV=development: the family and the token
# simulator exist only there.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR/.."

APP_BIN="${APP_BIN:-./bin/tenant-family-demo}"
APP_PORT="${APP_PORT:-18091}"
STARTUP_TIMEOUT="${STARTUP_TIMEOUT:-30}"
BASE_URL="http://127.0.0.1:${APP_PORT}"
API="/api/v1"

# Must match the framework's startup line (ADR-170) for the three static tenants.
STORE_LINE="[server.tenant] a tenant read from the request path is written only when the tenant store holds it (ADR-170) store=*config.TenantStore tenants=3"
REJECTED="[server.tenant] request rejected: invalid tenant"

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
[[ -x "$APP_BIN" ]] || fail "binary '$APP_BIN' not found — run 'make tenant-family-demo'"

LOG_DIR="${LOG_DIR:-$(mktemp -d "${TMPDIR:-/tmp}/tenant-family-demo.XXXXXX")}"
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

# jq_log FILTER [jq args...] — run FILTER over the log's JSON lines (non-JSON
# lines are skipped), slurped into one array.
jq_log() {
    local filter="$1"
    shift
    jq -Rn "$@" "[inputs | fromjson? | select(type == \"object\")] | $filter" "$APP_LOG"
}

# rejection_field REQUEST_ID FIELD — FIELD of the request's [server.tenant]
# rejection WARN, "<none>" when there is none, "<many>" when there are more.
rejection_field() {
    jq_log '[.[] | select(.request_id == $rid and (.message | startswith($m)))]
            | if length == 1 then (.[0][$f] // "<null>" | tostring)
              elif length == 0 then "<none>" else "<many>" end' \
        -r --arg rid "$1" --arg m "$REJECTED" --arg f "$2"
}

# message_attr REQUEST_ID KEY — the KEY="value" attribute inside the rejection
# WARN's message (reason, path), "<none>" when absent.
message_attr() {
    local msg
    msg="$(rejection_field "$1" message)"
    if [[ "$msg" =~ $2=\"([^\"]*)\" ]]; then
        echo "${BASH_REMATCH[1]}"
    else
        echo "<none>"
    fi
}

# send METHOD PATH [curl args...] — sets STATUS, BODY, RID (X-Request-ID) and
# HEADERS (the response header file). Waits for the request's access line
# (log.type=action), which is written after the response; a [server.tenant] WARN
# for the same request id comes before it, so the first line carrying the id is
# not enough.
send() {
    local method="$1" path="$2"
    shift 2
    HEADERS="$LOG_DIR/headers"
    BODY="$(curl -sS -D "$HEADERS" --max-time 10 -X "$method" "$@" "$BASE_URL$path")" || BODY=""
    STATUS="$(head -n1 "$HEADERS" | awk '{print $2}')"
    RID="$(grep -i '^x-request-id:' "$HEADERS" | head -n1 | cut -d: -f2- | tr -d ' \r')"
    if [[ -n "$RID" ]]; then
        for _ in $(seq 1 40); do
            [[ "$(jq_log 'any(.[]; .["log.type"] == "action" and .request_id == $rid)' \
                --arg rid "$RID")" == "true" ]] && break
            sleep 0.25
        done
    fi
}

header_value() {
    grep -i "^$1:" "$HEADERS" | head -n1 | cut -d: -f2- | sed 's/^ *//' | tr -d '\r'
}

data_field() {
    jq -r ".data.$1 // \"<null>\"" <<<"$BODY" 2>/dev/null || echo "<not json>"
}

# mint BODY — a token from the in-process issuer. The simulator sits behind the
# tenant seat, so it needs an X-Tenant-ID; the claim to mint is in BODY.
mint() {
    curl -sS --max-time 10 -X POST -H 'X-Tenant-ID: acme' -H 'Content-Type: application/json' \
        --data "$1" "$BASE_URL$API/__sim/partner-token" | jq -r '.data.token // empty'
}

# --- 0. boot an instance --------------------------------------------------

section "0/5  Boot cmd/tenant-family-demo on :$APP_PORT"

rc=0
curl -s -o /dev/null --max-time 2 "$BASE_URL/" || rc=$?
(( rc == 7 )) || fail "port $APP_PORT is already in use — set APP_PORT to a free port"

env -u DEBUG \
    APP_ENV=development \
    SERVER_PORT="$APP_PORT" \
    LOG_OUTPUT_FORMAT=json \
    LOG_LEVEL=info \
    "$APP_BIN" >"$APP_LOG" 2>&1 &
APP_PID=$!

deadline=$(( $(date +%s) + STARTUP_TIMEOUT ))
until curl -fsS -o /dev/null --max-time 2 "$BASE_URL$API/health" 2>/dev/null; do
    if ! kill -0 "$APP_PID" 2>/dev/null || (( $(date +%s) >= deadline )); then
        echo "--- last lines of $APP_LOG ---" >&2
        tail -n 20 "$APP_LOG" >&2 || true
        fail "the instance on :$APP_PORT did not become healthy"
    fi
    sleep 0.3
done
echo "instance  : :$APP_PORT (pid $APP_PID), log $APP_LOG"

check "ADR-170 startup line, tenants=3" "1" \
    "$(jq_log '[.[] | select(.message == $m and .level == "info")] | length' --arg m "$STORE_LINE")"
send GET "$API/ready"
check "/ready answers 200 (no tenant database is dialed)" "200" "$STATUS"

# --- 1. tenant-owned family (ADR-164) -------------------------------------

section "1/5  Tenant-owned family /partner/v2 (ADR-164)"

FAMILY="$API/partner/v2/whoami"

send GET "$FAMILY"
check "no bearer token answers 401" "401" "$STATUS"
check "401 carries WWW-Authenticate" "yes" "$([[ -n "$(header_value www-authenticate)" ]] && echo yes || echo no)"
check "401 body is empty" "" "$BODY"

ACME_TOKEN="$(mint '{"tenant":"acme"}')"
check "simulator minted a token for acme" "yes" "$([[ -n "$ACME_TOKEN" ]] && echo yes || echo no)"
send GET "$FAMILY" -H "Authorization: Bearer $ACME_TOKEN" -H 'X-Tenant-ID: globex'
check "claim acme + X-Tenant-ID globex answers 200" "200" "$STATUS"
check "tenant comes from the claim, not the header" "acme" "$(data_field tenant)"
check "source is the claim" "claim" "$(data_field source)"

send GET "$FAMILY" -H "Authorization: Bearer $(mint '{"omitTenant":true}')"
check "token without a tenant claim answers 403" "403" "$STATUS"
check "403 body is empty" "" "$BODY"

send GET "$FAMILY" -H "Authorization: Bearer $(mint '{"tenant":"ghost"}')"
check "claim naming an unheld tenant (ghost) answers 403" "403" "$STATUS"
check "403 body is empty" "" "$BODY"

send GET "$FAMILY" -H "Authorization: Bearer $(mint '{"tenant":"Acme!"}')"
check "claim outside the tenant-ID grammar answers 403" "403" "$STATUS"

# --- 2. missing tenant on a seat route (ADR-161) --------------------------

section "2/5  Missing tenant behind the seat (ADR-161)"

send GET "$API/tenants/whoami"
check "no X-Tenant-ID answers 400" "400" "$STATUS"
check "error message is Invalid tenant" "Invalid tenant" "$(jq -r '.error.message' <<<"$BODY")"
check "WARN reason=\"resolver error\"" "resolver error" "$(message_attr "$RID" reason)"
check "WARN level" "warn" "$(rejection_field "$RID" level)"
check "WARN http.route" "$API/tenants/whoami" "$(rejection_field "$RID" http.route)"
check "WARN request_id = X-Request-ID" "$RID" "$(rejection_field "$RID" request_id)"
check "WARN correlation_id = X-Request-ID" "$RID" "$(rejection_field "$RID" correlation_id)"

send GET "$API/tenants/whoami" -H 'X-Tenant-ID: ACME'
check "X-Tenant-ID outside the grammar (ACME) answers 400" "400" "$STATUS"

# --- 3. path tenant must be held (ADR-170) --------------------------------

section "3/5  Path tenant existence check (ADR-170)"

send GET "$API/t/ghost/whoami"
check "unknown path tenant (ghost) answers 400" "400" "$STATUS"
check "error message is Invalid tenant" "Invalid tenant" "$(jq -r '.error.message' <<<"$BODY")"
check "WARN reason=\"unknown tenant\"" "unknown tenant" "$(message_attr "$RID" reason)"
check "WARN path is masked" "$API/t/REDACTED/whoami" "$(message_attr "$RID" path)"
check "WARN http.route" "$API/t/:tenant/whoami" "$(rejection_field "$RID" http.route)"
check "WARN request_id = X-Request-ID" "$RID" "$(rejection_field "$RID" request_id)"
check "WARN correlation_id = X-Request-ID" "$RID" "$(rejection_field "$RID" correlation_id)"

send GET "$API/t/acme/whoami"
check "held path tenant (acme) answers 200" "200" "$STATUS"
check "tenant is acme" "acme" "$(data_field tenant)"

send GET "$API/t/acme/whoami" -H 'X-Tenant-ID: globex'
check "path acme + X-Tenant-ID globex answers 200" "200" "$STATUS"
check "path wins over the header" "acme" "$(data_field tenant)"

# --- 4. header tenants: form only -----------------------------------------

section "4/5  Header tenant is checked for form only"

send GET "$API/tenants/whoami" -H 'X-Tenant-ID: acme'
check "X-Tenant-ID acme answers 200" "200" "$STATUS"
check "tenant is acme" "acme" "$(data_field tenant)"

send GET "$API/tenants/whoami" -H 'X-Tenant-ID: ghost'
check "X-Tenant-ID ghost (well-formed, unheld) answers 200" "200" "$STATUS"
check "tenant ghost is written as is" "ghost" "$(data_field tenant)"
GHOST_RID="$RID"

# --- 5. no unknown tenant id reached a rejection line ---------------------
# Only the [server.tenant] rejection lines are covered. A header tenant the
# seat admitted (form-checked, never existence-checked) is the request's
# tenant, so its access line carries it as `tenant` — framework behavior.

section "5/5  Rejection lines never echo the tenant"

check "access line of the header-admitted ghost carries tenant=ghost" "ghost" \
    "$(jq_log '[.[] | select(.["log.type"] == "action" and .request_id == $rid)] | .[0].tenant // "<none>"' \
        -r --arg rid "$GHOST_RID")"
check "no [server.tenant] line names ghost" "0" \
    "$(jq_log '[.[] | select((.message // "") | startswith("[server.tenant]")) | select(.message | contains("ghost"))] | length')"

section "Verdict"

echo "passed=$PASSED failed=$FAILED  log: $APP_LOG"
if (( FAILED != 0 )); then
    fail "$FAILED check(s) failed — the instance's log is kept in $APP_LOG"
fi
echo "✅ tenant-owned family, path existence check, invalid-tenant WARN and grammar all hold"
