#!/usr/bin/env bash
# scripts/analyze-loadtest-results.sh
#
# Judges a metrics CSV written by scripts/monitor-loadtest.sh against
# loadtests/thresholds.yaml and prints a per-phase table plus a pass/fail
# verdict.
#
# Usage:
#   scripts/analyze-loadtest-results.sh <metrics.csv> [thresholds.yaml]
#   make loadtest-analyze FILE=loadtest-results/metrics-<timestamp>.csv
#
# Checks (the five pass_fail.required_checks, then three advisory peaks):
#   goroutine_peak_under_critical          max goroutines vs goroutines.{warning,critical}
#   memory_peak_under_critical             max heap_alloc_mb vs memory.heap.{warning,critical}
#   no_sustained_leaks                     phase "sustained": mean of the last 25% of
#                                          samples vs the first 25%, per metric, against
#                                          leak_detection.sustained_test.*.max_increase_percent
#   spike_recovery_successful              phase "spike": mean of the last 10% vs the
#                                          first 20% (the baseline stage), against
#                                          leak_detection.spike_recovery.*.max_final_percent_of_baseline
#   connection_pool_not_exhausted_below_100vu
#                                          phases "read_only" and "crud_mix": max db_total
#                                          below database.connections.max_configured
#   rss_peak / db_connections_peak / ...   advisory WARN/FAIL on memory.rss and
#                                          database.connections warning/critical
#
# A check whose samples are missing (a phase that did not run, a column the
# monitor could not read) reports SKIP and never fails the run. Phases come
# from the CSV's phase column, which scripts/run-loadtest-all-monitored.sh sets;
# a hand-started monitor labels every row "manual", so only the peak checks
# apply to it.
#
# Exit code: 0 = pass (warnings are reported, not fatal), 1 = FAIL count reached
# pass_fail.max_critical_issues, 2 = usage, unreadable input, or no required
# check had any samples to judge (an advisory peak alone never makes a verdict).

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

[[ $# -ge 1 && $# -le 2 ]] || { echo "usage: $0 <metrics.csv> [thresholds.yaml]" >&2; exit 2; }
CSV="$1"
THRESHOLDS="${2:-$(cd "$SCRIPT_DIR/.." && pwd)/loadtests/thresholds.yaml}"

[[ -r "$CSV" ]] || { echo "❌ cannot read metrics file: $CSV" >&2; exit 2; }
[[ -r "$THRESHOLDS" ]] || { echo "❌ cannot read thresholds file: $THRESHOLDS" >&2; exit 2; }

EXPECTED_HEADER="timestamp,elapsed_s,phase,goroutines,heap_alloc_mb,rss_mb,db_active,db_idle,db_total"
if [[ "$(head -n 1 "$CSV")" != "$EXPECTED_HEADER" ]]; then
    echo "❌ $CSV is not a scripts/monitor-loadtest.sh CSV (header must be: $EXPECTED_HEADER)" >&2
    exit 2
fi

# CSV column numbers.
C_GOROUTINES=4
C_HEAP=5
C_RSS=6
C_DB_TOTAL=9

# yaml_get <dotted.path> — a scalar from the thresholds file. The file is plain
# nested maps with two-space indentation, which is all this reader handles.
yaml_get() {
    local value
    value="$(awk -v want="$1" '
        /^[[:space:]]*(#|$)/ { next }
        {
            match($0, /^ */)
            depth = RLENGTH / 2
            line = substr($0, RLENGTH + 1)
            if (line ~ /^- /) next
            colon = index(line, ":")
            if (colon == 0) next
            key = substr(line, 1, colon - 1)
            val = substr(line, colon + 1)
            sub(/[[:space:]]+#.*$/, "", val)
            gsub(/^[[:space:]"]+|[[:space:]"]+$/, "", val)
            path[depth] = key
            p = path[0]
            for (i = 1; i <= depth; i++) p = p "." path[i]
            if (p == want && val != "") { print val; exit }
        }' "$THRESHOLDS")"
    if [[ -z "$value" ]]; then
        echo "❌ $THRESHOLDS has no value at $1" >&2
        exit 2
    fi
    echo "$value"
}

G_WARN="$(yaml_get goroutines.warning)"
G_CRIT="$(yaml_get goroutines.critical)"
H_WARN="$(yaml_get memory.heap.warning)"
H_CRIT="$(yaml_get memory.heap.critical)"
R_WARN="$(yaml_get memory.rss.warning)"
R_CRIT="$(yaml_get memory.rss.critical)"
C_MAX="$(yaml_get database.connections.max_configured)"
C_WARN="$(yaml_get database.connections.warning)"
C_CRIT="$(yaml_get database.connections.critical)"
LEAK_G="$(yaml_get leak_detection.sustained_test.goroutines.max_increase_percent)"
LEAK_M="$(yaml_get leak_detection.sustained_test.memory.max_increase_percent)"
LEAK_C="$(yaml_get leak_detection.sustained_test.connections.max_increase_percent)"
REC_G="$(yaml_get leak_detection.spike_recovery.goroutines.max_final_percent_of_baseline)"
REC_M="$(yaml_get leak_detection.spike_recovery.memory.max_final_percent_of_baseline)"
REC_C="$(yaml_get leak_detection.spike_recovery.connections.max_final_percent_of_baseline)"
MAX_CRITICAL="$(yaml_get pass_fail.max_critical_issues)"
MAX_WARNINGS="$(yaml_get pass_fail.max_warnings)"

# stat <column> <max|mean|count> [phases] [from] [to]
# Non-empty values of <column> on rows whose phase is in the comma-separated
# [phases] (all rows when empty), restricted to the slice [from, to) of those
# rows as fractions (0 1 = all). Prints nothing when no value matches.
stat() {
    awk -F, -v col="$1" -v fn="$2" -v phases="${3:-}" -v from="${4:-0}" -v to="${5:-1}" '
        BEGIN { n = split(phases, list, ","); for (i = 1; i <= n; i++) want[list[i]] = 1 }
        NR == 1 { next }
        n > 0 && !($3 in want) { next }
        $col != "" { vals[++count] = $col + 0 }
        END {
            if (count == 0) exit
            lo = int(count * from) + 1
            hi = int(count * to)
            if (hi < lo) hi = lo
            if (hi > count) hi = count
            m = 0; sum = 0; k = 0
            for (i = lo; i <= hi; i++) {
                if (k == 0 || vals[i] > m) m = vals[i]
                sum += vals[i]; k++
            }
            if (fn == "max") printf "%.1f\n", m
            else if (fn == "mean") printf "%.1f\n", sum / k
            else if (fn == "count") print k
        }' "$CSV"
}

# ge / gt <a> <b> — decimal-safe a >= b / a > b.
ge() { awk -v a="$1" -v b="$2" 'BEGIN { exit !(a + 0 >= b + 0) }'; }
gt() { awk -v a="$1" -v b="$2" 'BEGIN { exit !(a + 0 > b + 0) }'; }

FAILS=0
WARNS=0
# Required checks that had samples. The advisory peaks still add to FAILS and
# WARNS, but never to this count, so they alone cannot produce a PASS.
REQUIRED_JUDGED=0
ADVISORY=0
report() { # report <PASS|WARN|FAIL|SKIP> <check> <detail>
    printf '  %-4s  %-42s %s\n' "$1" "$2" "$3"
    case "$1" in
        FAIL) FAILS=$((FAILS + 1)) ;;
        WARN) WARNS=$((WARNS + 1)) ;;
    esac
    if [[ "$1" != "SKIP" && "$ADVISORY" -eq 0 ]]; then
        REQUIRED_JUDGED=$((REQUIRED_JUDGED + 1))
    fi
}

# peak_check <check> <column> <label> <unit> <warning> <critical>
peak_check() {
    local peak
    peak="$(stat "$2" max)"
    if [[ -z "$peak" ]]; then
        report SKIP "$1" "no $3 samples"
    elif ge "$peak" "$6"; then
        report FAIL "$1" "peak $3 $peak$4 >= critical $6$4"
    elif ge "$peak" "$5"; then
        report WARN "$1" "peak $3 $peak$4 >= warning $5$4"
    else
        report PASS "$1" "peak $3 $peak$4 < warning $5$4"
    fi
}

# leak_check <column> <label> <max_increase_percent> — sets LEAK_RESULT.
leak_check() {
    local first last samples
    samples="$(stat "$1" count sustained)"
    if [[ -z "$samples" || "$samples" -lt 4 ]]; then
        LEAK_RESULT="SKIP|$2: ${samples:-0} sustained sample(s), need 4"
        return
    fi
    first="$(stat "$1" mean sustained 0 0.25)"
    last="$(stat "$1" mean sustained 0.75 1)"
    if ! ge "$first" 0.1; then
        LEAK_RESULT="SKIP|$2: first-quarter mean is 0"
        return
    fi
    local pct
    pct="$(awk -v a="$first" -v b="$last" 'BEGIN { printf "%.1f", (b - a) / a * 100 }')"
    if gt "$pct" "$3"; then
        LEAK_RESULT="FAIL|$2 $first -> $last (+$pct% > $3%)"
    else
        LEAK_RESULT="PASS|$2 $first -> $last (${pct}%, limit +$3%)"
    fi
}

# recovery_check <column> <label> <max_final_percent_of_baseline> — sets REC_RESULT.
recovery_check() {
    local baseline final samples
    samples="$(stat "$1" count spike)"
    if [[ -z "$samples" || "$samples" -lt 5 ]]; then
        REC_RESULT="SKIP|$2: ${samples:-0} spike sample(s), need 5"
        return
    fi
    baseline="$(stat "$1" mean spike 0 0.2)"
    final="$(stat "$1" mean spike 0.9 1)"
    if ! ge "$baseline" 0.1; then
        REC_RESULT="SKIP|$2: baseline mean is 0"
        return
    fi
    local pct
    pct="$(awk -v a="$baseline" -v b="$final" 'BEGIN { printf "%.0f", b / a * 100 }')"
    if gt "$pct" "$3"; then
        REC_RESULT="FAIL|$2 baseline $baseline -> final $final ($pct% > $3%)"
    else
        REC_RESULT="PASS|$2 baseline $baseline -> final $final ($pct%, limit $3%)"
    fi
}

# combine <check> <result>... — one report line from several metric results:
# FAIL if any failed, SKIP if all skipped, else PASS.
combine() {
    local check="$1" status="SKIP" details="" r s
    shift
    for r in "$@"; do
        s="${r%%|*}"
        details="${details:+$details; }${r#*|}"
        case "$s" in
            FAIL) status="FAIL" ;;
            PASS) [[ "$status" == "FAIL" ]] || status="PASS" ;;
        esac
    done
    report "$status" "$check" "$details"
}

# --- report -------------------------------------------------------------------

ROWS="$(($(wc -l <"$CSV") - 1))"
echo "═══════════════════════════════════════════════════════════════════════"
echo " Load test resource analysis"
echo "   metrics    : $CSV ($ROWS sample(s))"
echo "   thresholds : $THRESHOLDS"
echo "═══════════════════════════════════════════════════════════════════════"
echo ""
echo "Per phase (peak / mean):"
printf '  %-10s %7s  %-15s %-15s %-9s %-13s\n' phase samples goroutines heap_mb rss_mb db_conns
awk -F, 'NR > 1 && !seen[$3]++ { print $3 }' "$CSV" | while read -r phase; do
    printf '  %-10s %7s  %-15s %-15s %-9s %-13s\n' "$phase" \
        "$(awk -F, -v p="$phase" 'NR > 1 && $3 == p { n++ } END { print n + 0 }' "$CSV")" \
        "$(stat $C_GOROUTINES max "$phase")/$(stat $C_GOROUTINES mean "$phase")" \
        "$(stat $C_HEAP max "$phase")/$(stat $C_HEAP mean "$phase")" \
        "$(stat $C_RSS max "$phase")" \
        "$(stat $C_DB_TOTAL max "$phase")/$(stat $C_DB_TOTAL mean "$phase")"
done
echo "  (a lone '/' means the monitor could not read that metric)"
echo ""
echo "Checks:"

peak_check goroutine_peak_under_critical "$C_GOROUTINES" goroutines "" "$G_WARN" "$G_CRIT"
peak_check memory_peak_under_critical "$C_HEAP" "heap" "MB" "$H_WARN" "$H_CRIT"

leak_check "$C_GOROUTINES" goroutines "$LEAK_G"; L1="$LEAK_RESULT"
leak_check "$C_HEAP" heap_mb "$LEAK_M"; L2="$LEAK_RESULT"
leak_check "$C_DB_TOTAL" db_conns "$LEAK_C"; L3="$LEAK_RESULT"
combine no_sustained_leaks "$L1" "$L2" "$L3"

recovery_check "$C_GOROUTINES" goroutines "$REC_G"; S1="$REC_RESULT"
recovery_check "$C_HEAP" heap_mb "$REC_M"; S2="$REC_RESULT"
recovery_check "$C_DB_TOTAL" db_conns "$REC_C"; S3="$REC_RESULT"
combine spike_recovery_successful "$S1" "$S2" "$S3"

POOL_PEAK="$(stat $C_DB_TOTAL max read_only,crud_mix)"
if [[ -z "$POOL_PEAK" ]]; then
    report SKIP connection_pool_not_exhausted_below_100vu "no read_only/crud_mix db samples"
elif ge "$POOL_PEAK" "$C_MAX"; then
    report FAIL connection_pool_not_exhausted_below_100vu "peak $POOL_PEAK >= max_configured $C_MAX"
else
    report PASS connection_pool_not_exhausted_below_100vu "peak $POOL_PEAK < max_configured $C_MAX"
fi

ADVISORY=1
peak_check rss_peak "$C_RSS" rss MB "$R_WARN" "$R_CRIT"
peak_check db_connections_peak "$C_DB_TOTAL" "db connections" "" "$C_WARN" "$C_CRIT"

echo ""
if [[ "$REQUIRED_JUDGED" -eq 0 ]]; then
    echo "⚠️  INCONCLUSIVE: every required check was skipped, so nothing was judged (see the monitor's warnings)"
    exit 2
fi
if [[ "$FAILS" -ge "$MAX_CRITICAL" ]]; then
    echo "❌ FAIL: $FAILS critical issue(s) (fails at $MAX_CRITICAL), $WARNS warning(s)"
    exit 1
fi
if [[ "$WARNS" -gt "$MAX_WARNINGS" ]]; then
    echo "⚠️  PASS with $WARNS warning(s), above pass_fail.max_warnings ($MAX_WARNINGS): investigate"
else
    echo "✅ PASS: 0 critical issues, $WARNS warning(s)"
fi
