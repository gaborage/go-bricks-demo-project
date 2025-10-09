# Go Bricks Demo Project Makefile

.PHONY: help build run test clean docker-up docker-down logs status check-deps deps fmt lint coverage check migrate test-products-api

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
	docker-compose up -d
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
	docker-compose down -v
	@echo "✅ All services stopped"

# View logs from all services
logs:
	docker-compose logs -f

# Show service status
status:
	@echo "📊 Service Status:"
	@docker-compose ps

# Run database migrations using Flyway
migrate:
	@echo "🚀 Running database migrations..."
	docker-compose --profile migrations run --rm flyway migrate
	@echo "✅ Migrations completed"

# Show migration status
migrate-info:
	@echo "📊 Migration status..."
	docker-compose --profile migrations run --rm flyway info

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
