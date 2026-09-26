package products

import (
	"context"
	"fmt"
	"time"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/products/handlers"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/products/job"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/products/repository"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/products/service"
	"github.com/gaborage/go-bricks/app"
	"github.com/gaborage/go-bricks/config"
	"github.com/gaborage/go-bricks/database"
	"github.com/gaborage/go-bricks/logger"
	"github.com/gaborage/go-bricks/messaging"
	"github.com/gaborage/go-bricks/server"
)

// productEventsExchange is the topic exchange the outbox relay publishes
// product lifecycle events to (outbox.defaultexchange in config.development.yaml).
const productEventsExchange = "product-events"

// reportHoldKey sets job.ReportJob.Hold (env CUSTOM_PRODUCTS_REPORT_HOLD). Unset
// means no hold; scripts/advisory-lock-demo.sh sets it on the replicas it starts.
const reportHoldKey = "custom.products.report.hold"

// Module demonstrates multi-tenant database operations with tenant-specific isolation
type Module struct {
	deps         *app.ModuleDeps
	service      *service.ProductService
	handler      *handlers.ProductHandler
	repo         repository.ProductRepository
	logger       logger.Logger
	getDB        func(context.Context) (database.Interface, error)
	getMessaging func(context.Context) (messaging.AMQPClient, error)
	reportHold   time.Duration
}

// Compile-time guard: the framework finds the declarer by type assertion, and
// v0.64.0 logs a per-module declaration count at startup — a mis-signatured or
// misspelled DeclareMessaging would otherwise register fine and declare nothing.
var _ app.MessagingDeclarer = (*Module)(nil)

// NewModule creates a new tenant module instance
func NewModule() *Module {
	return &Module{}
}

// Name returns the module name for registration
func (m *Module) Name() string {
	return "products"
}

// Init initializes the module with application dependencies
func (m *Module) Init(deps *app.ModuleDeps) error {
	m.logger = deps.Logger.WithFields(map[string]any{
		"module": "products",
	})

	// Setup functions to get context-dependent resources
	m.getDB = deps.DB
	m.getMessaging = deps.Messaging

	m.logger.Info().Msg("Initializing products module")

	// Parsed here, not in the job, so a bad value fails startup instead of every tick.
	hold, err := reportHold(deps.Config)
	if err != nil {
		return err
	}
	m.reportHold = hold

	m.logger.Info().Msg("Using existing database schema for products")

	// Initialize repository, service, jobs and handler
	m.repo = *repository.NewSQLProductRepository(m.getDB)
	m.service = service.NewService(&m.repo, m.logger, deps.Outbox, deps.DB)
	m.handler = handlers.NewProductHandler(m.service, m.logger)

	m.logger.Info().Msg("Products module initialized successfully")

	return nil
}

// SetActivityRecorder injects the product-activity stream recorder into the
// product service. Valid only after Init has built that service — so the caller
// must have seen this module ENABLED — and it must be called during startup
// (cmd/api/main.go does so right after module registration, before app.Run()).
//
// The framework has no ModuleDeps field for a cross-module seam, so the wiring is
// explicit at the composition root rather than discovered at runtime. The seam's
// interface and payload are declared in this module's service package: products
// is the core module and must build without the demo activity module.
func (m *Module) SetActivityRecorder(r service.ActivityRecorder) {
	m.service.SetActivityRecorder(r)
}

// RegisterRoutes registers HTTP endpoints for tenant operations
func (m *Module) RegisterRoutes(hr *server.HandlerRegistry, r server.RouteRegistrar) {
	// Registrar rutas HTTP para operaciones de productos
	m.handler.RegisterProductRoutes(hr, r)
}

// DeclareMessaging declares messaging infrastructure for this module
func (m *Module) DeclareMessaging(decls *messaging.Declarations) {
	// Declare the exchange used by outbox events for product lifecycle events.
	// The typed helper stores the same shape the old hand-built literal did —
	// durable topic, not auto-delete, not internal, no args — so a broker that
	// already holds product-events sees an equivalent redeclare.
	decls.DeclareTopicExchange(productEventsExchange)
}

func (m *Module) RegisterJobs(scheduler app.JobRegistrar) error {
	// Register scheduled jobs. Every replica ticks; the job's advisory lock picks
	// the one that runs (see job.ReportJob).
	return scheduler.FixedRate("test-job", &job.ReportJob{Hold: m.reportHold}, 30*time.Second)
}

// reportHold reads custom.products.report.hold as a Go duration ("6s"). Absent
// or empty means zero; a malformed or negative value is a startup error.
func reportHold(cfg *config.Config) (time.Duration, error) {
	if cfg == nil {
		return 0, nil
	}
	return parseReportHold(cfg.String(reportHoldKey))
}

func parseReportHold(raw string) (time.Duration, error) {
	if raw == "" {
		return 0, nil
	}
	hold, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("products: %s: %w", reportHoldKey, err)
	}
	if hold < 0 {
		return 0, fmt.Errorf("products: %s must not be negative", reportHoldKey)
	}
	return hold, nil
}

// Shutdown performs cleanup when the module is stopped
func (m *Module) Shutdown() error {
	return nil
}
