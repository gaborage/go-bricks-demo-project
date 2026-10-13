#!/usr/bin/env bash
# scripts/test-perf-harness.sh
#
# Offline tests for the in-cluster perf harness: perf-k8s-run.sh,
# perf-watchdog.sh, perf-nrql.sh, the Job templates, the NRQL query set and the
# name tags that keep the products load scripts' metric series bounded.
# Nothing here reaches a cluster, AWS or New Relic:
#   - KUBECONFIG=/dev/null and the AWS config files point nowhere;
#   - kubectl, curl, security and the reset SQL generator are fakes from
#     scripts/testdata/perf/bin-fake, and aws is a tripwire that fails the run;
#   - the watchdog runs under --simulate with kubectl and aws both tripwires.
#
# Also runs the local gates CI does not: shellcheck on the harness scripts,
# `git check-ignore` on every harness file, a scan for environment-specific
# names, and (when ruby is installed) a YAML parse of every rendered manifest
# plus a cross-check of the query parser against a real YAML parser.
#
# Usage: scripts/test-perf-harness.sh            (make perf-harness-test)
#   PERF_TEST_BASH=/bin/bash  run the harness scripts under another bash (3.2 check)
#   PERF_TEST_KUBECONFORM=1   also validate the rendered manifests with kubeconform
#                             (downloads the tool and the Kubernetes schemas)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
TD="$SCRIPT_DIR/testdata/perf"
cd "$REPO_ROOT"

TEST_BASH="${PERF_TEST_BASH:-bash}"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/perf-harness-test.XXXXXX")"
trap 'rm -rf "$WORK"' EXIT

export KUBECONFIG=/dev/null AWS_CONFIG_FILE=/dev/null AWS_SHARED_CREDENTIALS_FILE=/dev/null
unset AWS_PROFILE AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN NEW_RELIC_ACCOUNT_ID PERF_NRQL_VARS || true
FAKE_PATH="$TD/bin-fake:$TD/bin-forbidden:$PATH"
FORBIDDEN_PATH="$TD/bin-forbidden:$PATH"
# The watchdog's selector: load Jobs only, never the reset Job (component=reset).
LOAD_SEL="app.kubernetes.io/part-of=k6-perf,app.kubernetes.io/component=load-generator"
ENV_EXAMPLE="$REPO_ROOT/loadtests/k8s/.env.example"
HAVE_RUBY=0
command -v ruby >/dev/null 2>&1 && HAVE_RUBY=1

# Refuse to start unless every external tool resolves to a fake or a tripwire.
for tool in kubectl curl security perf-reset-products.sh aws; do
    want="$TD/bin-fake/$tool"
    [[ "$tool" != aws ]] || want="$TD/bin-forbidden/aws"
    got="$(PATH="$FAKE_PATH" command -v "$tool" || true)"
    if [[ "$got" != "$want" || ! -x "$want" ]]; then
        echo "test-perf-harness: $tool resolves to '${got:-nothing}', not the executable stub $want; refusing to run" >&2
        exit 1
    fi
done
got="$(PATH="$FORBIDDEN_PATH" command -v kubectl || true)"
[[ "$got" == "$TD/bin-forbidden/kubectl" ]] || { echo "test-perf-harness: the kubectl tripwire is not first on PATH" >&2; exit 1; }

PASS=0 FAILED=0 SKIPPED=0
ok() {
    PASS=$((PASS + 1)); echo "ok    $*"
    return 0
}
bad() {
    FAILED=$((FAILED + 1)); echo "FAIL  $*"
    return 0
}
skip() {
    SKIPPED=$((SKIPPED + 1)); echo "skip  $*"
    return 0
}
section() {
    echo; echo "== $*"
    return 0
}
show() { # FILE -> its last 25 lines, indented
    local file="$1"
    sed 's/^/      | /' "$file" | tail -n 25
    return 0
}

# yaml_json FILE -> one compact JSON document per YAML document (needs ruby)
yaml_json() {
    local file="$1"
    ruby -ryaml -rjson -e 'YAML.load_stream(File.read(ARGV[0])).compact.each { |d| puts JSON.generate(d) }' "$file"
    return $?
}

run_runner() { # DIR ARGS... -> rc in RC; stdout/stderr in DIR
    local dir="$1"
    shift
    RC=0
    PATH="$FAKE_PATH" FAKE_DIR="$dir" PERF_RESET_SCRIPT="$TD/bin-fake/perf-reset-products.sh" \
        PERF_K8S_RUN_STAMP=20261014T000000Z \
        "$TEST_BASH" scripts/perf-k8s-run.sh "$@" >"$dir/stdout" 2>"$dir/stderr" || RC=$?
    return 0
}

last_row() { # FILE
    local file="$1"
    tail -n 1 "$file"
    return 0
}

# ---------------------------------------------------------------------------
section "runner: validity gates on saved Job logs"
# ---------------------------------------------------------------------------

# runner_case NAME LOG WANT_RC WANT_REASON FACTS VOIDLOG [EXTRA ARGS...]
runner_case() {
    local name="$1" log="$2" want_rc="$3" want_reason="$4" facts="$5" voidlog="$6" dir row reason
    shift 6
    dir="$WORK/runner-$name"
    mkdir -p "$dir/out"
    [[ -z "$voidlog" ]] || cp "$TD/runner/$voidlog" "$dir/out/void.log"
    local args=(--env "$ENV_EXAMPLE" --kind k6 --label labela --scenario S4 --rep 1
        --script products-read-only.ts --rate 150 --warm 2m --duration 10m --out "$dir/out")
    [[ -z "$facts" ]] || args+=(--facts "$TD/runner/$facts")
    FAKE_LOG="$TD/runner/$log" FAKE_JOB_STATE="${JOB_STATE:-Complete}" run_runner "$dir" "${args[@]}" "$@"
    if [[ "$RC" -ne "$want_rc" ]]; then bad "runner $name: exit $RC, want $want_rc"; show "$dir/stderr"; return 0; fi
    if [[ "$want_rc" -eq 0 || "$want_rc" -eq 3 ]]; then
        row="$(last_row "$dir/out/windows.tsv")"
        reason="$(printf '%s' "$row" | awk -F'\t' -v col=9 '{ print $col }')"
        if [[ "$reason" != "$want_reason" ]]; then bad "runner $name: reason '$reason', want '$want_reason'"; show "$dir/stderr"; return 0; fi
    fi
    if grep -q ' delete ' "$dir/kubectl.calls" 2>/dev/null; then bad "runner $name: the runner deleted something"; return 0; fi
    if [[ -f "$dir/forbidden.calls" ]]; then bad "runner $name: reached a forbidden tool: $(cat "$dir/forbidden.calls")"; return 0; fi
    ok "runner $name -> exit $RC${want_reason:+ ($want_reason)}"
    return 0
}

runner_case valid            valid.log            0 ""                    facts-clean.env ""
runner_case dropped          dropped.log          3 "dropped"             facts-clean.env ""
runner_case 429              429.log              3 "rate;429"            facts-clean.env ""
runner_case rate             rate.log             3 "rate"                facts-clean.env ""
runner_case 5xx-partial-submetric 5xx-partial-submetric.log 3 "5xx"     facts-clean.env ""
runner_case 5xx-fallback     failed-fallback.log  3 "5xx"                 facts-clean.env ""
runner_case 5xx-counter      5xx-counter.log      3 "5xx"                 facts-clean.env ""
runner_case 4xx-only         4xx-only.log         0 ""                    facts-clean.env ""
runner_case warm-rc-nonzero  warm-rc-nonzero.log  3 "warmup"              facts-clean.env ""
runner_case warm-rc-missing  warm-rc-missing.log  3 "warmup"              facts-clean.env ""
runner_case missing-marker   missing-marker.log   3 "missing-summary"     facts-clean.env ""
runner_case wrong-run-id     wrong-runid.log      3 "missing-summary"     facts-clean.env ""
runner_case slo-threshold    slo-threshold.log    0 ""                    facts-clean.env ""
runner_case void-in-window   valid.log            3 "void"                facts-clean.env void-inside.log
runner_case void-outside     valid.log            0 ""                    facts-clean.env void-outside.log
runner_case cluster-facts    valid.log            3 "pod;node;k6cpu;acu"  facts-dirty.env ""
JOB_STATE=Deleted runner_case killed-job killed.log 3 "void;missing-summary" facts-clean.env void-inside.log
FAKE_PODS_JSON="$TD/runner/pods.json" runner_case live-facts valid.log 0 "" "" ""

# k6cpu scales with K6_CPU_REQUEST: with 4 cores the threshold is 2800m, not 700m.
# --env and --facts are last-wins, so the extra args replace runner_case's own.
env_cpu4="$WORK/cpu4.env"
{ cat "$ENV_EXAMPLE"; echo 'export K6_CPU_REQUEST=4 K6_CPU_LIMIT=4'; } >"$env_cpu4"
for m in 2000 2800 3000; do
    sed "s/^k6_cpu_max_m=.*/k6_cpu_max_m=$m/" "$TD/runner/facts-clean.env" >"$WORK/facts-cpu-$m.env"
done
runner_case k6cpu-4-cores-2000m valid.log 0 ""      "" "" --env "$env_cpu4" --facts "$WORK/facts-cpu-2000.env"
runner_case k6cpu-4-cores-2800m valid.log 3 "k6cpu" "" "" --env "$env_cpu4" --facts "$WORK/facts-cpu-2800.env"
runner_case k6cpu-4-cores-3000m valid.log 3 "k6cpu" "" "" --env "$env_cpu4" --facts "$WORK/facts-cpu-3000.env"
if grep -q 'gate k6cpu: max 3000m reached 70% of the 4000m request' "$WORK/runner-k6cpu-4-cores-3000m/stderr" \
    && grep -q 'gate k6cpu: max 750m reached 70% of the 1000m request' "$WORK/runner-cluster-facts/stderr"; then
    ok "runner k6cpu log names the configured request in millicores (4000m; default 1000m)"
else
    bad "runner k6cpu log line"; show "$WORK/runner-k6cpu-4-cores-3000m/stderr"
fi

if grep -q 'gate 5xx: 120 of 90000 requests (perf_http_5xx)' "$WORK/runner-5xx-counter/stderr" \
    && ! grep -q 'fell back' "$WORK/runner-valid/stderr" \
    && ! grep -q 'fell back' "$WORK/runner-5xx-counter/stderr" \
    && grep -q 'warning: 5xx gate fell back to http_req_failed' "$WORK/runner-5xx-fallback/stderr" \
    && grep -q 'gate 5xx: 190 of 90000 requests (http_req_failed)' "$WORK/runner-5xx-partial-submetric/stderr" \
    && grep -q 'warning: 5xx gate fell back to http_req_failed' "$WORK/runner-5xx-partial-submetric/stderr" \
    && ! grep -q 'fell back' "$WORK/runner-4xx-only/stderr"; then
    ok "runner 5xx source: perf_http_5xx, else http_req_failed with a warning; a partial http_reqs{status:5NN} set is ignored"
else
    bad "runner 5xx source order"; show "$WORK/runner-5xx-counter/stderr"
fi

# The valid run's row, summary and env wiring.
dir="$WORK/runner-valid"
want_row="$(printf 'labela-S4-r1\tS4\t1\t2025-10-14T00:00:00Z\t2025-10-14T00:02:07Z\t2025-10-14T00:12:09Z\t0\t1\t')"
if [[ "$(head -n 1 "$dir/out/windows.tsv")" == "$(printf 'run_id\tscenario\trep\twarm_start\trun_start\trun_end\trc\tvalid\treason')" ]] \
    && [[ "$(last_row "$dir/out/windows.tsv")" == "$want_row" ]]; then
    ok "runner windows.tsv header and row match the contract"
else
    bad "runner windows.tsv"; show "$dir/out/windows.tsv"
fi
if jq -e '.perfMeta.runId == "labela-S4-r1" and .metrics.http_reqs.values.count == 90000' "$dir/out/k6/S4-r1.json" >/dev/null 2>&1 \
    && [[ -s "$dir/out/S4-r1.log" ]]; then
    ok "runner saved <scenario>-r<n>.log and k6/<scenario>-r<n>.json"
else
    bad "runner result files"; ls -R "$dir/out"
fi
dir="$WORK/runner-killed-job"
row="$(last_row "$dir/out/windows.tsv")"
if [[ "$(printf '%s' "$row" | awk -F'\t' '{ print $6 "|" $7 }')" == "|" ]] && grep -q 'streamed during the run' "$dir/stderr"; then
    ok "runner kept the streamed log of a deleted Job (no RUN_END, no RC)"
else
    bad "runner killed-job row: $row"; show "$dir/stderr"
fi
dir="$WORK/runner-live-facts"
if grep -q '^pod_before=uid-app-1$' "$dir/out/facts/S4-r1.env" && grep -q '^node_after=node-a$' "$dir/out/facts/S4-r1.env"; then
    ok "runner gathered app pod facts through kubectl get pods"
else
    bad "runner live facts"; cat "$dir/out/facts/S4-r1.env"
fi
if grep -q '^k6_cpu_request_m=1000$' "$dir/out/facts/S4-r1.env" && grep -q '^k6_cpu_limit_m=2000$' "$dir/out/facts/S4-r1.env"; then
    ok "runner recorded the k6 CPU request and limit (millicores) in the facts file"
else
    bad "runner CPU facts"; cat "$dir/out/facts/S4-r1.env"
fi

# Staircase: --rate 0 + PERF_STEPS, no rate gate.
dir="$WORK/runner-staircase"
mkdir -p "$dir/out"
FAKE_LOG="$TD/runner/staircase.log" run_runner "$dir" --env "$ENV_EXAMPLE" --kind k6 --label labela --scenario S5 --rep 1 \
    --script products-read-only.ts --rate 0 --warm 2m --duration 10m --duration 0 \
    --set PERF_STEPS=50:3m,100:3m --set PERF_MAXVUS=400 --out "$dir/out" --facts "$TD/runner/facts-clean.env"
if [[ $RC -eq 0 ]] && grep -q 'rate gate skipped' "$dir/stderr" && grep -q '"activeDeadlineSeconds":810\|activeDeadlineSeconds: 810' "$dir/applied.yaml"; then
    ok "runner staircase: rate gate skipped, deadline 120+2x(180+15)+300=810"
else
    bad "runner staircase (exit $RC)"; show "$dir/stderr"
fi

# Discard: log only.
dir="$WORK/runner-discard"
mkdir -p "$dir/out"
FAKE_LOG="$TD/runner/valid.log" run_runner "$dir" --env "$ENV_EXAMPLE" --kind k6 --label labela --scenario W --rep 0 \
    --script products-read-only.ts --rate 150 --warm 0 --duration 10m --out "$dir/out" --discard
if [[ $RC -eq 0 && -s "$dir/out/W-r0.log" && ! -e "$dir/out/windows.tsv" && ! -e "$dir/out/k6/W-r0.json" ]] \
    && grep -q 'HARNESS_K6_FLAGS: --no-thresholds\|"--no-thresholds"' "$dir/applied.yaml"; then
    ok "runner --discard keeps the log only and runs without thresholds"
else
    bad "runner discard (exit $RC)"; show "$dir/stderr"
fi

# A re-run of the same rep moves the previous attempt aside.
dir="$WORK/runner-valid"
FAKE_LOG="$TD/runner/dropped.log" run_runner "$dir" --env "$ENV_EXAMPLE" --kind k6 --label labela --scenario S4 --rep 1 \
    --script products-read-only.ts --rate 150 --warm 2m --duration 10m --out "$dir/out" --facts "$TD/runner/facts-clean.env"
if [[ $RC -eq 3 && $(find "$dir/out/attempts" -name 'S4-r1-*.log' | wc -l) -eq 1 && $(wc -l <"$dir/out/windows.tsv") -eq 3 ]]; then
    ok "runner re-run archives the earlier attempt and appends a second row"
else
    bad "runner re-run (exit $RC)"; find "$dir/out" | head -n 20
fi
# A third run in the same (pinned) second must not overwrite the first archive.
FAKE_LOG="$TD/runner/rate.log" run_runner "$dir" --env "$ENV_EXAMPLE" --kind k6 --label labela --scenario S4 --rep 1 \
    --script products-read-only.ts --rate 150 --warm 2m --duration 10m --out "$dir/out" --facts "$TD/runner/facts-clean.env"
first="$dir/out/attempts/S4-r1-20261014T000000Z.log" second="$dir/out/attempts/S4-r1-20261014T000000Z-1.log"
if [[ $RC -eq 3 && -f "$first" && -f "$second" ]] && grep -q '"dropped_iterations":{[^}]*"values":{"count":0' "$first" \
    && grep -q '"dropped_iterations":{[^}]*"values":{"count":412' "$second"; then
    ok "runner archives made in the same second get -1, -2, ... instead of overwriting"
else
    bad "runner archive collision (exit $RC)"; find "$dir/out/attempts" | head -n 20
fi

# Usage errors.
usage_case() { # NAME ARGS...
    local name="$1"
    local dir="$WORK/usage-$name"
    shift
    mkdir -p "$dir"
    run_runner "$dir" "$@"
    if [[ $RC -eq 2 ]]; then ok "runner usage: $name -> exit 2"; else bad "runner usage: $name -> exit $RC"; show "$dir/stderr"; fi
    return 0
}
usage_case no-env --kind k6
usage_case rate-zero-without-steps --env "$ENV_EXAMPLE" --kind k6 --label l --scenario S4 --rep 1 --script a.ts --rate 0 --warm 0 --duration 1m --render-only
usage_case lowercase-set --env "$ENV_EXAMPLE" --kind k6 --set perf_rate=1
usage_case bad-kind --env "$ENV_EXAMPLE" --kind nope
usage_case bad-script --env "$ENV_EXAMPLE" --kind k6 --label l --scenario S4 --rep 1 --script ../x.ts --rate 1 --warm 0 --duration 1m --render-only
bad_storage_env="$WORK/bad-storage.env"
{ cat "$ENV_EXAMPLE"; echo 'export K6_EPHEMERAL_LIMIT=1GB'; } >"$bad_storage_env"
usage_case bad-ephemeral-storage --env "$bad_storage_env" --kind k6 --label l --scenario S4 --rep 1 --script a.ts --rate 1 --warm 0 --duration 1m --render-only

cpu_usage_case() { # NAME ENV_LINE WANT_MESSAGE -> exit 2, and stderr names the variable
    local name="$1" line="$2" want="$3"
    local dir="$WORK/usage-$name"
    mkdir -p "$dir"
    { cat "$ENV_EXAMPLE"; echo "$line"; } >"$dir/env"
    run_runner "$dir" --env "$dir/env" --kind k6 --label l --scenario S4 --rep 1 --script a.ts --rate 1 --warm 0 \
        --duration 1m --render-only
    if [[ $RC -eq 2 ]] && grep -Fq "$want" "$dir/stderr"; then
        ok "runner usage: $name -> exit 2 ($want)"
    else
        bad "runner usage: $name -> exit $RC, want 2 and '$want'"; show "$dir/stderr"
    fi
    return 0
}
cpu_usage_case cpu-request-abc 'export K6_CPU_REQUEST=abc' "K6_CPU_REQUEST must be a CPU quantity"
cpu_usage_case cpu-request-empty 'export K6_CPU_REQUEST=' "K6_CPU_REQUEST must be a CPU quantity"
cpu_usage_case cpu-request-decimal 'export K6_CPU_REQUEST=1.5' "K6_CPU_REQUEST must be a CPU quantity"
cpu_usage_case cpu-request-zero 'export K6_CPU_REQUEST=0' "K6_CPU_REQUEST must be above zero"
cpu_usage_case cpu-request-zero-m 'export K6_CPU_REQUEST=0m' "K6_CPU_REQUEST must be above zero"
cpu_usage_case cpu-limit-zero-m 'export K6_CPU_LIMIT=0m' "K6_CPU_LIMIT must be above zero"
cpu_usage_case cpu-limit-below-request 'export K6_CPU_REQUEST=4 K6_CPU_LIMIT=3500m' \
    "K6_CPU_LIMIT (3500m) must not be below K6_CPU_REQUEST (4)"
usage_case reset-bad-base-rows --env "$ENV_EXAMPLE" --kind reset --rows 10 --watermark 2026-01-01T00:00:00Z --out "$WORK/x" --base-rows abc

# ---------------------------------------------------------------------------
section "runner: --kind reset and --kind idle"
# ---------------------------------------------------------------------------

reset_case() { # NAME LOG WANT_RC
    local name="$1" log="$2" want_rc="$3"
    local dir="$WORK/reset-$name"
    mkdir -p "$dir/out"
    FAKE_LOG="$TD/runner/$log" run_runner "$dir" --env "$ENV_EXAMPLE" --kind reset --rows 250000 \
        --watermark 2026-01-01T00:00:00Z --clear-outbox --out "$dir/out"
    if [[ $RC -ne $want_rc ]]; then bad "reset $name: exit $RC, want $want_rc"; show "$dir/stderr"; return 0; fi
    if ! grep -q -- '-- perf-reset stub: --rows 250000 --watermark 2026-01-01T00:00:00Z --clear-outbox' "$dir/applied.yaml"; then
        bad "reset $name: the SQL ConfigMap does not carry the generator output"; return 0
    fi
    if [[ ! -s "$dir/out/reset-20261014T000000Z.log" ]]; then bad "reset $name: no reset-<ts>.log"; return 0; fi
    ok "reset $name -> exit $RC"
    return 0
}
reset_case counts-ok reset-ok.log 0
reset_case counts-mismatch reset-mismatch.log 3
if grep -q 'products=250131, expected 250106' "$WORK/reset-counts-mismatch/stderr" \
    && grep -q 'outbox_pending=4, expected 0' "$WORK/reset-counts-mismatch/stderr"; then
    ok "reset gate names products and outbox_pending"
else
    bad "reset gate messages"; show "$WORK/reset-counts-mismatch/stderr"
fi

dir="$WORK/idle"
mkdir -p "$dir/out"
run_runner "$dir" --env "$ENV_EXAMPLE" --kind idle --label labela --rep 0 --duration 1s --out "$dir/out" \
    --facts "$TD/runner/facts-clean.env"
if [[ $RC -eq 0 ]] && last_row "$dir/out/windows.tsv" | grep -q $'^labela-idle-r0\tidle\t0\t\t.*\t0\t1\t$'; then
    ok "idle window row written (scenario idle, rep 0, valid)"
else
    bad "idle window (exit $RC)"; show "$dir/stderr"
fi

# ---------------------------------------------------------------------------
section "templates: render-only output"
# ---------------------------------------------------------------------------

env_pinned="$WORK/pinned.env"
{ cat "$ENV_EXAMPLE"; echo 'export K6_NODEPOOL=pool-a K6_ZONE=zone-a'; } >"$env_pinned"
dir="$WORK/render-k6"
mkdir -p "$dir"
# shellcheck disable=SC2016 # a literal $(b): the runner must escape it for the kubelet
run_runner "$dir" --env "$env_pinned" --render-only --kind k6 --label dryrun --scenario S4 --rep 0 \
    --script products-read-only.ts --rate 5 --warm 2m --duration 30s --set PERF_READ_MIX=get --set 'PERF_LITERAL=a$(b)'
cp "$dir/stdout" "$WORK/k6-job.rendered.yaml"
if [[ $RC -ne 0 ]]; then
    bad "render k6 (exit $RC)"; show "$dir/stderr"
else
    if grep -Eq '\$\{[A-Z0-9_]+\}' "$dir/stdout"; then bad "render k6: an unsubstituted \${VAR} is left"; else ok "render k6: every listed placeholder substituted"; fi
    # shellcheck disable=SC2016 # literal Job shell text, which must not have been expanded
    if grep -Fq 'echo "RUN_START=$(date -u +%Y-%m-%dT%H:%M:%SZ)"' "$dir/stdout" && grep -Fq 'echo "RC=$rc"' "$dir/stdout" && ! grep -q -- '--no-summary' "$dir/stdout"; then
        ok "render k6: \$(date ...) and \$rc survive envsubst"
    else
        bad "render k6: the Job shell was altered by envsubst"
    fi
    if [[ $HAVE_RUBY -eq 1 ]]; then
        if yaml_json "$dir/stdout" >"$dir/job.json" && jq -e --slurp '
            .[0] as $j | $j.spec.template.spec as $p | $p.containers[0] as $c
            | ($c.env | map({ (.name): .value }) | add) as $e
            | $j.kind == "Job" and $j.spec.backoffLimit == 0 and $p.restartPolicy == "Never"
            and $j.spec.activeDeadlineSeconds == 450 and $j.spec.ttlSecondsAfterFinished == 86400
            and $j.metadata.labels["app.kubernetes.io/part-of"] == "k6-perf"
            and $j.spec.template.metadata.labels["app.kubernetes.io/part-of"] == "k6-perf"
            and $j.metadata.labels["app.kubernetes.io/component"] == "load-generator"
            and $j.spec.template.metadata.labels["app.kubernetes.io/component"] == "load-generator"
            and $j.spec.template.metadata.annotations["karpenter.sh/do-not-disrupt"] == "true"
            and $c.resources.requests.cpu == "1" and $c.resources.requests.memory == "512Mi"
            and $c.resources.limits.cpu == "2" and $c.resources.limits.memory == "1Gi"
            and $p.securityContext.runAsNonRoot == true and $p.securityContext.seccompProfile.type == "RuntimeDefault"
            and $p.nodeSelector["karpenter.sh/nodepool"] == "pool-a" and $p.nodeSelector["topology.kubernetes.io/zone"] == "zone-a"
            and $p.affinity.podAntiAffinity.requiredDuringSchedulingIgnoredDuringExecution[0].labelSelector.matchLabels["app.kubernetes.io/name"] == "go-bricks-demo-project"
            and ([$c.volumeMounts[] | .mountPath] | index("/scripts") != null and index("/scripts/types") != null)
            and ([$p.volumes[] | .configMap.name // empty] == ["k6-scripts-perf-harness-v1", "k6-types-perf-harness-v1"])
            and $e.PERF_SUMMARY_STDOUT == "1" and $e.PERF_RUN_ID == "dryrun-S4-r0" and $e.PERF_RATE == "5"
            and $e.PERF_DURATION == "30s" and $e.HARNESS_WARM == "120s" and $e.HARNESS_WARM_RATE == "5"
            and $c.resources.requests["ephemeral-storage"] == "256Mi" and $c.resources.limits["ephemeral-storage"] == "1Gi"
            and $e.K6_BASE_URL != null and $e.PERF_READ_MIX == "get" and $e.PERF_LITERAL == "a$$(b)"
            and ($j.metadata.name | test("^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$"))' "$dir/job.json" >/dev/null; then
            ok "render k6: Job shape (backoffLimit 0, deadline warm+dur+300, labels, resources, security, placement, mounts, env)"
        else
            bad "render k6: Job shape"; jq . "$dir/job.json" | head -80
        fi
    else
        skip "render k6: Job shape (ruby not installed, no YAML parser)"
    fi
fi

# K6_CPU_REQUEST / K6_CPU_LIMIT reach the template as spelled, quoted, request first.
render_cpu_case() { # NAME WANT ENV_LINE -> WANT is the rendered cpu values, in order
    local name="$1" want="$2" line="$3" got
    local dir="$WORK/render-cpu-$name"
    mkdir -p "$dir"
    { cat "$ENV_EXAMPLE"; echo "$line"; } >"$dir/env"
    run_runner "$dir" --env "$dir/env" --render-only --kind k6 --label dryrun --scenario S4 --rep 0 \
        --script products-read-only.ts --rate 5 --warm 2m --duration 30s
    got="$(awk '/^ +cpu: / { printf "%s%s", sep, $2; sep = " " }' "$dir/stdout")"
    if [[ $RC -eq 0 && "$got" == "$want" ]]; then
        ok "render k6 cpu $name: $want"
    else
        bad "render k6 cpu $name (exit $RC): got '$got', want '$want'"; show "$dir/stderr"
    fi
    return 0
}
render_cpu_case unset '"1" "2"' 'unset K6_CPU_REQUEST K6_CPU_LIMIT'
render_cpu_case explicit-defaults '"1" "2"' 'export K6_CPU_REQUEST=1 K6_CPU_LIMIT=2'
render_cpu_case cores '"4" "4"' 'export K6_CPU_REQUEST=4 K6_CPU_LIMIT=4'
render_cpu_case millicores '"3500m" "4"' 'export K6_CPU_REQUEST=3500m K6_CPU_LIMIT=4'
if cmp -s "$WORK/render-cpu-unset/stdout" "$WORK/render-cpu-explicit-defaults/stdout"; then
    ok "render k6 cpu: unset knobs render byte-for-byte what K6_CPU_REQUEST=1 K6_CPU_LIMIT=2 renders"
else
    bad "render k6 cpu: unset knobs differ from 1 / 2"; diff "$WORK/render-cpu-unset/stdout" "$WORK/render-cpu-explicit-defaults/stdout" | head -n 20
fi

dir="$WORK/render-reset"
mkdir -p "$dir"
run_runner "$dir" --env "$ENV_EXAMPLE" --render-only --kind reset --rows 250000 --watermark 2026-01-01T00:00:00Z --clear-outbox
cp "$dir/stdout" "$WORK/psql-job.rendered.yaml"
if [[ $RC -ne 0 ]]; then
    bad "render reset (exit $RC)"; show "$dir/stderr"
elif [[ $HAVE_RUBY -eq 1 ]]; then
    if yaml_json "$dir/stdout" >"$dir/docs.json" && jq -e --slurp '
        .[0] as $cm | .[1] as $j | $j.spec.template.spec as $p | $p.containers[0] as $c
        | ($c.env | map({ (.name): .value }) | add) as $e
        | $cm.kind == "ConfigMap" and ($cm.data["reset.sql"] | test("perf-reset stub"))
        and $j.kind == "Job" and $j.spec.backoffLimit == 0
        and $c.envFrom[0].secretRef.name == "go-bricks-demo-project-db"
        and $j.metadata.labels["app.kubernetes.io/part-of"] == "k6-perf"
        and $j.metadata.labels["app.kubernetes.io/component"] == "reset"
        and $j.spec.template.metadata.labels["app.kubernetes.io/component"] == "reset"
        and $cm.metadata.labels["app.kubernetes.io/component"] == "reset"
        and $e.PGHOST == "$(DATABASE_HOST)" and $e.PGPASSWORD == "$(DATABASE_PASSWORD)"
        and $e.PGSSLMODE == "verify-full" and $e.PGSSLROOTCERT == "/etc/perf-reset/ca/ca-bundle.pem"
        and ([$p.volumes[] | .configMap.name // empty] == [$cm.metadata.name, "db-ca-bundle"])
        and ($c.args | index("/sql/reset.sql") != null)
        and $p.securityContext.runAsNonRoot == true
        and $c.resources.requests["ephemeral-storage"] == "64Mi" and $c.resources.limits["ephemeral-storage"] == "256Mi"' "$dir/docs.json" >/dev/null; then
        ok "render reset: SQL ConfigMap + psql Job (envFrom secret, PG* from DATABASE_* keys, verify-full CA)"
    else
        bad "render reset: shape"; jq . "$dir/docs.json" | head -80
    fi
else
    skip "render reset: shape (ruby not installed)"
fi

dir="$WORK/render-reset-job-only"
mkdir -p "$dir"
run_runner "$dir" --env "$ENV_EXAMPLE" --render-only --kind reset
# shellcheck disable=SC2016 # a literal kubelet $(VAR) reference
if [[ $RC -eq 0 && $(grep -c '^kind: ' "$dir/stdout") -eq 1 ]] && grep -q '^kind: Job$' "$dir/stdout" \
    && grep -Fq 'value: "$(DATABASE_PASSWORD)"' "$dir/stdout"; then
    ok "render reset without --rows/--watermark: the Job alone, for an admission dry run"
else
    bad "render reset Job only (exit $RC)"; show "$dir/stderr"
fi

dir="$WORK/render-configmaps"
mkdir -p "$dir"
run_runner "$dir" --env "$ENV_EXAMPLE" --load-configmaps HEAD --render-only
cp "$dir/stdout" "$WORK/configmaps.rendered.yaml"
if [[ $RC -eq 0 ]] && grep -q '  loadtests/config.ts$' "$dir/stderr" && grep -q '  loadtests/types/index.ts$' "$dir/stderr" \
    && [[ $(grep -c '^---$' "$dir/stdout") -eq 2 ]] \
    && sed -n 2p "$dir/stdout" | jq -e '.metadata.name == "k6-scripts-head" and (.data | has("config.ts") and has("products-read-only.ts"))' >/dev/null \
    && sed -n 4p "$dir/stdout" | jq -e '.metadata.name == "k6-types-head" and (.data | has("index.ts"))' >/dev/null; then
    ok "--load-configmaps: two ConfigMaps from git archive plus a sha256 manifest"
else
    bad "--load-configmaps (exit $RC)"; show "$dir/stderr"
fi

if [[ "${PERF_TEST_KUBECONFORM:-0}" == 1 ]]; then
    if GOWORK=off go run github.com/yannh/kubeconform/cmd/kubeconform@v0.6.7 -strict -summary \
        -kubernetes-version 1.31.0 "$WORK/k6-job.rendered.yaml" "$WORK/psql-job.rendered.yaml" "$WORK/configmaps.rendered.yaml" >"$WORK/kubeconform.out" 2>&1; then
        ok "kubeconform -strict: $(tail -n 1 "$WORK/kubeconform.out")"
    else
        bad "kubeconform"; show "$WORK/kubeconform.out"
    fi
else
    skip "kubeconform (set PERF_TEST_KUBECONFORM=1; needs network)"
fi

# ---------------------------------------------------------------------------
section "watchdog: --simulate abort rules (kubectl and aws are tripwires)"
# ---------------------------------------------------------------------------

for expect in "$TD"/watchdog/cases/*.expect; do
    name="$(basename "$expect" .expect)"
    dir="$WORK/watch-$name"
    mkdir -p "$dir/sim"
    cp "$TD/watchdog/base.json" "$dir/sim/base.json"
    cp "$TD/watchdog/cases/$name.jsonl" "$dir/sim/samples.jsonl"
    end="2026-10-14T06:00:00Z"
    [[ ! -f "$TD/watchdog/cases/$name.session_end" ]] || end="$(cat "$TD/watchdog/cases/$name.session_end")"
    RC=0
    PATH="$FORBIDDEN_PATH" FAKE_DIR="$dir" "$TEST_BASH" scripts/perf-watchdog.sh watch --context ctx-test --namespace ns-test \
        --deployment demo-app --pod demo-app-0 --node node-a --job-selector "$LOAD_SEL" \
        --aurora-cluster db-test --acu-ceiling 6 --broker-namespace broker-ns --broker-pod broker-0 --broker-mem-mi 1024 \
        --node-cpu-pct 90 --interval 15 --session-end "$end" --out "$dir/watchdog.csv" --void-log "$dir/void.log" \
        --simulate "$dir/sim" >"$dir/stdout" 2>"$dir/stderr" || RC=$?
    {
        grep '^VOID ' "$dir/stdout" | sed -E 's/^VOID [^ ]+ (rule=[^ ]+ scope=[^ ]+ action=[^ ]+).*/VOID \1/' || true
        echo "KILLS $(grep -c '^KILL ' "$dir/stdout" || true)"
    } >"$dir/got"
    if [[ $RC -ne 0 ]]; then bad "watchdog $name: exit $RC"; show "$dir/stderr"; continue; fi
    if [[ -f "$dir/forbidden.calls" ]]; then bad "watchdog $name: simulate shelled out: $(cat "$dir/forbidden.calls")"; continue; fi
    if ! diff -u "$expect" "$dir/got" >"$dir/diff"; then bad "watchdog $name"; show "$dir/diff"; continue; fi
    if ! diff -q <(grep '^VOID ' "$dir/stdout" || true) "$dir/void.log" >/dev/null; then bad "watchdog $name: void.log differs from the reported VOID lines"; continue; fi
    if [[ $(wc -l <"$dir/watchdog.csv") -ne $(($(wc -l <"$dir/sim/samples.jsonl") + 1)) ]]; then bad "watchdog $name: one CSV row per sample"; continue; fi
    ok "watchdog $name: $(tr '\n' ' ' <"$expect" | sed 's/ $//')"
done

if grep -q 'VOID 2026-10-14T00:00:15Z rule=node-changed scope=session action=kill detail=pod demo-app-0 is on node-b, expected node-a' "$WORK/watch-node-changed/void.log"; then
    ok "watchdog VOID line format: VOID <ISO> rule= scope= action= detail="
else
    bad "watchdog VOID line format"; cat "$WORK/watch-node-changed/void.log"
fi

# Live collection (no --simulate), through the fake kubectl and a fake aws that
# serve base.json: covers the reads, NotFound detection, sample assembly and the
# delete call that --simulate replaces.
watch_live() { # NAME [ENV=VALUE...]
    local name="$1" pid n
    local dir="$WORK/watch-live-$name"
    shift
    mkdir -p "$dir"
    env "$@" PATH="$TD/bin-watch:$TD/bin-fake:$PATH" FAKE_DIR="$dir" FAKE_WATCH_BASE="$TD/watchdog/base.json" \
        "$TEST_BASH" scripts/perf-watchdog.sh watch --context ctx-test --namespace ns-test --deployment demo-app \
        --pod demo-app-0 --node node-a --job-selector "$LOAD_SEL" --aurora-cluster db-test \
        --acu-ceiling 6 --broker-namespace broker-ns --broker-pod broker-0 --broker-mem-mi 1024 --node-cpu-pct 90 \
        --interval 1 --session-end 2099-01-01T00:00:00Z --out "$dir/watchdog.csv" --void-log "$dir/void.log" \
        >"$dir/stdout" 2>"$dir/stderr" &
    pid=$!
    for n in $(seq 1 60); do
        [[ -f "$dir/watchdog.csv" && $(wc -l <"$dir/watchdog.csv") -ge 2 ]] && break
        sleep 0.25
    done
    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
    : "$n"
    return 0
}
watch_live healthy
dir="$WORK/watch-live-healthy"
if [[ "$(sed -n 2p "$dir/watchdog.csv" | cut -d, -f2-)" == "1,1,present,True,0,node-a,present,45,-,-,600,2.5,1,-,0" ]] \
    && [[ ! -s "$dir/void.log" ]] && ! grep -qv -E '^--context ctx-test (--namespace (ns-test|broker-ns) )?(get|top) ' "$dir/kubectl.calls" \
    && grep -q '^cloudwatch get-metric-statistics --namespace AWS/RDS --metric-name ServerlessDatabaseCapacity' <(sed 's/^aws //' "$dir/aws.calls"); then
    ok "watchdog live reads: kubectl get/top + CloudWatch only, one CSV row per sample"
else
    bad "watchdog live reads"; show "$dir/watchdog.csv"; show "$dir/kubectl.calls"; show "$dir/stderr"
fi
watch_live pod-notfound FAKE_WATCH_POD_NOTFOUND=1
dir="$WORK/watch-live-pod-notfound"
if sed -n 2p "$dir/watchdog.csv" | grep -q ',gone,' && grep -q 'rule=pod-gone scope=session action=kill' "$dir/void.log" \
    && grep -qxF -- "--context ctx-test --namespace ns-test delete job -l $LOAD_SEL --wait=false" "$dir/kubectl.calls" \
    && [[ $(grep -c ' delete ' "$dir/kubectl.calls") -eq $(grep -cxF -- "--context ctx-test --namespace ns-test delete job -l $LOAD_SEL --wait=false" "$dir/kubectl.calls") ]]; then
    ok "watchdog live NotFound: pod-gone VOID and its only action, delete job -l <selector>"
else
    bad "watchdog live NotFound"; show "$dir/watchdog.csv"; show "$dir/kubectl.calls"; show "$dir/stderr"
fi
# A reset Job (component=reset) running alone: not counted, not deleted.
watch_live reset-only FAKE_WATCH_POD_NOTFOUND=1 FAKE_WATCH_JOBS="$TD/watchdog/jobs-reset-only.json"
dir="$WORK/watch-live-reset-only"
if [[ "$(sed -n 2p "$dir/watchdog.csv" | cut -d, -f14,16)" == "0,0" ]] && grep -q 'rule=pod-gone' "$dir/void.log" \
    && ! grep -q ' delete ' "$dir/kubectl.calls" && [[ ! -e "$dir/deleted.jobs" ]]; then
    ok "watchdog: a reset Job running alone is neither counted as load nor deleted"
else
    bad "watchdog reset-only"; show "$dir/watchdog.csv"; show "$dir/kubectl.calls"
fi
# A load Job and a reset Job together: only the load Job is deleted.
watch_live load-and-reset FAKE_WATCH_POD_NOTFOUND=1 FAKE_WATCH_JOBS="$TD/watchdog/jobs-load-and-reset.json"
dir="$WORK/watch-live-load-and-reset"
if [[ "$(sed -n 2p "$dir/watchdog.csv" | cut -d, -f14,16)" == "1,1" ]] \
    && [[ "$(sort -u "$dir/deleted.jobs" 2>/dev/null)" == "k6-labela-s4-r1-1014000000" ]]; then
    ok "watchdog: with load and reset Jobs running, only the load Job is deleted"
else
    bad "watchdog load-and-reset"; show "$dir/watchdog.csv"; show "$dir/kubectl.calls"
fi

# --job-selector must carry the exact load-generator equality term.
selector_case() { # NAME WANT_RC SELECTOR
    local name="$1" want_rc="$2" selector="$3" rc=0
    local dir="$WORK/watch-selector-$name"
    mkdir -p "$dir/sim"
    cp "$TD/watchdog/base.json" "$dir/sim/base.json"
    cp "$TD/watchdog/cases/healthy.jsonl" "$dir/sim/samples.jsonl"
    PATH="$FORBIDDEN_PATH" FAKE_DIR="$dir" "$TEST_BASH" scripts/perf-watchdog.sh watch --context ctx-test --namespace ns-test \
        --deployment demo-app --pod demo-app-0 --node node-a --job-selector "$selector" \
        --aurora-cluster db-test --acu-ceiling 6 --broker-namespace broker-ns --broker-pod broker-0 --broker-mem-mi 1024 \
        --node-cpu-pct 90 --interval 15 --session-end 2026-10-14T06:00:00Z --out "$dir/watchdog.csv" --void-log "$dir/void.log" \
        --simulate "$dir/sim" >"$dir/stdout" 2>"$dir/stderr" || rc=$?
    if [[ $rc -ne $want_rc ]]; then bad "watchdog selector $name: exit $rc, want $want_rc"; show "$dir/stderr"; return 0; fi
    if [[ $want_rc -eq 2 ]] && ! grep -q 'must contain the term app.kubernetes.io/component=load-generator' "$dir/stderr"; then
        bad "watchdog selector $name: the usage error does not name the required term"; return 0
    fi
    ok "watchdog selector $name -> exit $rc"
    return 0
}
selector_case broad-part-of 2 "app.kubernetes.io/part-of=k6-perf"
selector_case lookalike-key 2 "app.kubernetes.io/part-of=k6-perf,x.app.kubernetes.io/component=load-generator"
selector_case lookalike-value 2 "app.kubernetes.io/part-of=k6-perf,app.kubernetes.io/component=load-generator-x"
selector_case not-equal 2 "app.kubernetes.io/component!=load-generator"
selector_case documented 0 "$LOAD_SEL"
selector_case double-equals-spaces 0 "app.kubernetes.io/part-of=k6-perf, app.kubernetes.io/component == load-generator"

usage_rc=0
PATH="$FORBIDDEN_PATH" "$TEST_BASH" scripts/perf-watchdog.sh watch --context c --namespace n >/dev/null 2>"$WORK/watch-usage.err" || usage_rc=$?
if [[ $usage_rc -eq 2 ]] && grep -q 'every flag is required' "$WORK/watch-usage.err"; then
    ok "watchdog watch: every flag is required (exit 2)"
else
    bad "watchdog watch usage (exit $usage_rc)"
fi

for expect in "$TD"/watchdog/idle/*.expect; do
    name="$(basename "$expect" .expect)"
    dir="$WORK/idle-$name"
    mkdir -p "$dir"
    RC=0
    mkdir -p "$dir/sim"
    cp "$TD/watchdog/idle/$name.txt" "$dir/sim/top.txt"
    PATH="$FORBIDDEN_PATH" FAKE_DIR="$dir" "$TEST_BASH" scripts/perf-watchdog.sh wait-idle --context ctx-test --namespace ns-test \
        --pod demo-app-0 --idle-millicores 20 --margin 15 --samples 2 --min 3m --max 10m \
        --simulate "$dir/sim" >"$dir/stdout" 2>"$dir/stderr" || RC=$?
    line="$(grep '^COOLDOWN ' "$dir/stdout" || true)"
    got="rc=$RC $(grep -oE 'result=[^ ]+' <<<"$line") $(grep -oE 'waited_s=[0-9]+' <<<"$line") $(grep -oE 'samples=[0-9]+' <<<"$line")"
    want="$(cat "$expect")"
    if [[ -f "$dir/forbidden.calls" ]]; then bad "wait-idle $name: simulate shelled out"; continue; fi
    if [[ "$got" == "$want" ]]; then ok "wait-idle $name: $want"; else bad "wait-idle $name: got '$got', want '$want'"; show "$dir/stdout"; fi
done
if grep -q '^NOTICE metrics-server unavailable' "$WORK/idle-no-metrics/stdout"; then
    ok "wait-idle logs a notice before the fixed fallback wait"
else
    bad "wait-idle fallback notice"
fi

# ---------------------------------------------------------------------------
section "nrql: substitution, key handling, errors"
# ---------------------------------------------------------------------------

Q="loadtests/nrql/queries.yaml"
VARS_A="$(cat "$TD/nrql/vars-labela.txt")"
nrql() { # DIR ARGS... (stdin passes through)
    local dir="$1"
    shift
    RC=0
    PATH="$FAKE_PATH" FAKE_DIR="$dir" "$TEST_BASH" scripts/perf-nrql.sh "$@" >"$dir/stdout" 2>"$dir/stderr" || RC=$?
    return 0
}

dir="$WORK/nrql-golden"
mkdir -p "$dir"
nrql "$dir" --queries "$Q" --windows "$TD/nrql/windows-labela.tsv" --vars "$VARS_A" --print
if [[ $RC -eq 0 ]] && diff -u "$TD/nrql/golden-labela.tsv" "$dir/stdout" >"$dir/diff"; then
    ok "nrql golden: rendered queries match golden-labela.tsv ($(wc -l <"$dir/stdout" | tr -d ' ') requests)"
else
    bad "nrql golden (exit $RC)"; show "$dir/diff"; show "$dir/stderr"
fi

dir="$WORK/nrql-when"
mkdir -p "$dir"
nrql "$dir" --queries "$Q" --windows "$TD/nrql/windows-labela.tsv" --vars "${VARS_A/,JOSE_METRIC=jose.operation.duration/}" --print
if [[ $RC -eq 0 ]] && grep -q 'skipping Q15a: JOSE_METRIC is not set' "$dir/stderr" \
    && grep -v $'^Q15a\t' "$TD/nrql/golden-labela.tsv" | diff -q - "$dir/stdout" >/dev/null; then
    ok "nrql skips a when: query until its placeholder is set, and renders the rest unchanged"
else
    bad "nrql when: skip (exit $RC)"; show "$dir/stderr"
fi

dir="$WORK/nrql-edge"
mkdir -p "$dir"
nrql "$dir" --queries "$Q" --windows "$TD/nrql/windows-edge.tsv" --vars "$VARS_A" --print
idle_since="$(jq -n '"2026-10-13T23:41:00Z" | fromdateiso8601 * 1000')"
if [[ $RC -eq 0 ]] \
    && grep -q 'warning: labela-idle-r0: windows.tsv line 2 is superseded by line 4' "$dir/stderr" \
    && [[ $(grep -c $'^Q7\tlabela-idle-r0\t' "$dir/stdout") -eq 1 ]] \
    && grep -q $'^Q7\tlabela-idle-r0\t.*SINCE '"$idle_since"' ' "$dir/stdout" \
    && grep -q $'^Q9\tlabela-idle-r0\t' "$dir/stdout"; then
    ok "nrql: the last row per run_id wins, with a warning; the idle window gets Q7 and Q9"
else
    bad "nrql supersede (exit $RC)"; show "$dir/stderr"
fi
if grep -q "warning: skipping labela-S4-r3: run_start '2026-13-14T03:02:00Z'" "$dir/stderr" \
    && ! grep -q 'labela-S4-r3' "$dir/stdout"; then
    ok "nrql skips a row whose timestamps do not convert (month 13), with a warning"
else
    bad "nrql bad timestamp"; show "$dir/stderr"
fi
if grep -q $'^Q18/Q18a\tlabela-S5-r2\t' "$dir/stdout" && grep -q $'^Q18/Q18b\tlabela-S5-r2\t' "$dir/stdout" \
    && grep -q 'labela-S5-r2 is void' "$dir/stderr" && ! grep -q 'labela-S1-r4' "$dir/stdout"; then
    ok "nrql captures the last S5 attempt although void, and skips other void rows"
else
    bad "nrql void S5"; show "$dir/stderr"
fi
dir="$WORK/nrql-unsafe-run-id"
mkdir -p "$dir"
printf 'run_id\tscenario\trep\twarm_start\trun_start\trun_end\trc\tvalid\treason\n../evil\tS4\t1\t\t2026-10-14T00:02:07Z\t2026-10-14T00:12:09Z\t0\t1\t\n' >"$dir/windows.tsv"
nrql "$dir" --queries "$Q" --windows "$dir/windows.tsv" --vars "$VARS_A" --print
if [[ $RC -eq 2 ]] && grep -q "run_id '../evil' is not" "$dir/stderr"; then
    ok "nrql refuses a run_id that is not a safe file name (exit 2)"
else
    bad "nrql unsafe run_id (exit $RC)"; show "$dir/stderr"
fi

dir="$WORK/nrql-labelb"
mkdir -p "$dir"
sed 's/labela/labelb/g' "$TD/nrql/windows-labela.tsv" >"$dir/windows.tsv"
nrql "$dir" --queries "$Q" --windows "$dir/windows.tsv" --vars "${VARS_A//labela/labelb}" --print
if [[ $RC -eq 0 ]] && sed 's/labelb/labela/g' "$dir/stdout" | diff -q - "$TD/nrql/golden-labela.tsv" >/dev/null; then
    ok "nrql substitution is byte-identical across labels (only the label differs)"
else
    bad "nrql cross-label substitution"
fi

dir="$WORK/nrql-env-vars"
mkdir -p "$dir"
PERF_NRQL_VARS="${VARS_A%%,CNS=*}" nrql "$dir" --queries "$Q" --windows "$TD/nrql/windows-labela.tsv" \
    --vars "CNS=namespace,CCT=container,CPOD=pod,CNODE=node" --print
if [[ $RC -eq 0 ]] && diff -q "$dir/stdout" "$TD/nrql/golden-labela.tsv" >/dev/null; then
    ok "nrql PERF_NRQL_VARS + --vars merge to the same rendering"
else
    bad "nrql PERF_NRQL_VARS merge (exit $RC)"; show "$dir/stderr"
fi

dir="$WORK/nrql-missing"
mkdir -p "$dir"
nrql "$dir" --queries "$Q" --windows "$TD/nrql/windows-labela.tsv" --vars "${VARS_A%%,CNS=*}" --out "$dir/out" --key-stdin </dev/null
if [[ $RC -eq 2 ]] && grep -q 'unresolved placeholder(s): CNS' "$dir/stderr" && [[ ! -e "$dir/curl.argv" ]]; then
    ok "nrql refuses an unresolved placeholder before any request (exit 2)"
else
    bad "nrql unresolved placeholder (exit $RC)"; show "$dir/stderr"
fi

dir="$WORK/nrql-derived"
mkdir -p "$dir"
nrql "$dir" --queries "$Q" --windows "$TD/nrql/windows-labela.tsv" --vars "$VARS_A,R=x" --print
if [[ $RC -eq 2 ]]; then ok "nrql refuses setting a derived placeholder"; else bad "nrql derived override (exit $RC)"; fi

dir="$WORK/nrql-injection"
mkdir -p "$dir"
nrql "$dir" --queries "$Q" --windows "$TD/nrql/windows-labela.tsv" --vars "$VARS_A,POD=x' OR 1=1" --print
if [[ $RC -eq 2 ]]; then ok "nrql refuses a quote in a placeholder value"; else bad "nrql quote in value (exit $RC)"; fi

for mode in "-x" "xtrace-env"; do
    dir="$WORK/nrql-xtrace$mode"
    mkdir -p "$dir"
    RC=0
    if [[ "$mode" == -x ]]; then
        PATH="$FAKE_PATH" FAKE_DIR="$dir" "$TEST_BASH" -x scripts/perf-nrql.sh --queries "$Q" --list >/dev/null 2>"$dir/stderr" || RC=$?
    else
        env PATH="$FAKE_PATH" FAKE_DIR="$dir" SHELLOPTS=xtrace "$TEST_BASH" scripts/perf-nrql.sh --queries "$Q" --list >/dev/null 2>"$dir/stderr" || RC=$?
    fi
    if [[ $RC -eq 2 ]] && grep -q 'refusing to run with xtrace' "$dir/stderr"; then
        ok "nrql refuses to run under xtrace ($mode)"
    else
        bad "nrql xtrace refusal ($mode, exit $RC)"
    fi
done

KEY="fake-user-key-for-offline-tests-0123456789"   # not the real key shape: push protection scans for it
KEY_FILE="$WORK/nr-key"
printf '%s\n' "$KEY" >"$KEY_FILE"
key_leaks() { # DIR [EXTRA_PATH] -> 0 when the key is found anywhere it must not be
    local dir="$1" extra="${2-}"
    if grep -rqF "$KEY" "$dir/stdout" "$dir/stderr" "$dir/curl.argv" "$dir/ps.txt" ${extra:+"$extra"} 2>/dev/null; then
        return 0
    fi
    return 1
}

dir="$WORK/nrql-batch"
mkdir -p "$dir"
FAKE_CURL_RESPONSE="$TD/nrql/response-ok.json" nrql "$dir" --queries "$Q" \
    --windows "$TD/nrql/windows-labela.tsv" --vars "$VARS_A" --out "$dir/out" --account 1234567 --key-stdin <"$KEY_FILE"
want_files="$(awk -F'\t' '{ split($1, id, "/"); print id[1] "\t" $2 }' "$TD/nrql/golden-labela.tsv" | sort -u | wc -l | tr -d ' ')"
got_files="$(find "$dir/out" -name '*.json' | wc -l | tr -d ' ')"
if [[ $RC -eq 0 && "$got_files" == "$want_files" && -f "$dir/out/Q0-labela-S4-r1.json" && -f "$dir/out/Q1a-session.json" \
    && -f "$dir/out/Q18-labela-S5-r1.json" && ! -e "$dir/out/Q18a-labela-S5-r1.json" && ! -e "$dir/out/Q18-labela-S4-r1.json" \
    && ! -e "$dir/out/Q0-labela-S1-r1.json" && -f "$dir/out/Q7-labela-idle-r0.json" && -f "$dir/out/Q9-labela-idle-r0.json" ]]; then
    ok "nrql batch: one raw response per request, named <id>-<run_id>.json ($got_files files)"
else
    bad "nrql batch (exit $RC, $got_files of $want_files files)"; show "$dir/stderr"
fi
if ! key_leaks "$dir" "$dir/out" && grep -qF "API-Key: $KEY" "$dir/curl.stdin" \
    && ! grep -qF "$KEY" "$dir/curl.argv"; then
    ok "nrql key reaches curl only on stdin: not in argv, ps, stdout, stderr or result files"
else
    bad "nrql key handling"
fi
if head -n 1 "$dir/curl.bodies" | jq -e '.variables.accountId == 1234567
    and .variables.nrql == "FROM Metric SELECT uniques(metricName, 1000) WHERE service.name = '"'demo-service'"' AND service.version = '"'v1.0.0-labela'"' SINCE 1 day ago"
    and (.query | test("nrql\\(query: \\$nrql"))' >/dev/null; then
    ok "nrql sends NRQL as a GraphQL variable (no string splicing into the query)"
else
    bad "nrql request body"; head -n 1 "$dir/curl.bodies"
fi
# shellcheck disable=SC2016 # literal GraphQL variable names
if grep -F 'Q18a: nrql(query: $q0' "$dir/curl.bodies" | jq -e '
    (.query | test("Q18b: nrql\\(query: \\$q1")) and (.variables.q0 | test("AS rps") and test("AS p95") and test("AS pct5xx"))
    and (.variables.q1 | test("AS mcores") and test("AS throttle")) and (.variables.q0 | test("TIMESERIES 1 minute"))' >/dev/null; then
    ok "nrql sends a group (Q18) as one request with each query aliased by its id"
else
    bad "nrql grouped Q18 request"; grep -F 'Q18a' "$dir/curl.bodies" | head -n 1
fi

dir="$WORK/nrql-errors"
mkdir -p "$dir"
FAKE_CURL_RESPONSE="$TD/nrql/response-errors.json" nrql "$dir" \
    --query "FROM Metric SELECT count(*) SINCE 1 hour ago" --account 1234567 --key-stdin <"$KEY_FILE"
if [[ $RC -eq 1 ]] && grep -q 'GraphQL errors' "$dir/stderr" && ! key_leaks "$dir"; then
    ok "nrql exits 1 on a GraphQL errors member"
else
    bad "nrql GraphQL errors (exit $RC)"; show "$dir/stderr"
fi

dir="$WORK/nrql-http"
mkdir -p "$dir"
FAKE_CURL_RESPONSE="$TD/nrql/response-ok.json" FAKE_CURL_CODE=500 nrql "$dir" \
    --query "FROM Metric SELECT count(*) SINCE 1 hour ago" --account 1234567 --key-stdin <"$KEY_FILE"
if [[ $RC -eq 1 ]] && grep -q 'HTTP 500' "$dir/stderr"; then ok "nrql exits 1 on a non-200"; else bad "nrql HTTP 500 (exit $RC)"; fi

dir="$WORK/nrql-batch-errors"
mkdir -p "$dir"
FAKE_CURL_RESPONSE="$TD/nrql/response-errors.json" nrql "$dir" --queries "$Q" \
    --windows "$TD/nrql/windows-labela.tsv" --vars "$VARS_A" --out "$dir/out" --account 1234567 --key-stdin <"$KEY_FILE"
if [[ $RC -eq 1 && -z "$(find "$dir/out" -name '*.json')" && -f "$dir/out/Q0-labela-S4-r1.json.err" ]]; then
    ok "nrql batch: failed requests exit 1 and keep the response as <file>.err only"
else
    bad "nrql batch errors (exit $RC)"
fi

# Result and error never coexist. Direction 1: an earlier .json, then a failure.
# An archive from the same (pinned) second already exists: the move must not
# overwrite it.
dir="$WORK/nrql-stale"
mkdir -p "$dir/out/attempts"
cp "$TD/nrql/stale-result.json" "$dir/out/Q0-labela-S4-r1.json"
cp "$TD/nrql/archived-result.json" "$dir/out/attempts/Q0-labela-S4-r1-20261014T000000Z.json"
PERF_NRQL_STAMP=20261014T000000Z FAKE_CURL_RESPONSE="$TD/nrql/response-errors.json" nrql "$dir" --queries "$Q" \
    --windows "$TD/nrql/windows-labela.tsv" --vars "$VARS_A" --out "$dir/out" --account 1234567 --key-stdin <"$KEY_FILE"
moved="$dir/out/attempts/Q0-labela-S4-r1-20261014T000000Z-1.json"
if [[ $RC -eq 1 && ! -e "$dir/out/Q0-labela-S4-r1.json" && -f "$dir/out/Q0-labela-S4-r1.json.err" && -f "$moved" ]] \
    && grep -q 'stale-capture-marker' "$moved" \
    && grep -q 'earlier-archive-marker' "$dir/out/attempts/Q0-labela-S4-r1-20261014T000000Z.json"; then
    ok "nrql failure moves an earlier result to attempts/ (with -1 when the name is taken) before writing .err"
else
    bad "nrql stale result on failure (exit $RC)"; find "$dir/out" | head -n 20
fi
# Direction 2: an earlier .err, then a success.
cp "$TD/nrql/stale-error.json" "$dir/out/Q0-labela-S4-r1.json.err"
FAKE_CURL_RESPONSE="$TD/nrql/response-ok.json" nrql "$dir" --queries "$Q" \
    --windows "$TD/nrql/windows-labela.tsv" --vars "$VARS_A" --out "$dir/out" --account 1234567 --key-stdin <"$KEY_FILE"
if [[ $RC -eq 0 && -f "$dir/out/Q0-labela-S4-r1.json" && ! -e "$dir/out/Q0-labela-S4-r1.json.err" \
    && -z "$(find "$dir/out" -maxdepth 1 -name '*.err')" ]] && jq -e '.data.actor.account.nrql.results' "$dir/out/Q0-labela-S4-r1.json" >/dev/null; then
    ok "nrql success removes a stale .err: result and error never coexist"
else
    bad "nrql stale error on success (exit $RC)"; find "$dir/out" -maxdepth 1 | head -n 20
fi

dir="$WORK/nrql-keychain"
mkdir -p "$dir"
FAKE_NR_KEY="$KEY" FAKE_CURL_RESPONSE="$TD/nrql/response-ok.json" USER=perf-tester nrql "$dir" \
    --graphql '{ actor { accounts { id name } } }' --region EU
if [[ $RC -eq 0 ]] && grep -q 'https://api.eu.newrelic.com/graphql' "$dir/curl.argv" && ! key_leaks "$dir" \
    && grep -qF "API-Key: $KEY" "$dir/curl.stdin" && jq -e '.data' "$dir/stdout" >/dev/null; then
    ok "nrql --graphql reads the key from the Keychain and honours --region EU"
else
    bad "nrql keychain/--graphql (exit $RC)"; show "$dir/stderr"
fi

dir="$WORK/nrql-empty-key"
mkdir -p "$dir"
nrql "$dir" --query "FROM Metric SELECT count(*)" --account 1 --key-stdin </dev/null
if [[ $RC -eq 1 && ! -e "$dir/curl.argv" ]]; then ok "nrql refuses an empty key"; else bad "nrql empty key (exit $RC)"; fi

if [[ $HAVE_RUBY -eq 1 ]]; then
    dir="$WORK/nrql-parse"
    mkdir -p "$dir"
    nrql "$dir" --queries "$Q" --list
    ruby -ryaml -e '
        d = YAML.load_file(ARGV[0])
        d["derived"].each { |k, v| puts ["D", k, v].join("\t") }
        d["queries"].each { |q| puts ["Q", q["id"], q["scope"] || "window", (q["scenarios"] || "all").to_s, q["when"] || "-", q["group"] || "-", q["nrql"]].join("\t") }
    ' "$Q" >"$dir/ruby.tsv"
    if [[ $RC -eq 0 ]] && diff -u "$dir/ruby.tsv" "$dir/stdout" >"$dir/diff"; then
        ok "nrql: the awk parser reads queries.yaml exactly as a YAML parser does ($(grep -c '^Q' "$dir/stdout") queries)"
    else
        bad "nrql parser vs YAML"; show "$dir/diff"
    fi
else
    skip "nrql parser vs YAML (ruby not installed)"
fi

# ---------------------------------------------------------------------------
section "loadtests: per-request URLs carry a fixed name tag"
# ---------------------------------------------------------------------------

# k6 tags every HTTP sample with the full URL (as both url and name) unless the
# request sets a name tag, which then replaces the URL in both. A URL that
# interpolates an id or a page number therefore opens new time series on every
# request: a 400 rps get-by-id run over a 250k-row seed passed 400k series and
# k6 was OOMKilled at its 1Gi limit.
#
# untagged_url_requests FILE -> "FILE:LINE" for each http.get/post/put/del whose
# URL is a template literal with ${...}, bare or as a call's first argument
# (getURL(...)), or a variable whose last assignment earlier in the same
# top-level function was one, and whose params carry no quoted name tag. The
# last line is "sites N", the number of such requests found, so a parser that
# matches nothing cannot pass.
#
# The file is read as one text, so line breaks inside a call or a declaration do
# not matter. Two copies of it keep the same offsets: src blanks comments, code
# also blanks the contents of string and template literals, so parens, "=" and
# "http.get(" inside them are not structure. Declarations may carry a type
# (const url: string = ...); plain reassignments (url = ...) count too.
untagged_url_requests() {
    awk -v q="'" '
        # dynamic(s): s starts with a template literal that interpolates ${...},
        # bare or as the first argument of a call such as getURL(...).
        function dynamic(s) {
            return s ~ ("^[[:space:]]*(" ID "[[:space:]]*[(][[:space:]]*)?`[^`]*[$][{]")
        }
        # closing(i): offset in code of the ")" that closes the "(" just before i.
        function closing(i,    d, n, c) {
            n = length(code)
            for (d = 1; i <= n; i++) {
                c = substr(code, i, 1)
                if (c == "(") d++
                else if (c == ")" && --d == 0) return i
            }
            return n + 1
        }
        function lineof(p,    t) { t = substr(code, 1, p); return gsub(/\n/, "", t) }
        BEGIN { ID = "[A-Za-z_$][A-Za-z0-9_$]*" }
        {
            file = FILENAME
            if (st == q || st == "\"") st = ""   # only /* */ and template literals span lines
            nc = ""; cd = ""; n = length($0)
            for (i = 1; i <= n; i++) {
                c = substr($0, i, 1); c2 = substr($0, i, 2)
                if (st == "*") {
                    if (c2 == "*/") { st = ""; c = "  "; i++ } else c = " "
                    nc = nc c; cd = cd c
                } else if (st != "") {
                    if (c == "\\" && i < n) { nc = nc c2; cd = cd "  "; i++; continue }
                    if (c == st) st = ""
                    nc = nc c; cd = cd (st == "" ? c : " ")
                } else if (c2 == "//") {
                    c = substr($0, i); gsub(/./, " ", c); nc = nc c; cd = cd c; break
                } else if (c2 == "/*") {
                    st = "*"; nc = nc "  "; cd = cd "  "; i++
                } else {
                    if (c == q || c == "\"" || c == "`") st = c
                    nc = nc c; cd = cd c
                }
            }
            src = src "\n" nc; code = code "\n" cd
        }
        END {
            FN = "\n(export[[:space:]]+)?(default[[:space:]]+)?(async[[:space:]]+)?function[^A-Za-z0-9_$]"
            SET = "[^A-Za-z0-9_$.]((const|let|var)[[:space:]]+" ID "([[:space:]]*:[^=;]*)?|" ID ")[[:space:]]*=[^=>]"
            CALL = "http[.](get|post|put|del)[(]"
            NAME = "tags[[:space:]]*:[[:space:]]*[{]([^}]*[^}A-Za-z0-9_$])?name[[:space:]]*:[[:space:]]*[\"" q "]"
            for (off = 1; match(substr(code, off), FN "|" SET "|" CALL); ) {
                s = off + RSTART - 1; e = s + RLENGTH - 1; tok = substr(code, s, RLENGTH); off = e
                if (tok ~ /^http/) {
                    off = closing(e + 1)
                    args = substr(src, e + 1, off - e - 1); argc = substr(code, e + 1, off - e - 1)
                    off++
                    if (!match(argc, /[^[:space:]]/)) continue
                    first = substr(argc, RSTART); url = substr(args, RSTART)
                    if (first ~ ("^" ID "[[:space:]]*(,|$)")) {
                        match(first, "^" ID); id = substr(first, 1, RLENGTH)
                        url_dyn = (id in dyn) ? dyn[id] : 0
                    } else {
                        url_dyn = dynamic(url)
                    }
                    if (!url_dyn) continue
                    sites++
                    if (argc !~ NAME) print file ":" lineof(s)
                } else if (tok ~ ("^" FN "$")) {
                    split("", dyn)
                } else {
                    id = substr(tok, 2); sub(/^(const|let|var)[[:space:]]+/, "", id)
                    match(id, "^" ID); id = substr(id, 1, RLENGTH)
                    dyn[id] = dynamic(substr(src, e))   # e: the first character after "="
                }
            }
            print "sites " sites + 0
        }
    ' "$1"
}

# check_name_tags FILE MIN_SITES: ok when every per-request URL in FILE carries
# a name tag and at least MIN_SITES such requests were found.
check_name_tags() {
    local file="$1" min="$2" out sites missing
    out="$(untagged_url_requests "$file")"
    sites="$(sed -n 's/^sites //p' <<<"$out")"
    missing="$(grep -v '^sites ' <<<"$out" || true)"
    if [[ -n "$missing" ]]; then
        bad "$file: requests with a per-request URL and no fixed name tag (k6 makes a time series per URL): ${missing//$'\n'/ }"
    elif (( sites < min )); then
        bad "$file: found $sites per-request URL requests, expected at least $min (the parser no longer sees them; update it or this floor)"
    else
        ok "$file: all $sites per-request URL requests carry a fixed name tag"
    fi
    return 0
}

check_name_tags loadtests/products-read-only.ts 3
check_name_tags loadtests/products-crud.ts 4

# The check must bite: drop the first name tag from a copy and expect exactly
# that request back.
awk -v q="'" '!done && sub(", name: " q "[^" q "]*" q, "") { done = 1 } { print }' \
    loadtests/products-crud.ts >"$WORK/products-crud-untagged.ts"
mutant="$(untagged_url_requests "$WORK/products-crud-untagged.ts" | grep -v '^sites ' || true)"
if [[ "$mutant" == "$WORK/products-crud-untagged.ts:"* && "$(wc -l <<<"$mutant")" -eq 1 ]]; then
    ok "name-tag check flags a request whose name tag was removed (line ${mutant##*:})"
else
    bad "name-tag check missed a removed name tag: '${mutant:-nothing flagged}'"
fi

# Shapes the two scripts do not use today but a new request could. A request
# marked "// flag" must be reported, "// site" is a per-request URL with a name
# tag, and an unmarked request has a fixed URL and must not count.
shapes="$WORK/name-tag-shapes.ts"
cat >"$shapes" <<'EOF'
function fixedURLs(id: string) {
  const healthURL = getURL('/health');
  http.get(healthURL);
  http.get(getURL('/ready'), { headers });
  let url = getURL(`/products/${id}`);
  url = getURL('/health');
  http.get(url, { headers });
}

function argumentOnNextLine(id: string) {
  const url = getURL(`/products/${id}`);
  http.get( // flag
    url,
    { headers, tags: { endpoint: 'get_product' } },
  );
  http.get( // flag
    getURL(`/products/${id}`),
    { headers },
  );
}

function templateOnNextLine(id: string) {
  const url = getURL(
    `/products/${id}`,
  );
  http.get(url, { headers }); // flag
  const other =
    `${base}/products/${id}`;
  http.del(other, null, { headers }); // flag
}

function spaceTypeAndReassignment(id: string) {
  http.get(getURL( `/products/${id}`), { headers }); // flag
  const typed: string = getURL(`/products/${id}`);
  http.put(typed, '{}', { headers }); // flag
  let url = getURL('/health'); url = getURL(`/products/${id}`);
  http.get(url, { headers }); // flag
}

function commentsAndStrings(id: string) {
  const url = getURL(`/products/${id}`); // http.get(url) in a comment is not a request
  http.get(url, { headers /* tags: { name: 'x' } */ }); // flag
  http.get(url, { headers, tags: { endpoint: 'get_product', name: 'GET /products/:id' } }); // site
  http.post( // site
    `${base}/products/${id}`,
    JSON.stringify({ note: 'a ) paren and an = sign' }),
    { headers, tags: { name: 'POST /products/:id' } },
  );
}
EOF
want_flags="$(grep -n '// flag$' "$shapes" | cut -d: -f1 | sed "s|^|$shapes:|")"
want_sites="$(grep -cE '// (flag|site)$' "$shapes")"
got="$(untagged_url_requests "$shapes")"
got_flags="$(grep -v '^sites ' <<<"$got" || true)"
if [[ "$got_flags" == "$want_flags" && "$got" == *$'\n'"sites $want_sites" ]]; then
    ok "name-tag check reads multi-line calls and declarations, typed declarations, reassignments, comments and strings"
else
    bad "name-tag check misread the shapes fixture: want sites $want_sites and lines ${want_flags//$'\n'/ }; got ${got//$'\n'/ }"
fi

# ---------------------------------------------------------------------------
section "local gates: shellcheck, gitignore, environment-specific names"
# ---------------------------------------------------------------------------

HARNESS_SCRIPTS=(scripts/perf-k8s-run.sh scripts/perf-watchdog.sh scripts/perf-nrql.sh scripts/test-perf-harness.sh
    scripts/lib/perf-common.sh)
while IFS= read -r f; do HARNESS_SCRIPTS+=("$f"); done < <(find scripts/testdata/perf/bin-fake scripts/testdata/perf/bin-forbidden scripts/testdata/perf/bin-watch -type f | sort)
if command -v shellcheck >/dev/null 2>&1; then
    if shellcheck -x "${HARNESS_SCRIPTS[@]}" >"$WORK/shellcheck.out" 2>&1; then
        ok "shellcheck clean (${#HARNESS_SCRIPTS[@]} scripts)"
    else
        bad "shellcheck"; show "$WORK/shellcheck.out"
    fi
else
    skip "shellcheck not installed"
fi

HARNESS_FILES=("${HARNESS_SCRIPTS[@]}" loadtests/k8s/k6-job.yaml loadtests/k8s/psql-job.yaml loadtests/k8s/README.md
    loadtests/k8s/.env.example loadtests/nrql/queries.yaml)
while IFS= read -r f; do HARNESS_FILES+=("$f"); done < <(find scripts/testdata/perf -type f | sort)
if command -v git >/dev/null 2>&1 && git rev-parse --git-dir >/dev/null 2>&1; then
    ignored="$(git check-ignore "${HARNESS_FILES[@]}" || true)"
    if [[ -z "$ignored" ]]; then ok "git check-ignore: no harness file is ignored (${#HARNESS_FILES[@]} files)"; else bad "ignored by .gitignore: $ignored"; fi
else
    skip "git check-ignore (not a git checkout)"
fi

# Names of one particular deployment must never land in the public harness.
LEAK_RE='novopayment|npa-[0-9]|ng-tst|(^|[^a-z0-9])tst([^a-z0-9]|$)|backend-go|rabbitmq-test|nr-k8s-otel-collector|tailscale|nvpm|sandbox-aurora|us-east-1|cifra|(^|[^.a-z])newrelic([^.a-z]|$)'
leaks="$(grep -nEi "$LEAK_RE" "${HARNESS_FILES[@]}" | grep -v 'LEAK_RE=' || true)"
if [[ -z "$leaks" ]]; then ok "no environment-specific names in the harness files"; else bad "environment-specific names:"; printf '%s\n' "$leaks"; fi

echo
echo "perf harness tests: $PASS passed, $FAILED failed, $SKIPPED skipped"
[[ $FAILED -eq 0 ]]
