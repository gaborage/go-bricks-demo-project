# In-cluster k6 harness

Templates and tooling for running the k6 suite **inside** a Kubernetes cluster, next
to the app, so no ingress, gateway or VPN sits in the measured path. It is built for
relative comparisons, for example the same app on two framework versions under the
same open-model load. It does not give end-user latency.

| File | What it is |
|---|---|
| `k6-job.yaml` | One k6 rep as a Job: an optional warm-up invocation, then the measured invocation |
| `psql-job.yaml` | The products reset/seed as a Job, running SQL from a ConfigMap |
| `.env.example` | Every setting the runner reads, with dummy values |
| [`scripts/perf-k8s-run.sh`](../../scripts/perf-k8s-run.sh) | Renders, applies and waits for either Job, then applies the validity gates |
| [`scripts/perf-watchdog.sh`](../../scripts/perf-watchdog.sh) | Session guard (`watch`) and adaptive cooldown (`wait-idle`) |
| [`scripts/perf-nrql.sh`](../../scripts/perf-nrql.sh) + [`../nrql/queries.yaml`](../nrql/queries.yaml) | Server-side capture from New Relic per rep window |
| [`scripts/test-perf-harness.sh`](../../scripts/test-perf-harness.sh) | Offline tests (`make perf-harness-test`) |

Each script's header documents its flags and exit codes.

## Rendering

`perf-k8s-run.sh` renders both templates with `envsubst` and an **explicit variable
list**. Only the placeholders below are substituted. The Job's own shell (`$rc`,
`$(date ...)`, `$HARNESS_*`) is not in the list and reaches the pod untouched. The runner
refuses to render a listed placeholder that is empty. To inspect the output without
touching a cluster:

```bash
scripts/perf-k8s-run.sh --env my.env --render-only --kind k6 --label dryrun --scenario S4 --rep 0 \
  --script products-read-only.ts --rate 5 --warm 0 --duration 30s
scripts/perf-k8s-run.sh --env my.env --render-only --kind reset
```

Pipe either command into `kubectl apply --dry-run=server -f -` for an admission check.

### `k6-job.yaml` placeholders

| Placeholder | Set by the runner from |
|---|---|
| `${JOB_NAME}` | `k6-<run id>-<UTC stamp>`, lowercased and cut to 63 characters |
| `${NAMESPACE}` | `NS` |
| `${RUN_ID_LABEL}` | `<label>-<scenario>-r<rep>` (also the `perf-run-id` label) |
| `${K6_IMAGE}` | `K6_IMAGE`: pin a k6 2.x digest (tested with 2.3.0); the image must ship `/bin/sh` |
| `${ACTIVE_DEADLINE_SECONDS}` | warm + duration + 300. For a `PERF_STEPS` staircase the duration is the sum of the step durations plus a 15s ramp per step |
| `${K6_RUN_AS_USER}` | `K6_RUN_AS_USER` (default `12345`, the UID of the k6 image) |
| `${K6_NODE_SELECTOR}` | JSON. `K6_NODEPOOL` sets `karpenter.sh/nodepool` and `K6_ZONE` sets `topology.kubernetes.io/zone`; with neither, `{}` |
| `${K6_AFFINITY}` | JSON. With `APP_SELECTOR` set, a required pod anti-affinity to the app pods per hostname; otherwise `{}` |
| `${K6_ENV}` | JSON env list. Every `$` is doubled, because the kubelet expands `$(VAR)` in env values |
| `${SCRIPTS_CONFIGMAP}` | `k6-scripts-<HARNESS_TAG>`, mounted at `/scripts` |
| `${TYPES_CONFIGMAP}` | `k6-types-<HARNESS_TAG>`, mounted at `/scripts/types` |
| `${K6_EPHEMERAL_REQUEST}` / `${K6_EPHEMERAL_LIMIT}` | `K6_EPHEMERAL_REQUEST` / `K6_EPHEMERAL_LIMIT`: ephemeral-storage request and limit (defaults `256Mi` / `1Gi`) |

The env list carries `K6_BASE_URL` (`TARGET_URL`), `K6_API_PREFIX` (`API_PREFIX`),
`PERF_RUN_ID`, `PERF_HARNESS_TAG`, `PERF_RATE`, `PERF_DURATION`, `PERF_SUMMARY_STDOUT=1`,
`PERF_SUMMARY_FILE`, every `--set K=V`, and the wrapper's `HARNESS_SCRIPT`,
`HARNESS_WARM`, `HARNESS_WARM_RATE` and `HARNESS_K6_FLAGS`. The Job is fixed at
`backoffLimit: 0`, `restartPolicy: Never` and `ttlSecondsAfterFinished: 86400`. It is
labelled `app.kubernetes.io/part-of=k6-perf` and `app.kubernetes.io/component=load-generator`
(the reset Job: `component=reset`), and annotated `karpenter.sh/do-not-disrupt:
"true"`. Resources are requests 1 CPU / 512Mi and limits 2 CPU / 1Gi, plus the
ephemeral-storage pair above. It runs as
non-root with seccomp `RuntimeDefault` and a read-only root filesystem (`/tmp` is an
`emptyDir`).

### `psql-job.yaml` placeholders

| Placeholder | Set by the runner from |
|---|---|
| `${JOB_NAME}` / `${SQL_CONFIGMAP}` | `perf-reset-<stamp>` / `perf-reset-sql-<stamp>` |
| `${NAMESPACE}` | `NS` |
| `${PSQL_IMAGE}` | `PSQL_IMAGE`: a pinned `postgres` image digest |
| `${PSQL_RUN_AS_USER}` | `PSQL_RUN_AS_USER` (default `70`, the postgres UID in the alpine image) |
| `${DB_SECRET}` | `DB_SECRET`: the Secret the app imports, used here with `envFrom` |
| `${PG_HOST_KEY}` `${PG_PORT_KEY}` `${PG_DB_KEY}` `${PG_USER_KEY}` `${PG_PASSWORD_KEY}` | Key names inside that Secret, mapped onto `PGHOST`, `PGPORT`, `PGDATABASE`, `PGUSER` and `PGPASSWORD` as `$(KEY)` references |
| `${PG_CA_CONFIGMAP}` / `${PG_CA_KEY}` | The ConfigMap and key holding the database CA bundle (`PGSSLMODE=verify-full`) |
| `${PSQL_EPHEMERAL_REQUEST}` / `${PSQL_EPHEMERAL_LIMIT}` | `PSQL_EPHEMERAL_REQUEST` / `PSQL_EPHEMERAL_LIMIT` (defaults `64Mi` / `256Mi`) |

The SQL comes from `scripts/perf-reset-products.sh --print-sql --rows N --watermark TS
[--clear-outbox] [--drop-seed]`. The runner stores it in a per-run ConfigMap. psql runs
quiet with tuples only, so the log holds one line:
`COUNT products=<n> seed=<n> outbox_pending=<n> last_analyze=<ts|null>`. The reset
gate requires `products = BASE_ROWS + rows` (or `BASE_ROWS` after `--drop-seed`),
`seed = rows` and `outbox_pending = 0`. A mismatch exits 3.

## Runner contract

```bash
scripts/perf-k8s-run.sh --env my.env --load-configmaps perf-harness-v1          # two ConfigMaps + sha256 manifest
scripts/perf-k8s-run.sh --env my.env --kind k6 --label base --scenario S4 --rep 1 \
  --script products-read-only.ts --rate 150 --warm 2m --duration 10m \
  --set PERF_READ_MIX=get --out results/base                                     # exit 0 valid, 3 void, 1 error, 2 usage
scripts/perf-k8s-run.sh --env my.env --kind reset --rows 250000 --watermark 2026-01-01T00:00:00Z --out results/base
scripts/perf-k8s-run.sh --env my.env --kind idle --label base --rep 0 --duration 10m --out results/base
```

The Job's wrapper prints `WARM_START=` and `WARM_RC=` (both only when there is a
warm-up), then `RUN_START=`, `RUN_END=` and `RC=`, each on a line of its own. The
warm-up runs with `--no-thresholds` and without the `PERF_SUMMARY_*` env, so it prints
no summary markers. The k6 summary sits between
`---PERF-SUMMARY-BEGIN---` and `---PERF-SUMMARY-END---` as one compact JSON line, with
a `perfMeta` block. The runner writes:

```
<out>/<scenario>-r<n>.log        full Job log
<out>/k6/<scenario>-r<n>.json    the summary line
<out>/windows.tsv                run_id scenario rep warm_start run_start run_end rc valid reason
<out>/facts/<scenario>-r<n>.env  inputs of the cluster-state gates
<out>/reset-<stamp>.log          reset counts
<out>/attempts/                  an earlier attempt of the same rep, moved aside on re-run
                                 (<name>-<stamp>[-N].<ext>; -N avoids a same-second clash)
```

`run_id` is `<label>-<scenario>-r<n>`. `reason` is empty, or `;`-joined codes from
`dropped`, `rate`, `429`, `5xx`, `void`, `pod`, `node`, `k6cpu`, `acu`,
`missing-summary` and `warmup`. `warmup` means a warm-up started but its `WARM_RC` is
missing or not 0, so the measured run started cold; `--warm 0` skips the warm-up and
the gate. The gates are listed in the runner's header. A gate whose input is
missing (no `APP_SELECTOR`, no metrics-server, no `ACU_WARM`) is logged as not
evaluated, and does not void the rep.

The `5xx` gate voids a rep at 0.1% of `http_reqs` or more. It reads the 5xx count from
the suite's `perf_http_5xx` counter, which the k6 scripts add whenever `PERF_RATE` or
`PERF_STEPS` is set. Without that counter it falls back to `http_req_failed` and logs a
warning: that count also includes 4xx responses and network errors, so it can void a
run with no 5xx, but it never under-counts them. `http_reqs{status:NNN}` submetrics
are not used, because k6 only creates the ones a threshold names.

**Idle window.** Record one per label, with no load running, before the measured reps:
`--kind idle --label <label> --rep 0 --duration 10m` waits through the window and writes
the row `<label>-idle-r0` with scenario `idle` and its `run_start`/`run_end`. No Job
runs. The New Relic capture then queries that window like any other, and idle
subtraction reads its `Q7` (app CPU) and `Q9` (allocations) files.

**Re-runs.** No tool here re-queues a rep. Running a rep or an idle window again
appends another row with the same `run_id`, and the last row per `run_id` is the one
that counts, for the capture and for reporting. The staircase (`S5`) is read from its
last attempt whether valid or void, because a k6 abort there marks the knee: do not
re-queue a void staircase.

## Watchdog

`perf-watchdog.sh watch` runs for the whole session. It needs `kubectl get`/`top` and
`aws cloudwatch get-metric-statistics`, and nothing else. Its only action is `kubectl
delete job -l <selector>`, and it kills load Jobs only. Every rule writes one line to the
void log:

```
VOID <ISO-8601 UTC> rule=<rule> scope=<rep|session> action=<kill|stop> detail=<text>
```

The runner voids any rep whose window contains such a line. The watchdog enforces:

- **Scale-out:** replicas or HPA above 1.
- **App pod:** a restart, OOMKill or NotReady, or the pod disappearing.
- **App node:** the node changing, a disruption or interruption on it, a pressure
  condition, or CPU above the threshold on two samples in a row.
- **Shared dependencies:** broker memory, and the Aurora ACU ceiling.
- **Time:** the session end.

**Selectors.** Two label selectors, for two jobs:

| Selector | Matches | Use it for |
|---|---|---|
| `app.kubernetes.io/part-of=k6-perf,app.kubernetes.io/component=load-generator` | k6 Jobs only | the watchdog's `--job-selector`, and a kill switch that stops load but leaves a running reset alone |
| `app.kubernetes.io/part-of=k6-perf` | every perf Job and ConfigMap, reset included | session cleanup: `kubectl delete job,configmap -l app.kubernetes.io/part-of=k6-perf` |

A reset Job carries `component=reset`, so the watchdog neither counts it as running load
nor deletes it. A rule that fires during a reset still writes its VOID line. `watch`
refuses to start (exit 2) unless one comma-separated term of `--job-selector` is
exactly `app.kubernetes.io/component=load-generator` (`==` and spaces are accepted), so
the broad `part-of=k6-perf` selector, or a lookalike label, cannot reach a reset Job.

Run either subcommand with `--help` for its flags; all of them are required. The 429, error-rate and p95 aborts
belong to k6 thresholds; dropped iterations and achieved rate to the runner gates. New
Relic dropped data points (`Q17a`) and collector memory belong to `perf-nrql.sh`. Other-tenant
database load and queue growth are not visible through those read-only commands.

`wait-idle` returns once the app pod's CPU (`kubectl top`) has stayed within
`--margin` millicores of `--idle-millicores` for `--samples` samples, after at least
`--min`. It exits 1 when that has not happened by `--max`. Without metrics-server it
logs a notice and waits a fixed 5 minutes.

## New Relic capture

```bash
security add-generic-password -a "$USER" -s nr-user-key -w     # once: the User key lives in the Keychain
export PERF_NRQL_VARS="SERVICE=...,ROUTE_PREFIX=...,NS=...,CONTAINER=...,BROKER_NS=...,NR_NS=...,NR_COLLECTOR=..."
scripts/perf-nrql.sh --queries loadtests/nrql/queries.yaml --windows results/base/windows.tsv --trim 1m \
  --vars "V=<service.version>,POD=<pod>,NODE=<node>,CNS=<attr>,CCT=<attr>,CPOD=<attr>,CNODE=<attr>" \
  --out results/base/nr
```

The key only ever reaches curl through `--config -` on stdin, and the script refuses
to run under `set -x`. `--print` shows the exact NRQL without sending it.

The capture takes the last row per `run_id` (with a warning naming the rows it
drops): rows with `valid=1`, plus the staircase `S5` whether valid or void. It refuses
a `run_id` that is not a safe file name, and skips a row whose timestamps do not
convert. Each response is the raw NerdGraph JSON,
`{"data":{"actor":{"account":{"nrql":{"results":[...]}}}}}`, in `<out>/<id>-<run_id>.json`.
A failed request leaves `<id>-<run_id>.json.err` instead, and moves any earlier
`.json` for the same id and run into `<out>/attempts/<id>-<run_id>-<stamp>.json`, with a
`-1`, `-2`, ... suffix when that name is already taken. A later success removes the
`.err`, so a result and an error never sit side by side.
The queries of one `group:` go out as one request, each aliased by its id under
`account`: `Q18-<run_id>.json` holds `Q18a` (per-minute `rps`, `n`, `p95`, `pct5xx`) and
`Q18b` (per-minute `mcores`, `throttle`, `pts`). Every row carries `beginTimeSeconds` and
`endTimeSeconds`. Downstream readers rely on these aliases: `Q2` `rps`/`n`, `Q3`
`p50`/`p95`/`p99`, `Q7` `cpu_s`/`mcores`, `Q8` `throttle`/`ws_avg`/`ws_max`, `Q9`
`bytes`/`objs`.

**Capturing without a User key.** `--print` needs no key: it prints every rendered
query as `<id> TAB <run_id> TAB <nrql>` (a grouped query as `<group>/<id>`). Any
NerdGraph client can run them, for example a New Relic MCP server. Save each response
as `<out>/<id>-<run_id>.json` in the envelope above, and a group as one
`<out>/<group>-<run_id>.json` whose `data.actor.account` holds one object per query id,
each with a `results` array (`{"data":{"actor":{"account":{"Q18a":{"results":[...]},"Q18b":{"results":[...]}}}}}`).
The top level must be the NerdGraph body itself: strip any wrapper the client adds,
and wrap bare rows in the envelope. A body with a top-level `errors` member, or with
no rows, reads as missing. See
[Capturing without a User key](../../wiki/PERF_VERSION_COMPARISON.md#capturing-without-a-user-key)
for what perf-report reads from each file.
