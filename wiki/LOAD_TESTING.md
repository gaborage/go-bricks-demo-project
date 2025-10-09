# Load Testing Guide

This guide covers load testing the go-bricks demo application using k6, a modern open-source load testing tool.

## Table of Contents

- [Quick Start](#quick-start)
- [Available Tests](#available-tests)
- [Understanding Results](#understanding-results)
- [Configuration Tuning](#configuration-tuning)
- [Best Practices](#best-practices)
- [Troubleshooting](#troubleshooting)
- [Advanced Usage](#advanced-usage)

## Quick Start

### 1. Install k6

```bash
# Using Makefile (recommended)
make loadtest-install

# Or manually on macOS
brew install k6

# Or manually on Ubuntu/Debian
sudo gpg -k
sudo gpg --no-default-keyring --keyring /usr/share/keyrings/k6-archive-keyring.gpg --keyserver hkp://keyserver.ubuntu.com:80 --recv-keys C5AD17C747E3415A3642D57D77C6C491D6AC1D69
echo "deb [signed-by=/usr/share/keyrings/k6-archive-keyring.gpg] https://dl.k6.io/deb stable main" | sudo tee /etc/apt/sources.list.d/k6.list
sudo apt-get update
sudo apt-get install k6
```

### 2. Start the Application

```bash
# Start infrastructure
make docker-up

# Run migrations
make migrate

# Start the application (in another terminal)
make run
```

### 3. Run a Quick Smoke Test

```bash
# Validate everything works with minimal load
make loadtest-smoke
```

### 4. Run Your First Load Test

```bash
# Start with the CRUD mix test
make loadtest-crud
```

## Available Tests

### 1. CRUD Mix Test (`loadtests/products-crud.js`)

**Purpose:** Simulates realistic production traffic with mixed operations.

**Operation Distribution:**
- 50% List operations (paginated reads)
- 25% Get by ID operations
- 15% Create operations
- 7% Update operations
- 3% Delete operations

**Load Profile:** Gradual ramp-up from 0 → 100 VUs over 15 minutes

**Best for:**
- Benchmarking realistic workloads
- Comparing performance across code changes
- Pre-production validation

**Run:**
```bash
make loadtest-crud
# Or with custom parameters
k6 run --vus 50 --duration 10m loadtests/products-crud.js
```

---

### 2. Read-Only Baseline Test (`loadtests/products-read-only.js`)

**Purpose:** Establish baseline performance for read operations.

**Operation Distribution:**
- 70% List operations
- 30% Get by ID operations

**Load Profile:** Sustained 50 VUs for 12 minutes

**Best for:**
- Establishing baseline metrics
- Testing caching effectiveness
- Identifying database query optimizations

**Run:**
```bash
make loadtest-read
```

---

### 3. Ramp-Up Test (`loadtests/ramp-up-test.js`)

**Purpose:** Find the system's breaking point by gradually increasing load.

**Load Profile:**
- Stage 1-5: Ramp from 10 → 100 VUs
- Stage 6: Hold at 100 VUs
- Stage 7-10: Push to 150 → 200 VUs
- Watch for increased latency and errors

**Duration:** ~17 minutes

**Best for:**
- Finding maximum sustainable throughput
- Identifying resource bottlenecks
- Capacity planning

**Run:**
```bash
make loadtest-ramp
```

**Watch for:**
- At what VU count does p95 latency start increasing significantly?
- When do database connection pool warnings appear?
- Are there any timeout errors?

---

### 4. Spike Test (`loadtests/spike-test.js`)

**Purpose:** Validate system resilience under sudden traffic increases.

**Load Profile:**
- Baseline: 10 VUs
- Spike: Jump to 300 VUs for 1 minute
- Recovery: Drop back to 10 VUs

**Duration:** ~6 minutes

**Best for:**
- Testing auto-scaling behavior
- Validating rate limiting
- Ensuring graceful degradation

**Run:**
```bash
make loadtest-spike
```

**Success criteria:**
- Some errors during spike are acceptable (<10%)
- System recovers quickly after spike ends
- No cascading failures
- No resource leaks

---

### 5. Sustained Load Test (`loadtests/sustained-load.js`)

**Purpose:** Detect memory/connection leaks over extended duration.

**Load Profile:** Constant 50 VUs for 15 minutes

**Duration:** ~17 minutes

**Best for:**
- Detecting memory leaks
- Detecting connection leaks
- Validating long-term stability

**Run:**
```bash
make loadtest-sustained
```

**Monitor:**
- Memory usage should stay flat
- Database connections should stay stable
- No gradual performance degradation

---

### 6. Run All Tests

```bash
# Run all tests sequentially (~60 minutes)
make loadtest-all
```

## Understanding Results

### Key Metrics

#### 1. Response Time Percentiles

```
http_req_duration
  avg=245.32ms    # Average response time
  p(95)=423.18ms  # 95% of requests faster than this
  p(99)=891.45ms  # 99% of requests faster than this
  max=2.1s        # Slowest request
```

**What to look for:**
- ✅ **Good:** p95 < 500ms, p99 < 1000ms
- ⚠️ **Warning:** p95 > 500ms, investigate slow queries
- 🚨 **Critical:** p99 > 2000ms, likely hitting timeouts

#### 2. Request Rate

```
http_reqs: 12543 (208.95/s)
```

**What to look for:**
- Compare against expected traffic
- Should scale linearly with VUs (until bottleneck hit)
- Sudden drops indicate saturation

#### 3. Error Rate

```
http_req_failed: 0.12% ✓ { rate<0.01 }
```

**What to look for:**
- ✅ **Good:** < 0.1% errors
- ⚠️ **Warning:** 0.1-1% errors, investigate
- 🚨 **Critical:** > 1% errors, system overloaded

#### 4. Checks

```
✓ list: status is 200         99.8% ✓ 8542    ✗ 18
✓ list: response time < 500ms 94.2% ✓ 8067    ✗ 493
```

**What to look for:**
- Check pass rates by endpoint
- Identify which operations are failing

### Example Analysis

```
Scenario: Ramp-up test shows degradation at 100 VUs

Observations:
- p95 latency: 250ms @ 50 VUs → 1200ms @ 100 VUs
- Database connection pool warnings at 90 VUs
- Error rate: 0.01% @ 50 VUs → 2.5% @ 100 VUs

Diagnosis:
- Database connection pool exhausted
- Current: database.pool.max.connections = 25
- Need: More connections to handle 100 concurrent requests

Action:
- Increase database.pool.max.connections to 50
- Rerun test to verify improvement
```

## Configuration Tuning

### Database Connection Pool

**Location:** `config.development.yaml`

```yaml
database:
  pool:
    max:
      connections: 25      # Maximum connections
    idle:
      connections: 5       # Idle connections to keep
      time: 5m            # Max idle time
    lifetime:
      max: 30m            # Max connection lifetime
```

**Tuning guidelines:**

| Workload | Max Connections | Reasoning |
|----------|----------------|-----------|
| Light (< 20 VUs) | 10-15 | Minimize resource usage |
| Moderate (20-50 VUs) | 25-40 | Balance throughput and resources |
| Heavy (50-100 VUs) | 50-75 | Support high concurrency |
| Very Heavy (100+ VUs) | 75-100 | Approaching DB limits |

**Important:** PostgreSQL default max connections = 100. Don't exceed this without increasing PostgreSQL's `max_connections`.

**Testing connection pool:**
```bash
# Run ramp-up test and watch logs
make loadtest-ramp

# In another terminal, monitor connections
docker exec -it go-bricks-demo-project-postgres-1 \
  psql -U postgres -d postgres \
  -c "SELECT count(*) FROM pg_stat_activity WHERE state = 'active';"
```

---

### HTTP Server Timeouts

**Location:** `config.development.yaml`

```yaml
server:
  timeout:
    read: 15s              # Time to read request body
    write: 20s             # Time to write response
    idle: 60s              # Keep-alive idle time
    middleware: 10s        # Middleware timeout
```

**Tuning guidelines:**

| Symptom | Action |
|---------|--------|
| Request timeout errors | Increase `read` timeout |
| Response write errors | Increase `write` timeout |
| Connection drops | Increase `idle` timeout |
| Middleware timeouts | Increase `middleware` timeout |

**Warning:** Don't set timeouts too high (> 30s) as it can lead to resource exhaustion under load.

---

### Rate Limiting

**Location:** `config.development.yaml`

```yaml
app:
  rate:
    limit: 100            # Requests per second
    burst: 200            # Burst capacity
```

**Tuning guidelines:**

| Target RPS | Limit | Burst | Notes |
|------------|-------|-------|-------|
| Low (< 50 RPS) | 50 | 100 | Development/small apps |
| Medium (50-200 RPS) | 100 | 200 | Current setting |
| High (200-500 RPS) | 300 | 600 | Production with monitoring |
| Very High (> 500 RPS) | 500+ | 1000+ | Requires infrastructure scaling |

**Testing rate limiting:**
```bash
# Use spike test to trigger rate limiting
make loadtest-spike

# Check logs for rate limit messages
docker-compose logs -f | grep -i "rate limit"
```

---

### Query Performance

**Enable slow query logging:**

```yaml
database:
  query:
    slow:
      threshold: 100ms    # Log queries slower than 100ms
      enabled: true
    log:
      parameters: true    # Log query parameters
```

**Analyzing slow queries:**
```bash
# Run a test
make loadtest-crud

# Check application logs for slow queries
docker-compose logs | grep "slow query"

# Add indexes or optimize queries as needed
```

## Best Practices

### Before Running Tests

1. **Use dedicated test environment**
   - Don't run load tests against production
   - Use realistic data volumes

2. **Establish baseline**
   ```bash
   # Always start with read-only test
   make loadtest-read
   # Record results for comparison
   ```

3. **Monitor system resources**
   ```bash
   # Terminal 1: Application logs
   docker-compose logs -f

   # Terminal 2: Database connections
   watch -n 2 'docker exec go-bricks-demo-project-postgres-1 \
     psql -U postgres -c "SELECT count(*), state FROM pg_stat_activity GROUP BY state;"'

   # Terminal 3: System resources
   docker stats
   ```

4. **Seed test data (optional)**
   ```bash
   # Create seed migration if needed
   make migrate
   ```

### During Tests

1. **Watch for these red flags:**
   - Steadily increasing memory usage (leak)
   - Database connection count hitting max
   - Increasing error rates over time
   - Latency degradation in sustained tests

2. **Record observations:**
   - Screenshot terminal output
   - Save JSON results: `k6 run --out json=results.json loadtests/products-crud.js`
   - Note any anomalies in logs

### After Tests

1. **Analyze results systematically:**
   - Compare p95/p99 across tests
   - Calculate requests per second per VU
   - Identify bottleneck (CPU, DB, network)

2. **Make one change at a time:**
   - Adjust single configuration parameter
   - Rerun test
   - Compare results

3. **Document findings:**
   - Keep log of changes and results
   - Use git commits to track config changes
   ```bash
   git add config.development.yaml
   git commit -m "perf: increase DB pool to 50 connections - improved p95 from 1200ms to 400ms @ 100 VUs"
   ```

## Troubleshooting

### Problem: High Error Rates

**Symptoms:**
```
http_req_failed: 15.2% ✗ { rate<0.01 }
```

**Possible causes:**
1. **Connection pool exhausted**
   - Check logs for "no connections available"
   - Increase `database.pool.max.connections`

2. **Timeouts**
   - Check logs for "context deadline exceeded"
   - Increase `server.timeout.read/write`

3. **Rate limiting triggered**
   - Check logs for "rate limit exceeded"
   - Increase `app.rate.limit/burst` or reduce test VUs

---

### Problem: High Latency (p95 > 1000ms)

**Symptoms:**
```
http_req_duration............: p(95)=1523.45ms
```

**Debugging steps:**

1. **Check slow query logs:**
   ```bash
   docker-compose logs | grep "slow query"
   ```
   - Add indexes to frequently queried columns
   - Optimize N+1 queries

2. **Check database connection pool:**
   ```bash
   # If connections are maxed out, increase pool size
   database.pool.max.connections: 50
   ```

3. **Profile the application:**
   ```go
   // Add pprof endpoints in debug mode
   import _ "net/http/pprof"
   ```

---

### Problem: Memory Leak

**Symptoms:**
```
Sustained load test shows:
  First 5 minutes:  200ms average
  Last 5 minutes:   850ms average
```

**Debugging steps:**

1. **Check for connection leaks:**
   ```sql
   -- Check if connections grow over time
   SELECT count(*) FROM pg_stat_activity WHERE state = 'idle in transaction';
   ```

2. **Profile memory usage:**
   ```bash
   # Use pprof to capture heap profile
   curl http://localhost:8080/debug/pprof/heap > heap.prof
   go tool pprof -http=:6060 heap.prof
   ```

3. **Check for goroutine leaks:**
   ```bash
   curl http://localhost:8080/debug/pprof/goroutine?debug=1
   ```

---

### Problem: Spike Test Doesn't Recover

**Symptoms:**
- Error rate stays high after spike ends
- Connections stay maxed out

**Possible causes:**

1. **Connection leak**
   - Connections not returned to pool
   - Check for missing `defer db.Close()` or `defer rows.Close()`

2. **Goroutine leak**
   - Background tasks not cleaning up
   - Check for missing context cancellation

3. **Circuit breaker stuck open**
   - Retry logic needs tuning

## Advanced Usage

### Custom Test Scenarios

Create your own test file:

```javascript
// loadtests/custom-test.js
import http from 'k6/http';
import { check } from 'k6';
import { config, getURL, headers } from './config.js';

export const options = {
  stages: [
    { duration: '1m', target: 20 },
    { duration: '5m', target: 20 },
  ],
};

export default function() {
  const response = http.get(getURL('/products?page=1&pageSize=10'), { headers });
  check(response, { 'status is 200': (r) => r.status === 200 });
}
```

Run:
```bash
k6 run loadtests/custom-test.js
```

### Environment Variables

Override configuration:

```bash
# Test against different environment
K6_BASE_URL=http://staging.example.com:8080 k6 run loadtests/products-crud.js

# Save results to file
k6 run --out json=results.json loadtests/products-crud.js

# Generate HTML report (requires k6-reporter)
k6 run --out json=results.json loadtests/products-crud.js
# Then use k6-reporter or similar tool
```

### Cloud Testing

Run tests from k6 Cloud:

```bash
# Sign up at https://app.k6.io/
k6 login cloud

# Run test in cloud
k6 cloud loadtests/products-crud.js

# View results in browser
```

### CI/CD Integration

Add to your CI pipeline:

```yaml
# .github/workflows/load-test.yml
name: Load Test
on:
  schedule:
    - cron: '0 2 * * *'  # Daily at 2am

jobs:
  loadtest:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - name: Install k6
        run: |
          curl https://github.com/grafana/k6/releases/download/v0.47.0/k6-v0.47.0-linux-amd64.tar.gz -L | tar xvz
          sudo mv k6-v0.47.0-linux-amd64/k6 /usr/bin/k6
      - name: Run load test
        run: k6 run --out json=results.json loadtests/products-crud.js
      - name: Upload results
        uses: actions/upload-artifact@v3
        with:
          name: load-test-results
          path: results.json
```

## Performance Benchmarks

### Expected Performance (Single Machine)

**Hardware:** 2 CPU cores, 4GB RAM

| Test | Max VUs | Avg RPS | p95 Latency | Notes |
|------|---------|---------|-------------|-------|
| Read-Only | 100 | 500-800 | < 400ms | With proper indexes |
| CRUD Mix | 75 | 300-500 | < 600ms | With DB pool tuning |
| Sustained | 50 | 200-300 | < 500ms | Stable over 15min |

**Database configuration for these results:**
```yaml
database.pool.max.connections: 50
```

### Scaling Recommendations

| Target RPS | Infrastructure |
|------------|----------------|
| < 500 | Single instance, 25 DB connections |
| 500-1000 | Single instance, 50 DB connections |
| 1000-2000 | 2 instances + load balancer, 75 DB connections |
| 2000+ | Horizontal scaling, connection pooling proxy (PgBouncer) |

## References

- [k6 Documentation](https://k6.io/docs/)
- [k6 Best Practices](https://k6.io/docs/testing-guides/test-types/)
- [PostgreSQL Connection Pooling](https://www.postgresql.org/docs/current/runtime-config-connection.html)
- [go-bricks Framework](../go-bricks)

## Getting Help

- **k6 Community:** https://community.k6.io/
- **go-bricks Issues:** https://github.com/gaborage/go-bricks/issues
- **Project Slack:** [Your Slack channel]

---

**Next Steps:**
1. Run `make loadtest-smoke` to validate setup
2. Run `make loadtest-read` to establish baseline
3. Run `make loadtest-crud` for realistic benchmarks
4. Review results and tune configuration
5. Document your findings for the team
