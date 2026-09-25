# Go Bricks Demo Project Makefile

.PHONY: help build run test clean docker-up docker-up-local docker-up-newrelic docker-down logs status check-deps deps fmt lint coverage check migrate migrate-info migrate-analytics migrate-analytics-info migrate-all migrate-multitenant-check migrate-multitenant-install migrate-multitenant-init migrate-multitenant-up migrate-multitenant-info migrate-multitenant-validate migrate-multitenant-reset migrate-multitenant-samples test-products-api show-sealed-message seal-event-demo generate-keys dev update check-k6 loadtest-install loadtest-crud loadtest-read loadtest-ramp loadtest-spike loadtest-sustained loadtest-smoke loadtest-tokens loadtest-tokens-smoke loadtest-type-check loadtest-all loadtest-all-monitored loadtest-monitor loadtest-analyze

# Default target
help:
	@echo "Go Bricks Demo Project"
	@echo ""
	@echo "Available targets:"
	@echo "  help              Show this help message"
	@echo "  deps              Download Go dependencies"
	@echo "  build             Build the application"
	@echo "  run               Run the application locally"
	@echo "  test              Run tests"
	@echo "  clean             Clean build artifacts"
	@echo ""
	@echo "Docker targets:"
	@echo "  docker-up         Start all services (PostgreSQL + RabbitMQ + local observability)"
	@echo "  docker-up-local   Start local observability stack (Prometheus/Grafana/Tempo/Loki)"
	@echo "  docker-up-newrelic Start New Relic observability stack (cloud)"
	@echo "  docker-down       Stop all services"
	@echo "  logs              View logs from all services"
	@echo "  status            Show service status"
	@echo ""
	@echo "Database targets:"
	@echo "  migrate           Run main database migrations (Flyway)"
	@echo "  migrate-info      Show main migration status"
	@echo "  migrate-analytics Run analytics database migrations"
	@echo "  migrate-analytics-info Show analytics migration status"
	@echo "  migrate-all       Run all migrations (main + analytics)"
	@echo ""
	@echo "Multi-tenant migrations (schema-per-tenant via go-bricks-migrate):"
	@echo "  migrate-multitenant-install   go install the go-bricks-migrate CLI"
	@echo "  migrate-multitenant-init      Bootstrap per-tenant roles + schemas in postgres"
	@echo "  migrate-multitenant-up        Boot postgres + apply migrations to every tenant"
	@echo "  migrate-multitenant-info      Show migration status for every tenant"
	@echo "  migrate-multitenant-validate  Validate (no apply) for every tenant"
	@echo "  migrate-multitenant-verdict   Show run verdicts + exit codes 0/2/1 (validate only)"
	@echo "  migrate-multitenant-check-roles Check tenant roles sit at the privilege floor (read-only)"
	@echo "  migrate-multitenant-reset     Drop and recreate every tenant's schema"
	@echo "  migrate-multitenant-samples   Capture sample Flyway JSON outputs (see go-bricks#376)"
	@echo ""
	@echo "Development targets:"
	@echo "  generate-keys     Generate RSA keypairs (webhook-signing, tokens-our, tokens-peer, payments-sign-v1, payments-encrypt-v1); patches tokens-peer public into config.development.yaml"
	@echo "  fmt               Format Go code"
	@echo "  lint              Run linters"
	@echo "  coverage          Generate test coverage report"
	@echo "  check             Run fmt, lint, and test (pre-commit)"
	@echo ""
	@echo "API Testing:"
	@echo "  test-products-api Test products API endpoints"
	@echo "  advisory-lock-demo Race two app replicas for the report job's advisory lock (one runs per tick)"
	@echo "  show-sealed-message Publish a sealed payment, dump the raw broker body, open it (open-event, card redacted)"
	@echo "  seal-event-demo   Mint sealed events outside the app (seal-event CLI): open, dedup, DLQ reject + open-event verdict"
	@echo "  demo-consumer-readiness /ready fails closed on a stalled consumer (boots its own app; stop make run first)"
	@echo "  external-exchange-demo Consume from an exchange another service owns: 404 abort, externalwait, consume"
	@echo ""
	@echo "JOSE request bodies (framework seal-payload CLI; JSON on stdin, sealed body on stdout):"
	@echo "  seal-payload      Seal as the peer for POST /api/v1/tokens (nested JWE-of-JWS)"
	@echo "  seal-mle          Seal a Visa MLE {\"encData\":...} body for POST /api/v1/__sim/peer/mle (bare JWE)"
	@echo ""
	@echo "Load Testing:"
	@echo "  loadtest-install          Install k6 load testing tool"
	@echo "  loadtest-crud             Run CRUD mix load test"
	@echo "  loadtest-read             Run read-only baseline test"
	@echo "  loadtest-ramp             Run ramp-up test (find limits)"
	@echo "  loadtest-spike            Run spike test (traffic bursts)"
	@echo "  loadtest-sustained        Run sustained load test (15min)"
	@echo "  loadtest-topology-repair  Delete both AMQP exchanges under load; report repair + lost 202s (~2.5min)"
	@echo "  loadtest-tokens           Run tokens relay (JOSE) load test (~12min)"
	@echo "  loadtest-tokens-smoke     Run tokens relay smoke test (30s)"
	@echo "  loadtest-tokens-mle       Run tokens MLE relay (bare JWE + encData) load test (~12min)"
	@echo "  loadtest-tokens-mle-smoke Run tokens MLE relay smoke test (30s)"
	@echo "  loadtest-tokens-vts       Run tokens VTS Issuer relay (JWS-of-JWE) load test (~12min)"
	@echo "  loadtest-tokens-vts-smoke Run tokens VTS Issuer relay smoke test (30s)"
	@echo "  loadtest-all              Run all load tests in sequence"
	@echo "  loadtest-all-monitored    Run all tests with monitoring & analysis"
	@echo "  loadtest-monitor          Start manual monitoring"
	@echo "  loadtest-analyze FILE=... Analyze metrics file"
	@echo ""
	@echo "Messaging resilience:"
	@echo "  redeclare-demo            Delete payment-events/product-events under the live app; watch them self-repair"

# Check if required dependencies are installed
check-deps:
	@echo "Checking dependencies..."
	@command -v go >/dev/null 2>&1 || { echo "❌ Go is required but not installed"; exit 1; }
	@command -v docker >/dev/null 2>&1 || { echo "❌ Docker is required but not installed"; exit 1; }
	@command -v docker-compose >/dev/null 2>&1 || { echo "❌ Docker Compose is required but not installed"; exit 1; }
	@echo "✅ All dependencies are installed"

# Download Go dependencies
deps:
	@echo "📦 Downloading Go dependencies..."
	go mod download
	go mod tidy
	@echo "✅ Dependencies downloaded"

# Build the application
build: deps
	@echo "🔨 Building application..."
	go build -o bin/go-bricks-demo-project ./cmd/api/
	@echo "✅ Build completed: bin/go-bricks-demo-project"

# Run the application locally (requires running databases).
#
# CORS_DEV_WILDCARD=true restores permissive dev CORS: as of go-bricks v0.50.0
# (ADR-038) dev wildcard CORS is opt-in — without it the server fails closed
# (no Access-Control-Allow-Origin) and logs a WARN at boot. Dev-only convenience;
# production should set an explicit allowlist via CORS_ORIGINS instead.
run: build
	@echo "🚀 Starting application..."
	@echo "Make sure services are running: make docker-up"
	unset DEBUG && APP_ENV=development \
	CORS_DEV_WILDCARD=true \
	./bin/go-bricks-demo-project

# Run tests
test:
	@echo "🧪 Running tests..."
	go test -v -race ./...
	@echo "✅ Tests completed"

# Clean build artifacts
clean:
	@echo "🧹 Cleaning build artifacts..."
	rm -rf bin/
	go clean -cache -testcache
	@echo "✅ Clean completed"

# Start all Docker services (defaults to local observability stack)
docker-up: docker-up-local

# Start Docker services with local observability stack (Prometheus + Grafana + Tempo + Loki)
docker-up-local: check-deps
	@echo "🐳 Starting Docker services with local observability stack..."
	docker-compose -f etc/docker/docker-compose.yml --env-file .env --profile local up -d
	@echo "⏳ Waiting for services to be ready..."
	@sleep 5
	@echo "✅ All services are running"
	@echo ""
	@echo "📋 Service URLs:"
	@echo "  PostgreSQL (main):    localhost:5432"
	@echo "  PostgreSQL (analytics): localhost:5433"
	@echo "  RabbitMQ AMQP:        localhost:5672"
	@echo "  RabbitMQ Streams:     localhost:5552"
	@echo "  RabbitMQ Management:  http://localhost:15672"
	@echo "  Prometheus:           http://localhost:9090"
	@echo "  Grafana:              http://localhost:3000 (admin/admin)"
	@echo "  Tempo:                http://localhost:3200"

# Start Docker services with New Relic observability stack (cloud)
docker-up-newrelic: check-deps
	@echo "🐳 Starting Docker services with New Relic observability stack..."
	@echo "⚠️  Requires NEW_RELIC_LICENSE_KEY in .env file"
	docker-compose -f etc/docker/docker-compose.yml --env-file .env --profile newrelic up -d
	@echo "⏳ Waiting for services to be ready..."
	@sleep 5
	@echo "✅ All services are running"
	@echo ""
	@echo "📋 Service URLs:"
	@echo "  PostgreSQL:           localhost:5432"
	@echo "  RabbitMQ AMQP:        localhost:5672"
	@echo "  RabbitMQ Management:  http://localhost:15672"
	@echo "  New Relic APM:        https://one.newrelic.com/nr1-core"

# Stop all Docker services
docker-down:
	@echo "🛑 Stopping Docker services..."
	docker-compose -f etc/docker/docker-compose.yml --env-file .env down -v
	@echo "✅ All services stopped"

# View logs from all services
logs:
	docker-compose -f etc/docker/docker-compose.yml --env-file .env logs -f

# Show service status
status:
	@echo "📊 Service Status:"
	@docker-compose -f etc/docker/docker-compose.yml --env-file .env ps

# Run database migrations using Flyway
migrate:
	@echo "🚀 Running database migrations..."
	docker-compose -f etc/docker/docker-compose.yml --env-file .env --profile migrations run --rm flyway migrate
	@echo "✅ Migrations completed"

# Show migration status
migrate-info:
	@echo "📊 Migration status..."
	docker-compose -f etc/docker/docker-compose.yml --env-file .env --profile migrations run --rm flyway info

# Run analytics database migrations using Flyway (named databases demo)
migrate-analytics:
	@echo "🚀 Running analytics database migrations..."
	docker-compose -f etc/docker/docker-compose.yml --env-file .env --profile migrations run --rm flyway-analytics migrate
	@echo "✅ Analytics migrations completed"

# Show analytics migration status
migrate-analytics-info:
	@echo "📊 Analytics migration status..."
	docker-compose -f etc/docker/docker-compose.yml --env-file .env --profile migrations run --rm flyway-analytics info

# Run all migrations (main + analytics)
migrate-all: migrate migrate-analytics
	@echo "✅ All migrations completed"

# ============================================================================
# Multi-tenant migration demo (schema-per-tenant via go-bricks-migrate)
# ============================================================================
# See wiki/MULTI_TENANT_MIGRATION_DEMO.md for the walkthrough.

MULTITENANT_CONFIG          := config.multitenant.yaml
MULTITENANT_FLYWAY_CONF     := flyway/flyway-multitenant.conf
MULTITENANT_MIGRATIONS_DIR  := migrations-multitenant
MULTITENANT_INIT_SQL        := etc/docker/postgres/multitenant-init.sql
MULTITENANT_FLYWAY_PATH     := scripts/flyway-docker.sh
MULTITENANT_POSTGRES_CONT   := go-bricks-postgres
GO_BRICKS_MIGRATE           := go-bricks-migrate
# Framework revision whose go-bricks-migrate CLI the demo targets. Must match the
# go-bricks version in go.mod: the `-url=` argv rewrite in
# scripts/flyway-docker.sh only fires against an ADR-085 CLI (go-bricks v0.61.0+),
# so a v0.60.0 pin here silently defeats it.
GO_BRICKS_REF               ?= v0.67.0

MULTITENANT_FLAGS := \
	--source-config $(MULTITENANT_CONFIG) \
	--credentials-from config-file \
	--flyway-config $(MULTITENANT_FLYWAY_CONF) \
	--migrations-dir $(MULTITENANT_MIGRATIONS_DIR) \
	--flyway-path $(MULTITENANT_FLYWAY_PATH) \
	--continue-on-error

# Internal: fail early with a friendly message if go-bricks-migrate is not on PATH.
migrate-multitenant-check:
	@command -v $(GO_BRICKS_MIGRATE) >/dev/null 2>&1 || { \
		echo "❌ $(GO_BRICKS_MIGRATE) not found on PATH"; \
		echo "   Install with: make migrate-multitenant-install"; \
		exit 1; \
	}

# Install the framework's multi-tenant migration CLI.
#
# The CLI lives in the independently-versioned tools/migration submodule, whose
# published go.mod can pin an older parent go-bricks than the CLI source needs,
# so a plain `go install ...@<ref>` from the module proxy may fail to compile or
# silently lack newer features. We therefore build from a CHECKOUT, where the
# repo's root go.work resolves the in-tree parent at $(GO_BRICKS_REF) — robust
# across versions (the v0.60.0 CLI was verified to build this way).
#
# The recipe passes GOWORK=<checkout>/go.work explicitly. Relying on go's
# go.work discovery made the result depend on the caller's environment: with
# GOWORK=off exported (as the demo's own scripts do), the build silently fell
# back to tools/migration/go.mod and linked its pinned parent (v0.66.0 for the
# v0.67.0 tag) instead of $(GO_BRICKS_REF). After installing, the recipe checks
# with `go version -m` that the binary's go-bricks dependency is the in-tree
# checkout, reported as (devel), and fails if it is not. The checkout path is
# resolved with `pwd -P`: go matches go.work's `use` entries against the
# physical working directory, and macOS's mktemp returns /var/..., a symlink
# to /private/var/....
#
# The clone is deliberately NOT `--depth 1 --branch $(GO_BRICKS_REF)`: `--branch`
# takes a branch or tag only, so it breaks whenever GO_BRICKS_REF is overridden
# with a commit hash (as it was pinned before the v0.63.0 tag existed). We clone
# the default branch with `--filter=blob:none` (full commit graph, blobs fetched
# lazily — so any commit OR tag is checkoutable, at roughly shallow-clone cost)
# and then check the ref out explicitly.
# Set GO_BRICKS_PATH to a pre-existing framework checkout to skip the clone.
migrate-multitenant-install:
	@echo "📦 Installing go-bricks-migrate..."
	@set -e; \
	if [ -n "$$GO_BRICKS_PATH" ] && [ -d "$$GO_BRICKS_PATH/tools/migration/cmd/go-bricks-migrate" ]; then \
		SRC=$$(cd "$$GO_BRICKS_PATH" && pwd -P); \
		echo "  using existing framework checkout: $$SRC"; \
	else \
		SRC=$$(cd "$$(mktemp -d)" && pwd -P); \
		trap 'rm -rf "$$SRC"' EXIT; \
		echo "  cloning framework into $$SRC, checking out $(GO_BRICKS_REF)"; \
		git clone --quiet --filter=blob:none https://github.com/gaborage/go-bricks.git "$$SRC"; \
		git -c advice.detachedHead=false -C "$$SRC" checkout --quiet "$(GO_BRICKS_REF)"; \
	fi; \
	if [ ! -f "$$SRC/go.work" ]; then \
		echo "❌ $$SRC/go.work not found: it is what builds the CLI against the in-tree go-bricks"; \
		exit 1; \
	fi; \
	GOWORK="$$SRC/go.work" go -C "$$SRC/tools/migration" install ./cmd/go-bricks-migrate; \
	BIN=$$(go env GOBIN); BIN=$${BIN:-$$(go env GOPATH | cut -d: -f1)/bin}; \
	if ! go version -m "$$BIN/go-bricks-migrate" | grep -Eq '^[[:space:]]+dep[[:space:]]+github.com/gaborage/go-bricks[[:space:]]+\(devel\)'; then \
		echo "❌ $$BIN/go-bricks-migrate did not link the in-tree go-bricks:"; \
		go version -m "$$BIN/go-bricks-migrate" | grep -E 'github.com/gaborage/go-bricks[[:space:]]' || true; \
		exit 1; \
	fi; \
	echo "  linked github.com/gaborage/go-bricks from $$SRC ($$(git -C "$$SRC" describe --tags --always 2>/dev/null))"
	@echo "✅ go-bricks-migrate installed (verify with: which go-bricks-migrate)"

# Apply the per-tenant role + schema bootstrap SQL against the running
# postgres container. Idempotent. Required before the first multi-tenant run.
migrate-multitenant-init: check-deps
	@echo "🏗  Bootstrapping multi-tenant roles + schemas in postgres..."
	@docker exec -i $(MULTITENANT_POSTGRES_CONT) psql -U postgres -d postgres -v ON_ERROR_STOP=1 < $(MULTITENANT_INIT_SQL)
	@echo "✅ Roles + schemas ready (acme, globex, initech)"

# Boot postgres and apply migrations to every tenant.
migrate-multitenant-up: docker-up migrate-multitenant-init migrate-multitenant-check
	@echo "🚀 Applying multi-tenant migrations..."
	$(GO_BRICKS_MIGRATE) migrate $(MULTITENANT_FLAGS)
	@echo "✅ Multi-tenant migrations applied"

migrate-multitenant-info: migrate-multitenant-check
	@echo "📊 Multi-tenant migration status..."
	$(GO_BRICKS_MIGRATE) info $(MULTITENANT_FLAGS)

migrate-multitenant-validate: migrate-multitenant-check
	@echo "🔍 Validating multi-tenant migrations..."
	$(GO_BRICKS_MIGRATE) validate $(MULTITENANT_FLAGS)

# ----------------------------------------------------------------------------
# Run verdicts and exit codes (go-bricks v0.67.0, ADR-115, #1770/#1771)
# ----------------------------------------------------------------------------
# make stops on ANY non-zero exit, so the targets above cannot tell exit 1
# (fleet split) from exit 2 (nothing attempted). This runs the CLI three ways
# with --json and prints each exit code and summary record: the real fleet
# (0, clean), an empty fleet (2, nothing_attempted) and the fleet plus one
# unreachable tenant (1, fleet_split). validate only: nothing is migrated.
# VERDICT_ACTION=info needs only migrate-multitenant-init. PG_PORT is honoured
# for a host Flyway only (MULTITENANT_FLYWAY_PATH=flyway); see the script header.
.PHONY: migrate-multitenant-verdict
migrate-multitenant-verdict: migrate-multitenant-check
	@echo "⚖️  Showing go-bricks-migrate run verdicts and exit codes..."
	@GO_BRICKS_MIGRATE=$(GO_BRICKS_MIGRATE) \
	MULTITENANT_CONFIG=$(MULTITENANT_CONFIG) \
	MULTITENANT_FLYWAY_CONF=$(MULTITENANT_FLYWAY_CONF) \
	MULTITENANT_MIGRATIONS_DIR=$(MULTITENANT_MIGRATIONS_DIR) \
	MULTITENANT_FLYWAY_PATH=$(MULTITENANT_FLYWAY_PATH) \
	./scripts/migrate-verdict-demo.sh

# ----------------------------------------------------------------------------
# Tenant roles at the privilege floor (go-bricks v0.66.0, #1718)
# ----------------------------------------------------------------------------
# cmd/check-tenant-roles logs in as each tenant of $(MULTITENANT_CONFIG) in a
# read-only session and asks migration.CheckPGRoleFloor about its role: OK, or
# the attributes it holds above the floor (SUPERUSER, CREATEDB, CREATEROLE,
# REPLICATION, BYPASSRLS). Exit 0 all at the floor, 1 not, 2 nothing checked.
# Unlike migrate-multitenant-init (docker exec) it dials from the HOST, so it is
# not chained into init: PG_HOST / PG_PORT, when set, replace every tenant's
# host / port for a postgres published somewhere other than localhost:5432.
# Built, not `go run`, because go run turns every non-zero exit into 1.
.PHONY: migrate-multitenant-check-roles
migrate-multitenant-check-roles:
	@echo "🔐 Checking tenant roles against the PostgreSQL privilege floor..."
	@go build -o bin/check-tenant-roles ./cmd/check-tenant-roles
	@set -- -config "$(MULTITENANT_CONFIG)"; \
	if [ -n "$$PG_HOST" ]; then set -- "$$@" -host "$$PG_HOST"; fi; \
	if [ -n "$$PG_PORT" ]; then set -- "$$@" -port "$$PG_PORT"; fi; \
	./bin/check-tenant-roles "$$@"

# Drop and recreate every tenant's schema. Useful between demo runs or when
# experimenting with broken migrations.
migrate-multitenant-reset:
	@echo "🧹 Dropping tenant schemas (acme, globex, initech)..."
	@./scripts/multitenant-reset.sh
	@echo "✅ Tenant schemas reset"

# Capture sample Flyway JSON outputs (7 scenarios) under samples/flyway-output/.
# Feeds the JSON-output parser design tracked in go-bricks#376.
migrate-multitenant-samples: docker-up migrate-multitenant-init
	@echo "📸 Capturing Flyway JSON output samples..."
	@./scripts/capture-flyway-samples.sh
	@echo "✅ Samples written to samples/flyway-output/"

# Format Go code
fmt:
	@echo "📝 Formatting Go code..."
	go fmt ./...
	@echo "✅ Code formatted"

# Run linting
lint:
	@echo "🔍 Running linters..."
	golangci-lint run
	@echo "✅ Linting completed"

# Generate test coverage
coverage:
	@echo "📊 Generating test coverage..."
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "✅ Coverage report generated: coverage.html"

# Pre-commit checks
check: fmt lint test
	@echo "✅ All checks passed!"

# Test products API endpoints
test-products-api:
	@echo "🧪 Testing products API..."
	@./scripts/test-products-api.sh

# --- Advisory-lock demo (products report job) --------------------------------
# Two-replica proof for the report job's PostgreSQL advisory lock, taken on a
# pinned database Session (go-bricks v0.65.0, ADR-112). Starts two extra app
# replicas on REPLICA_PORTS (default 8081 8082) with a lock hold, triggers the
# job on both at once, then waits for their own scheduled tick — exactly one
# replica runs the report each time. Requires infra up (make docker-up),
# migrations (make migrate) and keys (make generate-keys); the app on :8080 does
# not need to be running.
.PHONY: advisory-lock-demo
advisory-lock-demo: build
	@echo "🔒 Racing two replicas for the report job's advisory lock..."
	@./scripts/advisory-lock-demo.sh

# Broker-visibility proof for the sealed-messages demo: publish one
# PaymentAuthorized event, then read it off the consumerless tap queue via the
# RabbitMQ management API and assert the PAN never reaches the wire. Then open
# the same bytes with the framework's open-event CLI (go-bricks v0.65.0, #1640)
# and the consumer half of the keys — the card stays "<redacted>".
# Requires the app running (make run), infra up (make docker-up) and keys
# present (make generate-keys).
show-sealed-message:
	@echo "🔐 Inspecting a sealed message on the broker..."
	@./scripts/show-sealed-message.sh

# Consumer-side proof for the sealed-messages demo, using the framework's
# seal-event CLI (go-bricks v0.63.0, #1417) to mint event bodies OUTSIDE the app
# from the demo's own DER keys, then publishing them straight to the exchange.
# Shows three things the in-app POST flow cannot: an externally-minted event is
# opened, the SAME bytes published twice trip inbox dedup (the jti is stable per
# seal, while every HTTP call mints a fresh one), and a wrong -event-type is
# refused at open-rule 7 (SEAL_EVENT_TYPE_MISMATCH) and parks on the DLQ, where
# open-event reads the same code back off the parked bytes.
# Requires the app running (make run), infra up (make docker-up), the inbox
# ledger migrated (make migrate) and keys present (make generate-keys).
seal-event-demo:
	@echo "🔐 Minting sealed events outside the app with the seal-event CLI..."
	@./scripts/seal-event-demo.sh

# --- JOSE request bodies (framework seal-payload CLI) -------------------------
# Mint curl bodies for the tokens demo with the framework's seal-payload CLI
# (go-bricks v0.65.0, #1615/#1620), which replaced the demo-owned cmd/seal-payload.
# Both read a JSON payload on stdin and print ONLY the sealed body on stdout, so
# they pipe straight into `curl --data-binary @-` (README, Tokens walkthrough):
#   seal-payload  nested JWE-of-JWS for POST /api/v1/tokens: signs as tokens-peer,
#                 encrypts to tokens-our
#   seal-mle      Visa MLE {"encData":...} for POST /api/v1/__sim/peer/mle: bare
#                 A128GCM JWE (typ JOSE, ms iat) to tokens-peer, nothing signed
# scripts/seal-payload.sh reads the CLI version from go.mod, so there is no second
# pin to drift. Needs keys (make generate-keys); minting does not need the app.
.PHONY: seal-payload seal-mle
seal-payload:
	@./scripts/seal-payload.sh nested

seal-mle:
	@./scripts/seal-payload.sh mle

# --- Consumer-aware readiness (go-bricks v0.65.0, #1686/#1684, ADR-114) -------
# /ready fails closed once the payments.authorized consumer gives up
# re-subscribing. The script builds and boots its OWN app with
# MESSAGING_CONSUMERS_CRITICAL=true (config.development.yaml keeps the key off),
# so stop any `make run` first — it refuses a busy port. It revokes the app
# user's broker READ on payments.authorized only, closes the consumer's
# connection, and polls /ready while consumer_max_fail_streak climbs to 5 and
# the verdict turns 503. The recorded permissions are restored on every exit,
# then recovery is shown. The broker is never stopped: that would flip /ready
# through the publisher arm and hide the consumer arm.
# Requires infra up (make docker-up), migrations (make migrate) and keys
# (make generate-keys). Honors RABBIT_MGMT, RABBIT_CONTAINER, APP_URL and the
# app's own env overrides (DATABASE_PORT, MESSAGING_BROKER_URL, ...).
.PHONY: demo-consumer-readiness
demo-consumer-readiness:
	@echo "🩺 Demonstrating readiness that fails closed on a stalled consumer..."
	@./scripts/consumer-readiness-demo.sh

# --- External exchange demo (go-bricks v0.67.0, #1773/#1774, ADR-119) --------
# The partnerfeed module consumes partner-events, an exchange ANOTHER service
# owns: DeclareExternalExchange verifies it with a passive declare and never
# creates it. The script starts its OWN app instance, twice, with the module
# on (CUSTOM_PARTNERFEED_ENABLED=true) and plays the owning partner through the
# RabbitMQ management API: the exchange absent with externalwait 0 aborts startup
# on the broker's 404; with MESSAGING_DECLARE_EXTERNALWAIT=60s the app waits, the
# script creates the exchange, and startup completes; a published event is then
# consumed. Cleanup deletes what the run created. Requires infra up
# (make docker-up), migrations and keys — and NOT `make run`: it refuses when the
# app port is already taken. Honors APP_URL and RABBIT_MGMT overrides.
.PHONY: external-exchange-demo
external-exchange-demo: build
	@echo "🔌 Consuming from an exchange another service owns..."
	@./scripts/external-exchange-demo.sh

# Update dependencies to latest versions
update:
	@echo "📦 Updating dependencies..."
	go get -u ./...
	go mod tidy
	@echo "✅ Dependencies updated"

# Generate RSA key pairs for the KeyStore-backed demos:
#   - webhook-signing     : webhooks module (file/file)
#   - tokens-our          : tokens module, our half  (file/file)
#   - tokens-peer         : tokens module, peer half (value/file — public is inlined
#                           into config.development.yaml between BEGIN/END markers)
#   - payments-sign-v1    : payments module, sealed-event SIGN family generation v1
#   - payments-encrypt-v1 : payments module, sealed-event ENCRYPT family generation v1
#
# The two payments-* entries are sealing GENERATIONS (go-bricks v0.63.0, ADR-097):
# the "-v<N>" suffix is what gives the entry family semantics, so the seal tag names
# only the logical kid ("payments-sign") and rotation adds a -v2 entry rather than
# rewriting code. Both halves are generated for each family because this demo is
# producer AND consumer in one process (producer needs sign-private + encrypt-public,
# consumer needs sign-public + encrypt-private). A real deployment splits them.
generate-keys:
	@echo "🔑 Generating RSA key pairs..."
	@mkdir -p certs
	@openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -outform DER -out certs/webhook_signing_private.der 2>/dev/null
	@openssl rsa -in certs/webhook_signing_private.der -inform DER -pubout -outform DER -out certs/webhook_signing_public.der 2>/dev/null
	@openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -outform DER -out certs/tokens_our_private.der 2>/dev/null
	@openssl rsa -in certs/tokens_our_private.der -inform DER -pubout -outform DER -out certs/tokens_our_public.der 2>/dev/null
	@openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -outform DER -out certs/tokens_peer_private.der 2>/dev/null
	@openssl rsa -in certs/tokens_peer_private.der -inform DER -pubout -outform DER -out certs/tokens_peer_public.der 2>/dev/null
	@openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -outform DER -out certs/payments_sign_v1_private.der 2>/dev/null
	@openssl rsa -in certs/payments_sign_v1_private.der -inform DER -pubout -outform DER -out certs/payments_sign_v1_public.der 2>/dev/null
	@openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -outform DER -out certs/payments_encrypt_v1_private.der 2>/dev/null
	@openssl rsa -in certs/payments_encrypt_v1_private.der -inform DER -pubout -outform DER -out certs/payments_encrypt_v1_public.der 2>/dev/null
	@echo "🔁 Patching tokens-peer public key (base64) into config.development.yaml..."
	@grep -q 'BEGIN_TOKENS_PEER_PUB' config.development.yaml || { \
		echo "❌ config.development.yaml is missing the 'BEGIN_TOKENS_PEER_PUB' marker — refusing to silently skip the patch."; \
		exit 1; \
	}
	@grep -q 'END_TOKENS_PEER_PUB' config.development.yaml || { \
		echo "❌ config.development.yaml is missing the 'END_TOKENS_PEER_PUB' marker — refusing to silently skip the patch."; \
		exit 1; \
	}
	@PEER_PUB_B64=$$(base64 < certs/tokens_peer_public.der | tr -d '\n'); \
		awk -v key="$$PEER_PUB_B64" ' \
			/BEGIN_TOKENS_PEER_PUB/ {print; in_block=1; next} \
			/END_TOKENS_PEER_PUB/   {printf "        value: \"%s\"\n", key; print; in_block=0; next} \
			in_block {next} \
			{print}' config.development.yaml > config.development.yaml.tmp \
		&& mv config.development.yaml.tmp config.development.yaml
	@echo "✅ Keys generated in certs/ and base64 patched into config.development.yaml"
	@echo "   webhook-signing     : certs/webhook_signing_{public,private}.der"
	@echo "   tokens-our          : certs/tokens_our_{public,private}.der"
	@echo "   tokens-peer         : certs/tokens_peer_private.der (private)"
	@echo "                       : config.development.yaml between BEGIN_/END_TOKENS_PEER_PUB markers (public)"
	@echo "   payments-sign-v1    : certs/payments_sign_v1_{public,private}.der"
	@echo "   payments-encrypt-v1 : certs/payments_encrypt_v1_{public,private}.der"

# Development environment setup
dev: docker-up migrate-all generate-keys
	@echo "🚀 Development environment ready!"
	@echo ""
	@echo "Next steps:"
	@echo "  1. Run the application: make run"
	@echo "  2. Test the API:        make test-products-api"
	@echo ""
	@echo "📋 Useful endpoints:"
	@echo "  Health:     http://localhost:8080/api/v1/health"
	@echo "  Products:   http://localhost:8080/api/v1/products"
	@echo "  Analytics:  http://localhost:8080/api/v1/analytics/views"

# ============================================================================
# Load Testing Targets
# ============================================================================

# Check if k6 is installed
check-k6:
	@command -v k6 >/dev/null 2>&1 || { \
		echo "❌ k6 is not installed"; \
		echo ""; \
		echo "Install with: make loadtest-install"; \
		echo "Or manually: https://k6.io/docs/get-started/installation/"; \
		exit 1; \
	}

# Install k6 load testing tool
loadtest-install:
	@echo "🚀 Installing k6 load testing tool..."
	@./scripts/install-k6.sh

# Run CRUD mix load test (realistic production traffic)
loadtest-crud: check-k6
	@echo "🧪 Running CRUD mix load test..."
	@echo "This test simulates realistic production traffic with read/write operations"
	@echo ""
	@k6 run loadtests/products-crud.ts
	@echo ""
	@echo "✅ CRUD load test completed"

# Run read-only baseline test
loadtest-read: check-k6
	@echo "🧪 Running read-only baseline test..."
	@echo "This test establishes baseline performance for read operations"
	@echo ""
	@k6 run loadtests/products-read-only.ts
	@echo ""
	@echo "✅ Read-only load test completed"

# Run ramp-up test to find system limits
loadtest-ramp: check-k6
	@echo "🧪 Running ramp-up test..."
	@echo "This test gradually increases load to find breaking points"
	@echo "⚠️  Duration: ~17 minutes"
	@echo ""
	@k6 run loadtests/ramp-up-test.ts
	@echo ""
	@echo "✅ Ramp-up load test completed"

# Run spike test to validate resilience
loadtest-spike: check-k6
	@echo "🧪 Running spike test..."
	@echo "This test simulates sudden traffic spikes"
	@echo "⚠️  Duration: ~6 minutes"
	@echo ""
	@k6 run loadtests/spike-test.ts
	@echo ""
	@echo "✅ Spike load test completed"

# ----------------------------------------------------------------------------
# Topology self-repair (go-bricks v0.67.0, #1776/#1779, ADR-113 amendment)
# ----------------------------------------------------------------------------
# An exchange deleted under the live app now heals on the next publish: the
# publisher's replacement channel drives a redeclare pass of every exchange,
# queue and binding. Both targets delete exchanges through the RabbitMQ
# management API, so point them at a demo broker only, and both require the app
# running (make run) and infra up (make docker-up). Payment bodies carry
# documented test PANs and are never echoed or logged.
# Env: APP_URL (K6_BASE_URL for k6), RABBIT_MGMT, RABBIT_USER, RABBIT_PASS;
# see each script's header for the rest.
#
# redeclare-demo: delete payment-events, publish into the hole, show the
# exchange and both bindings back and a post-repair payment on
# payments.authorized.tap; then product-events, repaired by the outbox relay.
#
# loadtest-topology-repair: constant-arrival POST /products + POST
# /payments/authorize, both exchanges deleted at t=60s. Reports the repair
# times and the 202s that never reached the tap ("Lost in repair window", the
# documented ack-and-drop window: reported, never a failed threshold).
# Thresholds cover HTTP error rate and latency only. Destructive, so it is not
# part of loadtest-all.
.PHONY: redeclare-demo loadtest-topology-repair
redeclare-demo:
	@echo "🔧 Deleting exchanges under the live app and watching them self-repair..."
	@./scripts/topology-repair-demo.sh

loadtest-topology-repair: check-k6
	@echo "🧪 Running topology repair load test (exchange loss under load)..."
	@echo "⚠️  Duration: ~2.5 minutes; deletes product-events and payment-events midway"
	@echo ""
	@K6_BASE_URL="$${K6_BASE_URL:-$${APP_URL:-http://localhost:8080}}" k6 run loadtests/topology-repair.ts
	@echo ""
	@echo "✅ Topology repair load test completed"

# Run sustained load test to detect leaks
loadtest-sustained: check-k6
	@echo "🧪 Running sustained load test..."
	@echo "This test validates stability over extended duration"
	@echo "⚠️  Duration: ~17 minutes"
	@echo ""
	@k6 run loadtests/sustained-load.ts
	@echo ""
	@echo "✅ Sustained load test completed"

# Run all load tests in sequence
loadtest-all: check-k6
	@echo "🧪 Running all load tests in sequence..."
	@echo "⚠️  Total duration: ~60 minutes"
	@echo ""
	@echo "1/5: Read-only baseline test..."
	@k6 run loadtests/products-read-only.ts
	@echo ""
	@echo "2/5: CRUD mix test..."
	@k6 run loadtests/products-crud.ts
	@echo ""
	@echo "3/5: Spike test..."
	@k6 run loadtests/spike-test.ts
	@echo ""
	@echo "4/5: Ramp-up test..."
	@k6 run loadtests/ramp-up-test.ts
	@echo ""
	@echo "5/5: Sustained load test..."
	@k6 run loadtests/sustained-load.ts
	@echo ""
	@echo "✅ All load tests completed!"
	@echo "📊 Review results; each script's header under loadtests/ says what its scenario measures"

# Run a quick smoke test
loadtest-smoke: check-k6
	@echo "🧪 Running smoke test (quick validation)..."
	@k6 run --vus 1 --duration 30s loadtests/products-crud.ts
	@echo ""
	@echo "✅ Smoke test completed"

# Run the tokens relay (JOSE end-to-end) load test
loadtest-tokens: check-k6
	@echo "🧪 Running tokens relay load test (JOSE end-to-end)..."
	@echo "Each request triggers 4 JOSE ops across 2 HTTP hops (relay -> peer simulator)"
	@echo "⚠️  Duration: ~12 minutes (sustained profile, 50 VUs)"
	@echo ""
	@k6 run loadtests/tokens-relay.ts
	@echo ""
	@echo "✅ Tokens relay load test completed"

# Quick smoke validation of the tokens relay endpoint (good first run)
loadtest-tokens-smoke: check-k6
	@echo "🧪 Running tokens relay smoke test (quick validation)..."
	@k6 run --vus 1 --duration 30s loadtests/tokens-relay.ts
	@echo ""
	@echo "✅ Tokens relay smoke test completed"

# --- Tokens MLE relay (Visa Message Level Encryption) -------------------------
# POST /api/v1/tokens/mle-relay: bare JWE (A128GCM, no signature) inside the
# {"encData": ...} envelope, against the in-process /__sim/peer/mle simulator.
# K6_BASE_URL overrides the target (default http://localhost:8080).
.PHONY: loadtest-tokens-mle loadtest-tokens-mle-smoke

loadtest-tokens-mle: check-k6
	@echo "🧪 Running tokens MLE relay load test (bare JWE + encData envelope)..."
	@echo "⚠️  Duration: ~12 minutes (sustained profile, 50 VUs)"
	@echo ""
	@k6 run loadtests/tokens-mle-relay.ts
	@echo ""
	@echo "✅ Tokens MLE relay load test completed"

loadtest-tokens-mle-smoke: check-k6
	@echo "🧪 Running tokens MLE relay smoke test (quick validation)..."
	@k6 run --vus 1 --duration 30s loadtests/tokens-mle-relay.ts
	@echo ""
	@echo "✅ Tokens MLE relay smoke test completed"

# --- Tokens VTS Issuer relay (Visa Token Service Issuer, JWS-of-JWE) ----------
# POST /api/v1/tokens/vts-issuer-relay: encrypt (inner JWE, A256GCM), then sign
# (outer JWS, PS256), as application/jose both ways. The peer is the relay
# client's in-process base transport, so there is no /__sim/ route and no
# loopback hop. K6_BASE_URL overrides the target (default http://localhost:8080).
.PHONY: loadtest-tokens-vts loadtest-tokens-vts-smoke

loadtest-tokens-vts: check-k6
	@echo "🧪 Running tokens VTS Issuer relay load test (JWS-of-JWE)..."
	@echo "⚠️  Duration: ~12 minutes (sustained profile, 50 VUs)"
	@echo ""
	@k6 run loadtests/tokens-vts-issuer-relay.ts
	@echo ""
	@echo "✅ Tokens VTS Issuer relay load test completed"

loadtest-tokens-vts-smoke: check-k6
	@echo "🧪 Running tokens VTS Issuer relay smoke test (quick validation)..."
	@k6 run --vus 1 --duration 30s loadtests/tokens-vts-issuer-relay.ts
	@echo ""
	@echo "✅ Tokens VTS Issuer relay smoke test completed"

# Type check load test TypeScript files
loadtest-type-check:
	@echo "🔍 Type checking load tests..."
	@npm run type-check
	@echo "✅ Type check passed"

# Run all load tests with monitoring and automated analysis
loadtest-all-monitored:
	@echo "🔍 Running load tests with monitoring..."
	@echo "This will:"
	@echo "  - Monitor goroutines, memory, and DB connections"
	@echo "  - Run all 5 load tests (~60 minutes)"
	@echo "  - Generate automated analysis report"
	@echo ""
	@./scripts/run-loadtest-all-monitored.sh

# Start load test monitoring manually
loadtest-monitor:
	@echo "📊 Starting load test monitoring..."
	@echo "Metrics will be saved to loadtest-results/"
	@echo "Press Ctrl+C to stop"
	@echo ""
	@mkdir -p loadtest-results
	@./scripts/monitor-loadtest.sh loadtest-results/metrics-$$(date +%Y%m%d-%H%M%S).csv 10

# Analyze load test results
loadtest-analyze:
	@echo "📈 Analyzing load test results..."
	@if [ -z "$(FILE)" ]; then \
		echo "Usage: make loadtest-analyze FILE=loadtest-results/metrics-TIMESTAMP.csv"; \
		exit 1; \
	fi
	@./scripts/analyze-loadtest-results.sh $(FILE)
