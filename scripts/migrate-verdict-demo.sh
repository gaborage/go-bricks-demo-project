#!/usr/bin/env bash
# scripts/migrate-verdict-demo.sh
#
# Run verdicts and exit codes for the multi-tenant migration CLI
# (go-bricks v0.67.0, ADR-115, #1770/#1771).
#
# Every go-bricks-migrate migrate/validate/info run now ends with exactly one
# summary record, and the process exit code carries the same verdict:
#
#   0  clean              every listed tenant was attempted and none failed
#   1  fleet_split        at least one tenant attempted AND at least one failed
#                         or was never attempted — the fleet is at mixed versions
#   2  nothing_attempted  no tenant was attempted, so no schema was touched
#
# `make` stops on any non-zero exit, so the migrate-multitenant-* targets can't
# tell 1 from 2. This script runs the CLI directly, three ways, and prints the
# exit code and the `--json` summary record each time:
#
#   1. the real fleet (config.multitenant.yaml)       -> 0, clean
#   2. a throwaway fleet whose listing is empty       -> 2, nothing_attempted
#   3. the real fleet plus one unreachable tenant,    -> 1, fleet_split
#      with --continue-on-error
#
# The unreachable tenant points at the same Postgres as the real ones but names
# a role and a database that do not exist, and carries no password at all — no
# credential, real or fake, is written anywhere. Postgres refuses the login, so
# that one tenant fails while its siblings pass.
#
# READ-ONLY. The action is `validate` (or `info`); this script never runs
# `migrate`, so no tenant schema is created, changed or dropped. The throwaway
# configs live in a private temp dir removed on exit.
#
# Prerequisites:
#   make migrate-multitenant-install   # go-bricks-migrate at GO_BRICKS_REF (>= v0.67.0)
#   make docker-up                     # the demo postgres container
#   make migrate-multitenant-init      # tenant roles + schemas
#   make migrate-multitenant-up        # validate needs an applied fleet: Flyway
#                                      # validate fails on a pending migration
#
# Overrides (env; the Makefile passes its own values):
#   GO_BRICKS_MIGRATE, MULTITENANT_CONFIG, MULTITENANT_FLYWAY_CONF,
#   MULTITENANT_MIGRATIONS_DIR, MULTITENANT_FLYWAY_PATH,
#   VERDICT_ACTION   validate (default) | info — `info` needs only the init step
#   PG_PORT          the HOST port postgres is published on. Applied only when
#                    Flyway runs on the host (MULTITENANT_FLYWAY_PATH=flyway):
#                    scripts/flyway-docker.sh dials postgres INSIDE the compose
#                    network, where the container port in the config is right.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR/.."

GO_BRICKS_MIGRATE="${GO_BRICKS_MIGRATE:-go-bricks-migrate}"
MULTITENANT_CONFIG="${MULTITENANT_CONFIG:-config.multitenant.yaml}"
MULTITENANT_FLYWAY_CONF="${MULTITENANT_FLYWAY_CONF:-flyway/flyway-multitenant.conf}"
MULTITENANT_MIGRATIONS_DIR="${MULTITENANT_MIGRATIONS_DIR:-migrations-multitenant}"
MULTITENANT_FLYWAY_PATH="${MULTITENANT_FLYWAY_PATH:-scripts/flyway-docker.sh}"
VERDICT_ACTION="${VERDICT_ACTION:-validate}"

# The tenant case 3 appends. The ID sorts after acme/globex/initech, so the
# real tenants run first; with --continue-on-error the order does not change
# the verdict, only the reading order of the output.
GHOST_TENANT="unreachable"
GHOST_DATABASE="tenant_db_that_does_not_exist"

# --- helpers --------------------------------------------------------------

section() {
    printf '\n── %s ──────────────────────────\n' "$*"
}

fail() {
    echo "❌ $*" >&2
    exit 1
}

for tool in jq "$GO_BRICKS_MIGRATE"; do
    command -v "$tool" >/dev/null 2>&1 || fail "$tool is required but not on PATH (go-bricks-migrate: make migrate-multitenant-install)"
done

case "$VERDICT_ACTION" in
    validate | info) ;;
    *) fail "VERDICT_ACTION must be validate or info (got '$VERDICT_ACTION'); this demo never migrates" ;;
esac

# A migrator identity in the environment overrides every tenant's own role
# (#1766): one of the pair set makes EVERY run exit 2 before dispatch, both set
# connects each tenant as the migrator. Either way the three cases below would
# not show what they claim. Presence is checked, the values are never read.
for var in GOBRICKS_MIGRATE_MIGRATOR_USER GOBRICKS_MIGRATE_MIGRATOR_PASSWORD; do
    [[ -z "${!var+set}" ]] || fail "unset $var first — a migrator identity overrides every tenant's own role"
done

[[ -f "$MULTITENANT_CONFIG" ]] || fail "fleet config not found: $MULTITENANT_CONFIG"

# The throwaway configs. mktemp -d creates the directory 0700: the real-fleet
# copy below carries the demo's per-tenant dev passwords.
TMP_ROOT="${TMPDIR:-/tmp}"
WORK_DIR="$(mktemp -d "${TMP_ROOT%/}/migrate-verdict.XXXXXX")"
trap 'rm -rf "$WORK_DIR"' EXIT
# Signals become ordinary exits, so the EXIT trap removes the directory once and
# a Ctrl-C ends the demo with its own status instead of a jq error.
trap 'exit 130' INT
trap 'exit 143' TERM
trap 'exit 129' HUP

# --- the fleet config every case builds on -------------------------------

FLEET_CONFIG="$MULTITENANT_CONFIG"
case "$MULTITENANT_FLYWAY_PATH" in
    *flyway-docker.sh)
        if [[ -n "${PG_PORT:-}" ]]; then
            echo "ℹ️  PG_PORT=$PG_PORT not applied: Flyway runs inside the compose network"
            echo "   (scripts/flyway-docker.sh), where the config's container port is the right one."
        fi
        ;;
    *)
        if [[ -n "${PG_PORT:-}" ]]; then
            [[ "$PG_PORT" =~ ^[0-9]+$ ]] || fail "PG_PORT must be a port number (got '$PG_PORT')"
            # Host Flyway dials the host port. Rewrite every tenant's `port:` in a
            # copy; the tracked config is never edited.
            FLEET_CONFIG="$WORK_DIR/fleet.yaml"
            sed -E "s/^([[:space:]]+port:[[:space:]]*)[0-9]+[[:space:]]*$/\1${PG_PORT}/" \
                "$MULTITENANT_CONFIG" >"$FLEET_CONFIG"
            echo "ℹ️  Host Flyway: tenant port rewritten to PG_PORT=$PG_PORT in a temp copy of $MULTITENANT_CONFIG"
        fi
        ;;
esac

# first_value KEY FILE — the first `KEY: value` in FILE, i.e. the first tenant's.
first_value() {
    awk -v key="$1" '$1 == key":" { print $2; exit }' "$2"
}

# listed_count FILE — how many tenant IDs the CLI's own lister reads from FILE.
listed_count() {
    "$GO_BRICKS_MIGRATE" list --source-config "$1" --json | jq -r '.tenants | length'
}

# Case 2: an enabled fleet whose tenant listing comes back empty — what a
# control plane returning [] looks like to the CLI.
EMPTY_CONFIG="$WORK_DIR/empty-fleet.yaml"
cat >"$EMPTY_CONFIG" <<'YAML'
app:
  name: go-bricks-demo-migrate
  env: development

multitenant:
  enabled: true
  tenants: {}
YAML

# Case 3: the real fleet plus one tenant nobody can log in as. Same host and
# port as the first real tenant, so it takes the identical Flyway path.
GHOST_HOST="$(first_value host "$FLEET_CONFIG")"
GHOST_PORT="$(first_value port "$FLEET_CONFIG")"
[[ -n "$GHOST_HOST" && "$GHOST_PORT" =~ ^[0-9]+$ ]] \
    || fail "could not read a tenant host/port from $FLEET_CONFIG"

SPLIT_CONFIG="$WORK_DIR/split-fleet.yaml"
{
    cat "$FLEET_CONFIG"
    cat <<YAML

    # Appended by scripts/migrate-verdict-demo.sh: no such role, no such
    # database, and deliberately no password.
    ${GHOST_TENANT}:
      database:
        type: postgresql
        host: ${GHOST_HOST}
        port: ${GHOST_PORT}
        database: ${GHOST_DATABASE}
        username: ${GHOST_TENANT}
        timezone: UTC
YAML
} >"$SPLIT_CONFIG"

# Appending at tenant indentation only works while multitenant.tenants is the
# last block of the fleet config. Ask the CLI's own lister rather than trust it.
REAL_COUNT="$(listed_count "$FLEET_CONFIG")"
SPLIT_COUNT="$(listed_count "$SPLIT_CONFIG")"
[[ "$REAL_COUNT" -gt 0 ]] || fail "$MULTITENANT_CONFIG lists no tenants"
# A listing that FAILS also ends in nothing_attempted (exit 2), so case 2 proves
# an empty fleet only if this listing succeeds and returns zero tenants.
EMPTY_COUNT="$(listed_count "$EMPTY_CONFIG")" || fail "listing the empty fleet failed"
[[ "$EMPTY_COUNT" == 0 ]] || fail "the empty fleet lists '$EMPTY_COUNT' tenants, not 0"
[[ "$SPLIT_COUNT" -eq $((REAL_COUNT + 1)) ]] \
    || fail "appending '$GHOST_TENANT' did not add a tenant ($REAL_COUNT -> $SPLIT_COUNT); is multitenant.tenants still the last block of $MULTITENANT_CONFIG?"

# --- the three runs --------------------------------------------------------

MISMATCHES=0

# run_case TITLE CONFIG WANT_EXIT WANT_VERDICT WANT_FLEET WANT_FAILED
#
# WANT_FAILED pins the failed count too: fleet_split alone would also accept a
# case 3 where the real tenants failed beside the unreachable one. WANT_FLEET
# pins the tenant counts: every case runs with --continue-on-error, so each run
# must list WANT_FLEET tenants, attempt all of them and leave none unattempted.
run_case() {
    local title="$1" config="$2" want_exit="$3" want_verdict="$4" want_fleet="$5" want_failed="$6"
    local out="$WORK_DIR/stdout" err="$WORK_DIR/stderr" rc=0 summary verdict failed counts

    section "$title"
    echo "\$ $GO_BRICKS_MIGRATE $VERDICT_ACTION --source-config <$(basename "$config")> ... --continue-on-error --json"

    "$GO_BRICKS_MIGRATE" "$VERDICT_ACTION" \
        --source-config "$config" \
        --credentials-from config-file \
        --flyway-config "$MULTITENANT_FLYWAY_CONF" \
        --migrations-dir "$MULTITENANT_MIGRATIONS_DIR" \
        --flyway-path "$MULTITENANT_FLYWAY_PATH" \
        --continue-on-error \
        --json >"$out" 2>"$err" || rc=$?

    # One tenant_complete record per attempted tenant. The CLI's log lines share
    # stdout, so non-JSON and non-event lines are skipped.
    jq -Rr 'fromjson? | select(.event == "tenant_complete")
            | "  \(.tenant_id): \(.status)" + (if .error then " (\(.error))" else "" end)' "$out"

    # Why a tenant failed lives in Flyway's own output, which the framework logs
    # (password-redacted) on the error line. Surface only its FATAL/ERROR lines.
    jq -Rr 'fromjson? | select(.level == "error" and (.output // "") != "") | .output' "$out" \
        | grep -E 'FATAL|ERROR' | awk '!seen[$0]++' | head -n 3 \
        | sed 's/^[[:space:]]*/      flyway: /' || true

    summary="$(jq -Rc 'fromjson? | select(.event == "summary")' "$out" | tail -n 1)"
    [[ -n "$summary" ]] || { cat "$err" >&2; fail "no summary record — see the CLI error above"; }
    verdict="$(jq -r '.verdict // empty' <<<"$summary")"
    [[ -n "$verdict" ]] \
        || fail "summary has no verdict: this go-bricks-migrate predates go-bricks v0.67.0 (#1770). Re-run make migrate-multitenant-install"
    failed="$(jq -r '.failed' <<<"$summary")"
    counts="$(jq -r '"\(.listed)/\(.attempted)/\(.not_attempted)"' <<<"$summary")"

    echo "  summary:   $summary"
    if [[ -s "$err" ]]; then
        echo "  stderr:    $(tail -n 1 "$err")"
    fi
    # String compares: a missing field reads "null", which must mismatch, not error.
    if [[ "$rc" == "$want_exit" && "$verdict" == "$want_verdict" && "$failed" == "$want_failed" \
        && "$counts" == "$want_fleet/$want_fleet/0" ]]; then
        echo "  exit code: $rc  ✅ expected $want_exit ($want_verdict, $want_failed failed, listed/attempted/not_attempted $counts)"
    else
        echo "  exit code: $rc ($verdict, $failed failed, listed/attempted/not_attempted $counts)  ❌ expected $want_exit ($want_verdict, $want_failed failed, $want_fleet/$want_fleet/0)"
        MISMATCHES=$((MISMATCHES + 1))
    fi
}

run_case "1/3 the real fleet: $REAL_COUNT tenants from $MULTITENANT_CONFIG" \
    "$FLEET_CONFIG" 0 clean "$REAL_COUNT" 0
run_case "2/3 an empty fleet: the listing returns no tenant" \
    "$EMPTY_CONFIG" 2 nothing_attempted 0 0
run_case "3/3 a split fleet: the real $REAL_COUNT plus '$GHOST_TENANT' (no such role or database)" \
    "$SPLIT_CONFIG" 1 fleet_split "$SPLIT_COUNT" 1

section "Result"
if [[ "$MISMATCHES" -eq 0 ]]; then
    echo "✅ Exit codes 0 / 2 / 1 and verdicts clean / nothing_attempted / fleet_split, as ADR-115 defines them"
    exit 0
fi
echo "❌ $MISMATCHES case(s) did not match. Cases 1 and 3 need every real tenant to pass:"
echo "   validate needs an applied fleet (make migrate-multitenant-up), or re-run with"
echo "   VERDICT_ACTION=info, which needs only make migrate-multitenant-init."
exit 1
