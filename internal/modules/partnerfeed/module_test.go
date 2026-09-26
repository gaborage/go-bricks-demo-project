package partnerfeed

import (
	"testing"
	"time"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/partnerfeed/domain"
	"github.com/gaborage/go-bricks/app"
	"github.com/gaborage/go-bricks/config"
	"github.com/gaborage/go-bricks/logger"
	"github.com/gaborage/go-bricks/messaging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newModule runs Init against a config holding only the given keys, the way
// the framework would after loading YAML and the environment.
func newModule(t *testing.T, cfg map[string]any) *Module {
	t.Helper()
	loaded, err := config.LoadFromMap(cfg)
	require.NoError(t, err)

	m := NewModule()
	require.NoError(t, m.Init(&app.ModuleDeps{
		Logger: logger.New("error", false),
		Config: loaded,
	}))
	return m
}

func enabledModule(t *testing.T) *Module {
	t.Helper()
	return newModule(t, map[string]any{ConfigKeyEnabled: "true"}) // env spelling: a string
}

// Default-off is load-bearing: an external exchange in the default boot would
// fail `make run` on the broker's 404, because no partner service exists
// locally to create partner-events.
func TestModuleDeclaresNothingByDefault(t *testing.T) {
	m := newModule(t, map[string]any{})

	decls := messaging.NewDeclarations()
	m.DeclareMessaging(decls)

	assert.True(t, decls.IsEmpty(), "a disabled module must add no topology, got %+v", decls.Stats())
}

func TestModuleDeclaresNothingWhenSwitchedOffExplicitly(t *testing.T) {
	m := newModule(t, map[string]any{ConfigKeyEnabled: false})

	decls := messaging.NewDeclarations()
	m.DeclareMessaging(decls)

	assert.True(t, decls.IsEmpty())
}

func TestEnabledModuleReferencesThePartnerExchangeWithoutOwningIt(t *testing.T) {
	decls := messaging.NewDeclarations()
	enabledModule(t).DeclareMessaging(decls)

	require.NoError(t, decls.Validate(), "the external exchange must satisfy reference validation for the binding")

	partner, ok := decls.Exchanges[domain.ExchangeName]
	require.True(t, ok, "partner-events must be in the declaration set")
	assert.True(t, partner.Passive, "partner-events must be verified (passive), never created")
	// Name only: Validate refuses an external declaration carrying any shape,
	// and the broker would ignore it on a passive declare anyway.
	assert.Empty(t, partner.Type)
	assert.False(t, partner.Durable)
	assert.Empty(t, partner.Args)

	// Everything else is this service's own and is declared, not referenced.
	for name, exchange := range decls.Exchanges {
		if name != domain.ExchangeName {
			assert.False(t, exchange.Passive, "exchange %q is owned here and must be declared actively", name)
		}
	}
}

func TestEnabledModuleBindsItsOwnQueueWithAnExactKey(t *testing.T) {
	decls := messaging.NewDeclarations()
	enabledModule(t).DeclareMessaging(decls)

	_, ok := decls.Queues[domain.QueueName]
	require.True(t, ok, "the feed queue is owned here")
	_, ok = decls.Queues[domain.QueueName+".dlq"]
	assert.True(t, ok, "a feed from outside must park poison, not drop it")

	var bound bool
	for _, b := range decls.Bindings {
		if b.Queue == domain.QueueName && b.Exchange == domain.ExchangeName {
			bound = true
			assert.Equal(t, domain.RoutingKey, b.RoutingKey)
		}
	}
	assert.True(t, bound, "the queue must be bound to the partner exchange")
	assert.NotContains(t, domain.RoutingKey, "*", "an exact key routes the same on a direct or a topic exchange")
	assert.NotContains(t, domain.RoutingKey, "#")
}

func TestEnabledModuleDeclaresOneOrderedTypedConsumer(t *testing.T) {
	decls := messaging.NewDeclarations()
	enabledModule(t).DeclareMessaging(decls)

	consumers := decls.Consumers()
	require.Len(t, consumers, 1)
	c := consumers[0]
	assert.Equal(t, domain.QueueName, c.Queue)
	assert.Equal(t, domain.ConsumerTag, c.Consumer)
	assert.Equal(t, domain.EventType, c.EventType)
	assert.Equal(t, 1, c.Workers, "one worker keeps a SKU's updates in broker order")
	require.NotNil(t, c.Handler, "DeclareTypedConsumer builds the decode+validate handler")
	assert.Equal(t, domain.EventType, c.Handler.EventType())
}

// The trap this module is built around: every module shares one declaration
// set, so a name cannot be both owned in-process and marked external. If any
// module ever declared partner-events itself, startup would fail here.
func TestOwningThePartnerExchangeInProcessFailsValidation(t *testing.T) {
	decls := messaging.NewDeclarations()
	decls.DeclareTopicExchange(domain.ExchangeName) // a second module claiming ownership
	enabledModule(t).DeclareMessaging(decls)

	err := decls.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "declared locally and marked external in the same declaration set")
}

func TestOnStockUpdatedAcceptsAValidEvent(t *testing.T) {
	m := enabledModule(t)
	quantity := 0 // zero is a real stock level, not an absent field

	err := m.onStockUpdated(t.Context(), domain.StockUpdated{
		EventID:    "evt-1",
		PartnerID:  "acme-supply",
		SKU:        "SKU-001",
		Quantity:   &quantity,
		OccurredAt: time.Now(),
	})

	assert.NoError(t, err)
}
