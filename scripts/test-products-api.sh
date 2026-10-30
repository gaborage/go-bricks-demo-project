#!/usr/bin/env bash
# scripts/test-products-api.sh
#
# End-to-end smoke of the products API against a running app (`make run`).
# Walks the full CRUD cycle and asserts the three response contracts the demo
# exists to show off:
#
#   * enveloped routes wrap every body in {"data": ..., "meta": {...}} and the
#     meta carries a traceId (APIResponse envelope)
#   * the legacy module serves the SAME product raw — no envelope — via
#     server.WithRawResponse() (Strangler Fig migration pattern)
#   * a validation failure answers 400 with error.details.validationErrors,
#     which is gated on app.debug + a development environment (go-bricks
#     v0.61.0, ADR-084) — if this check fails first look at app.debug in
#     config.development.yaml
#   * a product id that is not a UUID answers 400 at the boundary, before any
#     query reaches the database
#
# Three checks need go-bricks v0.73.0:
#
#   * GET /products/<id>:activity is an inline-verb route (C73.10) the activity
#     module registers beside GET /products/:id; it answers that product's
#     slice of the stream projection
#   * a +json media type and any letter case of application/json bind on a
#     typed route; both answered 400 on v0.72.0
#   * a body over server.bodylimit answers 413 BODY_TOO_LARGE with a known
#     length and chunked alike (C73.6, ADR-152); v0.72.0 answered 413
#     INTERNAL_ERROR and 400 BAD_REQUEST
#
# The paginated list is exercised on purpose: its COUNT(*) goes through
# qb.MustExpr, the identifier-grammar hatch (go-bricks v0.60.0, ADR-082),
# which is a RUNTIME rejection — `go build` alone cannot prove it works.
#
# Prerequisites:
#   make docker-up && make migrate    # postgres (products table)
#   make run                          # app on :8080
#
# Overrides (env): API_BASE, SERVER_BODYLIMIT (the app's body limit in bytes,
#                  when it runs with one other than the 10 MB default)
#
# Exit code 0 = every check passed. First failure aborts with ❌.

set -euo pipefail

API_BASE="${API_BASE:-http://localhost:8080/api/v1}"
# The framework default for server.bodylimit (config.DefaultBodyLimitBytes).
BODY_LIMIT="${SERVER_BODYLIMIT:-10485760}"

PASS=0

section() {
    printf '\n── %s ──────────────────────────\n' "$*"
}

fail() {
    echo "❌ $*" >&2
    exit 1
}

ok() {
    echo "✅ $*"
    PASS=$((PASS + 1))
}

for tool in curl jq; do
    command -v "$tool" >/dev/null || fail "required tool '$tool' not found"
done
[[ "$BODY_LIMIT" =~ ^[0-9]+$ ]] || fail "SERVER_BODYLIMIT='$BODY_LIMIT' must be a byte count"

# One response file and one upload file for the whole run, from mktemp rather
# than guessable /tmp names another user could plant first as symlinks. The EXIT
# trap removes both even when fail() aborts mid-run.
BODY_FILE="$(mktemp)"
UPLOAD_FILE="$(mktemp)"
trap 'rm -f "$BODY_FILE" "$UPLOAD_FILE"' EXIT

# request METHOD PATH [JSON_BODY [CONTENT_TYPE]] — sets RESP_STATUS and
# RESP_BODY. A body is sent as application/json unless CONTENT_TYPE says
# otherwise.
request() {
    local method="$1" path="$2" body="${3:-}" ctype="${4:-application/json}"
    local args=(-sS -X "$method" "$API_BASE$path" -o "$BODY_FILE" -w '%{http_code}')
    [[ -n "$body" ]] && args+=(-H "Content-Type: $ctype" -d "$body")
    RESP_STATUS="$(curl "${args[@]}")" || fail "curl $method $path failed — is the app running? (make run)"
    RESP_BODY="$(cat "$BODY_FILE")"
}

# upload PATH [CURL_ARGS...] — POST UPLOAD_FILE as application/json; sets
# RESP_STATUS and RESP_BODY. curl sends a Content-Length unless CURL_ARGS ask
# for chunked transfer.
upload() {
    local path="$1"
    shift
    RESP_STATUS="$(curl -sS -X POST "$API_BASE$path" -H 'Content-Type: application/json' \
        --data-binary "@$UPLOAD_FILE" "$@" -o "$BODY_FILE" -w '%{http_code}')" \
        || fail "curl POST $path ($(wc -c <"$UPLOAD_FILE" | tr -d ' ') bytes) failed"
    RESP_BODY="$(cat "$BODY_FILE")"
}

# jq_get FILTER — evaluate FILTER against RESP_BODY, empty string if missing.
jq_get() {
    local filter="$1"
    jq -r "$filter // empty" <<<"$RESP_BODY"
}

expect_status() {
    local want="$1" label="$2"
    [[ "$RESP_STATUS" == "$want" ]] || fail "$label: expected HTTP $want, got $RESP_STATUS — body: $(head -c 300 <<<"$RESP_BODY")"
}

# ---------------------------------------------------------------------------

section "0/10  Preflight — app is up"

request GET /health
expect_status 200 "GET /health"
ok "app answers on $API_BASE"

section "1/10  Create — POST /products (201, enveloped)"

STAMP="$(date +%s)"
request POST /products "{\"name\":\"api-test-$STAMP\",\"description\":\"created by test-products-api.sh\",\"price\":42.50,\"imageUrl\":\"https://example.com/api-test.png\"}"
expect_status 201 "POST /products"

PRODUCT_ID="$(jq_get '.data.id')"
[[ -n "$PRODUCT_ID" ]] || fail "create response has no data.id — envelope broken?"
[[ -n "$(jq_get '.meta.traceId')" ]] || fail "create response meta carries no traceId"
ok "created $PRODUCT_ID inside the APIResponse envelope (meta.traceId present)"

section "2/10  Read — GET /products/:id"

request GET "/products/$PRODUCT_ID"
expect_status 200 "GET /products/$PRODUCT_ID"
[[ "$(jq_get '.data.name')" == "api-test-$STAMP" ]] || fail "read-back name mismatch"
[[ "$(jq_get '.data.price')" == "42.5" ]] || fail "read-back price mismatch: $(jq_get '.data.price')"
ok "read back the created product"

section "3/10  List + pagination — GET /products?page=1&pageSize=2 (MustExpr COUNT path)"

request GET '/products?page=1&pageSize=2'
expect_status 200 "GET /products (paginated)"
LISTED="$(jq -r '.data.products | length' <<<"$RESP_BODY")"
TOTAL="$(jq_get '.data.total')"
[[ "$LISTED" -le 2 ]] || fail "pageSize=2 returned $LISTED products"
[[ -n "$TOTAL" && "$TOTAL" -ge 1 ]] || fail "paginated list carries no data.total — the MustExpr COUNT(*) query broke at runtime"
ok "page of $LISTED products, total=$TOTAL (COUNT(*) through the qb.MustExpr hatch works)"

section "4/10  Update — PUT /products/:id"

request PUT "/products/$PRODUCT_ID" "{\"name\":\"api-test-$STAMP-v2\",\"description\":\"updated by test-products-api.sh\",\"price\":43.00,\"imageUrl\":\"https://example.com/api-test.png\"}"
expect_status 200 "PUT /products/$PRODUCT_ID"
[[ "$(jq_get '.data.name')" == "api-test-$STAMP-v2" ]] || fail "update did not change the name"
ok "updated the product"

section "5/10  Inline verb — GET /products/:id:activity (activity module, go-bricks v0.73.0)"

# Registered as /products/:id\:activity. The `\:` after the parameter name makes
# `:activity` a verb: the route matches only <id>:activity, binds id to the part
# before the colon, and boots beside the products module's GET /products/:id.
# v0.72.0 refused that pair at startup as a duplicate.
request GET "/products/${PRODUCT_ID}:activity"
expect_status 200 "GET /products/${PRODUCT_ID}:activity"
[[ "$(jq_get '.data.productId')" == "$PRODUCT_ID" ]] \
    || fail "the verb route did not bind id to the part before ':activity' — got productId '$(jq_get '.data.productId')'"
ok "the verb reached the activity module with id $PRODUCT_ID"

# The stream consumer projects the create and the update asynchronously, so the
# counts can trail the writes. They are printed, not asserted: the activity lane
# is best-effort, and this script tests the products API.
for _ in 1 2 3 4 5 6 7 8 9 10; do
    [[ "$(jq_get '.data.delivered')" -ge 2 ]] && break
    sleep 1
    request GET "/products/${PRODUCT_ID}:activity"
done
echo "   projected for this product: $(jq -c '.data.counts' <<<"$RESP_BODY") ($(jq_get '.data.recent | length') in the recent ring)"

section "6/10  Legacy raw route — GET /legacy/products/:id (no envelope)"

request GET "/legacy/products/$PRODUCT_ID"
expect_status 200 "GET /legacy/products/$PRODUCT_ID"
[[ "$(jq_get '.id')" == "$PRODUCT_ID" ]] || fail "legacy route: expected a TOP-LEVEL id (raw response), got: $(head -c 200 <<<"$RESP_BODY")"
[[ -z "$(jq_get '.data')" ]] || fail "legacy route is wrapped in an APIResponse envelope — WithRawResponse() contract broken"
ok "same product, raw JSON — WithRawResponse() bypasses the envelope"

section "7/10  Validation failures — a bad body, and a malformed id (400)"

request POST /products '{"price":-1}'
expect_status 400 "POST /products (invalid)"
[[ "$(jq_get '.error.code')" == "BAD_REQUEST" ]] || fail "expected error.code=BAD_REQUEST, got '$(jq_get '.error.code')'"
[[ -n "$(jq_get '.error.details.validationErrors')" ]] \
    || fail "error.details.validationErrors missing — check app.debug: true in config.development.yaml (ADR-084 gates details on debug + dev env)"
ok "400 carries error.details.validationErrors (app.debug + dev env gates pass)"

# A product id is validated as a UUID at the boundary, so a malformed one is
# refused before any query. It used to reach PostgreSQL's UUID column and
# answer 500.
request GET /products/not-a-uuid
expect_status 400 "GET /products/not-a-uuid"
[[ "$(jq_get '.error.code')" == "BAD_REQUEST" ]] || fail "malformed id: expected error.code=BAD_REQUEST, got '$(jq_get '.error.code')'"
ok "a malformed product id answers 400 at the boundary, not 500 from the database"

section "8/10  JSON media types — POST /products as +json and as Application/JSON (go-bricks v0.73.0)"

# Typed routes decode every application/json spelling and every */*+json type
# themselves (go-bricks #2249). On v0.72.0 both bodies below answered 400
# "Invalid request data": echo's binder took only the exact, lower-case
# application/json. A non-JSON or empty Content-Type still skips the body.
for ctype in 'application/vnd.gobricks.product+json' 'Application/JSON'; do
    request POST /products "{\"name\":\"api-test-$STAMP-media\",\"description\":\"sent as $ctype\",\"price\":1.00}" "$ctype"
    expect_status 201 "POST /products as $ctype"
    [[ "$(jq_get '.data.name')" == "api-test-$STAMP-media" ]] || fail "POST /products as $ctype: the body did not bind"
    MEDIA_ID="$(jq_get '.data.id')"
    request DELETE "/products/$MEDIA_ID"
    expect_status 204 "DELETE /products/$MEDIA_ID"
    ok "$ctype bound the body (201), then cleaned up"
done

section "9/10  Over-limit body — 413 BODY_TOO_LARGE, known length and chunked (go-bricks v0.73.0)"

# One byte over the limit. The body is a single long JSON string on purpose: a
# chunked body is decoded as it streams in, so anything that stops being valid
# JSON earlier answers 400 before the limit is ever reached.
JSON_OPEN='{"name":"'
JSON_CLOSE='"}'
PAD=$((BODY_LIMIT + 1 - ${#JSON_OPEN} - ${#JSON_CLOSE}))
{
    printf '%s' "$JSON_OPEN"
    head -c "$PAD" /dev/zero | tr '\0' 'a'
    printf '%s' "$JSON_CLOSE"
} >"$UPLOAD_FILE"

# Known length: the body-limit middleware refuses on Content-Length alone.
# v0.72.0 answered 413 with INTERNAL_ERROR.
upload /products
expect_status 413 "POST /products, $((BODY_LIMIT + 1)) bytes with Content-Length"
[[ "$(jq_get '.error.code')" == "BODY_TOO_LARGE" ]] \
    || fail "known-length overflow: expected error.code=BODY_TOO_LARGE, got '$(jq_get '.error.code')'"
ok "Content-Length over the limit → 413 BODY_TOO_LARGE"

# Unknown length: the limit trips while the typed route decodes the body.
# v0.72.0 answered 400 BAD_REQUEST. HTTP/1.1 is forced because chunked transfer
# exists only there: over HTTP/2 curl would send the body without it.
upload /products --http1.1 -H 'Transfer-Encoding: chunked'
expect_status 413 "POST /products, $((BODY_LIMIT + 1)) bytes chunked"
[[ "$(jq_get '.error.code')" == "BODY_TOO_LARGE" ]] \
    || fail "chunked overflow: expected error.code=BODY_TOO_LARGE, got '$(jq_get '.error.code')'"
ok "chunked body over the limit → 413 BODY_TOO_LARGE"

section "10/10  Delete — DELETE /products/:id, then 404"

request DELETE "/products/$PRODUCT_ID"
expect_status 204 "DELETE /products/$PRODUCT_ID"
request GET "/products/$PRODUCT_ID"
expect_status 404 "GET after delete"
ok "deleted; subsequent read answers 404"

printf '\n🎉 %d/%d checks passed\n' "$PASS" "$PASS"
