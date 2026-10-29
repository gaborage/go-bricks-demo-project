# Running outside development

`make run` boots with `APP_ENV=development` and reads two files from the working
directory. Any other environment (`APP_ENV=staging`, `production`, ...) reads the
base, plus a `config.<APP_ENV>.yaml` overlay when the deploy supplies one (this
repository ships none for a deployed environment), and takes everything else from
environment variables. This page lists what the base already provides and what a
deploy must supply.

## How configuration loads

go-bricks merges three layers; a later layer wins key by key:

1. **[config.yml](../config.yml)**, the base, loaded in every environment. go-bricks
   reads `config.yaml` first and falls back to `config.yml` only when `config.yaml`
   is absent, so never add a `config.yaml`: it would silently replace the base.
   `cmd/api/config_layout_test.go` fails when one appears.
2. **`config.<APP_ENV>.yaml`** (or `.yml`), when it exists.
   [config.development.yaml](../config.development.yaml) is the only one in this
   repository.
3. **Environment variables.** The name is lower-cased and every `_` becomes a `.`:
   `DATABASE_TLS_MODE` sets `database.tls.mode`.

Maps merge key by key, but a list set by a later layer replaces the earlier list.
An overlay that sets `log.sensitivefields` must repeat `pan`, or PAN masking is lost.

**Set `APP_ENV` explicitly.** It defaults to `development`, and the Docker image
sets `APP_ENV=development`. With that value the development overlay loads
`localhost` hosts, guest credentials and `certs/` key paths. A keystore `*_VALUE`
variable added on top of a `file:` entry is refused at startup (`both 'file' and
'value' set`).

## What the base provides

[config.yml](../config.yml) holds every setting that must match in all environments:

- the app identity, `app.name: go-bricks-demo-project` and `app.version` (bump it
  per release, or override either with `APP_NAME` / `APP_VERSION`). The name is
  the AMQP `app_id` and the startup log's `app`, not the OTel `service.name`,
  which comes from `OBSERVABILITY_SERVICE_NAME`
- the `/api/v1` route prefix and the health and ready paths
- the log filter's `pan` needle and the `auto` log format
- the database types and session timezones (`UTC` default, `Asia/Tokyo` analytics)
- the outbox, inbox and scheduler settings, with `autocreatetable: false`

The development overlay's benchmark and demo knobs do not apply elsewhere. Those
environments therefore run with the framework defaults:

| Setting | Development | Elsewhere |
|---------|-------------|-----------|
| `app.debug` (validation `error.details`) | `true` | `false` |
| `app.rate.limit` / `burst` | 2000 / 4000 | 100 / 200 per IP |
| `server.gzip.minlength` | 0 | 1024 |
| `messaging.streams.offsetstore.countbeforestorage` | 10 | 500 |

## Environment variables a deploy must set

| Variable | Notes |
|----------|-------|
| `APP_ENV` | Anything but `development`. |
| `DATABASE_HOST`, `DATABASE_PORT`, `DATABASE_DATABASE`, `DATABASE_USERNAME`, `DATABASE_PASSWORD` | Default database. A non-empty password must be at least 8 bytes. |
| `DATABASE_TLS_MODE`, `DATABASE_TLS_CA` | `require`, `verify-ca` or `verify-full`, and the CA file path. `DATABASE_TLS_CERT` / `DATABASE_TLS_KEY` for client certificates. |
| `DATABASES_ANALYTICS_HOST`, `_PORT`, `_DATABASE`, `_USERNAME`, `_PASSWORD`, `_TLS_MODE`, `_TLS_CA` | The `analytics` named database, same rules. |
| `MESSAGING_BROKER_URL` | AMQP broker, for example `amqps://user:pass@broker:5671/`. |
| `MESSAGING_STREAMS_URI` | Native stream listener, for example `rabbitmq-stream+tls://user:pass@broker:5551/%2f`. The activity module declares streams, so startup fails without it. |
| `MESSAGING_STREAMS_ADDRESSRESOLVER_HOST`, `_PORT` | Only behind port mapping, NAT or a load balancer. Set both or neither. |
| `KEYSTORE_KEYS_<NAME>_PUBLIC_VALUE`, `KEYSTORE_KEYS_<NAME>_PRIVATE_VALUE` | Base64 DER for each `<NAME>` below. |
| `CORS_ORIGINS` | Comma-separated browser origins. Outside development, no value means no `Access-Control-Allow-Origin`. Not needed for server-to-server callers. |

The keystore entries the modules use, both halves each:

| Entry | `<NAME>` | Used by |
|-------|----------|---------|
| `webhooks.signing` | `WEBHOOKS_SIGNING` | webhooks module: sign and verify. Read on each request, not at startup |
| `tokens.our` | `TOKENS_OUR` | tokens module: this service's JOSE keypair |
| `tokens.peer` | `TOKENS_PEER` | tokens module: the in-process peer simulators' keypair |
| `payments.sign.v1` | `PAYMENTS_SIGN_V1` | payments module: sealing sign family `payments.sign`, generation v1 |
| `payments.encrypt.v1` | `PAYMENTS_ENCRYPT_V1` | payments module: sealing encrypt family `payments.encrypt`, generation v1 |

Entry names are dotted paths (go-bricks ADR-144). The variable name is the
config path upper-cased with `.` turned into `_`, so
`KEYSTORE_KEYS_PAYMENTS_SIGN_V1_PRIVATE_VALUE` reaches entry `payments.sign.v1`
and `KEYSTORE_KEYS_TOKENS_OUR_PRIVATE_VALUE` reaches `tokens.our`. Every name
above is a valid shell identifier, so it can be set with a POSIX `export` as
well as through Docker `-e` / `--env-file`, Kubernetes `env` and `env(1)`. The
sealing entries end in a `v<N>` segment because a dotted family names its
generations `<family>.v<N>`. To mount DER files instead, set `..._FILE` to the
mounted path rather than `..._VALUE`, never both for one half.

These names replaced the flat ones (`WEBHOOKSIGNING`, `TOKENSOUR`,
`TOKENSPEER`, `PAYMENTSSIGN-V1`, `PAYMENTSENCRYPT-V1`). The secrets keep their
contents, but the names are wire identifiers too, so more than the variable
names changes:

- the tokens JOSE kids become `tokens.our` and `tokens.peer`;
- the signed webhook payload's `keyName` becomes `webhooks.signing`;
- the sealed `kid` and inner `iss` become `payments.sign.v1`, and the inner
  JWE `kid` becomes `payments.encrypt.v1`.

A JOSE partner or webhook verifier that pins the old strings must accept the
new ones before the cutover, and the sealing families need a drain first. See
[KEYSTORE_DOTTED_NAMES.md](KEYSTORE_DOTTED_NAMES.md) for the deploy order.

## Optional per-environment settings

All are off or unset by default. [config.yml](../config.yml) documents each one,
commented out:

- `MESSAGING_CONSUMERS_CRITICAL=true`: `/ready` answers 503 when the payments
  consumer stops re-subscribing, and at once on a broker outage. Gate liveness on
  `/health` if you set it.
- `MESSAGING_PUBLISHTIMEOUT`: an aggregate bound per publish, at least 35s with
  the framework's reconnect defaults.
- `MESSAGING_DECLARE_EXTERNALWAIT` and `CUSTOM_PARTNERFEED_ENABLED`: only where a
  partner service owns `partner-events`.
- `messaging.seal.active`: only during a sealing key rotation. The selector for
  family `payments.sign` is `MESSAGING_SEAL_ACTIVE_PAYMENTS_SIGN=v2`.
- `OBSERVABILITY_ENABLED=true` with `OBSERVABILITY_SERVICE_NAME`,
  `OBSERVABILITY_TRACE_ENDPOINT` / `_PROTOCOL` / `_INSECURE` and
  `OBSERVABILITY_METRICS_ENDPOINT` / `_PROTOCOL` / `_INSECURE` to export telemetry
  over OTLP.

## Streams over TLS

`MESSAGING_STREAMS_URI` can use `rabbitmq-stream+tls://`, usually on port 5551:
`rabbitmq-stream+tls://user:pass@broker:5551/%2f`. Such a URI failed startup
before go-bricks v0.73.0 and connects since (C73.11). The client verifies the
broker certificate against the process trust store: the system roots, which on
Linux honor `SSL_CERT_FILE` and `SSL_CERT_DIR` or the image's CA bundle.
go-bricks has no setting for a CA file or a server name.

The certificate must name every host the client dials: the URI host and each
leader or replica host the broker advertises, or only
`MESSAGING_STREAMS_ADDRESSRESOLVER_HOST` when it is set, because the resolver
then replaces the advertised hosts. Otherwise startup fails with an x509 error.
Development stays on plaintext 5552.

## Keep-alive idle timeout

`SERVER_TIMEOUT_IDLE` (`server.timeout.idle`) is how long the app keeps an idle
keep-alive connection open. Unset, it is 75s; it was 60s before go-bricks
v0.73.0. `0` also means 75s, and a negative value fails startup
([ADR-154](https://github.com/gaborage/go-bricks/blob/4cacc99c4fad32a12f75e6b06c0ae66aba6c232d/wiki/adr_154_server_idle_timeout_zero_takes_default.md)).
The probe listener uses the same value.

The side that closes an idle connection first should be the one that does not
send on it:

- **Behind an L7 proxy** (an AWS ALB, ingress-nginx), the proxy's upstream idle
  must be shorter than the app's, or the proxy can send a request down a
  connection the app is closing and answer 502. Both proxies default to 60s,
  below the new default. If the proxy's idle is 75s or more, set
  `SERVER_TIMEOUT_IDLE` above it.
- **Behind an L4 load balancer** (an AWS NLB holds a flow 350s), the app's idle
  must be shorter than the balancer's, so the balancer never drops a flow the
  app still holds.

## Keys a deploy must not set

go-bricks v0.73.0 refuses four messaging keys that no framework code ever read
([ADR-153](https://github.com/gaborage/go-bricks/blob/4cacc99c4fad32a12f75e6b06c0ae66aba6c232d/wiki/adr_153_retired_messaging_keys_refused.md)). Each one fails
startup with `config_invalid` when it is delivered, an empty value or a YAML null
included. The demo sets none of them.

| Variable | Instead |
|----------|---------|
| `MESSAGING_BROKER_VIRTUALHOST` | The vhost has always been the path of `MESSAGING_BROKER_URL`. Put a vhost other than `/` there, percent-encoded: `amqps://host:5671/orders`, or `amqps://host:5671/%2Forders` for `/orders`. That changes the vhost the service connects to, so check that it exists and that the user has permissions on it first. |
| `MESSAGING_ROUTING_EXCHANGE`, `MESSAGING_ROUTING_KEY` | Nothing. Each publisher sets its exchange and routing key in code. |
| `MESSAGING_HEADERS*` | Nothing. Each publisher sets its headers in code. |

A config file rendered for a deploy must spell every key in lowercase, once. A
root section such as `Server:` and two keys of one map that differ only by case
both fail startup now, where decode used to keep one silently
([ADR-157](https://github.com/gaborage/go-bricks/blob/4cacc99c4fad32a12f75e6b06c0ae66aba6c232d/wiki/adr_157_config_key_spelling_refused.md)). A variable reaches the
lowercase spelling, so a mixed-case YAML key beside one is such a pair too.

## Before the first start

The demo owns its outbox and inbox DDL (`autocreatetable: false`), so apply the
Flyway migrations in [migrations/](../migrations/) to the default database first.
Otherwise startup fails with `TableUnusableError`. The analytics database needs
its own schema as well (`make migrate-analytics` does that locally).

A base-only start with none of the variables above fails at config validation with
`database.host required`.
