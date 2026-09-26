package main

import (
	"reflect"
	"testing"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/activity"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/partnerfeed"
	partnerdomain "github.com/gaborage/go-bricks-demo-project/internal/modules/partnerfeed/domain"
	paymentsdomain "github.com/gaborage/go-bricks-demo-project/internal/modules/payments/domain"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/products"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/tokens"
	"github.com/gaborage/go-bricks/app"
	"github.com/gaborage/go-bricks/config"
	inboxtest "github.com/gaborage/go-bricks/inbox/testing"
	"github.com/gaborage/go-bricks/jose/sealed"
	jositest "github.com/gaborage/go-bricks/jose/testing"
	"github.com/gaborage/go-bricks/keystore"
	kstest "github.com/gaborage/go-bricks/keystore/testing"
	"github.com/gaborage/go-bricks/logger"
	"github.com/gaborage/go-bricks/messaging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Messaging declarations are validated only when app.Run() collects them, and every
// refusal there is a RUNTIME startup failure — `go build` and each module's own
// tests stay green. Two of those refusals are about the set as a whole rather than
// any one call site: a name two modules declare with shapes that cannot merge
// (go-bricks v0.66.0, #1736) and a local exchange of a type the broker does not know
// (#1712). No single module's test can see the first one, so this file collects
// every module's declarations into ONE set, exactly as the framework does, and
// validates it in CI.

// newModuleTestDeps returns the dependencies the demo modules read in Init, with no
// infrastructure behind any of them. DB, DBByName and Messaging stay nil: Init only
// stores those getters, and nothing here calls one.
//
// One RSA pair backs every keystore entry. Nothing in these tests seals, opens or
// verifies a payload; the startup checks only need each name to resolve in its role.
func newModuleTestDeps(t *testing.T) *app.ModuleDeps {
	t.Helper()

	priv, pub := jositest.GenerateTestKeyPair(t)
	ks := kstest.NewMockKeyStore()
	// The tokens module's JOSE routes resolve both kids when they register.
	for _, kid := range []string{tokens.OurKid, tokens.PeerKid} {
		ks.WithPrivateKey(kid, priv).WithPublicKey(kid, pub)
	}
	// The sealing families come from the event's own seal tags, so this cannot drift
	// from the declaration it provisions. One generation each, holding both halves,
	// as in config.development.yaml: the process both produces and consumes, and a
	// single provisioned generation auto-activates (no messaging.seal.active).
	spec, err := sealed.ScanType(reflect.TypeOf(paymentsdomain.PaymentAuthorized{}))
	require.NoError(t, err)
	require.NotNil(t, spec, "PaymentAuthorized must stay seal-tagged")
	for _, family := range []string{spec.SignLogical, spec.EncryptLogical} {
		gen := keystore.Generation{Logical: family, Version: "v1", Role: keystore.RolePrivate}
		ks.WithGeneration(gen.Logical, gen.Version, gen.Role).
			WithPrivateKey(gen.Kid(), priv).
			WithPublicKey(gen.Kid(), pub)
	}

	return &app.ModuleDeps{
		Logger: logger.New("disabled", false),
		// development: the environment `make run` boots in. The server section is
		// what a real boot validates: the framework's default port and the base path
		// config.development.yaml sets. The tokens module builds its relay URLs from it.
		Config: &config.Config{
			App:    config.AppConfig{Name: "test", Version: "1.0.0", Env: config.EnvDevelopment},
			Server: config.ServerConfig{Host: "0.0.0.0", Port: 8080, Path: config.PathConfig{Base: "/api/v1"}},
		},
		Inbox:    inboxtest.NewMockInbox(),
		KeyStore: ks,
	}
}

// configureTestSealing records the sealing runtime the way app.Run does before it
// collects declarations: the keystore the modules see, tenancy disabled
// (multitenant.enabled is false) and no Activation selector. The payments module's
// seal-tagged publisher and consumer resolve their key generations against it at
// declaration time, and a failure there surfaces from Validate.
//
// The runtime is process-global. The framework exports no reset, so cleanup puts
// back whatever was configured before; when nothing was, it leaves a runtime with no
// key material, under which any later seal-tagged declaration fails with
// ErrKeyStoreMissing instead of reusing this test's keys.
func configureTestSealing(t *testing.T, ks messaging.SealKeyStore) {
	t.Helper()

	previous := messaging.SealingRuntime()
	t.Cleanup(func() {
		if previous != nil {
			messaging.ConfigureSealing(previous)
			return
		}
		messaging.ConfigureSealing(&messaging.SealRuntime{Tenancy: messaging.SealTenancyDisabled})
	})

	messaging.ConfigureSealing(&messaging.SealRuntime{KeyStore: ks, Tenancy: messaging.SealTenancyDisabled})
}

// declareAllMessaging walks the list main() registers, in order, and does for every
// enabled module that declares messaging what app.Run does before it contacts a
// broker: Init, then DeclareMessaging into one shared set.
//
// The framework modules (scheduler, outbox, inbox, keystore) implement no
// DeclareMessaging in go-bricks v0.67.0, so the framework skips them and so does
// this loop — which matters, because their Init needs a validated config, a database
// or DER files on disk.
func declareAllMessaging(t *testing.T, deps *app.ModuleDeps) *messaging.Declarations {
	t.Helper()

	decls := messaging.NewDeclarations()
	for _, mod := range getModulesToLoad(products.NewModule(), activity.NewModule()) {
		declarer, ok := mod.Module.(app.MessagingDeclarer)
		if !mod.Enabled || !ok {
			continue
		}
		require.NoError(t, mod.Module.Init(deps), "Init %s", mod.Name)
		declarer.DeclareMessaging(decls)
	}
	return decls
}

func TestAllModulesMessagingDeclarationsValidateTogether(t *testing.T) {
	deps := newModuleTestDeps(t)
	configureTestSealing(t, deps.KeyStore)

	decls := declareAllMessaging(t, deps)

	require.NoError(t, decls.Validate())

	// Not a vacuous pass: both declaring modules contributed, including the sealed
	// publisher and consumer whose key resolution is part of what Validate judged.
	assert.Contains(t, decls.Exchanges, "product-events")
	assert.Contains(t, decls.Exchanges, "payment-events")
	assert.NotEmpty(t, decls.Publishers)
	assert.NotEmpty(t, decls.Consumers())

	// The partner feed is registered in every boot but off by default, so the
	// default set must not reference an exchange no local service creates: `make run`
	// would abort on the broker's 404.
	assert.NotContains(t, decls.Exchanges, partnerdomain.ExchangeName)
}

// Switched on (custom.partnerfeed.enabled), the partner feed adds an external
// reference to the same set that holds every owned exchange. A name one module owns
// and another marks external is refused only set-wide, so this is where CI proves the
// feed and the demo's own topology can boot together.
func TestAllModulesMessagingDeclarationsValidateWithPartnerFeedOn(t *testing.T) {
	deps := newModuleTestDeps(t)
	cfg, err := config.LoadFromMap(map[string]any{
		"app.name":                   "test",
		"app.version":                "1.0.0",
		"app.env":                    config.EnvDevelopment,
		partnerfeed.ConfigKeyEnabled: true,
	})
	require.NoError(t, err)
	deps.Config = cfg
	configureTestSealing(t, deps.KeyStore)

	decls := declareAllMessaging(t, deps)

	require.NoError(t, decls.Validate())

	partner, ok := decls.Exchanges[partnerdomain.ExchangeName]
	require.True(t, ok, "the enabled feed must reference %s", partnerdomain.ExchangeName)
	assert.True(t, partner.Passive, "an external exchange is verified, never created")
	for _, owned := range []string{"product-events", "payment-events"} {
		require.Contains(t, decls.Exchanges, owned)
		assert.False(t, decls.Exchanges[owned].Passive, "%s is owned here and declared actively", owned)
	}
	assert.Contains(t, decls.Queues, partnerdomain.QueueName)
}

// The same set with one defect added must fail, or the pass above proves nothing:
// these are the two refusals the test exists to bring forward from `make run`.
func TestAllModulesMessagingDeclarationsRefuseASetWideDefect(t *testing.T) {
	deps := newModuleTestDeps(t)
	configureTestSealing(t, deps.KeyStore)

	t.Run("conflicting re-declaration", func(t *testing.T) {
		decls := declareAllMessaging(t, deps)
		// Another module claiming the products exchange with a different type. The
		// incumbent is kept and the conflict is reported, whatever the module order.
		decls.DeclareDirectExchange("product-events")

		err := decls.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), `exchange "product-events": Type kept "topic" vs rejected "direct"`)
	})

	t.Run("unknown exchange type", func(t *testing.T) {
		decls := declareAllMessaging(t, deps)
		decls.RegisterExchange(&messaging.ExchangeDeclaration{Name: "product-audit", Type: "topics", Durable: true})

		err := decls.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), `exchange "product-audit" has unknown type "topics"`)
	})
}
