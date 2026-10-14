# go-bricks v0.71.1 → main `8cfb522f`: operator decision record

This record covers the pin to go-bricks main commit `8cfb522f` (pseudo-version
`v0.71.2-0.20261011230701-8cfb522fbbaa`), taken ahead of the v0.72.0 tag for
a performance test series. The pin is that exact commit, not the latest main.
The framework's CHANGELOG stops at 0.71.1 there; the changes are the atoms
C72.1 to C72.25 in its migration guide (`wiki/migrations.md`, section E72, at
that commit). This record lists the ones that reach the demo, the decision for
each, and what an operator must do before deploying. The previous record is
[GOBRICKS_V071_UPGRADE.md](GOBRICKS_V071_UPGRADE.md).

## At a glance

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

## Version path

| Hop | Commit date | What moves in `go.mod` |
|-----|-------------|------------------------|
| v0.71.1 → `8cfb522f` | 2026-10-11 | go-bricks (the only direct change), plus 32 indirect modules: OpenTelemetry 1.46.0 → 1.47.0, `otel/log` and `sdk/log` 0.22.0 → 1.47.0, the OTLP log exporters 0.22.0 → 0.23.0, `go-redis/v9` 9.22.0 → 9.23.0, and smaller bumps |

The `go` directive and the `toolchain` line are unchanged.

## Compile break: C72.9 and the cross-module messaging test

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

## Changes that reach this demo

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

## Per-environment operator config changes required before deploy

### Development (`config.development.yaml`, docker compose)

None. `TestConfigLayoutDevelopmentLoadsBaseAndOverlay` loads and validates the
base and the development overlay on the new pin, keystore block included.

### Non-development deploy (env vars, per [DEPLOYING.md](DEPLOYING.md))

| Item | Decision | Before deploy |
|------|----------|---------------|
| `OBSERVABILITY_<SIGNAL>_ENDPOINT` (C72.2, C72.3) | Not set by the demo. | An HTTP endpoint with userinfo, a query, a fragment, an encoded slash or no host, or a gRPC endpoint starting `grpc://`, now fails observability `Validate`. The service still boots, with the WARN `Failed to initialize observability, using no-op provider`, and exports no traces, metrics or logs. Write HTTP endpoints as `scheme://host[:port][/path]` and gRPC endpoints as `host:port`. An HTTP endpoint with a path now exports to exactly that path, so make it the collector's full export path. Check: no such WARN at boot. |
| `OTEL_EXPORTER_OTLP_*` (C72.4) | The demo sets none. | The endpoint, insecure and protocol variables, and their `_TRACES_`, `_METRICS_` and `_LOGS_` variants, are ignored: only `observability.*` config counts. In the same rollout, move each endpoint and insecure value to `OBSERVABILITY_<SIGNAL>_ENDPOINT` or `_INSECURE`. A per-signal HTTP URL moves as it is. The generic `OTEL_EXPORTER_OTLP_ENDPOINT` was a base, so join `/v1/<signal>` onto it: a path is now used exactly (C72.2). An `http://` env URL chose plaintext, so set `_INSECURE=true` with it. A gRPC value drops its `http://` or `https://` scheme and keeps `host:port`. Drop `*_PROTOCOL`: it never chose the transport, which is `observability.<signal>.protocol`. Nothing fails at startup; exports fail at runtime if the collector does not serve the configured endpoint over the configured transport. |
| `OBSERVABILITY_TRACE_SAMPLE_RATE` (C72.6) | Unset, so 1.0. | An inbound `traceparent` marked not sampled (`-00`) now drops this service's spans at every rate, 1.0 included. Check whether a gateway or upstream service sends such headers. If one does, expect fewer spans: no rate restores them. |
| `OBSERVABILITY_METRICS_TEMPORALITY=delta` (C72.12) | Default `cumulative`: no change. | Under `delta` with an OTLP endpoint, UpDownCounters and gauges (`go.goroutine.count`, `go.memory.used`, `db.client.connection.*` and others) now export as cumulative; counters and histograms stay delta. Repoint queries that summed or rated the old per-interval values to read the level. The v0.71.1 advice to avoid `delta` for these gauges no longer applies. |
| `KEYSTORE_KEYS_*` (C72.17) | The names in DEPLOYING.md keep their variables and wire kids. | Keystore config is now read through a tree reader. A variable whose path does not end at a source (`..._FILE` or `..._VALUE`, so not `..._FILENAME`), and any unknown key under an entry, now fail startup where v0.71.1 dropped them. Keep `PAYMENTSSIGN-V1` and `PAYMENTSENCRYPT-V1` hyphenated: an `_` respelling such as `KEYSTORE_KEYS_PAYMENTSSIGN_V1_PUBLIC_VALUE` now names a dotted entry, which startup refuses. Check: the first boot on the new pin starts. |

### Multi-tenant migrate CLI (`config.multitenant.yaml`, `go-bricks-migrate`)

| Item | Decision | Before deploy |
|------|----------|---------------|
| `GO_BRICKS_REF` | Now `8cfb522fbbaa`, the commit in the `go.mod` pseudo-version. The recipe checks the ref out with git, which cannot resolve a pseudo-version. | Rebuild with `make migrate-multitenant-install`. With `GO_BRICKS_PATH` set, check out `8cfb522f` there first. |
| Tenant migration targets (C72.14) | `acme`, `globex` and `initech` set no schema and have distinct usernames, so no two share a target. | None. |

### `.env`, `Dockerfile`, compose, CI

No change. The `Dockerfile` builds with `GOWORK=off`, so the image resolves the
pseudo-version from `go.mod` through the module proxy. The seal-event and
open-event scripts read their version from `go.mod`.

## Verification

- The gates ran with `GOWORK=off`, so they compiled against the `go.mod` pin.
- Mutation checks on the reworked test: removing either defect injection fails
  its subtest; putting payments back in the validated set fails the three
  validating tests with the not-configured error; dropping the
  `messaging/sealed` blank import from the payments module fails the seam test
  with `ErrSealingNotLinked`; declaring `product-events` in the payments module
  fails only the name test, with the partner feed off and on.
- On the new pin, `config.Load` with the development overlay accepted a
  hyphenated generation variable and refused both an `_` respelling and a
  `..._FILENAME` variable.
- `make migrate-multitenant-install` checked out `8cfb522fbbaa`, built the CLI
  and confirmed it linked the in-tree go-bricks.
- Not run here: a live boot on the docker stack. Until one runs, the payments
  key resolution, dead-letter routing and publisher destinations on this pin are
  unproven.
