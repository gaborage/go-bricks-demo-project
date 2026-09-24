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
| `messaging.declare.externalwait` (v0.67.0, #1774) | **Not set, so the default `0` applies:** a startup 404 aborts at once, as before. The demo owns every exchange it uses. | None. If it is ever set: a mistyped external name spends the whole budget, and the startup probe must allow first attempt + `externalwait` + one final attempt. |
| Sealed dedup key (v0.65.0 #1630, v0.66.0 #1700) | The payments handler takes the key from its own delivery and calls `ProcessOnce` under the handler ctx, so it complies. **It keeps logging `jti` and `dedupKey` at INFO on purpose:** the framework never renders a sealed key, but `seal-event-demo` correlates inbox dedup through it, and it spells `<sign family>:<jti>`, never the PAN or a secret. | None. Rule for new consumers: never cache a key, and never call `ProcessOnce` from `context.Background()` or a detached goroutine (`ErrInvalidEventID`). |
| JOSE relays refuse a plaintext 2xx (v0.65.0, #1637) | **`AllowPlaintextSuccess` stays unset** on both `/tokens/relay` and `/tokens/mle-relay`. The in-process simulators seal every 2xx, so nothing changes. | Pointing a relay at a real partner requires that partner to protect **every** 2xx route; fix the partner, never set the flag. |
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
| `SEAL_EVENT_VERSION` (`scripts/seal-event-demo.sh`) | Default bumped to `v0.67.0`. The CLI's output is unchanged across v0.64-v0.67. | None. |

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
