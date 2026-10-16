# go-bricks v0.71.1 → v0.72.0: operator decision record

This record covers the move from go-bricks v0.71.1 to the v0.72.0 tag in three
hops. The first two were pins to go-bricks main, taken ahead of the tag for a
performance test series: commit `8cfb522f` (pseudo-version
`v0.71.2-0.20261011230701-8cfb522fbbaa`), then commit `92883628`
(pseudo-version `v0.71.2-0.20261015181756-92883628ae2a`). Each pin was that
exact commit, not whatever main was later. The third hop moves to the tag,
`v0.72.0` (commit `745a59d7`).
The framework's CHANGELOG stopped at 0.71.1 at both main pins; the tag adds its
0.72.0 section. The changes are the atoms in its migration guide
(`wiki/migrations.md`, section E72): C72.1 to C72.25 for the first hop, C72.26
to C72.35 except C72.34 for the second, and C72.34 and C72.36 for the third.
C72.34 had no entry at `92883628`; it is go-bricks #2127, which landed after it.
This record lists the ones that reach the demo, the decision for each, and what
an operator must do before deploying. The previous record is
[GOBRICKS_V071_UPGRADE.md](GOBRICKS_V071_UPGRADE.md).

## At a glance

### v0.71.1 → `8cfb522f`

- **No non-test source change, no schema migration and no config key.**
  `config.yml` and every non-test `.go` file are unchanged, so the binary's only
  new inputs are `go.mod` and `go.sum`. The other edits are one test file, the
  pins in the Makefile and the docs, and this record.
- **One compile break, in a test (C72.9).** The binary never used the removed
  names.
- **No feature adopted.**
- **Operator must-dos (non-development deploys):** write every OTLP endpoint in
  a form `Validate` accepts; move any `OTEL_EXPORTER_OTLP_*` endpoint or insecure
  variable to `OBSERVABILITY_<SIGNAL>_ENDPOINT` or `_INSECURE` and drop any
  `*_PROTOCOL`; check whether anything in front of the service sends unsampled
  `traceparent` headers; repoint delta-temporality gauge queries; keep the
  keystore variables spelled as they are.

### `8cfb522f` → `92883628`

- **No source change, no schema migration and no required config key.**
  `config.yml` and every `.go` file are unchanged, so the binary's only new
  inputs are `go.mod` and `go.sum`. The other edits are the pins in the
  Makefile and the docs, and this record.
- **No compile or test break.**
- **No feature adopted by the demo's code.** The framework itself now runs the
  outbox relay as a `scheduler.Windowed` job, so the relay's logs and metrics
  change (C72.30).
- **Operator must-dos (non-development deploys):** repoint relay alerts and
  queries, ERROR alerts included, to the window and edge lines or the
  `outbox.relay.*` counters; join job summary lines to traces through
  `trace_id`, not `correlation_id`; drop `OTEL_LOGRECORD_ATTRIBUTE_COUNT_LIMIT`
  if it is set, but not in the middle of a perf series (its row below says why); do
  not compare runtime series (the gauges and the allocation counters) or relay
  log volume across the two pins.

### `92883628` → v0.72.0

- **No source change, no schema migration and no config key.** `config.yml` and
  every `.go` file are unchanged, so the binary's only new inputs are `go.mod`
  and `go.sum`. The other edits are the pins in the Makefile and the docs, one
  script comment, and this record.
- **No compile or test break.** Neither atom, C72.34 nor C72.36, matches a demo
  call site.
- **No feature adopted.** The skipped ones and why are under
  [Features](#features-92883628--v0720).
- **Operator must-dos (non-development deploys):** none new for this hop. A
  deploy that comes from behind `92883628` still owes the earlier hops'
  must-dos above.

## Version path

| Hop | Commit date | What moves in `go.mod` |
|-----|-------------|------------------------|
| v0.71.1 → `8cfb522f` | 2026-10-11 | go-bricks (the only direct change), plus 32 indirect modules: OpenTelemetry 1.46.0 → 1.47.0, `otel/log` and `sdk/log` 0.22.0 → 1.47.0, the OTLP log exporters 0.22.0 → 0.23.0, `go-redis/v9` 9.22.0 → 9.23.0, and smaller bumps |
| `8cfb522f` → `92883628` | 2026-10-15 | go-bricks and `go-jose/v4` 4.1.5 → 4.1.6, which is a direct module (the tokens relays import it), plus 9 indirect modules: `x/crypto` 0.57.0 → 0.58.0, `x/net` 0.60.0 → 0.61.0, `x/text` 0.42.0 → 0.43.0, `grpc-gateway/v2` 2.31.0 → 2.32.0, `klauspost/compress` 1.20.1 → 1.20.3, `go-colorable` 0.1.15 → 0.1.16, the two `genproto/googleapis` modules (`api`, `rpc`), and `go-pkcs12` 0.7.3 → 0.7.4 |
| `92883628` → v0.72.0 | 2026-10-15 | go-bricks only. Its own `go.mod` and `go.sum` are unchanged in `92883628..v0.72.0`, so no other module moves and `go mod tidy` changes nothing further |

The `go` directive (`go 1.27.0`) and the `toolchain` line (`go1.27.2`) are
unchanged at every hop.

## Compile break (v0.71.1 → `8cfb522f`): C72.9 and the cross-module messaging test

C72.9 (ADR-139) removed the exported seal-runtime seam from `messaging`,
including `ConfigureSealing`, `SealingRuntime`, `SealRuntime`, `SealKeyStore`
and `SealTenancyDisabled`. Only `cmd/api/messaging_declarations_test.go` used
them. It configured the sealing runtime with a mock keystore, so the payments
module's seal-tagged publisher and consumer could resolve their key generations
outside an App. The payments module only blank-imports `messaging/sealed`,
which is unchanged.

Now only `app.Run` can configure that runtime, and nothing replaces the test
door. Outside an App, every seal-tagged declaration records the runtime's
not-configured error, and `Validate` reports it before any topology rule, for
the whole set. The test is split:

- The set without payments is validated as before: the default set, the set
  with the partner feed on, and the two defect subtests (a conflicting
  re-declaration and an unknown exchange type).
- A new test declares the full set, payments included. It checks that payments
  contributed its exchange, its three queues, its publisher and its consumer.
  Then it asserts that `Validate` fails with the not-configured error and not
  with `ErrSealingNotLinked`, which proves the `messaging/sealed` codec is
  linked. That error has no exported sentinel, so its text is matched.
- A second new test declares payments alone, in a set of its own. It asserts
  that none of its exchange or queue names appears in the set the other modules
  build, with the partner feed off and on. So a name conflict with another
  module still fails CI.

Coverage lost: only a real boot now proves that the keystore resolves the
payments sign and encrypt generations, and that the payments topology passes the
set-wide rules on C72.13 dead-letter routing and publisher destinations. The
partner feed's DLQ pair uses the same `DeclareQueueWithDLQ` call and still goes
through the C72.13 check in the test.

## Changes that reach this demo: v0.71.1 → `8cfb522f`

| Change | Hits | Decision |
|--------|------|----------|
| C72.9: the seal-runtime seam is framework-only | Yes, one test. | Test reworked, above. |
| C72.13: `Validate` refuses a queue whose dead-letter route cannot park | Payments and the partner feed build their DLQ pairs only through `DeclareQueueWithDLQ`, which declares the whole route. | Keep. |
| C72.11: the opener refuses a clear member whose name case-folds to the sealed Subject | The payments consumer. Both producers (the app and the `seal-event` CLI) seal through go-bricks, which already refuses such a twin. | Keep. |
| C72.7: a pooled lookup that evicts returns after the displaced resource is closed | Always applies. Single-tenant with two databases, so nothing is evicted. | Keep. |
| C72.2 to C72.4, C72.6, C72.12, C72.17 | Not in development: the OTLP export is off and the keystore block holds file sources only. | See the operator table below. |

The other atoms have no match: no `replace` or `OTEL_GO_X_*` variable (C72.1),
no migration audit query (C72.5), no `PublishSealed` call (C72.10), no
multi-tenant app (C72.8, C72.15, C72.25), no cache (C72.18 to C72.24), and no
`database/testing` no-rows text pinned in a test (C72.16).

## Changes that reach this demo: `8cfb522f` → `92883628`

The range is 19 commits. Seven have no atom: three ship to the demo
(`3c9df95e`, `50820b01`, `6869cf37`) and four touch only go-bricks CI and
tests. Some atoms span two commits, so the table is keyed by commit.

| Commit | Atom | Hits | Decision |
|--------|------|------|----------|
| `504b498d`, `2bda7722`: the outbox relay is a `scheduler.Windowed` job | C72.30 | Yes: `outbox.enabled: true`, `pollinterval: 5s`. | Keep. Logs and metrics change; see the operator table. |
| `82b0f7ce`, `c996e5cc`: `scheduler.Windowed`, window lines, edge lines and Counter metrics | C72.28 | Through the relay only. The demo's own `test-job` stays a plain job with its per-run line. New optional key `scheduler.summary.window`. | Keep the 5m default. `config.yml` is unchanged: the perf method forbids a `config.yml` change between deploy commits. |
| `f0a916bc`: job summary `correlation_id` and `traceparent` | C72.26 | Every job summary line: `test-job`, `outbox-cleanup`, `inbox-cleanup` and manual triggers. Nothing in the repo reads either key. | Keep. |
| `3c9df95e`: runtime metrics are read fresh on every export | — | Only with the OTLP metrics export on. The read floor was a fixed 15s and is now half the export interval: 5s at the 10s default (the demo sets no `observability.metrics.interval`), 7.5s under the perf profile's `OBSERVABILITY_METRICS_INTERVAL=15s` ([PERF_VERSION_COMPARISON.md](PERF_VERSION_COMPARISON.md)). | Keep. Runtime series on this pin, the gauges and `go.memory.allocated`/`go.memory.allocations` alike, do not compare with `8cfb522f`. |
| `ba4b4e3e`: the OTel log bridge owns the 128-attribute cap | C72.27 | Only with the OTLP log export on, which development leaves off. No demo log line comes near the cap. | Keep. |
| `50820b01`: Go modules and CI tool pins | — | `go-jose/v4` 4.1.6 runs under the go-bricks `jose` doors and the tokens relays. The demo's keys are 2048-bit DER resolved by kid, with no `jwk` or `epk` header. | Keep. |
| `6869cf37`: `go-pkcs12` 0.7.4 | — | No. It changes the encode path only, and the keystore loads DER. | None. |
| `92883628`: the `f` value doors refuse a `RawExpression`; `BuildUpsert` splices one | C72.33 | No. Every `f.Eq` operand is a string id, `qb.MustExpr` appears only in `Select`, and the demo calls no `BuildUpsert`. | None. |
| `ad9adb38`: the IN doors accept scalars and expressions | C72.32 | No `In` or `NotIn` call. | None. |
| `a407f5a2`: `f.InSubquery` refuses a foreign subquery that renders `$N` or `:N` | C72.35 | No subquery. | None. |
| `eaa97c17`: a hold replay's consume span is a linked root | C72.29 | No. `inbox.hold.enabled` is unset (default `false`) and no stream consumer sets `Hold`. | None. |
| `abd57e9d`, `d1e68f4e`: the inbox hold drain reports once per window | C72.31 | No. The drain is registered only with `inbox.hold.enabled: true`. The payments dedup path (`ProcessOnce`) does not change. | None. |
| `40cce4e8`, `f505d0f6`, `68eb8d17`, `b2dc66a2` | — | No. They touch go-bricks CI, test helpers and its own Flyway CI pin. The demo's compose file pins its own Flyway image. | None. |

**Query building is unchanged.** C72.32, C72.33 and C72.35 change no statement
that runs here. go-bricks' outbox and inbox store sources (`outbox/store_sql.go`,
`outbox/store_postgres.go`, `inbox/store_postgres.go`) and their golden SQL
files (`outbox/testdata/sql/*.golden`, `inbox/testdata/sql/*.golden`) are
unchanged in `8cfb522f..92883628`. A scratch module rendered statements
equivalent to the demo's repository statements, the inbox dedup upsert and the
outbox statements through the changed query-builder doors: the SQL and
arguments were byte-identical under both pins.

**Lock and retry behaviour is unchanged.** The relay still runs as a
`FixedRate` job at `outbox.pollinterval`, behind the scheduler's overlap lock and
its own `FOR UPDATE NOWAIT` leader row, and its retry and poison handling is
the same. Only how it reports moves.

## Changes that reach this demo: `92883628` → v0.72.0

The range is 3 commits: two query-builder fixes and the release commit.

| Commit | Atom | Hits | Decision |
|--------|------|------|----------|
| `1e43eb65` (#2127, ADR-147): a `jf` value door refuses an aliased `RawExpression` with `dbtypes.ErrAliasInPredicate` at `ToSQL()` | C72.34 | No. The demo builds no join filter: no `qb.JoinFilter()`, no `*JoinOn` call, and no `errors.Is` on `ErrInvalidAlias`. Its two `qb.MustExpr` calls sit in `Select`, which still renders `AS <alias>`: [products/repository/repository.go:135](../internal/modules/products/repository/repository.go) (no alias) and [analytics/repository/repository.go:147](../internal/modules/analytics/repository/repository.go) (aliased). | None. |
| `aede5c9d` (#2129): PostgreSQL `BuildUpsert` keeps a `?` in a quoted insert key as written | C72.36 | No. The demo calls no `BuildUpsert`. The framework's two callers at the tag have no `?` in a key: the inbox dedup ledger (`inbox/store_postgres.go:46`: `tenant_id`, `event_id`, `processed_at`) and the hold store (`inbox/hold_store_postgres.go:115`, registered only with `inbox.hold.enabled`, which is unset). The new escaping leaves such keys as they are, so the rendered SQL is byte-identical. | None. |
| `745a59d7`: release 0.72.0 | — | No. CHANGELOG, release manifest and docs. | None. |

Only `database/internal/builder/helpers.go` and `postgres.go` change behaviour in
the range. `database/types` gains the `ErrAliasInPredicate` sentinel and doc
comments; the rest is go-bricks tests and docs (`llms.txt`, `wiki/`, ADR-147).

**CHANGELOG entries that do not reach the demo.** The 0.72.0 CHANGELOG covers the
whole v0.71.1 → v0.72.0 range, so most of its entries are atoms judged in the
earlier hops. These were checked against the repo again at the tag:

- #2065 (`go-bricks-migrate` decodes `--source-config` with the framework's own
  decoder): the Makefile passes `config.multitenant.yaml`, whose only top-level
  keys are `app` and `multitenant`. It holds no keystore block, no seal selector
  and no dotted key for the decoder to refuse.
- #2071 (seal selectors judged from every layer before the merge): the only
  selector is the commented-out `messaging.seal.active` example in `config.yml`
  (lines 176–178). Nothing sets `MESSAGING_SEAL_ACTIVE`.
- #2025 (opt-in at-least-once dead-lettering): `DeadLetterSpec.DeadLetterStrategy`
  is not set. Payments (`internal/modules/payments/module.go:149`) and the partner
  feed (`internal/modules/partnerfeed/module.go:106`) set only `QueueType`.
- #2031 (`WillFailBegin`, recorded `BeginTx` options): additive `database/testing`
  helpers.
- #2021, #2023 (`--shared-migrator`): not in `MULTITENANT_FLAGS`, and nothing sets
  `GOBRICKS_MIGRATE_SHARED_MIGRATOR`.
- #2028, #2032 (SCRAM-SHA-256 `PGRoleSpec` passwords, `ForEachTenant`): the repo
  uses neither.
- #1997 (`observability.metrics.cardinalitylimit`): unset in every config and env
  file, so the OTel SDK default applies.
- #1984 (semconv imports consolidated on v1.43.0): the non-test diff changes
  import paths, comments and one helper call
  (`DeploymentEnvironmentName(…)` → `DeploymentEnvironmentNameKey.String(…)`). No
  attribute constant is renamed, so queries keyed on attribute names keep working.

## Features: `92883628` → v0.72.0

None adopted. Skipped, with the reason:

- **`WillFailBegin` (#2031).** No hand-rolled Begin-failure double exists for it
  to replace: no service test fails a `Begin`. Adopting it would add tests, not
  replace code.
- **At-least-once dead-lettering (#2025).** A new opt-in capability that changes
  the DLQ and retry semantics of the payments and partner feed queues. It
  replaces nothing.
- **ADR-144 dotted keystore names (#2058 to #2072).** They replace no hand-rolled
  code. Renaming the payments sealing families to dotted names changes the wire
  kid, the inner JWE `iss` (the outer sign kid) and the inbox dedup key spelling
  (`<sign family>:<jti>`). That is a drain-then-cutover on the cardholder-data
  path, parked for a separate, explicitly approved change. Until then keep
  `PAYMENTSSIGN-V1` and `PAYMENTSENCRYPT-V1` hyphenated (the `KEYSTORE_KEYS_*` row
  below). Later: done; see [KEYSTORE_DOTTED_NAMES.md](KEYSTORE_DOTTED_NAMES.md).

## Per-environment operator config changes required before deploy

### Development (`config.development.yaml`, docker compose)

None. `TestConfigLayoutDevelopmentLoadsBaseAndOverlay` loads and validates the
base and the development overlay on each pin, keystore block included. On
`92883628` it does so with `scheduler.summary.window` unset, so the 5m default
passes the window check. On v0.72.0 nothing new: `config.development.yaml`,
`config.yml` and the compose file are unchanged, and the app booted on the
docker stack (see Verification).

### Non-development deploy (env vars, per [DEPLOYING.md](DEPLOYING.md))

No new item for `92883628` → v0.72.0: neither atom reads configuration, and no
key or variable in DEPLOYING.md moves. Every row below still applies to a deploy
that comes from behind `92883628`.

| Item | Decision | Before deploy |
|------|----------|---------------|
| `OBSERVABILITY_<SIGNAL>_ENDPOINT` (C72.2, C72.3) | Not set by the demo. | An HTTP endpoint with userinfo, a query, a fragment, an encoded slash or no host, or a gRPC endpoint starting `grpc://`, now fails observability `Validate`. The service still boots, with the WARN `Failed to initialize observability, using no-op provider`, and exports no traces, metrics or logs. Write HTTP endpoints as `scheme://host[:port][/path]` and gRPC endpoints as `host:port`. An HTTP endpoint with a path now exports to exactly that path, so make it the collector's full export path. Check: no such WARN at boot. |
| `OTEL_EXPORTER_OTLP_*` (C72.4) | The demo sets none. | The endpoint, insecure and protocol variables, and their `_TRACES_`, `_METRICS_` and `_LOGS_` variants, are ignored: only `observability.*` config counts. In the same rollout, move each endpoint and insecure value to `OBSERVABILITY_<SIGNAL>_ENDPOINT` or `_INSECURE`. A per-signal HTTP URL moves as it is. The generic `OTEL_EXPORTER_OTLP_ENDPOINT` was a base, so join `/v1/<signal>` onto it: a path is now used exactly (C72.2). An `http://` env URL chose plaintext, so set `_INSECURE=true` with it. A gRPC value drops its `http://` or `https://` scheme and keeps `host:port`. Drop `*_PROTOCOL`: it never chose the transport, which is `observability.<signal>.protocol`. Nothing fails at startup; exports fail at runtime if the collector does not serve the configured endpoint over the configured transport. |
| `OBSERVABILITY_TRACE_SAMPLE_RATE` (C72.6) | Unset, so 1.0. | An inbound `traceparent` marked not sampled (`-00`) now drops this service's spans at every rate, 1.0 included. Check whether a gateway or upstream service sends such headers. If one does, expect fewer spans: no rate restores them. |
| `OBSERVABILITY_METRICS_TEMPORALITY=delta` (C72.12) | Default `cumulative`: no change. | Under `delta` with an OTLP endpoint, UpDownCounters and gauges (`go.goroutine.count`, `go.memory.used`, `db.client.connection.*` and others) now export as cumulative; counters and histograms stay delta. Repoint queries that summed or rated the old per-interval values to read the level. The v0.71.1 advice to avoid `delta` for these gauges no longer applies. |
| `KEYSTORE_KEYS_*` (C72.17) | The names in DEPLOYING.md keep their variables and wire kids. | Keystore config is now read through a tree reader. A variable whose path does not end at a source (`..._FILE` or `..._VALUE`, so not `..._FILENAME`), and any unknown key under an entry, now fail startup where v0.71.1 dropped them. Keep `PAYMENTSSIGN-V1` and `PAYMENTSENCRYPT-V1` hyphenated: an `_` respelling such as `KEYSTORE_KEYS_PAYMENTSSIGN_V1_PUBLIC_VALUE` now names a dotted entry, which startup refuses. Check: the first boot on the new pin starts. Later: the demo renamed these entries to dotted names; see [KEYSTORE_DOTTED_NAMES.md](KEYSTORE_DOTTED_NAMES.md). |
| Outbox relay logs and metrics (C72.30, `92883628`) | `outbox.enabled: true`, `pollinterval: 5s`, single tenant. | At `log.level: info` the relay no longer writes, for a scheduled run, its per-run `Job 'outbox-relay' completed …` line (INFO, WARN when slow; 720 an hour per replica) or `Job 'outbox-relay' failed after …` line (ERROR), its `Job trigger skipped - job is already running` WARN or `Outbox relay cycle completed`: all are DEBUG. Each replica writes one `Job 'outbox-relay' window summary` line per `scheduler.summary.window`, WARN when the window had failing runs, and a `Job 'outbox-relay' failing edge: <reason>` line when the relay turns failing (`new`) or fails on a new key (`grew`), at most 2 per window: ERROR when the run errored, WARN otherwise. So in a sustained broker outage, relay ERRORs fall from one every 5s to at most two edge lines per window, which shows in `make redeclare-demo`, `make loadtest-topology-repair` and any error-log panel. Move an alert on an ERROR with `job.id=outbox-relay` to the edge line or the window line's `runs.failing`. It exports `outbox.relay.cycles` (`role`), `outbox.relay.led`, `outbox.relay.records` (`outcome`, `lane`) and `outbox.relay.unrecorded`, each with `job.id`; single tenant, so no `tenant` attribute and no startup WARN about it. Repoint every alert, dashboard or query on the old lines to these. A NACKed or poisoned row still counts as a successful run in `job.execution.total`, so alert on `outbox.relay.records` for those. Relay log volume and the idle allocation baseline drop for framework reasons, so compare them only within one pin. |
| `SCHEDULER_SUMMARY_WINDOW` (C72.28, `92883628`) | Unset, so 5m. | Optional. A window under 1m, one that does not divide 24h, or a negative value fails startup. Keep it longer than `outbox.pollinterval`, or startup logs the WARN `Windowed job interval is not shorter than scheduler.summary.window`. |
| Job summary `correlation_id` and `traceparent` (C72.26, `92883628`) | Nothing in the repo reads them. | `correlation_id` on a job summary line is now a UUID minted per run, not the OTel trace id: join job lines to traces through `trace_id`. `traceparent` carries the run span's real flags, so an unsampled run ends in `-00`, and it is empty without a valid span context. |
| `OBSERVABILITY_METRICS_INTERVAL` (`3c9df95e`, `92883628`) | Not set by the demo, so 10s (floor 15s → 5s). The perf profile in [PERF_VERSION_COMPARISON.md](PERF_VERSION_COMPARISON.md) pins `15s` (floor 15s → 7.5s). | The runtime read floor is now half the interval, where it was a fixed 15s. Under an interval below 30s, `go.memory.used`, `go.goroutine.count`, `go.memory.gc.goal`, `go.memory.allocated`, `go.memory.allocations` and `go.schedule.duration` are now read fresh on every export, where up to every other export repeated an older read; at 15s the old floor equalled the interval, so about every other export did. Names and units are unchanged, but gauge values (peaks in particular) do not compare with series from `8cfb522f` or earlier. For the two allocation counters a stale read only moves an increment to the next export, so a windowed `sum()` such as Q9 in `loadtests/nrql/queries.yaml` differs mostly at the window edges. With `OTEL_GO_X_DEPRECATED_RUNTIME_METRICS=true`, the stop-the-world `runtime.ReadMemStats` now runs on every export. |
| `OTEL_LOGRECORD_ATTRIBUTE_COUNT_LIMIT` (C72.27, `92883628`) | The demo sets none. | No longer read. With the OTLP log export on, the bridge caps each record at 128 attributes itself and writes `log.type` and the trace ids first. A line over the cap gains `log.attributes.dropped` and draws one WARN per process. Drop the variable from deploy manifests; nothing replaces it. During a running perf series, leave it as it is: it is ignored, and step 2 of [PERF_VERSION_COMPARISON.md](PERF_VERSION_COMPARISON.md) requires every `OTEL_*` variable to equal session 1's, so removing it mid-series fails that gate. Drop it after the series ends, before the next series' session 1. |
| `go-jose/v4` 4.1.6 (`50820b01`, `92883628`) | Moves with the pin. | A JWE with a content-encryption key of the wrong size, a `jwk` or `epk` header carrying private parameters, and a JWK RSA modulus over 4096 bits are now refused. Valid traffic on the demo's kid-resolved DER keys is unaffected. Check: a real partner integration on these doors still opens its replies. |

### Multi-tenant migrate CLI (`config.multitenant.yaml`, `go-bricks-migrate`)

| Item | Decision | Before deploy |
|------|----------|---------------|
| `GO_BRICKS_REF` | Now `v0.72.0` (was `92883628ae2a`, and `8cfb522fbbaa` before it). The recipe checks the ref out with git, which takes a tag as it is but cannot resolve a pseudo-version, so the main pins were written as their commit hashes. `tools/migration` has no `v0.72.0` tag; that does not matter, because the recipe builds the CLI from the checkout's `go.work`. | Rebuild with `make migrate-multitenant-install`. With `GO_BRICKS_PATH` set, check out `v0.72.0` there first. |
| Tenant migration targets (C72.14) | `acme`, `globex` and `initech` set no schema and have distinct usernames, so no two share a target. | None. |

No new must-do for `92883628` → v0.72.0 beyond the rebuild: `config.multitenant.yaml`
decodes under the #2065 decoder, and `--shared-migrator` stays off.

### `.env`, `Dockerfile`, compose, CI, load-test jobs

No change. The `Dockerfile` builds with `GOWORK=off`, so the image resolves the
`go.mod` version (a pseudo-version on the main pins, `v0.72.0` now) through the
module proxy. The seal-event and open-event scripts read their version from
`go.mod`, and both CI workflows read the Go version from it. `.env.example`,
`loadtests/k8s/.env.example`, `k6-job.yaml` and `psql-job.yaml` name no framework
version and no key these hops touch. There is no Helm chart.

## Verification

### v0.71.1 → `8cfb522f`

- The gates ran with `GOWORK=off`, so they compiled against the `go.mod` pin.
- Mutation checks on the reworked test: removing either defect injection fails
  its subtest; putting payments back in the validated set fails the three
  validating tests with the not-configured error; dropping the
  `messaging/sealed` blank import from the payments module fails the seam test
  with `ErrSealingNotLinked`; declaring `product-events` in the payments module
  fails only the name test, with the partner feed off and on.
- On `8cfb522f`, `config.Load` with the development overlay accepted a
  hyphenated generation variable and refused both an `_` respelling and a
  `..._FILENAME` variable.
- `make migrate-multitenant-install` checked out `8cfb522fbbaa`, built the CLI
  and confirmed it linked the in-tree go-bricks.
- Not run here: a live boot on the docker stack. Until one runs, the payments
  key resolution, dead-letter routing and publisher destinations on this pin are
  unproven.

### `8cfb522f` → `92883628`

- The gates ran with `GOWORK=off`, so they compiled against the `go.mod` pin.
  No test changed.
- go-bricks' outbox and inbox store sources and their golden SQL files
  (`outbox/testdata/sql/*.golden`, `inbox/testdata/sql/*.golden`) are unchanged
  in `8cfb522f..92883628`. A scratch module rendering statements equivalent to
  the demo's repository, inbox and outbox statements through the changed
  query-builder doors gave byte-identical SQL and arguments under both pins.
- Not run here: `make migrate-multitenant-install` at `92883628ae2a`, and a live
  boot on the docker stack. Until one runs, the relay's window line and
  `outbox.relay.*` counters on this pin have not been observed in this repo, and
  the payments items above remain unproven.

### `92883628` → v0.72.0

- The gates ran with `GOWORK=off`, so they compiled against the `go.mod` pin.
  No test changed.
- The C72.34 and C72.36 detects from the go-bricks migration guide have no hit:
  `git grep` finds no `JoinFilter()` or `*JoinOn(` call, no `ErrInvalidAlias`
  match and no `BuildUpsert(` call.
- `make migrate-multitenant-install` checked out `v0.72.0` in a scratch clone,
  built the CLI and confirmed it linked the in-tree go-bricks.
  `go-bricks-migrate list --source-config config.multitenant.yaml
  --credentials-from config-file` decoded the file and listed `acme`, `globex`
  and `initech` (exit 0) without dialing a database. `make
  migrate-multitenant-verdict` did not run: its validate runs dial the tenants
  at `localhost:5432`, which another project's Postgres held on that host.
- Live boot on the docker stack: `postgres`, `postgres-analytics` and `rabbitmq`,
  with the default database and the broker moved to other host ports by a
  scratch compose override for the same reason, then `flyway migrate` on both
  databases. `/api/v1/health` answered 200, `/api/v1/ready` 200 with
  `{"status":"ready"}`, and `GET /api/v1/products?page=1&pageSize=2` and
  `GET /api/v1/analytics/views?limit=1` 200. The boot loaded five keystore
  entries, validated the full messaging declaration set with payments in it,
  declared the payments exchange, DLX and three queues, and started the payments
  consumer. So what the first hop's test rework left to a real boot holds on
  v0.72.0: the keystore resolves the payments sign and encrypt generations, and
  the payments topology passes the set-wide `Validate` rules on dead-letter
  routing and publisher destinations. No payment was published, so a live
  sealed publish and its delivery are not covered here.
- The boot logged no ERROR line. Its one WARN was the expected development CORS
  wildcard notice, and `Messaging topology redeclared on new channel` appeared
  once with `channel_generation` 1, the benign boot case CLAUDE.md describes.
- The relay's first `Job 'outbox-relay' window summary` line arrived at the 5m
  boundary at INFO: `job.state` `clean`, 24 runs, none failing, with the
  `outbox.relay.*` counts on the line. It is the first time C72.30's window line
  was observed in this repo.
