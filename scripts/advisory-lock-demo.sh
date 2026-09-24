#!/usr/bin/env bash
# scripts/advisory-lock-demo.sh
#
# Two-replica proof for the products report job's PostgreSQL advisory lock
# (go-bricks v0.65.0 database Session door, ADR-112).
#
# The scheduler only keeps the SAME job from overlapping inside one process;
# every replica ticks on its own. The report job (internal/modules/products/job)
# therefore opens a pinned database Session, tries pg_try_advisory_lock on
# job.ReportLockKey, runs the report only when it gets the lock, and logs a skip
# when another replica holds it. This script makes that visible with two real
# replicas of this app:
#
#   0. Starts TWO extra replicas from the same binary and config, each on its
#      own HTTP port, with custom.products.report.hold set so the winner keeps
#      the lock long enough for the loser to find it held.
#   1. MANUAL    — ROUNDS times, fires POST /_sys/job/test-job at both replicas
#                  at once. Each round must log exactly one
#                  "Report job lock acquired" and one
#                  "Report job skipped: another replica holds the lock", and
#                  pg_locks shows exactly one holder of the key meanwhile.
#   2. SCHEDULED — waits for the replicas' own first FixedRate tick (30s after
#                  each one started). Both replicas tick; exactly one runs.
#
# Every time the winner logs "Report job lock released", pg_locks must show no
# holder while both replicas are still up: a key left held on the pinned
# backend would ride back into the pool and make every later tick skip. The
# script waits for that release before it stops the replicas, so the scheduled
# winner finishes its run instead of being cancelled mid-report.
#
# The app on APP_URL (make run), if it is up, is a third replica: it takes the
# same lock on its own ticks, and its skips land in its own log, not here.
#
# Prerequisites:
#   make docker-up && make migrate   # postgres + broker the replicas boot against
#   make generate-keys               # the keystore loads certs/ at boot
#   make build                       # bin/go-bricks-demo-project (the make target does it)
#
# Overrides (env): APP_BIN, APP_URL, API_BASE_PATH, REPLICA_PORTS, HOLD, ROUNDS,
#                  STARTUP_TIMEOUT, LOG_DIR, PG_HOST, PG_PORT, PG_USER, PG_DB,
#                  PGPASSWORD
# Every other variable in the environment — DATABASE_PORT, MESSAGING_BROKER_URL
# and the rest of an alternate-ports override set — passes through to the
# replicas unchanged, so they boot against the same infrastructure as the app.
#
# Exit code 0 = every round and the scheduled tick elected exactly one runner.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR/.."

APP_BIN="${APP_BIN:-./bin/go-bricks-demo-project}"
APP_URL="${APP_URL:-http://localhost:8080}"
API_BASE_PATH="${API_BASE_PATH:-/api/v1}"
REPLICA_PORTS="${REPLICA_PORTS:-8081 8082}"
HOLD="${HOLD:-8s}"
ROUNDS="${ROUNDS:-2}"
STARTUP_TIMEOUT="${STARTUP_TIMEOUT:-60}"

# Postgres — only to read pg_locks while a replica holds the key.
PG_HOST="${PG_HOST:-127.0.0.1}"
PG_PORT="${PG_PORT:-5432}"
PG_USER="${PG_USER:-postgres}"
PG_DB="${PG_DB:-postgres}"
export PGPASSWORD="${PGPASSWORD:-postgres}"

# Must match internal/modules/products: the job ID RegisterJobs schedules, its
# FixedRate interval, and the three log messages ReportJob writes.
JOB_ID="test-job"
TICK_INTERVAL=30
MSG_ACQUIRED="Report job lock acquired"
MSG_SKIPPED="Report job skipped: another replica holds the lock"
MSG_RELEASED="Report job lock released"

# job.ReportLockKey = 0x52505254 ("RPRT"). A bigint advisory key shows in
# pg_locks as classid = high 32 bits, objid = low 32 bits, objsubid = 1.
LOCK_OBJID=1380995668

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

read -r -a PORTS <<<"$REPLICA_PORTS"
[[ ${#PORTS[@]} -eq 2 ]] || fail "REPLICA_PORTS must name exactly two ports (got '$REPLICA_PORTS')"
[[ "$ROUNDS" =~ ^[0-9]+$ ]] || fail "ROUNDS must be a whole number (got '$ROUNDS')"
[[ "$HOLD" =~ ^[0-9]+s$ ]] || fail "HOLD must be whole seconds such as 8s (got '$HOLD')"
HOLD_SECONDS="${HOLD%s}"
(( HOLD_SECONDS >= 2 )) || fail "HOLD must be at least 2s so the losing replica finds the lock held"

# The replicas' JSON logs. stdout is a file, not a TTY, so log.output.format
# "auto" resolves to JSON and every line is one object.
LOG_DIR="${LOG_DIR:-$(mktemp -d "${TMPDIR:-/tmp}/advisory-lock-demo.XXXXXX")}"
mkdir -p "$LOG_DIR"
LOGS=("$LOG_DIR/replica-1.log" "$LOG_DIR/replica-2.log")
PIDS=()

stop_replicas() {
    local pid alive
    for pid in "${PIDS[@]:-}"; do
        if [[ -n "$pid" ]]; then
            kill -TERM "$pid" 2>/dev/null || true
        fi
    done
    # Graceful shutdown lets a replica mid-report release the lock (the unlock
    # runs on a context the shutdown does not cancel). SIGKILL only if it hangs.
    for _ in $(seq 1 20); do
        alive=0
        for pid in "${PIDS[@]:-}"; do
            if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
                alive=1
            fi
        done
        if (( alive == 0 )); then
            return 0
        fi
        sleep 0.5
    done
    for pid in "${PIDS[@]:-}"; do
        if [[ -n "$pid" ]]; then
            kill -KILL "$pid" 2>/dev/null || true
        fi
    done
}
trap stop_replicas EXIT

# count LOG TRIGGER MESSAGE — lines ReportJob wrote for one trigger type.
# Non-JSON lines (a panic trace, a banner) are skipped rather than fatal.
count() {
    jq -Rn --arg t "$2" --arg m "$3" \
        '[inputs | fromjson? | select(type == "object" and .trigger == $t and .message == $m)] | length' \
        "$1"
}

# total TRIGGER MESSAGE — the same count summed over both replicas.
total() {
    echo $(( $(count "${LOGS[0]}" "$1" "$2") + $(count "${LOGS[1]}" "$1" "$2") ))
}

# wait_for TIMEOUT_SECONDS TRIGGER MESSAGE... TARGET — poll until the summed
# count of the listed messages reaches TARGET. Returns non-zero on timeout.
wait_for() {
    local timeout="$1" trigger="$2" target="${*: -1}"
    local msgs=("${@:3:$#-3}")
    local deadline=$(( $(date +%s) + timeout ))
    local sum msg
    while :; do
        sum=0
        for msg in "${msgs[@]}"; do
            sum=$(( sum + $(total "$trigger" "$msg") ))
        done
        if (( sum >= target )); then
            return 0
        fi
        if (( $(date +%s) >= deadline )); then
            return 1
        fi
        sleep 0.5
    done
}

# Best-effort pg_locks reader: host psql at PG_HOST:PG_PORT, else the compose
# container. The docker probe passes PGPASSWORD as a bare `-e NAME`, which copies
# the value from this shell's environment instead of putting it in docker's argv.
PSQL_MODE=""
if command -v psql >/dev/null 2>&1 \
    && psql -h "$PG_HOST" -p "$PG_PORT" -U "$PG_USER" -d "$PG_DB" -tAc 'SELECT 1' >/dev/null 2>&1; then
    PSQL_MODE="host"
elif command -v docker >/dev/null 2>&1 \
    && docker exec -e PGPASSWORD go-bricks-postgres \
        psql -U "$PG_USER" -d "$PG_DB" -tAc 'SELECT 1' >/dev/null 2>&1; then
    PSQL_MODE="docker"
fi

lock_holders() {
    local sql="SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND classid = 0 AND objid = $LOCK_OBJID AND objsubid = 1 AND granted;"
    local out
    case "$PSQL_MODE" in
        host)
            out="$(psql -h "$PG_HOST" -p "$PG_PORT" -U "$PG_USER" -d "$PG_DB" -tAc "$sql" 2>/dev/null)" || return 1
            ;;
        docker)
            out="$(docker exec -e PGPASSWORD go-bricks-postgres \
                psql -U "$PG_USER" -d "$PG_DB" -tAc "$sql" 2>/dev/null)" || return 1
            ;;
        *) return 1 ;;
    esac
    printf '%s' "${out//[[:space:]]/}"
}

# check_released LABEL — run once the winner has logged the release. The unlock
# statement has already returned by then, so pg_locks must show no holder of
# the key. With the app on APP_URL up, a holder may be that third replica's own
# tick, so it is reported rather than failed.
check_released() {
    local after
    after="$(lock_holders)" || after="?"
    if [[ "$after" == "?" || "$after" == "0" ]]; then
        echo "$1: winner released the key (pg_locks holders=$after)"
    elif [[ -n "$APP_UP" ]]; then
        echo "$1: winner released the key; pg_locks holders=$after — the app on $APP_URL ticking?"
    else
        echo "❌ $1: the winner logged the release but pg_locks still shows $after holder(s) of the key" >&2
        FAILED=1
    fi
}

# trigger PORT — POST the manual trigger; prints the HTTP status ("000" when
# nothing answered — curl's -w still prints it on a connection failure).
# 127.0.0.1 on purpose: /_sys is behind the scheduler's CIDR middleware, which
# admits only loopback callers when scheduler.security.cidrallowlist is empty.
trigger() {
    curl -sS -o /dev/null -w '%{http_code}' -X POST \
        "http://127.0.0.1:$1$API_BASE_PATH/_sys/job/$JOB_ID" 2>/dev/null || true
}

# --- 0. start two replicas ------------------------------------------------

section "0/2  Start two replicas"

[[ -x "$APP_BIN" ]] || fail "app binary '$APP_BIN' not found — run 'make build'"

for port in "${PORTS[@]}"; do
    # curl exits 7 when nothing listens; anything else means the port is taken.
    rc=0
    curl -s -o /dev/null --max-time 2 "http://127.0.0.1:$port/" || rc=$?
    (( rc == 7 )) || fail "port $port is already in use — set REPLICA_PORTS to two free ports"
done

APP_UP=""
if curl -fsS -o /dev/null --max-time 2 "$APP_URL$API_BASE_PATH/health" 2>/dev/null; then
    APP_UP=1
    echo "note      : the app on $APP_URL is up — a third replica on the same lock;"
    echo "            its own ticks skip while a replica here holds the key (see its log)"
fi

LAUNCHED_AT="$(date +%s)"
for i in 0 1; do
    # Same binary, same config.development.yaml; only the port and the hold
    # differ from `make run`. env -u DEBUG mirrors its `unset DEBUG`.
    env -u DEBUG \
        APP_ENV="${APP_ENV:-development}" \
        CORS_DEV_WILDCARD=true \
        SERVER_PORT="${PORTS[$i]}" \
        CUSTOM_PRODUCTS_REPORT_HOLD="$HOLD" \
        "$APP_BIN" >"${LOGS[$i]}" 2>&1 &
    PIDS+=("$!")
done

for i in 0 1; do
    deadline=$(( LAUNCHED_AT + STARTUP_TIMEOUT ))
    until curl -fsS -o /dev/null --max-time 2 "http://127.0.0.1:${PORTS[$i]}$API_BASE_PATH/health" 2>/dev/null; do
        if ! kill -0 "${PIDS[$i]}" 2>/dev/null || (( $(date +%s) >= deadline )); then
            echo "--- last lines of ${LOGS[$i]} ---" >&2
            tail -n 20 "${LOGS[$i]}" >&2 || true
            fail "replica-$((i + 1)) on :${PORTS[$i]} did not become healthy — is the infrastructure up (make docker-up && make migrate)?"
        fi
        sleep 0.5
    done
    echo "replica-$((i + 1)): :${PORTS[$i]} (pid ${PIDS[$i]}), hold $HOLD, log ${LOGS[$i]}"
done

if [[ -n "$PSQL_MODE" ]]; then
    echo "pg_locks  : via psql ($PSQL_MODE)"
else
    echo "pg_locks  : psql not reachable — the holder count will be skipped"
fi

# --- 1. manual triggers: both replicas at once ----------------------------

section "1/2  Manual trigger on both replicas at once (x$ROUNDS)"

FAILED=0
ROUNDS_RUN=0
for round in $(seq 1 "$ROUNDS"); do
    # Every round must be over before the replicas' own first tick, or that tick
    # would collide with the manual trigger in the same process.
    if (( $(date +%s) - LAUNCHED_AT + HOLD_SECONDS + 4 >= TICK_INTERVAL )); then
        echo "round $round: skipped — it would overlap the replicas' first scheduled tick"
        break
    fi

    base_acq="$(total manual "$MSG_ACQUIRED")"
    base_skip="$(total manual "$MSG_SKIPPED")"
    base_rel="$(total manual "$MSG_RELEASED")"
    base_acq_1="$(count "${LOGS[0]}" manual "$MSG_ACQUIRED")"

    code_file_1="$LOG_DIR/trigger-$round-1"
    code_file_2="$LOG_DIR/trigger-$round-2"
    trigger "${PORTS[0]}" >"$code_file_1" &
    t1=$!
    trigger "${PORTS[1]}" >"$code_file_2" &
    t2=$!
    wait "$t1" "$t2" || true
    code_1="$(cat "$code_file_1")"
    code_2="$(cat "$code_file_2")"
    [[ "$code_1" == "202" && "$code_2" == "202" ]] \
        || fail "round $round: trigger answered $code_1 / $code_2, want 202 / 202"

    wait_for 15 manual "$MSG_ACQUIRED" "$MSG_SKIPPED" $(( base_acq + base_skip + 2 )) \
        || fail "round $round: both replicas did not report a lock decision within 15s — see $LOG_DIR"

    acquired=$(( $(total manual "$MSG_ACQUIRED") - base_acq ))
    skipped=$(( $(total manual "$MSG_SKIPPED") - base_skip ))
    holders="$(lock_holders)" || holders="?"

    if (( acquired == 1 )); then
        if (( $(count "${LOGS[0]}" manual "$MSG_ACQUIRED") > base_acq_1 )); then
            winner="replica-1"
        else
            winner="replica-2"
        fi
        echo "round $round: $winner ran the report, the other skipped (acquired=$acquired skipped=$skipped, pg_locks holders=$holders)"
        [[ "$holders" == "?" || "$holders" == "1" ]] \
            || { echo "❌ round $round: pg_locks shows $holders holders of the key, want 1" >&2; FAILED=1; }
        wait_for $(( HOLD_SECONDS + 10 )) manual "$MSG_RELEASED" $(( base_rel + 1 )) \
            || fail "round $round: the winner never logged the release — see $LOG_DIR"
        check_released "round $round"
    elif (( acquired == 0 )); then
        # Both skipped: something outside this pair held the key — typically
        # the app on APP_URL, whose own tick landed in this instant.
        echo "round $round: both replicas skipped — a third holder had the key (the app on $APP_URL ticking?)"
    else
        echo "❌ round $round: $acquired replicas ran the report at once — the lock did not exclude" >&2
        FAILED=1
    fi
    ROUNDS_RUN=$((ROUNDS_RUN + 1))
done

# --- 2. the replicas' own scheduled tick ----------------------------------

section "2/2  First scheduled tick (every ${TICK_INTERVAL}s, per replica)"

echo "waiting for both replicas to tick (about ${TICK_INTERVAL}s after they started)..."
wait_for $(( TICK_INTERVAL + STARTUP_TIMEOUT )) scheduled "$MSG_ACQUIRED" "$MSG_SKIPPED" 2 \
    || fail "the replicas did not both tick within $(( TICK_INTERVAL + STARTUP_TIMEOUT ))s — see $LOG_DIR"

sched_acquired="$(total scheduled "$MSG_ACQUIRED")"
sched_skipped="$(total scheduled "$MSG_SKIPPED")"
if (( sched_acquired == 1 && sched_skipped >= 1 )); then
    holders="$(lock_holders)" || holders="?"
    echo "scheduled tick: one replica ran the report, the other skipped (acquired=$sched_acquired skipped=$sched_skipped, pg_locks holders=$holders)"
    [[ "$holders" == "?" || "$holders" == "1" ]] \
        || { echo "❌ scheduled tick: pg_locks shows $holders holders of the key, want 1" >&2; FAILED=1; }
    # Let the winner finish before the EXIT trap stops the replicas. Stopping
    # them mid-report cancels the run: the unlock still happens, but the run
    # logs as failed and the release is never checked here.
    wait_for $(( HOLD_SECONDS + 10 )) scheduled "$MSG_RELEASED" 1 \
        || fail "scheduled tick: the winner never logged the release — see $LOG_DIR"
    check_released "scheduled tick"
elif (( sched_acquired >= 2 )); then
    # Not a lock failure by itself: two runs that did not overlap both get the
    # key. It means the two ticks landed more than HOLD apart.
    echo "❌ scheduled tick: both replicas ran — their first ticks were more than $HOLD apart (startup skew); rerun with a larger HOLD" >&2
    FAILED=1
else
    echo "scheduled tick: both replicas skipped — a third holder had the key (the app on $APP_URL ticking?)"
fi

# --- verdict --------------------------------------------------------------

section "Verdict"

manual_acquired="$(total manual "$MSG_ACQUIRED")"
if (( FAILED != 0 )); then
    fail "at least one tick did not elect exactly one runner — replica logs kept in $LOG_DIR"
fi
if (( manual_acquired + sched_acquired == 0 )); then
    fail "no replica ever acquired the lock — is something else holding key $LOCK_OBJID? Logs in $LOG_DIR"
fi
echo "✅ $ROUNDS_RUN manual round(s) and the first scheduled tick each ran the report on at most one replica"
echo "   replica logs: $LOG_DIR"
