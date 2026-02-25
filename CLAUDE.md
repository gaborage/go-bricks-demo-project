# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is a **go-bricks demo project** demonstrating production-ready patterns for building modular Go applications. It uses the `go-bricks` framework (located at `../go-bricks`) with local replacement via `go.mod`.

**Key characteristics:**
- Framework-based modular architecture
- Multi-tenant capable (currently running in single-tenant mode)
- PostgreSQL + RabbitMQ infrastructure
- REST API with Echo web framework
- Dual observability stacks: Prometheus/Grafana/Tempo/Loki (local) + New Relic (cloud)
- Comprehensive load testing with k6

**Requirements:**
- Go 1.25.1+
- Docker & Docker Compose
- Make

## Framework Philosophy

This repository is the **public showcase for GoBricks**. It exists so external engineers can clone the project, run it locally, and experience core framework capabilities—configuration, observability, secrets, jobs, messaging—without reverse engineering. Every contribution should sharpen that first-hour experience.

**GoBricks** is a production-grade framework for building MVPs fast. It provides enterprise-quality tooling (validation, observability, tracing, type safety) while enabling rapid development velocity. The framework itself maintains high quality standards so applications built with it can move quickly with confidence.

**Success Criteria:** Visitors should be able to say, "I stood up a tenant-aware API with tracing, secrets, jobs, and database access in under an hour using GoBricks," and they should leave confident they can repeat that pattern in their own domain.

## Core Development Principles

When working in this codebase, follow these principles from the [developer manifesto](wiki/developer.manifesto.md):

- **Framework First** → Reach for shipped bricks (config loader, module wiring, telemetry helpers, secrets store) before inventing bespoke plumbing
- **Explicit > Implicit** → Code must be clear. No hidden defaults, no magic configuration
- **Type Safety > Dynamic Hacks** → Refactor-friendly code. Breaking changes prioritized for compile-time safety
- **Deterministic > Dynamic Flow** → Predictable, testable logic. Same inputs always produce same outputs
- **Composition > Inheritance** → Flexible, simple structures. Use interfaces and embedding over class hierarchies
- **Context-First Design** → Always pass `context.Context` as first parameter for tracing, cancellation, deadlines. No global variables for tenant IDs or trace IDs—always thread context through calls
- **Security First** → Input validation mandatory at all boundaries. Secrets from env/vault only. Audit `WhereRaw()` usage with required annotations
- **Vendor Agnosticism** → Abstract high-cost dependencies (databases), embrace low-cost ones (HTTP frameworks)
- **Interface Segregation** → Small, focused interfaces for testability (e.g., `Client` vs `AMQPClient`)

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
make dev            # Full dev environment: docker-up + migrate (recommended first step)
make build          # Build application binary to bin/go-bricks-demo-project
make run            # Build + run (requires services to be running)
make test           # Run all tests with race detector
make check          # Run fmt + lint + test (pre-commit checks)
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
make loadtest-smoke      # Quick validation (30 seconds) - run this first!
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

**In repository methods:**
```go
func (r *Repository) GetByID(ctx context.Context, id string) (*Product, error) {
    db, err := r.getDB(ctx)  // Get DB for this request's context
    if err != nil {
        return nil, err
    }

    // Use type-safe Filter API
    qb := database.NewQueryBuilder(database.PostgreSQL)
    f := qb.Filter()
    query, args, err := qb.Select("id", "name", "price").
        From("products").
        Where(f.Eq("id", id)).
        ToSQL()
    if err != nil {
        return nil, err
    }

    // Execute query...
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

### Local Stack (Prometheus + Grafana + Tempo + Loki)

**Best for:** Local development with immediate feedback (< 30 seconds vs. 10-15 min cloud delay)

**Start:**
```bash
cd etc/docker
docker-compose --profile local up -d
```

**Access:**
- Prometheus: http://localhost:9090 (metrics storage)
- Grafana: http://localhost:3000 (admin/admin) - **Dashboards pre-loaded!**
- Tempo: http://localhost:3200 (distributed tracing backend)
- Grafana Drilldown → Traces: DataDog-like trace exploration (queryless!)
- Loki: http://localhost:3100 (log aggregation)

**Features:**
- **Metrics** scraped from OTel Collector on port 8889
- **Distributed tracing** with Tempo (DataDog APM-like capabilities)
- **APM metrics generation** - Automatic RED metrics from traces (like DataDog!)
- **Service graphs** - Visual service topology and dependencies
- **TraceQL** - Powerful query language for trace analysis
- **Log aggregation** with Loki (via Grafana Alloy)
- **Pre-built dashboards** (see Dashboard section below)
- Auto-provisioned Grafana datasources with **log ↔ trace correlation**
- No cloud dependency (work offline)

### Pre-built Grafana Dashboards

The local stack includes two production-ready dashboards:

**1. Application Overview** (`Go Bricks - Application Overview`)
- **Golden Signals:** Request rate, P95 latency, error rate, DB query time
- **Response Time Percentiles:** p50, p95, p99 over time
- **Request Rate by Endpoint:** Track traffic distribution
- **Database Performance:** Query latency by operation type (select, insert, update, delete)
- **HTTP Status Distribution:** Visualize 2xx, 4xx, 5xx responses
- **Live Application Logs:** Tail logs directly in the dashboard
- **Go Runtime Metrics (OTel):** Memory usage, goroutines, CPU, GC performance, file descriptors
- **Advanced Go Metrics:** GOMEMLIMIT, GOMAXPROCS, GOGC config, GC heap goal, scheduler latency, allocation rates

**OTel Runtime Metrics Support:**
The dashboard now uses OpenTelemetry semantic conventions for Go runtime metrics:
- Memory metrics: `gobricks_go_memory_used` (with type labels), `gobricks_go_memory_limit`, `gobricks_go_memory_allocated`
- Goroutine metrics: `gobricks_go_goroutine_count`
- GC metrics: `gobricks_go_memory_gc_goal`, existing `go_gc_duration_seconds`
- Config metrics: `gobricks_go_processor_limit` (GOMAXPROCS), `gobricks_go_config_gogc`
- Scheduler metrics: `gobricks_go_schedule_duration` (histogram)
- Allocation metrics: `gobricks_go_memory_allocations` (count)
- All panels include fallback to legacy `go_memstats_*` metrics for backward compatibility

**2. Error Analysis** (`Go Bricks - Error Analysis`)
- **HTTP Error Rate:** Track 4xx/5xx errors by endpoint over time
- **Error Count by Status Code:** Bar chart of total errors
- **Success Rate Gauge:** Real-time SLA tracking
- **Error Logs Stream:** Live error-level logs with JSON parsing
- **Top Error Endpoints:** Identify problematic routes
- **Log Volume by Level:** Visualize log distribution (info, warn, error)

**Access dashboards:**
1. Open Grafana: http://localhost:3000
2. Navigate to **Dashboards** → **Go Bricks** folder
3. Or use direct links:
   - Overview: http://localhost:3000/d/go-bricks-overview
   - Errors: http://localhost:3000/d/go-bricks-errors

**Dashboard features:**
- **Auto-refresh:** Every 10 seconds
- **Log → Trace correlation:** Click trace_id in logs to jump to Tempo trace
- **Trace → Log correlation:** Navigate from trace to related logs seamlessly
- **Customizable:** Edit and save your own versions

### Cloud Stack (New Relic)

**Best for:** Production-like monitoring and APM

**Setup:**
1. Get New Relic license key from https://one.newrelic.com/launcher/api-keys-ui.api-keys-launcher
2. Create `.env` file in project root:
   ```bash
   NEW_RELIC_LICENSE_KEY=your_license_key_here
   NEW_RELIC_REGION=US  # or EU
   ```
3. Start stack:
   ```bash
   make docker-up-newrelic
   # Or manually:
   cd etc/docker
   docker-compose --profile newrelic up -d
   ```

**Access:**
- New Relic One: https://one.newrelic.com/nr1-core
- APM & Services: https://one.newrelic.com/nr1-core?filters=(domain%20IN%20('APM'))
- Service name: `go-bricks-demo-project`

### Switching Observability Stacks

```bash
# Stop current stack
cd etc/docker && docker-compose down

# Start desired stack
docker-compose --profile local up -d      # For Prometheus/Grafana/Loki/Tempo
docker-compose --profile newrelic up -d   # For New Relic
```

**Note:** Application doesn't need restart when switching - it always sends to `localhost:4317`.

### Log Collection Architecture

**Planned implementation (OTLP export via Grafana Alloy):**
```
Application (zerolog) → OTel SDK → Grafana Alloy → Loki → Grafana
                                  ↓
                              (also exports to Tempo & Prometheus)
```

**Current Status:**
- ⚠️ **OTLP log export is NOT working yet** - go-bricks framework may not have fully implemented OTLP log export
- Configuration shows `mode="stdout+OTLP"` but logs are only going to stdout
- Grafana Alloy is configured and ready to receive OTLP logs on port 4317
- Loki is configured with `volume_enabled: true` and ready to ingest logs

**When OTLP logs work, you'll get:**
- Better log ↔ trace correlation (trace_id automatically linked)
- Structured log attributes as Loki labels
- Dual-mode logging: action logs (HTTP summaries) + trace logs (debug)

### Querying Logs in Grafana

**LogQL query examples:**

```logql
# All error-level logs
{container_name=~".*"} |= "level" | json | level="error"

# Logs for a specific trace
{container_name=~".*"} |= "trace_id" | json | trace_id="abc123"

# HTTP errors (status >= 400)
{container_name=~".*"} | json | http_status >= 400

# Search for specific text in messages
{container_name=~".*"} |= "database connection failed"

# Rate of error logs (errors per second)
sum(rate({container_name=~".*"} | json | level="error" [5m]))
```

**Tip:** Use **Explore** view in Grafana for ad-hoc log queries, or use pre-built dashboard panels.

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

### Testing Philosophy

This is a **demo application** built with GoBricks, not production code. Testing strategy reflects this:

**Coverage Target:** 60-70% on core business logic (repository queries, service methods, HTTP handlers)

**Testing Focus:**
- **Always test:** Database queries, HTTP handlers, messaging consumers
- **Happy paths** + critical error scenarios (validation failures, DB errors, not found cases)
- **Demo coverage:** Each showcased brick (telemetry spans, repository queries, scheduled jobs, secrets handling) has at least one runnable integration or acceptance example
- **Defer:** Exotic configuration combinations, rare edge cases
- **Iterate:** Some code may be throwaway/refactored as requirements evolve while refining the demo

**Quality Gate:** Run `make check` (fmt + lint + tests) before pushing to keep main branch green.

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

**TypeScript Support:**
All load tests are written in TypeScript for better type safety and IDE support. k6 v1.3.0+ has native TypeScript support, so tests run directly without any build step:

```bash
# Type check tests (optional - for catching errors before running)
npm run type-check

# Run tests directly - k6 handles TypeScript transpilation
k6 run loadtests/products-crud.ts
make loadtest-smoke

# No webpack or build step needed!
```

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

**Legacy module** (raw response, no APIResponse envelope):
- `GET /api/v1/legacy/products` - List products (raw JSON)
- `GET /api/v1/legacy/products/:id` - Get product by ID (raw JSON)

## Configuration Files

- `config.yaml` - Base configuration (not present in this project, uses framework defaults)
- [config.development.yaml](config.development.yaml) - Development overrides (extensively documented)
- `.env` - Secrets (gitignored, use `.env.example` as template)
- [etc/docker/docker-compose.yml](etc/docker/docker-compose.yml) - Infrastructure services
- [Makefile](Makefile) - Development commands

## Important Patterns

### Development Practices

Follow these engineering principles when contributing:

- **SOLID** - Encapsulate behavior behind narrow interfaces (see [internal/modules/products/repository/repository.go](internal/modules/products/repository/repository.go)) so services remain testable and swappable
- **Fail Fast** - Abort startup when initialization misbehaves ([cmd/api/main.go](cmd/api/main.go) uses fatal logging for module registration failures)
- **DRY** - Share cross-cutting capabilities via bricks in [internal/modules/shared/](internal/modules/shared/) instead of copy-pasting helpers
- **CQS** (Command Query Separation) - Split reads and writes where clarity improves ([internal/modules/products/http/](internal/modules/products/http/) handlers call query and command-specific service methods)
- **KISS** - Prefer the defaults that GoBricks provides before layering additional frameworks or wrappers
- **YAGNI** - Only build flows the showcase actively demonstrates today; defer speculative features to ADRs before investing
  - **Exceptions:** Abstractions for vendor differences (databases, cloud providers) are justified. Test utilities justified only if actively used

### Security Requirements

Security is mandatory, not optional:

- **Input validation** is **REQUIRED** at all boundaries (HTTP handlers, messaging consumers, database queries)
- **WhereRaw() audit requirement:** Any use of `WhereRaw()` must include this annotation:
  ```go
  // SECURITY: Manual SQL review completed - identifier quoting verified
  query := qb.WhereRaw("custom_condition")
  ```
- **Secrets management:** Only load secrets from environment variables or secret managers (AWS Secrets Manager, HashiCorp Vault). See [internal/modules/shared/secrets/](internal/modules/shared/secrets/)
- **No hardcoded credentials** - Never commit secrets. No secrets in logs or error messages
- **Audit logging** - Log sensitive operations (access control changes, data modifications) with trace IDs for correlation

### Raw Response Mode

Use `server.WithRawResponse()` to bypass the standard `APIResponse` envelope (`{"data": ..., "meta": {...}}`). This is designed for the **Strangler Fig migration pattern**: incrementally replacing legacy APIs while maintaining backward compatibility with existing consumers.

```go
// Standard route — response wrapped in APIResponse envelope
server.GET(hr, r, "/products/:id", h.GetProduct)
// → {"data": {"id": "...", "name": "..."}, "meta": {"timestamp": "...", "traceId": "..."}}

// Raw response route — handler return value sent directly as JSON
server.GET(hr, r, "/legacy/products/:id", h.GetProduct,
    server.WithRawResponse(),
    server.WithTags("legacy"),
)
// → {"id": "...", "name": "..."}
```

The handler signature is identical — only the route option changes the wire format. See [internal/modules/legacy/](internal/modules/legacy/) for a complete example.

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
Use go-bricks type-safe Filter API for all queries:

```go
qb := database.NewQueryBuilder(database.PostgreSQL)
f := qb.Filter()

// SELECT with filters
query, args, err := qb.Select("id", "name", "price").
    From("products").
    Where(f.Eq("status", "active")).
    Where(f.Gt("price", 10.0)).
    ToSQL()

// UPDATE with filters
query, args, err := qb.Update("products").
    Set("status", "inactive").
    Where(f.Eq("id", productID)).
    ToSQL()

// DELETE with filters
query, args, err := qb.Delete("products").
    Where(f.Eq("id", productID)).
    ToSQL()
```

**Filter methods:** `Eq`, `NotEq`, `Lt`, `Lte`, `Gt`, `Gte`, `In`, `NotIn`, `Like`, `Null`, `NotNull`, `Between`, `And`, `Or`, `Not`, `Raw`

**Important:** Always use `ToSQL()` (uppercase) not `ToSql()` for consistent API.

### Migrations
- Place SQL files in [migrations/](migrations/) directory
- Use Flyway naming: `V1__description.sql`, `V2__another.sql`
- Run with `make migrate`

## Docker Infrastructure

All Docker-related files are in [etc/docker/](etc/docker/) directory:
- `docker-compose.yml` - Main compose file with service profiles
- `otel/` - OpenTelemetry Collector configurations (Prometheus vs. New Relic)
- `prometheus/` - Prometheus scrape configuration
- `promtail/` - Promtail log collection configuration
- `loki/` - Loki log storage configuration
- `grafana/provisioning/` - Auto-provisioning configs
  - `datasources/` - Prometheus, Tempo, Loki datasources
  - `dashboards/` - Dashboard provider configuration
  - `dashboards/json/` - Pre-built dashboard JSON files
- `alloy/` - (Reserved for future Grafana Alloy integration)

**Service profiles:**
- `--profile local` - Prometheus + Grafana + Tempo + Loki (local development)
- `--profile newrelic` - New Relic Cloud integration (production-like)
- `--profile migrations` - Flyway migration runner

## Contribution Guidelines

When contributing to this showcase project, follow this workflow to maintain quality and consistency:

### Planning Changes

- **Framework-impacting changes:** Capture decisions in ADRs or the [wiki/](wiki/) directory so first-time readers see the latest guidance
- **Breaking changes:** Document in ADRs when changes improve safety/correctness (type safety, security)
- **New features:** Only add flows that actively demonstrate GoBricks capabilities

### Development Workflow

1. **Make your changes** following the Core Development Principles above
2. **Add examples** - When extending functionality, add example requests, scripts, or documentation showing how to experience it
3. **Keep demo fresh** - Ensure new capabilities are discoverable and runnable
4. **Update touchpoints** - Update relevant files when configuration or dependencies change:
   - [README.md](README.md) - If quick start or features change
   - `.env.example` - If new environment variables are needed
   - [config.development.yaml](config.development.yaml) - If new config options are added
   - [CLAUDE.md](CLAUDE.md) - If architecture or workflows change
   - Onboarding steps - If setup process changes

### Validation (Quality Gate)

Before pushing to `main`, run the quality gate:

```bash
make check  # Runs: fmt + lint + test
```

**Required checks:**
- `make fmt` - Code formatting with gofmt
- `make lint` - Static analysis with golangci-lint (must pass with no errors)
- `make test` - All tests pass with race detector

**Recommended checks:**
- `make coverage` - Review HTML coverage report, aim for 60-70% on business logic
- Integration tests - Add or update when introducing new database queries, HTTP endpoints, or messaging flows
- Load tests - Run `make loadtest-smoke` to validate performance hasn't regressed

### Testing Requirements

- **Always add tests for:** Database repository methods, HTTP handlers, service business logic
- **Integration tests:** Each new brick or capability should have at least one runnable example
- **Update existing tests:** When changing signatures or behavior, update affected tests

## Code & Runtime Tour

New to this codebase? Follow this tour to understand how everything fits together.

### Code Tour (15-20 minutes)

Explore the code in this order:

1. **[cmd/api/main.go](cmd/api/main.go)** - Application entry point
   - See how `app.New()` bootstraps the framework
   - Note `getModulesToLoad()` - how modules are registered
   - Observe fail-fast pattern with fatal logging

2. **[internal/modules/products/module.go](internal/modules/products/module.go)** - Module implementation
   - How modules implement `app.Module` interface
   - Dependency injection via `Init(deps *app.ModuleDeps)`
   - Module wiring: repository → service → handler chain
   - Route registration in `RegisterRoutes()`

3. **[internal/modules/products/http/](internal/modules/products/http/)** - HTTP handlers
   - Request validation
   - Service method calls
   - Error handling and status codes
   - Structured logging

4. **[internal/modules/products/repository/](internal/modules/products/repository/)** - Data access layer
   - Context-aware database access via `getDB(ctx)`
   - Type-safe Filter API usage
   - Query builder patterns (`Select`, `Where`, `ToSQL()`)

5. **[internal/modules/legacy/](internal/modules/legacy/)** - Raw response module
   - Demonstrates `WithRawResponse()` route option
   - Reuses products service/repository (cross-module dependency)
   - Compare route registration with products module to see the difference

6. **[internal/modules/shared/](internal/modules/shared/)** - Shared bricks
   - `secrets/` - Multi-tenant AWS Secrets Manager integration
   - Reusable cross-cutting capabilities

7. **[config.development.yaml](config.development.yaml)** - Configuration
   - Extensively commented config showing all options
   - Database pool settings
   - Observability configuration
   - Multi-tenant settings

### Runtime Tour (15-20 minutes)

Experience the application running:

1. **Bootstrap environment:**
   ```bash
   make dev  # Starts docker-up + runs migrations
   ```

2. **Start application:**
   ```bash
   make run  # Build and start the API server
   ```

3. **Exercise endpoints:**
   ```bash
   # Health checks
   curl http://localhost:8080/health
   curl http://localhost:8080/ready

   # Products CRUD
   curl http://localhost:8080/api/v1/products
   curl http://localhost:8080/api/v1/products/1

   # Or use the test script
   make test-products-api
   ```

4. **Review telemetry:**
   - **Logs:** Check terminal for structured JSON logs with trace IDs
   - **Metrics:** Open http://localhost:9090 (Prometheus) → Graph → search `gobricks_`
   - **Traces:** Open http://localhost:3000 (Grafana) → Explore → Tempo → search recent traces
   - **Dashboards:** http://localhost:3000/d/go-bricks-overview

5. **Inspect generated metrics:**
   ```bash
   # See what metrics are being emitted
   curl http://localhost:8889/metrics | grep gobricks_
   ```

6. **Run load test:**
   ```bash
   make loadtest-smoke  # 30-second quick validation
   # Watch metrics in Grafana update in real-time
   ```

After this tour, you'll understand the module system, dependency injection, observability integration, and how to extend the showcase with new capabilities.

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

### Grafana Not Showing Logs
```bash
# Symptom: Loki datasource works but no logs appear in dashboards
# Solution 1: Check Promtail is running and collecting logs
docker logs go-bricks-promtail

# Solution 2: Verify Loki is receiving data
curl http://localhost:3100/ready
curl http://localhost:3100/metrics | grep loki_ingester_streams_created_total

# Solution 3: Ensure application is running and generating logs
docker ps | grep go-bricks

# Solution 4: Test Loki query manually
curl -G -s "http://localhost:3100/loki/api/v1/query" --data-urlencode 'query={container_name=~".*"}' | jq
```

### OTel Collector Unhealthy Status
```bash
# This is expected behavior - collector may show "unhealthy" but still works
# Check if it's actually processing telemetry:
curl http://localhost:8889/metrics | grep gobricks_  # Should show metrics
docker logs go-bricks-otel-collector-local | tail -20  # Should show trace/metric processing
```
