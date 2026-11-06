# Log Correlation Demo

`make log-correlation-demo` checks three go-bricks logging behaviors against a
running instance of this app. They come with the go-bricks main `0eb5f27f` pin
and need no configuration:

| Atom | Behavior |
|------|----------|
| C73.29 (ADR-167) | The access line's `url.path` and message, the span's `url.path` and every framework WARN/ERROR `path` print the matched route template with each parameter as `REDACTED`. `http.route` keeps the raw template. An unmatched request prints the bare `REDACTED`. |
| C73.30–C73.32 | `Panic recovered`, `unhandled error`, `[server.tenant]`, `[server.ip_preguard]` and the forwarded-client-cert WARNs carry `http.route`, `request_id`, `correlation_id` (the same value) and, under a valid span, `trace_id`/`span_id`. |
| C73.33 | A WARN/ERROR logged through the request context, or `HandlerContext.EscalateSeverity`, keeps the access line and raises it to `level=warn result_code=WARN` (or error). |

## Run it

```bash
make docker-up && make migrate   # postgres + broker
make generate-keys               # first time only
make log-correlation-demo        # builds, boots its own instance on :18090
```

The script refuses a port that is already in use. `make run` on :8080 can stay
up. Set `APP_PORT` to move the instance, and `LOG_DIR` to keep its log in a
fixed place. The log is kept after the run, and its path is printed in the verdict.

## What it sends and checks

| Request | Checks |
|---------|--------|
| (startup) | one INFO `[server.paths] inbound paths render from the matched route template rule=masked_route_template listener=application token=REDACTED unmatched=REDACTED` |
| `GET /api/v1/legacy/products/<random uuid>` | 404; access line `url.path=/api/v1/legacy/products/REDACTED`, `http.route=/api/v1/legacy/products/:id`, message `GET /api/v1/legacy/products/REDACTED completed in …` |
| `GET /api/v1/no-such-route/<random uuid>` | 404; access line `url.path=REDACTED` |
| `GET /api/v1/__sim/observability/warn/<token>` | 200; the handler's WARN joins on `request_id`; access line `level=warn result_code=WARN`, status 200, masked `url.path` |
| `GET /api/v1/__sim/observability/escalate/<token>` | 200; access line `level=warn result_code=WARN` |
| `GET /api/v1/__sim/observability/panic/<token>` | 500; `Panic recovered` with `http.route=/api/v1/__sim/observability/panic/:token` and `request_id` = `correlation_id` = the response's `X-Request-ID` |
| `POST /api/v1/tokens` with `Content-Type: application/json` | 415; access line status 415 at `level=warn result_code=WARN`; the `jose: operation failed` ERROR carries `http.route` and `correlation_id` without raising the access line to error |
| (whole log) | none of the ids or tokens sent in a path appears in any line |

The `/__sim/observability` routes belong to the logproof module
([internal/modules/logproof/](../internal/modules/logproof/)) and are
registered only when two guards pass: the opt-in
`custom.logproof.enabled` (env `CUSTOM_LOGPROOF_ENABLED=true`, off by default)
and `APP_ENV=development`. One of them panics on purpose, and development is the
framework default and the Dockerfile's `APP_ENV`, so the environment alone is not
enough. The script sets both for its own instance; `make run` registers none of
them. The cmd/api route policy requires their `"simulator"` tag.

Each request is matched to its lines by the response's `X-Request-ID`, which
the access line carries as `request_id`. The framework stamps `request_id`
only on its own lines, so the logproof WARN handler copies the header onto its
own line.

## Reading the output

```
── 3/5  Panic recovered correlation (C73.31) ──
PASS  panic route answers 500
PASS  Panic recovered http.route is the template
PASS  Panic recovered correlation_id = X-Request-ID
note  panic access line reads status=200 level=info (client got 500)
```

The `note` line is not a check. At this pin, a recovered handler panic's access
line is written before the error handler answers 500. It reads status 200 at
`level=info`, with the recovered error in `error`. Join it to the
`Panic recovered` ERROR on `request_id`.

`trace_id`/`span_id` are not checked: `config.development.yaml` leaves the OTLP
export off, so no request has a valid span context. Boot with the
`OBSERVABILITY_*` variables from CLAUDE.md's runtime tour to see them.

## Repointing log queries

A query keyed on a concrete path finds nothing after the bump. Key it on the
template instead:

```logql
# before: one product's requests by its concrete id, now matches nothing
{container_name=~".*"} | json | url_path="/api/v1/products/0b9a6f0e-1c2d-4e5f-8a9b-0c1d2e3f4a5b"

# after: one series per route
{container_name=~".*"} | json | http_route="/api/v1/products/:id"
```

Loki's `json` stage turns `http.route` into `http_route`. Nothing in this
repository's Grafana dashboards or k6 scripts reads `url.path`. A concrete path
needed for an incident lives at the edge (load balancer or WAF logs).
