# go-bricks v0.72.0 → main `4cacc99c`: operator decision record

This record covers the move from the go-bricks v0.72.0 tag to go-bricks main
commit `4cacc99c` (pseudo-version `v0.72.1-0.20261029151308-4cacc99c4fad`). That
commit is the v0.73.0 release candidate: the draft release (go-bricks #2137) adds
only the release commit on top of it. The pin is that exact commit, not whatever
main is later. A re-pin to the `v0.73.0` tag follows once the tag is published
(see [Open follow-up](#open-follow-up)).

The range is 78 commits. The changes are the atoms in the framework's migration
guide (`wiki/migrations.md`, section E73): C73.1 to C73.19. There is no C73.15;
that is a gap in the guide's numbering, not a skipped atom. This record lists the
ones that reach the demo, the decision for each, and what an operator must do
before deploying. The previous record is
[GOBRICKS_V072_UPGRADE.md](GOBRICKS_V072_UPGRADE.md).

## At a glance

- **No source change, no schema migration and no config key in the bump.**
  The bump leaves `config.yml` and every `.go` file unchanged, so the binary's
  only new inputs are `go.mod` and `go.sum`. Its other edits are the
  `GO_BRICKS_REF` pin in the Makefile, the version literals in the docs, one
  script comment, and this record.
- **No compile or test break.** No atom matches a demo call site that the
  compiler or a test sees.
- **No feature adopted in the bump itself.** The skipped ones and why are under
  [Features not adopted](#features-not-adopted). The commits after the bump
  adopt some of them; see [Adopted after the bump](#adopted-after-the-bump).
- **Behaviour the operator sees changes:** AMQP redials after a connection close
  now wait (C73.3), the keep-alive idle default is 75s (C73.5), an over-limit
  body answers 413 `BODY_TOO_LARGE` (C73.6), stream reattach waits are 3–8s
  (C73.11), credential URLs in errors and log fields render `xxxxx` (C73.18),
  and two AMQP metrics are fixed.
- **Operator must-dos (non-development deploys):** remove
  `MESSAGING_BROKER_VIRTUALHOST`, `MESSAGING_ROUTING_EXCHANGE`,
  `MESSAGING_ROUTING_KEY` and every `MESSAGING_HEADERS*` variable (C73.7);
  check the idle timeout of every proxy in front of the service (C73.5);
  re-point log queries and alerts on the changed texts and codes.

## Version path

| Hop | Commit date | What moves in `go.mod` |
|-----|-------------|------------------------|
| v0.72.0 → `4cacc99c` | 2026-10-29 | go-bricks, `echo/v5` 5.4.0 → 5.4.2 and `koanf/v2` 2.3.8 → 2.4.1 (both direct), plus 5 indirect modules. See [Moved modules](#moved-modules). |

The `go` directive (`go 1.27.0`) and the `toolchain` line (`go1.27.3`) are
unchanged. `go-jose/v4` stays at 4.1.6 and `golang.org/x/crypto` does not move,
so the JOSE and sealing paths run on the same crypto modules as before.

## Changes that reach this demo

Classes: **hit** means the demo's code or config meets the change, **forced**
means the demo's runtime behaviour changes with no code change, **none** means
no effect.

| Atom | What | Class | Demo file:line | Decision |
|------|------|-------|----------------|----------|
| C73.1 | A type-less migration tenant runs with the runner's vendor; an undeliverable migrator identity is refused. | none | Every tenant sets `type: postgresql` ([config.multitenant.yaml:28](../config.multitenant.yaml), :37, :46). No migrator identity is set. | None. |
| C73.2 | The seal runtime is per App. A seal-tagged declaration on a hand-built `messaging.NewDeclarations()` fails `Validate`. | none (tests already expect it) | [cmd/api/messaging_declarations_test.go:125](../cmd/api/messaging_declarations_test.go) declares payments on a hand-built set, and :215-216 already assert the not-configured error. [payments/module_test.go:32](../internal/modules/payments/module_test.go) never calls `Validate`. | None. Both tests pass unchanged. |
| C73.3 | Every redial after an AMQP connection close waits. A connection that closes within `reconnect.delay` climbs the backoff ladder. | forced | Every AMQP client. `make demo-consumer-readiness` force-closes the consumer's connection ([scripts/consumer-readiness-demo.sh:490](../scripts/consumer-readiness-demo.sh)). | Keep the defaults. See [Forced behaviour changes](#forced-behaviour-changes). |
| C73.4 | `LogEvent.Err` drops a typed-nil pointer error under a redactor or an `error` needle. | none | The only demo needle is `pan` ([config.yml:221-222](../config.yml)). No `ErrorRedactor`. | None. |
| C73.5 | `server.timeout.idle` defaults to 75s instead of 60s. | forced | No `idle` key in `config.yml` or `config.development.yaml`, and no `SERVER_TIMEOUT_IDLE` in the repo. | Accept 75s. |
| C73.6 | An over-limit body answers 413 `BODY_TOO_LARGE`. It was 413 `INTERNAL_ERROR`, or 400 for an unknown length on a typed route. | forced (wire only) | No test, script or load test reads the code of a 413. The tokens VTS Issuer simulator's own 413 `JOSE_BODY_TOO_LARGE` ([vts_issuer_peer_simulator.go:170](../internal/modules/tokens/service/vts_issuer_peer_simulator.go)) is the demo's code and does not change. | None. |
| C73.7 | `messaging.broker.virtualhost`, `messaging.routing.exchange`, `messaging.routing.key` and `messaging.headers` fail startup. | none in the repo | No hit in YAML, env files or Go. | See [Removed messaging keys](#removed-messaging-keys). |
| C73.8 | A zero `server.timeout.idle` takes 75s on every config door; a negative fails startup. | none | No idle anywhere. | None. |
| C73.9 | `WithFields` writes a typed-nil pointer error as `null`. | none | Every demo `WithFields` map holds strings and numbers only. | None. |
| C73.10 | `\:<verb>` after a route parameter is an inline verb. | none | At the bump, no `\:` in any route. | None in the bump. Showcased after it: `GET /api/v1/products/<id>:activity`. |
| C73.11 | Stream client v1.9.0: each reattach wait is 3–8s, a lost super-stream consumer stays detached, and `rabbitmq-stream+tls://` connects. | forced | The activity module imports the lane ([activity/module.go:35](../internal/modules/activity/module.go), [activity/service/service.go:23](../internal/modules/activity/service/service.go)). Development uses plaintext 5552 ([config.development.yaml:51](../config.development.yaml)). | Keep. |
| C73.12 | Echo's `RouteNotFound` and `RouteAny` methods fail startup at framework doors. | none | No such route. | None. |
| C73.13 | A delivered-empty key is refused in any letter case. | none | No upper-case config key in the repo. | None. |
| C73.14 | A root config key spelled other than its lowercase section name fails startup. | none | Every root key is lowercase. `TestConfigLayoutDevelopmentLoadsBaseAndOverlay` loads the base and the overlay on the new pin. | None. |
| C73.16 | `In`/`NotIn` over a nil pointer to a list is the empty set; list elements are judged; `JSONContains` refuses an expression; `InsertStruct` splices an expression field. | none | No `In`, `NotIn` or `JSONContains`. `InsertStruct` at [products/repository/repository.go:71](../internal/modules/products/repository/repository.go) and :291 takes a struct of plain fields. | None. |
| C73.17 | The six comparator methods on `*database.QueryBuilder` are deleted. | none | Every compare goes through `qb.Filter()` ([products/repository/repository.go:92](../internal/modules/products/repository/repository.go), :246, :327). | None. |
| C73.18 | A credential URL in AMQP, streams, Oracle, httpclient and migration-source errors and log fields renders one posture. | forced (log text) | The streams `uri` log field for [config.development.yaml:51](../config.development.yaml). No test pins the old text. | None. |
| C73.19 | Two keys of one map that differ only by case fail startup. | none | No mixed-case key. | None. |

**Commits without an atom that reach the demo:**

| Commit | What | Effect here |
|--------|------|-------------|
| `678166d4` | AMQP close notifications. The `AMQP connection closed, reconnecting...` and `AMQP channel closed, reinitializing...` INFO lines now carry `error`. `messaging.connection.close` and `messaging.channel.close` gain `error.type` when the broker sent a reason. | A query that groups by that attribute splits. `sum()` is unaffected. |
| `5f6f5204` | The AMQP meter is bound before the lifecycle counters are read. The first lifecycle event of a process, normally its first `messaging.connection.create`, was dropped. It is now counted. | `messaging.connection.create` reads one higher per process start. |
| `424a51e9` | Typed routes decode every admitted JSON media type themselves, instead of through echo's `Bind`. | `application/json` is unchanged. `+json` media types now bind. Every demo route and load test sends `application/json`. |
| `01d20718` | The scheduler's unknown-job 404 no longer echoes `job_id`. | Only `POST /api/v1/_sys/job/:jobId` with an unknown id. `make advisory-lock-demo` triggers the known `test-job`. |
| `3ecef7eb`, `181f7c7c`, `5dc22aab`, `9d99ef6e`, `f47c6b6f`, `98f0c16b`, `4cacc99c` | Module bumps. | See [Moved modules](#moved-modules). |

**No schema change.** `outbox/` and `inbox/` have no non-test change in the
range, and no golden SQL file under `outbox/testdata` or `inbox/testdata` moved.
The demo's `migrations/` need nothing.

**Query building is unchanged for the demo.** A scratch module rendered
equivalents of every demo statement under both pins: the products insert, get,
count, list, update and delete, and the analytics insert and top-viewed query.
The SQL, arguments and errors were identical. The repository tests match SQL by
substring, so they alone would not show this.

## Forced behaviour changes

These need no code change, but an operator sees them.

- **AMQP redial waits (C73.3).** After a connection close, the client used to
  redial at once. Now every redial waits and logs `AMQP connection closed,
  waiting before redial` with `uptime`, `attempt` and `backoff`. It is INFO after
  a connection that stayed up at least `messaging.reconnect.delay` (default 5s),
  and WARN after a shorter one. After a stable connection the wait is a random
  value below `reconnect.delay`, 2.5s on average. A connection that closes sooner
  climbs a jittered ladder up to `messaging.reconnect.maxdelay` (default 60s).
  During the wait the client is not ready:
  - a publish that starts then waits up to `messaging.reconnect.readytimeout`
    (default 5s) before it fails;
  - the messaging readiness check fails. That fails `/ready` only under
    `messaging.consumers.critical`. Under that key, a close of the publisher's
    connection can now fail `/ready` for up to `reconnect.delay`, and
    re-subscribe attempts made during the wait count toward a consumer's streak.
- **Keep-alive idle 60s → 75s (C73.5).** The app listener and the probe listener
  now close an idle keep-alive connection after 75s. A load balancer or proxy
  with a 60s upstream idle (the default of an AWS ALB and of ingress-nginx) now
  closes first, so the 502s it answered when the service closed first go away.
  Idle connections are held 15s longer.
- **Over-limit body (C73.6).** A body over `server.bodylimit` (default 10 MB)
  now answers 413 with `code` `BODY_TOO_LARGE`, with or without a known length.
  A known length used to answer 413 `INTERNAL_ERROR`, and an unknown length on a
  typed route answered 400 `BAD_REQUEST`. On a JOSE route, an unknown length
  still answers `JOSE_BODY_TOO_LARGE`.
- **Stream reattach timing (C73.11).** Each reattach wait is 3–8s, where the
  first was 3–11s and later ones doubled to 15s. Counting the supervisor's 5s
  pass, a lost plain stream's consumer is now reported within about 13s and its
  producer within about 21s. Once one partition of a super-stream consumer is
  lost, its other queued partitions stay detached until restart. An orderly stop
  no longer logs `Failed to close stream consumer` or `Failed to close stream
  publisher` for a handle already reported lost.
- **Masked credential URLs (C73.18).** Passwords in these URLs render as
  `xxxxx`, and a query renders as `?xxxxx`. In development, the streams `uri`
  field on `Connected to RabbitMQ stream endpoint` read `guest:****@` and now
  reads `rabbitmq-stream://guest:xxxxx@localhost:5552/%2f`. The `broker_url`
  field renders `***` through the app's filtered logger, because `broker_url` is
  one of the framework's default filter needles. A username that holds `@`, `/`,
  `?` or `#` is now masked whole. Which URL is dialed does not change.
- **AMQP metrics.** See `678166d4` and `5f6f5204` above.

## Removed messaging keys

C73.7 removes four keys that no framework code ever read:

| Key | Variable |
|-----|----------|
| `messaging.broker.virtualhost` | `MESSAGING_BROKER_VIRTUALHOST` |
| `messaging.routing.exchange` | `MESSAGING_ROUTING_EXCHANGE` |
| `messaging.routing.key` | `MESSAGING_ROUTING_KEY` |
| `messaging.headers` (and every key under it) | `MESSAGING_HEADERS*` |

Any delivery of one of them now fails startup with a `config_invalid` error that
names the key and where the setting belongs. That includes an empty value
(`MESSAGING_BROKER_VIRTUALHOST=`) and a YAML null (`virtualhost:`). The error
never contains the broker URL.

- **The vhost was never read.** The AMQP client dials `messaging.broker.url` as
  written, so the vhost has always been the URL's path, or `/` for an empty
  path. If the key held a vhost other than `/` and that is the vhost you want,
  put it in the URL path, percent-encoded: `orders` becomes
  `amqps://host:5671/orders`, and `/orders` becomes `amqps://host:5671/%2Forders`.
  That changes the vhost the service connects to, so first check that the vhost
  exists and that the user has permissions on it.
- **Exchange, routing key and headers** configured nothing. They belong on each
  publisher's `messaging.PublisherOptions` (`Exchange`, `RoutingKey`,
  `Headers`), which is where the payments publisher already sets them
  ([payments/module.go:128](../internal/modules/payments/module.go)).

The demo's config files, env files and Go code carry none of the four.
`config.LoadFromMap` skips `Validate`, so `go-bricks-migrate --source-config`
still decodes a file that carries them.

## Moved modules

| Module | From | To | Direct? | Note |
|--------|------|----|---------|------|
| `github.com/gaborage/go-bricks` | v0.72.0 | `v0.72.1-0.20261029151308-4cacc99c4fad` | direct | This upgrade. |
| `github.com/labstack/echo/v5` | v5.4.0 | v5.4.2 | direct | v5.4.1 is tagged `[security]` upstream; its advisory text was not read here. The source diff hardens static-file paths, trusts `X-Real-IP` only from a trusted peer (go-bricks uses the `X-Forwarded-For` extractor, which is unchanged), rewrites the router for inline verbs, and no longer pools the body-limit reader. |
| `github.com/knadh/koanf/v2` | v2.3.8 | v2.4.1 | direct | The config layout tests load the base and the overlay on it. |
| `github.com/rabbitmq/amqp091-go` | v1.15.0 | v1.16.0 | indirect | Reader hardening; heartbeat frame checks; no goroutine leak on a failed dial; a dial reports the broker's close reason instead of a fixed 403. |
| `github.com/rabbitmq/rabbitmq-stream-go-client` | v1.8.3 | v1.9.0 | indirect | C73.11. |
| `google.golang.org/grpc` | v1.84.0 | v1.85.0 | indirect | The OTLP gRPC exporter's transport. |
| `github.com/go-co-op/gocron/v2` | v2.22.0 | v2.23.0 | indirect | The scheduler. |
| `github.com/fxamacker/cbor/v2` | v2.9.6 | v2.10.0 | indirect | go-bricks `cache` only; the demo enables no cache. |

This bump supersedes the open Dependabot pull requests for `echo/v5` 5.4.2
(#139) and `koanf/v2` 2.4.1 (#138). The `aws-sdk-go-v2` modules do not move.

## Features not adopted

None in the bump. Skipped there, with the reason:

- **JOSE options (ADR-150):** the open-only `SealModeJWSofJWEMinimal` (RS512,
  for peers that send no kid), `Policy.AcceptMissingKid`, `Policy.OmitKid` and
  `jose.AttachDetachedJWS`. None replaces demo code, and the demo's peers all
  send a kid. The default posture is unchanged: the relays' policies set neither
  flag, and a missing kid is still refused.
- **`rabbitmq-stream+tls://` stream URIs (C73.11).** Development stays on
  plaintext 5552. For deploys, see the table below.
- **`server.RouteConflict.Reason`.** Nothing in the demo builds a
  `RouteConflict`.

## Adopted after the bump

The commits after the bump change no `go.mod` line, no config key and no
schema. They add one route and some assertions, and document the rest.

- **Inline verb (C73.10).** The activity module serves
  `GET /api/v1/products/<id>:activity`, registered as `/products/:id\:activity`
  beside the products module's `GET /products/:id`. On v0.72.0 startup refused
  that pair as a duplicate. It answers one product's slice of the stream
  projection, and an id with no projected events answers 200 with zero counts,
  because the projection cannot tell whether a product exists.
  `cmd/api/inline_verb_route_test.go` boots both modules' routes on a real
  server and checks the dispatch, including that a percent-encoded colon
  reaches `GET /products/:id`. The route adds a router node beside
  `GET /products/:id`, which the products load tests call often; its cost was
  not measured. The route also made an old gap easy to hit: a malformed product
  id reached PostgreSQL's UUID column and answered 500. The products routes now
  validate the id as a UUID and answer 400 before any query.
- **`+json` binding (`424a51e9`).** `scripts/test-products-api.sh` posts a
  product as `application/vnd.gobricks.product+json` and as `Application/JSON`,
  and expects 201 for both. No route was added for it.
- **413 `BODY_TOO_LARGE` (C73.6).** The same script sends a body one byte over
  `server.bodylimit`, with a `Content-Length` and chunked, and expects 413
  `BODY_TOO_LARGE` for both. The chunked body is one long JSON string, because
  a body that stops being valid JSON earlier answers 400 before the limit.
- **Paced redial (C73.3, `678166d4`).** `scripts/consumer-readiness-demo.sh`
  asserts, after its forced close, the `AMQP connection closed, reconnecting...`
  line with the broker's reason in `error` and the `AMQP connection closed,
  waiting before redial` line. The publisher arm is not demonstrated: its wait
  can be close to zero, so a 503 may never be seen.
- **Docs.** CLAUDE.md gains a request-binding note, the inline-verb section,
  the redial and lifecycle-counter rows under Consumer-Aware Readiness, the
  `qb.Filter()`-only rule, the scheduler's 404 clause, the order-independent
  payments validation test, the stream re-timing, and four troubleshooting
  entries. `wiki/DEPLOYING.md` gains streams over TLS, the idle timeout and
  the retired messaging keys. `wiki/MULTI_TENANT_MIGRATION_DEMO.md` notes the
  type-less tenant change.
- **Verified** on the docker stack with an app built from those commits:
  `scripts/test-products-api.sh` passed all 13 checks, and
  `make demo-consumer-readiness` passed twice. The first run logged the redial
  wait at INFO with `attempt` 0 and a 2.5s backoff, reached the 503 on the
  consumer arm 53s after the close, and answered 200 again 24s after the
  restore. In the second, the closed connection had been up 4.9s, under
  `reconnect.delay`, so the line was a WARN with `attempt` 1, as C73.3 says.

## Per-environment operator config changes required before deploy

### Development (`config.development.yaml`, docker compose)

None. `TestConfigLayoutDevelopmentLoadsBaseAndOverlay` loads and validates the
base and the development overlay on the new pin, and the app booted on the
docker stack (see [Verification](#verification)).

### Non-development deploy (env vars, per [DEPLOYING.md](DEPLOYING.md))

| Item | Decision | Before deploy |
|------|----------|---------------|
| `MESSAGING_BROKER_VIRTUALHOST`, `MESSAGING_ROUTING_EXCHANGE`, `MESSAGING_ROUTING_KEY`, `MESSAGING_HEADERS*` (C73.7) | The demo sets none, and DEPLOYING.md lists none. | Remove each from manifests, env files and secrets. An empty value fails startup too. Move a vhost other than `/` into the broker URL path, as [above](#removed-messaging-keys). |
| `SERVER_TIMEOUT_IDLE` (C73.5, C73.8) | Unset, so 75s. | Check the idle timeout of every proxy in front of the service. Nothing to do when every L7 proxy has an upstream idle below 75s and every L4 load balancer has a flow idle above 75s. Otherwise set the key above the L7 proxy's idle and below the L4 balancer's (350s on an AWS NLB). `0` now means 75s, and a negative value fails startup. |
| `MESSAGING_RECONNECT_READYTIMEOUT` (C73.3) | Unset, so 5s. `outbox.publishtimeout` is 60s ([config.yml:305](../config.yml)). | Raise it only if publishes must ride through a reconnect wait. Startup then also requires `messaging.publishtimeout` (when set) to be at least `readytimeout + connectiontimeout`, and `outbox.publishtimeout` at least `readytimeout` and at least `connectiontimeout` (plus `resenddelay` unless `maxpublishattempts` is 1). |
| `MESSAGING_CONSUMERS_CRITICAL` (C73.3) | Off by default. | Where it is on, `/ready` can answer 503 for up to `reconnect.delay` after the publisher's connection closes. Size readiness probe thresholds for that, and keep liveness on `/health`. |
| `MESSAGING_STREAMS_URI` with `rabbitmq-stream+tls://` (C73.11) | DEPLOYING.md's example uses this scheme. Before this pin such a URI timed out at startup. | It now connects only if the broker certificate chains to the system trust store and names the dialed host, `MESSAGING_STREAMS_ADDRESSRESOLVER_HOST` included. go-bricks has no CA seam. Otherwise startup fails with an x509 error. |
| Mixed-case config keys (C73.13, C73.14, C73.19) | The repo is clean. | A rendered config file with keys such as `Server:` or `Port:`, or two keys that differ only by case, now fails startup. |
| Log queries and alerts | — | Re-point anything that matches the old URL text (C73.18), a 413 with `INTERNAL_ERROR` (C73.6), the 3–11s stream-loss window (C73.11), or `messaging.connection.create` counts across a version boundary (`5f6f5204`). |
| Performance comparisons across versions | See [PERF_VERSION_COMPARISON.md](PERF_VERSION_COMPARISON.md). | The idle default moves from 60s to 75s, which changes how a 60s proxy behaves after load. To compare with v0.72.0 series, either accept that or hold 60s with `SERVER_TIMEOUT_IDLE=60s`; an env var is the only door, because the method forbids a `config.yml` change mid-series. echo's body-limit reader is now one heap allocation per request (source read, not measured). |

### Multi-tenant migrate CLI (`config.multitenant.yaml`, `go-bricks-migrate`)

| Item | Decision | Before deploy |
|------|----------|---------------|
| `GO_BRICKS_REF` | Now `4cacc99c4fad` (was `v0.72.0`). The recipe checks the ref out with git, which cannot resolve a pseudo-version, so a main pin is written as its commit hash. `tools/migration` has no tag at this commit (the latest is `tools/migration/v0.72.0`). That does not matter: the recipe builds the CLI through the checkout's `go.work`, so it links the in-tree go-bricks at the commit. The CLI built here therefore carries C73.1, C73.14 and C73.19 now. | Rebuild with `make migrate-multitenant-install`. With `GO_BRICKS_PATH` set, check out `4cacc99c` there first. Still never export `GOBRICKS_MIGRATE_MIGRATOR_USER` or `GOBRICKS_MIGRATE_MIGRATOR_PASSWORD`. |
| Tenant vendor (C73.1) | `acme`, `globex` and `initech` each set `type: postgresql`. | None. |

### `.env`, `Dockerfile`, compose, CI, load-test jobs

No change. The `Dockerfile` builds with `GOWORK=off`, so the image resolves the
pseudo-version through the module proxy. The `seal-event`, `open-event` and
`seal-payload` scripts read their version from `go.mod`, and both CI workflows
read the Go version from it. No load-test script changed.

## Verification

- The gates ran with `GOWORK=off`, so they compiled against the `go.mod` pin:
  `make check` passed (fmt, lint, and the race-enabled tests). No test changed.
- `make migrate-multitenant-install` checked out `4cacc99c4fad` in a scratch
  clone, built the CLI and confirmed it linked the in-tree go-bricks.
  `go-bricks-migrate list --source-config config.multitenant.yaml
  --credentials-from config-file` decoded the file and listed `acme`, `globex`
  and `initech` (exit 0).
- `go install` of `cmd/seal-event` and `cmd/open-event` at the pseudo-version
  resolved through the module proxy, as the README examples now spell it.
- Live boot on the docker stack: `postgres`, `postgres-analytics` and `rabbitmq`,
  with the default database and the broker on other host ports, because another
  local stack held the standard ones. Both databases were already at their
  latest migration (`V4` and analytics `V1`). The boot loaded five keystore
  entries, validated the full messaging declaration set with payments in it,
  declared the payments exchange, three queues and three bindings, started the
  payments consumer and the stream consumer, and logged the masked stream `uri`
  above. The boot logged no ERROR. Its one WARN was the expected development
  CORS wildcard notice.
- Smoke calls: `/api/v1/health` 200; `/api/v1/ready` 200 with
  `{"status":"ready"}`; products list 200, create 201, get 200, delete 204;
  `POST /api/v1/payments/authorize` 202, then `Payment authorization consumed
  exactly once` in the log and one sealed copy on the tap queue, with the test
  PAN absent from the log; webhooks sign 200 (`keyName` `webhooks.signing`),
  verify 200 with `valid` true, and a tampered payload `valid` false;
  `GET /api/v1/products/activity` 200 with `publisherReady` true and the new
  product's event on a partition. The graceful shutdown logged no WARN or ERROR.
- C73.6 on the same stack: an 11 MB JSON body to `POST /api/v1/products`
  answered 413 with `code` `BODY_TOO_LARGE`, both with `Content-Length` and
  chunked.
- C73.3 on the same stack: closing an AMQP connection that had been up 14s,
  through the management API, logged `AMQP connection closed, reconnecting...`
  with the broker's `CONNECTION_FORCED` reason in `error` (`678166d4`), then
  `AMQP connection closed, waiting before redial` at INFO with `attempt` 0 and a
  backoff of about 4.2s, below `reconnect.delay`. The topology was redeclared on
  the new channel and `/ready` answered 200 again.
- `make demo-consumer-readiness`, pointed at the same stack, passed. It closes
  the consumer's own connection, so C73.3's wait applies. The publisher's
  connection stayed up and `/ready` stayed 200 while the streak climbed. The
  streak reached 5 about 70s after the close. `/ready` then answered 503 with
  `{"status":"not ready"}`, and the debug view named the consumer arm
  (`consumer re-subscribe exhausted`). After the script restored the
  permissions, the consumer re-subscribed and `/ready` answered 200 within 29s.
- Not checked here: the echo v5.4.1 advisory text; the changelogs of koanf
  v2.4.1, gocron v2.23.0, grpc v1.85.0 and cbor v2.10.0; the per-request
  allocation change; `make redeclare-demo` (an exchange delete closes a channel,
  not a connection, so C73.3 does not apply); any load test; and any deployed
  environment's variables.

## Open follow-up

Re-pin to the `v0.73.0` tag once it is published. Expected work: `go get
github.com/gaborage/go-bricks@v0.73.0` and `go mod tidy` with `GOWORK=off`; set
`GO_BRICKS_REF` to `v0.73.0`; change the pseudo-version literals in CLAUDE.md
and the README and the `4cacc99c` mentions in CLAUDE.md and
`scripts/consumer-readiness-demo.sh` back to the tag; re-point the go-bricks
wiki and ADR links pinned to commit `4cacc99c` in CLAUDE.md,
`wiki/DEPLOYING.md` and `wiki/MULTI_TENANT_MIGRATION_DEMO.md` to the tag;
confirm that the tag adds nothing past `4cacc99c` but the release commit; and
add a hop to this record.
