# Multi-stage build for the go-bricks demo API.
#
# The build context is an allowlist (.dockerignore): go.mod/go.sum, cmd/,
# internal/ and config.development.yaml. certs/, .env and the untracked go.work
# never reach the daemon, so no key or secret can land in a layer.
#
# Build:  docker build -t go-bricks-demo-project .
# Run:    see "Running the container" in README.md — the image carries no keys
#         (mount certs/ read-only) and dials the infrastructure named by env vars.

# Builder: the Go minor must satisfy go.mod's `go` directive (1.27.0).
FROM golang:1.27-alpine AS builder

WORKDIR /src

# GOWORK=off: build against go.mod alone, as CI does. A go.work in the context
# would point at a sibling framework checkout that does not exist in the image.
ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOWORK=off

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

RUN go build -trimpath -ldflags="-s -w" -o /out/app ./cmd/api

# Runtime
FROM alpine:3.24

# ca-certificates for outbound TLS. tzdata because config validation resolves
# every database.timezone with time.LoadLocation (the analytics database runs in
# Asia/Tokyo) and alpine ships no zoneinfo. busybox (in the base image) provides
# the wget the HEALTHCHECK below uses.
RUN apk --no-cache add ca-certificates tzdata && \
    addgroup -g 1001 appuser && \
    adduser -D -s /bin/sh -u 1001 -G appuser appuser

WORKDIR /app

COPY --from=builder /out/app ./app

# The demo ships one config file. go-bricks loads config.yaml (absent: framework
# defaults) plus config.<APP_ENV>.yaml from the working directory, so the image
# selects development by default. Environment variables override any key, e.g.
# DATABASE_HOST, MESSAGING_BROKER_URL, SERVER_PORT.
COPY config.development.yaml ./
ENV APP_ENV=development

USER appuser

EXPOSE 8080

# Liveness via the framework's static health route (server.path.base +
# server.path.health, /api/v1/health in config.development.yaml). The binary has
# no health-check flag; busybox wget does the request. Shell form so an
# overridden SERVER_PORT or SERVER_PATH_BASE is honoured.
HEALTHCHECK --interval=30s --timeout=5s --start-period=60s --retries=3 \
  CMD wget -q -O /dev/null "http://127.0.0.1:${SERVER_PORT:-8080}${SERVER_PATH_BASE:-/api/v1}/health" || exit 1

CMD ["./app"]
