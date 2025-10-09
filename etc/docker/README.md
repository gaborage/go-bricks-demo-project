# Docker Infrastructure

This directory contains all Docker-related configuration files for the go-bricks demo project.

## Directory Structure

```
etc/docker/
├── docker-compose.yml        # Main compose file with all services
├── otel/                      # OpenTelemetry Collector configurations
│   ├── otel-collector-datadog.yaml      # DataDog exporter (cloud)
│   └── otel-collector-prometheus.yaml   # Prometheus/Jaeger exporters (local)
├── prometheus/                # Prometheus configuration
│   └── prometheus.yml
└── grafana/                   # Grafana provisioning
    └── provisioning/
        └── datasources/
            └── datasources.yml
```

## Usage

### Start Services

All docker-compose commands must be run from this directory (`etc/docker/`):

```bash
cd etc/docker

# Start local observability stack (Prometheus + Grafana + Jaeger)
docker-compose --profile local up -d

# Start DataDog observability stack (requires DD_API_KEY in .env)
docker-compose --profile datadog up -d

# Start database migrations
docker-compose --profile migrations up flyway
```

### Stop Services

```bash
cd etc/docker
docker-compose down
```

### View Logs

```bash
cd etc/docker
docker-compose logs -f <service-name>

# Examples:
docker-compose logs -f otel-collector-local
docker-compose logs -f prometheus
docker-compose logs -f grafana
```

### Service Status

```bash
cd etc/docker
docker-compose ps
```

## Available Profiles

### `local` - Local Observability Stack

**Services**:
- PostgreSQL (database)
- RabbitMQ (message broker)
- OpenTelemetry Collector (with Prometheus/Jaeger exporters)
- Prometheus (metrics storage)
- Grafana (visualization)
- Jaeger (distributed tracing)

**Access**:
- Prometheus UI: http://localhost:9090
- Grafana UI: http://localhost:3000 (admin/admin)
- Jaeger UI: http://localhost:16686
- RabbitMQ Management: http://localhost:15672 (guest/guest)

**Use case**: Local development and testing with immediate feedback

### `datadog` - DataDog Observability Stack

**Services**:
- PostgreSQL (database)
- RabbitMQ (message broker)
- OpenTelemetry Collector (with DataDog connector & exporter)

**Requirements**:
- `DD_API_KEY` environment variable
- `DD_SITE` environment variable (default: us5.datadoghq.com)

**Access**:
- DataDog APM: https://us5.datadoghq.com/apm

**Use case**: Cloud observability, production-like monitoring

### `migrations` - Database Migrations

**Services**:
- Flyway migration tool

**Use case**: Running database migrations

## Configuration Files

### OTel Collector Configs

**[otel/otel-collector-prometheus.yaml](otel/otel-collector-prometheus.yaml)**
- Receives OTLP from application
- Exports metrics to Prometheus (scraped on port 8889)
- Exports traces to Jaeger via OTLP
- Simple, clean pipelines for local testing

**[otel/otel-collector-datadog.yaml](otel/otel-collector-datadog.yaml)**
- Receives OTLP from application
- Uses DataDog Connector to compute APM stats
- Exports traces and metrics to DataDog Cloud
- Required for APM visibility in DataDog UI

### Prometheus Config

**[prometheus/prometheus.yml](prometheus/prometheus.yml)**
- Scrapes OTel Collector metrics endpoint (port 8889)
- 15-second scrape interval
- 15-day retention

### Grafana Datasources

**[grafana/provisioning/datasources/datasources.yml](grafana/provisioning/datasources/datasources.yml)**
- Auto-provisions Prometheus datasource
- Auto-provisions Jaeger datasource
- No manual configuration needed

## Common Commands

### Restart a Single Service

```bash
cd etc/docker
docker-compose restart <service-name>

# Examples:
docker-compose restart prometheus
docker-compose restart otel-collector-local
```

### Rebuild and Restart

```bash
cd etc/docker
docker-compose --profile local up -d --force-recreate
```

### Clean Everything

```bash
cd etc/docker
docker-compose down -v  # -v removes volumes (WARNING: deletes data!)
```

## Environment Variables

Create a `.env` file in the project root (not in `etc/docker/`) with:

```bash
# DataDog (required for --profile datadog)
DD_API_KEY=your_datadog_api_key_here
DD_SITE=us5.datadoghq.com
```

## Switching Between Observability Stacks

### From Local to DataDog

```bash
cd etc/docker

# Stop local stack
docker-compose down

# Start DataDog stack
docker-compose --profile datadog up -d
```

### From DataDog to Local

```bash
cd etc/docker

# Stop DataDog stack
docker-compose down

# Start local stack
docker-compose --profile local up -d
```

**Note**: The application doesn't need to be restarted when switching stacks. It always sends telemetry to `localhost:4317`, and whichever OTel Collector is running will receive it.

## Troubleshooting

### Port Conflicts

If you see "port already allocated" errors:

```bash
cd etc/docker

# Stop all containers
docker-compose down

# Remove any orphaned containers
docker ps -a | grep go-bricks | awk '{print $1}' | xargs docker rm -f

# Start again
docker-compose --profile local up -d
```

### Volume Permissions

If you encounter permission errors with volumes:

```bash
cd etc/docker
docker-compose down -v
docker volume prune -f
docker-compose --profile local up -d
```

### Check Service Health

```bash
cd etc/docker

# Check if Prometheus is scraping targets
curl http://localhost:9090/api/v1/targets | jq .

# Check if Jaeger has services
curl http://localhost:16686/api/services | jq .

# Check OTel Collector health
curl http://localhost:13133/
```

## Related Documentation

- [PROMETHEUS_GRAFANA_SETUP.md](../../wiki/PROMETHEUS_GRAFANA_SETUP.md) - Complete Prometheus/Grafana setup guide
- [DATADOG_CONNECTOR_FIX.md](../../DATADOG_CONNECTOR_FIX.md) - DataDog connector configuration details
- [config.development.yaml](../../config.development.yaml) - Application observability configuration
