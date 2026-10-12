# Comparing framework versions under load

This page is the method for a relative performance comparison of two or more
go-bricks versions. Each version runs as this demo app, on the same single pod,
under the same open-model k6 load. k6 measures the client side and New Relic
measures the server side.
[`cmd/perf-report`](../cmd/perf-report/main.go) turns the captured files into
`runs.csv` and `REPORT.md`.

The numbers compare the versions with each other only. They are not end-user
latency, and they are not capacity planning figures.

## Measurement validity

- **Relative deltas only.** Every absolute number carries the environment's
  noise: the shared database, the node and its neighbours, the telemetry
  pipeline. Read a result as "B against A, measured the same way".
- **Repeat, then take the median.** One rep is an anecdote. Every scenario runs
  at least 3 measured reps per version, and the report summarizes each as the
  median and the min-max range.
- **Change one thing between versions.** Between two deploys only the
  framework version moves: `go.mod`, `go.sum` and test files. Then compare each
  version with the one before it and with the first.
- **Record before you rebuild.** Write each version's identity (module version,
  image digest, toolchain, telemetry service version) into the series manifest
  before moving on. A version cannot be revisited without a redeploy.

## Controlled variables

Any mismatch in these voids the rep or the session (see
[Abort criteria](#abort-criteria)).

1. **Replicas.** Exactly 1 for the whole series. Pause the autoscaler, re-check
   after every deploy, and sample the count during every rep. More than 1
   voids the rep.
2. **Pod shape.** The same CPU and memory requests and limits in every session.
   Record `GOMAXPROCS`, `GOGC` and `GOMEMLIMIT` and keep them equal. Protect the
   pod from voluntary node disruption for the length of each session.
3. **Node class.** Record the instance type, zone and capacity type in session
   1. A different class voids the session. If that happens twice, pin the class
   with a node selector.
4. **Binary.** One toolchain for every version. Between consecutive deploy
   commits, `go list -deps ./cmd/api` may differ only through `go.mod`,
   `go.sum` and `*_test.go`, and `config.yml` must not change. Pin
   unreleased refs to an exact commit.
5. **Telemetry profile.** Identical in every session:
   - `OBSERVABILITY_METRICS_TEMPORALITY=cumulative`, so every version emits the
     runtime and pool gauges through the same SDK path
   - `OBSERVABILITY_METRICS_HISTOGRAMAGGREGATION=exponential`
   - `OBSERVABILITY_METRICS_INTERVAL=15s`
   - the default trace sample rate, and log export off
6. **App config.** Identical apart from `OBSERVABILITY_SERVICE_VERSION`, which
   names the version. Raise `APP_RATE_LIMIT` far enough that the limiter stays
   in the path but never trips. Never set it to 0: that removes the middleware,
   which is a different code path.
7. **Load.** Open model (constant arrival rate), one k6 image digest, and one
   harness tag for the scripts, with the file hashes recorded. Calibrate the
   rates once, on the first version, and then freeze them.
8. **Data.** A deterministic seed of `N` product rows on top of the demo data:
   for each `g` in `1..N`, the id is `md5('perf-seed-' || g)::uuid`, the name is
   `'Seed Product ' || g`, `created_date` is
   `timestamptz '2025-01-01 00:00:00+00' + g * interval '1 minute'`, and every
   other column is a function of `g`. A reset deletes rows newer than a
   watermark, re-inserts any missing seed row, and checks the count. The outbox
   has 0 pending rows before every rep.
9. **Shared dependencies.** At session start the database capacity is at its
   idle level, other tenants put negligible load on the database, the pods
   next to the app use little CPU, and the telemetry pipeline reports no
   integration errors.
10. **Time.** The same weekday class and clock window in every session, away
    from scheduled scaling events.
11. **Order.** The same session schedule, the same rep order, the same warm-up,
    the same cooldown rule and the same telemetry window trims every time.

## Scenarios

| Id | Script | Shape | Role |
|---|---|---|---|
| S1 | `products-read-only.ts`, `PERF_READ_MIX=mixed` | list and get mix | **P2** (CPU per request) |
| S2 | `products-crud.ts` | create, read, update, delete; reset afterwards | descriptive |
| S3 | `tokens-relay.ts` | nested JOSE relay with its in-process peer | descriptive |
| S4 | `products-read-only.ts`, `PERF_READ_MIX=get`, `PERF_SEED_ROWS=N` | gets by seed id | **P1** (k6 p95) |
| S5 | `products-read-only.ts`, `PERF_STEPS="r:dur,..."` | arrival-rate staircase | descriptive, indicative |

Every measured run sets `PERF_RUN_ID=<run_id>` and aborts on any 429
(`PERF_ABORT_ON_429=1`) or an error rate above 2%
(`PERF_ABORT_ERROR_RATE=0.02`). The staircase also aborts above a p95 of
1000 ms (`PERF_ABORT_P95_MS=1000`) or an error rate above 1%.

## Procedure

One session per version, in this shape:

1. **Deploy.** Only the module pin and the telemetry service version change.
   Wait for the rollout to finish with the pod Ready and 0 restarts.
2. **Identity and diff gates.** `go.mod` shows the expected version. The deploy
   diff touches only `go.mod`, `go.sum`, `*_test.go`, `wiki/`, `loadtests/`,
   `scripts/`, `cmd/perf-report/` and `.github/`. The environment fingerprint
   (every `OBSERVABILITY_*`, `APP_RATE_*` and `OTEL_*` variable except the
   service version) equals session 1's. The build log shows the pinned
   toolchain.
3. **Pin replicas.** Pause the autoscaler at 1 replica and read the count twice,
   2 minutes apart.
4. **Pod, node and neighbours.** The pod is at least 20 minutes old with 0
   restarts, the node class equals session 1's, and every neighbour pod on the
   node uses under 50 millicores.
5. **Shared-infrastructure gates.** Database capacity at idle, other-tenant
   database load low, and a telemetry fingerprint whose `service.version`
   matches the label.
6. **Watchdog.** Start it for the whole session. It enforces the
   [Abort criteria](#abort-criteria) and writes VOID lines.
7. **Idle, reset, warm-up.** Record 10 minutes with no load as the `idle`
   window in `windows.tsv` with `scripts/perf-k8s-run.sh --kind idle`: it
   gives the pod's idle CPU, and perf-report needs it for every per-request
   metric. Reset the data, then run a 10-minute warm-up whose results are
   discarded, and wait for the database capacity to settle.
8. **Measured reps.** One Job per rep: a 2-minute warm-up invocation, then a
   10-minute measured one. Wait for the pod's CPU to return to idle before
   each. The order is a Latin square, identical in every session:

   | Rep | Scenario order |
   |---|---|
   | r1 | S4, S1, S2, S3 |
   | r2 | S1, S2, S3, S4 |
   | r3 | S2, S3, S4, S1 |

   A rep is valid when nothing was dropped, the achieved rate is within 1% of
   the target, there was no 429 and under 0.1% 5xx, no VOID line falls inside
   its window, the pod and node did not change, the k6 container stayed under
   70% of its CPU request, and the database capacity stayed within 1 unit of
   its warm level. A failing SLO threshold in a script is a result, not a void.
   Re-queue a void rep at the end of the schedule, at most twice per scenario,
   and log every attempt.
9. **Staircase.** S5 once per version, after the measured reps, with 3-minute
   steps under a fixed cap. It is indicative only. Its run crosses the
   dropped-iteration and rate gates on purpose, so a void S5 row is still read:
   perf-report uses each label's last S5 attempt, valid or void.
10. **Capture.** At least 5 minutes after the last Job, run
    `scripts/perf-nrql.sh --queries loadtests/nrql/queries.yaml` over the
    label's `windows.tsv` with `--trim 1m`, so each rep's window loses 1 minute
    on each side, then `go run ./cmd/perf-report --series <series> --out <series>`.
11. **Go or no-go.** Every scenario has 3 valid reps. If the reference
    version's spread fails the [rep extension](#statistics) rule, run 2 more
    reps of that scenario now and plan 5 for every later session.
12. **Teardown.** Delete the Jobs, lift the disruption protection, and commit
    the session's directory with the session verdict in the commit body.

**A/A tail.** After the last version, redeploy the first version's image under
a new service version (for example `<label>-aa`) and run S4 and S1, 3 reps each.
The drift between the two runs of the same binary is the series' noise floor.

## Results layout

```
<series>/
  series-manifest.json          labels in comparison order, optional A/A pairing
  runs.csv  REPORT.md           written by perf-report
  <label>/
    windows.tsv                 one row per attempted rep, plus the idle window
    <scenario>-r<n>.log         the Job log (WARM_START=, RUN_START=, RUN_END=, RC= lines)
    k6/<scenario>-r<n>.json     k6 handleSummary data plus a perfMeta block
    facts/<scenario>-r<n>.env   inputs of the runner's pod, node, k6 CPU and capacity gates
    attempts/                   an earlier attempt's files, moved aside when a rep is re-run
    reset-<ts>.log              row counts only
    nr/<query>-<run_id>.json    raw NerdGraph responses, queries Q0..Q18 (Q18 holds
                                Q18a and Q18b, sent as one request)
    nr/<query>-session.json     session-scope queries, once per session
    nr/<file>.json.err          a failed request's response; it replaces <file>.json,
                                and an earlier result moves to nr/attempts/
```

- **`series-manifest.json`** needs `labels`, an array whose first entry is the
  reference: `{"labels":["a","b","c"],"aa":{"a-aa":"a"}}`. `aa` maps an A/A
  label to the label it repeats. perf-report also reads four optional keys:
  `title`, `controls` and `covariates` (arrays of
  `{"name": ..., "values": {"<label>": ...}}` rows for the report's tables),
  and `notes` (an array of strings). It ignores every other key.
- **`windows.tsv`** is tab-separated with exactly this header:
  `run_id scenario rep warm_start run_start run_end rc valid reason`. Times are
  ISO-8601 UTC with a `Z`. `run_id` is `<label>-<scenario>-r<n>`. `valid` is 1
  or 0, and `reason` is empty or a `;`-joined list of `dropped`, `rate`, `429`,
  `5xx`, `void`, `pod`, `node`, `k6cpu`, `acu`, `missing-summary` and
  `warmup` (the rep's warm-up invocation failed, so it ran cold). Only
  valid reps enter the statistics, and every attempted rep appears in the
  validity log.
- **The runner's exit code** is 0 for a valid rep, 3 for a void one, 1 for an
  error and 2 for a usage error.

## perf-report

```bash
go run ./cmd/perf-report --series <series> --out <series>
```

It rebuilds both outputs from every label's files on each run, so running it
after each session adds that session's reps. It uses the standard library
only, and the same files always produce the same bytes.

- **Inputs.** Each rep's k6 summary, and the NR responses for Q2 (rate `rps`
  and count `n` per route), Q3 (latency percentiles per route, and per method
  when the query facets by it), Q7 (`cpu_s` and `mcores`), Q8 (`throttle`,
  `ws_avg`, `ws_max`), Q9 (`bytes`, `objs`) and, for S5, Q18 (the per-minute
  series). A missing metric renders as `n/a`. A missing file, a NerdGraph
  error body or an empty result renders the same, and the validity log lists
  the valid reps it affects. k6's built-in
  `http_req_duration{expected_response:true}` submetric is left out of the
  per-endpoint rows.
- **CPU per request (P2)** is `1000 × (cpu_s − idle_cores × window_s) / n`,
  with `n` summed over Q2's route facets and `idle_cores` from the label's
  `idle` window. `window_s` is read back from the NR data (`n / rps`), and
  falls back to `cpu_s / cores` and then to the `windows.tsv` window minus
  `--trim` (default `1m`) on each side. Allocations per request use the same
  idle subtraction. A label without an idle level, and a value at or below 0
  after subtraction, render `n/a`, with the reason in the validity log.
- **Flags.** A valid rep is flagged `throttle` when CFS throttling reaches 1%,
  and `nr-gap` when the NR request rate differs from k6's by more than 2%. The
  gap leaves out `/__sim/` routes, which k6 never calls directly. A reason code
  the runner wrote on a valid row is kept as a flag too.
- **Staircase.** The first step starts at `perfMeta.startedAt` (k6's setup
  runs after `RUN_START`), or at `RUN_START` when the summary lacks it. Every
  later step starts when the one before it ends plus a separate ramp stage of
  `perfMeta.stepRampSeconds` (15 s when absent). Only the minutes that fall
  wholly inside a step count; ramp minutes count for no step. k6 reports dropped iterations only for the
  whole run, so when that total is above 0 a step counts as "nothing dropped"
  only if NR saw at least 99% of its offered rate.
- **Not computed.** Stationarity, database capacity, neighbour and other-tenant
  flags need data perf-report does not read. Leak slopes for the optional soak
  are read from the raw files by hand.

## Statistics

- **Unit of analysis.** One measured rep: the k6 summary over its 10 measured
  minutes, and NR over the trimmed 8-minute window.
- **Pre-registered primaries.** Only two outcomes get a verdict word:
  - **P1:** S4, k6 `http_req_duration` p95. Framework-bound, and immune to the
    seed's `COUNT(*)`.
  - **P2:** S1, app CPU-ms per request with the idle level subtracted. App CPU
    leaves out the time spent waiting on the database.

  Everything else is descriptive, which keeps dozens of metric and version
  pairs from producing false positives.
- **Per version and scenario.** The median of the valid reps, the min-max
  range, and the relative range `(max − min) / median`.
- **Comparisons.** `Δ% = (median_X − median_ref) / median_ref` for each version
  against the one before it, and from the third version on also against the
  first (derived).
- **Noise band.** `B = max(5%, 2 × relative range of the comparison's
  reference, |Δ(A', A)|)`, where the last term is the A/A drift. The floor is
  10% for p99 metrics.
- **Verdict.** "Changed" only when the two min-max ranges do not overlap and
  `|Δ| > B`. Otherwise the verdict is "no detectable difference (band ±B%)",
  never "no difference". Each arm needs 3 valid reps. No p-values: with 3 reps
  per arm the smallest two-sided Mann-Whitney p is 0.10.
- **Rep extension.** If the reference version's relative range is above 10% for
  P1 or above 5% for P2, run that scenario 5 times in every session. With 5
  reps per arm the report adds the Hodges-Lehmann shift, still descriptive.
- **Flags and the sensitivity rerun.** A flagged rep is kept and marked. The
  primary verdicts are then recomputed without flagged reps, and a verdict that
  flips is labelled "environment-limited". When too few unflagged reps are
  left to rerun, the verdict stands and says so. The method's other flags are
  a per-minute p95 or CPU that moves more than 10% between the halves of the
  window, neighbours above 100 millicores, and other-tenant database load at or
  above 0.2 average active sessions. (Database capacity that moves by more than
  1 unit already voids the rep.)
- **Staircase knee.** The last step, counting from the first, where p95 stays
  under twice the first step's p95, 5xx under 0.1%, nothing is dropped and CPU
  throttling under 25%. Report the knee rate and the CPU at the knee. One rep
  per version, so it is indicative.
- **Leak slope (optional soak).** The least-squares slope of working set and
  goroutines per 10 minutes. Flag it when the slope over 60 minutes exceeds
  10% of the mean and the post-soak idle level does not return within 10% of
  the pre-soak one.

## Abort criteria

The watchdog enforces these, and deleting the k6 Jobs is the kill switch.

| Condition | Action |
|---|---|
| Replicas, or the autoscaler's desired count, above 1 | Kill the Jobs and void the rep. If it repeats, stop the session and fix the pin. |
| App pod restart, NotReady or OOMKilled | Kill and void the rep. Investigate before going on. |
| App node changed, or a spot interruption or node disruption event | Void the session. |
| Node CPU above 90% for 2 samples, or any node pressure condition | Kill and void the rep. |
| Database capacity at the agreed ceiling | Kill. The frozen rate is too high for the shared database: stop and re-plan. Never raise the ceiling mid-series. |
| Other-tenant database load above 0.5 average active sessions during a rep | Void the rep, without a kill. |
| Broker memory above its limit, or a queue the app consumes keeps growing | Kill and void the rep. |
| k6 sees a 429, an error rate above 2% (1% in the staircase), or a staircase p95 above 1000 ms | k6 aborts by itself. Void the rep. In the staircase the abort marks the knee, and the run is still read. |
| Dropped iterations above 0, or an achieved rate more than 1% off target | Void the rep. |
| Telemetry integration errors in the window, or the collector's memory 50% above its baseline | Void the rep: NR may have undercounted. |
| Wall clock past the planned session end | Stop. |
| Fingerprint, service version or environment fingerprint mismatch | Do not start the session. |
