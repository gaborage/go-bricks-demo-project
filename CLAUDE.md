# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is a **go-bricks demo project** demonstrating production-ready patterns for building modular Go applications. It uses the `go-bricks` framework (located at `../go-bricks`) with local replacement via `go.mod`.

**Key characteristics:**
- Framework-based modular architecture
- Multi-tenant capable (currently running in single-tenant mode)
- PostgreSQL + RabbitMQ infrastructure
- REST API with Echo web framework
- OpenTelemetry observability (with known configuration bug - see below)

## Essential Commands

### Development Workflow
```bash
make build          # Build application binary to bin/go-bricks-demo-project
make run            # Build + run (requires services to be running)
make test           # Run all tests with race detector
make check          # Run fmt + lint + test (pre-commit checks)
```

### Docker Infrastructure
```bash
make docker-up      # Start PostgreSQL + RabbitMQ + DataDog Agent
make docker-down    # Stop all services and remove volumes
make status         # Show running service status
make logs           # Follow logs from all services
```

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

## Architecture

### Application Bootstrap

The application uses `go-bricks/app.New()` which handles:
1. **Configuration loading** - Environment-based config from `config.yaml` (see Config System section)
2. **Database manager** - Connection pooling and lifecycle management
3. **Messaging manager** - RabbitMQ client setup
4. **Observability provider** - OpenTelemetry setup (CURRENTLY BROKEN - see Known Issues)
5. **HTTP server** - Echo server with middleware

**Entry point:** `cmd/api/main.go`
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

**Module structure pattern** (see `internal/modules/products/`):
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
- `APP_ENV=development` loads `config.yaml`
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
- See `internal/modules/shared/secrets/` for AWS Secrets Manager tenant config loading

## Known Issues

### CRITICAL: Observability Configuration Bug

**Status:** OpenTelemetry/DataDog integration is currently non-functional due to a go-bricks framework bug.

**Symptom:**
```
WRN Failed to initialize observability, using no-op provider
error="config_invalid: observability.trace unsupported type observability.TraceConfig"
```

**Root cause:**
- `go-bricks/app/bootstrap.go:84` uses `InjectInto()` to load observability config
- `InjectInto()` only supports primitive types, not nested structs
- `observability.Config` has nested `TraceConfig` and `MetricsConfig` structs

**Detailed analysis:** See `GOBRICKS_OBSERVABILITY_BUG_REPORT.md`

**Workaround options:**
1. Fix go-bricks (change `InjectInto` → `Unmarshal` and update struct tags to `mapstructure:`)
2. Use environment variables for all observability settings
3. Accept no-op telemetry for now

**DataDog integration setup** (ready but blocked by bug):
- `docker-compose.yml` has DataDog Agent configured
- `config.yaml` has OTLP endpoints configured
- See `DATADOG_SETUP.md` for complete setup guide

## Testing

### Unit Tests
```bash
go test ./internal/modules/products/...          # Test specific module
go test -v -race ./...                           # All tests with race detector
go test -run TestProductService_Create ./...     # Run specific test
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

3. **Register in `cmd/api/main.go`:**
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
- `observability` - OpenTelemetry provider (currently broken)

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

- `config.yaml` - Base configuration
- `config.development.yaml` - Development overrides (extensive examples/documentation)
- `.env` - Secrets (gitignored, use `.env.example` as template)
- `docker-compose.yml` - Infrastructure services
- `Makefile` - Development commands

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
- Place SQL files in `migrations/` directory
- Use Flyway naming: `V1__description.sql`, `V2__another.sql`
- Run with `make migrate`
