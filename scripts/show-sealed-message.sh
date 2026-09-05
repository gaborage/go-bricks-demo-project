#!/usr/bin/env bash
# scripts/show-sealed-message.sh
#
# Broker-visibility proof for the sealed-messages demo (go-bricks ADR-097).
#
# Publishes one PaymentAuthorized event through POST /api/v1/payments/authorize,
# then reads that message straight off the broker with the RabbitMQ management
# API and shows what an operator holding full queue access actually sees:
#
#   * delivery.Body is ONE RFC 7515 compact JWS — three dot-separated segments,
#     no readable business JSON at the top level
#   * its protected header carries typ=vnd.gobricks.sealed.v1+json (the only
#     sealed-message marker — there is no x-sealed AMQP header) plus the
#     concrete sign generation in `kid`
#   * the signed payload keeps orderId / amount / currency in the clear for
#     routing and DLQ triage, but the `card` member has been replaced IN PLACE
#     by a compact JWE (5 segments)
#   * the PAN appears NOWHERE in the body — asserted here, not just claimed
#
# Why a second queue? 'payments.authorized' has a live consumer that acks and
# removes each delivery within milliseconds, so there is nothing left to look
# at. 'payments.authorized.tap' is bound to the same exchange + routing key and
# has NO consumer, so a sealed copy waits there for inspection. Fetching from it
# with ackmode=ack_requeue_false consumes only the tap copy; the real consumer's
# delivery and its inbox ledger row are untouched. Because nothing drains it the
# tap is declared bounded (x-max-length + x-message-ttl, see the payments
# module), so a forgotten demo cannot fill the broker.
#
# Prerequisites:
#   make docker-up   # RabbitMQ (management plugin on :15672) + postgres
#   make migrate     # inbox ledger table (gobricks_inbox) — created by
#                    # migrations/V4__create_inbox_ledger.sql; the demo owns the
#                    # DDL, inbox.autocreatetable is false
#   make generate-keys
#   make run         # app must be running — it declares exchange + both queues
#
# Overrides (env): API_BASE, RABBIT_MGMT, RABBIT_USER, RABBIT_PASS, RABBIT_VHOST,
#                  TAP_QUEUE, TAP_ATTEMPTS, PAYMENT_PAYLOAD

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Endpoint guard + credential-file writer, shared with seal-event-demo.sh.
# shellcheck source-path=SCRIPTDIR source=lib/rabbitmq-mgmt.sh
source "$SCRIPT_DIR/lib/rabbitmq-mgmt.sh"

API_BASE="${API_BASE:-http://localhost:8080/api/v1}"
RABBIT_MGMT="${RABBIT_MGMT:-http://localhost:15672}"
RABBIT_USER="${RABBIT_USER:-guest}"
RABBIT_PASS="${RABBIT_PASS:-guest}"
RABBIT_VHOST="${RABBIT_VHOST:-%2F}"   # default vhost "/" percent-encoded
TAP_QUEUE="${TAP_QUEUE:-payments.authorized.tap}"
TAP_ATTEMPTS="${TAP_ATTEMPTS:-10}"

EXPECTED_TYP="vnd.gobricks.sealed.v1+json"
SIGN_FAMILY="payments-sign"       # Logical kid from the seal tag; the wire kid
                                  # is a GENERATION of it (payments-sign-v<N>)
ENCRYPT_FAMILY="payments-encrypt"

# The order id is NOT an input: the service mints it and returns it, and this
# script uses that value to prove the message on the broker is the one it just
# published (see handlers.AuthorizePaymentRequest for the accepted shape).
#
# DEMO DATA ONLY. 4111111111111111 is the universally published Visa test PAN —
# never put a real card number through this script.
PAYLOAD="${PAYMENT_PAYLOAD:-$(cat <<'JSON'
{
  "amount": 4599,
  "currency": "USD",
  "card": {
    "pan": "4111111111111111",
    "expMonth": 12,
    "expYear": 2030,
    "holder": "ADA LOVELACE"
  }
}
JSON
)}"

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
# Probe once so the decode helper stays a plain pipeline.
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

# --- endpoint guard -------------------------------------------------------

# Must precede the credential curls; it sits here rather than beside the
# defaults because fail() is defined above.
guard_mgmt_endpoint "$RABBIT_MGMT"

# --- credentials ----------------------------------------------------------

# Both scratch files are created here so one trap owns the cleanup.
CURL_CFG="$(mktemp)"
AUTH_RESPONSE_FILE="$(mktemp)"
trap 'rm -f "$CURL_CFG" "$AUTH_RESPONSE_FILE"' EXIT INT TERM HUP

write_curl_cfg "$CURL_CFG" "$RABBIT_MGMT" "$RABBIT_USER" "$RABBIT_PASS"

# --- preflight ------------------------------------------------------------

curl -fsS -o /dev/null "$API_BASE/health" 2>/dev/null \
    || fail "API not reachable at $API_BASE — start it with 'make run'"

curl -fsS -o /dev/null -K "$CURL_CFG" "$RABBIT_MGMT/api/overview" 2>/dev/null \
    || fail "RabbitMQ management API not reachable at $RABBIT_MGMT — run 'make docker-up'"

# --- 1. publish -----------------------------------------------------------

PAN="$(jq -r '.card.pan' <<<"$PAYLOAD")"
[[ -n "$PAN" && "$PAN" != "null" ]] || fail "payload has no .card.pan to assert against"

section "1/4  POST $API_BASE/payments/authorize"

# Nothing consumes the tap queue, so sealed copies accumulate across runs.
# Purge first and the message fetched below is unambiguously the one this run
# published — which the order-id correlation further down then asserts. Only
# the tap copy is dropped; 'payments.authorized' is a different queue.
PURGE_STATUS="$(curl -sS -o /dev/null -w '%{http_code}' -K "$CURL_CFG" \
    -X DELETE "$RABBIT_MGMT/api/queues/$RABBIT_VHOST/$TAP_QUEUE/contents" || true)"
case "$PURGE_STATUS" in
    200 | 204) echo "drained '$TAP_QUEUE' (HTTP $PURGE_STATUS) so only this run's message is left" ;;
    404) fail "queue '$TAP_QUEUE' does not exist — start the app with 'make run'; it declares the exchange and both queues at boot" ;;
    *) fail "could not drain '$TAP_QUEUE' (HTTP ${PURGE_STATUS:-<no response>})" ;;
esac

echo
echo "request body:"
jq '.card.pan = "<sent verbatim; masked in this echo only>"' <<<"$PAYLOAD"

AUTH_STATUS="$(curl -sS -o "$AUTH_RESPONSE_FILE" -w '%{http_code}' \
    -H 'content-type: application/json' \
    -X POST "$API_BASE/payments/authorize" \
    --data-binary "$PAYLOAD" || true)"

if [[ "$AUTH_STATUS" != 2* ]]; then
    echo "response body:" >&2
    cat "$AUTH_RESPONSE_FILE" >&2 || true
    fail "authorize returned HTTP ${AUTH_STATUS:-<no response>}"
fi
echo
echo "HTTP $AUTH_STATUS — API response (plaintext, this side of the broker;"
echo "the PAN does not come back out: only cardLast4 does):"
jq . "$AUTH_RESPONSE_FILE"

ORDER_ID="$(jq -r '.data.orderId // .orderId // empty' "$AUTH_RESPONSE_FILE")"

# --- 2. fetch the untouched copy from the tap queue -----------------------

section "2/4  GET one message from '$TAP_QUEUE' (no consumer)"

GET_BODY='{"count":1,"ackmode":"ack_requeue_false","encoding":"auto"}'
RAW_BODY=""

for ((attempt = 1; attempt <= TAP_ATTEMPTS; attempt++)); do
    RESPONSE="$(curl -sS -K "$CURL_CFG" \
        -H 'content-type: application/json' \
        -X POST "$RABBIT_MGMT/api/queues/$RABBIT_VHOST/$TAP_QUEUE/get" \
        --data-binary "$GET_BODY" || true)"

    if ! jq -e 'type == "array"' >/dev/null 2>&1 <<<"$RESPONSE"; then
        echo "management API said: $RESPONSE" >&2
        fail "queue '$TAP_QUEUE' not readable — is the app running? it declares the tap queue at startup"
    fi

    if [[ "$(jq 'length' <<<"$RESPONSE")" -gt 0 ]]; then
        if [[ "$(jq -r '.[0].payload_encoding' <<<"$RESPONSE")" == "base64" ]]; then
            RAW_BODY="$(jq -r '.[0].payload' <<<"$RESPONSE" | "${B64_DECODE[@]}")"
        else
            RAW_BODY="$(jq -r '.[0].payload' <<<"$RESPONSE")"
        fi
        break
    fi
    sleep 0.5
done

[[ -n "$RAW_BODY" ]] || fail "no message on '$TAP_QUEUE' after $TAP_ATTEMPTS attempts — check the app logs for a publish error"

echo "AMQP envelope the broker holds (routing metadata stays readable):"
jq '.[0] | {exchange, routing_key, redelivered, properties, payload_bytes}' <<<"$RESPONSE"

echo
echo "delivery.Body — ${#RAW_BODY} bytes, verbatim:"
echo
echo "$RAW_BODY"

# --- 3. anatomy -----------------------------------------------------------

section "3/4  Anatomy: one compact JWS, three segments"

IFS='.' read -r -a SEGMENTS <<<"$RAW_BODY"
[[ "${#SEGMENTS[@]}" -eq 3 ]] \
    || fail "expected 3 dot-separated JWS segments, got ${#SEGMENTS[@]} — this is not a compact JWS"
for i in 0 1 2; do
    [[ -n "${SEGMENTS[$i]}" ]] || fail "JWS segment $((i + 1)) is empty"
done

printf '  segment 1 (protected header) : %5d chars\n' "${#SEGMENTS[0]}"
printf '  segment 2 (payload)          : %5d chars\n' "${#SEGMENTS[1]}"
printf '  segment 3 (signature)        : %5d chars\n' "${#SEGMENTS[2]}"

HEADER_JSON="$(b64url_decode "${SEGMENTS[0]}")" || fail "protected header is not base64url"
echo
echo "Protected header (base64url-decoded):"
jq . <<<"$HEADER_JSON"

TYP="$(jq -r '.typ // empty' <<<"$HEADER_JSON")"
ALG="$(jq -r '.alg // empty' <<<"$HEADER_JSON")"
KID="$(jq -r '.kid // empty' <<<"$HEADER_JSON")"
ETYP="$(jq -r '.etyp // empty' <<<"$HEADER_JSON")"

[[ "$TYP" == "$EXPECTED_TYP" ]] || fail "typ is '$TYP', expected '$EXPECTED_TYP'"
[[ "$ALG" == "PS256" ]] \
    || fail "alg is '$ALG', expected PS256 — this demo's producer signs with PS256 only, so anything else on this wire is a foreign producer"
[[ "$KID" =~ ^${SIGN_FAMILY}-v[0-9]+$ ]] \
    || fail "kid '$KID' is not a generation of the '$SIGN_FAMILY' family"

echo
echo "  typ  = $TYP   ← the ONLY sealed-message marker (no x-sealed AMQP header)"
echo "  alg  = $ALG                          ← signature over the SEALED document"
echo "  kid  = $KID                ← concrete generation of '$SIGN_FAMILY'"
echo "  etyp = $ETYP           ← signed event type; a cross-type reroute is poison"
echo "  jti  = $(jq -r '.jti // "<absent>"' <<<"$HEADER_JSON")   ← dedup identity: '$SIGN_FAMILY:<jti>'"
echo "  sp   = $(jq -c '.sp // "<absent>"' <<<"$HEADER_JSON")                    ← signed sealed-paths manifest"

PAYLOAD_JSON="$(b64url_decode "${SEGMENTS[1]}")" || fail "JWS payload is not base64url"
echo
echo "Signed payload (base64url-decoded) — clear fields readable, Subject is not:"
jq . <<<"$PAYLOAD_JSON"

# Correlation: the drained queue plus this equality make it impossible to mistake
# a leftover message for the one this run published.
if [[ -n "$ORDER_ID" ]]; then
    WIRE_ORDER_ID="$(jq -r '.orderId // empty' <<<"$PAYLOAD_JSON")"
    [[ "$WIRE_ORDER_ID" == "$ORDER_ID" ]] \
        || fail "wire orderId '$WIRE_ORDER_ID' != the API's '$ORDER_ID' — this is not the message this run published"
    echo
    echo "  orderId matches the API response ($ORDER_ID) — same event, both sides."
fi

CARD_JWE="$(jq -r '.card // empty' <<<"$PAYLOAD_JSON")"
[[ -n "$CARD_JWE" ]] || fail "payload has no 'card' member — the Subject must always be present on the wire"

IFS='.' read -r -a JWE_SEGMENTS <<<"$CARD_JWE"
[[ "${#JWE_SEGMENTS[@]}" -eq 5 ]] \
    || fail "'card' has ${#JWE_SEGMENTS[@]} segments, expected a 5-segment compact JWE"

JWE_HEADER_JSON="$(b64url_decode "${JWE_SEGMENTS[0]}")" || fail "inner JWE header is not base64url"
echo
echo "Inner JWE header for the 'card' Subject:"
jq . <<<"$JWE_HEADER_JSON"

JWE_KID="$(jq -r '.kid // empty' <<<"$JWE_HEADER_JSON")"
JWE_ISS="$(jq -r '.iss // empty' <<<"$JWE_HEADER_JSON")"
JWE_ALG="$(jq -r '.alg // empty' <<<"$JWE_HEADER_JSON")"
JWE_ENC="$(jq -r '.enc // empty' <<<"$JWE_HEADER_JSON")"
[[ "$JWE_KID" =~ ^${ENCRYPT_FAMILY}-v[0-9]+$ ]] \
    || fail "inner kid '$JWE_KID' is not a generation of the '$ENCRYPT_FAMILY' family"
[[ "$JWE_ISS" == "$KID" ]] \
    || fail "inner iss '$JWE_ISS' != outer kid '$KID' — the authorship binding is broken"
[[ "$JWE_ALG" == "RSA-OAEP-256" ]] \
    || fail "inner alg is '$JWE_ALG', expected RSA-OAEP-256 — the content key was wrapped by something other than this demo's sealer"
[[ "$JWE_ENC" == "A256GCM" ]] \
    || fail "inner enc is '$JWE_ENC', expected A256GCM — the Subject was encrypted with an unexpected content cipher"

echo
echo "  kid = $JWE_KID   ← audience encrypt generation"
echo "  iss = $JWE_ISS      ← equals the outer kid: the binding that kills strip-and-re-sign"
echo "  alg = $JWE_ALG           ← how the content key is wrapped to that audience"
echo "  enc = $JWE_ENC                ← AEAD over the card itself"

# --- 4. the assertion that matters ---------------------------------------

section "4/4  The PAN is nowhere on the wire"

if grep -qF -- "$PAN" <<<"$RAW_BODY"; then
    fail "PAN FOUND IN THE RAW BODY — the Subject was not sealed"
fi
if grep -qF -- "$PAN" <<<"$PAYLOAD_JSON"; then
    fail "PAN FOUND IN THE SIGNED PAYLOAD — the Subject was not encrypted"
fi
if grep -qF -- "$PAN" <<<"$(jq -c '.[0].properties' <<<"$RESPONSE")"; then
    fail "PAN FOUND IN THE AMQP PROPERTIES/HEADERS"
fi

echo "✅ grep for the PAN fails against the raw body, the signed payload and the AMQP headers."
echo "✅ Only the consumer holding the '$ENCRYPT_FAMILY' PRIVATE key can read the card."
echo
echo "What an operator with full broker access still learns: order id, amount,"
echo "currency, event type, the producing key family — and the Subject's size class."
echo "That is the ADR-097 trade: routable + triageable, never readable."
echo
echo "Next:"
echo "  * consumer side: after the open the handler holds the card in memory only —"
echo "    the app log line for the 'payments.authorized' delivery carries cardLast4,"
echo "    never the plaintext card, and the delivery is deduped through the inbox."
echo "  * rotation: provision payments-sign-v2, then pin it with the commented-out"
echo "    'messaging.seal.active' selector in config.development.yaml, and re-run"
echo "    this script — the kid above moves, the seal tag never changes."
