// Package legacy demonstrates the go-bricks WithRawResponse() feature.
// It serves product data without the standard APIResponse envelope,
// which is useful for Strangler Fig migrations where legacy endpoints
// must return their original response format.
//
// This module reuses the existing products service and repository,
// demonstrating cross-module service reuse — itself a valuable
// architectural pattern in go-bricks applications.
package legacy

import (
	"context"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/legacy/handlers"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/products/repository"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/products/service"
	"github.com/gaborage/go-bricks/app"
	"github.com/gaborage/go-bricks/database"
	"github.com/gaborage/go-bricks/logger"
	"github.com/gaborage/go-bricks/messaging"
	"github.com/gaborage/go-bricks/server"
)

// Module demonstrates WithRawResponse() for Strangler Fig migration patterns.
// It reuses the products service/repository to serve the same data
// without the APIResponse envelope wrapping.
type Module struct {
	handler *handlers.LegacyHandler
	logger  logger.Logger
	getDB   func(context.Context) (database.Interface, error)
}

// NewModule creates a new legacy module instance.
func NewModule() *Module {
	return &Module{}
}

// Name returns the module name for registration.
func (m *Module) Name() string {
	return "legacy"
}

// Init initializes the module with application dependencies.
// It wires: getDB → ProductRepository → ProductService → LegacyHandler.
func (m *Module) Init(deps *app.ModuleDeps) error {
	m.logger = deps.Logger.WithFields(map[string]any{
		"module": "legacy",
	})

	m.logger.Info().Msg("Initializing legacy module")

	m.getDB = deps.DB

	// Reuse existing products repository and service
	repo := repository.NewSQLProductRepository(m.getDB)
	svc := service.NewService(repo, m.logger)
	m.handler = handlers.NewLegacyHandler(svc, m.logger)

	m.logger.Info().Msg("Legacy module initialized successfully — demonstrates WithRawResponse()")

	return nil
}

// RegisterRoutes registers HTTP endpoints that bypass the APIResponse envelope.
func (m *Module) RegisterRoutes(hr *server.HandlerRegistry, r server.RouteRegistrar) {
	m.handler.RegisterRoutes(hr, r)
}

// DeclareMessaging declares messaging infrastructure for this module.
func (m *Module) DeclareMessaging(_ *messaging.Declarations) {
	// No messaging needed for legacy module.
}

// RegisterJobs registers scheduled jobs for this module.
func (m *Module) RegisterJobs(_ app.JobRegistrar) error {
	// No scheduled jobs for legacy module.
	return nil
}

// Shutdown performs cleanup when the module is stopped.
func (m *Module) Shutdown() error {
	return nil
}
