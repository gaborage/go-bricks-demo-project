# Multi-Tenant AWS Secrets Manager Example

This example demonstrates how to build a **production-ready multi-tenant application** using the go-bricks framework with **AWS Secrets Manager** for dynamic tenant configuration. It showcases:

- 🔐 **Dynamic tenant database configuration** from AWS Secrets Manager
- 🏗️ **Intelligent caching** with TTL-based expiration
- 🚀 **Multiple database support** (PostgreSQL & Oracle)
- 🏥 **Comprehensive health checks** for tenant connections
- 🔄 **Automatic connection management** with LRU eviction
- 🧪 **Local development** with LocalStack simulation

## 🏗️ Architecture Overview

```
┌─────────────────┐    ┌──────────────────┐    ┌─────────────────┐
│   HTTP Request  │    │  Tenant Resolver │    │ AWS Secrets Mgr │
│  X-Tenant-ID    │───▶│   (Header-based) │───▶│   Config Store  │
└─────────────────┘    └──────────────────┘    └─────────────────┘
                                │                         │
                                ▼                         ▼
┌─────────────────┐    ┌──────────────────┐    ┌─────────────────┐
│  Tenant Module  │    │   DB Manager     │    │  Cache Layer    │
│   (Endpoints)   │◀───│  (LRU Eviction)  │◀───│   (TTL-based)   │
└─────────────────┘    └──────────────────┘    └─────────────────┘
                                │
                                ▼
┌─────────────────┐    ┌──────────────────┐    ┌─────────────────┐
│   PostgreSQL    │    │      Oracle      │    │   Health Checks │
│   Tenant DBs    │    │    Tenant DB     │    │   & Monitoring  │
└─────────────────┘    └──────────────────┘    └─────────────────┘
```

## 🚀 Quick Start

### Prerequisites

- **Go 1.24+**
- **Docker & Docker Compose**
- **curl** for testing
- **jq** (optional, for JSON formatting)

### 1. Start the Development Environment

```bash
# Start all services (databases + LocalStack)
make docker-up

# This will:
# - Start PostgreSQL containers for tenant1 & tenant2
# - Start Oracle XE container for tenant3
# - Start LocalStack for AWS Secrets Manager simulation
# - Initialize tenant secrets in LocalStack
# - Create sample data in each tenant database
```

### 2. Run the Application

```bash
# Build and run locally
make run

# Or run with Docker (includes app container)
make dev-app
```

### 3. Test Multi-Tenant Endpoints

```bash
# Get tenant information
curl -H 'X-Tenant-ID: tenant1' http://localhost:8080/api/v1/tenant/info | jq

# List users for tenant2
curl -H 'X-Tenant-ID: tenant2' http://localhost:8080/api/v1/tenant/users | jq

# Check Oracle tenant (tenant3)
curl -H 'X-Tenant-ID: tenant3' http://localhost:8080/api/v1/tenant/info | jq

# Health checks
curl http://localhost:8080/health/tenants | jq
curl http://localhost:8080/health/tenant/tenant1 | jq
```

## 📋 Available Endpoints

### Tenant Operations
- `GET /api/v1/tenant/info` - Get current tenant information
- `GET /api/v1/tenant/stats` - Get tenant database statistics
- `GET /api/v1/tenant/users` - List users for tenant
- `POST /api/v1/tenant/users` - Create new user
- `GET /api/v1/tenant/users/:id` - Get specific user
- `PUT /api/v1/tenant/users/:id` - Update user
- `DELETE /api/v1/tenant/users/:id` - Delete user

### Health & Monitoring
- `GET /health/live` - Liveness probe
- `GET /health/ready` - Readiness probe with DB checks
- `GET /health/tenants` - List all tenant connection health
- `GET /health/tenant/:id` - Specific tenant health check
- `GET /health/cache` - Cache performance metrics

## 🔧 Configuration

### Environment Variables

```bash
# Application
APP_ENV=development
APP_NAME=multitenant-aws-example

# AWS Configuration
AWS_REGION=us-east-1
AWS_ENDPOINT_URL=http://localhost:4566  # LocalStack
AWS_ACCESS_KEY_ID=test
AWS_SECRET_ACCESS_KEY=test

# Multi-tenant Settings
MULTITENANT_ENABLED=true
MULTITENANT_RESOLVER_TYPE=header
MULTITENANT_RESOLVER_HEADER=X-Tenant-ID
MULTITENANT_LIMITS_TENANTS=100

# Cache Configuration
AWS_SECRETS_CACHE_TTL=5m
AWS_SECRETS_CACHE_MAX_SIZE=1000
```

### AWS Secrets Structure

Each tenant has a secret in AWS Secrets Manager with this structure:

```json
{
  "type": "postgresql",
  "host": "postgres-tenant1",
  "port": 5432,
  "database": "tenant1_db",
  "username": "tenant1_user",
  "password": "tenant1_pass",
  "pool": {
    "max": {"connections": 20},
    "idle": {"connections": 5, "time": "30m"}
  },
  "query": {
    "slow": {"threshold": "200ms", "enabled": true}
  }
}
```

**Secret Path Pattern:** `/gobricks/{environment}/{tenant-id}/database`

## 🏢 Multi-Tenant Features

### 1. **Tenant Resolution**
- **Header-based**: Uses `X-Tenant-ID` header
- **Validation**: Regex pattern validation for tenant IDs
- **Middleware**: Automatic tenant context injection

### 2. **Database Management**
- **Dynamic Configuration**: Retrieved from AWS Secrets Manager
- **Connection Pooling**: Per-tenant connection pools
- **LRU Eviction**: Automatic cleanup of inactive tenants
- **Performance Tracking**: Query timing and slow query detection

### 3. **Caching Strategy**
- **L1 Cache**: AWS Secrets (5 min TTL)
- **L2 Cache**: Database connections (LRU with 100 tenant limit)
- **Metrics**: Hit rates, evictions, cache size tracking
- **Background Cleanup**: Automatic expired entry removal

### 4. **Database Support**

#### PostgreSQL (Tenants 1 & 2)
```yaml
tenant1:
  type: postgresql
  host: postgres-tenant1
  port: 5433
  database: tenant1_db
```

#### Oracle (Tenant 3)
```yaml
tenant3:
  type: oracle
  host: oracle-tenant3
  port: 1522
  database: XE
  oracle:
    service:
      name: XE
```

## 🧪 Testing

### Unit Tests
```bash
# Run all tests
make test

# Run with coverage
make coverage
```

### Integration Testing
```bash
# Test specific tenant
make test-tenant1

# Manual testing with curl
curl -H 'X-Tenant-ID: tenant1' \
     -X POST \
     -H 'Content-Type: application/json' \
     -d '{"name":"John Doe","email":"john@tenant1.com"}' \
     http://localhost:8080/api/v1/tenant/users | jq
```

### Load Testing Example
```bash
# Test cache performance with concurrent requests
for i in {1..100}; do
  curl -s -H 'X-Tenant-ID: tenant1' \
       http://localhost:8080/api/v1/tenant/info &
done
wait

# Check cache metrics
curl http://localhost:8080/health/cache | jq '.metrics'
```

## 🔍 Monitoring & Observability

### DataDog Integration

This project uses **DataDog** for distributed tracing and metrics via OpenTelemetry (OTLP).

#### Setup

1. **Get your DataDog API key** from [DataDog Organization Settings](https://app.datadoghq.com/organization-settings/api-keys)

2. **Create a `.env` file** (copy from `.env.example`):
   ```bash
   cp .env.example .env
   # Edit .env and add your DataDog API key
   ```

3. **Start the DataDog Agent**:
   ```bash
   docker-compose up -d datadog-agent
   ```

4. **Run your application** - telemetry will automatically be sent to DataDog

#### Configuration

The observability settings are configured in [config.yaml](config.yaml):

```yaml
observability:
  enabled: true
  service:
    name: "go-bricks-demo-api"
    version: "1.0.0"
  environment: "development"

  trace:
    enabled: true
    endpoint: "localhost:4317"  # DataDog Agent OTLP gRPC
    protocol: "grpc"
    sample:
      rate: 1.0  # 100% sampling for development

  metrics:
    enabled: true
    endpoint: "localhost:4317"
    protocol: "grpc"
    interval: 10s
```

#### Viewing Telemetry in DataDog

- **APM Traces**: [https://app.datadoghq.com/apm/traces](https://app.datadoghq.com/apm/traces)
- **Service Map**: [https://app.datadoghq.com/apm/map](https://app.datadoghq.com/apm/map)
- **Metrics Explorer**: [https://app.datadoghq.com/metric/explorer](https://app.datadoghq.com/metric/explorer)

Look for service name: **`go-bricks-demo-api`** with environment: **`development`**

#### Local Development Without DataDog

To run locally without DataDog (stdout logging only), update [config.yaml](config.yaml):

```yaml
observability:
  trace:
    endpoint: "stdout"
  metrics:
    endpoint: "stdout"
```

### Health Check Examples

```bash
# Check all tenant connections
curl http://localhost:8080/health/tenants | jq '.summary'

# Detailed tenant health
curl http://localhost:8080/health/tenant/tenant1 | jq

# Cache performance
curl http://localhost:8080/health/cache | jq '.metrics.hit_rate'
```

### Log Examples

The application provides structured logging with:

```json
{
  "level": "info",
  "tenant_id": "tenant1",
  "db_type": "postgresql",
  "duration_ms": 15,
  "message": "Successfully retrieved and cached database config"
}
```

## 🐳 Docker Environment

### Services

| Service | Port | Description |
|---------|------|-------------|
| LocalStack | 4566 | AWS Secrets Manager simulation |
| postgres-tenant1 | 5433 | Tenant 1 PostgreSQL database |
| postgres-tenant2 | 5434 | Tenant 2 PostgreSQL database |
| oracle-tenant3 | 1522 | Tenant 3 Oracle XE database |
| postgres-default | 5432 | Default PostgreSQL for health checks |
| multitenant-app | 8080 | Application (with --profile app) |

### Management Commands

```bash
# Start development environment
make dev

# View service logs
make docker-logs

# Check service status
make status

# Stop all services
make docker-down

# Reset environment
make docker-down && make docker-up
```

## 🔒 Security Considerations

### 1. **AWS Credentials**
- Uses IAM roles in production
- LocalStack credentials for development only
- Environment variable isolation

### 2. **Tenant Isolation**
- Database-level tenant separation
- Tenant ID validation and sanitization
- Connection pool isolation per tenant

### 3. **Secret Management**
- Encrypted at rest in AWS Secrets Manager
- TTL-based cache expiration
- No secrets in logs or error messages

## 📈 Performance Characteristics

### Cache Performance
- **Hit Rate**: >95% for established tenants
- **Lookup Time**: <1ms for cached configs
- **AWS API Calls**: Minimized with 5min TTL

### Connection Management
- **Pool Size**: 20 connections per tenant (configurable)
- **Idle Timeout**: 30 minutes (configurable)
- **LRU Limit**: 100 concurrent tenants

### Query Performance
- **Slow Query Threshold**: 200ms (configurable)
- **Connection Reuse**: Automatic pooling
- **Performance Tracking**: Built-in metrics

## 🛠️ Development Workflow

### 1. **Adding New Tenants**

```bash
# Create secret in LocalStack
aws secretsmanager create-secret \
  --name "/gobricks/local/tenant4/database" \
  --secret-string '{"type":"postgresql",...}' \
  --endpoint-url http://localhost:4566
```

### 2. **Modifying Tenant Configuration**

```bash
# Update existing secret
aws secretsmanager update-secret \
  --secret-id "/gobricks/local/tenant1/database" \
  --secret-string '{"type":"postgresql",...}' \
  --endpoint-url http://localhost:4566

# Clear cache to pick up changes immediately
curl -X POST http://localhost:8080/admin/cache/clear
```

### 3. **Debugging**

```bash
# Enable debug logging
export LOG_LEVEL=debug

# Check tenant resolution
curl -v -H 'X-Tenant-ID: tenant1' http://localhost:8080/api/v1/tenant/info

# Verify secrets in LocalStack
aws secretsmanager list-secrets --endpoint-url http://localhost:4566
```

## 🔄 Production Deployment

### 1. **AWS Setup**

```bash
# Create IAM role for Secrets Manager access
aws iam create-role --role-name go-bricks-secrets-role --assume-role-policy-document file://trust-policy.json

# Attach Secrets Manager read policy
aws iam attach-role-policy --role-name go-bricks-secrets-role --policy-arn arn:aws:iam::aws:policy/SecretsManagerReadWrite
```

### 2. **Environment Configuration**

```yaml
# Production config
aws:
  region: us-west-2
  secrets:
    prefix: /gobricks/production
    cache:
      ttl: 10m  # Longer TTL for production
      max_size: 10000

multitenant:
  limits:
    tenants: 1000  # Higher limit for production
```

### 3. **Health Check Integration**

```yaml
# Kubernetes health checks
livenessProbe:
  httpGet:
    path: /health/live
    port: 8080
  initialDelaySeconds: 30

readinessProbe:
  httpGet:
    path: /health/ready
    port: 8080
  initialDelaySeconds: 5
```

## 📚 Architecture Insights

`★ Insight ─────────────────────────────────────`
This example demonstrates several advanced patterns: (1) **Two-tier caching** minimizes AWS API calls while maintaining data freshness, (2) **LRU connection management** handles thousands of tenants efficiently, and (3) **Separate Go module** prevents framework bloat when users import go-bricks.
`─────────────────────────────────────────────────`

### Design Decisions

1. **Separate Go Module**: Prevents example from being included in main package downloads
2. **Header-based Resolution**: Simplest for API clients; easily extensible to JWT claims
3. **TTL Caching**: Balances performance with configuration freshness
4. **LRU Eviction**: Handles scaling to thousands of tenants efficiently
5. **Per-tenant Pools**: Provides isolation and prevents resource contention

### Scaling Considerations

- **Horizontal Scaling**: Stateless design supports multiple instances
- **Cache Warming**: Health checks pre-populate frequently used tenants
- **Circuit Breaker**: AWS failures don't cascade to healthy tenants
- **Graceful Degradation**: Falls back to cached configs during AWS outages

## 🤝 Contributing

This example follows the go-bricks development patterns:

1. **Code Style**: `gofmt` and `golangci-lint` compliance
2. **Testing**: >80% coverage requirement
3. **Documentation**: Comprehensive README and code comments
4. **Dependencies**: Minimal external dependencies

## 📄 License

This example is part of the go-bricks framework and follows the same license terms.

---

**🎯 Next Steps:**
1. Try the quick start: `make docker-up && make run`
2. Explore the API with different tenant IDs
3. Monitor cache performance with `/health/cache`
4. Experiment with different database configurations

For more go-bricks examples and documentation, visit the [main repository](../../README.md).