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
- External exchange reference: an opt-in module consumes `partner-events`, an exchange another service owns (passive verification + bounded startup wait)
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
make loadtest-topology-repair  # Delete both AMQP exchanges under load; repair time + lost 202s (~2.5 min)
make loadtest-all        # Run all tests sequentially (~60 min)
make loadtest-tokens-smoke      # Tokens nested JWE-of-JWS relay (30s); loadtest-tokens for the full run
make loadtest-tokens-mle-smoke  # Tokens MLE relay: bare JWE in the encData envelope (30s); loadtest-tokens-mle for the full run
make loadtest-tokens-vts-smoke  # Tokens VTS Issuer relay: JWS-of-JWE, PS256 (30s); loadtest-tokens-vts for the full run
```

See [wiki/LOAD_TESTING.md](wiki/LOAD_TESTING.md) for running the scripts and the scenarios that need more than their script header; the products scenarios are described in the header of their script under `loadtests/`.

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

The project includes k6 load testing scripts. Each products scenario is described in its script's header under `loadtests/`; [wiki/LOAD_TESTING.md](wiki/LOAD_TESTING.md) covers running them and the scenarios that need more than a header.

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
- **Topology Repair** - Deletes `product-events` and `payment-events` mid-run (destructive, so not in `loadtest-all`); reports the self-repair time and the payments lost in the repair window

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

**go-bricks version:** `go.mod` is pinned to go-bricks `v0.67.0`. There is no
`replace` directive — builds and CI resolve the framework from the module proxy
like any other dependency. The per-environment operator decisions for the
v0.64.0 → v0.67.0 upgrade live in
[wiki/GOBRICKS_V067_UPGRADE.md](wiki/GOBRICKS_V067_UPGRADE.md).

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
release. Promoting a framework change into the demo means bumping that pin with
`go get github.com/gaborage/go-bricks@<commit-or-tag>`, not adding a `replace`.

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
- `GET /_sys/health-debug` - Per-component readiness detail, including the consumer arm (framework debug endpoint at the URL root, off by default and loopback-only; `make demo-consumer-readiness` enables it for the app it boots)

**Scheduler system endpoints** (framework; loopback-only while `scheduler.security.cidrallowlist` is empty):
- `GET /api/v1/_sys/job` - List the registered jobs (the products report job is `test-job`)
- `POST /api/v1/_sys/job/:jobId` - Trigger a job now (202 Accepted); `make advisory-lock-demo` fires `test-job` on two replicas at once

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
- `POST /api/v1/tokens/mle-relay` - Plaintext entry that drives the outbound bare-JWE + Visa MLE envelope transport against the MLE peer simulator
- `POST /api/v1/__sim/peer/mle` - In-process MLE peer simulator (bare-JWE, manual `jose.Open`; demo-only)
- `POST /api/v1/tokens/vts-issuer-relay` - Plaintext entry that drives the outbound JWS-of-JWE (VTS Issuer) transport; its peer simulator is the relay client's in-process base transport, so it has no `/__sim/` route

**Payments module** (sealed AMQP messages demo):
- `POST /api/v1/payments/authorize` - Authorize a payment; publishes a sealed `payment.authorized` event (202 Accepted; the response carries `cardLast4`, never the PAN)

**Activity module** (RabbitMQ super-stream demo):
- `GET /api/v1/products/activity` - Projection built by the stream consumer: per-product event counts, per-partition delivery counts, a ring of the last 50 events (each carrying the `product-activity-N` partition it arrived on), and `publisherReady` — the v0.64.0 `streams.Publisher.Ready()` snapshot for the module's publisher handle
- `POST /api/v1/__sim/streams/poison` - Publishes malformed bytes through the same publisher handle so they land on a partition; the typed consumer skips them and keeps going (demo-only, like the tokens peer simulator)

**Partner feed module** (external exchange demo, off by default):
- No HTTP routes. Its only surface is a typed consumer on `partnerfeed.stock.updated`, bound to `partner-events` — an exchange another service owns. See [External Exchanges](#external-exchanges-consuming-from-an-exchange-another-service-owns).

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
  The same annotation is required on every raw-SQL door the framework lists — `f.Raw`, `jf.Raw`, `database.Raw`, a string `Having(...)`, and (framework convention as of go-bricks v0.65.0, #1616) every `qb.Expr` / `qb.MustExpr` SQL body. The compiler does not enforce it; review does.
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
**Exchange:** `product-events` (topic, durable) declared with `decls.DeclareTopicExchange` in products module's `DeclareMessaging()`

### Scheduled Job Under an Advisory Lock (Database Session)

The products report job ([internal/modules/products/job/report_job.go](internal/modules/products/job/report_job.go)) demonstrates the **database Session door** (go-bricks v0.65.0, ADR-112): `db.Session(ctx)` returns a handle pinned to ONE physical connection, for session-scoped state a pool would silently lose. Here that state is a PostgreSQL advisory lock that elects one runner across replicas. The scheduler only stops the SAME job overlapping inside one process, and every replica ticks on its own.

```go
sess, err := db.Session(ctx) // db is ctx.DB(), the job's context-aware handle
defer sess.Close()           // deferred first, so it runs LAST
var acquired bool
err = sess.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", ReportLockKey).Scan(&acquired)
if !acquired {
    log.Info().Msg("Report job skipped: another replica holds the lock")
    return nil // a skip is not a failure
}
defer releaseReportLock(ctx, sess) // pg_advisory_unlock, runs BEFORE Close
log.Info().Msg("Report job lock acquired")
return j.generate(ctx)
```

- **Lock, work and unlock share one Session.** Through the pool, the unlock can run on another backend and release nothing, while both statements still succeed.
- **Unlock before Close, on a detached context.** `Close` returns the connection to the pool without ending the backend. A lock left held would ride along on that pooled connection, and every replica would skip the report until the connection died. The unlock is registered as soon as the lock is held and runs on `context.WithoutCancel(ctx)` bounded to 5s, so a shutdown mid-report still releases it.
- **Non-blocking on purpose.** `pg_try_advisory_lock`, not `pg_advisory_lock`: the replica that loses skips this tick instead of queueing a second report.
- **A Session holds one pool connection for the whole run** (25 per pool by default), so acquire it late and release it early. A lock that only has to span one transaction should use `pg_advisory_xact_lock` on an ordinary transaction, with no Session.
- **The key is database-wide.** `ReportLockKey` (`0x52505254`, ASCII "RPRT") shares one bigint namespace with every client of the database.

**Testing:** `dbtest.TestDB.ExpectSession()` queues a strict `TestSession` with its own query expectations, and `dbtest.AssertSessionClosed` checks the release. `job/report_job_test.go` also asserts that the pool saw no statement and that the unlock ran on a live context after cancellation.

**Proof:** `make advisory-lock-demo` runs [scripts/advisory-lock-demo.sh](scripts/advisory-lock-demo.sh). It starts two extra replicas on `REPLICA_PORTS` (default `8081 8082`) with `custom.products.report.hold` set (env `CUSTOM_PRODUCTS_REPORT_HOLD`, script `HOLD`, default `8s`), so the winner keeps the lock long enough for the loser to find it held. It then fires `POST /api/v1/_sys/job/test-job` at both replicas at once and waits for their first scheduled tick. Each time, exactly one replica logs `Report job lock acquired` and the other logs `Report job skipped: another replica holds the lock`, while `pg_locks` shows one holder — and none once the winner logs `Report job lock released`, checked while both replicas are still up. `GET /api/v1/_sys/job` (list) and `POST /api/v1/_sys/job/:jobId` (manual trigger) are the scheduler's system endpoints, loopback-only while `scheduler.security.cidrallowlist` is empty. The hold is a demo knob and `make run` leaves it unset.

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

**Bare-JWE / Visa MLE (v0.64.0, ADR-107 + #1585):** the MLE relay endpoint exercises the second seal mode — `jose.Policy{Mode: jose.SealModeBareJWE}` is encrypt-only `JWE(payload)` (no inner JWS), paired with `httpclient.VisaMLEEnvelope()` on `JOSEConfig.Envelope`, which wraps the compact as `{"encData":"<compact>"}` `application/json` on the wire and unwraps inbound responses by shape. Bare mode admits `A128GCM` (via a direct `go-jose/v4` import — no go-bricks alias) and stamps `iat` in milliseconds when `IATMillis: true`. Two invariants shape the demo: bare mode does **not** authenticate the sender (no signature — production pairs it with mTLS / X-Pay-Token), and there is no `mode` key in the `jose:` struct-tag grammar, so a server route cannot select bare mode — the MLE peer simulator binds the envelope as plain JSON and opens/seals manually with `jose.Open`/`jose.Seal`. See [internal/modules/tokens/service/mle_relay_service.go](internal/modules/tokens/service/mle_relay_service.go).

**JWS-of-JWE / VTS Issuer (v0.65.0, ADR-111 + #1610/#1623):** `POST /api/v1/tokens/vts-issuer-relay` exercises the third seal mode — `jose.Policy{Mode: jose.SealModeJWSofJWE}` encrypts first and signs the compact JWE: an outer JWS (`PS256`, `typ: JOSE`, `cty: JWE`, `iat` in seconds, fixed by the mode) over the inner JWE bare mode builds (`A256GCM`, `Policy.Typ`, millisecond `iat` under `IATMillis`, no `cty` even though `WithJOSE` fills `Policy.Cty`). No `Envelope`: the compact is the body, `application/jose`, both ways. Three rules shape the code:
- **`SigAlg: josev4.PS256` is explicit on both policies.** Visa requires PS256; `httpclient.Builder.Build` fills an unset `SigAlg` with `jose.DefaultSigAlg` (RS256), so omitting it builds and seals and is rejected only by the partner. Inbound, `SigAlg` is a pin, not an allowlist: `Open` refuses any other outer `alg` (`JOSE_ALGORITHM_DISALLOWED`) before touching a key.
- **Verify before decrypt.** `Open` refuses a non-3-segment body (`JOSE_OUTER_NOT_JWS` — the nested and bare shapes are poison here, never a fallback), a bad signature, or an outer header without `cty: JWE` before the private key is used.
- **Key separation.** An inner JWE lifted out of a signed body decrypts on a bare-JWE route that shares its decrypt kid, where nothing authenticates the sender. The demo reuses `tokens-our`/`tokens-peer` across all three modes and is saved only by the MLE policies' `A128GCM` pin (this mode's inner JWE is `A256GCM`); `TestVTSIssuerInnerJWERefusedOnBareRoute` pins that. Production gives each mode its own kids.

**Why the VTS Issuer simulator is a transport, not a `/__sim/` route:** a typed go-bricks route (the only kind that takes `server.WithTags("simulator")`) always JSON-encodes its result, and the raw door `RouteRegistrar.Add` could answer `application/jose` but takes no route options, so its descriptor carries no tags; no `jose:` tag selects this mode either. Rather than wrap the compact in a JSON envelope Visa does not send, `service.VTSIssuerPeerSimulator` implements `http.RoundTripper` and is passed to `WithTransport` — the base slot below `JOSETransport` that production fills with its mTLS transport. Seal, retry loop, peer-labelled metrics, verify-then-decrypt and the plaintext-2xx refusal all run unchanged; only the dial is replaced. The relay addresses `http://vts-issuer-peer-sim.invalid/tokens` (RFC 6761: never resolves), so a client that lost that transport fails at DNS rather than reaching a real host. See [internal/modules/tokens/service/vts_issuer_relay_service.go](internal/modules/tokens/service/vts_issuer_relay_service.go).

**Helper CLI:** request bodies for `curl` come from the framework's `seal-payload` CLI (go-bricks v0.65.0, #1615/#1620). It replaced the demo's own `cmd/seal-payload`, which could only do nested mode. Both targets read JSON on stdin and print only the sealed body, so it pipes into `curl --data-binary @-`:

- `make seal-payload` plays the peer for `POST /api/v1/tokens`. It is a nested JWE-of-JWS that signs with `certs/tokens_peer_private.der` (`-sign-kid tokens-peer`, the route's `verify=`) and encrypts to `certs/tokens_our_public.der` (`-encrypt-kid tokens-our`, its `decrypt=`).
- `make seal-mle` mints a Visa MLE body for `POST /api/v1/__sim/peer/mle`: `-mode bare -enc A128GCM -typ JOSE -iat-ms -envelope visa-mle`, encrypted to `certs/tokens_peer_public.der` (`-encrypt-kid tokens-peer`), which is the key the MLE peer simulator opens with. Nothing is signed. This is the same header shape as `NewMLEOutboundPolicy`.

[scripts/seal-payload.sh](scripts/seal-payload.sh) runs with `GOWORK=off` and resolves the CLI version with `go list -m` from `go.mod`, so there is no second pin to drift. The script repeats the module's kids, so a kid rename has to touch it too (the server reports drift as `JOSE_KID_UNKNOWN`). The CLI only seals and never opens a reply; the relay endpoints are what decrypt.

```bash
printf '%s' '{"pan":"4111111111111111"}' | make seal-mle | \
  curl -s -X POST http://localhost:8080/api/v1/__sim/peer/mle \
       -H 'Content-Type: application/json' --data-binary @-
```

**PAN-bearing requests in logs (go-bricks v0.65.0, ADR-110):** `TokenizeRequest`, `PeerSimRequest`, `RelayRequest` and `MLERelayRequest` implement `logger.Redactor` with a value receiver, so a filtered logger handed the whole struct — the decrypted JOSE request or a plaintext relay body — renders `{"last4":"…"}` and never the PAN. Same rules as the payments card (see [Sealed Messages](#sealed-messages-jwe-of-jws-on-amqp)): it backs up `log.sensitivefields`, it is consulted only at `Interface`/`WithFields`, and it does not make logging request bodies acceptable. It cannot reach bytes that were already marshaled, so the relays' outbound `httpclient` body preview stays covered by the `pan` needle. [internal/modules/tokens/handlers/pan_redaction_test.go](internal/modules/tokens/handlers/pan_redaction_test.go) pins all four types.

**Reference:** [go-bricks v0.67.0 llms.txt](https://github.com/gaborage/go-bricks/blob/v0.67.0/llms.txt) JOSE section for the full API surface, error-code table, and security invariants.

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

**Proof:** `make show-sealed-message` publishes one payment, then reads the message off the consumerless `payments.authorized.tap` queue via the RabbitMQ management API and prints the raw body, its decoded JOSE headers and the still-clear routing fields — asserting the PAN appears nowhere on the wire. It then opens the same bytes with `open-event` and the consumer half of the keys (see below), asserting that the verified envelope and clear fields match the decoded wire view and that the card renders as `"<redacted>"`. See [scripts/show-sealed-message.sh](scripts/show-sealed-message.sh).

**Card data in logs (go-bricks v0.65.0, ADR-110):** `domain.CardDetails` implements `logger.Redactor` with a value receiver. `RedactedForLog()` returns `{last4}` only, the one card fragment `Last4` allows in a log line, so a whole `CardDetails`, `PaymentAuthorized` or `service.AuthorizeRequest` handed to a filtered logger's `Interface` or `WithFields` renders that shape, never the PAN, the expiry or the holder's name. The HTTP body's `handlers.CardRequest` renders the same view by delegating to it, which also covers a whole `AuthorizePaymentRequest`: the `pan` needle alone would mask the PAN there but leave the expiry and the holder in clear. This hardens the masking already in place and replaces none of it: the consumer still logs `cardLast4` explicitly, and `log.sensitivefields: [pan]` still masks any field named `pan`. The hook is not consulted at `Err`, through `Msgf` or by an unfiltered logger. Its result is filtered by the needle list again, so its keys must never contain `pan`. [internal/modules/payments/domain/card_redaction_test.go](internal/modules/payments/domain/card_redaction_test.go) and [internal/modules/payments/handlers/card_redaction_test.go](internal/modules/payments/handlers/card_redaction_test.go) log each struct through the app's filter and through the framework default (which has no `pan` needle), and assert that no digit run longer than four reaches the sink.

**Minting sealed events outside the app:** `make seal-event-demo` runs
[scripts/seal-event-demo.sh](scripts/seal-event-demo.sh), which uses the
framework's `seal-event` CLI (v0.63.0, #1417) to build event bodies in the shell
from `certs/payments_sign_v1_private.der` (sign PRIVATE) plus
`certs/payments_encrypt_v1_public.der` (encrypt PUBLIC) — the producer half of
both families — and publishes them to `payment-events` / `payment.authorized`
through the RabbitMQ management API. It demonstrates the three consumer-side
behaviors the in-app `POST /payments/authorize` flow cannot show: an
externally-minted body is opened (acceptance is key material plus declaration
agreement, never process identity); republishing the SAME bytes trips inbox
dedup on the stable `<sign family>:<jti>` key (every HTTP call mints a fresh
`jti`, so two calls never collide — only a replayed body does); and a body sealed
with a wrong `-event-type` is refused at open-rule 7 with
`SEAL_EVENT_TYPE_MISMATCH` and parks on `payments.authorized.dlq`. The broker
records only the `x-death` rejection. The `SEAL_*` code lives in the app log, as
a `*messaging.PayloadError` at stage `open`. The script's step 5 also reads it
back off the parked bytes with `open-event` (below): exit `3` plus
`SEAL_EVENT_TYPE_MISMATCH` under the consumer's declared type. A control run
under the sealed type opens cleanly, which shows that rule 7 alone refused it.

```bash
printf '%s' "$DOCUMENT" | go run github.com/gaborage/go-bricks/cmd/seal-event@v0.67.0 \
  -sign-key-file certs/payments_sign_v1_private.der \
  -encrypt-key-file certs/payments_encrypt_v1_public.der \
  -sign-kid payments-sign-v1 -encrypt-kid payments-encrypt-v1 \
  -subject card -event-type payment.authorized
```

`-tenant-id` is omitted on purpose: `multitenant.enabled` is false here, so the
signed `tid` carries no rule and is only surfaced on the envelope.

**Opening sealed events outside the app (v0.65.0, #1640 + #1633):** the
framework's `open-event` CLI mirrors `seal-event`. It verifies and decrypts one
body through `sealed.OpenDocument`, the type-free door that runs the typed
consume door's open rules in the same order with the same `SEAL_*` codes. It
takes the **consumer** half: `certs/payments_sign_v1_public.der` (sign PUBLIC) and
`certs/payments_encrypt_v1_private.der` (encrypt PRIVATE). Both scripts drive it
through [scripts/lib/open-event.sh](scripts/lib/open-event.sh):

- **Install, never `go run`.** `install_open_event` runs
  `GOBIN=<scratch> go install …/cmd/open-event@${SEAL_EVENT_VERSION}`. `go run`
  collapses every non-zero exit of its child into its own `1`, and the scripts
  assert the real codes: `0` opened, `1` tool error, `2` usage, `3` refused.
- **Never `-print-subject`.** It prints the decrypted card, PAN included, and is
  a fixture-only hatch. `open_event` refuses the flag in every spelling. The
  default renders the subject member as the fixed literal `"<redacted>"`, with no
  length hint. Under `-json` it travels HTML-escaped as `"\u003credacted\u003e"`,
  which `jq` decodes to `<redacted>`, so compare the decoded value, never the raw
  bytes. `assert_redacted` greps the
  captured stdout and stderr for the PAN before either is printed.
- **Kids are declared, not peeked.** `-sign-kid` and `-encrypt-kid` are required
  flags, never read from the unauthenticated header. After a rotation,
  show-sealed-message takes `OPEN_SIGN_KID` / `OPEN_ENCRYPT_KID` and derives the
  key file from the keystore's DER naming.
- **`-tenancy disabled`** is passed explicitly: `multitenant.enabled` is false.
- **The seal-event demo matches the parked message by bytes.** The DLQ is durable
  and accumulates across runs, so the script peeks up to `DLQ_PEEK_MAX` messages
  (`ack_requeue_true`) and opens the one that is byte-identical to the body it
  minted, not the head.
- **Rule 9 can be reached from the consumer side.** open-event with
  `-subject amount` refuses a valid body with `SEAL_MANIFEST_MISMATCH`, which is
  consumer declaration drift. seal-event cannot mint that case.

```bash
open-event \
  -sign-key-file certs/payments_sign_v1_public.der \
  -encrypt-key-file certs/payments_encrypt_v1_private.der \
  -sign-kid payments-sign-v1 -encrypt-kid payments-encrypt-v1 \
  -subject card -event-type payment.authorized -tenancy disabled -json < body.txt
```

**Reference:** framework [wiki/sealing.md](https://github.com/gaborage/go-bricks/blob/v0.67.0/wiki/sealing.md) (its "Minting test events" and "Inspecting sealed events" sections cover the two CLIs) and [ADR-097](https://github.com/gaborage/go-bricks/blob/v0.67.0/wiki/adr_097_sealed_amqp_messages.md) for the envelope table, the opener's rule order and error codes, the tenancy rules, and the rotation runbooks.

### Topology Self-Repair (exchange loss, go-bricks v0.67.0)

As of go-bricks v0.67.0 (#1776/#1779, ADR-113 amendment) an AMQP exchange deleted under a live app **heals itself**. Every pooled publisher drives the topology redeclare pass, not only the consumer: the first publish into the hole takes the broker's 404 on the publisher's channel, the client opens a replacement channel, and that channel wakes the registry, which re-declares every exchange, queue and binding over its own connection (INFO `Messaging topology redeclared on new channel`). The publish retries on the new channel. Before v0.67.0 nothing triggered that pass, and every later publish failed with `ErrPublishRetriesExhausted` until a restart. Both publishing paths here share the single-tenant pooled publisher, so a payment or an outbox drain of a product write repairs `payment-events` and `product-events` alike.

- **The streams lane does not self-repair.** `product-activity` (port 5552) is declared at startup only; a deleted stream, or a broker wipe, needs an app restart.
- **The repair has an ack-and-drop window.** The pass declares exchanges, then queues, then bindings, and the typed payments publisher sets no `Mandatory` flag (the framework has no returned-message handler). A publish landing after `payment-events` is back but before `payments.authorized` / `payments.authorized.tap` are re-bound is broker-acked and dropped as unroutable, while the caller already got **202**. Treat a deleted exchange as an incident and reconcile the payments authorized during the repair.
- **`product-events` has no bound queue** in this demo, so its repair proves the relay's publishes are confirmed again, not delivered.

**See it:** `make redeclare-demo` ([scripts/topology-repair-demo.sh](scripts/topology-repair-demo.sh)) publishes a payment, deletes `payment-events` through the management API, publishes into the hole, and shows the exchange and both bindings back and a post-repair payment on the tap; then it deletes `product-events` and lets the outbox relay repair it. The payment body carries a documented test PAN and is never echoed. **Measure it:** `make loadtest-topology-repair` ([loadtests/topology-repair.ts](loadtests/topology-repair.ts), see [wiki/LOAD_TESTING.md](wiki/LOAD_TESTING.md)) deletes both exchanges under constant-arrival traffic and reports "Lost in repair window" (202s that never reached the tap) as a number, never a failed threshold. Both honour `APP_URL` and `RABBIT_MGMT` / `RABBIT_USER` / `RABBIT_PASS`.

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

### Consumer-Aware Readiness (`messaging.consumers.critical`)

Before go-bricks v0.65.0, `/ready` judged the messaging kind by its **publisher** alone, so a service whose AMQP consumer had silently detached stayed in rotation while its queue filled up. v0.65.0 tracks every declared consumer's subscription (#1684), and the opt-in key `messaging.consumers.critical` (#1686, ADR-114) lets that state fail readiness:

- **Consumer arm:** once a declared AMQP consumer (here only `payments.authorized`) is unsubscribed **and** its supervisor has failed **5** re-subscribes in a row, `/ready` answers 503. Five is the framework constant at which the `Consumer re-subscribe attempt failed` log turns WARN, and it is not configurable. A reconnect that recovers inside the streak never reaches the verdict.
- **Publisher arm:** the existing "is the leased publisher ready?" check becomes critical too, so a broker outage answers 503 **at once**, with no streak. That is why the key is absent in [config.development.yaml](config.development.yaml), with only a commented example: turning it on is a per-environment decision (env `MESSAGING_CONSUMERS_CRITICAL=true`).
- **Not covered:** stream consumers (the activity projection). The streams kind is never critical.

Where to watch it:

| View | What it shows |
|------|---------------|
| `GET /api/v1/ready` → 200 | `messaging_stats.declared_consumers`, `subscribed_consumers`, `consumer_max_fail_streak` (worst current streak, `0` when healthy), `consumer_resubscribes` (cumulative successes) and `consumer_registries`. Bare numbers only: never which consumer. |
| `GET /api/v1/ready` → 503 | The fixed body `{"status":"not ready","messaging":"unhealthy","error":"messaging unavailable"}`. It carries no stats and no queue name (ADR-048), so the streak is not visible here once the verdict flips. |
| `GET /_sys/health-debug` | This view is off by default. It is served at the URL root, not under `/api/v1`, and is access-controlled (`debug.allowedips` defaults to loopback). `data.components.messaging` shows `critical`, the same counters under `details`, and the arm that failed: `error` is `consumer re-subscribe exhausted` or `publisher not ready`. |

**Proof:** `make demo-consumer-readiness` runs [scripts/consumer-readiness-demo.sh](scripts/consumer-readiness-demo.sh). The script:

1. Builds and boots its **own** app with `MESSAGING_CONSUMERS_CRITICAL=true`, plus `/_sys/health-debug` on loopback only. Stop `make run` first: the script refuses a busy port.
2. Records the app user's vhost permissions with `rabbitmqctl list_user_permissions`.
3. Revokes **read on `payments.authorized` only**, with the read regex `^(?!payments\.authorized$).*`. Configure and write are unchanged, and every other queue and stream stays readable.
4. Closes the consumer's own AMQP connection through the management API. The broker checks permissions at subscribe time, not per delivery.
5. Polls `/ready` while `consumer_max_fail_streak` climbs. Once it hits 5, `/ready` answers 503 and the debug view names the consumer arm.
6. Restores the **exact** recorded permissions and shows the recovery: `subscribed_consumers` back to `declared_consumers`, `consumer_resubscribes` +1, `/ready` 200.

A trap restores the permissions on every exit, including Ctrl-C. The exact restore command is printed before anything changes, in case of a SIGKILL. The demo never stops the broker: the publisher arm would flip `/ready` at once and hide the consumer arm, and every product write would stall on its streams publish for up to 2s.

**Operating it:** gate **liveness** on `/health` (static), never on `/ready`, or a broker incident becomes a restart loop. Read ADR-114's threat note before enabling the key. Anyone who can make a consumer's re-subscribe fail five times running can take every replica out of the load balancer at once, for example by revoking consume, deleting the queue, or causing a `PRECONDITION_FAILED` that is skipped until restart.

**Reference:** framework [ADR-114](https://github.com/gaborage/go-bricks/blob/v0.67.0/wiki/adr_114_critical_consumer_readiness.md) and [wiki/messaging.md](https://github.com/gaborage/go-bricks/blob/v0.67.0/wiki/messaging.md).

### External Exchanges (consuming from an exchange another service owns)

The partnerfeed module ([internal/modules/partnerfeed/](internal/modules/partnerfeed/)) demonstrates the **single-declarer pattern** (go-bricks v0.67.0, #1773/#1774, ADR-119): one service owns an exchange and declares its shape, and every other service only binds to it or publishes through it. `partner-events` belongs to a partner service outside this repository, so the module *references* it instead of declaring it. Every other exchange in the demo is declared by the module that uses it.

```go
func (m *Module) DeclareMessaging(decls *messaging.Declarations) {
    if !m.enabled { // custom.partnerfeed.enabled — off by default
        return
    }
    // Name only: verified with a passive exchange.declare, never created.
    partner := decls.DeclareExternalExchange("partner-events")
    // The queue, its DLQ pair and the binding are this service's own.
    queue := decls.DeclareQueueWithDLQ("partnerfeed.stock.updated",
        &messaging.DeadLetterSpec{QueueType: messaging.QueueTypeQuorum})
    decls.DeclareBinding(queue.Name, partner.Name, "partner.stock.updated") // exact key, not a pattern
    messaging.DeclareTypedConsumer(decls, &messaging.ConsumerOptions{
        Queue: queue.Name, Consumer: "partnerfeed-stock-updated",
        EventType: "partner.stock.updated", Workers: 1,
    }, m.onStockUpdated) // func(ctx, domain.StockUpdated) error — decoded + validated first
}
```

**Semantics that shape the module:**
- **Verified on every declare pass, never created.** The startup pass and each ADR-113 redeclare pass on a new channel issue `exchange.declare` with `passive=true`: the broker answers declare-ok or 404. The owner's later declare of its real shape still succeeds, because this service never sends one. The startup log line is `External exchange verified` (INFO, `exchange=partner-events`).
- **Existence only.** A passive declare cannot see the owner's type or durability, so nothing checks them. That is why the binding uses an exact routing key, which routes the same through a direct or a topic exchange; a wildcard would silently match nothing on a direct one.
- **Name only.** `DeclareExternalExchange` takes no type, flags or `Args`, and `Validate()` refuses an external declaration that carries any of them.
- **No `configure` permission needed.** A passive declare creates nothing, so in production the broker user can be scoped to the entities the service really owns. The demo keeps the default dev user and does not show this.
- **One owner per declaration set.** All modules share one set, so a name that one module declares and another marks external fails startup with `declared locally and marked external in the same declaration set`. That is why the demo uses a new name rather than `product-events` or `payment-events`.
- **A missing exchange aborts startup.** The module declares a consumer, so the passive declare's 404 is fatal: `failed to declare exchange partner-events: Exception (404) Reason: "NOT_FOUND - no exchange 'partner-events' in vhost '/'"`. A publisher-only service would warn and continue instead.
- **`messaging.declare.externalwait` makes the abort wait** (#1774, default `0`, env `MESSAGING_DECLARE_EXTERNALWAIT`). On a 404 the startup declare pass re-runs with backoff (first gap `min(1s, externalwait/4)`, doubling to 5s) until the owner creates the exchange or the budget runs out. It logs one WARN, `Broker answered 404, re-running the startup declare pass until it succeeds or externalwait elapses`. It engages only for a service that declared consumers, only on a 404, and only on the control-plane startup pass. Two costs: a **mistyped** external name also spends the whole budget before failing, and the HTTP listener starts only after this pass, so a `startupProbe` must allow the first attempt, plus `externalwait`, plus one final attempt. The demo config carries it commented out.
- **At-least-once, one worker.** The handler only logs, so a redelivery is harmless; a handler that writes state would dedup first (for example `inbox.ProcessOnce` on the partner's `eventId`, through `DeclareTypedConsumerWithMeta`). `Workers: 1` keeps one SKU's stock updates in broker order, because a stock level is last-write-wins.
- **The partner's contract is validated at the boundary.** `domain.StockUpdated` carries `validate` tags. A body that fails decode or validation is nacked without requeue and parks on `partnerfeed.stock.updated.dlq`. `Quantity` is a `*int`, so a missing field is refused rather than read as zero stock.

**Why it is off by default:** no partner service runs locally. Switched on with the exchange absent, plain `make run` would abort on the 404. Switch it on with `CUSTOM_PARTNERFEED_ENABLED=true` (or `custom.partnerfeed.enabled: true`) only where something owns `partner-events`. The module is always registered in [cmd/api/main.go](cmd/api/main.go) and reads the switch in `Init`, because `main` never sees the loaded config (`app.App` exposes no accessor).

**Proof:** `make external-exchange-demo` runs [scripts/external-exchange-demo.sh](scripts/external-exchange-demo.sh). It starts its own app instance with the module on and plays the partner through the RabbitMQ management API, in three steps:
1. With the exchange absent and `externalwait` at 0, startup fails fast on the broker's 404.
2. With `externalwait` at 60s, the app logs the WARN and keeps its listener down. The script creates the exchange, and startup completes with `External exchange verified`, without a restart.
3. A `partner.stock.updated` event published to the exchange reaches the consumer.

Cleanup deletes the exchange, plus the queue, DLQ and DLX when the run created them. The script refuses to run while anything listens on the `APP_URL` port, so stop `make run` first. It honors `APP_URL` and `RABBIT_MGMT`.

**Reference:** framework [wiki/messaging.md](https://github.com/gaborage/go-bricks/blob/v0.67.0/wiki/messaging.md#external-exchanges) ("External exchanges" and "Startup wait"), [ADR-119](https://github.com/gaborage/go-bricks/blob/v0.67.0/wiki/adr_119_external_exchange_passive_verification.md), and [wiki/startup_defaults.md](https://github.com/gaborage/go-bricks/blob/v0.67.0/wiki/startup_defaults.md) for the probe sizing.

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
// SECURITY: Manual SQL review completed - constant aggregate, no caller input
qb.Select(qb.MustExpr("COUNT(*)"))           // SAFE
// SECURITY: Manual SQL review completed - constant aggregate over a fixed column, no caller input
qb.Select(qb.MustExpr("AVG(price)", "avg"))  // SAFE — expression + alias
qb.OrderBy("created_date DESC")              // SAFE — bounded direction is in the grammar
```

`Expr`/`MustExpr` carry SQL verbatim and are NOT escaped — never interpolate user
input into them, and annotate every call site with `// SECURITY: Manual SQL review
completed - <what was verified>` (see [Security Requirements](#security-requirements)).
`cols.As(alias)` is the one door that **panics** (at the `As` call, with
`*dbtypes.InvalidAliasError`) rather than deferring to `ToSQL()`.

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
   - `job/report_job.go` — the scheduled report under a PostgreSQL advisory lock on a pinned `db.Session` (see [Scheduled Job Under an Advisory Lock](#scheduled-job-under-an-advisory-lock-database-session)); `make advisory-lock-demo` ([scripts/advisory-lock-demo.sh](scripts/advisory-lock-demo.sh)) races two replicas for it

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
   - Every PAN-bearing request struct implements `logger.Redactor` (value receiver), so a filtered logger handed one whole renders only `{"last4":"…"}`; `handlers/pan_redaction_test.go` pins all four
   - `service/relay_service.go` wires `httpclient.WithJOSE(...)` for the outbound `JOSETransport`
   - In-process peer simulator with the inverse policy makes the demo self-contained
   - `service/mle_relay_service.go` and `service/vts_issuer_relay_service.go` are the other two seal modes (bare JWE behind `VisaMLEEnvelope`; JWS-of-JWE with an explicit `PS256`); `service/vts_issuer_peer_simulator.go` is the one simulator wired as an `http.RoundTripper` via `WithTransport` instead of a `/__sim/` route
   - `make seal-payload` / `make seal-mle` ([scripts/seal-payload.sh](scripts/seal-payload.sh)) mint nested JWE-of-JWS and Visa MLE bodies for `curl` with the framework's `seal-payload` CLI, at the go-bricks version in `go.mod`

9. **[internal/modules/payments/](internal/modules/payments/)** - Payments module (sealed AMQP messages demo)
   - `domain/payment.go` declares the `seal:`-tagged event: one `seal:"subject"` field encrypted, the rest clear
   - `domain.CardDetails` and the HTTP body's `handlers.CardRequest` implement `logger.Redactor`, so a card (or the event or request that holds it) handed to a filtered logger renders `{last4}` and never the PAN, expiry or holder
   - `module.go` shows the classic typed lane — `DeclareTypedPublisher` + `DeclareTypedConsumerWithMeta`, the quorum DLQ pair (explicit `DeadLetterSpec.QueueType`; quorum is the v0.64.0 default, see Troubleshooting for the retained-volume trap) and the consumerless `payments.authorized.tap` queue
   - `service/service.go` mints the order id and publishes once behind `messaging.EventPublisher[T]`; `module.go`'s handler dedups the delivery through `inbox.ProcessOnce` on `Meta.DedupKey()`
   - `make show-sealed-message` proves the PAN never reaches the broker, then opens the same bytes with `open-event` (consumer keys, card still `"<redacted>"`)
   - `make demo-consumer-readiness` ([scripts/consumer-readiness-demo.sh](scripts/consumer-readiness-demo.sh)) stalls this module's `payments.authorized` consumer until `/ready` fails closed (see [Consumer-Aware Readiness](#consumer-aware-readiness-messagingconsumerscritical))
   - `make redeclare-demo` ([scripts/topology-repair-demo.sh](scripts/topology-repair-demo.sh)) deletes `payment-events` under the live app and watches the next publish repair it; `make loadtest-topology-repair` ([loadtests/topology-repair.ts](loadtests/topology-repair.ts)) measures the same under load (see [Topology Self-Repair](#topology-self-repair-exchange-loss-go-bricks-v0670))

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

12. **[wiki/MULTI_TENANT_MIGRATION_DEMO.md](wiki/MULTI_TENANT_MIGRATION_DEMO.md)** - Multi-tenant migration tooling (schema-per-tenant via `go-bricks-migrate`)
    - `make migrate-multitenant-verdict` ([scripts/migrate-verdict-demo.sh](scripts/migrate-verdict-demo.sh)) runs `go-bricks-migrate validate --json` three ways and prints exit codes 0 / 2 / 1 beside the `clean` / `nothing_attempted` / `fleet_split` summary records (ADR-115); read-only, nothing is migrated
    - `make migrate-multitenant-check-roles` builds [cmd/check-tenant-roles](cmd/check-tenant-roles/main.go), which logs in as each tenant's own role (read-only) and calls `migration.CheckPGRoleFloor`: exit 0 when every role sits at the floor, 1 when one holds an attribute above it or cannot be checked, 2 when nothing was checked. It dials `PG_HOST`/`PG_PORT`, so point those at the demo Postgres first

13. **[internal/modules/partnerfeed/](internal/modules/partnerfeed/)** - Partner feed module (external exchange demo, off by default)
    - `module.go` reads `custom.partnerfeed.enabled` in `Init` and, when on, calls `DeclareExternalExchange("partner-events")`: a name-only reference that every declare pass verifies passively and never creates (ADR-119). The queue, its quorum DLQ pair and the binding are declared normally.
    - `domain/stock.go` declares `StockUpdated`, the partner's `validate`-tagged contract, and the topology names. Its trap comment explains why no module in this process may also declare `partner-events`.
    - The typed consumer runs one worker, which keeps a SKU's updates in order, and only logs, so a redelivery is harmless.
    - [config.development.yaml](config.development.yaml) carries the switch and `messaging.declare.externalwait` commented out, with the startupProbe sizing caveat.
    - `make external-exchange-demo` shows the broker 404 abort, the `messaging.declare.externalwait` wait, and consumption.

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
# Fix: wrap the expression in the declared hatch, and annotate the call site
# with the `// SECURITY: Manual SQL review completed - ...` comment (#1616).
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

### DLQ PRECONDITION_FAILED on Retained Broker Volume (go-bricks v0.64.0)

```bash
# Symptom: startup fails declaring payments.authorized / payments.authorized.dlq
# with PRECONDITION_FAILED (inequivalent arg 'x-queue-type').
# As of go-bricks v0.64.0 (ADR-106), DeclareQueueWithDLQ resolves an empty
# DeadLetterSpec.QueueType to QUORUM on both the primary and the parking queue
# (was: broker default, classic). The demo declares this explicitly
# (internal/modules/payments/module.go). RabbitMQ cannot convert a queue type
# in place, so a retained volume holding the old classic queues refuses the
# redeclare.
# Fix: delete both queues (they are demo queues — drain first if you care):
docker exec go-bricks-rabbitmq rabbitmqctl delete_queue payments.authorized
docker exec go-bricks-rabbitmq rabbitmqctl delete_queue payments.authorized.dlq
# then restart the app. Or destroy the volume: make docker-down && make dev
# The same mismatch met at RECONNECT (not startup) is a WARN plus skip-until-restart
# as of v0.65.0 — see "AMQP Topology Re-declared on Reconnect" below.
# Related: the management API reports a quorum queue's `messages` on the ~5s
# stats emission tick — scripts/seal-event-demo.sh polls for DLQ growth instead
# of reading the depth once for exactly this reason.
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

### Sealed Dedup Key Is Typed and Bound to Its Delivery (go-bricks v0.65.0 / v0.66.0)

```bash
# Symptom (compile): cannot use key (variable of struct type messaging.DedupKey)
# as string value. As of go-bricks v0.65.0 (#1630) Metadata.DedupKey() returns,
# and InboxProcessor.ProcessOnce takes, a messaging.DedupKey. Render it with
# key.String(): the persisted gobricks_inbox spelling is unchanged (the wire id,
# or "<sign family>:<jti>" for a sealed key), so no ledger migration.
# messaging.IsSealedDedupKey is gone — use key.Sealed().
# Symptom (runtime, v0.66.0 #1700): a sealed delivery is refused with an error
# wrapping messaging.ErrInvalidEventID —
#   sealed dedup key outside a sealed delivery     (ctx lost the delivery marker)
#   sealed dedup key belongs to another delivery   (a key kept from another delivery)
# — no ledger row is written, the handler's work does not run, and the message
# takes the poison path to the DLQ. The inbox admits a sealed key only under the
# ctx of the delivery that produced it. Rules (payments/module.go follows both):
#   - take the key from THIS delivery's meta.DedupKey(); never cache it
#   - call ProcessOnce with the handler's ctx (or one derived from it), never
#     context.Background() or a detached goroutine
# A replay of the same envelope composes an equal key, so seal-event-demo's
# "same bytes twice" proof is still admitted and then deduplicated by the ledger.
# The payments handler logs dedupKey at INFO on purpose (the framework never
# renders a sealed key itself): it is an identifier, never the PAN or a secret.
```

### JOSE Relay Refuses a Plaintext 2xx (go-bricks v0.65.0)

```bash
# Symptom: POST /api/v1/tokens/relay or /api/v1/tokens/mle-relay fails with an
# error wrapping
#   httpclient: successful response was not JOSE-protected (peer: "...", status: 200)
# and the transport logs one WARN (never the body).
# As of go-bricks v0.65.0 (#1637, ADR-107 amendment) a JOSETransport with an
# Inbound policy refuses a 2xx it did not unwrap: nested mode needs Content-Type
# application/jose; envelope mode (VisaMLEEnvelope) needs a non-empty top-level
# encData member. Non-2xx replies (plaintext error bodies), 204, 304 and HEAD
# still pass through, and the refusal is not retried by WithRetries. Match it
# with errors.Is(err, httpclient.ErrJOSEPlaintextResponse).
# The relays set WithPeerName (#1648), so peer reads "tokens-peer-sim",
# "visa-mle-peer-sim" or (for /tokens/vts-issuer-relay, same rule as nested
# mode) "visa-vts-issuer-peer-sim" — the label their outbound metrics carry.
# The in-process simulators seal every 2xx, so the demo's behavior is unchanged
# — dropping WithRawResponse from the MLE simulator is what would trip it.
# Decision: AllowPlaintextSuccess stays UNSET on every relay; setting it hands
# the caller a body nothing authenticated. A real partner must protect every
# 2xx it answers — fix the partner route, don't set the flag.
```

### AMQP Topology Re-declared on Reconnect (go-bricks v0.65.0)

```bash
# What changed (go-bricks v0.65.0, #1676/#1675, ADR-113): after an AMQP reconnect
# the registry re-runs every exchange/queue/binding declaration once per new
# channel before the consumer re-subscribes, so a broker that lost its topology
# no longer leaves payments.authorized in a silent 404 loop. On success:
#   INFO  Messaging topology redeclared on new channel
# Symptom 1: at reconnect,
#   WARN  Messaging declaration rejected with PRECONDITION_FAILED, skipped until restart ...
# A surviving entity whose arguments no longer match (the classic-vs-quorum DLQ
# pair above, an old unbounded payments.authorized.tap) is skipped for the life
# of the process. Fix the server-side definition and restart. At STARTUP the same
# mismatch still aborts boot.
# Symptom 2: during a broker outage,
#   WARN  Consumer re-subscribe attempt failed, will retry
# from the 5th consecutive failed attempt (attempts 1-4 stay at Debug), with
# amqp_reply_code / amqp_reply_text when the broker refused it. The retry cadence
# is unchanged; the Error Analysis dashboard's log-level panel will show them.
# Not covered: the native streams lane. product-activity (port 5552) is declared
# at startup only, so after a broker wipe the super stream does not come back
# until the app restarts. Fix: once the broker is back, stop `make run` and start
# it again.
```

### `/ready` Key `active_consumers` Renamed (go-bricks v0.65.0)

```bash
# Symptom: a Grafana panel saved in the UI, a New Relic NRQL query or an alert
# reading messaging_stats.active_consumers from GET /api/v1/ready goes flat.
# As of go-bricks v0.65.0 (#1684) that key is gone: it counted tenant consumer
# REGISTRIES, and is now spelled consumer_registries. New beside it:
# declared_consumers, subscribed_consumers, consumer_resubscribes and
# consumer_max_fail_streak.
curl -s http://localhost:8080/api/v1/ready | jq .messaging_stats
# Fix: repoint readers to consumer_registries (the old meaning) or to
# declared_consumers / subscribed_consumers (what the old name suggested).
# Nothing in this repo reads the key. messaging_stats appear only in the 200
# body; a 503 carries the blocking kind's status and a fixed error.
```

### Topology Repair Driven by Publishers (go-bricks v0.67.0)

```bash
# What changed (go-bricks v0.67.0, #1776/#1779, ADR-113 amendment): every pooled
# publisher's new channel now also drives the redeclare pass, not only the
# consumer's. Two visible effects:
# 1. "Messaging topology redeclared on new channel" (INFO) now also appears when
#    the PUBLISHER's channel is replaced: a publish into a deleted exchange, or a
#    dropped publisher connection. It does not appear at the first publish, and
#    usually not at boot: the "" publisher is leased at startup pre-init, before
#    the consumer registry exists, so a first channel that comes up that early
#    finds no topology to replay. It CAN appear once at boot, with
#    channel_generation 1, when that first channel comes up after consumer setup
#    has begun (a slow broker connect); that line is benign. After startup, or
#    with a higher generation, it means a publisher channel was replaced. A
#    publisher the pool creates later (after messaging.publisher.idlettl evicts
#    it) does run one idempotent pass on its first channel.
# 2. An exchange deleted under a live app now heals itself. Before, every later
#    publish to it failed with ErrPublishRetriesExhausted until a restart.
# Caveat, money path: the repair is NOT atomic. The pass runs exchanges, then
# queues, then bindings. A publish that lands after payment-events is back but
# before payments.authorized / payments.authorized.tap are re-bound is
# broker-acked yet unroutable: the typed publisher sets no Mandatory flag, so
# the broker drops it silently. The caller still gets 202 Accepted and that
# payment.authorized event is lost. Treat a deleted exchange as an incident and
# reconcile the payments authorized during the repair window.
# See it: make redeclare-demo. Measure the window under load:
# make loadtest-topology-repair ("Lost in repair window" is reported, not a
# threshold). See "Topology Self-Repair" under Important Patterns.
```

### Multi-Tenant Migrate CLI Exit Codes and Summary (go-bricks v0.67.0)

```bash
# Symptom: a script that read any non-zero go-bricks-migrate exit as "a tenant
# failed", or scraped "N tenants total, M failed", misreads the v0.67.0 CLI
# (#1771/#1770, ADR-115). Rebuild the CLI after the pin moves:
make migrate-multitenant-install   # builds Makefile GO_BRICKS_REF (v0.67.0)
# Exit codes: 0 clean; 1 fleet split (something was attempted and something
# failed or was never attempted); 2 nothing attempted, no schema touched (empty
# or failed tenant listing, unreadable tenant store, credential provider that
# could not be built, half-set migrator identity, or any misuse such as an
# unknown flag or a stray argument).
# Every run prints exactly one summary line, even one that stopped early:
#   Migrate summary: verdict=clean, 3 listed, 3 attempted, 0 failed, 0 not attempted
# --json adds verdict / listed / attempted / failed / not_attempted to the
# summary record. make stops on any non-zero exit, so the migrate-multitenant-*
# targets cannot tell 1 from 2 — read the summary line.
make migrate-multitenant-verdict   # all three exit codes side by side, validate only
# Operator rule: NEVER export GOBRICKS_MIGRATE_MIGRATOR_USER or
# GOBRICKS_MIGRATE_MIGRATOR_PASSWORD. One alone makes every run exit 2. Both
# together make one role run every tenant's DDL, which collapses the per-role
# search_path tenant isolation (wiki/MULTI_TENANT_MIGRATION_DEMO.md).
```

### PostgreSQL and Cache Config Refusals (go-bricks v0.65.0 / v0.66.0)

```bash
# None of these fire on this repo's configs (TCP localhost hosts, no
# connectionstring, no cache). They bite when an environment changes that.
# Symptom: startup (or go-bricks-migrate) refuses a database section for:
# - a PostgreSQL connectionstring that carries service= (even an empty one), or
#   names none while PGSERVICE is set (v0.66.0, #1715). A libpq service file
#   would supply host and TLS out of the config's sight; inline its keys instead.
# - TLS claimed on a unix-socket (absolute-path) host: a database.tls block
#   (v0.65.0, #1613), sslmode/ssl* keys in the connectionstring (#1642), or PGSSL*
#   environment variables beside one (v0.66.0, #1699). pgx skips TLS on a socket,
#   so the claim would be dropped silently. Use a TCP host or drop the claim.
# - a connectionstring or host list that names no host, including an empty
#   comma-separated entry (pgx would fall back to an implicit unix socket).
# Cache (v0.66.0, #1740): with cache.enabled and NO cache.redis.keyprefix, every
# key is namespaced under app.name, so the cache re-keys once on upgrade and
# app.name must be a valid key namespace. Decide keyprefix before enabling a
# cache; an explicit "" opts out of the prefix.
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
