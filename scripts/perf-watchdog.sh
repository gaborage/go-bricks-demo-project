#!/usr/bin/env bash
# scripts/perf-watchdog.sh
#
# Session guard for the in-cluster perf harness. Two subcommands:
#
#   watch      samples the app, its node, the broker and the database every
#              --interval seconds, writes one CSV row per sample, and on an abort
#              rule kills the load Jobs and writes a VOID line.
#   wait-idle  the adaptive cooldown: waits until the app pod's CPU is back
#              within --margin millicores of its idle level.
#
# Read-only by construction. The only commands it runs are `kubectl get`,
# `kubectl top` and `aws cloudwatch get-metric-statistics`. Its only action is
# `kubectl delete job -l <--job-selector>` (with --wait=false), plus VOID lines in
# --void-log. Every parameter is a required flag: nothing is read from the
# environment, so a session's guard rails are all on its command line.
#
# It kills load Jobs only. Pass the load-generator selector,
#   --job-selector app.kubernetes.io/part-of=k6-perf,app.kubernetes.io/component=load-generator
# so a products reset Job (component=reset) is neither counted as running load
# nor deleted mid-reset. `watch` refuses to start (exit 2) unless one
# comma-separated term of --job-selector is exactly
# app.kubernetes.io/component=load-generator (`==` and surrounding spaces are
# accepted). part-of=k6-perf alone matches every perf Job and is for session
# cleanup, never for this flag.
#
# VOID line format (perf-k8s-run.sh voids any rep whose window contains one):
#   VOID <ISO-8601 UTC> rule=<rule> scope=<rep|session> action=<kill|stop> detail=<text>
#
# Abort rules enforced here (watch):
#   rule             trigger                                             scope    action
#   replicas         deployment spec or status replicas > 1              rep      kill
#   hpa              an HPA on the deployment desires/holds > 1          rep      kill
#   pod-restart      app pod restartCount went up                        rep      kill
#   pod-oom          ... and the last termination was OOMKilled          rep      kill
#   pod-notready     app pod Ready condition is False                    rep      kill
#   pod-gone         the --pod no longer exists (replaced)               session  kill
#   node-changed     app pod moved off --node, or the node is gone       session  kill
#   node-disruption  disruption/interruption taint or event on --node    session  kill
#   node-pressure    any *Pressure condition True on --node              rep      kill
#   node-cpu         node CPU above --node-cpu-pct on 2 samples in a row rep      kill
#   broker-mem       broker pod memory above --broker-mem-mi             rep      kill
#   acu-ceiling      Aurora ServerlessDatabaseCapacity >= --acu-ceiling  session  stop
#                    on --acu-sustain 1-minute datapoints in a row
#   session-end      wall clock past --session-end                       session  stop
# A rep-scope rule is re-evaluated each sample. A session-scope rule latches:
# from then on every Job that appears is killed (stop = the session is over;
# fix the cause and restart the watchdog). A VOID line is written when a rule
# starts firing, and again on every sample that finds a load Job running while
# it fires, so the line always falls inside the voided rep's window. A sample
# whose read failed fires nothing (logged as "?" in the CSV).
#
# acu-ceiling reads CloudWatch's 1-minute Maximum datapoints. It fires only when
# the --acu-sustain newest datapoints are that many consecutive minutes (60 s
# apart, ending at the newest) all at or above --acu-ceiling: Aurora Serverless
# v2 can report a one-minute capacity spike with no database load, and one such
# reading must not stop a session. A missing minute or a reading below the
# ceiling breaks the run, and fewer datapoints than --acu-sustain never fire.
# Each read covers max(5, --acu-sustain + 3) minutes, because CloudWatch publishes
# this metric 1-2 minutes late, so --acu-sustain is 1 to 1436: CloudWatch refuses
# a read of more than 1,440 one-minute datapoints, and an unaligned window holds
# --acu-sustain + 4 of them. A timestamp may be
# ISO-8601 with any UTC offset (the AWS CLI's default prints the local zone) or
# epoch seconds (cli_timestamp_format = wire); one that is neither makes the
# whole read count as failed, with a notice on stderr, because the newest
# datapoint is then unknown. The CSV's acu column is the newest datapoint, and
# acu_run the length of the run that ends at it.
#
# Abort criteria owned by other tools (none is visible through the commands
# above): k6 429 / error-rate / p95 aborts (k6 thresholds), dropped iterations
# and achieved rate (perf-k8s-run.sh gates), other-tenant database load
# (Performance Insights capture), queue growth (broker management API),
# New Relic dropped data points and collector memory (perf-nrql.sh Q17a/Q17b),
# and fingerprint mismatches (pre-session checks).
#
# --simulate <dir> replays fixtures instead of calling kubectl or aws, on a
# virtual clock (no sleeping), and prints "KILL ..." instead of deleting:
#   watch:     <dir>/base.json is a sample; <dir>/samples.jsonl holds one JSON
#              merge patch per line (jq `*`), applied to base in turn. A sample
#              has keys now, deploy, hpa, pod, node, events, top_node,
#              top_broker, acu, jobs holding the raw `kubectl get -o json` /
#              `kubectl top --no-headers` / CloudWatch output (acu.Datapoints
#              is replaced whole, so each line carries the full set); null means
#              NotFound and {"__error": "..."} means the read failed.
#   wait-idle: <dir>/top.txt has one `kubectl top pod --no-headers` line per
#              sample, or "ERROR <message>" for a failed read.
#
# Exit codes: watch 0 (stopped with Ctrl-C, or simulation done), 2 usage,
# 1 error. wait-idle 0 idle (or the fixed fallback wait finished), 1 not idle
# by --max, 2 usage.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=lib/perf-common.sh
source "$SCRIPT_DIR/lib/perf-common.sh"

log() {
    echo "perf-watchdog: $*" >&2
    return 0
}
die() { log "error: $*"; exit 1; }
usage_err() { log "$*"; log "run with --help for usage"; exit 2; }

usage() {
    cat <<'EOF'
Usage:
  perf-watchdog.sh watch --context CTX --namespace NS --deployment DEPLOY --pod POD --node NODE
      --job-selector SEL --aurora-cluster ID --acu-ceiling ACU --acu-sustain MINUTES
      --broker-namespace NS --broker-pod POD --broker-mem-mi MI --node-cpu-pct PCT
      --interval SECONDS --session-end ISO --out CSV --void-log FILE [--simulate DIR]

  perf-watchdog.sh wait-idle --context CTX --namespace NS --pod POD --idle-millicores M
      --margin MILLICORES --samples N --min DUR --max DUR [--simulate DIR]

wait-idle samples every 15s and returns once DUR --min has passed and N
consecutive samples were at or below idle + margin millicores. Without
metrics-server it logs a notice, waits a fixed 5 minutes and returns 0. A pod
that does not exist is an error (exit 1), not a reason to wait.
EOF
    return 0
}

# --------------------------------------------------------------------------
# Clock: real, or virtual under --simulate
# --------------------------------------------------------------------------

SIM_DIR="" VCLOCK=""
clock_now() {
    if [[ -n "$SIM_DIR" ]]; then echo "$VCLOCK"; else date +%s; fi
    return 0
}
clock_sleep() { # SECONDS
    local seconds="$1"
    if [[ -n "$SIM_DIR" ]]; then VCLOCK=$((VCLOCK + seconds)); else sleep "$seconds"; fi
    return 0
}

need_value() { # FLAG VALUE
    local flag="${1-}" value="${2-}"
    [[ -n "$value" ]] || usage_err "$flag needs a value"
    return 0
}

# ==========================================================================
# wait-idle
# ==========================================================================

IDLE_INTERVAL_S=15
FALLBACK_WAIT_S=300

cmd_wait_idle() {
    local ctx="" ns="" pod="" idle="" margin="" samples="" min="" max=""
    while [[ $# -gt 0 ]]; do
        local arg="$1" val="${2-}"
        case "$arg" in
            -h|--help) usage; exit 0 ;;
            --context) need_value "$arg" "$val"; ctx="$val"; shift 2 ;;
            --namespace) need_value "$arg" "$val"; ns="$val"; shift 2 ;;
            --pod) need_value "$arg" "$val"; pod="$val"; shift 2 ;;
            --idle-millicores) need_value "$arg" "$val"; idle="$val"; shift 2 ;;
            --margin) need_value "$arg" "$val"; margin="$val"; shift 2 ;;
            --samples) need_value "$arg" "$val"; samples="$val"; shift 2 ;;
            --min) need_value "$arg" "$val"; min="$val"; shift 2 ;;
            --max) need_value "$arg" "$val"; max="$val"; shift 2 ;;
            --simulate) need_value "$arg" "$val"; SIM_DIR="$val"; shift 2 ;;
            *) usage_err "wait-idle: unknown argument: $arg" ;;
        esac
    done
    local f
    for f in ctx ns pod idle margin samples min max; do
        [[ -n "${!f}" ]] || usage_err "wait-idle: every flag is required (missing ${f})"
    done
    [[ "$idle" =~ ^[0-9]+$ ]] || usage_err "--idle-millicores must be a whole number"
    [[ "$margin" =~ ^[0-9]+$ ]] || usage_err "--margin must be a whole number of millicores"
    [[ "$samples" =~ ^[1-9][0-9]*$ ]] || usage_err "--samples must be >= 1"
    local min_s max_s
    min_s="$(duration_s "$min")" || usage_err "--min is not a duration: $min"
    max_s="$(duration_s "$max")" || usage_err "--max is not a duration: $max"
    ((max_s >= min_s)) || usage_err "--max must be >= --min"

    local lines=() line
    if [[ -n "$SIM_DIR" ]]; then
        [[ -f "$SIM_DIR/top.txt" ]] || usage_err "--simulate needs $SIM_DIR/top.txt"
        while IFS= read -r line || [[ -n "$line" ]]; do lines+=("$line"); done <"$SIM_DIR/top.txt"
        VCLOCK=1767225600   # 2026-01-01T00:00:00Z
    fi

    local threshold=$((idle + margin)) start now elapsed taken=0 streak=0 last="?" out err m errf
    start="$(clock_now)"
    errf="$(mktemp "${TMPDIR:-/tmp}/perf-watchdog-top.XXXXXX")"
    report() { # RESULT
        local result="$1"
        now="$(clock_now)"
        echo "COOLDOWN pod=$pod start=$(epoch_to_iso "$start") end=$(epoch_to_iso "$now") waited_s=$((now - start)) result=$result threshold_m=$threshold last_m=$last samples=$taken"
        return 0
    }
    while :; do
        err=""
        if [[ -n "$SIM_DIR" ]]; then
            [[ $taken -lt ${#lines[@]} ]] || die "simulation ran out of samples after $taken"
            out="${lines[$taken]}"
            if [[ "$out" == ERROR* ]]; then err="${out#ERROR }"; out=""; fi
        elif ! out="$(kubectl --context "$ctx" --namespace "$ns" top pod "$pod" --no-headers 2>"$errf")"; then
            out=""; err="$(head -c 300 "$errf")"; err="${err:-kubectl top pod failed}"
        fi
        taken=$((taken + 1))
        m="$(printf '%s\n' "$out" | awk -v col=2 'NF >= col { c = $col; if (c ~ /m$/) { sub(/m$/, "", c); printf "%d", c } else if (c ~ /^[0-9.]+$/) printf "%d", c * 1000; exit }')"
        if [[ -z "$m" ]]; then
            case "$err" in
                *"Metrics API not available"*|*metrics.k8s.io*)
                    if [[ $taken -eq 1 ]]; then
                        rm -f "$errf"
                        echo "NOTICE metrics-server unavailable ($err); falling back to a fixed $((FALLBACK_WAIT_S / 60))-minute wait"
                        log "metrics-server unavailable; waiting a fixed $((FALLBACK_WAIT_S / 60)) minutes"
                        clock_sleep "$FALLBACK_WAIT_S"
                        report fallback-fixed-wait
                        return 0
                    fi ;;
                *NotFound*|*"not found"*)
                    rm -f "$errf"
                    log "error: pod $pod not found: $err"
                    report error
                    return 1 ;;
                *) ;;   # any other failure counts as a sample that is not idle
            esac
            streak=0
            log "notice: kubectl top pod failed (${err:-no CPU column}); counting the sample as not idle"
        else
            last="$m"
            if ((m <= threshold)); then streak=$((streak + 1)); else streak=0; fi
        fi
        now="$(clock_now)"; elapsed=$((now - start))
        if ((elapsed >= min_s && streak >= samples)); then rm -f "$errf"; report idle; return 0; fi
        if ((elapsed >= max_s)); then rm -f "$errf"; report timeout; return 1; fi
        clock_sleep "$IDLE_INTERVAL_S"
    done
    return 1
}

# ==========================================================================
# watch
# ==========================================================================

# Reasons Karpenter and the spot interruption handlers give events on a node
# that is about to go. DisruptionBlocked is deliberately absent: it is the
# do-not-disrupt annotation doing its job.
DISRUPTION_EVENTS='^(SpotInterrupted|SpotRebalanceRecommendation|InstanceTerminating|InstanceStopping|InstanceStateChange|TerminatingOnInterruption|RebootScheduled|Disruption(Launching|Terminating|WaitingReadiness)|Disrupted)$'
DISRUPTION_TAINTS='^(karpenter\.sh/disrupt|aws-node-termination-handler/|node\.kubernetes\.io/unschedulable$)'

# Reduce a raw sample to one TSV line of features. "?" marks an unreadable value.
# shellcheck disable=SC2016 # jq program
FEATURES_JQ='
def bad: type == "object" and has("__error");
def ok: . != null and (bad | not);
def q: if . == null then "?" elif . == "" then "-" else tostring end;
# ISO-8601 or epoch seconds -> whole epoch seconds, or null. Takes Z or a numeric
# UTC offset: the AWS CLI can print CloudWatch timestamps in the local zone
# (e.g. -05:00), and prints epoch seconds under cli_timestamp_format = wire.
def epoch: if type == "number" then floor
    elif type == "string" then
    (first(try (capture("^(?<b>[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2})(\\.[0-9]+)?(?<z>Z|(?<sg>[+-])(?<oh>[0-9]{2}):?(?<om>[0-9]{2}))$")
        | (.b + "Z" | fromdateiso8601)
          - (if .z == "Z" then 0 else (if .sg == "+" then 1 else -1 end) * ((.oh | tonumber) * 3600 + (.om | tonumber) * 60) end)) catch null) // null)
    else null end;
def mem_mi: if test("Ki$") then (sub("Ki$"; "") | tonumber / 1024)
            elif test("Mi$") then (sub("Mi$"; "") | tonumber)
            elif test("Gi$") then (sub("Gi$"; "") | tonumber * 1024)
            elif test("^[0-9]+$") then (tonumber / 1048576) else null end;
# Number of CloudWatch datapoints whose timestamp does not parse.
def acu_unparsed: [.Datapoints // [] | .[] | select((.Timestamp | epoch) == null)] | length;
# CloudWatch datapoints as {t: epoch, m: Maximum}, newest first, or null when a
# timestamp does not parse: the order is then unknown, so the read counts as failed.
def acu_points: if acu_unparsed > 0 then null
    else [.Datapoints // [] | .[] | {t: (.Timestamp | epoch), m: .Maximum}] | sort_by(.t) | reverse end;
# Length of the run of datapoints at or above $ceil that ends at the newest one,
# each 60 s after the one before it. A gap or a lower reading ends the run.
def acu_run: ($ceil | tonumber) as $c
    | acu_points
    | if . == null then null else
        reduce .[] as $d ({n: 0, prev: null, open: true};
            if .open and $d.t != null and ($d.m | type) == "number" and $d.m >= $c
                and (.prev == null or .prev - $d.t == 60)
            then .n += 1 | .prev = $d.t else .open = false end)
        | .n end;
. as $s
| [ ($s.now | q),
    (if ($s.deploy | ok) then ([$s.deploy.spec.replicas // 0, $s.deploy.status.replicas // 0] | max) else null end | q),
    (if ($s.hpa | ok) then ([$s.hpa.items[]? | select(.spec.scaleTargetRef.kind == "Deployment" and .spec.scaleTargetRef.name == $dep)
        | [.status.desiredReplicas // 0, .status.currentReplicas // 0] | max] | max // 0) else null end | q),
    (if $s.pod == null then "gone" elif ($s.pod | ok) then "present" else null end | q),
    (if ($s.pod | ok) then ([$s.pod.status.conditions[]? | select(.type == "Ready") | .status] | first // "Unknown") else null end | q),
    (if ($s.pod | ok) then ([$s.pod.status.containerStatuses[]?.restartCount] | add // 0) else null end | q),
    (if ($s.pod | ok) then ($s.pod.spec.nodeName // "") else null end | q),
    (if ($s.pod | ok) then ([$s.pod.status.containerStatuses[]?.lastState.terminated.reason // empty] | first // "-") else null end | q),
    (if $s.node == null then "gone" elif ($s.node | ok) then "present" else null end | q),
    (if ($s.node | ok) then ([$s.node.status.conditions[]? | select((.type | test("Pressure$")) and .status == "True") | .type] | join(",") | if . == "" then "-" else . end) else null end | q),
    (if ($s.node | ok) then ([$s.node.spec.taints[]? | .key | select(test($taints))]
        + (if $s.node.spec.unschedulable == true then ["unschedulable"] else [] end)
        + (if ($s.events | ok) then [$s.events.items[]? | select((.reason // "") | test($events))
            | select(((.lastTimestamp // .eventTime // .metadata.creationTimestamp) | epoch // 0) >= $since) | .reason] else [] end)
        | unique | join(",") | if . == "" then "-" else . end) else null end | q),
    (if ($s.top_node | type) == "string" then ($s.top_node | split(" ") | map(select(. != "")) | .[2] // ""
        | if test("^[0-9]+%$") then sub("%$"; "") else null end) else null end | q),
    (if ($s.top_broker | type) == "string" then ($s.top_broker | split(" ") | map(select(. != "")) | .[2] // "" | mem_mi
        | if . == null then null else floor end) else null end | q),
    (if ($s.acu | ok) then ($s.acu | acu_points | if . == null then null else (first | .m // null) end) else null end | q),
    (if ($s.jobs | ok) then [$s.jobs.items[]? | select(any(.status.conditions[]?; (.type == "Complete" or .type == "Failed") and .status == "True") | not)] | length else null end | q),
    (if ($s.acu | ok) then ($s.acu | acu_run) else null end | q),
    (if ($s.acu | ok) then ($s.acu | acu_unparsed) else 0 end | q)
  ] | @tsv'

W_CTX="" W_NS="" W_DEPLOY="" W_POD="" W_NODE="" W_SEL="" W_CLUSTER="" W_ACU_CEIL="" W_ACU_SUSTAIN="" W_BNS=""
W_BPOD="" W_BMEM="" W_CPU_PCT="" W_INTERVAL="" W_END="" W_OUT="" W_VOID="" W_ACU_WINDOW_S=""

kc() {
    kubectl --context "$W_CTX" "$@"
    return $?
}

# fetch_json FILE CMD... -> FILE holds the JSON output, null on NotFound, or {"__error": ...}
fetch_json() {
    local file="$1" err
    shift
    err="$(mktemp "${TMPDIR:-/tmp}/perf-watchdog-err.XXXXXX")"
    if "$@" >"$file" 2>"$err" && jq -e . "$file" >/dev/null 2>&1; then :
    elif grep -q NotFound "$err"; then echo null >"$file"
    else jq -cn --arg e "$(head -c 200 "$err")" '{__error: $e}' >"$file"; fi
    rm -f "$err"
    return 0
}

# fetch_text FILE CMD... -> FILE holds a JSON string of the output, or {"__error": ...}
fetch_text() {
    local file="$1" out
    shift
    if out="$("$@" 2>/dev/null)"; then jq -cn --arg s "$out" '$s' >"$file"
    else echo '{"__error":"command failed"}' >"$file"; fi
    return 0
}

# collect_live NOW_ISO -> raw sample JSON on stdout
collect_live() { # NOW_ISO
    local now="$1" d f
    d="$(mktemp -d "${TMPDIR:-/tmp}/perf-watchdog.XXXXXX")"
    fetch_json "$d/deploy" kc --namespace "$W_NS" get deployment "$W_DEPLOY" -o json
    fetch_json "$d/hpa" kc --namespace "$W_NS" get hpa -o json
    fetch_json "$d/pod" kc --namespace "$W_NS" get pod "$W_POD" -o json
    fetch_json "$d/node" kc get node "$W_NODE" -o json
    fetch_json "$d/events" kc get events --all-namespaces \
        --field-selector "involvedObject.kind=Node,involvedObject.name=$W_NODE" -o json
    fetch_text "$d/top_node" kc top node "$W_NODE" --no-headers
    fetch_text "$d/top_broker" kc --namespace "$W_BNS" top pod "$W_BPOD" --no-headers
    fetch_json "$d/acu" aws cloudwatch get-metric-statistics --namespace AWS/RDS \
        --metric-name ServerlessDatabaseCapacity --dimensions "Name=DBClusterIdentifier,Value=$W_CLUSTER" \
        --start-time "$(epoch_to_iso $(($(iso_to_epoch "$now") - W_ACU_WINDOW_S)))" --end-time "$now" \
        --period 60 --statistics Maximum --output json
    fetch_json "$d/jobs" kc --namespace "$W_NS" get jobs -l "$W_SEL" -o json
    for f in deploy hpa pod node events top_node top_broker acu jobs; do
        [[ -s "$d/$f" ]] || echo '{"__error":"empty"}' >"$d/$f"
    done
    jq -cn --arg now "$now" \
        --slurpfile deploy "$d/deploy" --slurpfile hpa "$d/hpa" --slurpfile pod "$d/pod" \
        --slurpfile node "$d/node" --slurpfile events "$d/events" --slurpfile top_node "$d/top_node" \
        --slurpfile top_broker "$d/top_broker" --slurpfile acu "$d/acu" --slurpfile jobs "$d/jobs" \
        '{now: $now, deploy: $deploy[0], hpa: $hpa[0], pod: $pod[0], node: $node[0], events: $events[0],
          top_node: $top_node[0], top_broker: $top_broker[0], acu: $acu[0], jobs: $jobs[0]}'
    rm -rf "$d"
    return 0
}

kill_jobs() { # NOW_ISO REASONS
    local now="$1" rules="$2"
    if [[ -n "$SIM_DIR" ]]; then
        echo "KILL $now kubectl --context $W_CTX --namespace $W_NS delete job -l $W_SEL --wait=false (simulated; rules: $rules)"
        return 0
    fi
    log "killing load Jobs ($rules): kubectl delete job -l $W_SEL"
    kc --namespace "$W_NS" delete job -l "$W_SEL" --wait=false >&2 || log "warning: kubectl delete job failed"
    return 0
}

void_line() { # NOW_ISO RULE SCOPE ACTION DETAIL
    local now="$1" rule="$2" scope="$3" action="$4" detail="$5"
    local line="VOID $now rule=$rule scope=$scope action=$action detail=$detail"
    echo "$line" >>"$W_VOID"
    log "$line"
    [[ -z "$SIM_DIR" ]] || echo "$line"
    return 0
}

cmd_watch() {
    while [[ $# -gt 0 ]]; do
        local arg="$1" val="${2-}"
        case "$arg" in
            -h|--help) usage; exit 0 ;;
            --context) need_value "$arg" "$val"; W_CTX="$val"; shift 2 ;;
            --namespace) need_value "$arg" "$val"; W_NS="$val"; shift 2 ;;
            --deployment) need_value "$arg" "$val"; W_DEPLOY="$val"; shift 2 ;;
            --pod) need_value "$arg" "$val"; W_POD="$val"; shift 2 ;;
            --node) need_value "$arg" "$val"; W_NODE="$val"; shift 2 ;;
            --job-selector) need_value "$arg" "$val"; W_SEL="$val"; shift 2 ;;
            --aurora-cluster) need_value "$arg" "$val"; W_CLUSTER="$val"; shift 2 ;;
            --acu-ceiling) need_value "$arg" "$val"; W_ACU_CEIL="$val"; shift 2 ;;
            --acu-sustain) need_value "$arg" "$val"; W_ACU_SUSTAIN="$val"; shift 2 ;;
            --broker-namespace) need_value "$arg" "$val"; W_BNS="$val"; shift 2 ;;
            --broker-pod) need_value "$arg" "$val"; W_BPOD="$val"; shift 2 ;;
            --broker-mem-mi) need_value "$arg" "$val"; W_BMEM="$val"; shift 2 ;;
            --node-cpu-pct) need_value "$arg" "$val"; W_CPU_PCT="$val"; shift 2 ;;
            --interval) need_value "$arg" "$val"; W_INTERVAL="$val"; shift 2 ;;
            --session-end) need_value "$arg" "$val"; W_END="$val"; shift 2 ;;
            --out) need_value "$arg" "$val"; W_OUT="$val"; shift 2 ;;
            --void-log) need_value "$arg" "$val"; W_VOID="$val"; shift 2 ;;
            --simulate) need_value "$arg" "$val"; SIM_DIR="$val"; shift 2 ;;
            *) usage_err "watch: unknown argument: $arg" ;;
        esac
    done
    local f flag
    for f in W_CTX:context W_NS:namespace W_DEPLOY:deployment W_POD:pod W_NODE:node W_SEL:job-selector \
        W_CLUSTER:aurora-cluster W_ACU_CEIL:acu-ceiling W_ACU_SUSTAIN:acu-sustain W_BNS:broker-namespace W_BPOD:broker-pod \
        W_BMEM:broker-mem-mi W_CPU_PCT:node-cpu-pct W_INTERVAL:interval W_END:session-end W_OUT:out \
        W_VOID:void-log; do
        flag="${f#*:}"; f="${f%%:*}"
        [[ -n "${!f}" ]] || usage_err "watch: every flag is required (missing --$flag)"
    done
    [[ "$W_SEL" == *=* ]] || usage_err "--job-selector must be a label selector (key=value)"
    has_load_term "$W_SEL" \
        || usage_err "--job-selector must contain the term app.kubernetes.io/component=load-generator, so only load Jobs are counted and killed (e.g. app.kubernetes.io/part-of=k6-perf,app.kubernetes.io/component=load-generator)"
    [[ "$W_ACU_CEIL" =~ ^[0-9]+(\.[0-9]+)?$ ]] || usage_err "--acu-ceiling must be a number"
    # At most 1436: an unaligned read of N + 3 minutes holds N + 4 one-minute
    # datapoints, and CloudWatch refuses a read of more than 1,440.
    if ! [[ "$W_ACU_SUSTAIN" =~ ^[1-9][0-9]{0,3}$ ]] || ((W_ACU_SUSTAIN > 1436)); then
        usage_err "--acu-sustain must be a whole number of minutes from 1 to 1436"
    fi
    # The read must hold the run plus three minutes, because CloudWatch publishes
    # this metric 1-2 minutes late, and never less than 5 minutes.
    W_ACU_WINDOW_S=$(((W_ACU_SUSTAIN + 3) * 60))
    ((W_ACU_WINDOW_S >= 300)) || W_ACU_WINDOW_S=300
    [[ "$W_BMEM" =~ ^[0-9]+$ ]] || usage_err "--broker-mem-mi must be a whole number"
    [[ "$W_CPU_PCT" =~ ^[0-9]+$ ]] || usage_err "--node-cpu-pct must be a whole number"
    [[ "$W_INTERVAL" =~ ^[1-9][0-9]*$ ]] || usage_err "--interval must be whole seconds >= 1"
    is_iso "$W_END" || usage_err "--session-end must be ISO-8601 UTC, e.g. 2026-10-14T23:30:00Z"
    local end_epoch
    end_epoch="$(iso_to_epoch "$W_END")"

    local base="" patches=()
    if [[ -n "$SIM_DIR" ]]; then
        [[ -f "$SIM_DIR/base.json" && -f "$SIM_DIR/samples.jsonl" ]] || usage_err "--simulate needs base.json and samples.jsonl in $SIM_DIR"
        base="$(jq -c . "$SIM_DIR/base.json")" || die "$SIM_DIR/base.json is not JSON"
        local line
        while IFS= read -r line || [[ -n "$line" ]]; do [[ -z "$line" ]] || patches+=("$line"); done <"$SIM_DIR/samples.jsonl"
        VCLOCK="$(iso_to_epoch "$(printf '%s' "$base" | jq -r .now)")"
        [[ -n "$VCLOCK" ]] || die "base.json needs now (ISO-8601 UTC)"
    else
        command -v kubectl >/dev/null 2>&1 || die "kubectl is required"
        command -v aws >/dev/null 2>&1 || die "the aws CLI is required (ACU reads)"
    fi
    mkdir -p "$(dirname "$W_OUT")" "$(dirname "$W_VOID")"
    touch "$W_VOID"
    [[ -s "$W_OUT" ]] || echo "ts,replicas,hpa_desired,pod,pod_ready,pod_restarts,pod_node,node,node_cpu_pct,node_pressure,node_disruption,broker_mem_mi,acu,active_jobs,violations,killed,acu_run" >"$W_OUT"

    local since
    since="$(clock_now)"
    log "watching $W_NS/$W_DEPLOY pod $W_POD on $W_NODE every ${W_INTERVAL}s until $W_END (jobs: $W_SEL)"
    trap 'log "stopped"; exit 0' INT TERM

    # Rule state: latched session rules (names + entries), the previous
    # sample's firing rules, the restart baseline and the node CPU streak.
    local latched_names=" " latched=() prev=" " baseline_restarts="" cpu_streak=0 i=0
    local raw now_iso replicas hpa pod pod_ready restarts pod_node last_reason node pressure disruption
    local cpu_pct broker_mi acu jobs acu_run acu_unparsed minutes entry rule scope action detail names reason_list jobs_running v
    while :; do
        if [[ -n "$SIM_DIR" ]]; then
            [[ $i -lt ${#patches[@]} ]] || { log "simulation done after $i samples"; return 0; }
            raw="$(jq -cn --argjson b "$base" --argjson p "${patches[$i]}" --arg now "$(epoch_to_iso "$VCLOCK")" \
                '($b | .now = $now) * $p')" || die "samples.jsonl line $((i + 1)) is not JSON"
        else
            raw="$(collect_live "$(now_iso)")"
        fi
        i=$((i + 1))
        IFS=$'\t' read -r now_iso replicas hpa pod pod_ready restarts pod_node last_reason node pressure disruption \
            cpu_pct broker_mi acu jobs acu_run acu_unparsed < <(printf '%s' "$raw" | jq -r --arg dep "$W_DEPLOY" --argjson since "$since" \
                --arg taints "$DISRUPTION_TAINTS" --arg events "$DISRUPTION_EVENTS" --arg ceil "$W_ACU_CEIL" "$FEATURES_JQ" || true)
        for v in replicas hpa pod pod_ready restarts pod_node last_reason node pressure disruption cpu_pct broker_mi acu jobs acu_run acu_unparsed; do
            [[ -n "${!v}" ]] || printf -v "$v" '%s' "?"
        done
        is_iso "$now_iso" || now_iso="$(now_iso)"

        # Each firing rule adds "rule|scope|action|detail".
        FIRED=()
        [[ "$replicas" == "?" ]] || ((replicas <= 1)) || fire replicas rep kill "deployment replicas=$replicas"
        [[ "$hpa" == "?" ]] || ((hpa <= 1)) || fire hpa rep kill "hpa desired/current=$hpa"
        if [[ "$pod" == gone ]]; then
            fire pod-gone session kill "pod $W_POD not found"
        elif [[ "$pod" == present ]]; then
            if [[ -z "$baseline_restarts" ]]; then
                baseline_restarts="$restarts"
            elif ((restarts > baseline_restarts)); then
                if [[ "$last_reason" == OOMKilled ]]; then
                    fire pod-oom rep kill "restartCount $baseline_restarts->$restarts, last termination OOMKilled"
                else
                    fire pod-restart rep kill "restartCount $baseline_restarts->$restarts, last termination $last_reason"
                fi
                baseline_restarts="$restarts"
            fi
            [[ "$pod_ready" != False ]] || fire pod-notready rep kill "pod $W_POD Ready=False"
            [[ "$pod_node" == "-" || "$pod_node" == "$W_NODE" ]] \
                || fire node-changed session kill "pod $W_POD is on $pod_node, expected $W_NODE"
        fi
        if [[ "$node" == gone ]]; then
            fire node-changed session kill "node $W_NODE not found"
        elif [[ "$node" == present ]]; then
            [[ "$disruption" == "-" ]] || fire node-disruption session kill "node $W_NODE: $disruption"
            [[ "$pressure" == "-" ]] || fire node-pressure rep kill "node $W_NODE: $pressure"
        fi
        if [[ "$cpu_pct" != "?" ]]; then
            if ((cpu_pct > W_CPU_PCT)); then cpu_streak=$((cpu_streak + 1)); else cpu_streak=0; fi
        fi
        ((cpu_streak < 2)) || fire node-cpu rep kill "node CPU ${cpu_pct}% > ${W_CPU_PCT}% on $cpu_streak samples in a row"
        [[ "$broker_mi" == "?" ]] || ((broker_mi <= W_BMEM)) \
            || fire broker-mem rep kill "broker $W_BNS/$W_BPOD memory ${broker_mi}Mi > ${W_BMEM}Mi"
        [[ "$acu_unparsed" == "?" || "$acu_unparsed" == 0 ]] \
            || log "notice: $acu_unparsed CloudWatch datapoint timestamp(s) are neither ISO-8601 nor epoch seconds; the ACU read counts as failed (\"?\") and acu-ceiling cannot fire on this sample"
        minutes=minutes
        [[ "$acu_run" != 1 ]] || minutes=minute
        [[ "$acu" == "?" || "$acu_run" == "?" ]] || ((acu_run < W_ACU_SUSTAIN)) \
            || fire acu-ceiling session stop "Aurora ACU >= ceiling $W_ACU_CEIL for $acu_run consecutive $minutes (latest $acu); stop and re-plan"
        (($(iso_to_epoch "$now_iso") < end_epoch)) || fire session-end session stop "past --session-end $W_END"

        # A latched session rule keeps firing after its trigger clears.
        names=" "
        for entry in ${FIRED[@]+"${FIRED[@]}"}; do names="$names${entry%%|*} "; done
        for entry in ${latched[@]+"${latched[@]}"}; do
            [[ "$names" == *" ${entry%%|*} "* ]] || { FIRED+=("$entry"); names="$names${entry%%|*} "; }
        done

        # Unknown job state counts as running: a kill with nothing to kill is a no-op.
        jobs_running=0
        [[ "$jobs" != "?" ]] && ((jobs == 0)) || jobs_running=1
        reason_list=""
        for entry in ${FIRED[@]+"${FIRED[@]}"}; do
            IFS='|' read -r rule scope action detail <<<"$entry"
            reason_list="${reason_list:+$reason_list,}$rule"
            if [[ "$scope" == session && "$latched_names" != *" $rule "* ]]; then
                latched+=("$entry"); latched_names="$latched_names$rule "
            fi
            # VOID when the rule starts firing, and on every sample that sees a Job.
            if [[ "$prev" != *" $rule "* ]] || [[ "$jobs" != "?" && "$jobs" -gt 0 ]]; then
                void_line "$now_iso" "$rule" "$scope" "$action" "$detail"
            fi
        done
        local killed=0
        if [[ -n "$reason_list" && $jobs_running -eq 1 ]]; then kill_jobs "$now_iso" "$reason_list"; killed=1; fi
        prev="$names"

        local viol="${reason_list//,/|}"
        echo "$now_iso,$replicas,$hpa,$pod,$pod_ready,$restarts,$pod_node,$node,$cpu_pct,${pressure//,/|},${disruption//,/|},$broker_mi,$acu,$jobs,${viol:--},$killed,$acu_run" >>"$W_OUT"
        clock_sleep "$W_INTERVAL"
    done
    return 0
}

# has_load_term SELECTOR -> 0 when one comma-separated term is the equality
# app.kubernetes.io/component=load-generator ("==" and surrounding spaces allowed).
# A lookalike key or value, or a set-based or != term, does not count.
has_load_term() {
    local selector="$1" term key value
    while IFS= read -r term; do
        if [[ "$term" == *==* ]]; then key="${term%%==*}"; value="${term#*==}"
        elif [[ "$term" == *=* ]]; then key="${term%%=*}"; value="${term#*=}"
        else continue; fi
        key="${key#"${key%%[![:space:]]*}"}"; key="${key%"${key##*[![:space:]]}"}"
        value="${value#"${value%%[![:space:]]*}"}"; value="${value%"${value##*[![:space:]]}"}"
        if [[ "$key" == "app.kubernetes.io/component" && "$value" == "load-generator" ]]; then return 0; fi
    done < <(printf '%s\n' "$selector" | tr ',' '\n')
    return 1
}

FIRED=()
fire() { # RULE SCOPE ACTION DETAIL
    local rule="$1" scope="$2" action="$3" detail="$4"
    FIRED+=("$rule|$scope|$action|$detail")
    return 0
}

# ==========================================================================

[[ $# -gt 0 ]] || { usage; exit 2; }
command -v jq >/dev/null 2>&1 || die "jq is required"
sub="$1"
shift
case "$sub" in
    watch) cmd_watch "$@" ;;
    wait-idle) cmd_wait_idle "$@" ;;
    -h|--help) usage ;;
    *) usage_err "unknown subcommand: $sub (watch or wait-idle)" ;;
esac
