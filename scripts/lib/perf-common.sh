#!/usr/bin/env bash
# scripts/lib/perf-common.sh
#
# Helpers shared by the in-cluster perf harness: perf-k8s-run.sh,
# perf-watchdog.sh and perf-nrql.sh. Sourced, never executed. Bash 3.2
# compatible (macOS /bin/bash), and every date conversion goes through jq so
# GNU and BSD date never disagree.

# now_iso -> current UTC time as ISO-8601 with a Z suffix
now_iso() { date -u +%Y-%m-%dT%H:%M:%SZ; }

# is_iso STRING -> 0 when STRING is ISO-8601 UTC with a Z suffix (fraction allowed)
is_iso() { [[ "$1" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]+)?Z$ ]]; }

# iso_to_epoch ISO -> epoch seconds; prints nothing on bad input
iso_to_epoch() {
    jq -rn --arg t "$1" '$t | sub("\\.[0-9]+Z$"; "Z") | try fromdateiso8601 catch empty' 2>/dev/null || true
}

# epoch_to_iso EPOCH -> ISO-8601 UTC with a Z suffix
epoch_to_iso() { jq -rn --argjson e "$1" '$e | todateiso8601'; }

# duration_s DUR -> seconds. Accepts k6-style durations (90s, 10m, 1m30s, 1h)
# and a bare number of seconds; returns 1 for anything else.
duration_s() {
    local d="$1" total=0 n unit
    if [[ "$d" =~ ^[0-9]+$ ]]; then echo "$d"; return 0; fi
    [[ "$d" =~ ^([0-9]+[hms])+$ ]] || return 1
    while [[ -n "$d" ]]; do
        [[ "$d" =~ ^([0-9]+)([hms])(.*)$ ]] || return 1
        n="${BASH_REMATCH[1]}"; unit="${BASH_REMATCH[2]}"; d="${BASH_REMATCH[3]}"
        case "$unit" in
            h) total=$((total + n * 3600)) ;;
            m) total=$((total + n * 60)) ;;
            s) total=$((total + n)) ;;
        esac
    done
    echo "$total"
}

# fgt A B -> 0 when A > B; fge A B -> 0 when A >= B (floating point)
fgt() { awk -v a="$1" -v b="$2" 'BEGIN { exit !(a + 0 > b + 0) }'; }
fge() { awk -v a="$1" -v b="$2" 'BEGIN { exit !(a + 0 >= b + 0) }'; }
