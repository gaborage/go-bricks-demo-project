#!/usr/bin/env bash
# scripts/lib/perf-common.sh
#
# Helpers shared by the in-cluster perf harness: perf-k8s-run.sh,
# perf-watchdog.sh and perf-nrql.sh. Sourced, never executed. Bash 3.2
# compatible (macOS /bin/bash), and every date conversion goes through jq so
# GNU and BSD date never disagree.

# now_iso -> current UTC time as ISO-8601 with a Z suffix
now_iso() {
    date -u +%Y-%m-%dT%H:%M:%SZ
    return 0
}

# is_iso STRING -> 0 when STRING is ISO-8601 UTC with a Z suffix (fraction allowed)
is_iso() {
    local value="$1"
    if [[ "$value" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]+)?Z$ ]]; then
        return 0
    fi
    return 1
}

# iso_to_epoch ISO -> epoch seconds; prints nothing on bad input. Strict: the
# value must round-trip, so an impossible date (month 13, February 30) that
# strptime would reject or roll over prints nothing too.
iso_to_epoch() {
    local value="$1"
    jq -rn --arg t "$value" '($t | sub("\\.[0-9]+Z$"; "Z")) as $n
        | ($n | try fromdateiso8601 catch null) as $e
        | if $e != null and ($e | todateiso8601) == $n then $e else empty end' 2>/dev/null || true
    return 0
}

# epoch_to_iso EPOCH -> ISO-8601 UTC with a Z suffix
epoch_to_iso() {
    local epoch="$1"
    jq -rn --argjson e "$epoch" '$e | todateiso8601'
    return 0
}

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
            *) return 1 ;;   # unreachable: the pattern above admits h, m and s only
        esac
    done
    echo "$total"
    return 0
}

# free_path STEM EXT -> STEM.EXT, or the first of STEM-1.EXT, STEM-2.EXT, ... that
# does not exist yet. Archive names carry a one-second stamp; this keeps two
# archives made in the same second from overwriting each other.
free_path() {
    local stem="$1" ext="$2" path n=0
    path="$stem.$ext"
    while [[ -e "$path" ]]; do
        n=$((n + 1))
        path="$stem-$n.$ext"
    done
    printf '%s' "$path"
    return 0
}

# fgt A B -> 0 when A > B; fge A B -> 0 when A >= B (floating point)
fgt() {
    local a="$1" b="$2"
    if awk -v a="$a" -v b="$b" 'BEGIN { exit !(a + 0 > b + 0) }'; then return 0; fi
    return 1
}
fge() {
    local a="$1" b="$2"
    if awk -v a="$a" -v b="$b" 'BEGIN { exit !(a + 0 >= b + 0) }'; then return 0; fi
    return 1
}
