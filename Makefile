# Go Bricks Demo Project Makefile

.PHONY: help build run test clean docker-up docker-down logs status check-deps deps fmt lint coverage check migrate test-products-api loadtest-install loadtest-crud loadtest-read loadtest-ramp loadtest-spike loadtest-sustained loadtest-all loadtest-all-monitored loadtest-monitor loadtest-analyze

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
	@echo "  docker-up         Start all services (PostgreSQL + RabbitMQ)"
	@echo "  docker-down       Stop all services"
	@echo "  logs              View logs from all services"
	@echo "  status            Show service status"
	@echo ""
	@echo "Database targets:"
	@echo "  migrate           Run database migrations (Flyway)"
	@echo "  migrate-info      Show migration status"
	@echo ""
	@echo "Development targets:"
	@echo "  fmt               Format Go code"
	@echo "  lint              Run linters"
	@echo "  coverage          Generate test coverage report"
	@echo "  check             Run fmt, lint, and test (pre-commit)"
	@echo ""
	@echo "API Testing:"
	@echo "  test-products-api Test products API endpoints"
	@echo ""
	@echo "Load Testing:"
	@echo "  loadtest-install          Install k6 load testing tool"
	@echo "  loadtest-crud             Run CRUD mix load test"
	@echo "  loadtest-read             Run read-only baseline test"
	@echo "  loadtest-ramp             Run ramp-up test (find limits)"
	@echo "  loadtest-spike            Run spike test (traffic bursts)"
	@echo "  loadtest-sustained        Run sustained load test (15min)"
	@echo "  loadtest-all              Run all load tests in sequence"
	@echo "  loadtest-all-monitored    Run all tests with monitoring & analysis"
	@echo "  loadtest-monitor          Start manual monitoring"
	@echo "  loadtest-analyze FILE=... Analyze metrics file"

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
	go build -o bin/go-bricks-demo-project ./cmd/api/main.go
	@echo "✅ Build completed: bin/go-bricks-demo-project"

# Run the application locally (requires running databases)
run: build
	@echo "🚀 Starting application..."
	@echo "Make sure services are running: make docker-up"
	unset DEBUG && APP_ENV=development \
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

# Start all Docker services
docker-up: check-deps
	@echo "🐳 Starting Docker services..."
	docker-compose -f etc/docker/docker-compose.yml up -d
	@echo "⏳ Waiting for services to be ready..."
	@sleep 5
	@echo "✅ All services are running"
	@echo ""
	@echo "📋 Service URLs:"
	@echo "  PostgreSQL:           localhost:5432"
	@echo "  RabbitMQ AMQP:        localhost:5672"
	@echo "  RabbitMQ Management:  http://localhost:15672"

# Stop all Docker services
docker-down:
	@echo "🛑 Stopping Docker services..."
	docker-compose -f etc/docker/docker-compose.yml down -v
	@echo "✅ All services stopped"

# View logs from all services
logs:
	docker-compose -f etc/docker/docker-compose.yml logs -f

# Show service status
status:
	@echo "📊 Service Status:"
	@docker-compose -f etc/docker/docker-compose.yml ps

# Run database migrations using Flyway
migrate:
	@echo "🚀 Running database migrations..."
	docker-compose -f etc/docker/docker-compose.yml --profile migrations run --rm flyway migrate
	@echo "✅ Migrations completed"

# Show migration status
migrate-info:
	@echo "📊 Migration status..."
	docker-compose -f etc/docker/docker-compose.yml --profile migrations run --rm flyway info

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

# Update dependencies to latest versions
update:
	@echo "📦 Updating dependencies..."
	go get -u ./...
	go mod tidy
	@echo "✅ Dependencies updated"

# Development environment setup
dev: docker-up migrate
	@echo "🚀 Development environment ready!"
	@echo ""
	@echo "Next steps:"
	@echo "  1. Run the application: make run"
	@echo "  2. Test the API:        make test-products-api"
	@echo ""
	@echo "📋 Useful endpoints:"
	@echo "  Health:    http://localhost:8080/health"
	@echo "  Products:  http://localhost:8080/api/v1/products"

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
	@k6 run loadtests/products-crud.js
	@echo ""
	@echo "✅ CRUD load test completed"

# Run read-only baseline test
loadtest-read: check-k6
	@echo "🧪 Running read-only baseline test..."
	@echo "This test establishes baseline performance for read operations"
	@echo ""
	@k6 run loadtests/products-read-only.js
	@echo ""
	@echo "✅ Read-only load test completed"

# Run ramp-up test to find system limits
loadtest-ramp: check-k6
	@echo "🧪 Running ramp-up test..."
	@echo "This test gradually increases load to find breaking points"
	@echo "⚠️  Duration: ~17 minutes"
	@echo ""
	@k6 run loadtests/ramp-up-test.js
	@echo ""
	@echo "✅ Ramp-up load test completed"

# Run spike test to validate resilience
loadtest-spike: check-k6
	@echo "🧪 Running spike test..."
	@echo "This test simulates sudden traffic spikes"
	@echo "⚠️  Duration: ~6 minutes"
	@echo ""
	@k6 run loadtests/spike-test.js
	@echo ""
	@echo "✅ Spike load test completed"

# Run sustained load test to detect leaks
loadtest-sustained: check-k6
	@echo "🧪 Running sustained load test..."
	@echo "This test validates stability over extended duration"
	@echo "⚠️  Duration: ~17 minutes"
	@echo ""
	@k6 run loadtests/sustained-load.js
	@echo ""
	@echo "✅ Sustained load test completed"

# Run all load tests in sequence
loadtest-all: check-k6
	@echo "🧪 Running all load tests in sequence..."
	@echo "⚠️  Total duration: ~60 minutes"
	@echo ""
	@echo "1/5: Read-only baseline test..."
	@k6 run loadtests/products-read-only.js
	@echo ""
	@echo "2/5: CRUD mix test..."
	@k6 run loadtests/products-crud.js
	@echo ""
	@echo "3/5: Spike test..."
	@k6 run loadtests/spike-test.js
	@echo ""
	@echo "4/5: Ramp-up test..."
	@k6 run loadtests/ramp-up-test.js
	@echo ""
	@echo "5/5: Sustained load test..."
	@k6 run loadtests/sustained-load.js
	@echo ""
	@echo "✅ All load tests completed!"
	@echo "📊 Review results and see wiki/LOAD_TESTING.md for analysis guidance"

# Run a quick smoke test
loadtest-smoke: check-k6
	@echo "🧪 Running smoke test (quick validation)..."
	@k6 run --vus 1 --duration 30s loadtests/products-crud.js
	@echo ""
	@echo "✅ Smoke test completed"

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
