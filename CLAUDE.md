# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Delegation policy (read first)

The session model (Fable) is the orchestrator, not the worker. Fable is the most expensive tier; spend its tokens only on decomposition, judgment calls, synthesis, and talking to the user. Delegate everything else to subagents via the Agent tool, picking the cheapest model that can do the job well:

- `model: "opus"` — the default worker tier. Anything requiring real judgment: implementation, debugging, architecture-aware exploration, adversarial review.
- `model: "sonnet"` — cheap tier for mechanical or low-stakes work: running tests and reporting output, simple greps/lookups with a known target, rote refactors from an exact spec, formatting, screenshot capture, admin chores. If getting it slightly wrong is cheap to catch, use sonnet.

- **Exploration/research**: never read broadly yourself. Spawn `Explore` agents (model: opus) with tightly scoped questions; consume their synthesized reports, not raw files. Trivial "find the file that defines X" lookups can go to sonnet.
- **Implementation**: for any multi-file change, spawn `general-purpose` agents (model: opus) with exact file paths, the relevant doctrine from this file, and a definition of done (tests to run). Independent changes get parallel agents in one message.
- **Verification/review**: adversarial review and blast-radius checks go to opus agents. Plain test runs and lint passes go to sonnet agents.
- Fable itself only edits directly when the change is small (one or two files, already-known locations).
- **Tripwire (added after Fable did a 5-file change inline, 2026-08-26): before the first Edit/Write, count the files the change will touch. Three or more, or any screenshot/browser-proof chore: stop and spawn agents instead. Inline Fable work is only sequential diagnosis (each command depends on the previous answer) and 1-2 file edits.**

Token rules:
- Batch independent agent launches in a single message so they run concurrently.
- Give agents file paths and constraints up front so they don't rediscover this file's contents; paste the relevant doctrine into the prompt.
- Never re-read files an agent already summarized; trust the report, spot-check only what you'll edit.
- Read only the line ranges you need from large files (`docs/core/PROGRESS.md` is 835 lines — read the lessons ledger at the end, not the whole file).
- Don't echo file contents or long diffs back to the user; report conclusions.

## Project Overview

This is a **go-bricks demo project** demonstrating production-ready patterns for building modular Go applications. It uses the `go-bricks` framework, resolved from the module proxy at the version pinned in `go.mod` (see [Framework Dependency](#framework-dependency)) — not a `replace` directive.

**Key characteristics:**
- Framework-based modular architecture
- Multi-tenant capable (currently running in single-tenant mode)
- PostgreSQL + RabbitMQ infrastructure
- REST API with Echo web framework
- Transactional Outbox for reliable event publishing (dual-write pattern)
- KeyStore for named RSA key pair management (signing/verification)
- RabbitMQ native streams: a partitioned super stream (`product-activity`, 3 partitions) consumed by a typed, replayable projection
- Dual observability stacks: Prometheus/Grafana/Tempo/Loki (local) + New Relic (cloud)
- Comprehensive load testing with k6

**Requirements:**
- Go 1.27+
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
# 1. Start infrastructure (PostgreSQL, RabbitMQ, observability)
make docker-up

# 2. Run database migrations
make migrate

# 3. Generate RSA keys for KeyStore demo (first time only)
make generate-keys

# 4. Build and run application
make run

# 4. Test the API
curl http://localhost:8080/api/v1/health
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

**Note:** A bare `DEBUG` environment variable used to conflict with go-bricks' `debug` config section (startup crash). As of go-bricks **v0.43.0 (#601)** the framework silently drops a bare `DEBUG` env var, so this workaround is **no longer required** — kept for anyone on an older framework version:
```bash
unset DEBUG && make run   # only needed on go-bricks < v0.43.0
```

**Not to be confused with `app.debug`** (a different key from the `debug` section above). [config.development.yaml](config.development.yaml) sets `app.debug: true` because go-bricks v0.61.0 (ADR-084) gates response `error.details` on `app.debug` **and** a development environment; without it the demo's validation errors lose their details map.

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

### Database Session Timezone (v0.29.0+)

Every new database connection has its session timezone set to a configured IANA name. **Breaking behavior change** in go-bricks v0.29.0: apps that previously inherited the database server's default now default to UTC. Set `database.timezone: "-"` to preserve the legacy behavior — keep the quotes in YAML so the hyphen doesn't get parsed as a list marker.

The framework applies the setting per *physical* connection (PostgreSQL via pgx `RuntimeParams` in the StartupMessage), so pool members spawned later for growth or after drops don't drift back to the server default — a real bug a one-shot `SET TIME ZONE` after `sql.Open` would have.

Demo config in [config.development.yaml](config.development.yaml) — default DB uses `UTC`, the `analytics` named DB uses `Asia/Tokyo` to make the per-DB enforcement visible:

```bash
psql -h localhost -p 5432 -U postgres -d postgres -c "SHOW TIMEZONE;"   # → UTC
psql -h localhost -p 5433 -U postgres -d analytics -c "SHOW TIMEZONE;"  # → Asia/Tokyo
```

See [ADR-016](https://github.com/gaborage/go-bricks/blob/main/wiki/adr_016_database_session_timezone.md) for the full rationale (incl. Oracle implementation, accepted values, rejected alternatives).

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
curl http://localhost:8080/api/v1/health
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

**go-bricks version:** `go.mod` pins a pseudo-version of go-bricks `main`
(`v0.62.1-0.20260904182202-8bebd2789ce1`, the unreleased v0.63.0). There is no
`replace` directive — builds and CI resolve the framework from the module proxy
like any other dependency.

**Local iteration** against a sibling checkout at `../go-bricks` uses a `go.work`
file. It stays untracked — `.gitignore` is a deny-all allowlist, so `go.work` is
ignored without an explicit rule — and it must: committing it would point CI and
every clean clone at a checkout that does not exist there.

```bash
go work init . ../go-bricks   # untracked by .gitignore — never force-add
cd ../go-bricks
# Make changes
cd ../go-bricks-demo-project
make build  # picks up local changes while go.work exists
```

Delete or rename `go.work` (or build with `GOWORK=off`) to go back to the pinned
pseudo-version. Promoting a framework change into the demo means bumping that
pin with `go get github.com/gaborage/go-bricks@<commit-or-tag>`, not adding a
`replace`.

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
- `GET /api/v1/health` - Liveness probe
- `GET /api/v1/ready` - Readiness probe (checks DB + messaging)

**Products module:**
- `GET /api/v1/products` - List all products
- `GET /api/v1/products/:id` - Get product by ID
- `POST /api/v1/products` - Create product
- `PUT /api/v1/products/:id` - Update product
- `DELETE /api/v1/products/:id` - Delete product

**Legacy module** (raw response, no APIResponse envelope):
- `GET /api/v1/legacy/products` - List products (raw JSON)
- `GET /api/v1/legacy/products/:id` - Get product by ID (raw JSON)

**Webhooks module** (KeyStore signing demo):
- `POST /api/v1/webhooks/sign` - Sign a JSON payload with RSA key
- `POST /api/v1/webhooks/verify` - Verify a payload's signature

**Tokens module** (JOSE middleware demo — VTS-style):
- `POST /api/v1/tokens` - JOSE-protected partner endpoint (decrypt+verify in, sign+encrypt out)
- `POST /api/v1/tokens/relay` - Plaintext entry that drives the outbound `JOSETransport` against the peer simulator
- `POST /api/v1/__sim/peer/tokens` - In-process peer simulator (inverse JOSE policy; demo-only)

**Payments module** (sealed AMQP messages demo):
- `POST /api/v1/payments/authorize` - Authorize a payment; publishes a sealed `payment.authorized` event (202 Accepted; the response carries `cardLast4`, never the PAN)

**Activity module** (RabbitMQ super-stream demo):
- `GET /api/v1/products/activity` - Projection built by the stream consumer: per-product event counts, per-partition delivery counts, and a ring of the last 50 events (each carrying the `product-activity-N` partition it arrived on)
- `POST /api/v1/__sim/streams/poison` - Publishes malformed bytes through the same publisher handle so they land on a partition; the typed consumer skips them and keeps going (demo-only, like the tokens peer simulator)

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

### Transactional Outbox Pattern

The products module demonstrates reliable event publishing using the **dual-write pattern**. When creating or deleting a product, the business data and an outbox event are committed in the same database transaction. A background relay (provided by the `outbox` framework module) polls the outbox table and publishes events to RabbitMQ.

```go
// In service — transactional create:
tx, _ := db.Begin(ctx)
defer tx.Rollback(ctx)
repo.CreateTx(ctx, tx, product)
outbox.Publish(ctx, tx, &app.OutboxEvent{
    EventType:   "product.created",
    AggregateID: product.ID,
    Payload:     product,
})
tx.Commit(ctx)
```

**Config:** See `outbox:` section in [config.development.yaml](config.development.yaml).

**The demo owns the outbox DDL** (`outbox.autocreatetable: false`). go-bricks v0.61.0 (ADR-088) reshaped the ledger — rows gained `seq` and `lane`, plus a companion `gobricks_outbox_leader` table so one replica drains — and framework autocreate only ever CREATEs a missing table, never ALTERs an existing one. `migrations/V3__upgrade_outbox_ledger.sql` carries that shape, so **run `make migrate` before `make run`**, on a fresh volume as well as a retained one.

**Framework modules registered in main.go:**
- `scheduler.NewModule()` — provides the job scheduler for the outbox relay
- `outbox.NewModule()` — provides `deps.Outbox` (OutboxPublisher)

**Event types:** `product.created`, `product.updated`, `product.deleted`
**Exchange:** `product-events` (topic, durable) declared in products module's `DeclareMessaging()`

### KeyStore RSA Signing

The webhooks module demonstrates the **KeyStore** brick — named RSA key pairs loaded from DER files at startup. The signing service uses `deps.KeyStore.PrivateKey("webhook-signing")` to sign and `PublicKey("webhook-signing")` to verify payloads.

```go
// Sign a payload
privKey, _ := keyStore.PrivateKey("webhook-signing")
sig, _ := rsa.SignPKCS1v15(rand.Reader, privKey, crypto.SHA256, hash)

// Verify a signature
pubKey, _ := keyStore.PublicKey("webhook-signing")
err := rsa.VerifyPKCS1v15(pubKey, crypto.SHA256, hash, sig)
```

**Config:** See `keystore:` section in [config.development.yaml](config.development.yaml).
**Key generation:** `make generate-keys` creates DER files in `certs/` (gitignored).

### JOSE Middleware (Nested JWE-of-JWS)

The tokens module ([internal/modules/tokens/](internal/modules/tokens/)) demonstrates the framework's JOSE middleware on a Visa Token Services–style integration. Both directions are exercised:

- **Inbound**: request body is a compact JWE-of-JWS. The framework decrypts with our private key, verifies the inner JWS with the peer public key, then binds the plaintext into a struct before the handler runs.
- **Outbound**: response struct is sealed with our private signing key + peer public encryption key.
- **Outbound `JOSETransport`**: the relay endpoint wraps an `httpclient.Client` with `WithJOSE(...)` and POSTs to an in-process peer simulator, exercising the same code path a production app uses to call Visa.

```go
// Both halves of the integration must declare matching jose: tags. Asymmetric
// declaration (only request OR only response tagged) panics at startup.
type TokenizeRequest struct {
    _   struct{} `jose:"decrypt=tokens-our,verify=tokens-peer"`
    PAN string   `json:"pan" validate:"required,min=13,max=19"`
}

type TokenizeResponse struct {
    _     struct{}      `jose:"sign=tokens-our,encrypt=tokens-peer"`
    Token *domain.Token `json:"token"`
}
```

**Module registration order matters:** `keystore.NewModule()` must be registered before any module that declares `jose:`-tagged routes. The framework auto-wires a `jose.KeyStoreResolver` into the handler registry only when `deps.KeyStore` is populated.

**Outbound transport wiring:**
```go
// Build returns (Client, error) as of v0.56.0: it rejects unsafe transport
// composition and (v0.57.0) validates both JOSE policies, so no separate
// Policy.Validate() pre-check is needed at the call site.
client, err := httpclient.NewBuilder(logger).
    WithJOSE(httpclient.JOSEConfig{
        Outbound: outbound, // sign with our key, encrypt to peer
        Inbound:  inbound,  // decrypt with our key, verify peer signature
        Resolver: jose.NewKeyStoreResolver(keyStore),
    }).
    Build()
if err != nil {
    return nil, fmt.Errorf("build relay client: %w", err)
}
```

**Keystore source styles:** the demo intentionally uses both `file:` (DER on disk) and `value:` (inline base64) sources for a single keypair (`tokens-peer`). `make generate-keys` regenerates DER files AND patches the base64 between `BEGIN_TOKENS_PEER_PUB` / `END_TOKENS_PEER_PUB` markers in `config.development.yaml`. In production the `value:` source is typically populated from a secret manager (AWS Secrets Manager, Vault) projected into the pod environment.

**Helper CLI:** `cmd/seal-payload` plays the peer role — reads JSON from stdin, signs with peer private + encrypts to our public, prints a compact JWE for `curl --data-binary @-`. See [cmd/seal-payload/main.go](cmd/seal-payload/main.go).

**Reference:** [go-bricks llms.txt](https://github.com/gaborage/go-bricks/blob/main/llms.txt) (`main`, unreleased v0.63.0 — the version this demo pins) JOSE section for the full API surface, error-code table, and security invariants.

### Sealed Messages (JWE-of-JWS on AMQP)

The payments module ([internal/modules/payments/](internal/modules/payments/)) demonstrates **payload sealing**: one declared Subject field crosses the broker encrypted while its siblings stay readable for routing and DLQ triage, and the producer signs the whole document. The typed publish and consume doors engage sealing from the tags alone — no call site touches go-jose.

```go
// The import gate `_ "github.com/gaborage/go-bricks/messaging/sealed"` registers
// the codec; without it a seal-tagged declaration fails Validate at startup with
// messaging.ErrSealingNotLinked.
type PaymentAuthorized struct {
    _        struct{}    `seal:"sign=payments-sign,encrypt=payments-encrypt"`
    OrderID  string      `json:"orderId" validate:"required"`
    Amount   int64       `json:"amount" validate:"required,gt=0"` // minor units
    Currency string      `json:"currency" validate:"required,len=3,alpha"`
    Card     CardDetails `json:"card" seal:"subject"` // "card" is the signed sp entry
}
```

Ordering is the security decision: **encrypt the Subject first, then sign the whole result** — signing a plaintext PAN would be a confirmation oracle, so the signature always covers ciphertext. `delivery.Body` is one compact JWS whose payload is the business JSON with the `card` member replaced in place by a compact JWE, and `typ: vnd.gobricks.sealed.v1+json` is the only sealed marker (there is no `x-sealed` AMQP header). The tag names **Logical kids**, never key generations: the keystore holds `payments-sign-v1` and `payments-encrypt-v1`, and with a single generation provisioned the producer auto-activates it — so this demo ships no `messaging.seal.active` selector, only a commented-out one in [config.development.yaml](config.development.yaml) for the rotation story (rotation flips the selector; the tag never changes). Lane rules: sealing rides the classic typed lane only — `DeclareTypedPublisher[T]` plus `DeclareTypedConsumerWithMeta` (the meta-less consume door refuses a seal-tagged `T`, since `Meta.DedupKey()` is what the inbox dedups on), while streams typed declarations refuse a seal-tagged `T` and `outbox.Publish` refuses a seal-tagged struct payload with `outbox.ErrSealedPayloadNeedsBytes` (that lane takes `publisher.Seal(ctx, evt)` bytes instead).

**Module registration order matters:** `keystore.NewModule()` and `inbox.NewModule()` must both be registered before the payments module — the seal runtime resolves key material from `deps.KeyStore` at declaration time, and the sealed consumer dedups through `deps.Inbox.ProcessOnce` on the `<sign family>:<jti>` key (the module's `Init` fails fast when `deps.Inbox` is nil). The ledger lives in the framework-default `gobricks_inbox` table.

**Proof:** `make show-sealed-message` publishes one payment, then reads the message off the consumerless `payments.authorized.tap` queue via the RabbitMQ management API and prints the raw body, its decoded JOSE headers and the still-clear routing fields — asserting the PAN appears nowhere on the wire. See [scripts/show-sealed-message.sh](scripts/show-sealed-message.sh).

**Reference:** framework [wiki/sealing.md](https://github.com/gaborage/go-bricks/blob/main/wiki/sealing.md) and [ADR-097](https://github.com/gaborage/go-bricks/blob/main/wiki/adr_097_sealed_amqp_messages.md) for the envelope table, the opener's rule order and error codes, the tenancy rules, and the rotation runbooks.

### Streams & Super-Streams (native RabbitMQ stream protocol)

The activity module ([internal/modules/activity/](internal/modules/activity/)) demonstrates the **native stream lane** — RabbitMQ's stream protocol on port 5552 (`rabbitmq_stream` plugin), not the AMQP lane on 5672. Streams are append-only replicated logs: reads are non-destructive, positions are offsets, and the broker itself remembers where a named consumer got to, so a restart resumes instead of replaying from scratch.

The demo declares one **super stream** — `product-activity`, 3 partitions, which the broker materializes as `product-activity-0` … `product-activity-2` — publishes to it keyed by product ID, and projects it back through a typed consumer:

```go
// The lane is opt-in at the build graph (ADR-091): importing
// github.com/gaborage/go-bricks/messaging/streams is what registers the runtime.
// A `messaging.streams.uri` with no import anywhere in the build fails startup with
// app.ErrStreamsNotLinked. Any import of the package links the lane:
// internal/modules/activity/module.go imports it by name and uses it, because the
// module declares topology. A blank `_` import is what a process that declares no
// topology of its own would need.
func (m *Module) DeclareStreams(decls *streams.Declarations) {
    // Streams never shrink when consumed — retention is explicit or there is none.
    decls.DeclareSuperStream("product-activity", 3, &streams.StreamSpec{
        MaxAge: 24 * time.Hour, // applies to every partition
    })

    // Hold the handle: there is no ModuleDeps field and no accessor to look one up again.
    m.publisher = decls.DeclareSuperStreamPublisher(&streams.SuperStreamPublisherOptions{
        SuperStream: "product-activity",
    })

    // Decode (JSON) -> validate (the same `validate` tags HTTP handlers use) -> handler.
    // WithMeta is how a typed handler still reads msg.Stream (the partition) and msg.Offset.
    streams.DeclareTypedSuperStreamConsumerWithMeta(decls, &streams.SuperStreamConsumerOptions{
        SuperStream: "product-activity",
        Name:        "product-activity-projector", // the offset-tracking key, per partition
        Start:       streams.OffsetFirst(),
    }, m.service.Project) // func(ctx, domain.ProductActivity, *streams.Message) error
}

// RoutingKey picks the partition. Same product -> same partition -> ordered per product.
err := m.publisher.Publish(ctx, &streams.PublishMessage{
    Data:       payload,
    RoutingKey: activity.ProductID,
})
```

**Semantics that shape the handler:**
- **At-least-once, with batched offset commits → handlers must be idempotent.** An offset is committed only *after* its handler returned successfully, and then only in batches: every `offsetstore.countbeforestorage` successes (framework default 500; this demo lowers it to 10 so the count-driven commit is reachable at demo volume — the 5s flush would commit either way), every `offsetstore.flushinterval` (5s), and once more as a final flush at shutdown. That flush narrows the replay window without closing it, so a crash re-delivers everything after the last stored offset.
- **A super-stream handler is called concurrently across partitions → it must be goroutine-safe.** Each partition is its own connection with its own delivery loop: sequential and ordered *within* a partition, concurrent *between* them. There is no worker pool and no handler timeout — bound your own slow work with `context.WithTimeout`.
- **Poison is skipped, never parked (ADR-092).** A body that fails to decode, or decodes but fails `validate`, is deterministic poison: it fails the same way on every attempt and every replica. The lane returns it `Permanent` (no in-place retry whatever `Retry` says), never parks it in the hold ledger, and skips its offset. It survives only in the failure log line and the consume metric — match the two modes with `errors.Is` against `streams.ErrPayloadUndecodable` / `streams.ErrPayloadInvalid`. This is what `POST /api/v1/__sim/streams/poison` proves: the consumer logs and moves on rather than stalling the partition.
- **A stored offset always wins over `Start`.** At startup — and at each SAC promotion — the framework asks the broker for the consumer name's stored offset and resumes at `stored + 1`. `OffsetFirst()` therefore replays the whole log only on the *first* run under that consumer name; after that it is ignored. A failed offset query never silently falls back to `Start`.
- **Routing is murmur3, and that is a compatibility guarantee.** The client hashes `RoutingKey` with murmur3 under RabbitMQ's shared seed, modulo the partition list — the cross-client default, so the Java, .NET and Python clients place the same key on the same partition. `msg.Stream` reports the partition a message actually reached.

**Two traps:**
- **Changing the partition count on an existing super stream is accepted silently.** Where `DeclareStream` surfaces a retention mismatch as precondition-failed and aborts startup, the client swallows "already exists" for super streams — an edited `partitions` value neither reshapes the topology nor fails, and the service just keeps consuming the partitions that exist. (The count is also the murmur3 divisor, so changing it would move existing keys anyway.) Change it by declaring a *new* super stream and cutting over.
- **Under Docker port mapping you need `addressresolver`.** Without it the client dials the address the broker advertises in its metadata response, which is unreachable from outside the container. Both `host` and `port` are set, or neither.

**Lane rules:**
- **Sealing is refused on this lane.** All four typed stream entry points panic at declaration on a `seal:`-tagged `T` — payload sealing is classic-lane only, and a stream consumer decoding a sealed body as plaintext would poison every delivery silently. Sealed events go through the AMQP typed consumer (see [Sealed Messages](#sealed-messages-jwe-of-jws-on-amqp)).
- **One publisher per target per process.** A second `DeclareSuperStreamPublisher` on `product-activity` panics at startup, which is why the poison simulator publishes through the *same* handle the products lane uses rather than declaring its own. The same rule is why a super stream listed in `outbox.superstreams` cannot also be published to directly — this demo lists none, so the direct publisher stays available.
- **Publishing is synchronous and confirmed.** `Publish` blocks until the broker confirms, the client fails, `ctx` expires, or the publisher closes. A `nil` means the broker acknowledged. An error *after* submission (context expiry, confirmation timeout, shutdown sweep) means the outcome is **unknown** — the message may still have landed — so retries are safe only because consumers are idempotent. A super-stream publisher also rejects an empty `RoutingKey` before touching the client: hashing `""` would pile every message onto one partition.
- **Publishing here is the best-effort lane, on purpose.** The products service publishes a `ProductActivity` after each successful create/update/delete through a narrow `ActivityRecorder` interface — declared products-side, so products and legacy compile without the activity module, and injected in [cmd/api/main.go](cmd/api/main.go) only when both modules are enabled. A publish failure is logged at WARN and does **not** fail the HTTP request — the transactional outbox stays the reliable path for anything that must not be lost.
- **Best-effort still means the request waits.** That publish is synchronous and inline, so a broker in trouble blocks the product write's HTTP response for up to the service's 2s `publishTimeout` before the failure is swallowed; it is kept synchronous because moving it off-thread would let two events for one product id reorder on their shared partition. The outbox lane is the reliable path and never blocks on the broker.

**Config:** see the `messaging.streams` section in [config.development.yaml](config.development.yaml) — `uri` (`rabbitmq-stream://…@localhost:5552/%2f`, never derived from `messaging.broker.url`), `addressresolver`, and `offsetstore.countbeforestorage`.

**Requires RabbitMQ 3.13+** — `DeclareSuperStream` is a 3.13-only command — plus the `rabbitmq_stream` plugin enabled and port 5552 published.

**Reference:** framework [wiki/streams.md](https://github.com/gaborage/go-bricks/blob/main/wiki/streams.md) for the full lane, plus [ADR-059](https://github.com/gaborage/go-bricks/blob/main/wiki/adr_059_streams_consumption.md) (consumption and skip-on-failure), [ADR-063](https://github.com/gaborage/go-bricks/blob/main/wiki/adr_063_streams_native_publishing.md) (native publishing), [ADR-091](https://github.com/gaborage/go-bricks/blob/main/wiki/adr_091_streams_opt_in_registration.md) (opt-in at the build graph) and [ADR-092](https://github.com/gaborage/go-bricks/blob/main/wiki/adr_092_typed_stream_consumers_skip_poison.md) (typed consumers skip poison).

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

**Identifier validation (go-bricks v0.60.0, ADR-082):** every identifier argument —
`Select`/`Columns` column lists, `From`/JOIN tables, `OrderBy`/`GroupBy`, and every
`Filter`/`JoinFilter` column — is validated against a safe identifier grammar. An
expression, a function call, a constant or an alias is rejected from `ToSQL()`; move
it to the declared expression hatch `qb.Expr()` / `qb.MustExpr()`:

```go
qb.Select("COUNT(*)")                        // REJECTED
qb.Select(qb.MustExpr("COUNT(*)"))           // SAFE
qb.Select(qb.MustExpr("AVG(price)", "avg"))  // SAFE — expression + alias
qb.OrderBy("created_date DESC")              // SAFE — bounded direction is in the grammar
```

`Expr`/`MustExpr` carry SQL verbatim and are NOT escaped — never interpolate user
input into them. `cols.As(alias)` is the one door that **panics** (at the `As` call,
with `*dbtypes.InvalidAliasError`) rather than deferring to `ToSQL()`.

### Migrations
- Place SQL files in [migrations/](migrations/) directory
- Use Flyway naming: `V1__description.sql`, `V2__another.sql`
- Run with `make migrate`

## Docker Infrastructure

All Docker-related files are in [etc/docker/](etc/docker/) directory:
- `docker-compose.yml` - Main compose file with service profiles
- `rabbitmq/` - Broker config: `rabbitmq.conf` (guest-user loopback override) and `enabled_plugins` (turns on `rabbitmq_stream` for the streams lane on 5552)
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

### CI/CD (GitHub Actions)

All PRs to `main` and pushes to `main` run automated checks via GitHub Actions.

**CI workflow** (`.github/workflows/ci.yml`) — 3 parallel jobs:

| Job | What it runs | Notes |
|-----|-------------|-------|
| **Lint** | `golangci-lint` via official action | v2 config; produces inline PR annotations |
| **Test** | `go test -v -race -coverprofile` | Uploads coverage artifact (7-day retention) |
| **Build** | `go build -o /dev/null ./cmd/api/main.go` | Verifies compilation |

**Security workflow** (`.github/workflows/security.yml`):
- Runs `govulncheck ./...` on PRs, pushes to main, and weekly (Monday 8am UTC)

**Dependabot** (`.github/dependabot.yml`):
- Go modules — weekly updates (prefix: `chore(deps)`)
- GitHub Actions — weekly updates (prefix: `chore(ci)`)

**CI badge** is displayed at the top of README.md.

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

7. **[internal/modules/webhooks/](internal/modules/webhooks/)** - Webhooks module (KeyStore demo)
   - Uses `deps.KeyStore.PrivateKey()` / `PublicKey()` for RSA signing
   - Simple sign/verify HTTP endpoints
   - See `service/signing_service.go` for the core KeyStore usage

8. **[internal/modules/tokens/](internal/modules/tokens/)** - Tokens module (JOSE middleware demo)
   - `handlers/handlers.go` declares `jose:`-tagged request/response structs that drive the inbound + outbound middleware
   - `service/relay_service.go` wires `httpclient.WithJOSE(...)` for the outbound `JOSETransport`
   - In-process peer simulator with the inverse policy makes the demo self-contained
   - [cmd/seal-payload/](cmd/seal-payload/) is the developer tool that produces compact JWE-of-JWS bodies for `curl`

9. **[internal/modules/payments/](internal/modules/payments/)** - Payments module (sealed AMQP messages demo)
   - `domain/payment.go` declares the `seal:`-tagged event: one `seal:"subject"` field encrypted, the rest clear
   - `module.go` shows the classic typed lane — `DeclareTypedPublisher` + `DeclareTypedConsumerWithMeta`, the DLQ queue and the consumerless `payments.authorized.tap` queue
   - `service/service.go` mints the order id and publishes once behind `messaging.EventPublisher[T]`; `module.go`'s handler dedups the delivery through `inbox.ProcessOnce` on `Meta.DedupKey()`
   - `make show-sealed-message` proves the PAN never reaches the broker

10. **[internal/modules/activity/](internal/modules/activity/)** - Activity module (RabbitMQ super-stream demo)
    - `module.go` carries the `messaging/streams` import that opts the lane in (ADR-091) and holds the `DeclareStreams` topology: super stream, publisher handle, typed consumer
    - `domain/` declares `ProductActivity`, the `validate`-tagged struct the typed consumer decodes into
    - `service/` holds the projection (per-product counts, per-partition counts, last-50 ring) — goroutine-safe because partitions deliver concurrently, idempotent because delivery is at-least-once
    - `handlers/` serves `GET /api/v1/products/activity` and the guarded `POST /api/v1/__sim/streams/poison`
    - Products publishes into it via the `ActivityRecorder` seam — interface and payload declared in `products/service` (the consumer owns the contract), adapted onto `activity/domain` in `module.go`, wired in [cmd/api/main.go](cmd/api/main.go) — best-effort, WARN on failure

11. **[config.development.yaml](config.development.yaml)** - Configuration
    - Outbox configuration (poll interval, batch size, retention)
    - KeyStore configuration (DER file paths for RSA keys, including the sealing generations)
    - The commented-out `messaging.seal.active` selector (rotation story)
    - `messaging.streams` — stream URI (port 5552), `addressresolver` for Docker port mapping, and the lowered `offsetstore.countbeforestorage`
    - See `make generate-keys` for key generation

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
   curl http://localhost:8080/api/v1/health
   curl http://localhost:8080/api/v1/ready

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

### DEBUG Environment Variable Conflict (resolved in go-bricks v0.43.0)
```bash
# Symptom (go-bricks < v0.43.0 only): configuration error on startup from a bare DEBUG env var.
# As of v0.43.0 (#601) the framework drops a bare DEBUG var, so this workaround is no longer needed:
unset DEBUG && make run
```

### DB Password Minimum Length (go-bricks v0.49.0)

```bash
# Symptom: app startup or a tenant migration fails with ErrDatabasePasswordTooShort.
# As of go-bricks v0.49.0 (ADR-037), a NON-EMPTY database password shorter than
# 8 bytes fails validation (config.MinDatabasePasswordLength = 8). Empty
# passwords (trust/IAM auth) remain allowed.
#
# The default dev password `postgres` (config.development.yaml) is exactly 8
# bytes — at the floor, zero margin. The multi-tenant demo derives per-tenant
# passwords as `<tenant>_pass` (etc/docker/postgres/multitenant-init.sql +
# config.multitenant.yaml) so even the shortest (acme_pass) is 9 bytes.
# On a RETAINED postgres volume (old `_pw` roles already bootstrapped), just
# re-run `make migrate-multitenant-init` — the bootstrap SQL now re-asserts each
# role's password via ALTER ROLE, migrating `_pw` -> `_pass` without recreating
# the volume.
# Fix: use a password >= 8 bytes, or leave it empty for trust/IAM auth.
```

### Dev CORS Fails Closed (go-bricks v0.50.0)

```bash
# Symptom: after upgrading, a browser client on another origin (e.g. a SPA on
# localhost:3000 calling the API on :8080) is blocked by CORS, and the server
# logs a WARN at startup about wildcard CORS.
# As of go-bricks v0.50.0 (ADR-038), permissive dev wildcard CORS is opt-in.
# The `make run` target sets CORS_DEV_WILDCARD=true to preserve the pre-upgrade
# permissive behavior for local development. To run the binary directly:
CORS_DEV_WILDCARD=true APP_ENV=development ./bin/go-bricks-demo-project
# Production: do NOT use the wildcard — set an explicit allowlist via CORS_ORIGINS.
# curl/k6 and same-origin Grafana are unaffected (no browser CORS enforcement).
```

### Query Builder Rejects Expressions in `Select` (go-bricks v0.60.0)

```bash
# Symptom: a query that built fine before now fails at ToSQL() with
#   invalid select identifier "COUNT(*)": must be a simple or qualified identifier,
#   or a wildcard ("*", "t.*") — use qb.Expr()/Raw() for expressions and aliases
# As of go-bricks v0.60.0 (ADR-082), every identifier door is validated against a
# safe identifier grammar. This bit the products repository's pagination COUNT
# query (internal/modules/products/repository/repository.go).
# Fix: wrap the expression in the declared hatch.
#   qb.Select("COUNT(*)")              ->  qb.Select(qb.MustExpr("COUNT(*)"))
# Note this is a RUNTIME rejection, not a compile error — `go build` stays green,
# so exercise the affected endpoint (GET /api/v1/products?page=1&pageSize=2) after
# upgrading. `OrderBy("created_date DESC")` is unaffected: a bounded ASC/DESC
# direction is part of the grammar.
```

### Outbox TableUnusableError on Retained Volume (go-bricks v0.61.0)

```bash
# Symptom: startup fails with
#   outbox: table "gobricks_outbox" is not usable (missing table or insufficient
#   privileges); run migrations or set outbox.autocreatetable=true: ...
# on a postgres volume that predates the upgrade.
# go-bricks v0.61.0 (ADR-088) reshaped the ledger: rows gained `seq` (identity,
# the drain order) and `lane`, and the relay takes a companion
# gobricks_outbox_leader row FOR UPDATE NOWAIT so exactly one replica drains.
# Framework autocreate only ever CREATEs a MISSING table — it never ALTERs an
# existing one — so flipping autocreatetable back to true does NOT fix this.
# The demo now owns the outbox DDL (outbox.autocreatetable: false).
# Fix: apply the migration that reshapes the ledger.
make migrate   # migrations/V3__upgrade_outbox_ledger.sql

# Alternative (destroys all local data, then re-migrates from scratch):
make docker-down && make dev
```

### Validation `error.details` Missing (go-bricks v0.61.0)

```bash
# Symptom: a 400 from POST /api/v1/products still carries code + message, but the
# details map (validationErrors) is gone, so you can't see WHICH field failed.
# As of go-bricks v0.61.0 (ADR-084), response error.details are gated on
# app.debug: true AND a development environment — the env alone used to be
# enough. Both gates must pass.
# Fix: the demo's dev config now sets app.debug (config.development.yaml).
APP_ENV=development make run   # app.debug: true is already in config.development.yaml
# Production keeps details off on purpose: they render schema facts that a public
# error body should not carry.
```

### Direct AMQP Publish APIs Removed (go-bricks v0.63.0)

```bash
# Symptom: build fails with "c.Publish undefined" / "PublishToExchange undefined"
# / "undefined: messaging.PublishOptions", or a test double stops compiling
# (MockMessagingClient.Publish, MockAMQPClient.PublishToExchange are gone).
# go-bricks v0.63.0 (ADR-096) made the typed publisher the ONLY module-facing
# publish door; the raw-bytes methods left the module-facing types with it.
# Fix: declare a typed publisher and publish values, not bytes.
#   pub := messaging.DeclareTypedPublisher[ProductEvent](decls, opts)  // DeclareMessaging
#   pub.Publish(ctx, client, evt)                                      // service/handler
# Swap the handle in tests behind messaging.EventPublisher[T]
# (messaging/testing.CapturePublisher[T] satisfies it).
# Runtime failure mode: a hand-written AMQPClient or an app.Options
# MessagingClientFactory product carries no byte door, so every publish fails
# with messaging.ErrPublishDoorUnavailable — publish through a framework-built
# client instead.
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
