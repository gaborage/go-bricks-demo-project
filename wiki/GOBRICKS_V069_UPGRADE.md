# go-bricks v0.67.0 → v0.69.0: operator decision record

This record covers the upgrade through v0.68.0 and v0.69.0. It lists the
changes that reach this demo, the decision taken for each, and what an operator
must do before deploying. Symptoms and fixes are in the CLAUDE.md "Common
Troubleshooting" section; this page records **decisions**. The previous record
is [GOBRICKS_V067_UPGRADE.md](GOBRICKS_V067_UPGRADE.md). Later feature commits
append to [Adopted](#adopted).

## At a glance

- **No schema migration and no required config key.** Every environment boots
  as it is.
- **One code decision:** the payments publisher is `Mandatory`, so a payment no
  queue receives answers 500 instead of 202 and silent loss.
- **Operator must-dos:** repoint every reader of the `/ready` body; never
  deliver an empty `SERVER_PROBES_HOST`; alert on the new log lines; re-read
  retry dashboards (one fewer retry per exhausted publish).

## Version path

| Hop | Released | What moves in `go.mod` |
|-----|----------|------------------------|
| v0.67.0 → v0.68.0 | 2026-09-28 | go-bricks, plus `github.com/knadh/koanf/v2` v2.3.6 → v2.3.7, which v0.68.0 requires |
| v0.68.0 → v0.69.0 | 2026-09-29 | go-bricks only |

No AWS module moves: go-bricks' root module requires none. The bump compiled
and tested without source changes; the runtime changes below were fixed in
follow-up commits.

## Changes by hop

Buckets: **compiler** (the build fails), **runtime** (it builds and behaves
differently), **none** (nothing in the demo is in the population).

### v0.68.0

| Change | Bucket | Demo resolution |
|--------|--------|-----------------|
| Probe exemption keyed on the routed request and GET/HEAD (GHSA-h4jw-4c64-48mh, C68.1) | runtime | Not exposed: every module route has a static first segment, no module registers global middleware, and multitenant is off. The fix arrives with the bump. Side effect: a non-GET/HEAD call to `<base>/health` or `<base>/ready` now runs the identity chain. Nothing in the repo sends one. |
| Opt-in probe listener `server.probes.port` / `server.probes.host` (#1802/#1803, ADR-120) | none | Left off (see [Skipped](#skipped) and the [checklist](#probe-listener-enablement-checklist)). |
| Delivered-empty `server.probes.host` refused at startup, whatever the port (ADR-120) | runtime | No config or env sets it. Rule under [Per-environment](#per-environment-operator-config-changes-required-before-deploy). |
| `/ready` answers 503 while stopping (#1801) | runtime | Accepted. With the probe listener off it reaches only keep-alive requests already on the app listener. |
| Probe `/ready` gated on a live app listener (#1806) | none | Applies only with the probe listener on. |
| Concurrent readiness judgments coalesced (#1807) | runtime | Accepted. No config. |

### v0.69.0

| Change | Bucket | Demo resolution |
|--------|--------|-----------------|
| `HealthStatus.PublicErr` deleted (C69.2) | compiler | Not hit: the demo builds no `app.HealthStatus`. |
| `RouteConflict.FirstPath` added, unkeyed literals stop compiling (C69.3) | compiler | Not hit: no `server.RouteConflict{` literal. |
| `/ready` answers exactly `{"status":"ready"}` / `{"status":"not ready"}` (#1832, C69.1, ADR-120) | runtime | Two readers fixed: `make demo-consumer-readiness` reads `/_sys/health-debug` and fails without it; the k6 products-crud `setup()` logs the status only. Status codes and verdict rules are unchanged. |
| A returned `Mandatory` publish fails with `ErrPublishUnroutable` (#1835, C69.5, ADR-122) | runtime | Adopted on payments (see [Adopted](#adopted)). Default-on for every publish: the last attempt logs WARN `Publish failed after its last attempt, giving up` plus span event `amqp.publish.exhausted` instead of a retry WARN, and `retry.reason` on `messaging.client.publish.retries` gains the value `returned`. |
| A lost stream is reported at ERROR, never re-declared (#1830/#1829, C69.6, ADR-123) | runtime | Automatic. The `streams` kind stays unhealthy until a restart; it is non-critical, so `/ready` stays 200. A lost consumer skips its shutdown offset flush. |
| Duplicate routes refused by the server, keyed by echo node identity (#1823/#1824, C69.3, ADR-124) | runtime | Not hit: the real route table has no duplicate. |
| Non-struct request types refused at registration (#1843, C69.4, ADR-121) | runtime | Not hit: every typed handler takes a struct. |
| Legacy per-request binding chain deleted (#1844) | none | Internal. |
| `url.path` logs the raw path when set (#1821); JOSE failures omit `http.route` when no route matched (#1822) | runtime | Accepted. Only percent-encoded or unmatched requests log differently. |

## Features

### Adopted

| Feature | Decision | Caveats |
|---------|----------|---------|
| `Mandatory: true` on the `payment.authorized` publisher (#1835, ADR-122) | **Adopted.** During a topology repair a payment that no queue received was broker-acked, dropped and answered 202. It now retries on a 100ms backoff within `messaging.reconnect.maxpublishattempts` (default 5, about 0.4s), then fails with `ErrPublishRetriesExhausted` wrapping `ErrPublishUnroutable`, and the existing handler path answers **500** `INTERNAL_ERROR`. No 503 mapping, no idempotency key, `maxpublishattempts` unchanged. Tests pin the flag, the consumer-before-tap binding order, and a 500 with no card data in the error, response or log. | Bindings replay in declaration order (`payments.authorized`, then the tap), so a tap-based count can still show a small loss. A redeclare slower than the budget turns losses into 500s, and the unchanged k6 payments error threshold may trip. amqp091 drops a return if its 256-slot buffer stays full for 5s. A return that arrives after a 30s confirm timeout can fail a delivered publish, and a client that retries a 500 mints a new `orderId`. |

Money path only. `product-events` has no bound queue in this demo, so every
product event is unroutable by design and must never be `Mandatory`; the outbox
relay cannot set the flag anyway (upstream #1819).

### Skipped

- **Readiness gauges** (`app.readiness.status`, `messaging.consumer.*`,
  `messaging.streams.*`, #1820): automatic, but no-ops here because
  `config.development.yaml` has no observability block. No panel yet.
- **Probe listener** (`server.probes.port`): left off. Turning it on moves the
  probes off `/api/v1`, which the `Dockerfile`, the scripts and the k6 tests
  call. See the [checklist](#probe-listener-enablement-checklist).
- **`httpclient` `WithBearerTokenFile`** (#1837/#1838): no demo client sends a
  bearer token.
- **Streams lost-topology supervisor** (#1830): automatic, nothing to opt into.
  Opt-in re-creation is upstream #1826.

## Per-environment operator config changes required before deploy

### `config.development.yaml` (APP_ENV=development, the only runtime environment)

| Item | Decision | Before deploy |
|------|----------|---------------|
| `/ready` body | No key. The file's comments describe the v0.69.0 shape. | None. |
| `messaging.reconnect.maxpublishattempts` | Not set, so the default 5 applies: a returned payment retries for about 0.4s. | Size it against the measured redeclare time before an environment adds registries or pooled publishers (a longer pass). Measured in this upgrade (k6 topology-repair, single-tenant): `payment-events` back after 232ms and its bindings after 233ms, inside the budget, so the default stays. |
| `server.probes.port` / `server.probes.host` | Neither set: the probes stay at `/api/v1/health` and `/api/v1/ready`. | Never write `host: ""` or a bare `host:`: startup refuses it even with the port at 0. |
| `messaging.consumers.critical` | Still absent (commented example). | None. |
| `debug` | No section, so `/_sys/health-debug` stays off. `make demo-consumer-readiness` turns it on for its own app only. | An environment that needs the counters sets `debug.enabled`, keeps `debug.allowedips` to trusted sources and, behind a load balancer, sets `debug.trustedproxies`. The view is more sensitive than the old `/ready` body. |
| Observability | No block, so the new gauges are no-ops. | None. Turning the OTLP export on turns the gauges on too. |
| `keystore.tokens-peer.public.value` | `make dev` re-runs `make generate-keys`, which regenerates `certs/` and patches this value between the `BEGIN_TOKENS_PEER_PUB` markers. | Do not commit that local change with the upgrade. |

### `config.multitenant.yaml` (read by the `go-bricks-migrate` CLI and by `make migrate-multitenant-check-roles`)

| Item | Decision | Before deploy |
|------|----------|---------------|
| Tenants `acme`, `globex`, `initech` | No change. Neither hop lists a migrate or tenant-database change. | None. Secret-bearing entries (names only): `multitenant.tenants.<tenant>.database.password`, untouched. |
| `cmd/check-tenant-roles` | `cmd/check-tenant-roles` (built by `make migrate-multitenant-check-roles`) also reads it, and it is built at the `go.mod` pin (v0.69.0), not at `GO_BRICKS_REF`. Neither hop touches the tenant store or `migration.CheckPGRoleFloor`, so no change is needed. | None. |

### `.env` / `.env.example`

| Item | Decision | Before deploy |
|------|----------|---------------|
| Variables | No change. Entries: `NEW_RELIC_LICENSE_KEY`, `NEW_RELIC_REGION`, and commented `CORS_DEV_WILDCARD` / `CORS_ORIGINS`. `.env` feeds docker-compose only; the app does not read it. | The same rules hold for any shell, container or Helm env that reaches the app: never deliver `SERVER_PROBES_HOST=` empty, set `SERVER_PROBES_PORT` only with the checklist below, and `MESSAGING_RECONNECT_MAXPUBLISHATTEMPTS` only after sizing. The v0.67.0 rule stands: never export `GOBRICKS_MIGRATE_MIGRATOR_USER` or `GOBRICKS_MIGRATE_MIGRATOR_PASSWORD`. |

### `Dockerfile`

| Item | Decision | Before deploy |
|------|----------|---------------|
| Build | `GOWORK=off`, so the image resolves v0.69.0 from `go.mod` whatever a local `go.work` says. | None. |
| `HEALTHCHECK` | `wget` GET of `${SERVER_PATH_BASE:-/api/v1}/health`. A GET, so the GHSA fix does not change its answer. | None while the probes stay on the app listener. With `SERVER_PROBES_PORT` set it gets 404: point it at `:<probe port>/health`. |

### `etc/docker/docker-compose*.yml`

| Item | Decision | Before deploy |
|------|----------|---------------|
| Broker, PostgreSQL, observability | No change. Compose does not run the app. | None. |

### Makefile and CLI pins

| Item | Decision | Before deploy |
|------|----------|---------------|
| `GO_BRICKS_REF` (migrate CLI source) | Still `v0.67.0` on this branch. Neither hop lists a migrate change, so the installed `go-bricks-migrate` behaves the same. | Optional: bump it to `v0.69.0` to keep one version, then `make migrate-multitenant-install`. |
| `SEAL_EVENT_VERSION` | No pin to bump: the scripts read the version from `go.mod`, so `seal-event` and `open-event` now install at v0.69.0. | The first run needs module-proxy access to install the v0.69.0 CLIs. |

### CI (`.github/workflows/ci.yml`, `security.yml`) and Dependabot

| Item | Decision | Before deploy |
|------|----------|---------------|
| Build, test, lint, govulncheck | No workflow change. CI has no `go.work`, so it resolves v0.69.0 from `go.mod`. | Locally, run the gates with `GOWORK=off`: the untracked `go.work` makes `make build`, `make run`, `make test` and every demo script compile against `../go-bricks` HEAD instead. |
| Dependabot | The koanf v2.3.7 branch is superseded by this bump. The AWS branches (`aws-sdk-go-v2` 1.47.1, `config` 1.33.6, `secretsmanager` 1.50.1) are not. | Close the koanf branch; review the AWS branches separately. |

### External consumers (outside this repo)

| Item | Decision | Before deploy |
|------|----------|---------------|
| Readers of the `/ready` body: dashboards, NRQL, alerts, synthetic monitors, `curl \| jq` steps | Nothing in the repo reads it after this upgrade. | Judge by the status code. Read detail from `/_sys/health-debug` (`.data.components.<kind>`) or the gauges with OTLP on. The blocking kind is on the ERROR `Readiness check failed` (`component=<kind>`). |
| Monitors that call a probe path with a method other than GET/HEAD | None in the repo. | Switch them to GET or HEAD. A POST now answers 400 under multitenant and 401 under `forwardedclientcert.require` (was 405). |
| Alerting | New signals: `messaging.ErrPublishUnroutable` (ERROR `Failed to authorize payment`, error ending `publish returned by broker as unroutable`), WARN `Publish failed after its last attempt, giving up`, and ERROR `Stream consumer closed unexpectedly` / `Stream publisher closed unexpectedly`. | Add alerts on them. Move any alert keyed on the stream client's own `won't be reconnected` line to the new ERROR, and restart every replica that logs it. |
| Retry-metric dashboards | An exhausted publish, for every cause and every publisher, now records `maxpublishattempts − 1` retries (was `maxpublishattempts`). The existing `retry.reason` attribute on `messaging.client.publish.retries` gains the value `returned` (beside `publish_error`, `nack`, `timeout`), so a dashboard grouping by it sees one new series. | Adjust any panel or alert that expects `maxpublishattempts` retries per failure. |
| Callers of `POST /api/v1/payments/authorize` | A payment no queue receives answers 500 `INTERNAL_ERROR`, not 202. | Treat a 500 as unconfirmed, not refused: a deadline, shutdown or lost confirm can end in a 500 after the event reached a queue. Reconcile on the `orderId` in the ERROR `Failed to authorize payment` log line before re-submitting; a retry mints a new `orderId`, and there is no idempotency key yet. |

### Probe listener enablement checklist

Nothing sets `server.probes.port` today. Before an environment sets it above 0
(env `SERVER_PROBES_PORT`), change all of these together:

- **Orchestrator probes and the `Dockerfile` `HEALTHCHECK`** move to
  `:<port>/health` and `:<port>/ready`, with no base path. `<base>/health` and
  `<base>/ready` answer 404 on the app listener.
- **Every health pre-check in the repo:** `scripts/advisory-lock-demo.sh`,
  `external-exchange-demo.sh`, `show-sealed-message.sh`,
  `test-products-api.sh`, `topology-repair-demo.sh` and
  `run-loadtest-all-monitored.sh`; the k6 tests `products-crud`,
  `products-read-only`, `ramp-up-test`, `spike-test`, `sustained-load`,
  `tokens-common` and `topology-repair`; and the `/ready` reads in
  `consumer-readiness-demo.sh` and `products-crud.ts`.
- **`make advisory-lock-demo`** boots two extra replicas on one host: each needs
  its own probe port.
- **Ports and hosts:** a probe port equal to `server.port` on overlapping hosts
  is refused at startup. `server.probes.host` is a real address or absent,
  never empty.
- **Network posture:** the probe listener has no rate limiter, no TLS and no
  identity chain, so keep it off every public path. With `server.tls.enabled`,
  the app listener's leaf certificate needs a SAN, or `Start` refuses.

## Follow-up recommendations

- **Idempotency key on `POST /api/v1/payments/authorize` first**, so a client
  can retry a 500 without minting a second `orderId`.
- **Only once that key exists, map `ErrPublishUnroutable` to 503 with
  `Retry-After`** instead of the generic 500: during a repair the condition is
  transient, but a 503 invites automatic retries, and without the key each one
  mints a new `orderId` for a payment that may already be queued. Until then,
  callers must not retry automatically and must reconcile on the `orderId` in
  the ERROR `Failed to authorize payment` log line.
- **Count delivery on `payments.authorized`, not only the tap,** in
  `loadtests/topology-repair.ts`, so the between-bindings gap stops reading as a
  loss.
- **Readiness-gauge Grafana panels** (`app.readiness.status`,
  `messaging.consumer.*`) once the dashboard metric-prefix gap is fixed.
- **AWS Dependabot branches:** review and merge separately.
- **Upstream #1826** (opt-in stream re-creation): would let `product-activity`
  heal like the AMQP lane.
- **Upstream #1819** (outbox `Mandatory`): not for `product-events`, which has
  no bound queue, but needed before any outbox event that must be routed.

## Verification

- The gates ran with `GOWORK=off`, so they compiled against the `go.mod` pin,
  as CI and the `Dockerfile` do.
- Unit tests pin the adoption: the publisher carries `Mandatory` (a mutation to
  `false` fails the test), the consumer binding is declared before the tap, and
  an unroutable publish answers 500 with no card data.
- `make redeclare-demo` asserts that a 2xx reaches the tap, and reports a 5xx
  as unconfirmed, on the tap or not, without failing.
- Live proofs, on the local docker stack with `config.development.yaml`:
  - Clean boot: no ERROR, the only WARN is the development CORS one; `/ready`
    answered exactly `{"status":"ready"}`.
  - `make show-sealed-message`: the PAN is absent from the wire, and the card
    opens as `"<redacted>"`.
  - `make redeclare-demo`: the publish into the hole answered 202 and reached
    the tap.
  - `make loadtest-topology-repair`: 0 lost in the repair window, 0 payments
    5xx, 601 of 601 payments on the tap, every threshold passed.
  - `make demo-consumer-readiness`: `/ready` answered 503
    `{"status":"not ready"}` at a fail streak of 5 with health-debug naming
    `consumer re-subscribe exhausted`, recovered without a restart, and the
    broker permissions read back as recorded.
  - Deleted bindings, exchange kept: with both `payment-events` bindings
    removed, two payments 15s apart each answered 500 in about 0.43s (4 WARN
    returns, the give-up WARN, the ERROR ending `publish returned by broker as
    unroutable`). Nothing re-declared the bindings until a restart. This was
    checked within the default 1h publisher idle TTL
    (`messaging.publisher.idlettl`), after which a recreated publisher runs one
    redeclare pass.
