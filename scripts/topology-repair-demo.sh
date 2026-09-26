#!/usr/bin/env bash
# scripts/topology-repair-demo.sh
#
# Exchange loss under a live app, repaired without a restart (go-bricks v0.67.0,
# #1776/#1779, ADR-113 amendment).
#
# As of v0.67.0 every pooled publisher drives the topology redeclare pass, not
# only the consumer. Deleting an exchange under a running app therefore heals on
# the next publish: that publish takes the broker's 404 on the PUBLISHER's
# channel, the client opens a replacement channel, and that new channel wakes the
# registry, which replays every exchange, queue and binding it declared over its
# own connection. The publish itself retries on the new channel after a short
# backoff. Before v0.67.0 only the registry's own client could trigger a pass,
# and that client never rotated here, so every publish from then on failed with
# ErrPublishRetriesExhausted until someone restarted the process.
#
# The script shows both publishing paths heal:
#
#   1. payment-events (sealed payments): publish one payment and see it on the
#      consumerless payments.authorized.tap queue; delete the exchange; publish
#      again; see the exchange and both of its bindings come back, and a payment
#      published after the repair reach the tap.
#   2. product-events (outbox relay): delete it, create a product, and see the
#      exchange come back once the relay's next poll publishes into the hole.
#
# What it does NOT promise:
#
#   * The repair is not atomic. The pass declares exchanges, then queues, then
#     bindings, and the typed publisher sets no Mandatory flag. A publish that
#     lands after payment-events is back but before it is re-bound is acked by the
#     broker and dropped as unroutable, while the caller already got 202. If the
#     payment published into the hole never reaches the tap, that is this window,
#     reported as such. `make loadtest-topology-repair` measures it under load.
#   * product-events has no bound queue in this demo, so outbox product events
#     are unroutable by design. Its repair proves the relay's publishes are
#     confirmed again, not that anyone received them.
#   * The native streams lane (product-activity, port 5552) is not covered: it is
#     declared at startup only. A deleted stream comes back with an app restart.
#
# The payment request body carries a test PAN and is NEVER echoed: it reaches
# curl on stdin, so it is not in argv either. Only the order id, the status and
# the card's last four digits (from the API response) are printed.
#
# DEMO DATA ONLY. 4111111111111111 is the universally published Visa test PAN.
# Never put a real card number through this script.
#
# Prerequisites:
#   make docker-up      # RabbitMQ (management plugin on :15672) + postgres
#   make migrate        # outbox + inbox ledgers
#   make generate-keys  # payments sealing keys
#   make run            # app must be running, on go-bricks >= v0.67.0
#
# Overrides (env): APP_URL (app root, default http://localhost:8080), API_BASE
#                  (default $APP_URL/api/v1), RABBIT_MGMT, RABBIT_USER,
#                  RABBIT_PASS, RABBIT_VHOST, TAP_ATTEMPTS, REPAIR_TIMEOUT

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Endpoint guard + credential-file writer, shared with the sealed-message demos.
# shellcheck source-path=SCRIPTDIR source=lib/rabbitmq-mgmt.sh
source "$SCRIPT_DIR/lib/rabbitmq-mgmt.sh"

APP_URL="${APP_URL:-http://localhost:8080}"
API_BASE="${API_BASE:-${APP_URL%/}/api/v1}"
RABBIT_MGMT="${RABBIT_MGMT:-http://localhost:15672}"
RABBIT_USER="${RABBIT_USER:-guest}"
RABBIT_PASS="${RABBIT_PASS:-guest}"
RABBIT_VHOST="${RABBIT_VHOST:-%2F}"   # default vhost "/" percent-encoded
TAP_ATTEMPTS="${TAP_ATTEMPTS:-10}"    # tap reads, 0.5s apart
# Seconds to wait for a repair. The product path waits on the outbox relay's
# poll (outbox.pollinterval, 5s), so keep this well above it.
REPAIR_TIMEOUT="${REPAIR_TIMEOUT:-30}"

# Topology — must match internal/modules/payments/module.go,
# internal/modules/products/module.go and outbox.defaultexchange.
PAYMENT_EXCHANGE="payment-events"
PRODUCT_EXCHANGE="product-events"
ROUTING_KEY="payment.authorized"
QUEUE="payments.authorized"
TAP_QUEUE="payments.authorized.tap"

# See handlers.AuthorizePaymentRequest for the accepted shape. Built with a
# heredoc so the PAN never appears in any process's argv.
PAYMENT_PAYLOAD="$(cat <<'JSON'
{"amount": 4599, "currency": "USD", "card": {"pan": "4111111111111111", "expMonth": 12, "expYear": 2030, "holder": "ADA LOVELACE"}}
JSON
)"

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

for tool in curl jq; do
    command -v "$tool" >/dev/null 2>&1 || fail "$tool is required but not installed"
done

# GNU coreutils spells it --decode, older BSD/macOS base64 only knows -D.
if printf '' | base64 --decode >/dev/null 2>&1; then
    B64_DECODE=(base64 --decode)
elif printf '' | base64 -D >/dev/null 2>&1; then
    B64_DECODE=(base64 -D)
else
    B64_DECODE=(openssl base64 -d -A)
fi

# JOSE segments are base64url WITHOUT padding (RFC 7515 §2); translate the
# alphabet and re-pad before handing them to a standard base64 decoder.
b64url_decode() {
    local data="${1//-/+}"
    data="${data//_//}"
    case $(( ${#data} % 4 )) in
        2) data="${data}==" ;;
        3) data="${data}=" ;;
    esac
    printf '%s' "$data" | "${B64_DECODE[@]}"
}

# --- endpoint guard + credentials -----------------------------------------

guard_mgmt_endpoint "$RABBIT_MGMT"

# Both scratch files are created here so one trap owns the cleanup. The trap
# also flags an exchange this script deleted and never saw come back.
CURL_CFG="$(mktemp)"
RESPONSE_FILE="$(mktemp)"
AWAITING_REPAIR=""
on_exit() {
    rm -f "$CURL_CFG" "$RESPONSE_FILE"
    if [[ -n "$AWAITING_REPAIR" ]]; then
        echo "⚠️  '$AWAITING_REPAIR' may still be missing. Publish once more, or restart the app (make run), to re-declare it." >&2
    fi
}
trap on_exit EXIT
# Signals become ordinary exits, so the EXIT trap cleans up once and a Ctrl-C
# is never read as a repair that timed out.
trap 'exit 130' INT
trap 'exit 143' TERM
trap 'exit 129' HUP

write_curl_cfg "$CURL_CFG" "$RABBIT_MGMT" "$RABBIT_USER" "$RABBIT_PASS"

# --- broker helpers -------------------------------------------------------

exchange_status() {
    curl -sS -o /dev/null -w '%{http_code}' -K "$CURL_CFG" \
        "$RABBIT_MGMT/api/exchanges/$RABBIT_VHOST/$1" 2>/dev/null || true
}

exchange_exists() {
    [[ "$(exchange_status "$1")" == "200" ]]
}

# bound_to_payment_events QUEUE — the queue carries a payment-events binding on
# the payment routing key. The default-exchange binding every queue has is not it.
bound_to_payment_events() {
    curl -sS -K "$CURL_CFG" "$RABBIT_MGMT/api/queues/$RABBIT_VHOST/$1/bindings" 2>/dev/null \
        | jq -e --arg ex "$PAYMENT_EXCHANGE" --arg rk "$ROUTING_KEY" \
            'type == "array" and any(.[]; .source == $ex and .routing_key == $rk)' >/dev/null 2>&1
}

payment_topology_back() {
    exchange_exists "$PAYMENT_EXCHANGE" \
        && bound_to_payment_events "$QUEUE" \
        && bound_to_payment_events "$TAP_QUEUE"
}

delete_exchange() {
    local status
    status="$(curl -sS -o /dev/null -w '%{http_code}' -K "$CURL_CFG" \
        -X DELETE "$RABBIT_MGMT/api/exchanges/$RABBIT_VHOST/$1" || true)"
    [[ "$status" == "204" ]] || fail "could not delete exchange '$1' (HTTP ${status:-<no response>})"
}

# wait_for SECONDS CMD... — poll CMD every 0.2s until it succeeds or SECONDS
# pass. Prints the whole seconds it waited (bash's SECONDS, so +/- 1s).
wait_for() {
    local timeout="$1" start="$SECONDS"
    shift
    while ! "$@"; do
        ((SECONDS - start >= timeout)) && return 1
        sleep 0.2
    done
    echo "$((SECONDS - start))"
}

# tap_has_order ORDER_ID — drain what is on the tap (nothing consumes it) and
# report whether ORDER_ID is among it. The order id sits in the CLEAR part of
# the signed payload, so no key is needed to read it; the card stays sealed.
tap_has_order() {
    local want="$1" attempt response count i body order
    for ((attempt = 1; attempt <= TAP_ATTEMPTS; attempt++)); do
        response="$(curl -sS -K "$CURL_CFG" -H 'content-type: application/json' \
            -X POST "$RABBIT_MGMT/api/queues/$RABBIT_VHOST/$TAP_QUEUE/get" \
            --data-binary '{"count":10,"ackmode":"ack_requeue_false","encoding":"auto"}' || true)"
        jq -e 'type == "array"' >/dev/null 2>&1 <<<"$response" \
            || fail "queue '$TAP_QUEUE' not readable — management API said: ${response:-<no response>}"

        count="$(jq 'length' <<<"$response")"
        for ((i = 0; i < count; i++)); do
            if [[ "$(jq -r ".[$i].payload_encoding" <<<"$response")" == "base64" ]]; then
                body="$(jq -r ".[$i].payload" <<<"$response" | "${B64_DECODE[@]}")"
            else
                body="$(jq -r ".[$i].payload" <<<"$response")"
            fi
            order="$(b64url_decode "$(cut -d. -f2 <<<"$body")" 2>/dev/null \
                | jq -r '.orderId // empty' 2>/dev/null || true)"
            [[ -n "$order" && "$order" == "$want" ]] && return 0
        done
        sleep 0.5
    done
    return 1
}

# --- app helpers ----------------------------------------------------------

# authorize — POST one payment. Sets AUTH_STATUS, AUTH_SECONDS and ORDER_ID.
# The body reaches curl on stdin, so it is neither echoed nor in argv.
authorize() {
    local out
    out="$(printf '%s' "$PAYMENT_PAYLOAD" | curl -sS -o "$RESPONSE_FILE" -w '%{http_code} %{time_total}' \
        -H 'content-type: application/json' \
        -X POST "$API_BASE/payments/authorize" --data-binary @- || true)"
    AUTH_STATUS="${out%% *}"
    AUTH_SECONDS="${out##* }"
    ORDER_ID=""
    if [[ "$AUTH_STATUS" == 2* ]]; then
        ORDER_ID="$(jq -r '.data.orderId // .orderId // empty' "$RESPONSE_FILE" 2>/dev/null || true)"
    fi
}

# report_authorize — the order id, status and last four digits, never the body
# that was sent. On failure, only the API's error message.
report_authorize() {
    if [[ "$AUTH_STATUS" == 2* ]]; then
        echo "HTTP $AUTH_STATUS in ${AUTH_SECONDS}s — orderId=$ORDER_ID cardLast4=$(jq -r '.data.cardLast4 // .cardLast4 // "?"' "$RESPONSE_FILE" 2>/dev/null || echo '?')"
    else
        echo "HTTP ${AUTH_STATUS:-<no response>} in ${AUTH_SECONDS:-?}s — $(jq -r '.error.message // .message // "no error message"' "$RESPONSE_FILE" 2>/dev/null || echo 'no error message')"
    fi
}

# --- 0. preflight ---------------------------------------------------------

section "0/4  Preflight"

curl -fsS -o /dev/null "$API_BASE/health" 2>/dev/null \
    || fail "API not reachable at $API_BASE — start it with 'make run'"

curl -fsS -o /dev/null -K "$CURL_CFG" "$RABBIT_MGMT/api/overview" 2>/dev/null \
    || fail "RabbitMQ management API not reachable at $RABBIT_MGMT — run 'make docker-up'"

for exchange in "$PAYMENT_EXCHANGE" "$PRODUCT_EXCHANGE"; do
    exchange_exists "$exchange" \
        || fail "exchange '$exchange' is missing before the demo started — restart the app ('make run'); it declares its topology at boot"
done
payment_topology_back \
    || fail "'$QUEUE' / '$TAP_QUEUE' are not bound to '$PAYMENT_EXCHANGE' — restart the app ('make run')"

echo "app       : $API_BASE"
echo "broker    : $RABBIT_MGMT — '$PAYMENT_EXCHANGE' -> '$QUEUE' + '$TAP_QUEUE', and '$PRODUCT_EXCHANGE'"
echo "payload   : test PAN ending 1111, never echoed"

# --- 1. baseline ----------------------------------------------------------

section "1/4  Baseline: a payment reaches the tap"

# Nothing consumes the tap, so copies accumulate across runs. Purge first so the
# order-id checks below can only match what this run published. Only the tap
# copy is dropped; 'payments.authorized' is a different queue.
PURGE_STATUS="$(curl -sS -o /dev/null -w '%{http_code}' -K "$CURL_CFG" \
    -X DELETE "$RABBIT_MGMT/api/queues/$RABBIT_VHOST/$TAP_QUEUE/contents" || true)"
case "$PURGE_STATUS" in
    200 | 204) echo "drained '$TAP_QUEUE' (HTTP $PURGE_STATUS)" ;;
    *) fail "could not drain '$TAP_QUEUE' (HTTP ${PURGE_STATUS:-<no response>})" ;;
esac

authorize
report_authorize
[[ "$AUTH_STATUS" == 2* && -n "$ORDER_ID" ]] || fail "the baseline payment failed — check the app log"
tap_has_order "$ORDER_ID" \
    || fail "order $ORDER_ID never reached '$TAP_QUEUE' with the topology intact — check the app log for a publish error"
echo "✅ order $ORDER_ID is on '$TAP_QUEUE'"

# --- 2. delete payment-events and publish into the hole -------------------

section "2/4  Delete '$PAYMENT_EXCHANGE' under the live app, then publish"

delete_exchange "$PAYMENT_EXCHANGE"
AWAITING_REPAIR="$PAYMENT_EXCHANGE"
echo "DELETE '$PAYMENT_EXCHANGE' -> 204. Now: exchange HTTP $(exchange_status "$PAYMENT_EXCHANGE"), queues still there,"
echo "but deleting an exchange removes its bindings with it:"
for q in "$QUEUE" "$TAP_QUEUE"; do
    if bound_to_payment_events "$q"; then
        echo "  $q <- $PAYMENT_EXCHANGE : still bound (unexpected)"
    else
        echo "  $q <- $PAYMENT_EXCHANGE : GONE"
    fi
done

echo
echo "Publishing into the hole. This publish takes the broker's 404, which closes"
echo "the publisher's channel. Its replacement wakes the registry, which re-declares"
echo "the whole topology; the publish retries on the new channel."
authorize
report_authorize
TRIP_STATUS="$AUTH_STATUS"
TRIP_ORDER="$ORDER_ID"

if ! WAITED="$(wait_for "$REPAIR_TIMEOUT" payment_topology_back)"; then
    fail "'$PAYMENT_EXCHANGE' and its bindings did not come back within ${REPAIR_TIMEOUT}s. Is the app built on go-bricks >= v0.67.0? Before it, only a restart re-declares: run 'make run' again."
fi
AWAITING_REPAIR=""
echo
echo "✅ '$PAYMENT_EXCHANGE' is back, bound to '$QUEUE' and '$TAP_QUEUE' (${WAITED}s after the publish returned)."
echo "   No restart. The app log has 'Messaging topology redeclared on new channel'."

# --- 3. did the payments reach the tap? -----------------------------------

section "3/4  Delivery after the repair"

if [[ "$TRIP_STATUS" == 2* && -n "$TRIP_ORDER" ]]; then
    if tap_has_order "$TRIP_ORDER"; then
        echo "✅ the payment published into the hole (order $TRIP_ORDER) reached the tap:"
        echo "   its retry ran on the new channel after the pass had re-bound the queues."
    else
        echo "⚠️  order $TRIP_ORDER got HTTP $TRIP_STATUS but never reached the tap."
        echo "   That is the ack-and-drop window: its retry landed after '$PAYMENT_EXCHANGE' was"
        echo "   re-declared but before the bindings were, so the broker acked it and dropped"
        echo "   it as unroutable. The typed publisher sets no Mandatory flag, so nothing"
        echo "   told the caller. Reconcile payments authorized during a repair."
    fi
else
    echo "ℹ️  the payment published into the hole failed loudly (HTTP ${TRIP_STATUS:-<no response>}). Its outcome"
    echo "   is unknown to the caller, which is the honest answer; nothing claimed success."
fi

echo
echo "One more payment, now that the topology is whole:"
authorize
report_authorize
[[ "$AUTH_STATUS" == 2* && -n "$ORDER_ID" ]] || fail "the post-repair payment failed — the repair did not restore publishing"
tap_has_order "$ORDER_ID" \
    || fail "order $ORDER_ID did not reach '$TAP_QUEUE' after the repair"
echo "✅ order $ORDER_ID is on '$TAP_QUEUE': steady state restored."

# --- 4. product-events via the outbox relay -------------------------------

section "4/4  Delete '$PRODUCT_EXCHANGE'; the outbox relay's next publish repairs it"

delete_exchange "$PRODUCT_EXCHANGE"
AWAITING_REPAIR="$PRODUCT_EXCHANGE"
echo "DELETE '$PRODUCT_EXCHANGE' -> 204. Now: exchange HTTP $(exchange_status "$PRODUCT_EXCHANGE")."

PRODUCT_STATUS="$(printf '{"name":"topology-repair-demo %s","description":"make redeclare-demo","price":1}' "$(date +%s)" \
    | curl -sS -o "$RESPONSE_FILE" -w '%{http_code}' -H 'content-type: application/json' \
        -X POST "$API_BASE/products" --data-binary @- || true)"
[[ "$PRODUCT_STATUS" == "201" ]] || fail "POST /products returned HTTP ${PRODUCT_STATUS:-<no response>}"
PRODUCT_ID="$(jq -r '.data.id // .id // empty' "$RESPONSE_FILE")"
echo "POST /products -> 201 (id $PRODUCT_ID). The row and its product.created outbox event"
echo "commit in one transaction; the relay publishes it on its next poll (outbox.pollinterval)."

if ! WAITED="$(wait_for "$REPAIR_TIMEOUT" exchange_exists "$PRODUCT_EXCHANGE")"; then
    fail "'$PRODUCT_EXCHANGE' did not come back within ${REPAIR_TIMEOUT}s — check the app log for the outbox relay"
fi
AWAITING_REPAIR=""
echo "✅ '$PRODUCT_EXCHANGE' is back ${WAITED}s after the create, with no payment traffic to trip it."
echo "   The relay's publish took the 404, and its replacement channel drove a full pass."
echo "   Nothing is bound to '$PRODUCT_EXCHANGE' in this demo, so that event is confirmed"
echo "   by the broker and dropped as unroutable by design."

# The product only existed to drive the relay. Deleting it emits one more outbox
# event, which the now-repaired exchange simply confirms.
if [[ -n "$PRODUCT_ID" ]]; then
    DELETE_STATUS="$(curl -sS -o /dev/null -w '%{http_code}' -X DELETE "$API_BASE/products/$PRODUCT_ID" || true)"
    [[ "$DELETE_STATUS" == 2* ]] || echo "ℹ️  could not delete demo product $PRODUCT_ID (HTTP ${DELETE_STATUS:-<no response>}) — harmless"
fi

echo
echo "What heals: any AMQP exchange this app declares. The first publish to hit the"
echo "hole drives one redeclare pass that replays EVERY exchange, queue and binding."
echo "What does not: the native streams lane (product-activity, port 5552) is"
echo "declared at startup only; a deleted stream needs an app restart."
echo "The window: payments published mid-repair can get 202 and still be lost."
echo "Measure it under load: make loadtest-topology-repair"
