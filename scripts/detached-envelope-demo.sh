#!/usr/bin/env bash
# scripts/detached-envelope-demo.sh
#
# Runnable proof for the tokens module's detached-signature gateway
# (go-bricks v0.73.0, ADR-150), driven by `make detached-envelope-demo`.
#
# POST /api/v1/tokens/detached serves a kid-less peer:
#   - body       application/json {"data":"<compact JWE>"}, RSA-OAEP-256 +
#                A256GCM, no kid;
#   - header     X-Peer-Signature: JWS <header>..<signature>, an RS512 JWS over
#                the exact JWE string with its payload segment emptied
#                (RFC 7515 Appendix F), no kid, no cty;
#   - reply      {"data":"<kid-less bare JWE>"} sealed to the peer, or
#                {"code":"<CODE>"} on a refusal.
# POST /api/v1/__sim/peer/detached plays that peer in-process: it mints the
# request, calls the gateway over loopback HTTP, opens the reply, and returns
# the token plus the three ciphertext artefacts that crossed the wire.
#
# Steps:
#   1. preflight: /health answers 200;
#   2. one exchange through the simulator; the PAN appears nowhere in its reply;
#   3. decode the three protected headers and assert they carry no kid;
#   4. replay the captured request verbatim: accepted, with a fresh seal (the
#      gateway keeps no replay ledger; see the stance printed below);
#   5. one request per refusal, each asserted by status and code;
#   6. print the code table.
#
# The script reads no app log: it runs against `make run`, so log hygiene
# (refusals log the jose code only, never a cause, ciphertext or PAN) is pinned
# by Go tests in internal/modules/tokens/handlers.
#
# DEMO DATA ONLY: the documented Visa test PAN 4111111111111111, never echoed.
#
# Prerequisites: the app running (make run), keys present (make generate-keys).
# Requires curl, jq, openssl and base64.
#
# The CTY_REJECTED case must sign a new header as the peer, so it needs the
# private half of the TARGET's tokens.detached.peer entry. It uses the local
# file PEER_SIGN_KEY (default certs/tokens_detached_peer_private.der, the one
# the dev keystore loads) only after proving that this key signed the exchange
# in step 2. Against a deployed APP_URL with other keys, the local file does not
# match, and the case is reported as skipped rather than failed.
#
# Overrides (env): APP_URL (default http://localhost:8080), PEER_SIGN_KEY

# pass() always returns 0, so every `cond && pass ... || fail ...` below is a
# true if-then-else.
# shellcheck disable=SC2015
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR/.."

APP_URL="${APP_URL:-http://localhost:8080}"
API="${APP_URL%/}/api/v1"
GATEWAY="$API/tokens/detached"
SIMULATOR="$API/__sim/peer/detached"

# Must match internal/modules/tokens: handlers.DetachedSignatureHeader, the
# 8 KiB header bound, and the DetachedPeerKid entry's DER file.
SIG_HEADER="X-Peer-Signature"
MAX_SIG_HEADER_BYTES=8192
PEER_SIGN_KEY="${PEER_SIGN_KEY:-certs/tokens_detached_peer_private.der}"

TEST_PAN="4111111111111111"     # documented Visa test PAN
LUHN_BAD_PAN="4111111111111112" # passes the simulator's format check, fails Luhn

for tool in curl jq openssl base64; do
    command -v "$tool" >/dev/null 2>&1 || { echo "❌ $tool is required" >&2; exit 1; }
done

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

FAILURES=0
SUMMARY=()

pass() { echo "   ✅ $*"; }
fail() { echo "   ❌ $*"; FAILURES=$((FAILURES + 1)); }
die()  { echo "❌ $*" >&2; exit 1; }

# b64url_encode reads stdin.
b64url_encode() { base64 | tr -d '\n=' | tr '+/' '-_'; }

b64url_decode() {
    local s="${1//-/+}"
    s="${s//_//}"
    case $(( ${#s} % 4 )) in
        2) s="$s==" ;;
        3) s="$s=" ;;
    esac
    printf '%s' "$s" | base64 --decode
}

# header_of prints the decoded protected header (first segment) of a compact.
header_of() { b64url_decode "${1%%.*}" | jq -c .; }

# assert_no_pan fails the run if the test PAN appears in a file, without
# printing it.
assert_no_pan() {
    if grep -qF "$TEST_PAN" "$1"; then
        fail "$2: the PAN appears in it"
    else
        pass "$2 carries no PAN"
    fi
}

# post_gateway CONTENT_TYPE BODY_FILE [SIGNATURE_HEADER_VALUE...]
# Each value is sent as its own header line; none means no header at all.
# Prints the HTTP status; the body lands in $WORK/resp.
post_gateway() {
    local content_type="$1" body_file="$2"
    shift 2
    local args=()
    local value
    for value in "$@"; do
        args+=(-H "$SIG_HEADER: $value")
    done
    curl -sS -o "$WORK/resp" -w '%{http_code}' -X POST "$GATEWAY" \
        -H "Content-Type: $content_type" ${args[@]+"${args[@]}"} \
        --data-binary @"$body_file"
}

# expect CASE WANT_STATUS WANT_CODE CONTENT_TYPE BODY_FILE [SIGNATURE...]
# A refusal must answer exactly {"code":WANT_CODE}; WANT_CODE "-" expects a
# 200 with a data member.
expect() {
    local name="$1" want_status="$2" want_code="$3"
    shift 3
    local status code
    status="$(post_gateway "$@")"
    if [[ "$want_code" == "-" ]]; then
        code="$(jq -r 'if (keys == ["data"]) then "data" else "unexpected body" end' "$WORK/resp" 2>/dev/null || echo "not JSON")"
        [[ "$status" == "$want_status" && "$code" == "data" ]] \
            && pass "$name → $status {\"data\":…}" \
            || fail "$name → $status $code (want $want_status with data)"
        SUMMARY+=("$(printf '%-52s %-4s %s' "$name" "$status" "(sealed data)")")
        return
    fi
    code="$(jq -r 'if (keys == ["code"]) then .code else "unexpected body" end' "$WORK/resp" 2>/dev/null || echo "not JSON")"
    [[ "$status" == "$want_status" && "$code" == "$want_code" ]] \
        && pass "$name → $status $code" \
        || fail "$name → $status $code (want $want_status $want_code)"
    SUMMARY+=("$(printf '%-52s %-4s %s' "$name" "$status" "$code")")
}

# body_file NAME JWE writes {"data":JWE} and prints its path.
body_file() {
    jq -cn --arg d "$2" '{data: $d}' >"$WORK/$1.json"
    echo "$WORK/$1.json"
}

echo "🔏 Detached-signature gateway demo (go-bricks v0.73.0, ADR-150) against $APP_URL"
echo ""

# --- 1. Preflight ---------------------------------------------------------------
echo "1. Preflight"
health="$(curl -s -o /dev/null -w '%{http_code}' "$API/health" || true)"
[[ "$health" == "200" ]] || die "GET $API/health answered '$health'. Start the app first: make run"
pass "GET /api/v1/health → 200"
echo ""

# --- 2. Exchange ----------------------------------------------------------------
echo "2. One exchange through the simulator (POST /api/v1/__sim/peer/detached)"
jq -cn --arg pan "$TEST_PAN" '{pan: $pan}' >"$WORK/sim-request.json"
status="$(curl -sS -o "$WORK/exchange.json" -w '%{http_code}' -X POST "$SIMULATOR" \
    -H 'Content-Type: application/json' --data-binary @"$WORK/sim-request.json")"
[[ "$status" == "200" ]] || die "the simulator answered $status: $(jq -c '.error // .' "$WORK/exchange.json" 2>/dev/null)"
pass "simulator → 200"
last4="$(jq -r '.data.token.last4' "$WORK/exchange.json")"
[[ "$last4" == "1111" ]] && pass "token for card ending $last4: $(jq -r '.data.token.token' "$WORK/exchange.json")" \
    || fail "token last4 is '$last4', want 1111"
assert_no_pan "$WORK/exchange.json" "the simulator's whole reply (token and wire)"

SIGNATURE="$(jq -r '.data.wire.signatureHeader' "$WORK/exchange.json")"
jq -j '.data.wire.requestBody' "$WORK/exchange.json" >"$WORK/request.json"
jq -j '.data.wire.replyBody' "$WORK/exchange.json" >"$WORK/reply.json"
JWE="$(jq -r '.data' "$WORK/request.json")"
REPLY_JWE="$(jq -r '.data' "$WORK/reply.json")"
echo ""

# --- 3. The wire ----------------------------------------------------------------
echo "3. What crossed the wire"
DETACHED="${SIGNATURE#JWS }"
[[ "$SIGNATURE" == "JWS "* ]] && pass "$SIG_HEADER carries the 'JWS ' scheme" || fail "$SIG_HEADER lacks the 'JWS ' scheme"
IFS='.' read -r SIG_PROTECTED SIG_MIDDLE SIG_VALUE <<<"$DETACHED"
segments="$(awk -F. '{print NF}' <<<"$DETACHED")"
[[ "$segments" == "3" && -z "$SIG_MIDDLE" ]] \
    && pass "detached JWS: 3 segments, empty payload segment (RFC 7515 Appendix F)" \
    || fail "detached JWS has $segments segments, middle '${SIG_MIDDLE:0:8}'"
outer="$(header_of "$DETACHED")"
echo "      signature header : $outer"
jq -e '.alg == "RS512" and (has("kid") | not) and (has("cty") | not)' <<<"$outer" >/dev/null \
    && pass "outer JWS: alg RS512, no kid, no cty" || fail "outer JWS header is $outer"

inner="$(header_of "$JWE")"
echo "      request JWE      : $inner"
jq -e '.alg == "RSA-OAEP-256" and .enc == "A256GCM" and (has("kid") | not)' <<<"$inner" >/dev/null \
    && pass "request JWE: RSA-OAEP-256 + A256GCM, no kid" || fail "request JWE header is $inner"

# Does the local peer key belong to the target? Derive its public half and
# verify the exchange's own signature with it: RS512 is RSASSA-PKCS1-v1_5 over
# SHA-512 of header.b64url(jwe).
LOCAL_PEER_KEY=0
if [[ -f "$PEER_SIGN_KEY" ]] \
    && openssl rsa -in "$PEER_SIGN_KEY" -inform DER -pubout -outform DER -out "$WORK/peer-public.der" 2>/dev/null; then
    b64url_decode "$SIG_VALUE" >"$WORK/signature.bin"
    if printf '%s' "$SIG_PROTECTED.$(printf '%s' "$JWE" | b64url_encode)" \
        | openssl dgst -sha512 -verify "$WORK/peer-public.der" -keyform DER -signature "$WORK/signature.bin" >/dev/null 2>&1; then
        LOCAL_PEER_KEY=1
        pass "the signature verifies under $PEER_SIGN_KEY: the local peer key is the target's"
    fi
fi
(( LOCAL_PEER_KEY )) || echo "   ⏭  $PEER_SIGN_KEY did not sign this exchange: the case that must sign as the peer is skipped"

reply="$(header_of "$REPLY_JWE")"
echo "      reply JWE        : $reply"
jq -e '(has("kid") | not) and (has("cty") | not) and (has("typ") | not)' <<<"$reply" >/dev/null \
    && pass "reply JWE: no kid, no cty, no typ (OmitKid)" || fail "reply JWE header is $reply"
echo ""

# --- 4. Replay ------------------------------------------------------------------
echo "4. Replay the captured request verbatim"
status="$(post_gateway application/json "$WORK/request.json" "$SIGNATURE")"
replayed="$(jq -r '.data // empty' "$WORK/resp")"
if [[ "$status" == "200" && -n "$replayed" && "$replayed" != "$REPLY_JWE" ]]; then
    pass "replay → 200 with a fresh seal (new CEK and IV, so the data differs)"
else
    fail "replay → $status"
fi
SUMMARY+=("$(printf '%-52s %-4s %s' "replay of the captured request" "$status" "(sealed data)")")
cat <<'STANCE'
      Stance: this gateway keeps no replay ledger. A replay re-tokenizes the same
      PAN to the same token, and the reply is sealed to the peer's own key, so a
      replayer learns nothing it did not already hold. A production gateway that
      must refuse replays records SHA-256 of the JWE string, computed only after
      jose.Open succeeded, through jose.ReplayRecorder.GetOrSet, in one ledger
      per tenant shared by every route of the profile.
STANCE
echo ""

# --- 5. Refusals ----------------------------------------------------------------
echo "5. Refusals"
REQUEST="$WORK/request.json"

expect "no signature header" 401 SIGNATURE_HEADER_INVALID application/json "$REQUEST"
expect "signature header sent twice" 401 SIGNATURE_HEADER_INVALID application/json "$REQUEST" "$SIGNATURE" "$SIGNATURE"
expect "Bearer scheme instead of JWS" 401 SIGNATURE_HEADER_INVALID application/json "$REQUEST" "Bearer $DETACHED"
oversize="JWS $(head -c "$MAX_SIG_HEADER_BYTES" /dev/zero | tr '\0' 'a')"
expect "signature header over 8 KiB" 400 SIGNATURE_HEADER_INVALID application/json "$REQUEST" "$oversize"

# The attached form: the JWE string filled into the empty payload segment.
attached="$SIG_PROTECTED.$(printf '%s' "$JWE" | b64url_encode).$SIG_VALUE"
expect "attached compact in the header" 400 MALFORMED application/json "$REQUEST" "JWS $attached"

# Open checks the outer alg before it touches a key, then the kid, then the
# signature, so a rewritten header needs no valid signature to be refused.
rs256="$(printf '%s' '{"alg":"RS256"}' | b64url_encode)"
expect "outer alg RS256" 400 ALGORITHM_REJECTED application/json "$REQUEST" "JWS $rs256..$SIG_VALUE"
kid_header="$(printf '%s' '{"alg":"RS512","kid":"tokens.peer"}' | b64url_encode)"
expect "outer kid naming another entry" 401 KEY_UNKNOWN application/json "$REQUEST" "JWS $kid_header..$SIG_VALUE"

# One character of the JWE ciphertext segment changed after signing.
IFS='.' read -r j_hdr j_key j_iv j_ct j_tag <<<"$JWE"
mid=$(( ${#j_ct} / 2 ))
[[ "${j_ct:mid:1}" == "A" ]] && swap="B" || swap="A"
tampered="$j_hdr.$j_key.$j_iv.${j_ct:0:mid}$swap${j_ct:mid+1}.$j_tag"
expect "one ciphertext character changed" 401 SIGNATURE_INVALID application/json "$(body_file tampered "$tampered")" "$SIGNATURE"
expect "data swapped for the reply's JWE" 401 SIGNATURE_INVALID application/json "$(body_file swapped "$REPLY_JWE")" "$SIGNATURE"

# The outer cty is judged only after the signature verifies, so this header is
# signed for real with the peer's key, exactly as the peer would sign it. That
# needs the target's peer private key, proven in step 3.
if (( LOCAL_PEER_KEY )); then
    cty_header="$(printf '%s' '{"alg":"RS512","cty":"JWS"}' | b64url_encode)"
    cty_sig="$(printf '%s' "$cty_header.$(printf '%s' "$JWE" | b64url_encode)" \
        | openssl dgst -sha512 -sign "$PEER_SIGN_KEY" -keyform DER | b64url_encode)"
    expect "outer cty JWS, validly signed" 400 CTY_REJECTED application/json "$REQUEST" "JWS $cty_header..$cty_sig"
else
    echo "   ⏭  outer cty JWS, validly signed: skipped: needs the peer's private key ($PEER_SIGN_KEY does not match the target)"
    SUMMARY+=("$(printf '%-52s %-4s %s' "outer cty JWS, validly signed" "-" "skipped (needs the peer's private key)")")
fi

expect "Content-Type text/plain (body not bound)" 400 DATA_MISSING text/plain "$REQUEST" "$SIGNATURE"
expect "empty data member" 400 DATA_MISSING application/json "$(body_file empty "")" "$SIGNATURE"

# A PAN that passes the simulator's format check but fails the gateway's Luhn
# check: the gateway answers PAYLOAD_INVALID, which the simulator surfaces.
jq -cn --arg pan "$LUHN_BAD_PAN" '{pan: $pan}' >"$WORK/sim-luhn.json"
status="$(curl -sS -o "$WORK/sim-luhn-resp.json" -w '%{http_code}' -X POST "$SIMULATOR" \
    -H 'Content-Type: application/json' --data-binary @"$WORK/sim-luhn.json")"
code="$(jq -r '.error.code // "none"' "$WORK/sim-luhn-resp.json")"
[[ "$status" == "400" && "$code" == "PAYLOAD_INVALID" ]] \
    && pass "Luhn-invalid PAN via the simulator → $status $code" \
    || fail "Luhn-invalid PAN via the simulator → $status $code (want 400 PAYLOAD_INVALID)"
SUMMARY+=("$(printf '%-52s %-4s %s' "Luhn-invalid PAN (via the simulator)" "$status" "$code")")

# go-bricks 424a51e9: any */*+json type binds a typed route's body. v0.72.0
# skipped this body and the gateway would have answered DATA_MISSING.
expect "Content-Type application/vnd.peer+json" 200 - application/vnd.peer+json "$REQUEST" "$SIGNATURE"
echo ""

# --- 6. Summary -----------------------------------------------------------------
echo "6. Code table"
printf '   %-52s %-4s %s\n' "Request" "HTTP" "Reply"
for row in "${SUMMARY[@]}"; do
    echo "   $row"
done
echo ""
if (( FAILURES > 0 )); then
    echo "❌ $FAILURES check(s) failed"
    exit 1
fi
echo "✅ Detached-signature gateway behaves as documented"
