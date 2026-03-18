// Package main is the entry point for the go-bricks demo API application.
package main

import (
	"github.com/gaborage/go-bricks-demo-project/internal/modules/analytics"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/legacy"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/products"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/webhooks"
	"github.com/gaborage/go-bricks/app"
	"github.com/gaborage/go-bricks/keystore"
	"github.com/gaborage/go-bricks/logger"
	"github.com/gaborage/go-bricks/outbox"
	"github.com/gaborage/go-bricks/scheduler"
)

func main() {
	// Create application instance with environment-based configuration
	application, log, err := app.New()
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to initialize application")
	}

	modulesToLoad := getModulesToLoad()

	if err := registerModules(application, modulesToLoad, log); err != nil {
		log.Fatal().Err(err).Msg("Failed to register modules")
	}

	if err := application.Run(); err != nil {
		log.Fatal().Err(err).Msg("Failed to start application")
	}
}

type ModuleConfig struct {
	Name    string
	Enabled bool
	Module  app.Module
}

func getModulesToLoad() []ModuleConfig {
	return []ModuleConfig{
		// --- Framework modules (order matters: scheduler → outbox → keystore) ---
		{
			// Scheduler provides cron/fixed-rate job execution.
			// Must be registered before outbox (the relay runs as a scheduled job).
			Name:    "scheduler",
			Enabled: true,
			Module:  scheduler.NewModule(),
		},
		{
			// Outbox provides transactional event publishing (dual-write pattern).
			// Events written inside a DB transaction are reliably relayed to RabbitMQ.
			Name:    "outbox",
			Enabled: true,
			Module:  outbox.NewModule(),
		},
		{
			// KeyStore loads named RSA key pairs from DER files at startup.
			// Used by the webhooks module for payload signing/verification.
			Name:    "keystore",
			Enabled: true,
			Module:  keystore.NewModule(),
		},

		// --- Business modules ---
		{
			Name:    "products",
			Enabled: true,
			Module:  products.NewModule(),
		},
		{
			// Analytics module demonstrates the go-bricks named databases feature.
			// It uses deps.DBByName(ctx, "analytics") to connect to a separate database.
			Name:    "analytics",
			Enabled: true,
			Module:  analytics.NewModule(),
		},
		{
			// Legacy module demonstrates WithRawResponse() for Strangler Fig migrations.
			// Routes bypass the standard APIResponse envelope, returning JSON directly.
			Name:    "legacy",
			Enabled: true,
			Module:  legacy.NewModule(),
		},
		{
			// Webhooks module demonstrates KeyStore RSA signing/verification.
			Name:    "webhooks",
			Enabled: true,
			Module:  webhooks.NewModule(),
		},
	}
}

func registerModules(appInstance *app.App, modules []ModuleConfig, log logger.Logger) error {
	for _, mod := range modules {
		if !mod.Enabled {
			log.Info().Str("Module %s is disabled, skipping registration", mod.Name)
			continue
		}

		log.Info().Str("Registering module: %s", mod.Name)
		if err := appInstance.RegisterModule(mod.Module); err != nil {
			return err
		}
		log.Info().Str("Module %s registered successfully", mod.Name)
	}

	return nil
}
