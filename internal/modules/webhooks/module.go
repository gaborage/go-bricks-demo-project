// Package webhooks demonstrates the go-bricks KeyStore feature.
// It provides HTTP endpoints to sign and verify JSON payloads using
// named RSA key pairs configured in the keystore section of config.
package webhooks

import (
	"github.com/gaborage/go-bricks-demo-project/internal/modules/webhooks/handlers"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/webhooks/service"
	"github.com/gaborage/go-bricks/app"
	"github.com/gaborage/go-bricks/logger"
	"github.com/gaborage/go-bricks/messaging"
	"github.com/gaborage/go-bricks/server"
)

// Module showcases the KeyStore brick by exposing sign/verify endpoints.
type Module struct {
	handler *handlers.WebhookHandler
	logger  logger.Logger
}

// NewModule creates a new webhooks module instance.
func NewModule() *Module {
	return &Module{}
}

// Name returns the module name for registration.
func (m *Module) Name() string {
	return "webhooks"
}

// Init initializes the module with application dependencies.
// It wires: KeyStore → SigningService → WebhookHandler.
func (m *Module) Init(deps *app.ModuleDeps) error {
	m.logger = deps.Logger.WithFields(map[string]any{
		"module": "webhooks",
	})

	m.logger.Info().Msg("Initializing webhooks module")

	svc := service.NewSigningService(deps.KeyStore)
	m.handler = handlers.NewWebhookHandler(svc, m.logger)

	m.logger.Info().Msg("Webhooks module initialized — demonstrates KeyStore RSA signing")

	return nil
}

// RegisterRoutes registers HTTP endpoints for signing and verification.
func (m *Module) RegisterRoutes(hr *server.HandlerRegistry, r server.RouteRegistrar) {
	m.handler.RegisterRoutes(hr, r)
}

// DeclareMessaging declares messaging infrastructure for this module.
func (m *Module) DeclareMessaging(_ *messaging.Declarations) {
	// No messaging needed for webhooks module.
}

// RegisterJobs registers scheduled jobs for this module.
func (m *Module) RegisterJobs(_ app.JobRegistrar) error {
	// No scheduled jobs for webhooks module.
	return nil
}

// Shutdown performs cleanup when the module is stopped.
func (m *Module) Shutdown() error {
	return nil
}
