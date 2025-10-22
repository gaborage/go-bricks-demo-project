package products

import (
	"context"
	"time"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/products/http"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/products/job"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/products/repository"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/products/service"
	"github.com/gaborage/go-bricks/app"
	"github.com/gaborage/go-bricks/database"
	"github.com/gaborage/go-bricks/logger"
	"github.com/gaborage/go-bricks/messaging"
	"github.com/gaborage/go-bricks/server"
)

// Module demonstrates multi-tenant database operations with tenant-specific isolation
type Module struct {
	deps         *app.ModuleDeps
	service      *service.ProductService
	handler      *http.ProductHandler
	repo         repository.ProductRepository
	logger       logger.Logger
	getDB        func(context.Context) (database.Interface, error)
	getMessaging func(context.Context) (messaging.AMQPClient, error)
}

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
	m.getDB = deps.GetDB
	m.getMessaging = deps.GetMessaging

	m.logger.Info().Msg("Initializing products module")

	m.logger.Info().Msg("Using existing database schema for products")

	// Initialize repository, service, jobs and handler
	m.repo = *repository.NewSQLProductRepository(m.getDB)
	m.service = service.NewService(&m.repo, m.logger)
	m.handler = http.NewProductHandler(m.service, m.logger)

	m.logger.Info().Msg("Products module initialized successfully")

	return nil
}

// RegisterRoutes registers HTTP endpoints for tenant operations
func (m *Module) RegisterRoutes(hr *server.HandlerRegistry, r server.RouteRegistrar) {
	// Registrar rutas HTTP para operaciones de productos
	m.handler.RegisterProductRoutes(hr, r)
}

// DeclareMessaging declares messaging infrastructure for this module
func (m *Module) DeclareMessaging(_ *messaging.Declarations) {
	// No messaging needed for this example
}

func (m *Module) RegisterJobs(scheduler app.JobRegistrar) error {
	// Register scheduled jobs
	return scheduler.FixedRate("test-job", &job.ReportJob{}, 30*time.Second)
}

// Shutdown performs cleanup when the module is stopped
func (m *Module) Shutdown() error {
	return nil
}
