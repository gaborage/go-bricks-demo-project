#!/usr/bin/env bash
# scripts/flyway-docker.sh
#
# Thin wrapper passed to `go-bricks-migrate --flyway-path` so the demo does
# not require a local Flyway install. Runs flyway/flyway:11-alpine in
# Docker, attached to the same compose network as the demo's postgres
# container, and forwards stdin/stdout/stderr verbatim.
#
# Inputs (env vars set by go-bricks-migrate per tenant):
#   DB_HOST, DB_PORT, DB_USER, DB_PASSWORD, DB_NAME
#
# Behaviour notes:
#   * DB_HOST=localhost is rewritten to the postgres container hostname,
#     since config.multitenant.yaml uses `host: localhost` (developer-friendly
#     and works equally for a local Flyway install) but inside the wrapper
#     container `localhost` would point at the wrapper itself.
#   * The `-url=` argv element gets the same host rewrite (see ADR-085 below).
#   * The host network is auto-detected from the running postgres container
#     so this works regardless of the compose project name. Override with
#     FLYWAY_NETWORK if you need to point elsewhere.
#   * The repo root is mounted read-only at /work so relative paths in the
#     go-bricks-migrate args (--flyway-config flyway/flyway-multitenant.conf,
#     --migrations-dir migrations-multitenant) resolve.

set -euo pipefail

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IMAGE="${FLYWAY_IMAGE:-flyway/flyway:11-alpine}"
POSTGRES_CONTAINER="${FLYWAY_POSTGRES_CONTAINER:-go-bricks-postgres}"

NETWORK="${FLYWAY_NETWORK:-}"
if [[ -z "$NETWORK" ]]; then
    # `|| true` lets the empty-NETWORK check below print the friendly error instead of pipefail short-circuiting.
    NETWORK="$(docker inspect -f '{{range $net,$_ := .NetworkSettings.Networks}}{{$net}} {{end}}' "$POSTGRES_CONTAINER" 2>/dev/null | awk '{print $1}' || true)"
fi
if [[ -z "$NETWORK" ]]; then
    echo "flyway-docker: could not auto-detect network for container '$POSTGRES_CONTAINER'." >&2
    echo "flyway-docker: run 'make docker-up' first, or set FLYWAY_NETWORK explicitly." >&2
    exit 1
fi

CONTAINER_DB_HOST="${FLYWAY_POSTGRES_HOSTNAME:-postgres}"

EFFECTIVE_DB_HOST="${DB_HOST:-}"
case "$EFFECTIVE_DB_HOST" in
    localhost|127.0.0.1|::1)
        EFFECTIVE_DB_HOST="$CONTAINER_DB_HOST"
        ;;
esac

# Since go-bricks v0.61.0 (ADR-085) the go-bricks-migrate CLI builds the PostgreSQL
# JDBC URL itself from database.* and passes it as `-url=` on the argv, where it
# outranks flyway.url in flyway/flyway-multitenant.conf. That URL is built from
# config.multitenant.yaml's `host: localhost`, which the DB_HOST rewrite above no
# longer reaches — so rewrite the host inside the URL too. Host only: the port,
# database and query string (ApplicationName, sslmode, sslrootcert) pass through
# untouched, as does every other argument.
for ((_i = $#; _i > 0; _i--)); do
    arg="$1"
    shift
    case "$arg" in
        -url=jdbc:postgresql://*)
            arg="$(printf '%s' "$arg" | sed -E "s#^(-url=jdbc:postgresql://)(localhost|127\.0\.0\.1|\[::1\])([:/])#\1${CONTAINER_DB_HOST}\3#")"
            ;;
    esac
    set -- "$@" "$arg"
done

exec docker run --rm \
    --network="$NETWORK" \
    -v "$PROJECT_ROOT":/work:ro \
    -w /work \
    -e DB_HOST="$EFFECTIVE_DB_HOST" \
    -e DB_PORT="${DB_PORT:-5432}" \
    -e DB_USER="${DB_USER:-}" \
    -e DB_PASSWORD="${DB_PASSWORD:-}" \
    -e DB_NAME="${DB_NAME:-}" \
    "$IMAGE" "$@"
