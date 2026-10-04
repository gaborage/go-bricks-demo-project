# Running outside development

`make run` boots with `APP_ENV=development` and reads two files from the working
directory. Any other environment (`APP_ENV=staging`, `production`, ...) reads only
the base and takes everything else from environment variables. This page lists
what the base already provides and what a deploy must supply.

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

The keystore entries the modules resolve at startup, both halves each:

| `<NAME>` | Used by |
|----------|---------|
| `WEBHOOKSIGNING` | webhooks module: sign and verify |
| `TOKENSOUR` | tokens module: this service's JOSE keypair |
| `TOKENSPEER` | tokens module: the in-process peer simulators' keypair |
| `PAYMENTSSIGN-V1` | payments module: sealing sign family, generation v1 |
| `PAYMENTSENCRYPT-V1` | payments module: sealing encrypt family, generation v1 |

The variable name is the config path upper-cased with `.` turned into `_`, so
an entry name carries no separator: an `_` would read back as a path separator,
and a POSIX shell `export` refuses a `-`. The two sealing entries keep their
`-v<N>` suffix anyway, because go-bricks treats only a name ending in
`-v<digits>` as a sealing generation. Their variables, for example
`KEYSTORE_KEYS_PAYMENTSSIGN-V1_PRIVATE_VALUE`, work through Docker `-e` /
`--env-file`, Kubernetes `env` and `env(1)`, but not through a shell `export`.
To mount DER files instead, set `..._FILE` to the mounted path rather than
`..._VALUE`, never both for one half.

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
- `messaging.seal.active`: only during a sealing key rotation.
- `OBSERVABILITY_ENABLED=true` with `OBSERVABILITY_SERVICE_NAME`,
  `OBSERVABILITY_TRACE_ENDPOINT` / `_PROTOCOL` / `_INSECURE` and
  `OBSERVABILITY_METRICS_ENDPOINT` / `_PROTOCOL` / `_INSECURE` to export telemetry
  over OTLP.

## Before the first start

The demo owns its outbox and inbox DDL (`autocreatetable: false`), so apply the
Flyway migrations in [migrations/](../migrations/) to the default database first.
Otherwise startup fails with `TableUnusableError`. The analytics database needs
its own schema as well (`make migrate-analytics` does that locally).

A base-only start with none of the variables above fails at config validation with
`database.host required`.
