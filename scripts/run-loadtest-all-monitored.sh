#!/usr/bin/env bash
# scripts/run-loadtest-all-monitored.sh
#
# Runs the five products k6 scenarios of `make loadtest-all` in the same order,
# with scripts/monitor-loadtest.sh sampling the app throughout, then judges the
# samples with scripts/analyze-loadtest-results.sh.
#
#   read_only  loadtests/products-read-only.ts   (~12 min)
#   crud_mix   loadtests/products-crud.ts        (~15 min)
#   spike      loadtests/spike-test.ts           (~6 min)
#   ramp_up    loadtests/ramp-up-test.ts         (~15 min)
#   sustained  loadtests/sustained-load.ts       (~17 min)
#
# The monitor labels each sample with the scenario running at the time
# (phase "cooldown" between scenarios), which is what lets the analyzer check
# leaks inside the sustained run and recovery inside the spike run.
#
# Usage:
#   make loadtest-all-monitored
#   scripts/run-loadtest-all-monitored.sh
#
# Overrides (env):
#   TESTS             scenarios to run, space separated, in the order given
#                     (default: "read_only crud_mix spike ramp_up sustained")
#   K6_FLAGS          extra `k6 run` flags for every scenario. A short check of
#                     the whole pipeline: K6_FLAGS="--vus 2 --duration 20s".
#                     The PERF_* knobs of loadtests/config.ts pass through too.
#   COOLDOWN          seconds of idle between scenarios, so each one starts
#                     from a settled app and a cool machine (default 60)
#   MONITOR_INTERVAL  seconds between samples (default 10)
#   RESULTS_DIR       where the run directory is created (default loadtest-results)
#   APP_URL           the app under test (default: K6_BASE_URL when set, else
#                     http://localhost:8080). The preflight, the monitor and k6
#                     all use it; a K6_BASE_URL that names another target is
#                     refused, since the samples would describe the wrong app
#   API_BASE_PATH     the app's server.path.base, for the preflight health
#                     check (default /api/v1)
#   APP_PID, PG_CONTAINER, PG_USER, PG_DB   passed to the monitor
#
# The goroutine and heap columns need the app's debug endpoints; see the header
# of scripts/monitor-loadtest.sh. Without them the run still completes, and
# those checks report SKIP.
#
# Output, in loadtest-results/run-<timestamp>-<random>/:
#   metrics.csv                monitor samples
#   <scenario>.log             full k6 output (summary included)
#   <scenario>-summary.json    k6 summary data, for scripts that honour
#                              PERF_SUMMARY_FILE
#   analysis.txt               the analyzer's report
#
# Exit code: 0 when every scenario's k6 thresholds passed, the analysis passed
# and the monitor sampled the whole run; 1 otherwise. Every scenario runs even
# after one fails.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR/.."

TESTS="${TESTS:-read_only crud_mix spike ramp_up sustained}"
K6_FLAGS="${K6_FLAGS:-}"
COOLDOWN="${COOLDOWN:-60}"
MONITOR_INTERVAL="${MONITOR_INTERVAL:-10}"
RESULTS_DIR="${RESULTS_DIR:-loadtest-results}"
APP_URL="${APP_URL:-${K6_BASE_URL:-http://localhost:8080}}"
API_BASE_PATH="${API_BASE_PATH:-/api/v1}"
if [[ -n "${K6_BASE_URL:-}" && "${K6_BASE_URL%/}" != "${APP_URL%/}" ]]; then
    echo "❌ K6_BASE_URL ($K6_BASE_URL) and APP_URL ($APP_URL) name different targets; k6 would load one app while the monitor samples another. Set only APP_URL." >&2
    exit 1
fi
export APP_URL
export K6_BASE_URL="$APP_URL"

script_for() {
    case "$1" in
        read_only) echo loadtests/products-read-only.ts ;;
        crud_mix) echo loadtests/products-crud.ts ;;
        spike) echo loadtests/spike-test.ts ;;
        ramp_up) echo loadtests/ramp-up-test.ts ;;
        sustained) echo loadtests/sustained-load.ts ;;
        *) return 1 ;;
    esac
}

# --- preflight ----------------------------------------------------------------

for tool in k6 curl jq; do
    command -v "$tool" >/dev/null 2>&1 || { echo "❌ $tool is required (k6: make loadtest-install)" >&2; exit 1; }
done
[[ "$COOLDOWN" =~ ^[0-9]+$ ]] || { echo "❌ COOLDOWN must be whole seconds, got '$COOLDOWN'" >&2; exit 1; }
# The same check scripts/monitor-loadtest.sh applies. Refused there, it would
# only surface after every scenario ran with no samples taken.
[[ "$MONITOR_INTERVAL" =~ ^[1-9][0-9]*$ ]] || { echo "❌ MONITOR_INTERVAL must be a positive whole number of seconds, got '$MONITOR_INTERVAL'" >&2; exit 1; }
[[ -n "${TESTS// /}" ]] || { echo "❌ TESTS is empty" >&2; exit 1; }
for t in $TESTS; do
    script_for "$t" >/dev/null || { echo "❌ unknown scenario '$t' in TESTS (read_only crud_mix spike ramp_up sustained)" >&2; exit 1; }
done
if ! curl -fsS --max-time 5 "$APP_URL$API_BASE_PATH/health" >/dev/null 2>&1; then
    echo "❌ $APP_URL$API_BASE_PATH/health does not answer. Start the app first (make run)." >&2
    exit 1
fi

# K6_FLAGS is word-split on purpose: it carries several flags.
read -r -a K6_ARGS <<<"$K6_FLAGS"

# mktemp creates the directory atomically under a unique name, so two runs
# started in the same second never share a phase file, logs or metrics.csv.
mkdir -p "$RESULTS_DIR"
RUN_DIR="$(mktemp -d "$RESULTS_DIR/run-$(date +%Y%m%d-%H%M%S)-XXXXXX")"
METRICS="$RUN_DIR/metrics.csv"
PHASE_FILE="$RUN_DIR/phase"
echo "idle" >"$PHASE_FILE"

echo "🔍 Monitored load test run"
echo "   app        : $APP_URL"
echo "   scenarios  : $TESTS"
echo "   k6 flags   : ${K6_FLAGS:-(none: full native profiles)}"
echo "   cooldown   : ${COOLDOWN}s between scenarios"
echo "   results    : $RUN_DIR"
echo ""

PHASE_FILE="$PHASE_FILE" scripts/monitor-loadtest.sh "$METRICS" "$MONITOR_INTERVAL" >"$RUN_DIR/monitor.log" 2>&1 &
MONITOR_PID=$!

# The monitor samples until it is signalled, so finding it gone before
# stop_monitor signals it means it died mid-run and the samples are partial.
MONITOR_EARLY_EXIT=0
MONITOR_STOPPED=0
stop_monitor() {
    [[ $MONITOR_STOPPED -eq 0 ]] || return 0
    MONITOR_STOPPED=1
    if kill -0 "$MONITOR_PID" 2>/dev/null; then
        kill -TERM "$MONITOR_PID" 2>/dev/null || true
        wait "$MONITOR_PID" 2>/dev/null || true
    else
        MONITOR_EARLY_EXIT=1
        wait "$MONITOR_PID" 2>/dev/null || true
    fi
}
trap stop_monitor EXIT
trap 'echo ""; echo "Interrupted; partial results in $RUN_DIR"; exit 130' INT TERM

# Let the monitor take a pre-load sample (and print its source warnings).
sleep 2
sed 's/^/   monitor: /' "$RUN_DIR/monitor.log"
echo ""

declare -a RESULTS=()
K6_FAILED=0
first=1
for t in $TESTS; do
    if [[ $first -eq 0 && "$COOLDOWN" -gt 0 ]]; then
        echo "cooldown" >"$PHASE_FILE"
        echo "❄️  Cooling down ${COOLDOWN}s..."
        sleep "$COOLDOWN"
    fi
    first=0

    script="$(script_for "$t")"
    echo "$t" >"$PHASE_FILE"
    echo "▶️  $t: k6 run ${K6_FLAGS:+$K6_FLAGS }$script"
    rc=0
    PERF_SUMMARY_FILE="$RUN_DIR/$t-summary.json" \
        k6 run ${K6_ARGS[@]+"${K6_ARGS[@]}"} "$script" >"$RUN_DIR/$t.log" 2>&1 || rc=$?
    if [[ $rc -eq 0 ]]; then
        RESULTS+=("  PASS  $t")
    else
        # 99 is k6's "thresholds crossed"; anything else is a run failure.
        RESULTS+=("  FAIL  $t (k6 exit $rc, see $RUN_DIR/$t.log)")
        K6_FAILED=1
    fi
    echo "   k6 exit $rc"
done

echo "idle" >"$PHASE_FILE"
sleep "$MONITOR_INTERVAL"
stop_monitor

echo ""
echo "k6 thresholds:"
printf '%s\n' ${RESULTS[@]+"${RESULTS[@]}"}
echo ""

ANALYSIS_RC=0
scripts/analyze-loadtest-results.sh "$METRICS" | tee "$RUN_DIR/analysis.txt" || ANALYSIS_RC=${PIPESTATUS[0]}

echo ""
if [[ $MONITOR_EARLY_EXIT -ne 0 ]]; then
    echo "❌ The monitor exited before the run ended, so the samples are partial (see $RUN_DIR/monitor.log)"
fi
echo "📁 Results: $RUN_DIR"
if [[ $K6_FAILED -ne 0 || $ANALYSIS_RC -ne 0 || $MONITOR_EARLY_EXIT -ne 0 ]]; then
    exit 1
fi
