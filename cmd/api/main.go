// Package main is the entry point for the go-bricks demo API application.
package main

import (
	"github.com/gaborage/go-bricks-demo-project/internal/modules/activity"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/analytics"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/legacy"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/payments"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/products"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/tokens"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/webhooks"
	"github.com/gaborage/go-bricks/app"
	"github.com/gaborage/go-bricks/inbox"
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

	// Concrete handles kept so the products → activity seam can be wired below.
	// Everything else reaches modules through app.ModuleDeps.
	productsModule := products.NewModule()
	activityModule := activity.NewModule()

	modulesToLoad := getModulesToLoad(productsModule, activityModule)

	if err := registerModules(application, modulesToLoad, log); err != nil {
		log.Fatal().Err(err).Msg("Failed to register modules")
	}

	// Cross-module wiring. It happens HERE, at the composition root, because the
	// framework has no ModuleDeps field for a module-to-module seam — and it
	// happens AFTER registration because that is when Init has run on both
	// modules and their services exist. It is still before app.Run(), so the
	// write lands before the server goroutine that will read it is spawned.
	//
	// Both ends must be ENABLED: registerModules skips Init for a disabled module,
	// which leaves its service nil. Wiring unconditionally would either call a
	// setter on a products module that has no service, or hand products a recorder
	// backed by nothing. (The activity module's Recorder() is nil-safe on its own
	// side too — belt and braces on a seam whose failure mode is a panic on the
	// first product write.)
	//
	// Every successful product write then also lands on the product-activity super
	// stream. A failure on that lane is logged and swallowed: the transactional
	// outbox remains the reliable path for product lifecycle events.
	if moduleEnabled(modulesToLoad, "products") && moduleEnabled(modulesToLoad, "activity") {
		productsModule.SetActivityRecorder(activityModule.Recorder())
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

// getModulesToLoad takes the two modules main() holds concrete handles to; the
// rest are constructed inline.
func getModulesToLoad(productsModule *products.Module, activityModule *activity.Module) []ModuleConfig {
	return []ModuleConfig{
		// --- Framework modules (order matters: scheduler → outbox → inbox → keystore) ---
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
			// Inbox provides consumer-side exactly-once processing: ProcessOnce
			// commits the handler's writes and the dedup row in one transaction.
			// Must be registered before any module that consumes with it (payments).
			Name:    "inbox",
			Enabled: true,
			Module:  inbox.NewModule(),
		},
		{
			// KeyStore loads named RSA key pairs from DER files at startup.
			// Used by the webhooks module for payload signing/verification, the
			// tokens module's JOSE routes, and the payments module's sealed events.
			Name:    "keystore",
			Enabled: true,
			Module:  keystore.NewModule(),
		},

		// --- Business modules ---
		{
			Name:    "products",
			Enabled: true,
			Module:  productsModule,
		},
		{
			// Activity module demonstrates the RabbitMQ streams lane (native
			// stream protocol) over a 3-partition super stream: a publisher keyed
			// by product id, a typed super-stream consumer projecting every
			// partition, and a poison simulator. Importing it links the stream
			// runtime (ADR-091) — messaging.streams.uri must be configured.
			Name:    "activity",
			Enabled: true,
			Module:  activityModule,
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
		{
			// Tokens module demonstrates the JOSE middleware (nested JWE-of-JWS)
			// and the httpclient JOSETransport against an in-process peer simulator.
			// Requires the keystore module above to populate deps.KeyStore.
			Name:    "tokens",
			Enabled: true,
			Module:  tokens.NewModule(),
		},
		{
			// Payments module demonstrates sealed AMQP messages (ADR-097): the
			// event's seal tags encrypt the card Subject and sign the document,
			// and the consumer dedups on the verified envelope's jti.
			// Requires the keystore module (key material) and the inbox module
			// (exactly-once ledger) above.
			Name:    "payments",
			Enabled: true,
			Module:  payments.NewModule(),
		},
	}
}

// moduleEnabled reports whether the named module is present and enabled in the
// registration list — the same list registerModules walks, so the two can never
// disagree about which modules actually got their Init called.
func moduleEnabled(modules []ModuleConfig, name string) bool {
	for _, mod := range modules {
		if mod.Name == name {
			return mod.Enabled
		}
	}
	return false
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
