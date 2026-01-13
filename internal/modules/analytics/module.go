// Package analytics demonstrates the go-bricks named databases feature.
// It stores product view analytics in a separate "analytics" database,
// accessed via deps.DBByName(ctx, "analytics") instead of deps.DB(ctx).
package analytics

import (
	"context"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/analytics/handlers"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/analytics/repository"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/analytics/service"
	"github.com/gaborage/go-bricks/app"
	"github.com/gaborage/go-bricks/database"
	"github.com/gaborage/go-bricks/logger"
	"github.com/gaborage/go-bricks/messaging"
	"github.com/gaborage/go-bricks/server"
)

const (
	// analyticsDBName is the name of the named database in config.yaml.
	// This matches the key under the "databases:" section in config.development.yaml.
	analyticsDBName = "analytics"
)

// Module demonstrates the go-bricks named databases feature.
// Unlike the products module which uses the default database via deps.DB,
// this module uses a named database via deps.DBByName to store analytics data
// in a separate PostgreSQL instance.
type Module struct {
	deps    *app.ModuleDeps
	service *service.AnalyticsService
	handler *handlers.AnalyticsHandler
	repo    repository.Repository
	logger  logger.Logger

	// getAnalyticsDB retrieves the analytics database connection.
	// This uses DBByName to access the named database configured under "databases.analytics".
	getAnalyticsDB func(context.Context) (database.Interface, error)
}

// NewModule creates a new analytics module instance.
func NewModule() *Module {
	return &Module{}
}

// Name returns the module name for registration.
func (m *Module) Name() string {
	return "analytics"
}

// Init initializes the module with application dependencies.
// This demonstrates the DBByName pattern for accessing named databases.
func (m *Module) Init(deps *app.ModuleDeps) error {
	m.deps = deps
	m.logger = deps.Logger.WithFields(map[string]any{
		"module": "analytics",
	})

	m.logger.Info().Msg("Initializing analytics module")

	// KEY PATTERN: Create a wrapper function that calls DBByName with the analytics database name.
	// This is the core demonstration of the named databases feature.
	//
	// Unlike the products module which uses:
	//   m.getDB = deps.DB  // Gets the default database from "database:" config
	//
	// We use:
	//   m.getAnalyticsDB = func(ctx) { return deps.DBByName(ctx, "analytics") }
	//   // Gets the named database from "databases.analytics:" config
	m.getAnalyticsDB = func(ctx context.Context) (database.Interface, error) {
		return deps.DBByName(ctx, analyticsDBName)
	}

	m.logger.Info().
		Str("database", analyticsDBName).
		Msg("Using named database for analytics - demonstrates go-bricks DBByName feature")

	// Initialize repository with the analytics database getter.
	// The repository will use this function to get connections to the analytics database.
	m.repo = repository.NewAnalyticsRepository(m.getAnalyticsDB)

	// Initialize service and handler.
	m.service = service.NewService(m.repo, m.logger)
	m.handler = handlers.NewAnalyticsHandler(m.service, m.logger)

	m.logger.Info().Msg("Analytics module initialized successfully")

	return nil
}

// RegisterRoutes registers HTTP endpoints for analytics operations.
func (m *Module) RegisterRoutes(hr *server.HandlerRegistry, r server.RouteRegistrar) {
	m.handler.RegisterRoutes(hr, r)
}

// DeclareMessaging declares messaging infrastructure for this module.
func (m *Module) DeclareMessaging(_ *messaging.Declarations) {
	// No messaging needed for analytics module.
}

// RegisterJobs registers scheduled jobs for this module.
func (m *Module) RegisterJobs(_ app.JobRegistrar) error {
	// No scheduled jobs for analytics module.
	return nil
}

// Shutdown performs cleanup when the module is stopped.
func (m *Module) Shutdown() error {
	m.logger.Info().Msg("Shutting down analytics module")
	return nil
}
