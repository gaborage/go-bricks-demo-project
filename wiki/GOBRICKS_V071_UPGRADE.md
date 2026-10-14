# go-bricks v0.69.0 → v0.71.1: operator decision record

This record covers the upgrade through v0.70.0, v0.71.0 and v0.71.1. It lists
the changes that reach this demo, the decision taken for each, and what an
operator must do before deploying. This hop adds no CLAUDE.md "Common
Troubleshooting" entry: the startup refusals an env var can trigger are
described in the operator tables below. The previous record is
[GOBRICKS_V069_UPGRADE.md](GOBRICKS_V069_UPGRADE.md).

## At a glance

- **No source change, no schema migration and no required config key.** The
  repo's own configs boot unchanged. A deploy that overrides `SOURCE_TYPE`, or
  `APP_NAME` with the OTLP export on, can now fail at startup: check its
  overrides against the must-dos below before deploying. The only edits are the
  pins, one `config.yml` comment, one README note and pointer cases in the
  redaction tests.
- **No feature adopted.** Every candidate is skipped below, with its reason.
- **Operator must-dos:** keep `SOURCE_TYPE` unset or `static`; keep `APP_NAME`
  a valid `host[:port]` when the OTLP export is on; set
  `SERVER_TRUSTEDPROXIES` only behind a public or `100.64.0.0/10` proxy; keep
  the termination grace period at 15s or more; repoint saved observability
  queries that read the dropped HTTP attributes; decide on retained logs for
  GHSA-3m9p-48v4-c9vp; never share a seal key with a jose route.

## Version path

| Hop | Released | What moves in `go.mod` |
|-----|----------|------------------------|
| v0.69.0 → v0.70.0 | 2026-10-01 | go-bricks, plus `labstack/echo/v5` v5.3.1 → v5.4.0 (direct) and `labstack/echo-opentelemetry` v0.0.3 → `labstack/echo-otel/v5` v5.0.0 (indirect) |
| v0.70.0 → v0.71.0 | 2026-10-03 | go-bricks only |
| v0.71.0 → v0.71.1 | 2026-10-03 | go-bricks only |

`go.sum` moves `go.opentelemetry.io/contrib/propagators/b3` from v1.42.0 to
v1.46.0. It reaches the module graph only through echo-otel/v5's tests; no
package the demo builds imports it. The `go`
directive and the `toolchain` line are unchanged. The bump compiled and tested
without source changes.

## Changes by hop

Each hop has two buckets: **compile-time** (the build would fail) and
**config/runtime** (it builds and behaves differently). "Hits" says whether
anything in this repo is in the population.

### v0.70.0

**Compile-time: none hit.** `StopConsumers` now takes a `context.Context`
(C70.14), and `Publisher[T].Seal`, `jose/sealed.Seal` / `SealDocument` and
`messaging.Sealer` now return the signed `jti` (C70.15). The demo calls none of
them: its `Seal` calls are the HTTP `jose.Seal`, which is unchanged. The
`auth.NewVerifier` variadics do not apply; the demo does not import `auth`.

| Change | Hits | Decision |
|--------|------|----------|
| `source.type` must agree with `Options.ResourceSource` and is enum-checked in every mode (#1859, C70.1, ADR-125) | No. No `source:` block, no `SOURCE_TYPE`, no `ResourceSource`. | Keep. A typo such as `dynmic` now fails startup instead of reading `static`. |
| A resource plan decides availability, pre-init and pre-warm per kind (#1864, C70.2/3/5/6, ADR-127) | No. No `ResourceSource` and no cache; the built-in store applies the same root-block tests. | Keep. |
| Multi-tenant messaging: shared tenancy needs a control-plane broker; per-tenant tenancy where no static tenant sets `messaging.url` is unavailable (#1864/#1876, C70.4/C70.11, ADR-128) | No for the app, which is single-tenant. `config.multitenant.yaml` sets no tenant `messaging.url`, but only the migrate CLI and `check-tenant-roles` read it. | Keep. The README's multi-tenant note now says every static tenant needs `multitenant.tenants.<id>.messaging.url`. The rule applies to a real multi-tenant app overlay. `config.multitenant.yaml` has no root `database` block, so the app refuses it at config validation (`database.host required`) on v0.69.0 and v0.71.1 alike. |
| Outbox broker check reads `ModuleDeps.MessagingConfigured` (per-tenant ledger) or `ControlPlaneMessagingAbsent` (shared ledger) (#1871/#1875, C70.7–C70.10) | Yes, unchanged in effect: single-tenant with a root broker means `MessagingConfigured` is true. | Keep. The boot logged `Outbox module initialized`. |
| Consumer stop joins the consumer supervisors before clients close (#1894/#1895, C70.14) | Yes: the `payments.authorized` consumer, and `partnerfeed` when enabled. The join is bounded by the shutdown context and a fixed 5s budget, inside `server.timeout.shutdown` (default 10s, not overridden); the outer hard stop is 15s. | Keep the default. The payments handler honours cancellation, and the boot's SIGTERM stopped every consumer in the same second with no `abandoning them` WARN. |
| HTTP server telemetry moves to echo-otel/v5 (#1923, C70.17, ADR-132) | Yes, with the OTLP export on (off in `config.development.yaml`). | Keep `app.name: go-bricks-demo-project`, which parses as a host, and accept the HTTP series reset. The `config.yml` comment now says `server.address` is on spans only. The Grafana dashboards' metric panels group only by `le`, `http_route`, `http_response_status_code` and `db_operation_name`, so they need no change. |
| `X-Forwarded-Proto` trusted only from loopback, link-local, RFC 1918, unix sockets and `server.trustedproxies`; HSTS and `url.scheme` follow (#1922, C70.16, echo GHSA-2ffq-g2xg-c22p) | No in development: plain http on loopback, where HSTS was never sent. | Keep. No repo config; the right value depends on the deploy (see below). |
| Seal-tagged `DeclareTypedPublisher` builds a producer-side verifier; every provisioned RSA generation of both seal families is role-tagged `seal` at startup (#1908) | Yes: the payments publisher. The verifier is used only by `PublishSealed`, which the demo does not call. | Keep. `paymentssign-v1` / `paymentsencrypt-v1` share no entry with `tokensour`, `tokenspeer` or `webhooksigning`; the boot logged no dual-role WARN. |
| `jose/sealed` `Open` refuses a resolver that returns a nil key (#1893) | No: the keystore never returns a nil key. | Keep. |
| JOSE transport: a rejected 2xx is terminal and keeps its type; every client refuses a credentialed https→http redirect (#1888/#1889, C70.12/C70.13) | No: the relays configure no retries and no `CheckRedirect`, and their peers are in-process. | Keep. |
| Outbox leader-lock `NOWAIT` loss and Oracle inbox duplicates log at DEBUG (#1910) | Yes with more than one replica (`make advisory-lock-demo`). | Keep. The false ERRORs leave the replicas' logs. |
| The AMQP reconnect goroutine stays quiet after `Close` (#1912) | Yes: `make external-exchange-demo` step 1 loses a spurious ERROR. The script's assertions do not read it. | Keep. |
| echo v5.4.0 security fixes in Static, JSONP, MethodOverride, Proxy and redirect handling | No: the demo uses none of those middlewares. | Accept the bump. |

Docs only: `inbox.ProcessOnce` deduplicates per service, keyed
`(tenant_id, event_id)` (#1877), and the inbox module must be registered before
the modules that use it (#1882). Payments is the only inbox user and is
registered after it.

### v0.71.0

**Compile-time: none hit.** Removed: `logger.WithAMQPCounter` /
`WithDBCounter`, `FactoryResolver.MessagingClientFactory`,
`RouteRegistry.AddRoute` / `RoutesByModule`, the `postgresql` / `oracle`
`Statement` / `Transaction` aliases, ten database tracking re-exports,
`database.TrackedDB` / `NewTrackedDB`, the app seams (`TimeoutProvider`,
`SignalHandler.WaitForSignal`, `app.Prober`, `RegisterStreamRuntime` /
`StreamRuntime`, `App.MessagingDeclarations`), the `httpclient` trace constants
and `IsJOSEError` (now `jose.IsError`), `messaging.Client.Consume` and its mock
helpers, 46 testing constants and `cache/testing.AssertOperationCountGreaterThan`.
`server.NewHandlerContextForTestWithOptions` folded into
`NewHandlerContextForTest`, which gains a trailing variadic: the demo's 12
direct three-argument calls compile unchanged.

| Change | Hits | Decision |
|--------|------|----------|
| `resourcepool` reserves a create's seed lease for every waiter, so the `pool churn` failure is gone; a refused claim returns `ErrPoolClosed` (#1976/#1977) | No script, test or alert matches `pool churn`. | Keep. |
| Single-adapter internal seams collapsed, which also fixes the inbox `HoldDrain` backoff overflow (#1936) | Inbox hold is off here. The products and analytics repository tests pin the rendered SQL and still pass. | Keep. |

### v0.71.1

**Compile-time: none hit.** The API is unchanged.

| Change | Hits | Decision |
|--------|------|----------|
| The log filter follows every non-nil pointer and walks string slices that hold JSON or PEM (GHSA-3m9p-48v4-c9vp, ADR-086 amendment) | No demo call site logged an affected shape. The Redactor types (`CardDetails`, `CardRequest`, `TokenizeRequest`, `PeerSimRequest`, `RelayRequest`, `MLERelayRequest`) have value receivers and are logged by value; nothing logs a `*map`, `*[]byte`, `*json.RawMessage`, `*string`, `*any` or a string slice. | Keep. The three redaction tests now also log through a pointer and through a pointer to a map. The pointer-to-map rows fail against v0.69.0 and pass on v0.71.1. |

## Features

### Adopted

None.

### Skipped

- **`database.WithExpectedError`** (#1906): no demo statement produces an
  expected driver error that still logs at ERROR. The framework already applies
  it to the outbox leader lock.
- **`Publisher[T].PublishSealed`, `SealedEventPublisher`,
  `CaptureSealedPublisher`** (#1908): the demo never stores or republishes
  sealed bytes. Adopting it would add a sealed-republish path, a new security
  capability.
- **`Seal` returning the signed `jti`** (C70.15): no call site.
- **`jose/sealed.Verify`** (#1893): nothing hand-rolled to replace, and no
  framework CLI exposes it to the scripts.
- **In-app mTLS client verification, `server.tls.clientauth` /
  `clientcafile` / `clientcavalue`** (#1885, ADR-130): a new authentication
  capability. Note that `require-verify` is refused beside
  `server.probes.port > 0`.
- **`server.Options.TLSVerifyPeerCertificate`** (#1891): depends on in-app
  mTLS, which is not adopted.
- **`auth.WithDeferredKeySet` / `WithMeterProvider`** (#1890, #1886): the demo
  does not import `auth`.
- **`trace` package getters, `logger.WithRequestCounters`,
  `MockAMQPClient.ExpectConsumeFromQueueAny`**: replacements for removed
  symbols the demo never used.

The `ErrRedirectDowngrade` refusal and the terminal JOSE 2xx apply by default;
there is nothing to opt into.

## Per-environment operator config changes required before deploy

### Development (`config.development.yaml`, docker compose)

| Item | Decision | Before deploy |
|------|----------|---------------|
| Config keys | None added or changed. | None. Run the gates with `GOWORK=off`. |
| Observability | No block, so the export is off. | Turning it on (CLAUDE.md runtime tour) exercises echo-otel/v5. HTTP server series start fresh at the upgrade. |
| `server.timeout.shutdown` | Not set: the default 10s applies. | None. |
| `server.trustedproxies` | Not set. | None: plain http on loopback. |

### Non-development deploy (env vars, per [DEPLOYING.md](DEPLOYING.md))

| Item | Decision | Before deploy |
|------|----------|---------------|
| `SOURCE_TYPE` | Not set by the demo. | Leave it unset or exactly `static`. Any other value now fails startup. |
| `APP_NAME` with `OBSERVABILITY_ENABLED=true` | The default `go-bricks-demo-project` parses. | An override must parse as `host[:port]`: no leading `:` and a numeric port only. Otherwise the server refuses to start with `app.name "<v>" is not a valid host[:port] for the OpenTelemetry server.address`. |
| `OBSERVABILITY_SERVICE_NAME` | HTTP server metrics no longer carry `server.address`. | Give each app its own service name: it is now the only label that tells apps apart on those metrics. |
| `SERVER_TRUSTEDPROXIES` | Not set. | Needed only behind a proxy whose peer address is public or in `100.64.0.0/10` (a CDN, a global cloud load balancer, a pod network on that range). List the proxy's egress CIDRs, never a default route (refused), and make the proxy overwrite `X-Forwarded-Proto`. An in-cluster ingress on a private range needs nothing. Without it, HSTS is not sent and `url.scheme` reads `http`. Check: a request through the proxy answers with `Strict-Transport-Security`. |
| Termination grace period | Consumer stop can take up to 5s inside the 10s shutdown budget; the outer hard stop is 15s. | Keep the pod grace period at 15s or more (the Kubernetes default of 30s covers it), and at least `SERVER_TIMEOUT_SHUTDOWN` + 5s if that is raised. |
| Seal key rotation | One generation per seal family today. | A second generation (for example `paymentssign-v2`) is role-tagged as seal material at startup, before it is activated. Never give it a kid that a `jose:` route resolves. The WARN is `keystore entry resolved by both HTTP jose and sealing; never reuse a kid across them`. |
| `MESSAGING_BROKER_URL` | Still mandatory. | Without it, outbox `Init` fails as before, now worded through `MessagingConfigured`. |
| `OBSERVABILITY_METRICS_TEMPORALITY` | Unchanged in v0.71.1: under `delta`, the exporter's selector returns delta for every instrument kind, UpDownCounters included (`observability/metrics.go`). | A backend that rejects non-monotonic delta sums, such as New Relic, drops the runtime and pool gauges (`go.goroutine.count`, `go.memory.used`, `db.client.connection.*`). Export `cumulative` to keep them until a release carries the per-kind selector. Superseded at go-bricks `8cfb522f` (C72.12): see [GOBRICKS_V072_UPGRADE.md](GOBRICKS_V072_UPGRADE.md). |

### Multi-tenant migrate CLI (`config.multitenant.yaml`, `go-bricks-migrate`, `make migrate-multitenant-check-roles`)

| Item | Decision | Before deploy |
|------|----------|---------------|
| Tenants `acme`, `globex`, `initech` | No change. No `source:` block, so the store reads `static`. The CLI source is unchanged since v0.68.0; it now links v0.71.1 config validation. | None. |
| `GO_BRICKS_REF` | Bumped to `v0.71.1`, equal to `go.mod`. The v0.71.1 tag's `go.work` still uses `./tools/migration`, so the recipe is unchanged. | Rebuild with `make migrate-multitenant-install`. With `GO_BRICKS_PATH` set, check out `v0.71.1` there first. Never export `GOBRICKS_MIGRATE_MIGRATOR_USER` or `GOBRICKS_MIGRATE_MIGRATOR_PASSWORD`. |
| `cmd/check-tenant-roles` | Built at the `go.mod` pin. The tenant store and `migration.CheckPGRoleFloor` are unchanged. | None. |
| App overlay | The file is a CLI fleet file, never an app overlay. | Never boot the app with `APP_ENV=multitenant`: the file has no root `database` block, so config validation refuses it (`database.host required`). |

### `.env`, `Dockerfile`, compose, CI

No change. The `Dockerfile` builds with `GOWORK=off`, so the image resolves
v0.71.1 from `go.mod`. The seal-event and open-event scripts read their version
from `go.mod`, so their first run needs module-proxy access for the v0.71.1
CLIs.

### External consumers (outside this repo)

| Item | Decision | Before deploy |
|------|----------|---------------|
| Saved observability queries, dashboards and alerts | Metric names, `http.route`, `http.request.method` and `http.response.status_code` are unchanged. HTTP metric history restarts at the deploy. | Repoint anything that groups by `server.address`, `server.port` or `http.request.method_original`, or filters on the old instrumentation scope `github.com/labstack/echo-opentelemetry` (new: `github.com/labstack/echo-otel/v5`). Also check readers of `network.protocol.version` `2.0`/`3.0` (now `2`/`3`), span `error.type` on 4xx (now unset) and the old `[PANIC RECOVER]` status (now `panic: panic (type: <T>)`). The repo's NRQL catalogue (`loadtests/nrql/queries.yaml`) already avoids these. |
| Retained logs (GHSA-3m9p-48v4-c9vp) | No demo call site logged an affected shape, so repo evidence does not call for a purge or a rotation. | Whether to purge logs retained from before v0.71.1, or rotate credentials anyway, is the operator's and the security owner's decision. |
| Alerting | New WARN `Consumer supervisors still running after the stop budget; abandoning them`. The outbox leader-lock ERROR and the shutdown reconnect ERRORs go quiet by design. | Add an alert on the new WARN; expect alerts keyed on the old ERRORs to fall silent. |

## Follow-up recommendations

- **Document per-service inbox dedup** (#1877) in the CLAUDE.md Sealed
  Messages section: a second inbox-deduped consumer of `payment-events` in this
  service would be skipped silently.
- **Add an optional `SERVER_TRUSTEDPROXIES` row** to
  [DEPLOYING.md](DEPLOYING.md).
- **In-app mTLS** (`server.tls.clientauth`) for a deploy that terminates
  partner mTLS in the app.
- **`PublishSealed`** only if a reliable outbox lane for sealed payment events
  is wanted.

## Verification

- The gates ran with `GOWORK=off`, so they compiled against the `go.mod` pin,
  as CI and the `Dockerfile` do.
- Mutation check: the new pointer-to-map rows of the three redaction tests fail
  against v0.69.0 (the card or request reaches the sink with its PAN) and pass
  on v0.71.1. The plain pointer rows pass on both: a pointer to a Redactor type
  already took the hook.
- Live proofs, on the local docker stack with `config.development.yaml`:
  - Clean boot: 12 modules registered, no ERROR, the only WARN the development
    CORS one, no dual-role keystore WARN. `/health` and `/ready` answered 200
    (`{"status":"ready"}`); a products list 200, a tokens relay 200 and a
    payment 202 that the consumer took exactly once. The fixture PAN appeared
    in no response body and no log line.
  - SIGTERM: every consumer stopped within the same second, no
    `abandoning them` WARN, exit 0.
  - OTLP export on: the same results. The products request's server span in
    Tempo has scope `github.com/labstack/echo-otel/v5` and `server.address`
    `go-bricks-demo-project`. The Prometheus
    `http_server_request_duration_seconds` series carry `http_route`,
    `http_request_method`, `http_response_status_code`, `url_scheme`,
    `network_protocol_*` and `service_name`, and no `server_address` or
    `server_port`.
  - OTLP export on with `APP_NAME=":demo"`: startup refused, exit 1, with
    `app.name ":demo" is not a valid host[:port] for the OpenTelemetry
    server.address`.
