# go-bricks v0.64.0 → v0.67.0: operator decision record

This record covers the upgrade through v0.65.0, v0.66.0 and v0.67.0. For each
environment it lists the runtime changes that reach this demo, the decision
taken, and what an operator must do before deploying. Symptoms and fixes are in
the CLAUDE.md "Common Troubleshooting" section; this page records **decisions**.
Later feature commits append to [Adopted features](#adopted-features).

## At a glance

- **No schema migration.** The outbox and inbox DDL did not change, and
  `gobricks_inbox` keys keep their spelling.
- **No config key is required.** Every environment passes the new validators
  as it is.
- **Operator must-dos:** rebuild the migrate CLI; repoint external readers of
  `active_consumers`; never export the migrator identity pair (see below).

## Per-environment decisions

### `config.development.yaml` (APP_ENV=development, the only runtime environment)

| Item | Decision | Before deploy |
|------|----------|---------------|
| `messaging.consumers.critical` (v0.65.0, #1686) | **Absent (false) by default.** A commented example in this file plus a demo script that turns it on only for its own run is the opt-in path; it arrives with the readiness showcase. The reason: with the key on, `/ready` also answers 503 whenever the publisher is not ready, so turning it on is a per-environment call, not a demo default. | None. An environment that turns it on (env `MESSAGING_CONSUMERS_CRITICAL`) accepts 503 on consumer give-up (5 failed re-subscribes) **and** on publisher not-ready. |
| `messaging.declare.externalwait` (v0.67.0, #1774) | **Not set, so the default `0` applies:** a startup 404 aborts at once, as before. The default boot owns every exchange it uses; the only external one belongs to the opt-in partnerfeed module (see [Adopted features](#adopted-features)), and `make external-exchange-demo` sets the key for its own run only. | None. If it is ever set: a mistyped external name spends the whole budget, and the startup probe must allow first attempt + `externalwait` + one final attempt. |
| Sealed dedup key (v0.65.0 #1630, v0.66.0 #1700) | The payments handler takes the key from its own delivery and calls `ProcessOnce` under the handler ctx, so it complies. **It keeps logging `jti` and `dedupKey` at INFO on purpose:** the framework never renders a sealed key, but `seal-event-demo` correlates inbox dedup through it, and it spells `<sign family>:<jti>`, never the PAN or a secret. | None. Rule for new consumers: never cache a key, and never call `ProcessOnce` from `context.Background()` or a detached goroutine (`ErrInvalidEventID`). |
| JOSE relays refuse a plaintext 2xx (v0.65.0, #1637) | **`AllowPlaintextSuccess` stays unset** on `/tokens/relay`, `/tokens/mle-relay` and `/tokens/vts-issuer-relay`. The in-process simulators seal every 2xx, so nothing changes. | Pointing a relay at a real partner requires that partner to protect **every** 2xx route; fix the partner, never set the flag. |
| AMQP redeclare per channel and re-subscribe WARN (v0.65.0, #1676/#1675) | Accept the framework behavior (no config key). | Expect `Messaging topology redeclared on new channel` (INFO) after reconnects, `Consumer re-subscribe attempt failed, will retry` at WARN from the 5th failed attempt, and a WARN plus skip-until-restart for a 406 at reconnect. The streams lane (`product-activity`) is declared at startup only: after a broker wipe, restart the app. |
| Publisher-driven topology repair (v0.67.0, #1776/#1779) | Accept the framework behavior. The payments typed publisher stays without `Mandatory`. | Expect `Messaging topology redeclared on new channel` (INFO) also when a publisher's channel is replaced (a publish into a deleted exchange, a dropped publisher connection). It does not appear at the first publish, and usually not at boot, because the publisher is leased before the consumer registry exists. It can appear once at boot, with `channel_generation` 1, when the pre-init publisher's first channel comes up after consumer setup has begun; that line is benign. After startup, or with a higher generation, it means a publisher channel was replaced. **Treat a deleted exchange as an incident:** in the repair window a payment can get 202 and still be lost (broker-acked, unroutable). Reconcile the payments authorized in that window. |
| Database sections (`database`, `databases.analytics`) | No change: TCP `localhost`, no `connectionstring`, no `database.tls`. They pass #1613, #1642, #1699 and #1715. | None. |
| Cache | No cache section, and none is planned. | None (see conditional rules). |

### `config.multitenant.yaml` (read only by the `go-bricks-migrate` CLI)

| Item | Decision | Before deploy |
|------|----------|---------------|
| Tenants `acme`, `globex`, `initech` | No change. They use TCP `localhost` with no `connectionstring`, no TLS and no reserved schema or role names, so they pass the v0.65-v0.67 checks. | None. Secret-bearing entries (names only): `multitenant.tenants.<tenant>.database.password`. They are untouched. |

### `.env` / `.env.example`

| Item | Decision | Before deploy |
|------|----------|---------------|
| Variables | No change. `.env.example` carries only `NEW_RELIC_LICENSE_KEY` and `NEW_RELIC_REGION`. | **Never add or export `GOBRICKS_MIGRATE_MIGRATOR_USER` or `GOBRICKS_MIGRATE_MIGRATOR_PASSWORD`** in any shell or CI job that runs `make migrate-multitenant-*`. One alone makes every run exit 2. Both together make one role run every tenant's DDL, which collapses the per-role `search_path` tenant isolation. Do not add `PGSERVICE` or `PGSSL*` either (see conditional rules). |

### `etc/docker/docker-compose.yml` (broker, PostgreSQL, observability)

| Item | Decision | Before deploy |
|------|----------|---------------|
| RabbitMQ service and retained volume | No change. | None new. The v0.64.0 classic-to-quorum trap on a retained volume is still a **startup** failure (see the DLQ troubleshooting entry). |
| PostgreSQL services | No change. | None. |

### Makefile and CLI pins

| Item | Decision | Before deploy |
|------|----------|---------------|
| `GO_BRICKS_REF` (migrate CLI source) | Bumped to `v0.67.0` to match `go.mod`. | Run `make migrate-multitenant-install` to rebuild `go-bricks-migrate`. The CLI now exits 0 (clean), 1 (fleet split) or 2 (nothing attempted), and prints `verdict=…, N listed, N attempted, N failed, N not attempted` (#1771/#1770). No Makefile logic change is needed: `make` stops on any non-zero exit. |
| `SEAL_EVENT_VERSION` (`scripts/seal-event-demo.sh`, `scripts/show-sealed-message.sh`) | No pin to bump: both scripts default it to the go-bricks version in `go.mod` (`go list -m`, as `scripts/seal-payload.sh` does), and the variable is an override only. The CLI's output is unchanged across v0.64-v0.67. | None. |

### CI (`.github/workflows/ci.yml`, `security.yml`)

| Item | Decision | Before deploy |
|------|----------|---------------|
| Build, test, lint, govulncheck | No workflow change. CI resolves go-bricks from `go.mod` because `go.work` is untracked. | Run the checks CI does not run locally: boot (`make dev && make run`), `make show-sealed-message`, `make seal-event-demo`, `make loadtest-smoke`, and the multi-tenant migrate targets with the rebuilt CLI. |

### External dashboards and alerts (outside this repo)

| Item | Decision | Before deploy |
|------|----------|---------------|
| `/ready` `messaging_stats.active_consumers` (v0.65.0, #1684) | The key is renamed. Nothing in this repo reads it. | Repoint Grafana panels saved in the UI and New Relic NRQL or alerts to `consumer_registries` (same meaning), or to `declared_consumers` / `subscribed_consumers`. Optional new panels: `consumer_resubscribes` and `consumer_max_fail_streak`. |

### Helm / Kubernetes

None exist in this repo.

## Conditional rules (no environment hits them today)

- **PostgreSQL `connectionstring`** (v0.66.0, #1715): must not carry `service=`
  (even an empty one), and must not run with `PGSERVICE` set. Inline the keys
  instead.
- **TLS on a unix-socket host** (v0.65.0 #1613/#1642, v0.66.0 #1699): refused
  whether it comes from a `database.tls` block, from `ssl*` keys in the
  connection string, or from `PGSSL*` variables. Use a TCP host.
- **Empty host**: a connection string or host list that names no host,
  including an empty comma-separated entry, is refused.
- **Cache** (v0.66.0, #1740): before any environment sets `cache.enabled`,
  decide `cache.redis.keyprefix`. When it is absent, keys are namespaced under
  `app.name` and the cache re-keys once; an explicit `""` opts out.
- **AWS tenant secret store** (`internal/modules/shared/secrets/`, not wired):
  before wiring it, audit each tenant secret's host and TLS fields (by entry
  name) against the rules above.

## Adopted features

Each feature commit on top of this upgrade appends a row here: the feature, the
decision, and any operator action.

| Feature | Decision | Before deploy |
|---------|----------|---------------|
| `product-events` declared with `DeclareTopicExchange` (v0.66.0, #1712) | The hand-built `RegisterExchange` literal in the products module becomes the typed helper. The stored declaration is field-for-field identical (durable topic, not auto-delete, not internal, empty args); `internal/modules/products/module_test.go` pins it against the old literal. | None. A broker that already holds `product-events` sees an equivalent redeclare, so a retained volume needs no reset. |
| Products report job under a PostgreSQL advisory lock on a pinned `db.Session` (v0.65.0, #1639 / #1650, ADR-112) | **Adopted: one runner per run, elected by a session-level try-lock.** Each run opens a Session from the job's DB, calls `pg_try_advisory_lock(ReportLockKey)` (`0x52505254`), and on success logs `Report job lock acquired`, runs the report, then `pg_advisory_unlock` on a detached 5s context before `Close`. On failure it logs `Report job skipped: another replica holds the lock` and returns nil. With several replicas, at most one generates the report at a time. New optional key `custom.products.report.hold` (env `CUSTOM_PRODUCTS_REPORT_HOLD`, Go duration, unset = 0) keeps the lock after the report. Only `make advisory-lock-demo` sets it. | None for `config.development.yaml`: the hold stays unset. For an environment that runs several replicas: key `0x52505254` must stay unique among advisory locks in that database; each run holds one extra pool connection; a malformed or negative hold fails startup. |
| Framework `seal-payload` CLI replaces `cmd/seal-payload` (v0.65.0, #1615/#1620) | **The demo-owned tool is deleted, not wrapped.** `make seal-payload` (nested JWE-of-JWS for `POST /api/v1/tokens`: signs as `tokens-peer`, encrypts to `tokens-our`) and `make seal-mle` (bare `A128GCM` JWE in the Visa `{"encData":…}` envelope for `POST /api/v1/__sim/peer/mle`, encrypted to `tokens-peer`, the same header shape as `NewMLEOutboundPolicy`) both run `scripts/seal-payload.sh`. The script reads the CLI version from `go.mod` with `go list -m` under `GOWORK=off`, so there is no second pin to drift, and prints only the sealed body on stdout. It repeats the tokens module's kids, so a kid rename must touch it too (drift shows as `JOSE_KID_UNKNOWN`). | None for any environment: minting is a developer tool and needs no running app. It needs the `tokens_*` key files (`make generate-keys`) and, on a cold module cache, module-proxy access. Unlike the old tool, the CLI does not pre-validate that stdin is JSON. |
| `open-event` CLI in the sealed-message scripts (v0.65.0, #1640 + #1633) | **Adopted in both scripts.** `show-sealed-message` opens the tapped body, and `seal-event-demo` reads `SEAL_EVENT_TYPE_MISMATCH` back off the DLQ-parked body, so no one has to grep the app log. The CLI runs from a scratch `GOBIN` because `go run` hides exit `3`. `-print-subject` is refused in `scripts/lib/open-event.sh`, and both outputs are grepped for the PAN before they are printed. `SEAL_EVENT_VERSION` now pins both CLIs and is read by both scripts; both default it to the go-bricks version in `go.mod`, so there is no second pin to drift. | The consumer-half key files (`certs/payments_sign_v1_public.der`, `certs/payments_encrypt_v1_private.der`) must exist (`make generate-keys`). The first run needs module-proxy access to install the CLI. After a sign/encrypt rotation, pass `OPEN_SIGN_KID` / `OPEN_ENCRYPT_KID` to `show-sealed-message`. |
| Card and PAN masking through `logger.Redactor` (v0.65.0, #1624, ADR-110) | **Adopted as a backstop, not a licence to log bodies.** `domain.CardDetails` renders `{last4}` only (never the PAN, expiry or holder), which also covers `PaymentAuthorized` and `AuthorizeRequest` because both carry it as their `Card` field. The HTTP DTO `handlers.CardRequest` delegates to the same view, so a whole `AuthorizePaymentRequest` logs no PAN, expiry or holder either. `TokenizeRequest`, `PeerSimRequest`, `RelayRequest` and `MLERelayRequest` render `{last4}` through one helper. All hooks use value receivers, pinned by compile-time assertions. The filter consults a hook only at `Interface`/`WithFields`, never at `Err`, `Msgf` or on an unfiltered logger. The explicit `cardLast4` log field stays. | None. `log.sensitivefields: [pan]` stays in `config.development.yaml`: it still covers the relays' outbound `httpclient` body preview, which is marshaled bytes no hook can reach. |
| Relay peer names and the plaintext-2xx refusal pinned (v0.65.0, #1648 + #1637) | **Each relay client names its counterparty** with `WithPeerName`: `tokens-peer-sim` (nested) and `visa-mle-peer-sim` (MLE), set in `module.go` next to the simulator URL they describe. The name labels the outbound `httpclient` metrics and is printed in `ErrJOSEPlaintextResponse` and its WARN. Unit tests pin the fail-closed rule on both relays against a peer that answers 200 `application/json`, including an MLE reply whose `encData` sits under the envelope's `data` member. `loadtests/tokens-mle-relay.ts` gives the MLE relay load coverage on the shared `loadtests/tokens-common.ts` (fixture PANs, response-shape check, scenario factory; no body is ever printed). `wiki/LOAD_TESTING.md` is re-created. | None. A peer name is a low-cardinality metrics label: a real integration gets its own name, never a per-request value. |
| JWS-of-JWE VTS Issuer relay (v0.65.0, ADR-111 + #1610/#1623): `POST /api/v1/tokens/vts-issuer-relay` | **Adopted with an explicit `SigAlg: PS256` on both policies**, because `Build` fills an unset `SigAlg` with RS256 and only the partner would notice. Inner JWE `A256GCM`, `typ: JOSE`, millisecond `iat`; no envelope, `application/jose` both ways; peer name `visa-vts-issuer-peer-sim`. **No `/__sim/peer/vts-issuer` route.** A taggable route always JSON-encodes its result and the raw door carries no tags, so the peer simulator is an `http.RoundTripper` passed through `WithTransport`, the slot production fills with its mTLS transport, and the relay addresses `http://vts-issuer-peer-sim.invalid/tokens` (never resolves). The three modes share the `tokens-our`/`tokens-peer` kids, kept apart only by the MLE policies' `A128GCM` pin, which a test covers. `make loadtest-tokens-vts(-smoke)` measures it; its latencies skip one HTTP hop, so they are not comparable with the other relays. | None: no config key or env var; the URL, peer name and transport are constants in `module.go`. A production integration gives each seal mode its own kids and replaces the simulator transport with its mTLS transport. |
| Readiness that fails closed on a stalled consumer (v0.65.0, #1686/#1684, ADR-114): `make demo-consumer-readiness` | **The key stays absent** in `config.development.yaml`, which has only a commented `messaging.consumers.critical` example explaining both arms. The script boots its own app with `MESSAGING_CONSUMERS_CRITICAL=true`, plus `/_sys/health-debug` on loopback only. It records the app user's vhost permissions (`rabbitmqctl list_user_permissions`) and revokes **read on `payments.authorized` only**. It refuses to run if the recorded read is not the default `.*`. It closes that consumer's connection and polls `/ready` until 503, then restores the exact recorded set. A trap restores it on every exit. **The broker is never stopped:** the publisher arm would answer 503 at once and hide the consumer arm, and product writes would stall up to 2s on the streams publish. The k6 smoke `setup()` logs the `/ready` consumer counters, but only as information: a 503 is expected there and not counted as a failure. | Running the script changes broker authz for the app user for one to three minutes, so run it only against the demo broker. After a SIGKILL, run the `rabbitmqctl set_permissions` line it printed before revoking. An environment that turns the key on must gate liveness on `/health`, never `/ready`. It must also accept that any credential able to revoke consume or delete the queue can take every replica out of rotation (ADR-114 threat note). |
| Publisher-driven topology self-repair, shown and measured (v0.67.0, #1776/#1779, ADR-113 amendment): `make redeclare-demo`, `make loadtest-topology-repair` | **Both tools delete real exchanges and are kept out of `loadtest-all`.** `scripts/topology-repair-demo.sh` deletes `payment-events` under the live app, publishes into the hole, waits for the exchange and both bindings, then does the same for `product-events` through the outbox relay. `loadtests/topology-repair.ts` deletes both at t=60s under 5/s of product writes and payments. Delivery is counted by draining `payments.authorized.tap` for this run's orders, never from queue depth. **"Lost in repair window" is reported, never a threshold:** a payment published between the exchange and its bindings is broker-acked and dropped while the caller gets 202, because the typed publisher sets no `Mandatory`. Thresholds cover HTTP error rate and latency only (payments under 2% failed, so the retry burst is tolerated). Card numbers come from the shared `TEST_PANS` in `loadtests/tokens-common.ts`, and nothing prints a body. | Run both only against a demo broker. Neither repairs the streams lane (`product-activity`), which is declared at startup only. The load test leaves about 600 products and their outbox rows behind and drains the tap. |
| Migrate CLI run verdicts shown side by side (v0.67.0, #1770/#1771, ADR-115): `make migrate-multitenant-verdict` | **A read-only demo, not a Makefile logic change.** `make` stops on any non-zero exit, so the `migrate-multitenant-*` targets cannot tell 1 from 2. `scripts/migrate-verdict-demo.sh` runs `go-bricks-migrate validate --json` (or `VERDICT_ACTION=info`) three ways: the real fleet (0, `clean`), an empty fleet (2, `nothing_attempted`) and the fleet plus one unreachable tenant that carries no credential (1, `fleet_split`). The throwaway configs live in a private temp dir removed on exit, and nothing is migrated. | `validate` needs an applied fleet; otherwise use `VERDICT_ACTION=info`. Case 3 appends a tenant block, so it relies on `multitenant.tenants` staying the last block in `config.multitenant.yaml` (the script checks the count and fails loudly). |
| Tenant roles checked against the PostgreSQL privilege floor (v0.66.0, #1718): `make migrate-multitenant-check-roles` | **A standalone, read-only check, deliberately not chained onto `migrate-multitenant-init`.** `cmd/check-tenant-roles` loads the fleet the way `go-bricks-migrate --source-config` does and calls `migration.CheckPGRoleFloor` for each tenant, logged in as that tenant's own role (no superuser) over a `default_transaction_read_only` connection. It prints OK, `ABOVE FLOOR holds <attribute>` or `NOT CHECKED`, with the password redacted from every error, and exits 0 (all at the floor), 1 (any above, missing or not checked) or 2 (nothing checked). The target builds a binary because `go run` would turn every non-zero exit into 1. A `database.connectionstring` tenant is refused, never quoted. pgx and koanf move from indirect to direct requires. | `init` and Flyway never dial the host port, but this check does: set `PG_HOST`/`PG_PORT` to the demo Postgres, or each tenant's dev password is offered to whatever answers on `localhost:5432`. Keep pgx and koanf compatible with the versions go-bricks pins when Dependabot bumps them. |
| External exchange consumer (v0.67.0, #1773/#1774, ADR-119): `internal/modules/partnerfeed` references `partner-events` with `DeclareExternalExchange` and binds its own quorum queue + DLQ with the exact key `partner.stock.updated` | **Off by default** (`custom.partnerfeed.enabled`, env `CUSTOM_PARTNERFEED_ENABLED`): no partner service runs locally, and a consumer-declaring service aborts startup on the passive declare's 404. The module is always registered and reads the switch in `Init`, so a disabled module declares nothing. It uses a new exchange name because every module shares one declaration set, and marking `product-events` or `payment-events` external would fail `Validate` as an ownership conflict. The binding uses an exact key because a passive declare cannot see the owner's exchange type. `make external-exchange-demo` shows the fast 404 abort, the `externalwait` wait (the script creates the exchange mid-boot) and consumption, then deletes what it created. | None for the default boot. An environment that turns it on needs the owner to have declared `partner-events` first, or `messaging.declare.externalwait` sized with its startup probe (first attempt + wait + one final attempt). A passive declare needs no `configure` permission, so that environment's broker user needs `configure` only on what the module declares (the queue, DLQ and DLX), never on `partner-events`. |
