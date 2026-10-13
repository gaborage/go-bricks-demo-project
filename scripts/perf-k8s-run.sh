#!/usr/bin/env bash
# scripts/perf-k8s-run.sh
#
# One runner for the in-cluster performance harness: it renders, applies and
# waits for a k6 Job (loadtests/k8s/k6-job.yaml) or a products reset Job
# (loadtests/k8s/psql-job.yaml), saves the Job log, extracts the k6 summary,
# appends one row to windows.tsv and applies the validity gates.
#
# Kinds and modes:
#
#   --kind k6      one rep: optional warm-up invocation + measured invocation
#   --kind reset   run the SQL that `perf-reset-products.sh --print-sql` prints,
#                  and gate on the counts it reports
#   --kind idle    record a no-load window of --duration as scenario "idle"
#                  (no Job; idle subtraction needs the window)
#   --render-only  print the manifests to stdout and exit; touches no cluster.
#                  Pipe into `kubectl apply --dry-run=server -f -` for an
#                  admission check.
#   --load-configmaps <git-ref>
#                  git archive <ref> -> two ConfigMaps, loadtests/*.ts and
#                  loadtests/types/*.ts, named k6-scripts-<ref> and
#                  k6-types-<ref>; prints a sha256 manifest of every file.
#
# Labels. Every Job and ConfigMap the runner creates carries
# app.kubernetes.io/part-of=k6-perf, the cleanup selector for all perf objects.
# The k6 Job adds app.kubernetes.io/component=load-generator and the reset Job
# app.kubernetes.io/component=reset, so the watchdog selector
#   app.kubernetes.io/part-of=k6-perf,app.kubernetes.io/component=load-generator
# kills load Jobs only.
#
# Results under --out:
#   <scenario>-r<n>.log          full Job log
#   k6/<scenario>-r<n>.json      the summary line between the ---PERF-SUMMARY-*--- markers
#   windows.tsv                  run_id scenario rep warm_start run_start run_end rc valid reason
#   facts/<scenario>-r<n>.env    inputs of the cluster-state gates
#   reset-<UTC stamp>.log        reset output (counts only)
#   attempts/                    an earlier attempt's files, moved aside when a rep is re-run
#
# Validity gates (windows.tsv reason codes, `;`-joined in this order):
#   dropped          dropped_iterations > 0, or the metric is absent
#   rate             iterations / duration more than 1% off --rate, or
#                    perfMeta.rate differs from --rate (skipped for a PERF_STEPS staircase)
#   429              http_reqs{status:429} > 0, or the submetric is absent
#   5xx              5xx responses >= 0.1% of http_reqs, counted from the suite's
#                    perf_http_5xx counter; without it, from http_req_failed, an upper
#                    bound that also counts 4xx and network errors (logged as a
#                    warning). http_reqs{status:NNN} submetrics are never used: k6
#                    only creates the ones a threshold names, so a set can be partial.
#   void             a VOID line in --void-log (default <out>/void.log) inside
#                    [warm or run start, run end or now]
#   pod / node       app pod replaced, restarted or more than one / node changed,
#                    from facts read before and after the Job (APP_SELECTOR)
#   k6cpu            k6 container CPU reached 70% of its CPU request, K6_CPU_REQUEST
#                    (default 1 core), sampled with kubectl top
#   acu              Aurora ACU left ACU_WARM +/- 1 inside the window (CloudWatch)
#   missing-summary  no single summary line after RUN_START=, no RC=, or a
#                    perfMeta.runId that is not this rep's run id
#   warmup           a warm-up started (WARM_START=) but WARM_RC= is missing or not
#                    0, so the measured run started cold. No warm-up (--warm 0) is fine.
# A gate whose input is missing (no APP_SELECTOR, no metrics-server, no
# ACU_WARM) is logged as not evaluated. --facts <file> supplies the cluster-state
# inputs directly, which is how the gates are tested offline. A failing k6 SLO
# threshold (RC=99) is a result, not a void.
#
# Exit codes: 0 valid, 3 void (row written) or reset gate failed, 1 error
# (nothing measured: render/apply failure, no log), 2 usage.
#
# Configuration comes from --env <file>, a sourced bash file (see
# loadtests/k8s/.env.example). Flags are last-wins; --set accumulates and a
# later NAME wins.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
# shellcheck source-path=SCRIPTDIR source=lib/perf-common.sh
source "$SCRIPT_DIR/lib/perf-common.sh"

log() {
    echo "perf-k8s-run: $*" >&2
    return 0
}
die() { log "error: $*"; exit 1; }
usage_err() { log "$*"; log "run with --help for usage"; exit 2; }

usage() {
    cat <<'EOF'
Usage:
  perf-k8s-run.sh --env FILE --kind k6 --label L --scenario S --rep N --script F.ts
                  --rate R --warm DUR --duration DUR [--set K=V]... --out DIR
                  [--discard] [--void-log FILE] [--facts FILE] [--acu-warm X] [--render-only]
  perf-k8s-run.sh --env FILE --kind reset --rows N --watermark ISO --out DIR
                  [--clear-outbox] [--drop-seed] [--base-rows N] [--render-only]
  perf-k8s-run.sh --env FILE --kind idle --label L --rep N --duration DUR --out DIR
                  [--void-log FILE] [--facts FILE]
  perf-k8s-run.sh --env FILE --load-configmaps GIT_REF [--out DIR] [--render-only]

  --rate 0 is only valid with --set PERF_STEPS=r:dur,... and --duration 0 (staircase).
  --warm 0 skips the warm-up invocation. --discard keeps the log only: no
  summary, no gates and no windows.tsv row, and the measured run gets
  --no-thresholds.
  DUR is a k6-style duration (90s, 10m, 1m30s, 1h); a bare number is seconds.

Exit codes: 0 valid, 3 void, 1 error, 2 usage.
EOF
    return 0
}

# ---------------------------------------------------------------------------
# Argument parsing. Flag values live in lowercase opt_* variables, so a sourced
# env file (uppercase, e.g. WARM=2m) can never overwrite a flag.
# ---------------------------------------------------------------------------

opt_env="" opt_kind="" opt_render_only=0 opt_load_ref="" opt_label="" opt_scenario="" opt_rep=""
opt_script="" opt_rate="" opt_warm="" opt_duration="" opt_out="" opt_discard=0 opt_rows=""
opt_watermark="" opt_clear_outbox=0 opt_drop_seed=0 opt_base_rows="" opt_void_log=""
opt_facts="" opt_acu_warm=""
opt_set_names=() opt_set_values=()

need_value() { # FLAG VALUE
    local flag="${1-}" value="${2-}"
    [[ -n "$value" ]] || usage_err "$flag needs a value"
    return 0
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        -h|--help) usage; exit 0 ;;
        --env) need_value "$@"; opt_env="$2"; shift 2 ;;
        --kind) need_value "$@"; opt_kind="$2"; shift 2 ;;
        --render-only) opt_render_only=1; shift ;;
        --load-configmaps) need_value "$@"; opt_load_ref="$2"; shift 2 ;;
        --label) need_value "$@"; opt_label="$2"; shift 2 ;;
        --scenario) need_value "$@"; opt_scenario="$2"; shift 2 ;;
        --rep) need_value "$@"; opt_rep="$2"; shift 2 ;;
        --script) need_value "$@"; opt_script="$2"; shift 2 ;;
        --rate) need_value "$@"; opt_rate="$2"; shift 2 ;;
        --warm) need_value "$@"; opt_warm="$2"; shift 2 ;;
        --duration) need_value "$@"; opt_duration="$2"; shift 2 ;;
        --set)
            need_value "$@"
            [[ "$2" =~ ^([A-Z_][A-Z0-9_]*)=(.*)$ ]] || usage_err "--set expects NAME=value with NAME in [A-Z0-9_], got: $2"
            opt_set_names+=("${BASH_REMATCH[1]}"); opt_set_values+=("${BASH_REMATCH[2]}")
            shift 2 ;;
        --out) need_value "$@"; opt_out="$2"; shift 2 ;;
        --discard) opt_discard=1; shift ;;
        --rows) need_value "$@"; opt_rows="$2"; shift 2 ;;
        --watermark) need_value "$@"; opt_watermark="$2"; shift 2 ;;
        --clear-outbox) opt_clear_outbox=1; shift ;;
        --drop-seed) opt_drop_seed=1; shift ;;
        --base-rows) need_value "$@"; opt_base_rows="$2"; shift 2 ;;
        --void-log) need_value "$@"; opt_void_log="$2"; shift 2 ;;
        --facts) need_value "$@"; opt_facts="$2"; shift 2 ;;
        --acu-warm) need_value "$@"; opt_acu_warm="$2"; shift 2 ;;
        *) usage_err "unknown argument: $1" ;;
    esac
done

[[ -n "$opt_env" ]] || usage_err "--env is required"
[[ -f "$opt_env" ]] || usage_err "--env file not found: $opt_env"
[[ -z "$opt_facts" || -f "$opt_facts" ]] || usage_err "--facts file not found: $opt_facts"
# shellcheck source=/dev/null
source "$opt_env"

for tool in jq envsubst; do
    command -v "$tool" >/dev/null 2>&1 || die "$tool is required"
done

# Constants are set after the env file, so it cannot change them.
K6_TEMPLATE="$REPO_ROOT/loadtests/k8s/k6-job.yaml"
PSQL_TEMPLATE="$REPO_ROOT/loadtests/k8s/psql-job.yaml"
# Test hook: the offline suite points this at a stub of the SQL generator.
RESET_SCRIPT="${PERF_RESET_SCRIPT:-$SCRIPT_DIR/perf-reset-products.sh}"
# shellcheck disable=SC2016 # literal ${NAME} tokens: envsubst's explicit variable list
K6_TEMPLATE_VARS='${JOB_NAME} ${NAMESPACE} ${RUN_ID_LABEL} ${K6_IMAGE} ${ACTIVE_DEADLINE_SECONDS} ${K6_RUN_AS_USER} ${K6_NODE_SELECTOR} ${K6_AFFINITY} ${K6_ENV} ${SCRIPTS_CONFIGMAP} ${TYPES_CONFIGMAP} ${K6_EPHEMERAL_REQUEST} ${K6_EPHEMERAL_LIMIT} ${K6_CPU_REQUEST} ${K6_CPU_LIMIT}'
# shellcheck disable=SC2016
PSQL_TEMPLATE_VARS='${JOB_NAME} ${NAMESPACE} ${PSQL_IMAGE} ${PSQL_RUN_AS_USER} ${DB_SECRET} ${PG_HOST_KEY} ${PG_PORT_KEY} ${PG_DB_KEY} ${PG_USER_KEY} ${PG_PASSWORD_KEY} ${PG_CA_CONFIGMAP} ${PG_CA_KEY} ${SQL_CONFIGMAP} ${PSQL_EPHEMERAL_REQUEST} ${PSQL_EPHEMERAL_LIMIT}'
K6_CPU_GATE_PCT=70         # k6cpu gate threshold, percent of the K6_CPU_REQUEST the run uses
RATE_TOLERANCE=0.01        # rate gate: |achieved - target| / target
MAX_5XX_PCT=0.1            # 5xx gate: must stay below this percent of http_reqs
STEP_RAMP_S=15             # ramp before each PERF_STEPS step (k6 suite)
DEADLINE_SLACK_S=300       # activeDeadlineSeconds = warm + duration + slack
WAIT_GRACE_S=60            # wait past the deadline so the Job can report Failed itself
PSQL_DEADLINE_S=900        # activeDeadlineSeconds in psql-job.yaml
POLL_S=10
SUMMARY_FILE_IN_POD=/tmp/perf-summary.json

# ---------------------------------------------------------------------------
# Small helpers
# ---------------------------------------------------------------------------

lower() {
    tr '[:upper:]' '[:lower:]'
    return 0
}

# dns_name STRING MAXLEN -> DNS-1123 label fragment (lowercase alnum and '-')
dns_name() {
    local text="$1" max="$2" s
    s="$(printf '%s' "$text" | lower | tr -c 'a-z0-9-' '-' | sed -e 's/--*/-/g' -e 's/^-//' -e 's/-$//')"
    s="${s:0:$max}"
    printf '%s' "${s%-}"
    return 0
}

sha256_stdin() {
    if command -v sha256sum >/dev/null 2>&1; then sha256sum | cut -d' ' -f1
    else shasum -a 256 | cut -d' ' -f1; fi
    return 0
}

require_env() { # NAME... : unset or empty is a usage error
    local name
    for name in "$@"; do
        [[ -n "${!name:-}" ]] || usage_err "the env file must set $name (see loadtests/k8s/.env.example)"
    done
    return 0
}

kc() {
    kubectl --context "$KCTX" --namespace "$NS" "$@"
    return $?
}

stamp() { # UTC stamp for unique names; the offline suite pins it
    if [[ -n "${PERF_K8S_RUN_STAMP:-}" ]]; then echo "$PERF_K8S_RUN_STAMP"; else date -u +%Y%m%dT%H%M%SZ; fi
    return 0
}

# storage_quantity NAME DEFAULT -> the env value of NAME, or DEFAULT; refuses a non-quantity
storage_quantity() {
    local name="$1" default="$2" value
    value="${!name:-$default}"
    [[ "$value" =~ ^[0-9]+(Ki|Mi|Gi)$ ]] || usage_err "$name must be a storage quantity such as 256Mi, got '$value'"
    printf '%s' "$value"
    return 0
}

# cpu_quantity NAME DEFAULT -> the env value of NAME, or DEFAULT; refuses anything but
# whole cores (4) or millicores (3500m), and refuses zero
cpu_quantity() {
    local name="$1" default="$2" value
    value="${!name:-$default}"
    [[ "$value" =~ ^(0|[1-9][0-9]{0,8})m?$ ]] \
        || usage_err "$name must be a CPU quantity in whole cores such as 4 or millicores such as 3500m, got '$value'"
    [[ "$(cpu_millicores "$value")" -gt 0 ]] || usage_err "$name must be above zero, got '$value'"
    printf '%s' "$value"
    return 0
}

# cpu_millicores QUANTITY -> millicores of a quantity cpu_quantity accepted
cpu_millicores() {
    local q="$1"
    if [[ "$q" == *m ]]; then echo $((10#${q%m})); else echo $((10#$q * 1000)); fi
    return 0
}

# render TEMPLATE VARLIST -> stdout. Refuses an empty variable from the list:
# envsubst would render it as nothing, silently.
render() {
    local template="$1" list="$2" v name
    for v in $list; do
        name="${v#\$\{}"; name="${name%\}}"
        [[ -n "${!name:-}" ]] || die "template variable $name is empty (rendering $(basename "$template"))"
        export "${name?}"   # envsubst reads the values from its environment
    done
    # Drop the header comment block: after substitution it would echo values.
    if ! envsubst "$list" <"$template" | sed '/^#/d'; then
        return 1
    fi
    return 0
}

# set_value NAME -> the last --set value for NAME; returns 1 when not set.
set_value() {
    local name="$1" i found=1 value=""
    for ((i = 0; i < ${#opt_set_names[@]}; i++)); do
        if [[ "${opt_set_names[$i]}" == "$name" ]]; then value="${opt_set_values[$i]}"; found=0; fi
    done
    [[ $found -eq 0 ]] || return 1
    printf '%s' "$value"
    return 0
}

# ---------------------------------------------------------------------------
# windows.tsv, void log, facts, gates
# ---------------------------------------------------------------------------

append_window() { # run_id scenario rep warm_start run_start run_end rc valid reason
    local file="$opt_out/windows.tsv"
    if [[ ! -s "$file" ]]; then
        printf 'run_id\tscenario\trep\twarm_start\trun_start\trun_end\trc\tvalid\treason\n' >"$file"
    fi
    printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$@" >>"$file"
    return 0
}

# void_in_window START_EPOCH END_EPOCH -> prints matching VOID lines; 0 if any.
# Line format, written by perf-watchdog.sh:
#   VOID <ISO-8601 UTC> rule=<rule> scope=<rep|session> action=<kill|stop> detail=<text>
void_in_window() {
    local start="$1" end="$2" line ts epoch hit=1
    if [[ ! -f "$opt_void_log" ]]; then
        log "notice: void log $opt_void_log does not exist (is perf-watchdog.sh running?); the void gate sees no lines"
        return 1
    fi
    while IFS= read -r line; do
        [[ "$line" == "VOID "* ]] || continue
        ts="$(printf '%s\n' "$line" | awk -v col=2 '{ print $col }')"
        epoch="$(iso_to_epoch "$ts")"
        [[ -n "$epoch" ]] || continue
        if ((epoch >= start && epoch <= end)); then
            echo "$line"; hit=0
        fi
    done <"$opt_void_log"
    return $hit
}

facts_get() { # FILE KEY -> value, empty when absent. Parsed, never sourced.
    local file="$1" key="$2"
    [[ -f "$file" ]] || return 0
    awk -v k="$key" 'index($0, k "=") == 1 { v = substr($0, length(k) + 2) } END { if (v != "") print v }' "$file"
    return 0
}

# app_pod_facts SUFFIX -> pods_<s>= pod_<s>= restarts_<s>= node_<s>= lines
app_pod_facts() {
    local suffix="$1" json
    [[ -n "${APP_SELECTOR:-}" ]] || return 0
    if ! json="$(kc get pods -l "$APP_SELECTOR" -o json 2>/dev/null)"; then
        log "notice: could not read the app pods ($APP_SELECTOR); pod/node gates not evaluated"
        return 0
    fi
    printf '%s' "$json" | jq -r --arg s "$suffix" '
        [.items[] | select(.metadata.deletionTimestamp == null)] as $p
        | "pods_\($s)=\($p | length)",
          "pod_\($s)=\($p | map(.metadata.uid) | sort | join(","))",
          "restarts_\($s)=\($p | map([.status.containerStatuses[]?.restartCount] | add // 0) | add // 0)",
          "node_\($s)=\($p | map(.spec.nodeName // "") | sort | join(","))"'
    return 0
}

# acu_facts START_ISO END_ISO -> acu_min= acu_max= lines (CloudWatch, read-only)
acu_facts() {
    local start="$1" end="$2" json
    [[ -n "${AURORA_CLUSTER:-}" ]] || { log "notice: AURORA_CLUSTER unset; acu gate not evaluated"; return 0; }
    command -v aws >/dev/null 2>&1 || { log "notice: aws CLI not found; acu gate not evaluated"; return 0; }
    if ! json="$(aws cloudwatch get-metric-statistics --namespace AWS/RDS \
        --metric-name ServerlessDatabaseCapacity \
        --dimensions "Name=DBClusterIdentifier,Value=$AURORA_CLUSTER" \
        --start-time "$start" --end-time "$end" --period 60 \
        --statistics Maximum Minimum --output json 2>/dev/null)"; then
        log "notice: CloudWatch ACU read failed; acu gate not evaluated"
        return 0
    fi
    printf '%s' "$json" | jq -r '
        if (.Datapoints | length) == 0 then empty
        else "acu_min=\([.Datapoints[].Minimum] | min)", "acu_max=\([.Datapoints[].Maximum] | max)" end'
    return 0
}

G_DROPPED=0 G_RATE=0 G_429=0 G_5XX=0 G_VOID=0 G_POD=0 G_NODE=0 G_K6CPU=0 G_ACU=0 G_MISSING=0 G_WARMUP=0
reasons() {
    local out="" pair
    for pair in "$G_DROPPED:dropped" "$G_RATE:rate" "$G_429:429" "$G_5XX:5xx" "$G_VOID:void" \
        "$G_POD:pod" "$G_NODE:node" "$G_K6CPU:k6cpu" "$G_ACU:acu" "$G_MISSING:missing-summary" \
        "$G_WARMUP:warmup"; do
        [[ "${pair%%:*}" -eq 0 ]] || out="${out:+$out;}${pair#*:}"
    done
    echo "$out"
    return 0
}

# facts_gates FILE MODE -> pod and node gates; MODE k6 adds k6cpu and acu.
facts_gates() {
    local f="$1" mode="$2" v1 v2 r1 r2 n1 n2 cpu warm amin amax k
    # Values feed shell arithmetic: refuse a facts file with non-numbers in them.
    for k in pods_before pods_after restarts_before restarts_after k6_cpu_max_m; do
        v1="$(facts_get "$f" "$k")"
        [[ -z "$v1" || "$v1" =~ ^[0-9]+$ ]] || die "facts $f: $k must be a whole number, got '$v1'"
    done
    for k in acu_warm acu_min acu_max; do
        v1="$(facts_get "$f" "$k")"
        [[ -z "$v1" || "$v1" =~ ^[0-9]+(\.[0-9]+)?$ ]] || die "facts $f: $k must be a number, got '$v1'"
    done
    v1="$(facts_get "$f" pod_before)"; v2="$(facts_get "$f" pod_after)"
    if [[ -n "$v1" && -n "$v2" ]]; then
        r1="$(facts_get "$f" restarts_before)"; r2="$(facts_get "$f" restarts_after)"
        n1="$(facts_get "$f" pods_before)"; n2="$(facts_get "$f" pods_after)"
        if [[ "$v1" != "$v2" || "$r1" != "$r2" || "${n1:-1}" -gt 1 || "${n2:-1}" -gt 1 ]]; then
            log "gate pod: before=$v1 (restarts $r1, pods ${n1:-?}) after=$v2 (restarts $r2, pods ${n2:-?})"
            G_POD=1
        fi
    else
        log "notice: pod gate not evaluated (no app pod facts; set APP_SELECTOR)"
    fi
    v1="$(facts_get "$f" node_before)"; v2="$(facts_get "$f" node_after)"
    if [[ -n "$v1" && -n "$v2" ]]; then
        if [[ "$v1" != "$v2" ]]; then log "gate node: before=$v1 after=$v2"; G_NODE=1; fi
    else
        log "notice: node gate not evaluated (no node facts)"
    fi
    [[ "$mode" == k6 ]] || return 0
    cpu="$(facts_get "$f" k6_cpu_max_m)"
    if [[ -n "$cpu" ]]; then
        if ((cpu * 100 >= K6_CPU_REQUEST_M * K6_CPU_GATE_PCT)); then
            log "gate k6cpu: max ${cpu}m reached ${K6_CPU_GATE_PCT}% of the ${K6_CPU_REQUEST_M}m request"
            G_K6CPU=1
        fi
    else
        log "notice: k6cpu gate not evaluated (no kubectl top samples)"
    fi
    warm="$(facts_get "$f" acu_warm)"; amin="$(facts_get "$f" acu_min)"; amax="$(facts_get "$f" acu_max)"
    if [[ -n "$warm" && -n "$amin" && -n "$amax" ]]; then
        if fgt "$amax" "$(awk -v w="$warm" 'BEGIN { print w + 1 }')" \
            || fgt "$(awk -v w="$warm" 'BEGIN { print w - 1 }')" "$amin"; then
            log "gate acu: window min=$amin max=$amax, warm level $warm +/- 1"
            G_ACU=1
        fi
    else
        log "notice: acu gate not evaluated (needs ACU_WARM and CloudWatch datapoints)"
    fi
    return 0
}

# warmup_gate SEEN WARM_RC -> G_WARMUP when a warm-up started (SEEN=1) and its
# WARM_RC is missing or not 0: the measured run then started cold.
warmup_gate() {
    local seen="$1" warm_rc="$2"
    [[ "$seen" -eq 1 ]] || return 0   # no warm-up (--warm 0)
    if [[ -z "$warm_rc" ]]; then
        log "gate warmup: WARM_START= without WARM_RC= (the warm-up did not finish)"
        G_WARMUP=1
    elif [[ "$warm_rc" != 0 ]]; then
        log "gate warmup: the warm-up exited $warm_rc, so the measured run started cold"
        G_WARMUP=1
    fi
    return 0
}

void_gate() { # START_ISO END_ISO
    local start_iso="$1" end_iso="$2" s e lines
    s="$(iso_to_epoch "$start_iso")"; e="$(iso_to_epoch "$end_iso")"
    if [[ -z "$s" || -z "$e" ]]; then log "notice: void gate not evaluated (window unknown)"; return 0; fi
    if lines="$(void_in_window "$s" "$e")"; then
        log "gate void: watchdog VOID line(s) inside $start_iso .. $end_iso:"
        printf '%s\n' "$lines" >&2
        G_VOID=1
    fi
    return 0
}

archive_previous() { # PATH... -> moved into <out>/attempts/, so a re-run never overwrites
    local p base ext ts dest
    ts="$(stamp)"
    for p in "$@"; do
        [[ -e "$p" ]] || continue
        mkdir -p "$opt_out/attempts"
        base="$(basename "$p")"; ext="${base##*.}"
        dest="$(free_path "$opt_out/attempts/${base%.*}-$ts" "$ext")"
        mv "$p" "$dest"
        log "moved the previous attempt's $base to attempts/$(basename "$dest")"
    done
    return 0
}

# ---------------------------------------------------------------------------
# Job wait and logs
# ---------------------------------------------------------------------------

K6_CPU_MAX="" JOB_STATE=""

sample_k6_cpu() { # JOB -> raises K6_CPU_MAX from `kubectl top` (no-op without metrics-server)
    local job="$1" out m
    out="$(kc top pod -l "job-name=$job" --containers --no-headers 2>/dev/null)" || return 0
    m="$(printf '%s\n' "$out" | awk -v col=3 '
        { c = $col; if (c ~ /m$/) { sub(/m$/, "", c); v = c + 0 } else if (c ~ /^[0-9.]+$/) { v = c * 1000 } else next
          if (v > max) max = v; seen = 1 }
        END { if (seen) printf "%d", max }')"
    [[ -n "$m" ]] || return 0
    if [[ -z "$K6_CPU_MAX" ]] || ((m > K6_CPU_MAX)); then K6_CPU_MAX="$m"; fi
    return 0
}

# wait_job JOB TIMEOUT_S SAMPLE_CPU(0|1) -> JOB_STATE = Complete|Failed|Deleted|Timeout
wait_job() {
    local job="$1" timeout="$2" sample="$3" start conds
    start="$(date +%s)"
    while :; do
        if conds="$(kc get job "$job" -o jsonpath='{range .status.conditions[?(@.status=="True")]}{.type}{" "}{end}' 2>&1)"; then
            case " $conds " in
                *" Complete "*) JOB_STATE=Complete; return 0 ;;
                *" Failed "*) JOB_STATE=Failed; return 0 ;;
                *) ;;   # still running
            esac
        elif printf '%s' "$conds" | grep -q NotFound; then
            JOB_STATE=Deleted; return 0
        else
            log "notice: reading job/$job failed, retrying: $conds"
        fi
        [[ "$sample" -eq 0 ]] || sample_k6_cpu "$job"
        if (($(date +%s) - start >= timeout)); then JOB_STATE=Timeout; return 0; fi
        sleep "$POLL_S"
    done
    return 0
}

# fetch_logs JOB CONTAINER PARTIAL DEST -> 0 when DEST ends up non-empty
fetch_logs() {
    local job="$1" container="$2" partial="$3" dest="$4" tmp
    tmp="$(mktemp "${TMPDIR:-/tmp}/perf-k8s-run-log.XXXXXX")"
    if kc logs "job/$job" -c "$container" >"$tmp" 2>/dev/null && [[ -s "$tmp" ]]; then
        mv "$tmp" "$dest"
    elif [[ -s "$partial" ]]; then
        log "notice: the pod log is gone; keeping the log streamed during the run"
        rm -f "$tmp"; cp "$partial" "$dest"
    else
        rm -f "$tmp"; return 1
    fi
    return 0
}

# apply_and_wait MANIFEST JOB CONTAINER TIMEOUT SAMPLE_CPU DEST_LOG -> 1 when no log
apply_and_wait() {
    local manifest="$1" job="$2" container="$3" timeout="$4" sample="$5" dest="$6" partial follower i rc=0
    printf '%s\n' "$manifest" | kc apply -f - >&2 || die "kubectl apply failed for job/$job"
    log "applied job/$job; waiting up to ${timeout}s"
    partial="$(mktemp "${TMPDIR:-/tmp}/perf-k8s-run-stream.XXXXXX")"
    # Stream the log while the Job runs: a killed Job takes its pod, and the log, with it.
    kc logs -f "job/$job" -c "$container" --pod-running-timeout="${timeout}s" >"$partial" 2>/dev/null &
    follower=$!
    wait_job "$job" "$timeout" "$sample"
    log "job/$job: $JOB_STATE"
    for ((i = 0; i < 10; i++)); do kill -0 "$follower" 2>/dev/null || break; sleep 1; done
    kill "$follower" 2>/dev/null || true
    wait "$follower" 2>/dev/null || true
    fetch_logs "$job" "$container" "$partial" "$dest" || rc=1
    rm -f "$partial"
    return $rc
}

# ---------------------------------------------------------------------------
# --load-configmaps
# ---------------------------------------------------------------------------

configmap_json() { # NAME REF COMMIT DIGEST FILE... -> ConfigMap JSON, files keyed by basename
    local name="$1" ref="$2" commit="$3" digest="$4" cm f
    shift 4
    cm="$(jq -cn --arg n "$name" --arg ns "$NS" --arg ref "$ref" --arg c "$commit" --arg d "$digest" '{
        apiVersion: "v1", kind: "ConfigMap",
        metadata: { name: $n, namespace: $ns,
            labels: { "app.kubernetes.io/part-of": "k6-perf", "app.kubernetes.io/component": "load-generator-scripts" },
            annotations: { "perf-harness/git-ref": $ref, "perf-harness/commit": $c, "perf-harness/manifest-sha256": $d } },
        data: {} }')"
    for f in "$@"; do
        cm="$(printf '%s' "$cm" | jq -c --arg k "$(basename "$f")" --rawfile v "$f" '.data[$k] = $v')"
    done
    printf '%s\n' "$cm"
    return 0
}

load_configmaps() {
    local ref="$1" commit tmp manifest digest scripts_cm types_cm sname tname f header
    require_env NS
    [[ $opt_render_only -eq 1 ]] || require_env KCTX
    command -v git >/dev/null 2>&1 || die "git is required"
    commit="$(git -C "$REPO_ROOT" rev-parse --verify --quiet "$ref^{commit}")" || usage_err "unknown git ref: $ref"
    tmp="$(mktemp -d "${TMPDIR:-/tmp}/perf-harness.XXXXXX")"
    # shellcheck disable=SC2064 # expand now: tmp is local
    trap "rm -rf '$tmp'" EXIT
    git -C "$REPO_ROOT" archive --format=tar "$commit" -- loadtests | tar -x -C "$tmp"
    local scripts=() types=()
    for f in "$tmp"/loadtests/*.ts; do [[ -f "$f" ]] && scripts+=("$f"); done
    for f in "$tmp"/loadtests/types/*.ts; do [[ -f "$f" ]] && types+=("$f"); done
    [[ ${#scripts[@]} -gt 0 ]] || die "no loadtests/*.ts at $ref"
    [[ ${#types[@]} -gt 0 ]] || die "no loadtests/types/*.ts at $ref"

    manifest="$(for f in "${scripts[@]}" "${types[@]}"; do
        printf '%s  %s\n' "$(sha256_stdin <"$f")" "${f#"$tmp"/}"
    done | LC_ALL=C sort -k2)"
    digest="$(printf '%s\n' "$manifest" | sha256_stdin)"
    sname="k6-scripts-$(dns_name "$ref" 40)"
    tname="k6-types-$(dns_name "$ref" 40)"
    scripts_cm="$(configmap_json "$sname" "$ref" "$commit" "$digest" "${scripts[@]}")"
    types_cm="$(configmap_json "$tname" "$ref" "$commit" "$digest" "${types[@]}")"
    header="# ref $ref commit $commit manifest-sha256 $digest configmaps $sname $tname"

    if [[ $opt_render_only -eq 1 ]]; then
        printf -- '---\n%s\n---\n%s\n' "$scripts_cm" "$types_cm"
        { echo "$header"; printf '%s\n' "$manifest"; } >&2
        exit 0
    fi
    command -v kubectl >/dev/null 2>&1 || die "kubectl is required"
    # Server-side apply: client-side apply copies the object into an annotation
    # capped at 256 KiB, which the scripts ConfigMap can outgrow.
    printf -- '---\n%s\n---\n%s\n' "$scripts_cm" "$types_cm" \
        | kc apply --server-side --force-conflicts --field-manager=perf-k8s-run -f - >&2 \
        || die "kubectl apply of the scripts ConfigMaps failed"
    echo "$header"
    printf '%s\n' "$manifest"
    if [[ -n "$opt_out" ]]; then
        mkdir -p "$opt_out"
        { echo "$header"; printf '%s\n' "$manifest"; } >"$opt_out/harness-$(dns_name "$ref" 60).sha256"
    fi
    exit 0
}

# ---------------------------------------------------------------------------
# --kind k6
# ---------------------------------------------------------------------------

validate_run_identity() {
    [[ -n "$opt_label" ]] || usage_err "--label is required"
    [[ "$opt_label" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,39}$ ]] || usage_err "--label must match [A-Za-z0-9][A-Za-z0-9._-]{0,39}"
    [[ -n "$opt_rep" ]] || usage_err "--rep is required"
    [[ "$opt_rep" =~ ^[0-9]{1,3}$ ]] || usage_err "--rep must be a number"
    return 0
}

# evaluate_k6 LOG JSON_OUT RUN_ID RC MEASURED_S STEPS -> sets the summary gate flags
evaluate_k6() {
    local log_file="$1" json_file="$2" run_id="$3" rc="$4" measured_s="$5" steps="$6"
    local start_line begin end n_begin n_end
    start_line="$(grep -n -E '^RUN_START=' "$log_file" | tail -n 1 | cut -d: -f1 || true)"
    if [[ -z "$start_line" ]]; then
        log "gate missing-summary: no RUN_START= line"
        G_MISSING=1; return 0
    fi
    # grep, not awk: the summary is one very long line.
    begin="$(grep -n -x -F -e '---PERF-SUMMARY-BEGIN---' "$log_file" | cut -d: -f1 | awk -v s="$start_line" -v col=1 '$col > s' || true)"
    end="$(grep -n -x -F -e '---PERF-SUMMARY-END---' "$log_file" | cut -d: -f1 | awk -v s="$start_line" -v col=1 '$col > s' || true)"
    n_begin="$(printf '%s' "$begin" | grep -c . || true)"
    n_end="$(printf '%s' "$end" | grep -c . || true)"
    if [[ "$n_begin" -ne 1 || "$n_end" -ne 1 ]] || [[ "$end" -ne $((begin + 2)) ]]; then
        log "gate missing-summary: expected one marker pair around one line after RUN_START= (begin x$n_begin, end x$n_end)"
        G_MISSING=1; return 0
    fi
    sed -n "$((begin + 1))p" "$log_file" >"$json_file"
    if ! jq -e 'type == "object" and (.metrics | type == "object")' "$json_file" >/dev/null 2>&1; then
        log "gate missing-summary: the summary line is not a k6 summary object"
        rm -f "$json_file"; G_MISSING=1; return 0
    fi
    if [[ -z "$rc" ]]; then
        log "gate missing-summary: no RC= line (the wrapper did not finish)"
        G_MISSING=1
    fi

    local fields dropped iters reqs n429 failed counter5xx meta_run meta_rate
    fields="$(jq -r '
        def cnt(k): (.metrics[k].values.count // "absent");
        [ cnt("dropped_iterations"), cnt("iterations"), cnt("http_reqs"), cnt("http_reqs{status:429}"),
          (.metrics.http_req_failed.values.passes // "absent"), cnt("perf_http_5xx"),
          (.perfMeta.runId // "absent"), (.perfMeta.rate // "absent") ] | @tsv' "$json_file")"
    IFS=$'\t' read -r dropped iters reqs n429 failed counter5xx meta_run meta_rate <<<"$fields"

    if [[ "$meta_run" != "$run_id" ]]; then
        log "gate missing-summary: perfMeta.runId is '$meta_run', this rep is '$run_id'"
        G_MISSING=1
    fi
    if [[ "$dropped" == absent ]]; then
        log "gate dropped: dropped_iterations is absent from the summary, so zero cannot be shown"
        G_DROPPED=1
    elif fgt "$dropped" 0; then
        log "gate dropped: dropped_iterations=$dropped"
        G_DROPPED=1
    fi
    if [[ "$n429" == absent ]]; then
        log "gate 429: http_reqs{status:429} is absent from the summary, so zero cannot be shown"
        G_429=1
    elif fgt "$n429" 0; then
        log "gate 429: $n429 responses with status 429"
        G_429=1
    fi
    if [[ "$reqs" == absent ]] || ! fgt "$reqs" 0; then
        log "notice: 5xx gate not evaluated (no http_reqs)"
    else
        # The suite's perf_http_5xx counter, else http_req_failed: a superset that
        # never under-counts 5xx. http_reqs{status:NNN} submetrics are not used:
        # k6 creates one only for a status a threshold names, so a partial set
        # (500 without 503) would pass for a complete count.
        local bad="$counter5xx" source="perf_http_5xx"
        if [[ "$bad" == absent && "$failed" != absent ]]; then
            bad="$failed"; source="http_req_failed"
            log "warning: 5xx gate fell back to http_req_failed, which also counts 4xx and network errors (no perf_http_5xx in the summary)"
        fi
        if [[ "$bad" == absent ]]; then
            log "notice: 5xx gate not evaluated (no perf_http_5xx and no http_req_failed)"
        elif fge "$(awk -v b="$bad" -v r="$reqs" 'BEGIN { printf "%.6f", b * 100 / r }')" "$MAX_5XX_PCT"; then
            log "gate 5xx: $bad of $reqs requests ($source); the limit is below ${MAX_5XX_PCT}%"
            G_5XX=1
        fi
    fi
    if [[ -n "$steps" ]]; then
        log "notice: rate gate skipped for a PERF_STEPS staircase"
    elif [[ "$iters" == absent ]]; then
        log "gate rate: iterations is absent from the summary"
        G_RATE=1
    else
        local achieved
        achieved="$(awk -v n="$iters" -v d="$measured_s" 'BEGIN { printf "%.4f", n / d }')"
        if awk -v a="$achieved" -v t="$opt_rate" -v tol="$RATE_TOLERANCE" \
            'BEGIN { d = (a - t) / t; if (d < 0) d = -d; exit !(d > tol) }'; then
            log "gate rate: achieved ${achieved}/s against ${opt_rate}/s (iterations=$iters over ${measured_s}s)"
            G_RATE=1
        fi
        if [[ "$meta_rate" != absent && "$meta_rate" != "$opt_rate" ]]; then
            log "gate rate: perfMeta.rate=$meta_rate but --rate $opt_rate"
            G_RATE=1
        fi
    fi
    return 0
}

run_k6() {
    validate_run_identity
    [[ -n "$opt_scenario" ]] || usage_err "--scenario is required"
    [[ "$opt_scenario" =~ ^[A-Za-z0-9]{1,10}$ ]] || usage_err "--scenario must be 1-10 letters or digits"
    [[ -n "$opt_script" ]] || usage_err "--script is required"
    [[ "$opt_script" =~ ^[A-Za-z0-9._-]+\.(ts|js)$ ]] || usage_err "--script must be a file name under loadtests/ (e.g. products-read-only.ts)"
    [[ -n "$opt_rate" ]] || usage_err "--rate is required"
    [[ "$opt_rate" =~ ^[0-9]+$ ]] || usage_err "--rate must be a whole number of iterations/s"
    [[ -n "$opt_warm" ]] || usage_err "--warm is required (0 for none)"
    [[ -n "$opt_duration" ]] || usage_err "--duration is required"
    local warm_s measured_s steps step_s first_rate="" item
    warm_s="$(duration_s "$opt_warm")" || usage_err "--warm is not a duration: $opt_warm"
    measured_s="$(duration_s "$opt_duration")" || usage_err "--duration is not a duration: $opt_duration"

    steps="$(set_value PERF_STEPS || true)"
    if [[ -n "$steps" ]]; then
        [[ "$opt_rate" -eq 0 ]] || usage_err "--rate must be 0 when PERF_STEPS drives the rate"
        [[ "$measured_s" -eq 0 ]] || usage_err "--duration must be 0 when PERF_STEPS sets the duration"
        while IFS= read -r item; do
            [[ "$item" =~ ^([0-9]+):([0-9hms]+)$ ]] || usage_err "PERF_STEPS entry is not rate:duration: $item"
            step_s="$(duration_s "${BASH_REMATCH[2]}")" || usage_err "PERF_STEPS duration is not a duration: $item"
            [[ -n "$first_rate" ]] || first_rate="${BASH_REMATCH[1]}"
            measured_s=$((measured_s + step_s + STEP_RAMP_S))
        done < <(printf '%s\n' "$steps" | tr ',' '\n')
    else
        [[ "$opt_rate" -gt 0 ]] || usage_err "--rate 0 needs --set PERF_STEPS=r:dur,... (open-model runs only)"
        [[ "$measured_s" -gt 0 ]] || usage_err "--duration 0 needs --set PERF_STEPS=r:dur,..."
        first_rate="$opt_rate"
    fi

    local run_id="$opt_label-$opt_scenario-r$opt_rep"
    local deadline=$((warm_s + measured_s + DEADLINE_SLACK_S))
    require_env NS TARGET_URL K6_IMAGE HARNESS_TAG

    # Container env: harness wiring first, then --set (a later NAME wins).
    local names=() values=() i pairs=()
    add_env() { # NAME VALUE
        local name="$1" value="$2"
        names+=("$name"); values+=("$value")
        return 0
    }
    add_env HOME /tmp
    add_env K6_NO_USAGE_REPORT true
    add_env K6_BASE_URL "$TARGET_URL"
    # An API_PREFIX set to empty is deliberate: the API is served at the root.
    if [[ -n "${API_PREFIX+set}" ]]; then add_env K6_API_PREFIX "$API_PREFIX"; fi
    add_env PERF_RUN_ID "$run_id"
    add_env PERF_HARNESS_TAG "$HARNESS_TAG"
    [[ "$opt_rate" -eq 0 ]] || add_env PERF_RATE "$opt_rate"
    [[ -n "$steps" ]] || add_env PERF_DURATION "${measured_s}s"
    if [[ $opt_discard -eq 0 ]]; then
        add_env PERF_SUMMARY_STDOUT 1
        add_env PERF_SUMMARY_FILE "$SUMMARY_FILE_IN_POD"
        add_env HARNESS_K6_FLAGS ""
    else
        add_env HARNESS_K6_FLAGS "--no-thresholds"
    fi
    add_env HARNESS_SCRIPT "$opt_script"
    if [[ "$warm_s" -gt 0 ]]; then add_env HARNESS_WARM "${warm_s}s"; else add_env HARNESS_WARM 0; fi
    add_env HARNESS_WARM_RATE "$first_rate"
    for ((i = 0; i < ${#opt_set_names[@]}; i++)); do add_env "${opt_set_names[$i]}" "${opt_set_values[$i]}"; done
    for ((i = 0; i < ${#names[@]}; i++)); do pairs+=("${names[$i]}" "${values[$i]}"); done

    # One JSON array; a later duplicate name replaces the earlier value in place.
    # Every '$' is doubled because the kubelet expands $(VAR) in env values.
    # NUL-separated on stdin, not --args: jq would parse a value like
    # "--no-thresholds" as one of its own options.
    K6_ENV="$(printf '%s\0' "${pairs[@]}" | jq -Rsc 'split("\u0000") | .[:-1] as $a
        | reduce range(0; $a | length; 2) as $i ({}; .[$a[$i]] = $a[$i + 1])
        | to_entries | map({ name: .key, value: (.value | gsub("\\$"; "$$")) })')"
    K6_NODE_SELECTOR="$(jq -cn --arg p "${K6_NODEPOOL:-}" --arg z "${K6_ZONE:-}" '{}
        + (if $p != "" then { "karpenter.sh/nodepool": $p } else {} end)
        + (if $z != "" then { "topology.kubernetes.io/zone": $z } else {} end)')"
    if [[ -n "${APP_SELECTOR:-}" ]]; then
        [[ "$APP_SELECTOR" =~ ^[A-Za-z0-9./_-]+=[A-Za-z0-9._-]+(,[A-Za-z0-9./_-]+=[A-Za-z0-9._-]+)*$ ]] \
            || usage_err "APP_SELECTOR must be equality-based k=v[,k=v]: $APP_SELECTOR"
        K6_AFFINITY="$(jq -cn --arg sel "$APP_SELECTOR" '{ podAntiAffinity: { requiredDuringSchedulingIgnoredDuringExecution: [ {
            labelSelector: { matchLabels: ($sel | split(",") | map(split("=") | { (.[0]): .[1] }) | add) },
            topologyKey: "kubernetes.io/hostname" } ] } }')"
    else
        K6_AFFINITY='{}'
    fi
    JOB_NAME="k6-$(dns_name "$run_id" 46)-$(stamp | tr -dc '0-9' | tail -c 10)"
    NAMESPACE="$NS"
    RUN_ID_LABEL="$run_id"
    ACTIVE_DEADLINE_SECONDS="$deadline"
    K6_RUN_AS_USER="${K6_RUN_AS_USER:-12345}"
    SCRIPTS_CONFIGMAP="k6-scripts-$(dns_name "$HARNESS_TAG" 40)"
    TYPES_CONFIGMAP="k6-types-$(dns_name "$HARNESS_TAG" 40)"
    K6_EPHEMERAL_REQUEST="$(storage_quantity K6_EPHEMERAL_REQUEST 256Mi)"
    K6_EPHEMERAL_LIMIT="$(storage_quantity K6_EPHEMERAL_LIMIT 1Gi)"
    # The template gets each CPU quantity as spelled; the k6cpu gate gets the request in millicores.
    K6_CPU_REQUEST="$(cpu_quantity K6_CPU_REQUEST 1)"
    K6_CPU_LIMIT="$(cpu_quantity K6_CPU_LIMIT 2)"
    K6_CPU_REQUEST_M="$(cpu_millicores "$K6_CPU_REQUEST")"
    K6_CPU_LIMIT_M="$(cpu_millicores "$K6_CPU_LIMIT")"
    ((K6_CPU_LIMIT_M >= K6_CPU_REQUEST_M)) \
        || usage_err "K6_CPU_LIMIT ($K6_CPU_LIMIT) must not be below K6_CPU_REQUEST ($K6_CPU_REQUEST)"
    export JOB_NAME NAMESPACE RUN_ID_LABEL K6_IMAGE ACTIVE_DEADLINE_SECONDS K6_RUN_AS_USER \
        K6_NODE_SELECTOR K6_AFFINITY K6_ENV SCRIPTS_CONFIGMAP TYPES_CONFIGMAP K6_EPHEMERAL_REQUEST K6_EPHEMERAL_LIMIT \
        K6_CPU_REQUEST K6_CPU_LIMIT

    local manifest
    manifest="$(render "$K6_TEMPLATE" "$K6_TEMPLATE_VARS")"
    if [[ $opt_render_only -eq 1 ]]; then
        printf '%s\n' "$manifest"
        exit 0
    fi

    [[ -n "$opt_out" ]] || usage_err "--out is required"
    require_env KCTX
    command -v kubectl >/dev/null 2>&1 || die "kubectl is required"
    mkdir -p "$opt_out/k6" "$opt_out/facts"
    opt_void_log="${opt_void_log:-$opt_out/void.log}"
    local log_file="$opt_out/$opt_scenario-r$opt_rep.log"
    local json_file="$opt_out/k6/$opt_scenario-r$opt_rep.json"
    local facts="$opt_out/facts/$opt_scenario-r$opt_rep.env"
    archive_previous "$log_file" "$json_file" "$facts"

    local applied_at finished_at
    applied_at="$(now_iso)"
    if [[ -n "$opt_facts" ]]; then : >"$facts"; else app_pod_facts before >"$facts"; fi

    log "run $run_id: $opt_script rate=$opt_rate warm=$opt_warm duration=$opt_duration deadline=${deadline}s"
    if ! apply_and_wait "$manifest" "$JOB_NAME" k6 $((deadline + WAIT_GRACE_S)) 1 "$log_file"; then
        die "job/$JOB_NAME ($JOB_STATE) left no log; nothing was measured"
    fi
    finished_at="$(now_iso)"

    local warm_start run_start run_end rc warm_seen=0 warm_rc
    grep -q -E '^WARM_START=' "$log_file" && warm_seen=1
    warm_rc="$(grep -E '^WARM_RC=[0-9]+$' "$log_file" | tail -n 1 | cut -d= -f2- || true)"
    warm_start="$(grep -E '^WARM_START=' "$log_file" | tail -n 1 | cut -d= -f2- || true)"
    run_start="$(grep -E '^RUN_START=' "$log_file" | tail -n 1 | cut -d= -f2- || true)"
    run_end="$(grep -E '^RUN_END=' "$log_file" | tail -n 1 | cut -d= -f2- || true)"
    rc="$(grep -E '^RC=[0-9]+$' "$log_file" | tail -n 1 | cut -d= -f2- || true)"
    is_iso "$warm_start" || warm_start=""
    is_iso "$run_start" || run_start=""
    is_iso "$run_end" || run_end=""

    if [[ $opt_discard -eq 1 ]]; then
        log "discarded run $run_id: log saved to $log_file (RC=${rc:-none}); no summary, gates or windows.tsv row"
        [[ "$rc" == "0" ]] || die "discarded run ended with RC=${rc:-none}"
        [[ $warm_seen -eq 0 || "$warm_rc" == "0" ]] || die "discarded run's warm-up ended with WARM_RC=${warm_rc:-none}"
        exit 0
    fi

    if [[ -n "$opt_facts" ]]; then
        cp "$opt_facts" "$facts"
    else
        local warm_level="${opt_acu_warm:-${ACU_WARM:-}}"
        {
            app_pod_facts after
            [[ -z "$K6_CPU_MAX" ]] || echo "k6_cpu_max_m=$K6_CPU_MAX"
            echo "k6_cpu_request_m=$K6_CPU_REQUEST_M"
            echo "k6_cpu_limit_m=$K6_CPU_LIMIT_M"
            if [[ -n "$warm_level" ]]; then
                echo "acu_warm=$warm_level"
                acu_facts "${run_start:-$applied_at}" "${run_end:-$finished_at}"
            fi
        } >>"$facts"
    fi

    # The summary must come from this Job: perfMeta.runId echoes PERF_RUN_ID.
    local expect_run_id
    expect_run_id="$(set_value PERF_RUN_ID || printf '%s' "$run_id")"
    [[ "$expect_run_id" == "$run_id" ]] \
        || log "notice: --set PERF_RUN_ID=$expect_run_id differs from the windows.tsv run_id $run_id"
    evaluate_k6 "$log_file" "$json_file" "$expect_run_id" "$rc" "$measured_s" "$steps"
    void_gate "${warm_start:-${run_start:-$applied_at}}" "${run_end:-$finished_at}"
    facts_gates "$facts" k6
    warmup_gate "$warm_seen" "$warm_rc"

    local reason valid=1
    reason="$(reasons)"
    [[ -z "$reason" ]] || valid=0
    append_window "$run_id" "$opt_scenario" "$opt_rep" "$warm_start" "$run_start" "$run_end" "$rc" "$valid" "$reason"
    if [[ $valid -eq 1 ]]; then
        log "run $run_id: VALID (RC=$rc)"
        exit 0
    fi
    log "run $run_id: VOID ($reason)"
    exit 3
}

# ---------------------------------------------------------------------------
# --kind reset
# ---------------------------------------------------------------------------

count_field() { # LINE KEY -> the value of KEY=value in LINE
    local line="$1" key="$2"
    printf '%s\n' "$line" | sed -E "s/.*(^| )$key=([^ ]*).*/\\2/"
    return 0
}

run_reset() {
    require_env NS PSQL_IMAGE DB_SECRET PG_HOST_KEY PG_PORT_KEY PG_DB_KEY PG_USER_KEY PG_PASSWORD_KEY PG_CA_CONFIGMAP PG_CA_KEY
    local ts
    ts="$(stamp)"
    JOB_NAME="perf-reset-$(dns_name "$ts" 40)"
    SQL_CONFIGMAP="perf-reset-sql-$(dns_name "$ts" 40)"
    NAMESPACE="$NS"
    PSQL_RUN_AS_USER="${PSQL_RUN_AS_USER:-70}"
    PSQL_EPHEMERAL_REQUEST="$(storage_quantity PSQL_EPHEMERAL_REQUEST 64Mi)"
    PSQL_EPHEMERAL_LIMIT="$(storage_quantity PSQL_EPHEMERAL_LIMIT 256Mi)"
    export JOB_NAME SQL_CONFIGMAP NAMESPACE PSQL_RUN_AS_USER PSQL_EPHEMERAL_REQUEST PSQL_EPHEMERAL_LIMIT
    if [[ -n "$opt_rows" ]]; then [[ "$opt_rows" =~ ^[0-9]+$ ]] || usage_err "--rows must be a whole number"; fi
    if [[ -n "$opt_watermark" ]]; then is_iso "$opt_watermark" || usage_err "--watermark must be ISO-8601 UTC, e.g. 2026-01-01T00:00:00Z"; fi

    local sql_cm="" job sql args
    if [[ -n "$opt_rows" && -n "$opt_watermark" ]]; then
        [[ -x "$RESET_SCRIPT" ]] || die "$RESET_SCRIPT is missing or not executable (it prints the reset SQL)"
        args=(--print-sql --rows "$opt_rows" --watermark "$opt_watermark")
        [[ $opt_clear_outbox -eq 0 ]] || args+=(--clear-outbox)
        [[ $opt_drop_seed -eq 0 ]] || args+=(--drop-seed)
        sql="$("$RESET_SCRIPT" "${args[@]}")" || die "$(basename "$RESET_SCRIPT") --print-sql failed"
        [[ -n "$sql" ]] || die "$(basename "$RESET_SCRIPT") --print-sql printed nothing"
        sql_cm="$(jq -cn --arg n "$SQL_CONFIGMAP" --arg ns "$NS" --arg sql "$sql" '{
            apiVersion: "v1", kind: "ConfigMap",
            metadata: { name: $n, namespace: $ns, labels: {
                "app.kubernetes.io/part-of": "k6-perf", "app.kubernetes.io/component": "reset" } },
            data: { "reset.sql": $sql } }')"
    fi
    job="$(render "$PSQL_TEMPLATE" "$PSQL_TEMPLATE_VARS")"

    if [[ $opt_render_only -eq 1 ]]; then
        # Without --rows/--watermark only the Job renders: it is what admission checks.
        [[ -z "$sql_cm" ]] || printf -- '---\n%s\n' "$sql_cm"
        printf -- '---\n%s\n' "$job"
        exit 0
    fi

    [[ -n "$opt_rows" ]] || usage_err "--rows is required"
    [[ -n "$opt_watermark" ]] || usage_err "--watermark is required"
    [[ -n "$opt_out" ]] || usage_err "--out is required"
    local base="${opt_base_rows:-${BASE_ROWS:-}}"
    [[ -n "$base" ]] || usage_err "--base-rows or BASE_ROWS is required (rows the migrations insert)"
    [[ "$base" =~ ^[0-9]+$ ]] || usage_err "base rows must be a whole number"
    require_env KCTX
    command -v kubectl >/dev/null 2>&1 || die "kubectl is required"
    mkdir -p "$opt_out"

    printf '%s\n' "$sql_cm" | kc apply -f - >&2 || die "kubectl apply of configmap/$SQL_CONFIGMAP failed"
    local log_file="$opt_out/reset-$ts.log"
    apply_and_wait "$job" "$JOB_NAME" psql $((PSQL_DEADLINE_S + WAIT_GRACE_S)) 0 "$log_file" \
        || die "job/$JOB_NAME ($JOB_STATE) left no log"
    [[ "$JOB_STATE" == Complete ]] || die "job/$JOB_NAME ended $JOB_STATE; see $log_file"

    local line products seed pending analyze want_seed="$opt_rows" fail=0
    line="$(grep -E '^COUNT products=[0-9]+ seed=[0-9]+ outbox_pending=[0-9]+ last_analyze=[^ ]+$' "$log_file" | tail -n 1 || true)"
    [[ -n "$line" ]] || die "no COUNT line in $log_file"
    echo "$line"
    products="$(count_field "$line" products)"
    seed="$(count_field "$line" seed)"
    pending="$(count_field "$line" outbox_pending)"
    analyze="$(count_field "$line" last_analyze)"
    [[ $opt_drop_seed -eq 0 ]] || want_seed=0
    if [[ "$products" -ne $((base + want_seed)) ]]; then
        log "reset gate: products=$products, expected $((base + want_seed)) ($base base + $want_seed seed)"; fail=1
    fi
    if [[ "$seed" -ne "$want_seed" ]]; then log "reset gate: seed=$seed, expected $want_seed"; fail=1; fi
    if [[ "$pending" -ne 0 ]]; then log "reset gate: outbox_pending=$pending, expected 0"; fail=1; fi
    [[ "$analyze" != null ]] || log "notice: last_analyze is null; wait for autovacuum to analyze before measuring"
    [[ $fail -eq 0 ]] || exit 3
    log "reset gate: OK"
    exit 0
}

# ---------------------------------------------------------------------------
# --kind idle
# ---------------------------------------------------------------------------

run_idle() {
    validate_run_identity
    [[ $opt_render_only -eq 0 ]] || usage_err "--kind idle has nothing to render"
    [[ -n "$opt_duration" ]] || usage_err "--duration is required"
    local secs
    secs="$(duration_s "$opt_duration")" || usage_err "--duration is not a duration: $opt_duration"
    [[ "$secs" -gt 0 ]] || usage_err "--duration must be > 0"
    [[ -n "$opt_out" ]] || usage_err "--out is required"
    mkdir -p "$opt_out/facts"
    opt_void_log="${opt_void_log:-$opt_out/void.log}"
    local run_id="$opt_label-idle-r$opt_rep" facts="$opt_out/facts/idle-r$opt_rep.env" start end
    archive_previous "$facts"
    if [[ -n "$opt_facts" ]]; then
        cp "$opt_facts" "$facts"
    else
        [[ -z "${APP_SELECTOR:-}" ]] || require_env KCTX NS
        app_pod_facts before >"$facts"
    fi
    start="$(now_iso)"
    log "idle window $run_id: ${secs}s from $start, no load"
    sleep "$secs"
    end="$(now_iso)"
    [[ -n "$opt_facts" ]] || app_pod_facts after >>"$facts"
    void_gate "$start" "$end"
    facts_gates "$facts" idle
    local reason valid=1
    reason="$(reasons)"
    [[ -z "$reason" ]] || valid=0
    append_window "$run_id" idle "$opt_rep" "" "$start" "$end" 0 "$valid" "$reason"
    if [[ $valid -eq 1 ]]; then log "idle $run_id: VALID"; exit 0; fi
    log "idle $run_id: VOID ($reason)"
    exit 3
}

# ---------------------------------------------------------------------------

if [[ -n "$opt_load_ref" ]]; then
    [[ -z "$opt_kind" ]] || usage_err "--load-configmaps takes no --kind"
    load_configmaps "$opt_load_ref"
fi
case "$opt_kind" in
    k6) run_k6 ;;
    reset) run_reset ;;
    idle) run_idle ;;
    "") usage_err "--kind is required (k6, reset or idle), or --load-configmaps" ;;
    *) usage_err "unknown --kind: $opt_kind" ;;
esac
