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
#
# The paginated list is exercised on purpose: its COUNT(*) goes through
# qb.MustExpr, the identifier-grammar hatch (go-bricks v0.60.0, ADR-082),
# which is a RUNTIME rejection — `go build` alone cannot prove it works.
#
# Prerequisites:
#   make docker-up && make migrate    # postgres (products table)
#   make run                          # app on :8080
#
# Overrides (env): API_BASE
#
# Exit code 0 = every check passed. First failure aborts with ❌.

set -euo pipefail

API_BASE="${API_BASE:-http://localhost:8080/api/v1}"

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

# request METHOD PATH [JSON_BODY] — sets RESP_STATUS and RESP_BODY.
request() {
    local method="$1" path="$2" body="${3:-}"
    local args=(-sS -X "$method" "$API_BASE$path" -o /tmp/products-api-body.$$ -w '%{http_code}')
    [[ -n "$body" ]] && args+=(-H 'Content-Type: application/json' -d "$body")
    RESP_STATUS="$(curl "${args[@]}")" || fail "curl $method $path failed — is the app running? (make run)"
    RESP_BODY="$(cat /tmp/products-api-body.$$)"
    rm -f /tmp/products-api-body.$$
}

# jq_get FILTER — evaluate FILTER against RESP_BODY, empty string if missing.
jq_get() {
    jq -r "$1 // empty" <<<"$RESP_BODY"
}

expect_status() {
    [[ "$RESP_STATUS" == "$1" ]] || fail "$2: expected HTTP $1, got $RESP_STATUS — body: $(head -c 300 <<<"$RESP_BODY")"
}

# ---------------------------------------------------------------------------

section "0/7  Preflight — app is up"

request GET /health
expect_status 200 "GET /health"
ok "app answers on $API_BASE"

section "1/7  Create — POST /products (201, enveloped)"

STAMP="$(date +%s)"
request POST /products "{\"name\":\"api-test-$STAMP\",\"description\":\"created by test-products-api.sh\",\"price\":42.50,\"imageUrl\":\"https://example.com/api-test.png\"}"
expect_status 201 "POST /products"

PRODUCT_ID="$(jq_get '.data.id')"
[[ -n "$PRODUCT_ID" ]] || fail "create response has no data.id — envelope broken?"
[[ -n "$(jq_get '.meta.traceId')" ]] || fail "create response meta carries no traceId"
ok "created $PRODUCT_ID inside the APIResponse envelope (meta.traceId present)"

section "2/7  Read — GET /products/:id"

request GET "/products/$PRODUCT_ID"
expect_status 200 "GET /products/$PRODUCT_ID"
[[ "$(jq_get '.data.name')" == "api-test-$STAMP" ]] || fail "read-back name mismatch"
[[ "$(jq_get '.data.price')" == "42.5" ]] || fail "read-back price mismatch: $(jq_get '.data.price')"
ok "read back the created product"

section "3/7  List + pagination — GET /products?page=1&pageSize=2 (MustExpr COUNT path)"

request GET '/products?page=1&pageSize=2'
expect_status 200 "GET /products (paginated)"
LISTED="$(jq -r '.data.products | length' <<<"$RESP_BODY")"
TOTAL="$(jq_get '.data.total')"
[[ "$LISTED" -le 2 ]] || fail "pageSize=2 returned $LISTED products"
[[ -n "$TOTAL" && "$TOTAL" -ge 1 ]] || fail "paginated list carries no data.total — the MustExpr COUNT(*) query broke at runtime"
ok "page of $LISTED products, total=$TOTAL (COUNT(*) through the qb.MustExpr hatch works)"

section "4/7  Update — PUT /products/:id"

request PUT "/products/$PRODUCT_ID" "{\"name\":\"api-test-$STAMP-v2\",\"description\":\"updated by test-products-api.sh\",\"price\":43.00,\"imageUrl\":\"https://example.com/api-test.png\"}"
expect_status 200 "PUT /products/$PRODUCT_ID"
[[ "$(jq_get '.data.name')" == "api-test-$STAMP-v2" ]] || fail "update did not change the name"
ok "updated the product"

section "5/7  Legacy raw route — GET /legacy/products/:id (no envelope)"

request GET "/legacy/products/$PRODUCT_ID"
expect_status 200 "GET /legacy/products/$PRODUCT_ID"
[[ "$(jq_get '.id')" == "$PRODUCT_ID" ]] || fail "legacy route: expected a TOP-LEVEL id (raw response), got: $(head -c 200 <<<"$RESP_BODY")"
[[ -z "$(jq_get '.data')" ]] || fail "legacy route is wrapped in an APIResponse envelope — WithRawResponse() contract broken"
ok "same product, raw JSON — WithRawResponse() bypasses the envelope"

section "6/7  Validation failure — POST /products with a bad body (400 + details)"

request POST /products '{"price":-1}'
expect_status 400 "POST /products (invalid)"
[[ "$(jq_get '.error.code')" == "BAD_REQUEST" ]] || fail "expected error.code=BAD_REQUEST, got '$(jq_get '.error.code')'"
[[ -n "$(jq_get '.error.details.validationErrors')" ]] \
    || fail "error.details.validationErrors missing — check app.debug: true in config.development.yaml (ADR-084 gates details on debug + dev env)"
ok "400 carries error.details.validationErrors (app.debug + dev env gates pass)"

section "7/7  Delete — DELETE /products/:id, then 404"

request DELETE "/products/$PRODUCT_ID"
expect_status 204 "DELETE /products/$PRODUCT_ID"
request GET "/products/$PRODUCT_ID"
expect_status 404 "GET after delete"
ok "deleted; subsequent read answers 404"

printf '\n🎉 %d/%d checks passed\n' "$PASS" "$PASS"
