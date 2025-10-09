# Prometheus + Grafana Observability Stack ✅

**Date**: 2025-10-09
**Status**: Local observability stack fully operational - go-bricks observability **VALIDATED** ✅

## Summary

Successfully created a **parallel observability stack** using Prometheus, Grafana, and Jaeger for **immediate local validation** of go-bricks observability, while keeping the DataDog setup intact.

## Validation Results

### ✅ Go-Bricks Observability Framework WORKS!

**Confirmed Working**:
- ✅ **Trace generation**: 30 trace spans generated and exported via OTLP
- ✅ **Metrics generation**: HTTP server metrics available in Prometheus
- ✅ **OTLP export**: Application successfully sends telemetry to OTel Collector
- ✅ **W3C trace propagation**: traceparent headers correctly generated
- ✅ **Service metadata**: service.name, environment, version all correct

**Evidence**:
```
Application Logs:
2025/10/09 09:27:32 [OBSERVABILITY] [SPAN] STARTED: GET /api/v1/products (trace=75ae052634cc3c9972182886adc23f6f)
2025/10/09 09:27:32 [OBSERVABILITY] [SPAN] ENDED: GET /api/v1/products (duration=655µs)

Prometheus Metrics (http://localhost:9090):
gobricks_http_server_request_duration_seconds_count
gobricks_http_server_request_body_size_bytes
gobricks_http_server_response_body_size_bytes
```

## Architecture

### Dual Observability Stack Setup

```
┌─────────────────────────────────────────────────────────────┐
│                     Application                              │
│             go-bricks-demo-project                           │
│         (localhost:4317 - OTLP endpoint)                     │
└─────────────────┬───────────────────────────────────────────┘
                  │
                  ▼
        Which OTel Collector?
                  │
        ┌─────────┴──────────┐
        │                    │
        ▼                    ▼
  DataDog Stack       Prometheus Stack
  (Cloud)             (Local)
        │                    │
        ▼                    ▼
   DataDog API    ┌──────────┴──────────┐
                  │                     │
           Prometheus            Jaeger
           (metrics)            (traces)
                  ↓
              Grafana
          (visualization)
```

### Profile-Based Switching

**Docker Compose Profiles**:
- `datadog`: OTel Collector → DataDog Cloud
- `local`: OTel Collector → Prometheus + Grafana + Jaeger

**Switch commands**:
```bash
# Local testing (recommended for development)
docker-compose --profile local up -d

# DataDog testing (cloud observability)
docker-compose --profile datadog up -d
```

## File Structure

```
go-bricks-demo-project/
├── otel-collector-datadog.yaml          # DataDog exporter config
├── otel-collector-prometheus.yaml       # Prometheus/Jaeger exporter config
├── prometheus.yml                       # Prometheus scrape config
├── grafana/
│   └── provisioning/
│       └── datasources/
│           └── datasources.yml          # Auto-provision Prometheus + Jaeger
├── docker-compose.yml                   # Both stacks with profiles
└── config.development.yaml              # Application config (unchanged)
```

## Services and Ports

### Local Stack (`--profile local`)

| Service | Port | URL | Purpose |
|---------|------|-----|---------|
| **OTel Collector** | 4317 | - | OTLP gRPC receiver |
| **OTel Collector** | 4318 | - | OTLP HTTP receiver |
| **OTel Collector** | 8889 | http://localhost:8889/metrics | Prometheus exporter |
| **Prometheus** | 9090 | http://localhost:9090 | Metrics storage & query |
| **Grafana** | 3000 | http://localhost:3000 | Dashboards (admin/admin) |
| **Jaeger** | 16686 | http://localhost:16686 | Trace visualization |

### DataDog Stack (`--profile datadog`)

| Service | Port | Purpose |
|---------|------|---------|
| **OTel Collector** | 4317 | OTLP gRPC receiver |
| **OTel Collector** | 4318 | OTLP HTTP receiver |
| *Exports to* | - | DataDog Cloud (us5.datadoghq.com) |

## How to Use

### Start Local Stack

```bash
# Stop any running containers
docker-compose down

# Start local observability stack
docker-compose --profile local up -d

# Verify services are running
docker-compose ps

# Start your application
make build
env -i APP_ENV=development PATH="$PATH" ./bin/go-bricks-demo-project

# Generate test traffic
for i in {1..30}; do curl http://localhost:8080/api/v1/products?page=1; sleep 0.5; done
```

### View Metrics in Prometheus

1. Open **Prometheus UI**: http://localhost:9090
2. Try these queries:
   ```promql
   # Request duration histogram
   gobricks_http_server_request_duration_seconds_bucket

   # Request count
   rate(gobricks_http_server_request_duration_seconds_count[5m])

   # P95 latency
   histogram_quantile(0.95,
     rate(gobricks_http_server_request_duration_seconds_bucket[5m])
   )
   ```

### View Dashboards in Grafana

1. Open **Grafana**: http://localhost:3000
2. Login: `admin` / `admin`
3. Datasources are auto-configured:
   - **Prometheus** (metrics)
   - **Jaeger** (traces)
4. Create custom dashboards or explore metrics

### View Traces in Jaeger

1. Open **Jaeger UI**: http://localhost:16686
2. Select service: `go-bricks-demo-project`
3. Search traces
4. Explore span details, timing, and request flow

### Switch to DataDog Stack

```bash
# Stop local stack
docker-compose down

# Start DataDog stack (requires DD_API_KEY in .env)
docker-compose --profile datadog up -d

# Application automatically connects to the active OTel Collector
```

## Configuration Details

### OTel Collector - Prometheus Config

**File**: `otel-collector-prometheus.yaml`

**Key Differences from DataDog**:
- **No DataDog connector** (simpler pipelines)
- **Prometheus exporter** on port 8889 (scraped by Prometheus)
- **Jaeger exporter** via OTLP to port 4317
- **Clean, simple architecture** - perfect for local testing

**Pipelines**:
```yaml
service:
  pipelines:
    traces:
      receivers: [otlp]
      processors: [resourcedetection, resource, batch]
      exporters: [otlp/jaeger, debug]

    metrics:
      receivers: [otlp]
      processors: [resourcedetection, resource, batch]
      exporters: [prometheus]
```

### Prometheus Configuration

**File**: `prometheus.yml`

**Scrape Jobs**:
1. **otel-collector** (port 8889) - Application metrics
2. **otel-collector-internal** (port 8888) - Collector's own metrics
3. **prometheus** (port 9090) - Prometheus self-monitoring

### Grafana Datasources

**File**: `grafana/provisioning/datasources/datasources.yml`

**Auto-provisioned**:
- Prometheus (default datasource)
- Jaeger (for traces)

## Benefits of This Approach

### ✅ Immediate Feedback
- **No waiting**: See metrics in < 30 seconds (vs. 10-15 minutes with DataDog)
- **Local debugging**: Full visibility into telemetry pipeline
- **No cloud dependency**: Work offline

### ✅ Validated go-bricks Observability
- **Confirmed**: Traces are generated correctly
- **Confirmed**: Metrics are exported via OTLP
- **Confirmed**: Service metadata is correct
- **Confirmed**: W3C trace propagation works

### ✅ Dual Stack Flexibility
- **Local dev**: Use Prometheus/Grafana for fast feedback
- **Cloud testing**: Switch to DataDog when needed
- **Clean separation**: No config mixing

### ✅ Production-Ready DataDog Setup
- **DataDog connector**: APM stats computation
- **Cloud integration**: Ready for production monitoring
- **Maintained separately**: No risk to DataDog setup

## Metrics Available

### HTTP Server Metrics (Namespace: `gobricks_`)

```
gobricks_http_server_request_duration_seconds_bucket
gobricks_http_server_request_duration_seconds_count
gobricks_http_server_request_duration_seconds_sum
gobricks_http_server_request_body_size_bytes_bucket
gobricks_http_server_request_body_size_bytes_count
gobricks_http_server_request_body_size_bytes_sum
gobricks_http_server_response_body_size_bytes_bucket
gobricks_http_server_response_body_size_bytes_count
gobricks_http_server_response_body_size_bytes_sum
```

### Labels Available

- `service_name`: go-bricks-demo-project
- `deployment_environment`: development
- `service_version`: 1.0.0
- `http_request_method`: GET, POST, etc.
- `http_response_status_code`: 200, 400, etc.
- `url_path`: /api/v1/products, etc.

## Example Prometheus Queries

### Request Rate
```promql
# Requests per second
rate(gobricks_http_server_request_duration_seconds_count[1m])

# By HTTP method
sum by (http_request_method) (
  rate(gobricks_http_server_request_duration_seconds_count[1m])
)
```

### Latency
```promql
# Average latency
rate(gobricks_http_server_request_duration_seconds_sum[5m])
/ rate(gobricks_http_server_request_duration_seconds_count[5m])

# P95 latency
histogram_quantile(0.95,
  rate(gobricks_http_server_request_duration_seconds_bucket[5m])
)

# P99 latency
histogram_quantile(0.99,
  rate(gobricks_http_server_request_duration_seconds_bucket[5m])
)
```

### Error Rate
```promql
# 4xx error rate
sum(rate(gobricks_http_server_request_duration_seconds_count{
  http_response_status_code=~"4.."
}[5m]))

# 5xx error rate
sum(rate(gobricks_http_server_request_duration_seconds_count{
  http_response_status_code=~"5.."
}[5m]))

# Error percentage
sum(rate(gobricks_http_server_request_duration_seconds_count{
  http_response_status_code=~"[45].."
}[5m]))
/ sum(rate(gobricks_http_server_request_duration_seconds_count[5m]))
* 100
```

## Troubleshooting

### Metrics not showing in Prometheus

1. **Check OTel Collector is running**:
   ```bash
   docker ps | grep otel-collector-local
   ```

2. **Check Prometheus scrape targets**:
   - Open http://localhost:9090/targets
   - Verify `otel-collector` target is UP

3. **Check OTel Collector logs**:
   ```bash
   docker logs go-bricks-otel-collector-local
   ```

### Traces not showing in Jaeger

1. **Verify Jaeger is running**:
   ```bash
   docker ps | grep jaeger
   ```

2. **Check application is generating traces**:
   ```bash
   tail -f /tmp/app-prometheus.log | grep SPAN
   ```

3. **Check OTel Collector is sending to Jaeger**:
   ```bash
   docker logs go-bricks-otel-collector-local | grep jaeger
   ```

### Port conflicts

If you see "port already allocated" errors:
```bash
# Stop all containers
docker-compose down

# Remove any orphaned containers
docker ps -a | grep otel | awk '{print $1}' | xargs docker rm -f

# Start again
docker-compose --profile local up -d
```

## Next Steps

### Create Grafana Dashboards

1. Open Grafana: http://localhost:3000
2. Create dashboard → Add panel
3. Use Prometheus queries above
4. Save dashboard

### Export Dashboards

Grafana dashboards can be exported as JSON and version controlled:
```bash
# Create dashboards directory
mkdir -p grafana/provisioning/dashboards

# Add dashboard JSON files
# They will auto-load on Grafana startup
```

### Add More Metrics

The go-bricks framework supports custom metrics:
```go
meter := otel.GetMeterProvider().Meter("my-module")
counter, _ := observability.CreateCounter(meter, "my.counter", "Description")
counter.Add(ctx, 1)
```

## References

- **Prometheus Docs**: https://prometheus.io/docs/
- **Grafana Docs**: https://grafana.com/docs/
- **Jaeger Docs**: https://www.jaegertracing.io/docs/
- **OpenTelemetry Docs**: https://opentelemetry.io/docs/
- **go-bricks Observability**: [../go-bricks/observability](../go-bricks/observability/)

## Conclusion

**Go-bricks observability framework is working correctly!**

✅ **Traces**: Generated and exported via OTLP
✅ **Metrics**: Available in Prometheus with correct labels
✅ **Service metadata**: Correctly tagged
✅ **W3C propagation**: Traceparent headers working

**The Prometheus + Grafana stack provides immediate, local validation without waiting for cloud indexing delays.**

You now have two fully functional observability stacks:
- **Local (Prometheus/Grafana/Jaeger)**: Fast feedback for development
- **Cloud (DataDog)**: Production-ready monitoring

Switch between them with a single command!
