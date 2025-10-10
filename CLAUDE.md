# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is a **go-bricks demo project** demonstrating production-ready patterns for building modular Go applications. It uses the `go-bricks` framework (located at `../go-bricks`) with local replacement via `go.mod`.

**Key characteristics:**
- Framework-based modular architecture
- Multi-tenant capable (currently running in single-tenant mode)
- PostgreSQL + RabbitMQ infrastructure
- REST API with Echo web framework
- Dual observability stacks: Prometheus/Grafana/Jaeger (local) + DataDog (cloud)
- Comprehensive load testing with k6

**Requirements:**
- Go 1.25.1+
- Docker & Docker Compose
- Make

## Quick Start

```bash
# 1. Start infrastructure services
make docker-up

# 2. Run database migrations
make migrate

# 3. Build and run application
make run

# 4. Test the API
curl http://localhost:8080/health
curl http://localhost:8080/api/v1/products
```

## Essential Commands

### Development Workflow
```bash
make build          # Build application binary to bin/go-bricks-demo-project
make run            # Build + run (requires services to be running)
make test           # Run all tests with race detector
make check          # Run fmt + lint + test (pre-commit checks)
make dev            # Start docker-up + migrate (full dev environment setup)
```

### Docker Infrastructure
```bash
make docker-up      # Start PostgreSQL + RabbitMQ + observability stack
make docker-down    # Stop all services and remove volumes
make status         # Show running service status
make logs           # Follow logs from all services
```

**Note:** All docker-compose files are located in `etc/docker/` directory, but Makefile handles the path for you.

### Database Migrations
```bash
make migrate        # Run Flyway migrations (uses --profile migrations)
make migrate-info   # Show migration status
```

### Code Quality
```bash
make fmt            # Format code with gofmt
make lint           # Run golangci-lint
make coverage       # Generate HTML coverage report
```

### Load Testing
```bash
make loadtest-install    # Install k6 load testing tool
make loadtest-smoke      # Quick validation (30 seconds)
make loadtest-crud       # Realistic CRUD mix test (~15 min)
make loadtest-read       # Read-only baseline test (~12 min)
make loadtest-ramp       # Find breaking points (~17 min)
make loadtest-spike      # Test resilience under traffic spikes (~6 min)
make loadtest-sustained  # Detect memory/connection leaks (~17 min)
make loadtest-all        # Run all tests sequentially (~60 min)
```

See [wiki/LOAD_TESTING.md](wiki/LOAD_TESTING.md) for detailed load testing guide.

## Architecture

### Application Bootstrap

The application uses `go-bricks/app.New()` which handles:
1. **Configuration loading** - Environment-based config from `config.yaml` (see Config System section)
2. **Database manager** - Connection pooling and lifecycle management
3. **Messaging manager** - RabbitMQ client setup
4. **Observability provider** - OpenTelemetry setup (see Observability section)
5. **HTTP server** - Echo server with middleware

**Entry point:** [cmd/api/main.go](cmd/api/main.go)
- Calls `app.New()` to bootstrap framework
- Registers modules via `getModulesToLoad()`
- Starts server with `application.Run()`

### Module System

Modules must implement `app.Module` interface:

```go
type Module interface {
    Name() string
    Init(*app.ModuleDeps) error
    RegisterRoutes(*server.HandlerRegistry, server.RouteRegistrar)
    DeclareMessaging(*messaging.Declarations)
    Shutdown() error
}
```

**Module structure pattern** (see [internal/modules/products/](internal/modules/products/)):
```
products/
├── module.go           # Module implementation, wires dependencies
├── domain/             # Domain models (Product)
├── repository/         # Data access layer (ProductRepository)
├── service/            # Business logic (ProductService)
└── http/               # HTTP handlers (ProductHandler)
```

**Dependency injection flow:**
1. Framework calls `module.Init(deps *app.ModuleDeps)`
2. Module receives `deps.GetDB` and `deps.GetMessaging` (context-aware functions)
3. Module creates repository → service → handler chain
4. Module registers HTTP routes in `RegisterRoutes()`

### Configuration System

**go-bricks config** uses `koanf` for YAML loading with two loading methods:

1. **`Unmarshal(key, &struct)`** - For nested structs with `mapstructure:` tags
2. **`InjectInto(&struct)`** - For flat structs with `config:` tags (only supports primitives)

**Environment-based config:**
- `APP_ENV=development` loads `config.yaml` + `config.development.yaml`
- Can be overridden by `config.{env}.yaml`
- Environment variables override YAML (e.g., `APP_NAME` overrides `app.name`)

**IMPORTANT:** The `DEBUG` environment variable conflicts with go-bricks' `debug` config section. Unset it before running:
```bash
unset DEBUG && make run
```

### Database Access Pattern

Modules receive context-aware database access via `deps.GetDB`:

```go
func (m *Module) Init(deps *app.ModuleDeps) error {
    m.getDB = deps.GetDB  // Store function, don't call yet
    m.repo = repository.NewSQLProductRepository(m.getDB)
    // ...
}
```

**In handlers/services:**
```go
func (h *Handler) GetProduct(ctx context.Context, id string) (*Product, error) {
    db, err := h.getDB(ctx)  // Get DB for this request's context
    if err != nil {
        return nil, err
    }
    return h.repo.FindByID(ctx, db, id)
}
```

**Why context-aware?** Enables multi-tenant mode where `ctx` determines which database connection to use.

### Multi-Tenant Support

**Current mode:** Single-tenant (see `config.yaml: multitenant.enabled: false`)

**Multi-tenant mode** (can be enabled):
- Tenant ID resolved from HTTP header (`X-Tenant-ID`)
- Each tenant gets isolated database connection
- `deps.GetDB(ctx)` returns tenant-specific DB based on context
- See [internal/modules/shared/secrets/](internal/modules/shared/secrets/) for AWS Secrets Manager tenant config loading

## Observability & Monitoring

The project supports **two observability stacks** that can be switched using Docker Compose profiles:

### Local Stack (Prometheus + Grafana + Jaeger)

**Best for:** Local development with immediate feedback (< 30 seconds vs. 10-15 min cloud delay)

**Start:**
```bash
cd etc/docker
docker-compose --profile local up -d
```

**Access:**
- Prometheus: http://localhost:9090
- Grafana: http://localhost:3000 (admin/admin)
- Jaeger: http://localhost:16686

**Features:**
- Metrics scraped from OTel Collector on port 8889
- Distributed tracing with Jaeger
- Auto-provisioned Grafana datasources
- No cloud dependency (work offline)

### Cloud Stack (DataDog)

**Best for:** Production-like monitoring and APM

**Setup:**
1. Get DataDog API key from https://app.datadoghq.com/organization-settings/api-keys
2. Create `.env` file in project root:
   ```bash
   DD_API_KEY=your_api_key_here
   DD_SITE=us5.datadoghq.com
   ```
3. Start stack:
   ```bash
   cd etc/docker
   docker-compose --profile datadog up -d
   ```

**Access:**
- DataDog APM: https://us5.datadoghq.com/apm
- Service name: `go-bricks-demo-project`

### Switching Observability Stacks

```bash
# Stop current stack
cd etc/docker && docker-compose down

# Start desired stack
docker-compose --profile local up -d      # For Prometheus/Grafana
docker-compose --profile datadog up -d    # For DataDog
```

**Note:** Application doesn't need restart when switching - it always sends to `localhost:4317`.

### Available Metrics

```promql
# HTTP server metrics (namespace: gobricks_)
gobricks_http_server_request_duration_seconds_bucket
gobricks_http_server_request_body_size_bytes_bucket
gobricks_http_server_response_body_size_bytes_bucket

# Example queries:
rate(gobricks_http_server_request_duration_seconds_count[5m])  # RPS
histogram_quantile(0.95, rate(...[5m]))                        # p95 latency
```

See [wiki/PROMETHEUS_GRAFANA_SETUP.md](wiki/PROMETHEUS_GRAFANA_SETUP.md) for complete observability guide.

## Testing

### Unit Tests
```bash
go test ./internal/modules/products/...          # Test specific module
go test -v -race ./...                           # All tests with race detector
go test -run TestProductService_Create ./...     # Run specific test
make test                                        # Run all tests (uses race detector)
```

### API Testing
```bash
make test-products-api     # Uses scripts/test-products-api.sh
```

**Manual API testing:**
```bash
# Ensure services are running
make docker-up

# Start app
make run

# Test endpoints
curl http://localhost:8080/health
curl http://localhost:8080/api/v1/products
```

### Load Testing

The project includes comprehensive k6 load testing scripts. See [wiki/LOAD_TESTING.md](wiki/LOAD_TESTING.md) for details.

**Quick start:**
```bash
# Install k6
make loadtest-install

# Run quick smoke test
make loadtest-smoke

# Run realistic CRUD test
make loadtest-crud
```

**Available tests:**
- **CRUD Mix** - Realistic production traffic (50% reads, 25% gets, 15% creates, 7% updates, 3% deletes)
- **Read-Only** - Baseline read performance
- **Ramp-Up** - Find breaking points by gradually increasing load
- **Spike** - Validate resilience under sudden traffic spikes
- **Sustained** - Detect memory/connection leaks over 15 minutes

**Performance tuning:**
- Database pool: `config.development.yaml` → `database.pool.max.connections`
- Rate limiting: `config.development.yaml` → `app.rate.limit/burst`
- Slow query detection: `database.query.slow.threshold`

## Adding New Modules

1. **Create module directory structure:**
   ```bash
   mkdir -p internal/modules/mymodule/{domain,repository,service,http}
   ```

2. **Implement `app.Module` interface** in `module.go`:
   ```go
   type Module struct {
       deps *app.ModuleDeps
       // ... your fields
   }

   func (m *Module) Init(deps *app.ModuleDeps) error {
       m.deps = deps
       // Wire up repository → service → handler
       return nil
   }

   func (m *Module) RegisterRoutes(hr *server.HandlerRegistry, r server.RouteRegistrar) {
       // Register HTTP routes
   }
   ```

3. **Register in [cmd/api/main.go](cmd/api/main.go):**
   ```go
   func getModulesToLoad() []ModuleConfig {
       return []ModuleConfig{
           {Name: "products", Enabled: true, Module: products.NewModule()},
           {Name: "mymodule", Enabled: true, Module: mymodule.NewModule()},
       }
   }
   ```

## Framework Dependency

**go-bricks location:** `../go-bricks` (local replacement)

When modifying go-bricks:
```bash
cd ../go-bricks
# Make changes
cd ../go-bricks-demo-project
make build  # Automatically picks up local changes
```

**go-bricks provides:**
- `app` - Application bootstrap and module system
- `config` - Configuration loading with koanf
- `database` - Multi-database support (PostgreSQL, Oracle, MongoDB)
- `messaging` - RabbitMQ AMQP client
- `server` - Echo HTTP server with middleware
- `logger` - Structured logging with zerolog
- `observability` - OpenTelemetry provider (traces + metrics)

## API Endpoints

Base path: `/api/v1` (configured in `config.yaml: server.path.base`)

**Health checks:**
- `GET /health` - Liveness probe
- `GET /ready` - Readiness probe (checks DB + messaging)

**Products module:**
- `GET /api/v1/products` - List all products
- `GET /api/v1/products/:id` - Get product by ID
- `POST /api/v1/products` - Create product
- `PUT /api/v1/products/:id` - Update product
- `DELETE /api/v1/products/:id` - Delete product

## Configuration Files

- `config.yaml` - Base configuration (not present in this project, uses framework defaults)
- [config.development.yaml](config.development.yaml) - Development overrides (extensively documented)
- `.env` - Secrets (gitignored, use `.env.example` as template)
- [etc/docker/docker-compose.yml](etc/docker/docker-compose.yml) - Infrastructure services
- [Makefile](Makefile) - Development commands

## Important Patterns

### Error Handling
Use go-bricks structured errors where possible. Handlers should return appropriate HTTP status codes.

### Logging
Use structured logging via `deps.Logger`:
```go
m.logger.Info().
    Str("product_id", id).
    Msg("Product created successfully")
```

### Database Queries
Use Squirrel query builder (imported by go-bricks):
```go
query := squirrel.Select("*").
    From("products").
    Where(squirrel.Eq{"id": id})
```

### Migrations
- Place SQL files in [migrations/](migrations/) directory
- Use Flyway naming: `V1__description.sql`, `V2__another.sql`
- Run with `make migrate`

## Docker Infrastructure

All Docker-related files are in [etc/docker/](etc/docker/) directory:
- `docker-compose.yml` - Main compose file with service profiles
- `otel/` - OpenTelemetry Collector configurations (Prometheus vs. DataDog)
- `prometheus/` - Prometheus scrape configuration
- `grafana/` - Grafana datasource auto-provisioning

**Service profiles:**
- `--profile local` - Prometheus + Grafana + Jaeger (local development)
- `--profile datadog` - DataDog Cloud integration (production-like)
- `--profile migrations` - Flyway migration runner

## Common Troubleshooting

### DEBUG Environment Variable Conflict
```bash
# Symptom: Configuration error on startup
# Solution: Unset DEBUG environment variable
unset DEBUG && make run
```

### Port Conflicts
```bash
# Stop all services and remove orphaned containers
make docker-down
docker ps -a | grep go-bricks | awk '{print $1}' | xargs docker rm -f
make docker-up
```

### Database Connection Pool Exhaustion
```bash
# Symptom: "no connections available" errors under load
# Solution: Increase pool size in config.development.yaml
database.pool.max.connections: 50  # Increase from default 25
```

### Slow Query Performance
```bash
# Enable slow query logging in config.development.yaml
database.query.slow.threshold: 100ms
database.query.slow.enabled: true

# Run application and check logs for slow queries
make run
```
