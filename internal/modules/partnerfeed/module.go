// Package partnerfeed demonstrates consuming from an exchange ANOTHER service
// owns (go-bricks v0.67.0, ADR-119).
//
// Every other exchange in this demo is declared by the module that publishes to
// it. Here the owner is a partner service that is not in this repository, so the
// module references partner-events with DeclareExternalExchange instead of
// declaring it: the framework verifies the exchange with a passive
// exchange.declare on every declare pass and never creates it, so the owner's
// shape is never raced. The queue, its dead-letter pair, the binding and the
// typed consumer are this service's own and are declared normally.
//
// The module is OFF by default and declares nothing until
// custom.partnerfeed.enabled is true. Default-off is what keeps plain `make run`
// working: no partner service exists locally, so an external exchange in the
// default boot would fail startup on the broker's 404.
// `make external-exchange-demo` switches it on for its own run only and plays the
// partner by creating the exchange through the management API.
package partnerfeed

import (
	"context"
	"time"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/partnerfeed/domain"
	"github.com/gaborage/go-bricks/app"
	"github.com/gaborage/go-bricks/logger"
	"github.com/gaborage/go-bricks/messaging"
	"github.com/gaborage/go-bricks/server"
)

// ConfigKeyEnabled switches the module on. A custom.* key so the framework's
// config loader carries it: YAML under `custom:` or, with the env mapping
// (lower-case, "_" -> "."), CUSTOM_PARTNERFEED_ENABLED=true. One word on purpose:
// an underscore in the key would be split into another level by that mapping.
const ConfigKeyEnabled = "custom.partnerfeed.enabled"

// consumerWorkers is 1 on purpose. A stock level is last-write-wins, and several
// workers would handle two updates for one SKU in either order. Raise it only
// once the handler orders updates itself (by OccurredAt or a partner version).
const consumerWorkers = 1

// Module implements app.Module and app.MessagingDeclarer.
type Module struct {
	logger  logger.Logger
	enabled bool
}

var _ app.MessagingDeclarer = (*Module)(nil)

// NewModule returns an unwired Module. Init populates dependencies.
func NewModule() *Module {
	return &Module{}
}

// Name implements app.Module.
func (m *Module) Name() string { return "partnerfeed" }

// Init reads the on/off switch. It runs at RegisterModule time, BEFORE the
// framework calls DeclareMessaging, so the switch is settled by the time the
// topology is collected.
func (m *Module) Init(deps *app.ModuleDeps) error {
	m.logger = deps.Logger.WithFields(map[string]any{"module": "partnerfeed"})
	m.enabled = deps.Config != nil && deps.Config.Bool(ConfigKeyEnabled, false)

	if !m.enabled {
		m.logger.Info().
			Str("switch", ConfigKeyEnabled).
			Msg("Partner feed module disabled — declares no topology (set CUSTOM_PARTNERFEED_ENABLED=true to consume partner-events)")
		return nil
	}

	m.logger.Info().
		Str("externalExchange", domain.ExchangeName).
		Str("queue", domain.QueueName).
		Str("routingKey", domain.RoutingKey).
		Msg("Partner feed module initialized — consuming an exchange another service owns")
	return nil
}

// RegisterRoutes registers nothing: the module's only surface is its consumer.
func (m *Module) RegisterRoutes(*server.HandlerRegistry, server.RouteRegistrar) {}

// DeclareMessaging references the partner's exchange and declares this
// service's side of the feed. A no-op while the module is disabled.
//
// What a missing exchange does. The passive declare answers 404 and ends the
// startup declare pass; because this module declares a consumer, startup then
// aborts rather than serving HTTP while consuming nothing. With
// messaging.declare.externalwait > 0 the framework re-runs the pass on a 404
// until the owner creates the exchange or the budget runs out — see the
// commented entry in config.development.yaml for what that costs.
func (m *Module) DeclareMessaging(decls *messaging.Declarations) {
	if !m.enabled {
		return
	}

	// Name only: a passive declare ignores type, flags and Args, so the helper
	// takes none, and Validate refuses an external declaration that carries any.
	partner := decls.DeclareExternalExchange(domain.ExchangeName)

	// The feed comes from outside this service, so a body that fails decode or
	// validation must park rather than vanish: the typed consumer nacks it
	// without requeue, and the DLQ keeps it for triage. QueueType is spelled out
	// for the same reason as the payments queue: it is the v0.64.0 default, and
	// an explicit value survives a change of default.
	queue := decls.DeclareQueueWithDLQ(domain.QueueName, &messaging.DeadLetterSpec{
		QueueType: messaging.QueueTypeQuorum,
	})
	decls.DeclareBinding(queue.Name, partner.Name, domain.RoutingKey)

	messaging.DeclareTypedConsumer(decls, &messaging.ConsumerOptions{
		Queue:       queue.Name,
		Consumer:    domain.ConsumerTag,
		EventType:   domain.EventType,
		Description: "Partner stock updates from an exchange the partner service owns",
		Workers:     consumerWorkers,
	}, m.onStockUpdated)
}

// onStockUpdated runs once the framework has decoded and validated the
// delivery, so every field is present and bounded here.
//
// Delivery is at-least-once: a redelivery after a crash, or a partner
// republishing, runs this again. Logging is idempotent, so the demo needs no
// guard. A handler that writes state would dedup first — for instance
// inbox.ProcessOnce keyed on the partner's eventId, which needs the
// DeclareTypedConsumerWithMeta door. It holds no mutable state, so it stays
// safe if consumerWorkers is ever raised.
//
//nolint:gocritic // hugeParam: the signature is the framework's typed-consumer contract, func(ctx, T) error.
func (m *Module) onStockUpdated(_ context.Context, evt domain.StockUpdated) error {
	m.logger.Info().
		Str("exchange", domain.ExchangeName).
		Str("eventId", evt.EventID).
		Str("partnerId", evt.PartnerID).
		Str("sku", evt.SKU).
		Int("quantity", *evt.Quantity). // non-nil: `required` ran before this handler
		Str("occurredAt", evt.OccurredAt.UTC().Format(time.RFC3339Nano)).
		Msg("Partner stock update consumed")
	return nil
}

// Shutdown is a no-op — nothing this module owns needs explicit teardown.
func (m *Module) Shutdown() error { return nil }
