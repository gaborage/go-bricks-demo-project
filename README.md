# Go-Bricks Demo Project

[![CI](https://github.com/gaborage/go-bricks-demo-project/actions/workflows/ci.yml/badge.svg)](https://github.com/gaborage/go-bricks-demo-project/actions/workflows/ci.yml)

Production-ready demonstration of the [go-bricks framework](https://github.com/gaborage/go-bricks) showcasing modular architecture, REST APIs, observability, and performance testing.

## Features

- **Modular Architecture** - Domain-driven design with clean separation of concerns
- **REST API** - Full CRUD operations with Echo web framework
- **Transactional Outbox** - Reliable event publishing via dual-write pattern
- **KeyStore** - Named RSA key pair management for signing/verification
- **JOSE Middleware** - Nested JWE-of-JWS protection on HTTP bodies (VTS-style integrations) + outbound `JOSETransport` for partner calls
- **RabbitMQ Streams** - Partitioned super stream on the native stream protocol, projected by a typed consumer
- **Dual Observability** - Prometheus/Grafana/Tempo/Loki (local) + New Relic (cloud)
- **Load Testing** - Comprehensive k6 test suite
- **Multi-tenant Ready** - Framework supports multi-tenancy (currently disabled)
- **Raw Response Mode** - `WithRawResponse()` for Strangler Fig migration patterns
- **Production Patterns** - Health checks, structured logging, connection pooling

## Quick Start

```bash
# Start infrastructure (PostgreSQL, RabbitMQ, observability)
make docker-up

# Run database migrations
make migrate

# Generate RSA keys for KeyStore demo (first time only)
make generate-keys

# Build and run application
make run

# Test the API
curl http://localhost:8080/api/v1/health
curl "http://localhost:8080/api/v1/products?page=1&pageSize=10"
```

> **Upgrading an existing checkout?** The streams demo needs a RabbitMQ container
> with the `rabbitmq_stream` plugin enabled and port 5552 published, so the broker's
> compose definition changed. `docker-compose restart rabbitmq` will **not** pick
> that up — the container has to be recreated:
>
> ```bash
> make docker-up          # recreates whatever the compose file changed
> ```
>
> Run it from the repository root. Invoking `docker-compose` from `etc/docker`
> instead fails before it starts anything: the compose file's New Relic service
> declares `NEW_RELIC_LICENSE_KEY` as required, and interpolation covers the whole
> file even when you name a single service, so it needs the root `.env` that the
> Makefile passes with `--env-file`. Without the recreated container, startup
> cannot reach the stream endpoint on 5552.

## API Endpoints

### Products
- `GET /api/v1/products` - List products (paginated)
- `GET /api/v1/products/:id` - Get product by ID
- `POST /api/v1/products` - Create product
- `PUT /api/v1/products/:id` - Update product
- `DELETE /api/v1/products/:id` - Delete product

### Analytics (Named Database Example)
- `POST /api/v1/analytics/views` - Record a product view
- `GET /api/v1/analytics/views` - Get top viewed products
- `GET /api/v1/analytics/views/:productId` - Get view stats for product

### Legacy (Raw Response Example)
- `GET /api/v1/legacy/products` - List products (no APIResponse envelope)
- `GET /api/v1/legacy/products/:id` - Get product by ID (no APIResponse envelope)

### Webhooks (KeyStore Signing Example)
- `POST /api/v1/webhooks/sign` - Sign a JSON payload with RSA key
- `POST /api/v1/webhooks/verify` - Verify a payload's RSA signature

### Tokens (JOSE Middleware Example)
A Visa Token Services–style integration showing nested JWE-of-JWS protection on
both inbound and outbound HTTP bodies, plus the framework's outbound
`JOSETransport` against an in-process peer simulator.

- `POST /api/v1/tokens` — JOSE-protected partner endpoint. Decrypts + verifies
  the request, returns a signed-and-encrypted token. Requires
  `Content-Type: application/jose`.
- `POST /api/v1/tokens/relay` — plaintext entry that drives the outbound JOSE
  path. Wraps the request via `httpclient.WithJOSE(...)` and POSTs to the
  in-process peer simulator.
- `POST /api/v1/__sim/peer/tokens` — peer simulator with the inverse JOSE
  policy. Demo-only; real integrations point at a counterparty URL instead.

#### Walkthrough

```bash
# 1. Generate keypairs and patch the inline base64 into config.development.yaml.
make generate-keys

# 2. Start the app.
make run

# 3. Seal a payload as the peer would and POST it to /tokens.
echo '{"pan":"4111111111111111"}' | go run ./cmd/seal-payload | \
  curl -s -X POST http://localhost:8080/api/v1/tokens \
       -H 'Content-Type: application/jose' --data-binary @-
# Response is a compact JWE — decode it back to plaintext via seal-payload's
# inverse logic, or hit the relay endpoint instead which unwraps for you.

# 4. Drive the outbound JOSETransport via the relay endpoint.
curl -s -X POST http://localhost:8080/api/v1/tokens/relay \
     -H 'Content-Type: application/json' \
     -d '{"pan":"4111111111111111"}'
# {"data":{"token":{"token":"tok_...","masked_pan":"************1111", ...}}}
```

The keystore exercises both source styles for a single keypair: `tokens-our` is
file-backed (production pattern for Kubernetes secret mounts), and
`tokens-peer` mixes file-backed private with inline base64 public (the
`value:` style typical of secret-manager projection).

### Payments (Sealed AMQP Messages Example)
A `seal:`-tagged event type: the framework encrypts the one declared Subject
(the card) and signs the whole document on publish, then verifies and decrypts
on consume — no crypto at any call site. The module is both producer and
consumer, so the demo is self-contained.

- `POST /api/v1/payments/authorize` — publishes a sealed `payment.authorized`
  event and answers `202 Accepted`. The response carries `cardLast4` only; the
  PAN never comes back out of the API and never appears in the clear on the
  broker.

#### Walkthrough

```bash
# 1. Keypairs, migrations (V4 creates the inbox ledger), and the app.
make generate-keys
make migrate
make run

# 2. Authorize a payment.
#    DEMO DATA ONLY — 4111111111111111 is the published Visa test PAN.
curl -s -X POST http://localhost:8080/api/v1/payments/authorize \
     -H 'Content-Type: application/json' \
     -d '{"amount":4599,"currency":"USD","card":{"pan":"4111111111111111","expMonth":12,"expYear":2030,"holder":"ADA LOVELACE"}}'
# {"data":{"orderId":"...","status":"authorized","amount":4599,"currency":"USD","cardLast4":"1111"},"meta":{...}}

# 3. Read the published message off the broker and see what an operator with
#    full queue access actually gets: a compact JWS whose `card` member is a
#    JWE, with the PAN nowhere on the wire.
make show-sealed-message
```

### Activity (RabbitMQ Super-Stream Example)
The **native stream protocol** (port 5552, `rabbitmq_stream` plugin) rather than the
AMQP lane on 5672. `product-activity` is a super stream of 3 partitions — the broker
materializes `product-activity-0` … `product-activity-2` — published to with the
product ID as the routing key and projected back by a typed consumer.

- `GET /api/v1/products/activity` — the projection: per-product event counts,
  per-partition delivery counts, and a ring of the last 50 events with the
  partition each arrived on.
- `POST /api/v1/__sim/streams/poison` — publishes malformed bytes through the same
  publisher handle. Demo-only; it exists to show the consumer *skipping* poison.

#### Walkthrough

```bash
# 1. Create two products. Each successful write publishes a ProductActivity onto
#    product-activity, keyed by the product id.
WIDGET=$(curl -s -X POST http://localhost:8080/api/v1/products \
  -H 'Content-Type: application/json' \
  -d '{"name":"Widget","description":"A widget","price":19.99}' | jq -r '.data.id')

GADGET=$(curl -s -X POST http://localhost:8080/api/v1/products \
  -H 'Content-Type: application/json' \
  -d '{"name":"Gadget","description":"A gadget","price":49.50}' | jq -r '.data.id')

# 2. Update, then delete, the widget — three events sharing one routing key.
curl -s -X PUT "http://localhost:8080/api/v1/products/$WIDGET" \
  -H 'Content-Type: application/json' -d '{"price":24.99}' > /dev/null
curl -s -X DELETE "http://localhost:8080/api/v1/products/$WIDGET" > /dev/null

# 3. Read the projection.
curl -s http://localhost:8080/api/v1/products/activity | jq '.data'
```

```jsonc
{
  "superStream": "product-activity",
  "partitions": 3,                                   // declared partition count
  "consumerName": "product-activity-projector",      // the offset-tracking key
  "delivered": 4,
  "productCounts": {                                 // product id -> action -> count
    "<WIDGET>": { "created": 1, "updated": 1, "deleted": 1 },
    "<GADGET>": { "created": 1 }
  },
  "partitionCounts": {                               // partition -> deliveries
    "product-activity-1": 3,
    "product-activity-2": 1
  },
  "recent": [                                        // newest FIRST, capped at 50
    { "productId": "<WIDGET>", "action": "deleted", "name": "", "price": 0,
      "occurredAt": "2026-01-31T18:04:12.881Z",
      "partition": "product-activity-1", "offset": 2 },
    { "productId": "<WIDGET>", "action": "updated", "name": "Widget", "price": 24.99,
      "occurredAt": "2026-01-31T18:04:12.744Z",
      "partition": "product-activity-1", "offset": 1 },
    { "productId": "<GADGET>", "action": "created", "name": "Gadget", "price": 49.5,
      "occurredAt": "2026-01-31T18:04:12.602Z",
      "partition": "product-activity-2", "offset": 0 },
    { "productId": "<WIDGET>", "action": "created", "name": "Widget", "price": 19.99,
      "occurredAt": "2026-01-31T18:04:12.470Z",
      "partition": "product-activity-1", "offset": 0 }
  ]
}
```

`?limit=N` (1–50) trims `recent`. The delete carries an empty `name` and a zero
`price` on purpose: the row is gone by the time the event is minted, so the
projection tallies a delete by product id alone. Which `product-activity-N` a given
id lands on is decided by the hash, so expect different partition names than the
ones above.

**Partition stickiness is the thing to notice:** all three widget events land on the
same `product-activity-N`, because the client hashes the routing key with murmur3
under RabbitMQ's shared seed and takes the remainder over the partition list. That is
the cross-client default, so the Java, .NET and Python clients would place the same
key on the same partition. Order is guaranteed *within* a partition, not across the
super stream — which is exactly why a per-product key is the right key here.

`offset` counts **within** its partition, so the numbers are only comparable between
events sharing a `partition`. Gaps in that sequence (0, 1, 2, 3, 5, 6 …) are normal:
the broker writes its own offset-tracking chunk entries into the stream, so a missing
number is bookkeeping, not a lost message.

```bash
# 4. Publish deliberately malformed bytes. productId is the routing key, so this
#    lands on the SAME partition the Gadget's events use. (Omit it and the
#    simulator falls back to the "poison-demo" key, which hashes wherever it
#    hashes — pass one when you want to choose the partition.)
curl -s -X POST http://localhost:8080/api/v1/__sim/streams/poison \
  -H 'Content-Type: application/json' \
  -d "{\"productId\":\"$GADGET\"}"

# 5. Write to that same product again — same key, same partition, right behind the
#    poison — then re-read the projection.
curl -s -X PUT "http://localhost:8080/api/v1/products/$GADGET" \
  -H 'Content-Type: application/json' -d '{"price":44.50}' > /dev/null

curl -s http://localhost:8080/api/v1/products/activity | jq '.data.partitionCounts'
```

The poisoned partition **keeps delivering** — the Gadget's `updated` event is queued
behind the malformed body on that same partition, and it still lands in the
projection. A body that fails to decode or fails validation is deterministic poison:
it would fail identically on every retry and every replica, so the framework returns
it `Permanent`, never parks it, skips its offset and logs it. Nothing durable records
it, which is the deliberate trade (framework ADR-092). Watch the app log for the
skip line.

Restarting the app does **not** rebuild this projection. Offsets are stored
server-side per consumer name, and a stored offset always wins over the declared
`OffsetFirst()` start, so the consumer resumes just past what it already handled and
the in-memory projection stays empty until new writes arrive. The idempotency guard
in the projection is there for the narrower case — a crash that loses uncommitted
offsets, or a consumer re-promotion re-attaching a partition at its last stored
offset. `countbeforestorage` is lowered to 10 in
[config.development.yaml](config.development.yaml) so the count-driven commit is
reachable at demo volume; the 5s flush interval would commit either way.

### System
- `GET /api/v1/health` - Liveness probe
- `GET /api/v1/ready` - Readiness probe (checks DB + messaging)
- `GET /debug/*` - Debug endpoints (goroutines, gc, info)

## Observability

### Local Stack (Recommended)
```bash
make docker-up-local
# Or manually:
cd etc/docker
docker-compose --profile local up -d
```
- **Prometheus:** http://localhost:9090
- **Grafana:** http://localhost:3000 (admin/admin)
- **Tempo:** http://localhost:3200

### New Relic Stack
```bash
# Create .env with NEW_RELIC_LICENSE_KEY and NEW_RELIC_REGION
make docker-up-newrelic
# Or manually:
cd etc/docker
docker-compose --profile newrelic up -d
```
- **New Relic One:** https://one.newrelic.com/nr1-core

**Switch stacks:** Just run `docker-compose down` and start the other profile. Application auto-connects to `localhost:4317`.

See [wiki/PROMETHEUS_GRAFANA_SETUP.md](wiki/PROMETHEUS_GRAFANA_SETUP.md) for details.

## Testing

### Unit Tests
```bash
make test                                    # All tests with race detector
go test ./internal/modules/products/...     # Specific module
make coverage                                # HTML coverage report
```

### Load Testing
```bash
make loadtest-install    # Install k6
make loadtest-smoke      # Quick validation (30s)
make loadtest-crud       # Realistic mix (~15 min)
make loadtest-ramp       # Find breaking points (~17 min)
make loadtest-spike      # Test resilience (~6 min)
```

See [wiki/LOAD_TESTING.md](wiki/LOAD_TESTING.md) for detailed guide and performance tuning.

## Configuration

Key files:
- **[config.development.yaml](config.development.yaml)** - All configuration options with examples
- **[etc/docker/docker-compose.yml](etc/docker/docker-compose.yml)** - Infrastructure services
- **`.env`** - Secrets (gitignored, see `.env.example`)

Common settings:
```yaml
database.pool.max.connections: 25    # Increase for high load
app.rate.limit: 100                  # Requests per second
observability.enabled: true          # Enable telemetry
multitenant.enabled: false           # Multi-tenant mode (disabled)

messaging.streams.uri: rabbitmq-stream://guest:guest@localhost:5552/%2f
messaging.streams.addressresolver.host: localhost      # Required under Docker port mapping
messaging.streams.addressresolver.port: 5552
messaging.streams.offsetstore.countbeforestorage: 10   # Framework default 500
```

## Development

### Essential Commands
```bash
make dev            # docker-up + migrate
make build          # Build binary
make run            # Build + run
make check          # fmt + lint + test (pre-commit)

make show-sealed-message   # Publish a sealed payment, dump the raw broker body
```

### Adding a Module

1. Create structure: `mkdir -p internal/modules/mymodule/{domain,repository,service,http}`
2. Implement `app.Module` interface in `module.go`
3. Register in [cmd/api/main.go](cmd/api/main.go)

See [products module](internal/modules/products/) for reference.

## Multi-Tenant Support

**Status:** Disabled (`multitenant.enabled: false`)

Framework supports:
- Header/subdomain/composite tenant resolution
- Per-tenant database connections
- AWS Secrets Manager integration (see [internal/modules/shared/secrets/](internal/modules/shared/secrets/))
- LRU connection management

To enable: Set `multitenant.enabled: true` in config and configure tenant resolver.

## Named Databases

The go-bricks framework supports **multiple independent database connections**, each identified by a unique name. This enables data-layered architectures where different concerns (products, analytics, audit logs) have isolated storage.

### How It Works

- **Default database**: `deps.DB(ctx)` - configured under `database:`
- **Named databases**: `deps.DBByName(ctx, "name")` - configured under `databases.{name}:`

### Configuration

```yaml
# Default database (products module)
database:
  type: postgresql
  host: localhost
  port: 5432
  database: postgres

# Named databases
databases:
  analytics:                    # Database name identifier
    type: postgresql
    host: localhost
    port: 5433                  # Separate PostgreSQL instance
    database: analytics
    username: postgres
    password: postgres
    pool:
      max:
        connections: 20         # Smaller pool for analytics workload
```

### Example: Analytics Module

The [analytics module](internal/modules/analytics/) demonstrates this pattern:

```go
// In module.Init() - create a closure for the named database
m.getAnalyticsDB = func(ctx context.Context) (database.Interface, error) {
    return deps.DBByName(ctx, "analytics")
}

// Pass to repository
m.repo = repository.NewAnalyticsRepository(m.getAnalyticsDB)
```

### Infrastructure

The project includes two PostgreSQL instances:
- `postgres` (port 5432) - Main database for products
- `postgres-analytics` (port 5433) - Analytics database

Each has independent migrations in `migrations/` and `migrations-analytics/`.

### Testing Analytics API

```bash
# Record a product view
curl -X POST http://localhost:8080/api/v1/analytics/views \
  -H "Content-Type: application/json" \
  -d '{"productId":"test-id","userAgent":"curl","ipAddress":"127.0.0.1"}'

# Get top viewed products
curl "http://localhost:8080/api/v1/analytics/views?limit=5"

# Get stats for specific product
curl http://localhost:8080/api/v1/analytics/views/test-id
```

## Troubleshooting

**DEBUG env conflict** (go-bricks < v0.43.0 only; resolved by #601): `unset DEBUG && make run`

**Port conflicts:** `make docker-down && make docker-up`

**Streams demo can't reach the broker:** the `rabbitmq_stream` plugin and port 5552 arrived with a compose change. Recreate the container with `make docker-up` from the repository root — a `restart` does not apply it, and a bare `docker-compose` from `etc/docker` fails on the New Relic service's required `NEW_RELIC_LICENSE_KEY` because it has no `--env-file`.

**`messaging.streams.uri is set but the streams lane is not linked`:** the lane is opt-in at the build graph. Something must import `github.com/gaborage/go-bricks/messaging/streams`; [internal/modules/activity/](internal/modules/activity/) imports it by name to declare its topology, and that import is what links the lane (a blank `_` import would do for a process declaring none).

**Connection pool exhausted:** Increase `database.pool.max.connections` in [config.development.yaml](config.development.yaml)

**Observability not working:** Check OTel Collector: `docker-compose ps | grep otel-collector`

## Documentation

- **[CLAUDE.md](CLAUDE.md)** - Complete developer guide
- **[FLYWAY_MIGRATIONS.md](FLYWAY_MIGRATIONS.md)** - Single-tenant Flyway walkthrough (Postgres + Oracle)
- **[wiki/MULTI_TENANT_MIGRATION_DEMO.md](wiki/MULTI_TENANT_MIGRATION_DEMO.md)** - Schema-per-tenant migrations via `go-bricks-migrate`
- **[wiki/LOAD_TESTING.md](wiki/LOAD_TESTING.md)** - Load testing guide
- **[wiki/PROMETHEUS_GRAFANA_SETUP.md](wiki/PROMETHEUS_GRAFANA_SETUP.md)** - Observability setup
- **[etc/docker/README.md](etc/docker/README.md)** - Docker infrastructure

## Project Structure

```
go-bricks-demo-project/
├── cmd/api/main.go              # Entry point
├── internal/modules/
│   ├── products/                # Products CRUD module (+ transactional outbox events)
│   ├── analytics/               # Analytics module (named database example)
│   ├── legacy/                  # Legacy module (WithRawResponse example)
│   ├── webhooks/                # Webhooks module (KeyStore signing example)
│   ├── tokens/                  # Tokens module (JOSE middleware: nested JWE-of-JWS + outbound relay)
│   ├── payments/                # Payments module (sealed AMQP messages: signed document, encrypted Subject)
│   ├── activity/                # Activity module (RabbitMQ super-stream: partitioned publish + typed projection)
│   └── shared/secrets/          # Multi-tenant AWS integration
├── migrations/                  # Flyway migrations (default database)
├── migrations-analytics/        # Flyway migrations (analytics database)
├── loadtests/                   # k6 load tests
├── etc/docker/                  # Docker Compose + configs
├── config.development.yaml      # Configuration
└── Makefile                     # Development commands
```

## License

MIT License - see [LICENSE](LICENSE) file for details.

This project is fully open source and free to use, modify, and distribute.

---

**Built with [go-bricks](https://github.com/gaborage/go-bricks)** - Production-ready modular framework for Go.
