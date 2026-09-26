#!/usr/bin/env bash
# scripts/seal-event-demo.sh
#
# Externally-minted sealed events for the payments demo (go-bricks ADR-097),
# using the framework's `seal-event` CLI (go-bricks v0.63.0, #1417).
#
# `make show-sealed-message` proves the PRODUCER half: the app publishes and we
# read the sealed bytes off the broker. This script proves the CONSUMER half by
# taking the app out of the producing role entirely — the event body is minted
# in the shell from the demo's own DER keys and published straight to the
# exchange. Three things only an out-of-band producer can demonstrate:
#
#   1. OPENS  — a body this app never produced is accepted, because acceptance
#               is key material + declaration agreement, not process identity.
#               The seal-event CLI runs the production sealed.SealDocument path,
#               so a body it emits is one the consume door opens by construction.
#   2. DEDUPS — publishing the SAME bytes twice trips the inbox ledger. The `jti`
#               is minted once, per seal, so the two deliveries share a dedup key
#               ("<sign family>:<jti>") and the second is skipped. The in-app flow
#               CANNOT show this: every POST /payments/authorize is a fresh seal
#               with a fresh jti, so re-running it never collides. Re-running the
#               CLI is likewise a new seal — only republishing the same body is
#               the dedup test.
#   3. REJECTS — a body whose signed `etyp` disagrees with the consumer's declared
#               EventType is refused at open-rule 7 (SEAL_EVENT_TYPE_MISMATCH) and
#               parks on the DLQ. Signature, kids and manifest are all valid; only
#               the event type is wrong, which is the cross-type reroute class the
#               inbox ledger cannot close (a captured event of another type has a
#               jti the ledger has never seen). The broker records only that the
#               message was rejected, so step 5 reads the verdict back off the
#               PARKED bytes with the framework's `open-event` CLI (go-bricks
#               v0.65.0, #1640): same open rules, same SEAL_* code, no app log.
#
# The keys: this demo is producer AND consumer in one process, so its keystore
# holds both halves of both families. seal-event takes the PRODUCER half — the
# sign PRIVATE key and the encrypt PUBLIC key — exactly as a separate producing
# service would, and never sees the consumer's half. open-event takes the
# CONSUMER half — the sign PUBLIC key and the encrypt PRIVATE key — exactly as
# the payments consumer resolves them from its keystore.
#
# DEMO DATA ONLY. 4111111111111111 is the universally published Visa test PAN.
# Never put a real cardholder number through this script.
#
# Prerequisites:
#   make docker-up      # RabbitMQ (management plugin on :15672) + postgres
#   make migrate        # inbox ledger table (gobricks_inbox), V4
#   make generate-keys  # certs/payments_{sign,encrypt}_v1_*.der
#   make run            # app must be running — it declares the exchange, the
#                       # queue, its DLQ and the tap queue at boot, and it is the
#                       # consumer whose behavior this script observes
#
# Overrides (env): RABBIT_MGMT, RABBIT_USER, RABBIT_PASS, RABBIT_VHOST,
#                  SEAL_EVENT_VERSION, PG_HOST, PG_PORT, PG_USER, PG_DB,
#                  PGPASSWORD, SETTLE_SECONDS, DLQ_PEEK_MAX

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR/.."

# Endpoint guard + credential-file writer, shared with show-sealed-message.sh.
# Resolved from SCRIPT_DIR, so the cd above cannot move it.
# shellcheck source-path=SCRIPTDIR source=lib/rabbitmq-mgmt.sh
source "$SCRIPT_DIR/lib/rabbitmq-mgmt.sh"

# open-event installer + runner (refuses -print-subject), shared the same way.
# shellcheck source-path=SCRIPTDIR source=lib/open-event.sh
source "$SCRIPT_DIR/lib/open-event.sh"

RABBIT_MGMT="${RABBIT_MGMT:-http://127.0.0.1:15672}"
RABBIT_USER="${RABBIT_USER:-guest}"
RABBIT_PASS="${RABBIT_PASS:-guest}"
RABBIT_VHOST="${RABBIT_VHOST:-%2F}"   # default vhost "/" percent-encoded

# Pin the CLIs to the tag this demo pins the framework to, so the sealer that
# mints these bodies and the opener that judges them are the same code the app
# links. One variable for both: they ship in the same module at the same tag.
# Empty means "the version in go.mod", resolved once go is known to exist.
SEAL_EVENT_VERSION="${SEAL_EVENT_VERSION:-}"

# Topology — must match internal/modules/payments/module.go.
EXCHANGE="payment-events"
ROUTING_KEY="payment.authorized"
EVENT_TYPE="payment.authorized"
QUEUE="payments.authorized"
DLQ="payments.authorized.dlq"

# Keystore generations. The seal tag names only the LOGICAL kids
# (payments-sign / payments-encrypt); the wire carries these generations.
SIGN_KID="payments-sign-v1"
ENCRYPT_KID="payments-encrypt-v1"
SIGN_FAMILY="payments-sign"
SIGN_KEY_FILE="certs/payments_sign_v1_private.der"    # producer half: PRIVATE
ENCRYPT_KEY_FILE="certs/payments_encrypt_v1_public.der" # producer half: PUBLIC
OPEN_SIGN_KEY_FILE="certs/payments_sign_v1_public.der"       # consumer half: PUBLIC
OPEN_ENCRYPT_KEY_FILE="certs/payments_encrypt_v1_private.der" # consumer half: PRIVATE

# The one member carrying seal:"subject" in domain.PaymentAuthorized.
SUBJECT="card"

# A valid event type the payments consumer does NOT declare — the rejection case.
WRONG_EVENT_TYPE="payment.captured"

# Postgres — where the inbox ledger (gobricks_inbox, V4) lives.
PG_HOST="${PG_HOST:-127.0.0.1}"
PG_PORT="${PG_PORT:-5432}"
PG_USER="${PG_USER:-postgres}"
PG_DB="${PG_DB:-postgres}"
export PGPASSWORD="${PGPASSWORD:-postgres}"

SETTLE_SECONDS="${SETTLE_SECONDS:-2}"

# How many DLQ messages step 4 peeks while looking for the body it just parked.
DLQ_PEEK_MAX="${DLQ_PEEK_MAX:-100}"

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

for tool in curl jq go; do
    command -v "$tool" >/dev/null 2>&1 || fail "$tool is required but not installed"
done

# SEAL_EVENT_VERSION defaults to the go-bricks version go.mod pins, read the way
# scripts/seal-payload.sh reads it, so no second pin can drift from go.mod. The
# workspace is off for the lookup: an untracked go.work would make go list report
# the workspace module, which has no version.
if [[ -z "$SEAL_EVENT_VERSION" ]]; then
    SEAL_EVENT_VERSION="$(GOWORK=off go list -m -f '{{.Version}}' github.com/gaborage/go-bricks)" \
        || fail "could not read the github.com/gaborage/go-bricks version from go.mod"
fi
[[ -n "$SEAL_EVENT_VERSION" ]] || fail "go.mod reports no version for github.com/gaborage/go-bricks"
SEAL_EVENT_PKG="github.com/gaborage/go-bricks/cmd/seal-event@${SEAL_EVENT_VERSION}"
OPEN_EVENT_PKG="github.com/gaborage/go-bricks/cmd/open-event@${SEAL_EVENT_VERSION}"

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

# assert_pan_absent FILE LABEL — the assertion that matters, run against every
# body this script mints BEFORE it is published.
#
# Two greps, and the second is the load-bearing one. A compact JWS is base64url
# all the way down, so a PAN sitting in the CLEAR inside the signed payload does
# not appear in the raw body: grepping only the raw bytes passes even when the
# Subject was never sealed. Decode segment 2 and grep that as well — the same
# pair show-sealed-message.sh asserts against the message it reads off the broker.
assert_pan_absent() {
    local file="$1" label="$2" body payload
    body="$(cat "$file")"
    grep -qF -- "$PAN" <<<"$body" \
        && fail "PAN FOUND IN THE RAW $label — the Subject was not sealed"
    payload="$(b64url_decode "$(cut -d. -f2 <<<"$body")")" \
        || fail "$label: JWS payload is not base64url"
    grep -qF -- "$PAN" <<<"$payload" \
        && fail "PAN FOUND IN THE SIGNED PAYLOAD of the $label — the Subject was not encrypted"
    return 0
}

# --- endpoint guard -------------------------------------------------------

guard_mgmt_endpoint "$RABBIT_MGMT"

# --- credentials ----------------------------------------------------------

# All scratch files (and the directory open-event is installed into) are
# created here so one trap owns the cleanup.
CURL_CFG="$(mktemp)"
BODY_VALID="$(mktemp)"
BODY_WRONG_TYPE="$(mktemp)"
OPEN_OUT="$(mktemp)"
OPEN_ERR="$(mktemp)"
TOOL_DIR="$(mktemp -d)"
trap 'rm -f "$CURL_CFG" "$BODY_VALID" "$BODY_WRONG_TYPE" "$OPEN_OUT" "$OPEN_ERR"; rm -rf "$TOOL_DIR"' EXIT INT TERM HUP

write_curl_cfg "$CURL_CFG" "$RABBIT_MGMT" "$RABBIT_USER" "$RABBIT_PASS"

# --- broker + ledger helpers ---------------------------------------------

# queue_depth NAME — messages currently sitting in the queue, or "" if absent.
queue_depth() {
    curl -sS -K "$CURL_CFG" "$RABBIT_MGMT/api/queues/$RABBIT_VHOST/$1" \
        | jq -r 'if type == "object" and has("messages") then .messages else empty end'
}

# wait_queue_depth_above NAME BASELINE — poll until the queue's depth exceeds
# BASELINE, up to ~20s, echoing the depth that satisfied it (or the last read).
# The DLQ is a quorum queue as of go-bricks v0.64.0 (ADR-106), and the
# management API surfaces a quorum queue's `messages` on the stats emission
# tick (~5s) — a single read right after a publish reliably under-reports.
wait_queue_depth_above() {
    local depth="" deadline=$((SECONDS + 20))
    while ((SECONDS < deadline)); do
        depth="$(queue_depth "$1")"
        if [[ -n "$depth" && "$depth" -gt "$2" ]] 2>/dev/null; then
            echo "$depth"
            return 0
        fi
        sleep 1
    done
    echo "$depth"
    return 1
}

# publish_body FILE — publish the file's contents to the exchange with the demo's
# routing key. This is the management API's publish endpoint, which is exactly
# what `rabbitmqadmin publish` drives; curl is used here so the broker password
# stays out of argv (rabbitmqadmin takes -u/-p on the command line). The
# equivalent one-liner for anyone who has rabbitmqadmin installed:
#
#   rabbitmqadmin publish exchange=payment-events routing_key=payment.authorized \
#     payload="$(cat body.txt)" \
#     properties='{"content_type":"application/octet-stream"}'
#
# No x-tenant-id header: multitenant.enabled is false in this demo, so the signed
# `tid` carries no rule (it is surfaced on the envelope and never compared).
publish_body() {
    local payload response routed
    payload="$(cat "$1")"

    # jq --arg builds the JSON so a payload containing quotes or backslashes
    # cannot break out of the string.
    response="$(jq -n --arg p "$payload" --arg rk "$ROUTING_KEY" \
        '{properties: {content_type: "application/octet-stream"},
          routing_key: $rk, payload: $p, payload_encoding: "string"}' \
        | curl -sS -K "$CURL_CFG" -H 'content-type: application/json' \
            -X POST "$RABBIT_MGMT/api/exchanges/$RABBIT_VHOST/$EXCHANGE/publish" \
            --data-binary @- || true)"

    routed="$(jq -r 'if type == "object" then (.routed // empty) else empty end' <<<"$response" 2>/dev/null || true)"
    [[ "$routed" == "true" ]] \
        || fail "publish to '$EXCHANGE' was not routed — broker said: ${response:-<no response>}"
}

# ledger_count KEY — rows in the inbox ledger for one dedup key. Best-effort: it
# prints a count on success and returns non-zero on any failure (no psql, no
# table, query error), so a caller writing `count="$(ledger_count …)" || count="?"`
# downgrades the assertion to advisory instead of aborting the demo.
#
# The docker probe passes PGPASSWORD as a bare `-e NAME`, which copies the value
# from this shell's environment. `-e NAME=VALUE` would put the database password
# in docker's own argv, readable through ps(1) by every user on the host.
PSQL_MODE=""
if command -v psql >/dev/null 2>&1 \
    && psql -h "$PG_HOST" -p "$PG_PORT" -U "$PG_USER" -d "$PG_DB" -tAc 'SELECT 1' >/dev/null 2>&1; then
    PSQL_MODE="host"
elif command -v docker >/dev/null 2>&1 \
    && docker exec -e PGPASSWORD go-bricks-postgres \
        psql -U "$PG_USER" -d "$PG_DB" -tAc 'SELECT 1' >/dev/null 2>&1; then
    PSQL_MODE="docker"
fi

ledger_count() {
    local sql out
    # `:'key'` is a psql CLIENT-side substitution — it expands to a properly
    # quoted SQL literal, so the key cannot break out of the string, but it is
    # not a server-side bind parameter. Crucially, psql expands -v variables only
    # in a script it READS (stdin or -f); inside a -c string the `:'key'` text is
    # sent to the server verbatim and comes back a syntax error. Hence -f -.
    sql="SELECT count(*) FROM gobricks_inbox WHERE event_id = :'key';"
    case "$PSQL_MODE" in
        host)
            out="$(printf '%s\n' "$sql" \
                | psql -h "$PG_HOST" -p "$PG_PORT" -U "$PG_USER" -d "$PG_DB" \
                    -v key="$1" -tA -f - 2>/dev/null)" || return 1
            ;;
        docker)
            out="$(printf '%s\n' "$sql" \
                | docker exec -i -e PGPASSWORD go-bricks-postgres \
                    psql -U "$PG_USER" -d "$PG_DB" -v key="$1" -tA -f - 2>/dev/null)" || return 1
            ;;
        *) return 1 ;;
    esac
    printf '%s' "${out//[[:space:]]/}"
}

# --- preflight ------------------------------------------------------------

section "0/5  Preflight"

for f in "$SIGN_KEY_FILE" "$ENCRYPT_KEY_FILE" "$OPEN_SIGN_KEY_FILE" "$OPEN_ENCRYPT_KEY_FILE"; do
    [[ -r "$f" ]] || fail "missing key '$f' — run 'make generate-keys'"
done
[[ "$DLQ_PEEK_MAX" =~ ^[1-9][0-9]*$ ]] || fail "DLQ_PEEK_MAX='$DLQ_PEEK_MAX' must be a positive integer"

curl -fsS -o /dev/null -K "$CURL_CFG" "$RABBIT_MGMT/api/overview" 2>/dev/null \
    || fail "RabbitMQ management API not reachable at $RABBIT_MGMT — run 'make docker-up'"

for q in "$QUEUE" "$DLQ"; do
    [[ -n "$(queue_depth "$q")" ]] \
        || fail "queue '$q' does not exist — start the app with 'make run'; it declares the exchange, the queue and its DLQ at boot"
done

# Installed up front so a missing toolchain or proxy fails before anything is
# published, not halfway through step 5.
install_open_event "$SEAL_EVENT_VERSION" "$TOOL_DIR"

echo "keys      : $SIGN_KEY_FILE (sign PRIVATE) + $ENCRYPT_KEY_FILE (encrypt PUBLIC)"
echo "            for seal-event, the producer half"
echo "            $OPEN_SIGN_KEY_FILE (sign PUBLIC) + $OPEN_ENCRYPT_KEY_FILE (encrypt PRIVATE)"
echo "            for open-event, the consumer half"
echo "broker    : $RABBIT_MGMT — exchange '$EXCHANGE', queue '$QUEUE', DLQ '$DLQ'"
if [[ -n "$PSQL_MODE" ]]; then
    echo "ledger    : gobricks_inbox via psql ($PSQL_MODE)"
else
    echo "ledger    : psql not reachable — dedup will be reported, not asserted"
fi
echo "seal CLI  : go run $SEAL_EVENT_PKG"
echo "open CLI  : $OPEN_EVENT_PKG, installed to a scratch GOBIN"
echo "            (go run would turn its refusal exit 3 into a generic 1)"

# --- 1. mint outside the app ----------------------------------------------

section "1/5  Mint a sealed body with the seal-event CLI"

# The document is plain business JSON. The CLI replaces the -subject member in
# place with a compact JWE and signs the whole result — the same
# sealed.SealDocument path the app's typed publisher runs.
#
# DEMO DATA ONLY — 4111111111111111 is the published Visa test PAN.
ORDER_ID="ext-$(date +%s)"
DOCUMENT="$(jq -n --arg id "$ORDER_ID" \
    '{orderId: $id, amount: 4599, currency: "USD",
      card: {pan: "4111111111111111", expMonth: 12, expYear: 2030, holder: "ADA LOVELACE"}}')"

# Read the PAN back out of the document rather than repeating the literal, so the
# assertions below can only ever test the value actually sent to the sealer.
PAN="$(jq -r '.card.pan' <<<"$DOCUMENT")"
[[ -n "$PAN" && "$PAN" != "null" ]] || fail "document has no .card.pan to assert against"

echo "document (plaintext, this side of the seal):"
jq '.card.pan = "<sent verbatim; masked in this echo only>"' <<<"$DOCUMENT"
echo
echo "go run $SEAL_EVENT_PKG \\"
echo "  -sign-key-file $SIGN_KEY_FILE \\"
echo "  -encrypt-key-file $ENCRYPT_KEY_FILE \\"
echo "  -sign-kid $SIGN_KID -encrypt-kid $ENCRYPT_KID \\"
echo "  -subject $SUBJECT -event-type $EVENT_TYPE"

printf '%s' "$DOCUMENT" | go run "$SEAL_EVENT_PKG" \
    -sign-key-file "$SIGN_KEY_FILE" \
    -encrypt-key-file "$ENCRYPT_KEY_FILE" \
    -sign-kid "$SIGN_KID" \
    -encrypt-kid "$ENCRYPT_KID" \
    -subject "$SUBJECT" \
    -event-type "$EVENT_TYPE" >"$BODY_VALID" \
    || fail "seal-event failed to mint the valid body"

[[ -s "$BODY_VALID" ]] || fail "seal-event produced an empty body"
SEALED="$(cat "$BODY_VALID")"

IFS='.' read -r -a SEGMENTS <<<"$SEALED"
[[ "${#SEGMENTS[@]}" -eq 3 ]] \
    || fail "expected 3 dot-separated JWS segments, got ${#SEGMENTS[@]} — this is not a compact JWS"

HEADER_JSON="$(b64url_decode "${SEGMENTS[0]}")" || fail "protected header is not base64url"
JTI="$(jq -r '.jti // empty' <<<"$HEADER_JSON")"
[[ -n "$JTI" ]] || fail "minted header carries no jti"
DEDUP_KEY="$SIGN_FAMILY:$JTI"

echo
echo "minted ${#SEALED} bytes. Protected header:"
jq . <<<"$HEADER_JSON"
echo
echo "  dedup key the consumer will derive : $DEDUP_KEY"
echo "  (the Logical family, not the generation — a rotation must not re-open the window)"

assert_pan_absent "$BODY_VALID" "minted body"
echo "  PAN is absent from the minted body — from the raw bytes AND from the"
echo "  base64url-decoded signed payload, which is where an unsealed Subject would"
echo "  actually show up ✅"

# --- 2. the consumer opens a body this app never produced -----------------

section "2/5  Publish it — the consumer opens an externally-minted event"

DLQ_BEFORE="$(queue_depth "$DLQ")"
echo "DLQ depth before: $DLQ_BEFORE"

publish_body "$BODY_VALID"
echo "published to '$EXCHANGE' with routing key '$ROUTING_KEY' (routed=true)"

sleep "$SETTLE_SECONDS"

LEDGER_AFTER_FIRST="$(ledger_count "$DEDUP_KEY")" || LEDGER_AFTER_FIRST="?"
DLQ_AFTER_FIRST="$(queue_depth "$DLQ")"

echo "inbox ledger rows for '$DEDUP_KEY': $LEDGER_AFTER_FIRST"
echo "DLQ depth after: $DLQ_AFTER_FIRST"

if [[ "$LEDGER_AFTER_FIRST" == "1" ]]; then
    echo "✅ opened and processed: one ledger row, nothing on the DLQ."
elif [[ "$LEDGER_AFTER_FIRST" == "?" ]]; then
    echo "ℹ️  ledger not queryable here — check the app log for the 'payments.authorized'"
    echo "    delivery carrying cardLast4=1111 and orderId=$ORDER_ID."
else
    fail "expected exactly 1 ledger row after the first publish, got '$LEDGER_AFTER_FIRST' — check the app log for an open failure"
fi
[[ "$DLQ_AFTER_FIRST" == "$DLQ_BEFORE" ]] \
    || fail "DLQ grew ($DLQ_BEFORE -> $DLQ_AFTER_FIRST) — the valid body was refused; check the app log for the SEAL_* code"

echo
echo "The app did not produce this event. It accepted it because the wire kid is a"
echo "provisioned generation of the family its seal tag names and the signature"
echo "verifies — acceptance is key material plus declaration agreement, never"
echo "process identity."

# --- 3. the same bytes twice: inbox dedup ---------------------------------

section "3/5  Publish the SAME bytes again — inbox dedup"

echo "Re-running the CLI would mint a fresh jti and prove nothing; the dedup test"
echo "is republishing the identical body, which is exactly what a redelivery, a DLQ"
echo "drain or an outbox re-drive does."
echo

publish_body "$BODY_VALID"
echo "published the identical ${#SEALED}-byte body a second time (routed=true)"

sleep "$SETTLE_SECONDS"

LEDGER_AFTER_SECOND="$(ledger_count "$DEDUP_KEY")" || LEDGER_AFTER_SECOND="?"
DLQ_AFTER_SECOND="$(queue_depth "$DLQ")"

echo "inbox ledger rows for '$DEDUP_KEY': $LEDGER_AFTER_SECOND (was $LEDGER_AFTER_FIRST)"
echo "DLQ depth: $DLQ_AFTER_SECOND"

if [[ "$LEDGER_AFTER_SECOND" == "1" ]]; then
    echo "✅ still exactly one row: the second delivery hit the ledger's duplicate"
    echo "   short-circuit, was skipped and ACKed. The handler body never re-ran."
elif [[ "$LEDGER_AFTER_SECOND" == "?" ]]; then
    echo "ℹ️  ledger not queryable here — the app log records the duplicate"
    echo "    short-circuit for key '$DEDUP_KEY'."
else
    fail "expected the ledger to stay at 1 row, got '$LEDGER_AFTER_SECOND' — dedup did not engage"
fi
[[ "$DLQ_AFTER_SECOND" == "$DLQ_BEFORE" ]] \
    || fail "DLQ grew on the duplicate — a duplicate must be skipped, not parked"

echo
echo "Why the HTTP flow cannot show this: POST /payments/authorize seals on every"
echo "call, and each seal mints a fresh jti, so two calls are two distinct events."
echo "Only a body captured once and replayed collides — which is precisely the"
echo "replay the ledger exists to absorb. inbox.retentionperiod IS that window."

# --- 4. wrong event type: SEAL_EVENT_TYPE_MISMATCH -> DLQ -----------------

section "4/5  Wrong -event-type — open-rule 7 refuses, message parks on the DLQ"

echo "Same keys, same subject, same document: only the signed etyp changes to"
echo "'$WRONG_EVENT_TYPE'. Signature, kids and manifest are all valid — this is the"
echo "cross-type reroute class, the one the ledger cannot close (a captured event"
echo "of another type carries a jti the ledger has never seen)."
echo

printf '%s' "$DOCUMENT" | go run "$SEAL_EVENT_PKG" \
    -sign-key-file "$SIGN_KEY_FILE" \
    -encrypt-key-file "$ENCRYPT_KEY_FILE" \
    -sign-kid "$SIGN_KID" \
    -encrypt-kid "$ENCRYPT_KID" \
    -subject "$SUBJECT" \
    -event-type "$WRONG_EVENT_TYPE" >"$BODY_WRONG_TYPE" \
    || fail "seal-event failed to mint the wrong-event-type body"

WRONG_HEADER="$(b64url_decode "$(cut -d. -f1 <"$BODY_WRONG_TYPE")")" \
    || fail "wrong-type protected header is not base64url"
echo "minted with etyp = $(jq -r '.etyp' <<<"$WRONG_HEADER")   (consumer declares '$EVENT_TYPE')"

# This body is about to park on a DURABLE DLQ and sit there, so it gets the same
# assertion the accepted body got. A refused event is still a sealed event.
assert_pan_absent "$BODY_WRONG_TYPE" "wrong-event-type body"
echo "PAN absent from it too, raw and decoded — the etyp is wrong, the seal is not."
echo

publish_body "$BODY_WRONG_TYPE"
echo "published to '$EXCHANGE' with routing key '$ROUTING_KEY' (routed=true)"
echo "— the PUBLISH succeeds: the broker routes on the routing key and never opens"
echo "  the envelope. The refusal happens in the consumer."

DLQ_AFTER_WRONG="$(wait_queue_depth_above "$DLQ" "$DLQ_BEFORE")" || true
echo
echo "DLQ depth: $DLQ_BEFORE -> $DLQ_AFTER_WRONG"

if [[ "$DLQ_AFTER_WRONG" -gt "$DLQ_BEFORE" ]] 2>/dev/null; then
    echo "✅ the message parked on '$DLQ': the open failed at rule 7 and the delivery"
    echo "   was nacked WITHOUT requeue, so it dead-lettered instead of hot-looping."
else
    fail "DLQ did not grow ($DLQ_BEFORE -> $DLQ_AFTER_WRONG) — expected the wrong etyp to be refused; is the app running?"
fi

# Find THIS run's parked message. The DLQ is durable and keeps whatever earlier
# runs parked, so its head is not necessarily ours: peek up to DLQ_PEEK_MAX
# messages and keep the one whose payload is byte-identical to the body minted
# above (each seal mints a fresh jti, so no other body can match). ackmode
# ack_requeue_true puts every peeked message back — marked redelivered — so a
# second run of this script still sees a populated DLQ. Retried briefly in case
# the growth counted above was some other message and ours is still in flight.
MINTED_WRONG="$(cat "$BODY_WRONG_TYPE")"
PEEK_REQUEST="$(jq -nc --argjson n "$DLQ_PEEK_MAX" \
    '{count: $n, ackmode: "ack_requeue_true", encoding: "auto"}')"
PARKED_ENTRY=""
for ((attempt = 1; attempt <= 5; attempt++)); do
    PARKED="$(curl -sS -K "$CURL_CFG" -H 'content-type: application/json' \
        -X POST "$RABBIT_MGMT/api/queues/$RABBIT_VHOST/$DLQ/get" \
        --data-binary "$PEEK_REQUEST" || true)"
    PARKED_ENTRY="$(jq -c --arg body "$MINTED_WRONG" \
        'if type == "array"
         then (map(select(.payload_encoding == "string" and .payload == $body)) | first // empty)
         else empty end' <<<"$PARKED" 2>/dev/null || true)"
    [[ -n "$PARKED_ENTRY" ]] && break
    sleep 1
done
[[ -n "$PARKED_ENTRY" ]] \
    || fail "this run's body is not among the first $DLQ_PEEK_MAX messages on '$DLQ' — purge it (command at the end of this script) or raise DLQ_PEEK_MAX"

# The AMQP properties and headers are the third surface an operator reads, and
# the broker echoes back whatever the publisher set. Assert the PAN is not
# hiding there either, as show-sealed-message.sh does for its tap message.
grep -qF -- "$PAN" <<<"$(jq -c '.properties' <<<"$PARKED_ENTRY")" \
    && fail "PAN FOUND IN THE AMQP PROPERTIES/HEADERS of the parked message"
PARKED_BODY="$(jq -r '.payload' <<<"$PARKED_ENTRY")"

echo
echo "found this run's body on '$DLQ' (byte-identical to the one minted above)."
echo "x-death on it (broker-written on nack-without-requeue):"
jq '.properties.headers["x-death"] // "<absent>"' <<<"$PARKED_ENTRY"

# --- 5. the consumer's verdict, read back off the parked bytes -------------

section "5/5  open-event on the parked body — the consumer's verdict, no app log"

echo "The broker records only THAT the message was rejected (x-death above), never"
echo "WHY. open-event runs the consume door's open rules — same order, same SEAL_*"
echo "codes — over the parked bytes, holding the consumer half of the keys and"
echo "declaring exactly what the payments consumer declares:"
echo
echo "open-event \\"
echo "  -sign-key-file $OPEN_SIGN_KEY_FILE \\"
echo "  -encrypt-key-file $OPEN_ENCRYPT_KEY_FILE \\"
echo "  -sign-kid $SIGN_KID -encrypt-kid $ENCRYPT_KID \\"
echo "  -subject $SUBJECT -event-type $EVENT_TYPE -tenancy disabled -json"
echo

# -tenancy disabled: multitenant.enabled is false here, so the signed tid carries
# no rule — the same reason publish_body sends no x-tenant-id header. -json puts
# a refusal on stdout as {code, details}; details are presence/length facts only.
# NEVER add -print-subject: it prints the decrypted card, PAN included, and
# open_event refuses it. Both outputs are checked for the PAN before either is
# printed.
OPEN_RC=0
open_event \
    -sign-key-file "$OPEN_SIGN_KEY_FILE" \
    -encrypt-key-file "$OPEN_ENCRYPT_KEY_FILE" \
    -sign-kid "$SIGN_KID" \
    -encrypt-kid "$ENCRYPT_KID" \
    -subject "$SUBJECT" \
    -event-type "$EVENT_TYPE" \
    -tenancy disabled \
    -json <<<"$PARKED_BODY" >"$OPEN_OUT" 2>"$OPEN_ERR" || OPEN_RC=$?
assert_redacted "$PAN" "open-event output" "$OPEN_OUT" "$OPEN_ERR"

OPEN_CODE="$(jq -r '.code // empty' "$OPEN_OUT" 2>/dev/null || true)"
if [[ "$OPEN_RC" -ne 3 || "$OPEN_CODE" != "SEAL_EVENT_TYPE_MISMATCH" ]]; then
    cat "$OPEN_ERR" >&2
    fail "expected open-event to refuse with exit 3 and SEAL_EVENT_TYPE_MISMATCH, got exit $OPEN_RC and '${OPEN_CODE:-<no code>}'"
fi
echo "exit $OPEN_RC (0 opened, 1 tool error, 2 usage, 3 refused), stdout:"
jq . "$OPEN_OUT"
echo
echo "✅ refused with SEAL_EVENT_TYPE_MISMATCH — the code the consumer logged for"
echo "   this delivery (a *messaging.PayloadError at stage 'open' wrapping"
echo "   *sealed.OpenError), reproduced from the parked bytes alone. 'len' is the"
echo "   signed etyp's length: a refusal carries presence/length facts, never a"
echo "   subject byte."

echo
echo "Control: the SAME parked bytes, declaring the type they were sealed with"
echo "('$WRONG_EVENT_TYPE'), open cleanly — so signature, kids and manifest all"
echo "verify, and rule 7 alone refused the delivery:"
echo

OPEN_RC=0
open_event \
    -sign-key-file "$OPEN_SIGN_KEY_FILE" \
    -encrypt-key-file "$OPEN_ENCRYPT_KEY_FILE" \
    -sign-kid "$SIGN_KID" \
    -encrypt-kid "$ENCRYPT_KID" \
    -subject "$SUBJECT" \
    -event-type "$WRONG_EVENT_TYPE" \
    -tenancy disabled \
    -json <<<"$PARKED_BODY" >"$OPEN_OUT" 2>"$OPEN_ERR" || OPEN_RC=$?
assert_redacted "$PAN" "open-event output" "$OPEN_OUT" "$OPEN_ERR"

if [[ "$OPEN_RC" -ne 0 ]]; then
    cat "$OPEN_OUT" "$OPEN_ERR" >&2
    fail "expected the parked body to open under -event-type $WRONG_EVENT_TYPE, got exit $OPEN_RC"
fi
# -json keeps Go's HTML escaping, so the placeholder travels as
# "\u003credacted\u003e", which jq decodes to <redacted>: compare the DECODED
# value, never the raw bytes.
jq -e --arg et "$WRONG_EVENT_TYPE" --arg id "$ORDER_ID" \
    '.envelope.eventType == $et and .document.orderId == $id and .document.card == "<redacted>"' \
    "$OPEN_OUT" >/dev/null \
    || fail "open-event opened the parked body, but not as the event minted above (etyp, orderId or redacted card differ)"
jq . "$OPEN_OUT"
echo
echo "✅ exit 0: this run's orderId, signed etyp '$WRONG_EVENT_TYPE', and the card"
echo "   decrypted but rendered as \"<redacted>\" — open-event's default, with no"
echo "   length hint. -print-subject would print it; it is fixture-only and never"
echo "   used here."
echo
echo "Other codes the same trick reaches, one seal-event flag at a time — and"
echo "open-event names each one from the parked bytes the same way:"
echo "  -sign-kid payments-sign-v9      -> SEAL_KID_UNKNOWN_GENERATION (recoverable:"
echo "                                     right family, generation not provisioned —"
echo "                                     this is the rotation-lag signature)"
echo "  -sign-kid tokens-our-v1         -> SEAL_KID_FAMILY_MISMATCH"
echo "  flip one byte in the body       -> a rule-class, not one code: a payload or"
echo "                                     signature byte fails rule 5"
echo "                                     (SEAL_SIGNATURE_INVALID); a header byte"
echo "                                     usually fails earlier, on rules 1-4"
echo "                                     (NOT_SEALED / SEAL_ALG_NOT_ALLOWED /"
echo "                                     SEAL_CTY_INVALID / SEAL_KID_*)"
echo
echo "SEAL_MANIFEST_MISMATCH (rule 9, signed sp vs the declared sealed set) is NOT"
echo "reachable by changing a seal-event flag. -subject only accepts a member the"
echo "document actually has — '-subject holder' is nested inside card, so it fails"
echo "at SEAL time with SEAL_DOCUMENT_INVALID and never reaches a consumer — and"
echo "pinning any other top-level member would ship the card UNSEALED. Minting a"
echo "real manifest mismatch needs custom tooling, not seal-event. open-event reaches"
echo "rule 9 from the CONSUMER side instead: declare a sealed set the body was not"
echo "signed with ('-subject amount') and it refuses with SEAL_MANIFEST_MISMATCH —"
echo "declaration drift on the consumer, not a forged message."
echo
echo "Clean up the parked message when you are done:"
echo "  curl -u $RABBIT_USER:<pass> -X DELETE '$RABBIT_MGMT/api/queues/$RABBIT_VHOST/$DLQ/contents'"
echo
echo "Next: 'make show-sealed-message' for the producer half — the same envelope,"
echo "read off the broker after the app itself published it."
