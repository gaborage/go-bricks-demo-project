# Go-Bricks Demo Project

Production-ready demonstration of the [go-bricks framework](../go-bricks) showcasing modular architecture, REST APIs, observability, and performance testing.

## Features

- **Modular Architecture** - Domain-driven design with clean separation of concerns
- **REST API** - Full CRUD operations with Echo web framework
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

# Build and run application
make run

# Test the API
curl http://localhost:8080/health
curl "http://localhost:8080/api/v1/products?page=1&pageSize=10"
```

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

### System
- `GET /health` - Liveness probe
- `GET /ready` - Readiness probe (checks DB + messaging)
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
```

## Development

### Essential Commands
```bash
make dev            # docker-up + migrate
make build          # Build binary
make run            # Build + run
make check          # fmt + lint + test (pre-commit)
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

**DEBUG env conflict:** `unset DEBUG && make run`

**Port conflicts:** `make docker-down && make docker-up`

**Connection pool exhausted:** Increase `database.pool.max.connections` in [config.development.yaml](config.development.yaml)

**Observability not working:** Check OTel Collector: `docker-compose ps | grep otel-collector`

## Documentation

- **[CLAUDE.md](CLAUDE.md)** - Complete developer guide
- **[wiki/LOAD_TESTING.md](wiki/LOAD_TESTING.md)** - Load testing guide
- **[wiki/PROMETHEUS_GRAFANA_SETUP.md](wiki/PROMETHEUS_GRAFANA_SETUP.md)** - Observability setup
- **[etc/docker/README.md](etc/docker/README.md)** - Docker infrastructure

## Project Structure

```
go-bricks-demo-project/
├── cmd/api/main.go              # Entry point
├── internal/modules/
│   ├── products/                # Products CRUD module
│   ├── analytics/               # Analytics module (named database example)
│   ├── legacy/                  # Legacy module (WithRawResponse example)
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

**Built with [go-bricks](../go-bricks)** - Production-ready modular framework for Go.
