# Docker Infrastructure

This directory contains all Docker-related configuration files for the go-bricks demo project.

## Directory Structure

```
etc/docker/
├── docker-compose.yml        # Main compose file with all services
├── otel/                      # OpenTelemetry Collector configurations
│   ├── otel-collector-newrelic.yaml     # New Relic exporter (cloud)
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

Run every command from the repository root, through the make targets or with
`-f etc/docker/docker-compose.yml --env-file .env`, as the Makefile does. A bare
`docker-compose` run from this directory fails before it does anything: the New
Relic collector declares `NEW_RELIC_LICENSE_KEY` as required, and compose
interpolates the whole file for every profile and command, so it needs the
root `.env` (see [Environment Variables](#environment-variables)).

```bash
# Start local observability stack (Prometheus + Grafana + Tempo + Loki)
make docker-up-local
# Or manually:
docker-compose -f etc/docker/docker-compose.yml --env-file .env --profile local up -d

# Start New Relic observability stack (requires a real NEW_RELIC_LICENSE_KEY in .env)
make docker-up-newrelic
# Or manually:
docker-compose -f etc/docker/docker-compose.yml --env-file .env --profile newrelic up -d

# Run database migrations (main database, then the analytics one)
make migrate
make migrate-analytics
# Or manually:
docker-compose -f etc/docker/docker-compose.yml --env-file .env --profile migrations run --rm flyway migrate
docker-compose -f etc/docker/docker-compose.yml --env-file .env --profile migrations run --rm flyway-analytics migrate
```

### Stop Services

```bash
# Keeps the volumes. `make docker-down` stops the services and deletes the volumes.
docker-compose -f etc/docker/docker-compose.yml --env-file .env down
```

### View Logs

```bash
make logs   # every service

# One service:
docker-compose -f etc/docker/docker-compose.yml --env-file .env logs -f <service-name>

# Examples:
docker-compose -f etc/docker/docker-compose.yml --env-file .env logs -f grafana-alloy
docker-compose -f etc/docker/docker-compose.yml --env-file .env logs -f prometheus
docker-compose -f etc/docker/docker-compose.yml --env-file .env logs -f grafana
```

### Service Status

```bash
make status
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

### `newrelic` - New Relic Observability Stack

**Services**:
- PostgreSQL (database)
- RabbitMQ (message broker)
- OpenTelemetry Collector (with New Relic OTLP exporter)

**Requirements**:
- `NEW_RELIC_LICENSE_KEY` environment variable
- `NEW_RELIC_REGION` environment variable (default: US, options: US or EU)

**Access**:
- New Relic One: https://one.newrelic.com/nr1-core
- APM & Services: https://one.newrelic.com/nr1-core?filters=(domain%20IN%20('APM'))

**Use case**: Cloud observability, production-like monitoring

### `migrations` - Database Migrations

**Services**:
- Flyway migration tool

**Use case**: Running database migrations

## Configuration Files

### OTel Collector Configs

**[otel/otel-collector-prometheus.yaml](otel/otel-collector-prometheus.yaml)**
- Receives OTLP from application
- Exports metrics to Prometheus (scraped on port 8889). Superseded by Grafana Alloy in the `local` profile; no service mounts this file
- Exports traces to Jaeger via OTLP
- Simple, clean pipelines for local testing

**[otel/otel-collector-newrelic.yaml](otel/otel-collector-newrelic.yaml)**
- Receives OTLP from application
- Exports traces, metrics, and logs to New Relic using standard OTLP protocol
- No connector needed (simpler than DataDog setup)
- Native OTLP support for full observability in New Relic One

### Prometheus Config

**[prometheus/prometheus.yml](prometheus/prometheus.yml)**
- Receives application metrics by remote_write from Grafana Alloy (`--web.enable-remote-write-receiver`)
- Scrapes only itself and Alloy. The app serves no `/metrics`: go-bricks exports metrics by OTLP push, never by pull
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
docker-compose -f etc/docker/docker-compose.yml --env-file .env restart <service-name>

# Examples:
docker-compose -f etc/docker/docker-compose.yml --env-file .env restart prometheus
docker-compose -f etc/docker/docker-compose.yml --env-file .env restart grafana-alloy
```

### Rebuild and Restart

```bash
docker-compose -f etc/docker/docker-compose.yml --env-file .env --profile local up -d --force-recreate
```

### Clean Everything

```bash
make docker-down  # runs `down -v`: removes the volumes (WARNING: deletes data!)
```

## Environment Variables

Create a `.env` file in the project root (not in `etc/docker/`), for example
with `cp .env.example .env`. Every compose command needs `NEW_RELIC_LICENSE_KEY`
set, whatever the profile, because compose interpolates the whole file; the
placeholder from `.env.example` is enough unless you run `--profile newrelic`:

```bash
# New Relic (a real key only for --profile newrelic)
NEW_RELIC_LICENSE_KEY=your_license_key_here
NEW_RELIC_REGION=US  # or EU
```

## Switching Between Observability Stacks

### From Local to New Relic

```bash
# Stop local stack (keeps the volumes)
docker-compose -f etc/docker/docker-compose.yml --env-file .env down

# Start New Relic stack
make docker-up-newrelic
```

### From New Relic to Local

```bash
# Stop New Relic stack (keeps the volumes)
docker-compose -f etc/docker/docker-compose.yml --env-file .env down

# Start local stack
make docker-up-local
```

**Note**: The application doesn't need to be restarted when switching stacks. It always sends telemetry to `localhost:4317`, and whichever OTel Collector is running will receive it.

## Troubleshooting

### Port Conflicts

If you see "port already allocated" errors:

```bash
# Stop all containers
docker-compose -f etc/docker/docker-compose.yml --env-file .env down

# Remove any orphaned containers
docker ps -a | grep go-bricks | awk '{print $1}' | xargs docker rm -f

# Start again
make docker-up-local
```

### Volume Permissions

If you encounter permission errors with volumes:

```bash
make docker-down   # down -v: deletes the volumes
docker volume prune -f
make docker-up-local
```

### Check Service Health

```bash
# Check if Prometheus is scraping targets
curl http://localhost:9090/api/v1/targets | jq .

# Check if Jaeger has services
curl http://localhost:16686/api/services | jq .

# Check OTel Collector health
curl http://localhost:13133/
```

## Related Documentation

- [PROMETHEUS_GRAFANA_SETUP.md](../../wiki/PROMETHEUS_GRAFANA_SETUP.md) - Complete Prometheus/Grafana setup guide
- [config.yml](../../config.yml) and [config.development.yaml](../../config.development.yaml) - Application configuration (base and development overlay)
- [New Relic OTLP Documentation](https://docs.newrelic.com/docs/more-integrations/open-source-telemetry-integrations/opentelemetry/opentelemetry-setup/) - Official New Relic OTLP guide
