#!/usr/bin/env bash
# scripts/consumer-readiness-demo.sh
#
# Readiness that fails closed on a stalled consumer (go-bricks v0.65.0, #1686 +
# #1684, ADR-114).
#
# `messaging.consumers.critical: true` makes GET /api/v1/ready answer 503 once a
# declared AMQP consumer is unsubscribed AND its supervisor has failed 5
# re-subscribes in a row — the same streak at which the framework's
# "Consumer re-subscribe attempt failed" log turns WARN. A reconnect that
# recovers inside the streak never reaches the verdict. config.development.yaml
# leaves the key OFF: it also judges the publisher arm critically, so any
# publisher not-ready moment answers 503 at once. This script turns it on for
# the one app process it boots, and for nothing else.
#
# What it does:
#   1. builds and boots its OWN app with MESSAGING_CONSUMERS_CRITICAL=true, plus
#      the access-controlled /_sys/health-debug view on loopback only — the 503
#      body carries no statistics, so that is where the streak stays visible;
#   2. finds the connection behind the payments.authorized consumer through the
#      management API, and records the app user's permissions on the vhost with
#      `rabbitmqctl list_user_permissions`;
#   3. revokes READ on payments.authorized ONLY — configure and write are
#      untouched, every other queue and stream stays readable — and closes that
#      one connection, so the consumer must re-subscribe and the broker refuses
#      its basic.consume with 403 ACCESS_REFUSED;
#   4. polls /ready while messaging_stats.consumer_max_fail_streak climbs on the
#      200 body, until the verdict turns 503 at the threshold;
#   5. restores the EXACT recorded permissions and polls until the consumer is
#      subscribed again (consumer_resubscribes +1) and /ready is back to 200.
#
# Why not stop the broker? With the key on, the publisher arm is critical too:
# a broker stop flips /ready to 503 immediately — the consumer arm is never what
# you see — and every product write would stall on its streams publish for up
# to the 2s publish timeout. Revoking one queue's read keeps the broker, the
# publisher and the streams lane healthy, which isolates the consumer arm.
#
# Broker authz for the app user is changed while this runs (typically one to
# three minutes). The recorded permissions are restored on EVERY exit — success,
# failure or Ctrl-C (trap EXIT). Only a SIGKILL skips that, so the exact restore
# command is printed before anything changes. Never point this at a shared
# broker.
#
# Prerequisites:
#   make docker-up      # RabbitMQ (management API) + postgres
#   make migrate        # outbox + inbox ledgers the app needs at boot
#   make generate-keys  # keystore DER files the app loads at boot
#   stop any app already running (make run) — this script boots its own and
#   refuses to start while the port is taken
#
# Overrides (env): APP_URL, SERVER_PORT, RABBIT_MGMT, RABBIT_USER, RABBIT_PASS,
#                  RABBIT_VHOST, RABBIT_CONTAINER, BOOT_TIMEOUT, GIVEUP_TIMEOUT,
#                  RECOVER_TIMEOUT, POLL_INTERVAL. Every other variable in the
#                  caller's environment (DATABASE_PORT, MESSAGING_BROKER_URL, ...)
#                  reaches the booted app unchanged, so a stack on alternate
#                  ports works as long as those overrides are exported.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# The app reads config.development.yaml and certs/ relative to its working
# directory, so it must start from the repo root.
cd "$SCRIPT_DIR/.."

# Endpoint guard + credential-file writer, shared with the sealed-message demos.
# Resolved from SCRIPT_DIR, so the cd above cannot move it.
# shellcheck source-path=SCRIPTDIR source=lib/rabbitmq-mgmt.sh
source "$SCRIPT_DIR/lib/rabbitmq-mgmt.sh"

RABBIT_MGMT="${RABBIT_MGMT:-http://127.0.0.1:15672}"
RABBIT_USER="${RABBIT_USER:-guest}"
RABBIT_PASS="${RABBIT_PASS:-guest}"
RABBIT_VHOST="${RABBIT_VHOST:-%2F}"   # default vhost "/" percent-encoded
RABBIT_CONTAINER="${RABBIT_CONTAINER:-go-bricks-rabbitmq}"

BOOT_TIMEOUT="${BOOT_TIMEOUT:-90}"
# Worst case to the verdict: the four backoffs before attempt 5 are full jitter
# under 10s, 20s, 40s and 60s (5s floor doubling, 60s cap) — about 130s, plus
# the consumer client's own reconnect.
GIVEUP_TIMEOUT="${GIVEUP_TIMEOUT:-240}"
# Once past the threshold each backoff can reach the 60s cap before the next
# attempt notices the restored permission.
RECOVER_TIMEOUT="${RECOVER_TIMEOUT:-180}"
POLL_INTERVAL="${POLL_INTERVAL:-2}"

# Topology — must match internal/modules/payments/module.go (queueName).
QUEUE="payments.authorized"
# Everything the default dev grant ('.*') allows, except that one queue. Erlang
# `re` is PCRE, so the negative lookahead is honored.
REVOKED_READ='^(?!payments\.authorized$).*'
# Framework constants (messaging/registry.go, app/readiness.go at v0.67.0).
GIVEUP_STREAK=5
CONSUMER_ARM_ERROR="consumer re-subscribe exhausted"

# --- helpers --------------------------------------------------------------

section() {
    # No truncation: `cut -c` counts BYTES under LC_ALL=C and would slice these
    # multi-byte rule characters in half.
    printf '\n── %s ──────────────────────────\n' "$*"
}

fail() {
    echo "❌ $*" >&2
    exit 1
}

for tool in curl jq go docker; do
    command -v "$tool" >/dev/null 2>&1 || fail "$tool is required but not installed"
done

[[ "$RABBIT_VHOST" =~ ^[A-Za-z0-9._~%-]+$ ]] \
    || fail "RABBIT_VHOST='$RABBIT_VHOST' must be percent-encoded (the default vhost \"/\" is %2F)"
# rabbitmqctl takes the vhost name, the management API its encoded form.
VHOST_NAME="$(printf '%b' "${RABBIT_VHOST//%/\\x}")"

# The app this script boots listens on SERVER_PORT (framework key server.port)
# and is polled at APP_URL; each defaults from the other so they cannot drift.
APP_URL="${APP_URL:-}"
APP_URL="${APP_URL%/}"
URL_PORT=""
if [[ "$APP_URL" =~ ^http://[^/:]+:([0-9]+)$ ]]; then
    URL_PORT="${BASH_REMATCH[1]}"
elif [[ -n "$APP_URL" ]]; then
    fail "APP_URL='$APP_URL' must look like http://<host>:<port> — it is the app this script boots"
fi
SERVER_PORT="${SERVER_PORT:-${URL_PORT:-8080}}"
[[ -z "$URL_PORT" || "$URL_PORT" == "$SERVER_PORT" ]] \
    || fail "APP_URL port ($URL_PORT) and SERVER_PORT ($SERVER_PORT) disagree"
APP_URL="${APP_URL:-http://127.0.0.1:${SERVER_PORT}}"
API_BASE="$APP_URL/api/v1"
# Debug endpoints hang off the URL root (server.RootGroup), outside
# server.path.base.
DEBUG_HEALTH_URL="$APP_URL/_sys/health-debug"

# Must precede the credential curls; it sits here rather than beside the
# defaults because fail() is defined above.
guard_mgmt_endpoint "$RABBIT_MGMT"

# --- scratch space, credentials and the one cleanup path ------------------

WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/consumer-readiness.XXXXXX")"
CURL_CFG="$(mktemp "$WORK_DIR/curl.XXXXXX")"
APP_BIN="$WORK_DIR/go-bricks-demo-project"
APP_LOG="$WORK_DIR/app.log"
READY_BODY="$WORK_DIR/ready.json"
DEBUG_BODY="$WORK_DIR/health-debug.json"

APP_PID=""
PERMS_RECORDED=0
APP_USER=""
ORIG_CONF=""
ORIG_WRITE=""
ORIG_READ=""
RESTORE_HINT=""
REVOKE_IN_FLIGHT=0

# rmqctl ARGS... — rabbitmqctl inside the broker container.
rmqctl() {
    docker exec "$RABBIT_CONTAINER" rabbitmqctl "$@"
}

# perms_match — the broker, read back, holds exactly the recorded set for the
# app user on the vhost. An exit status alone proves only that one
# set_permissions call ran, not which of two racing calls landed last.
perms_match() {
    rmqctl -q list_user_permissions --formatter json "$APP_USER" 2>/dev/null \
        | jq -e --arg v "$VHOST_NAME" --arg c "$ORIG_CONF" --arg w "$ORIG_WRITE" --arg r "$ORIG_READ" \
            '[.[] | select(.vhost == $v)] | first
             | .configure == $c and .write == $w and .read == $r' >/dev/null 2>&1
}

# Restore first (the one step that must not be skipped), then stop the app,
# then drop scratch files. The log survives a failed run for diagnosis.
cleanup() {
    local status=$?
    trap - EXIT
    # A second Ctrl-C would otherwise run `exit 130` in the middle of this
    # function and skip the restore, the app stop and the removal of the
    # credential file. Ignoring the signals also covers the docker exec children
    # below; every wait in here is bounded.
    trap '' INT TERM HUP
    set +e
    if [[ "$PERMS_RECORDED" == 1 ]]; then
        # Interrupted mid-revoke: the signal killed the docker CLI, but its
        # rabbitmqctl keeps running inside the container and could land after
        # the restore below. Give it time to finish first.
        [[ "$REVOKE_IN_FLIGHT" == 1 ]] && sleep 3
        # Unconditional: re-applying the recorded set is a no-op when step 5
        # already restored it. Only a matching read-back counts as restored;
        # a mismatch re-applies the set, a bounded number of times.
        local attempt restored=0
        for attempt in 1 2 3 4 5; do
            rmqctl set_permissions -p "$VHOST_NAME" "$APP_USER" \
                "$ORIG_CONF" "$ORIG_WRITE" "$ORIG_READ" >/dev/null 2>&1
            if perms_match; then
                restored=1
                break
            fi
            ((attempt < 5)) && sleep 2
        done
        if ((restored)); then
            echo "🔓 broker permissions for '$APP_USER' on vhost '$VHOST_NAME' read back as the recorded set" >&2
        else
            echo "❌ could NOT confirm the broker permissions were restored — run: $RESTORE_HINT" >&2
        fi
    fi
    if [[ -n "$APP_PID" ]] && kill -0 "$APP_PID" 2>/dev/null; then
        kill -TERM "$APP_PID" 2>/dev/null
        local waited=0
        while kill -0 "$APP_PID" 2>/dev/null && ((waited < 30)); do
            sleep 1
            waited=$((waited + 1))
        done
        kill -KILL "$APP_PID" 2>/dev/null
        wait "$APP_PID" 2>/dev/null
    fi
    rm -f "$CURL_CFG" "$APP_BIN"
    # A preflight refusal never started the app, so there is no log to keep.
    if [[ $status -eq 0 || ! -s "$APP_LOG" ]]; then
        rm -rf "$WORK_DIR"
    else
        echo "ℹ️  app log kept at $APP_LOG" >&2
    fi
    exit "$status"
}
trap cleanup EXIT
# Signals become ordinary exits so the EXIT trap — and the restore — runs.
trap 'exit 130' INT
trap 'exit 143' TERM
trap 'exit 129' HUP

write_curl_cfg "$CURL_CFG" "$RABBIT_MGMT" "$RABBIT_USER" "$RABBIT_PASS"

# mgmt ARGS... — a management API call with the 0600 credential file.
mgmt() {
    curl -sS --max-time 10 -K "$CURL_CFG" "$@"
}

# http_status URL OUT — body into OUT, HTTP status on stdout ("000" when
# nothing answered).
http_status() {
    curl -sS -o "$2" -w '%{http_code}' --max-time 5 "$1" 2>/dev/null || true
}

# debug_messaging — the messaging component of /_sys/health-debug on stdout,
# or a non-zero return when the debug view does not answer.
debug_messaging() {
    [[ "$(http_status "$DEBUG_HEALTH_URL" "$DEBUG_BODY")" == 200 ]] || return 1
    jq -e '.data.components.messaging' "$DEBUG_BODY" 2>/dev/null
}

# app_log_lines JQ_FILTER — the app log's JSON lines matching the filter. The
# app is started with log.output.format=json, so every framework line parses.
app_log_lines() {
    jq -Rr "fromjson? | $1" "$APP_LOG" 2>/dev/null || true
}

# app_failed MESSAGE — surface the app's own warnings and errors (message and
# error fields only), then fail.
app_failed() {
    echo "last warnings/errors in the app log:" >&2
    app_log_lines 'select(.level == "warn" or .level == "error" or .level == "fatal" or .level == "panic")
        | "  [\(.level)] \(.message // "")\(if .error then " — \(.error)" else "" end)"' | tail -n 15 >&2
    fail "$1"
}

ready_line() {
    jq -r '"/ready 200  messaging=\(.messaging)  subscribed \(.messaging_stats.subscribed_consumers)/\(.messaging_stats.declared_consumers)  consumer_max_fail_streak=\(.messaging_stats.consumer_max_fail_streak)  consumer_resubscribes=\(.messaging_stats.consumer_resubscribes)"' "$READY_BODY"
}

stats_json() {
    jq '{status, messaging, messaging_stats: (.messaging_stats | {status, declared_consumers, subscribed_consumers, consumer_max_fail_streak, consumer_resubscribes, consumer_registries, active_publishers})}' "$READY_BODY"
}

# --- preflight ------------------------------------------------------------

[[ "$(docker inspect -f '{{.State.Running}}' "$RABBIT_CONTAINER" 2>/dev/null || true)" == true ]] \
    || fail "container '$RABBIT_CONTAINER' is not running — run 'make docker-up' (or set RABBIT_CONTAINER)"

mgmt -f -o /dev/null "$RABBIT_MGMT/api/overview" 2>/dev/null \
    || fail "RabbitMQ management API not reachable at $RABBIT_MGMT — run 'make docker-up' (or set RABBIT_MGMT)"

# curl exits 7 when nothing accepts the connection: the only answer that means
# the port is free for the app this script boots.
port_rc=0
curl -s -o /dev/null --max-time 2 "$APP_URL/" || port_rc=$?
[[ $port_rc -eq 7 ]] \
    || fail "something already listens on $APP_URL — stop it (e.g. the 'make run' terminal) first; this script boots its own app with messaging.consumers.critical on"

[[ -d certs ]] || fail "certs/ not found — run 'make generate-keys' first"

# --- 1. build and boot ----------------------------------------------------

section "1/5  Build and boot the app with messaging.consumers.critical=true"

go build -o "$APP_BIN" ./cmd/api/

# Same launch as `make run`, plus:
#   MESSAGING_CONSUMERS_CRITICAL  the key under demonstration (messaging.consumers.critical)
#   LOG_OUTPUT_FORMAT=json        so this script can read the re-subscribe log lines
#   DEBUG_*                       /_sys/health-debug only, loopback only; the
#                                 goroutine, gc and info endpoints stay off
(
    unset DEBUG
    export APP_ENV=development CORS_DEV_WILDCARD=true SERVER_PORT
    export MESSAGING_CONSUMERS_CRITICAL=true
    export LOG_OUTPUT_FORMAT=json
    export DEBUG_ENABLED=true DEBUG_ALLOWEDIPS=127.0.0.1,::1 DEBUG_ENDPOINTS_HEALTH=true \
        DEBUG_ENDPOINTS_GOROUTINES=false DEBUG_ENDPOINTS_GC=false DEBUG_ENDPOINTS_INFO=false
    exec "$APP_BIN"
) >"$APP_LOG" 2>&1 &
APP_PID=$!
echo "app PID $APP_PID on $APP_URL (log: $APP_LOG)"

deadline=$((SECONDS + BOOT_TIMEOUT))
code="000"
while :; do
    kill -0 "$APP_PID" 2>/dev/null || app_failed "the app exited during startup"
    code="$(http_status "$API_BASE/ready" "$READY_BODY")"
    if [[ "$code" == 200 ]] && jq -e '.messaging_stats.declared_consumers >= 1
            and .messaging_stats.subscribed_consumers == .messaging_stats.declared_consumers' \
            "$READY_BODY" >/dev/null 2>&1; then
        break
    fi
    ((SECONDS < deadline)) || app_failed "no subscribed consumer on /ready after ${BOOT_TIMEOUT}s (last HTTP $code)"
    sleep 1
done

echo
echo "GET $API_BASE/ready → 200 (the consumer counters are go-bricks v0.65.0, #1684):"
stats_json
BASE_RESUBSCRIBES="$(jq -r '.messaging_stats.consumer_resubscribes' "$READY_BODY")"

DEBUG_OK=0
if MESSAGING_DEBUG="$(debug_messaging)"; then
    DEBUG_OK=1
    echo
    echo "GET $DEBUG_HEALTH_URL → messaging component:"
    jq '{status, critical}' <<<"$MESSAGING_DEBUG"
    [[ "$(jq -r '.critical' <<<"$MESSAGING_DEBUG")" == true ]] \
        || fail "messaging is not critical — MESSAGING_CONSUMERS_CRITICAL did not reach the app"
    echo "  critical=true ← the override took: this kind can now fail /ready"
else
    echo
    echo "⚠️  $DEBUG_HEALTH_URL did not answer — continuing with /ready alone"
fi

# --- 2. find the consumer and record permissions --------------------------

section "2/5  Locate the '$QUEUE' consumer and record broker permissions"

# The management API lists a new consumer on its next stats tick, and can list
# it for a tick with empty channel_details ({}: no connection name yet). It can
# also still list a just-stopped app's consumer for a moment. So poll until
# exactly one consumer on the queue resolves to a live connection.
deadline=$((SECONDS + 30))
count=0
CONN_NAME=""
CONN_JSON=""
while :; do
    consumers="$(mgmt "$RABBIT_MGMT/api/consumers/$RABBIT_VHOST" 2>/dev/null || echo '[]')"
    count="$(jq --arg q "$QUEUE" '[.[]? | select(.queue.name == $q)] | length' <<<"$consumers" 2>/dev/null || echo 0)"
    if ((count == 1)); then
        CONN_NAME="$(jq -r --arg q "$QUEUE" '.[] | select(.queue.name == $q)
            | .channel_details | objects | .connection_name // empty' <<<"$consumers" 2>/dev/null || true)"
        if [[ -n "$CONN_NAME" ]]; then
            CONN_ENC="$(jq -rn --arg s "$CONN_NAME" '$s | @uri')"
            CONN_JSON="$(mgmt -f "$RABBIT_MGMT/api/connections/$CONN_ENC" 2>/dev/null)" && break
        fi
    fi
    if ((SECONDS >= deadline)); then
        ((count <= 1)) \
            || fail "expected exactly one consumer on '$QUEUE' (this script's app), found $count — stop the other app instances first"
        ((count == 1)) || fail "the management API lists no consumer on '$QUEUE' after 30s"
        fail "the '$QUEUE' consumer did not resolve to a live connection within 30s — re-run"
    fi
    sleep 1
done

CONSUMER_TAG="$(jq -r --arg q "$QUEUE" '.[] | select(.queue.name == $q) | .consumer_tag' <<<"$consumers")"
APP_USER="$(jq -r '.user // empty' <<<"$CONN_JSON")"
[[ -n "$APP_USER" ]] || fail "could not read the user of connection '$CONN_NAME'"

echo "consumer   : tag '$CONSUMER_TAG' on '$QUEUE'"
echo "connection : $CONN_NAME   ← the consumer registry's own AMQP connection;"
echo "             the publisher pool dials a separate one, which stays up"
echo "app user   : $APP_USER on vhost '$VHOST_NAME'"

# The permissions are rewritten through RABBIT_CONTAINER, but the connection
# was found through RABBIT_MGMT: prove they are the same broker first.
rmqctl -q list_connections name --formatter json 2>/dev/null \
    | jq -e --arg n "$CONN_NAME" 'any(.[]; .name == $n)' >/dev/null 2>&1 \
    || fail "container '$RABBIT_CONTAINER' does not hold connection '$CONN_NAME' — RABBIT_CONTAINER must be the broker behind RABBIT_MGMT ($RABBIT_MGMT)"

PERMS_JSON="$(rmqctl -q list_user_permissions --formatter json "$APP_USER")" \
    || fail "rabbitmqctl list_user_permissions failed for '$APP_USER'"
PERMS_ROW="$(jq -c --arg v "$VHOST_NAME" '[.[] | select(.vhost == $v)] | first // empty' <<<"$PERMS_JSON" 2>/dev/null || true)"
[[ -n "$PERMS_ROW" ]] || fail "'$APP_USER' holds no permissions on vhost '$VHOST_NAME'"
ORIG_CONF="$(jq -r '.configure' <<<"$PERMS_ROW")"
ORIG_WRITE="$(jq -r '.write' <<<"$PERMS_ROW")"
ORIG_READ="$(jq -r '.read' <<<"$PERMS_ROW")"

echo
echo "recorded (rabbitmqctl list_user_permissions):"
printf '  configure=%s  write=%s  read=%s\n' "$ORIG_CONF" "$ORIG_WRITE" "$ORIG_READ"

# Rewriting a custom read pattern would need to merge regexes; the demo only
# narrows the default dev grant and refuses anything else.
[[ "$ORIG_READ" == ".*" ]] \
    || fail "read pattern is '$ORIG_READ', not the default '.*' — refusing to rewrite a custom permission set"

RESTORE_HINT="docker exec $RABBIT_CONTAINER rabbitmqctl set_permissions -p '$VHOST_NAME' '$APP_USER' '$ORIG_CONF' '$ORIG_WRITE' '$ORIG_READ'"
PERMS_RECORDED=1
echo
echo "Restored automatically on exit. If this script is killed with SIGKILL, run:"
echo "  $RESTORE_HINT"

# --- 3. revoke read on one queue and force a re-subscribe -----------------

section "3/5  Revoke READ on '$QUEUE' only, then force a re-subscribe"

REVOKE_IN_FLIGHT=1
rmqctl set_permissions -p "$VHOST_NAME" "$APP_USER" "$ORIG_CONF" "$ORIG_WRITE" "$REVOKED_READ" >/dev/null
REVOKE_IN_FLIGHT=0
printf '  configure=%s  write=%s  read=%s\n' "$ORIG_CONF" "$ORIG_WRITE" "$REVOKED_READ"
echo "  ← basic.consume on '$QUEUE' now answers 403 ACCESS_REFUSED; publishing,"
echo "    re-declaring the topology and every other queue and stream are untouched"

# The broker checks permissions when a consumer subscribes, not per delivery,
# so the live subscription must be dropped for the revocation to bite.
close_status="$(mgmt -o /dev/null -w '%{http_code}' -X DELETE \
    -H 'X-Reason: consumer-readiness-demo: forcing a consumer re-subscribe' \
    "$RABBIT_MGMT/api/connections/$CONN_ENC" 2>/dev/null || true)"
[[ "$close_status" == 204 ]] \
    || fail "closing connection '$CONN_NAME' returned HTTP ${close_status:-<no response>}"
echo
echo "closed connection '$CONN_NAME' (HTTP 204) — the consumer's delivery channel"
echo "is gone and its supervisor starts re-subscribing"

# --- 4. watch the streak climb until /ready fails closed ------------------

section "4/5  Poll /ready while the re-subscribe streak climbs to $GIVEUP_STREAK"

echo "Give-up threshold: $GIVEUP_STREAK consecutive failed re-subscribes (framework constant,"
echo "shared with the WARN escalation). Backoff is full jitter from a 5s floor, capped"
echo "at 60s, so expect the verdict in roughly 30s to 2.5min."
echo

T0=$SECONDS
deadline=$((SECONDS + GIVEUP_TIMEOUT))
last=""
while :; do
    kill -0 "$APP_PID" 2>/dev/null || app_failed "the app exited while the consumer was refused"
    code="$(http_status "$API_BASE/ready" "$READY_BODY")"
    [[ "$code" == 503 ]] && break
    if [[ "$code" == 200 ]]; then
        line="$(ready_line)"
    else
        line="/ready $code"
    fi
    if [[ "$line" != "$last" ]]; then
        printf '  [+%3ds] %s\n' $((SECONDS - T0)) "$line"
        last="$line"
    fi
    ((SECONDS < deadline)) || fail "/ready did not turn 503 within ${GIVEUP_TIMEOUT}s"
    sleep "$POLL_INTERVAL"
done
printf '  [+%3ds] /ready 503\n' $((SECONDS - T0))

echo
echo "GET $API_BASE/ready → 503 (fixed body: no statistics, no queue name — ADR-048):"
jq . "$READY_BODY"
[[ "$(jq -r '.status' "$READY_BODY")" == "not ready" && "$(jq -r '.messaging // empty' "$READY_BODY")" == unhealthy ]] \
    || fail "the 503 was not raised by the messaging kind"

if [[ "$DEBUG_OK" == 1 ]]; then
    MESSAGING_DEBUG="$(debug_messaging)" || fail "$DEBUG_HEALTH_URL stopped answering"
    echo
    echo "GET $DEBUG_HEALTH_URL → messaging component (where the streak stays visible):"
    jq '{status, critical, error, details: (.details | {declared_consumers, subscribed_consumers, consumer_max_fail_streak, consumer_resubscribes})}' <<<"$MESSAGING_DEBUG"
    DEBUG_ERROR="$(jq -r '.error // empty' <<<"$MESSAGING_DEBUG")"
    [[ "$DEBUG_ERROR" == "$CONSUMER_ARM_ERROR" ]] \
        || fail "the 503 came from '$DEBUG_ERROR', not the consumer arm ('$CONSUMER_ARM_ERROR') — is the broker up?"
    echo "  error='$CONSUMER_ARM_ERROR' ← the consumer arm, not the publisher's"
    echo "  'publisher not ready' — the broker and the publisher never went down"
fi

echo
echo "App log — the supervisor's failed attempts (Debug for 1-4, WARN from $GIVEUP_STREAK):"
# The WARN is written just after the streak moves, so a poll can see the 503
# first; give the log a moment to catch up.
WARN_LINES=""
for _ in 1 2 3 4 5; do
    WARN_LINES="$(app_log_lines 'select(.level == "warn" and .message == "Consumer re-subscribe attempt failed, will retry")
        | "  [warn] attempt=\(.attempt) amqp_reply_code=\(.amqp_reply_code // "-") \(.amqp_reply_text // .error // "")"' | tail -n 3)"
    [[ -n "$WARN_LINES" ]] && break
    sleep 1
done
echo "${WARN_LINES:-  (none in the log — check $APP_LOG)}"

# --- 5. restore and watch the recovery ------------------------------------

section "5/5  Restore the recorded permissions and watch the recovery"

rmqctl set_permissions -p "$VHOST_NAME" "$APP_USER" "$ORIG_CONF" "$ORIG_WRITE" "$ORIG_READ" >/dev/null
RESTORED_READ="$(rmqctl -q list_user_permissions --formatter json "$APP_USER" \
    | jq -r --arg v "$VHOST_NAME" '[.[] | select(.vhost == $v)] | first | .read // empty' 2>/dev/null || true)"
[[ "$RESTORED_READ" == "$ORIG_READ" ]] \
    || fail "restore did not take (read='$RESTORED_READ') — the exit trap retries it"
printf '  configure=%s  write=%s  read=%s  ← the recorded set\n' "$ORIG_CONF" "$ORIG_WRITE" "$ORIG_READ"
echo "  the next scheduled re-subscribe attempt (≤60s away) will succeed"
echo

T0=$SECONDS
deadline=$((SECONDS + RECOVER_TIMEOUT))
last=""
while :; do
    kill -0 "$APP_PID" 2>/dev/null || app_failed "the app exited during recovery"
    code="$(http_status "$API_BASE/ready" "$READY_BODY")"
    if [[ "$code" == 200 ]]; then
        line="$(ready_line)"
        if jq -e '.messaging_stats.declared_consumers >= 1
                and .messaging_stats.subscribed_consumers == .messaging_stats.declared_consumers
                and .messaging_stats.consumer_max_fail_streak == 0' "$READY_BODY" >/dev/null 2>&1; then
            printf '  [+%3ds] %s\n' $((SECONDS - T0)) "$line"
            break
        fi
    else
        line="/ready $code"
    fi
    if [[ "$line" != "$last" ]]; then
        printf '  [+%3ds] %s\n' $((SECONDS - T0)) "$line"
        last="$line"
    fi
    ((SECONDS < deadline)) || fail "the consumer did not re-subscribe within ${RECOVER_TIMEOUT}s"
    sleep "$POLL_INTERVAL"
done

echo
echo "GET $API_BASE/ready → 200:"
stats_json
FINAL_RESUBSCRIBES="$(jq -r '.messaging_stats.consumer_resubscribes' "$READY_BODY")"
((FINAL_RESUBSCRIBES > BASE_RESUBSCRIBES)) \
    || fail "consumer_resubscribes did not move ($BASE_RESUBSCRIBES → $FINAL_RESUBSCRIBES)"

echo
echo "App log — the re-subscribe that ended the outage:"
RESUB_LINE=""
for _ in 1 2 3 4 5; do
    RESUB_LINE="$(app_log_lines 'select(.message == "Consumer re-subscribed after delivery channel closed")
        | "  [\(.level)] attempt=\(.attempt) \(.message)"' | tail -n 1)"
    [[ -n "$RESUB_LINE" ]] && break
    sleep 1
done
echo "${RESUB_LINE:-  (none in the log — check $APP_LOG)}"

echo
echo "✅ /ready failed closed once '$QUEUE' gave up re-subscribing, and came back"
echo "   by itself once the consumer re-subscribed (consumer_resubscribes $BASE_RESUBSCRIBES → $FINAL_RESUBSCRIBES)."
echo
echo "Operating the key (ADR-114):"
echo "  * it is opt-in and per environment: the publisher arm turns critical too,"
echo "    so a broker outage answers 503 at once, with no streak;"
echo "  * gate LIVENESS on /health, never on /ready, or a broker incident becomes"
echo "    a restart loop;"
echo "  * anyone who can revoke consume (or delete the queue) can now take every"
echo "    replica out of rotation — that is the trade the key makes;"
echo "  * stream consumers (the activity projection) are not covered: they are"
echo "    judged by the separate streams kind, which is never critical."
