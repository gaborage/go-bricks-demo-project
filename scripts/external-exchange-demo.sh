#!/usr/bin/env bash
# scripts/external-exchange-demo.sh
#
# Consuming from an exchange ANOTHER service owns (go-bricks v0.67.0: #1773
# DeclareExternalExchange, #1774 messaging.declare.externalwait, ADR-119).
#
# The partnerfeed module references `partner-events` with DeclareExternalExchange:
# every declare pass VERIFIES it with a passive exchange.declare and never creates
# it. No partner service runs locally, so this script plays the partner — it
# creates the exchange through the RabbitMQ management API, the way the owning
# service's own deploy would. Three runs, each against an app instance this script
# starts itself with the module switched on (CUSTOM_PARTNERFEED_ENABLED=true):
#
#   1. FAILS FAST — exchange absent, externalwait 0 (the default). The passive
#                   declare answers 404, and because the module declares a
#                   consumer, startup aborts on the broker's own reply instead of
#                   serving HTTP while consuming nothing.
#   2. WAITS      — exchange absent, externalwait 60s. The app logs one WARN and
#                   re-runs the startup declare pass with backoff while the HTTP
#                   listener stays down. The "partner" then creates the exchange,
#                   the next attempt logs "External exchange verified", and startup
#                   completes with no restart.
#   3. CONSUMES   — one partner.stock.updated event published to partner-events
#                   through the management API reaches the typed consumer.
#
# On exit — success, failure or Ctrl-C — the app is stopped and every broker
# entity this run created is deleted: partner-events, plus the feed queue, its
# DLQ and its DLX when they did not exist before the run.
#
# Prerequisites (the same as `make run`; the app boots in full each time):
#   make docker-up      # RabbitMQ (management API) + postgres
#   make migrate        # outbox/inbox ledgers
#   make generate-keys  # keystore DER files
#   make build          # bin/go-bricks-demo-project (make external-exchange-demo does it)
#   NOTHING listening on the APP_URL port: this script starts its OWN instance, so
#   stop `make run` first — it refuses to run otherwise.
#
# Overrides (env): APP_URL, API_BASE, APP_BIN, RABBIT_MGMT, RABBIT_USER,
#                  RABBIT_PASS, RABBIT_VHOST, EXTERNAL_WAIT, OWNER_DELAY,
#                  BOOT_TIMEOUT, KEEP_LOGS. The app inherits every other variable
#                  from this shell, so infra overrides such as DATABASE_PORT or
#                  MESSAGING_BROKER_URL reach it unchanged.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR/.."

# Endpoint guard + credential-file writer, shared with the sealed-message demos.
# shellcheck source-path=SCRIPTDIR source=lib/rabbitmq-mgmt.sh
source "$SCRIPT_DIR/lib/rabbitmq-mgmt.sh"

APP_URL="${APP_URL:-http://localhost:8080}"
APP_URL="${APP_URL%/}"
API_BASE="${API_BASE:-$APP_URL/api/v1}"
APP_BIN="${APP_BIN:-bin/go-bricks-demo-project}"

RABBIT_MGMT="${RABBIT_MGMT:-http://127.0.0.1:15672}"
RABBIT_USER="${RABBIT_USER:-guest}"
RABBIT_PASS="${RABBIT_PASS:-guest}"
RABBIT_VHOST="${RABBIT_VHOST:-%2F}"   # default vhost "/" percent-encoded

EXTERNAL_WAIT="${EXTERNAL_WAIT:-60s}" # messaging.declare.externalwait for run 2
OWNER_DELAY="${OWNER_DELAY:-5}"       # seconds the partner "deploys" after the app
BOOT_TIMEOUT="${BOOT_TIMEOUT:-90}"    # seconds to wait for a boot to fail or finish
KEEP_LOGS="${KEEP_LOGS:-0}"           # 1 keeps the per-run app logs

# Topology — must match internal/modules/partnerfeed/domain/stock.go.
EXCHANGE="partner-events"
ROUTING_KEY="partner.stock.updated"
QUEUE="partnerfeed.stock.updated"
DLQ="$QUEUE.dlq"   # derived by DeclareQueueWithDLQ
DLX="$QUEUE.dlx"

# Log messages this script waits on — verbatim from go-bricks v0.67.0
# (app/messaging_setup.go, messaging/registry.go), cmd/api/main.go (the fatal
# line) and the partnerfeed module.
MSG_FATAL="Failed to start application"
MSG_WAIT="Broker answered 404, re-running the startup declare pass until it succeeds or externalwait elapses"
MSG_VERIFIED="External exchange verified"
MSG_CONSUMERS="Consumers started on the control-plane key"
MSG_CONSUMED="Partner stock update consumed"
NOT_FOUND="NOT_FOUND - no exchange '$EXCHANGE'"

# --- helpers --------------------------------------------------------------

section() {
    printf '\n── %s ──────────────────────────\n' "$*"
}

fail() {
    echo "❌ $*" >&2
    if [[ -n "${CURRENT_LOG:-}" && -s "$CURRENT_LOG" ]]; then
        echo "--- last app log lines ($CURRENT_LOG) ---" >&2
        tail -n 15 "$CURRENT_LOG" >&2 || true
    fi
    exit 1
}

for tool in curl jq; do
    command -v "$tool" >/dev/null 2>&1 || fail "$tool is required but not installed"
done

# APP_URL's authority, split for the port probe and the app's own SERVER_PORT.
AUTHORITY="${APP_URL#*://}"
AUTHORITY="${AUTHORITY%%/*}"
case "$AUTHORITY" in
    *:*) APP_HOST="${AUTHORITY%:*}"; APP_PORT="${AUTHORITY##*:}" ;;
    *) APP_HOST="$AUTHORITY"; APP_PORT=80 ;;
esac
APP_HOST="${APP_HOST#[}"
APP_HOST="${APP_HOST%]}"
[[ "$APP_PORT" =~ ^[0-9]+$ ]] || fail "APP_URL='$APP_URL' has no usable port"

# port_busy — true when something already accepts connections on APP_URL's port.
# bash's /dev/tcp needs no extra tool (nc/lsof differ across macOS and Linux).
port_busy() {
    (exec 3<>"/dev/tcp/$APP_HOST/$APP_PORT") 2>/dev/null
}

# log_entry FILE MESSAGE [JQ_FILTER] — the first JSON log line with that
# message, compacted, or nothing. Lines that are not JSON objects (a Go panic
# trace, a bare scalar) are skipped rather than fatal, and jq's own first()
# stops the read, so no `| head` can SIGPIPE it under pipefail. Always succeeds:
# the log file may not exist yet right after a start.
log_entry() {
    jq -nRc --arg m "$2" \
        "first(inputs | fromjson? | objects | select(.message == \$m) ${3:+| select($3)})" "$1" 2>/dev/null || true
}

# app_alive — the instance this script started is still running.
app_alive() {
    [[ -n "${APP_PID:-}" ]] && kill -0 "$APP_PID" 2>/dev/null
}

# wait_for_log FILE MESSAGE TIMEOUT [JQ_FILTER] — poll until the message is
# logged. Gives up early when the app exits without logging it.
wait_for_log() {
    local deadline=$((SECONDS + $3))
    while ((SECONDS < deadline)); do
        [[ -n "$(log_entry "$1" "$2" "${4:-}")" ]] && return 0
        if ! app_alive; then
            [[ -n "$(log_entry "$1" "$2" "${4:-}")" ]] && return 0
            return 1
        fi
        sleep 1
    done
    return 1
}

# start_app LOG EXTERNALWAIT — boot one instance with the module switched on.
# LOG_OUTPUT_FORMAT=json keeps the file machine-readable (the default "auto"
# already picks JSON when stdout is not a terminal; this pins it).
start_app() {
    CURRENT_LOG="$1"
    APP_ENV=development \
    CORS_DEV_WILDCARD=true \
    LOG_OUTPUT_FORMAT=json \
    SERVER_PORT="$APP_PORT" \
    CUSTOM_PARTNERFEED_ENABLED=true \
    MESSAGING_DECLARE_EXTERNALWAIT="$2" \
        "$APP_BIN" >"$1" 2>&1 &
    APP_PID=$!
}

# stop_app — SIGTERM (graceful shutdown), SIGKILL after 30s.
stop_app() {
    local deadline
    [[ -n "${APP_PID:-}" ]] || return 0
    if kill -0 "$APP_PID" 2>/dev/null; then
        kill -TERM "$APP_PID" 2>/dev/null || true
        deadline=$((SECONDS + 30))
        while kill -0 "$APP_PID" 2>/dev/null && ((SECONDS < deadline)); do
            sleep 1
        done
        kill -KILL "$APP_PID" 2>/dev/null || true
    fi
    wait "$APP_PID" 2>/dev/null || true
    APP_PID=""
}

# mgmt_status METHOD PATH [BODY] — HTTP status of one management call, bounded
# to 10s so a hung management API cannot stall the cleanup.
mgmt_status() {
    local args=(-sS --max-time 10 -o /dev/null -w '%{http_code}' -K "$CURL_CFG" -X "$1" "$RABBIT_MGMT/api/$2")
    if [[ $# -ge 3 ]]; then
        args+=(-H 'content-type: application/json' --data-binary "$3")
    fi
    curl "${args[@]}" 2>/dev/null || echo "000"
}

exists() { # exists exchanges|queues NAME
    [[ "$(mgmt_status GET "$1/$RABBIT_VHOST/$2")" == "200" ]]
}

# --- endpoint guard + credentials ----------------------------------------

guard_mgmt_endpoint "$RABBIT_MGMT"

CURL_CFG="$(mktemp)"
LOG_DIR="$(mktemp -d)"
APP_PID=""
CURRENT_LOG=""
# Keep-by-default: until the preflight has looked, every feed entity counts as
# pre-existing, so a run that fails before that point deletes nothing.
CREATED_EXCHANGE=0
QUEUE_PREEXISTED=1
DLQ_PREEXISTED=1
DLX_PREEXISTED=1

# Stop the app first (a live app would re-declare the feed queue it consumes
# from), then delete what this run created, then drop the credential file.
cleanup() {
    local status=$?
    trap - EXIT
    # A second Ctrl-C would otherwise run `exit 130` in the middle of this
    # function and skip the deletes and the removal of the credential file.
    # Every wait below is bounded: stop_app 30s, each management call 10s.
    trap '' INT TERM HUP
    set +e
    stop_app
    if [[ -s "$CURL_CFG" ]]; then
        # Delete only what this run created; a queue that existed before the run
        # belongs to whoever made it. Deleting the exchange drops its bindings.
        if [[ "$CREATED_EXCHANGE" == "1" ]]; then
            mgmt_status DELETE "exchanges/$RABBIT_VHOST/$EXCHANGE" >/dev/null
        fi
        [[ "$QUEUE_PREEXISTED" == "1" ]] || mgmt_status DELETE "queues/$RABBIT_VHOST/$QUEUE" >/dev/null
        [[ "$DLQ_PREEXISTED" == "1" ]] || mgmt_status DELETE "queues/$RABBIT_VHOST/$DLQ" >/dev/null
        [[ "$DLX_PREEXISTED" == "1" ]] || mgmt_status DELETE "exchanges/$RABBIT_VHOST/$DLX" >/dev/null
    fi
    rm -f "$CURL_CFG"
    if [[ "$KEEP_LOGS" == "1" ]]; then
        echo "app logs kept in $LOG_DIR"
    else
        rm -rf "$LOG_DIR"
    fi
    exit "$status"
}
trap cleanup EXIT
# Signals become ordinary exits so the EXIT trap — and the deletes — run.
trap 'exit 130' INT
trap 'exit 143' TERM
trap 'exit 129' HUP

write_curl_cfg "$CURL_CFG" "$RABBIT_MGMT" "$RABBIT_USER" "$RABBIT_PASS"

# --- preflight ------------------------------------------------------------

section "0/3  Preflight"

[[ -x "$APP_BIN" ]] || fail "no app binary at '$APP_BIN' — run 'make build' (make external-exchange-demo builds it)"

if port_busy; then
    fail "something already listens on $APP_HOST:$APP_PORT — this demo starts its OWN app instance with the partnerfeed module on; stop 'make run' first"
fi

curl -fsS --max-time 10 -o /dev/null -K "$CURL_CFG" "$RABBIT_MGMT/api/overview" 2>/dev/null \
    || fail "RabbitMQ management API not reachable at $RABBIT_MGMT — run 'make docker-up'"

if exists exchanges "$EXCHANGE"; then
    fail "exchange '$EXCHANGE' already exists, so the missing-exchange runs cannot be shown. If it is left over from an aborted run, delete it (curl prompts for the password, which keeps it off argv):
    curl -u '$RABBIT_USER' -X DELETE '$RABBIT_MGMT/api/exchanges/$RABBIT_VHOST/$EXCHANGE'"
fi

exists queues "$QUEUE" || QUEUE_PREEXISTED=0
exists queues "$DLQ" || DLQ_PREEXISTED=0
exists exchanges "$DLX" || DLX_PREEXISTED=0

echo "app      : $APP_BIN on $APP_URL (port free), CUSTOM_PARTNERFEED_ENABLED=true"
echo "broker   : $RABBIT_MGMT — '$EXCHANGE' absent (this run plays its owner)"
echo "feed     : queue '$QUEUE' ← '$EXCHANGE' / '$ROUTING_KEY' (DLQ '$DLQ')"

# --- 1. absent exchange, no wait -------------------------------------------

section "1/3  Exchange absent, externalwait 0 — startup fails fast"

LOG_A="$LOG_DIR/run1-fail-fast.log"
BOOT_START=$SECONDS
start_app "$LOG_A" 0s
echo "started pid $APP_PID with MESSAGING_DECLARE_EXTERNALWAIT=0s"

DEADLINE=$((SECONDS + BOOT_TIMEOUT))
while app_alive && ((SECONDS < DEADLINE)); do
    sleep 1
done
app_alive && fail "the app was still running after ${BOOT_TIMEOUT}s — expected startup to abort on the missing exchange"

EXIT_CODE=0
wait "$APP_PID" || EXIT_CODE=$?
APP_PID=""
echo "exited with code $EXIT_CODE after $((SECONDS - BOOT_START))s — never listened on $APP_URL"

FATAL="$(log_entry "$LOG_A" "$MSG_FATAL")"
[[ -n "$FATAL" ]] || fail "no '$MSG_FATAL' line in the app log"
grep -qF -- "$NOT_FOUND" <<<"$FATAL" \
    || fail "startup failed, but not on the partner exchange's 404 — check the prerequisites above. Fatal line:
$FATAL"
[[ "$EXIT_CODE" -ne 0 ]] || fail "the app exited 0; expected a failed startup"
[[ -z "$(log_entry "$LOG_A" "$MSG_WAIT")" ]] || fail "the app waited although externalwait was 0"

echo
echo "fatal line (the broker's own 404, no wait):"
jq . <<<"$FATAL"
echo
echo "✅ passive declare → 404 → the consumer-declaring service aborted at once."

# --- 2. absent exchange, bounded wait --------------------------------------

section "2/3  Exchange absent, externalwait $EXTERNAL_WAIT — the partner deploys late"

LOG_B="$LOG_DIR/run2-wait.log"
start_app "$LOG_B" "$EXTERNAL_WAIT"
echo "started pid $APP_PID with MESSAGING_DECLARE_EXTERNALWAIT=$EXTERNAL_WAIT"

wait_for_log "$LOG_B" "$MSG_WAIT" "$BOOT_TIMEOUT" \
    || fail "no retry WARN — is the framework older than v0.67.0, or did startup fail earlier?"
echo
echo "retry WARN (logged once; later attempts log at DEBUG):"
jq . <<<"$(log_entry "$LOG_B" "$MSG_WAIT")"

sleep "$OWNER_DELAY"
if curl -fsS -o /dev/null "$API_BASE/health" 2>/dev/null; then
    fail "$API_BASE/health answered while the declare pass was still waiting"
fi
echo
echo "${OWNER_DELAY}s later the app is still waiting and $API_BASE/health does not"
echo "answer: the listener starts only after this pass, which is why a startupProbe"
echo "must allow first attempt + externalwait + one final attempt."

echo
echo "The partner service deploys: creating '$EXCHANGE' (durable topic — the owner's"
echo "shape; this service never sends one)."
STATUS="$(mgmt_status PUT "exchanges/$RABBIT_VHOST/$EXCHANGE" \
    '{"type":"topic","durable":true,"auto_delete":false,"internal":false,"arguments":{}}')"
[[ "$STATUS" == "201" || "$STATUS" == "204" ]] || fail "creating '$EXCHANGE' returned HTTP $STATUS"
CREATED_EXCHANGE=1
echo "PUT /api/exchanges/$RABBIT_VHOST/$EXCHANGE → HTTP $STATUS"

wait_for_log "$LOG_B" "$MSG_VERIFIED" 30 ".exchange == \"$EXCHANGE\"" \
    || fail "the app never logged '$MSG_VERIFIED' for '$EXCHANGE'"
echo
echo "verified on the next attempt (passive declare → declare-ok):"
jq . <<<"$(log_entry "$LOG_B" "$MSG_VERIFIED" ".exchange == \"$EXCHANGE\"")"

wait_for_log "$LOG_B" "$MSG_CONSUMERS" 30 || fail "consumers never started"

DEADLINE=$((SECONDS + 30))
until curl -fsS -o /dev/null "$API_BASE/health" 2>/dev/null; do
    app_alive || fail "the app exited after verifying the exchange"
    ((SECONDS < DEADLINE)) || fail "$API_BASE/health never answered"
    sleep 1
done
echo
echo "✅ startup completed without a restart: consumers started, $API_BASE/health answers."

# --- 3. a partner event is consumed ---------------------------------------

section "3/3  The partner publishes — the typed consumer receives it"

EVENT_ID="demo-$(date +%s)"
EVENT="$(jq -nc --arg id "$EVENT_ID" --arg at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    '{eventId: $id, partnerId: "acme-supply", sku: "SKU-001", quantity: 12, occurredAt: $at}')"
echo "event: $EVENT"

RESPONSE="$(jq -n --arg p "$EVENT" --arg rk "$ROUTING_KEY" --arg id "$EVENT_ID" \
    '{properties: {content_type: "application/json", message_id: $id, type: $rk},
      routing_key: $rk, payload: $p, payload_encoding: "string"}' \
    | curl -sS -K "$CURL_CFG" -H 'content-type: application/json' \
        -X POST "$RABBIT_MGMT/api/exchanges/$RABBIT_VHOST/$EXCHANGE/publish" \
        --data-binary @- || true)"
[[ "$(jq -r 'if type == "object" then (.routed // empty) else empty end' <<<"$RESPONSE" 2>/dev/null || true)" == "true" ]] \
    || fail "publish to '$EXCHANGE' was not routed — broker said: ${RESPONSE:-<no response>}"
echo "published to '$EXCHANGE' with routing key '$ROUTING_KEY' (routed=true)"

wait_for_log "$LOG_B" "$MSG_CONSUMED" 20 ".eventId == \"$EVENT_ID\"" \
    || fail "the consumer never logged event '$EVENT_ID'"
echo
echo "consumer log line:"
jq . <<<"$(log_entry "$LOG_B" "$MSG_CONSUMED" ".eventId == \"$EVENT_ID\"")"
echo
echo "✅ decoded, validated and handled — from an exchange this service never declared."

echo
echo "Cleaning up: stopping the app and deleting '$EXCHANGE' and the feed queues this"
echo "run created."
