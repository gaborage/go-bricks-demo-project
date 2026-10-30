package main

import (
	"reflect"
	"slices"
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
	jositest "github.com/gaborage/go-bricks/jose/testing"
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
//
// The payments module is the one exception, and the reason is the framework, not
// the module. Its event is seal-tagged, and a seal-tagged declaration resolves its
// key generations against the sealing runtime that app.Run configures just before
// it collects declarations. Since go-bricks C72.9 (ADR-139) only app.Run can
// configure that runtime: ConfigureSealing is gone and nothing replaces it. Outside
// an App every seal-tagged declaration records the runtime's not-configured error,
// and Validate reports that error BEFORE any topology rule runs, for the whole set.
// So the topology is validated without payments, and the full set, payments
// included, is checked to stop at exactly that seam. Payments' exchange and queue
// names are still checked against every other module's, in a set of their own, so
// a name conflict still fails CI.
//
// Coverage this costs, now proven only by a real boot (`make run`,
// `make show-sealed-message`): that the keystore resolves the payments sign and
// encrypt generations, and that the payments topology (payment-events, the
// payments.authorized DLQ pair, the tap and their bindings) passes the set-wide
// rules on dead-letter routing (C72.13) and publisher destinations. Copying the
// full set's exported maps into a fresh set would get past the seal error, but it
// would also drop the conflicts and the at-least-once DLQ facts the framework
// keeps unexported, and pass a set the original fails.

// sealedModule is the module whose seal-tagged declarations stop Validate outside
// an App. See the file comment.
const sealedModule = "payments"

// newModuleTestDeps returns the dependencies the demo modules read in Init, with no
// infrastructure behind any of them. DB, DBByName and Messaging stay nil: Init only
// stores those getters, and nothing here calls one.
//
// One RSA pair backs the tokens module's JOSE kids, which its routes resolve when
// they register, and the detached gateway's entries, whose halves its Init
// checks. The payments sealing generations are not provisioned: nothing outside
// an App resolves them (see the file comment), and payments' Init only checks
// that a keystore is present.
func newModuleTestDeps(t *testing.T) *app.ModuleDeps {
	t.Helper()

	priv, pub := jositest.GenerateTestKeyPair(t)
	ks := kstest.NewMockKeyStore()
	for _, kid := range []string{tokens.OurKid, tokens.PeerKid, tokens.DetachedOurKid, tokens.DetachedPeerKid} {
		ks.WithPrivateKey(kid, priv).WithPublicKey(kid, pub)
	}

	return &app.ModuleDeps{
		Logger: logger.New("disabled", false),
		// development: the environment `make run` boots in. The server section is
		// what a real boot validates: the framework's default port and the base path
		// config.yml sets. The tokens module builds its relay URLs from it.
		Config: &config.Config{
			App:    config.AppConfig{Name: "test", Version: "1.0.0", Env: config.EnvDevelopment},
			Server: config.ServerConfig{Host: "0.0.0.0", Port: 8080, Path: config.PathConfig{Base: "/api/v1"}},
		},
		Inbox:    inboxtest.NewMockInbox(),
		KeyStore: ks,
	}
}

// declareAllMessaging walks the list main() registers, in order, and does for every
// enabled module that declares messaging what app.Run does before it contacts a
// broker: Init, then DeclareMessaging into one shared set. Modules named in skip
// are left out.
//
// The framework modules (scheduler, outbox, inbox, keystore) implement no
// DeclareMessaging in the pinned go-bricks, so the framework skips them and so does
// this loop — which matters, because their Init needs a validated config, a database
// or DER files on disk.
func declareAllMessaging(t *testing.T, deps *app.ModuleDeps, skip ...string) *messaging.Declarations {
	t.Helper()

	decls := messaging.NewDeclarations()
	for _, mod := range getModulesToLoad(products.NewModule(), activity.NewModule()) {
		declarer, ok := mod.Module.(app.MessagingDeclarer)
		if !mod.Enabled || !ok || slices.Contains(skip, mod.Name) {
			continue
		}
		require.NoError(t, mod.Module.Init(deps), "Init %s", mod.Name)
		declarer.DeclareMessaging(decls)
	}
	return decls
}

// declareModuleMessaging does the same for the one module named, alone, into a
// fresh set.
func declareModuleMessaging(t *testing.T, deps *app.ModuleDeps, name string) *messaging.Declarations {
	t.Helper()

	for _, mod := range getModulesToLoad(products.NewModule(), activity.NewModule()) {
		if mod.Name != name {
			continue
		}
		declarer, ok := mod.Module.(app.MessagingDeclarer)
		require.True(t, mod.Enabled && ok, "%s must be enabled and declare messaging", name)
		decls := messaging.NewDeclarations()
		require.NoError(t, mod.Module.Init(deps), "Init %s", name)
		declarer.DeclareMessaging(decls)
		return decls
	}
	require.FailNow(t, "module not registered", name)
	return nil
}

// partnerFeedOnConfig is the config of a boot with custom.partnerfeed.enabled set.
func partnerFeedOnConfig(t *testing.T) *config.Config {
	t.Helper()

	cfg, err := config.LoadFromMap(map[string]any{
		"app.name":                   "test",
		"app.version":                "1.0.0",
		"app.env":                    config.EnvDevelopment,
		partnerfeed.ConfigKeyEnabled: true,
	})
	require.NoError(t, err)
	return cfg
}

func TestAllModulesMessagingDeclarationsValidateTogether(t *testing.T) {
	deps := newModuleTestDeps(t)

	decls := declareAllMessaging(t, deps, sealedModule)

	require.NoError(t, decls.Validate())

	// Not a vacuous pass: the products exchange is in the set Validate judged.
	assert.Contains(t, decls.Exchanges, "product-events")

	// The partner feed is registered in every boot but off by default, so the
	// default set must not reference an exchange no local service creates: `make run`
	// would abort on the broker's 404.
	assert.NotContains(t, decls.Exchanges, partnerdomain.ExchangeName)
}

// Switched on (custom.partnerfeed.enabled), the partner feed adds an external
// reference to the same set that holds every owned exchange. A name one module owns
// and another marks external is refused only set-wide, so this is where CI proves the
// feed and the demo's own topology can boot together. The feed's queue is a DLQ
// pair, so this set is also where the dead-letter routing rules judge one.
func TestAllModulesMessagingDeclarationsValidateWithPartnerFeedOn(t *testing.T) {
	deps := newModuleTestDeps(t)
	deps.Config = partnerFeedOnConfig(t)

	decls := declareAllMessaging(t, deps, sealedModule)

	require.NoError(t, decls.Validate())

	partner, ok := decls.Exchanges[partnerdomain.ExchangeName]
	require.True(t, ok, "the enabled feed must reference %s", partnerdomain.ExchangeName)
	assert.True(t, partner.Passive, "an external exchange is verified, never created")
	require.Contains(t, decls.Exchanges, "product-events")
	assert.False(t, decls.Exchanges["product-events"].Passive, "product-events is owned here and declared actively")
	assert.Contains(t, decls.Queues, partnerdomain.QueueName)
	assert.NotEmpty(t, decls.Consumers())
}

// The full set app.Run collects, payments included, must stop at the sealing seam:
// Validate reports the seal error first, so that is all this set can show. The
// codec check runs before the runtime check, so an error that is not
// ErrSealingNotLinked proves the messaging/sealed blank import is in the build. The
// not-configured error has no exported sentinel (it lives in go-bricks'
// internal/sealruntime), so its text is matched. The expectation holds because
// nothing in this test binary runs an App, the only caller that configures the
// process-global sealing runtime.
func TestAllModulesMessagingDeclarationsStopAtTheSealingSeam(t *testing.T) {
	require.True(t, messaging.IsSealTagged(reflect.TypeOf(paymentsdomain.PaymentAuthorized{})),
		"PaymentAuthorized is no longer seal-tagged: put %s back in the validated set", sealedModule)

	deps := newModuleTestDeps(t)

	decls := declareAllMessaging(t, deps)

	// Payments contributed its whole topology before the seam refused it.
	assert.Contains(t, decls.Exchanges, "product-events")
	assert.Contains(t, decls.Exchanges, "payment-events")
	for _, queue := range []string{"payments.authorized", "payments.authorized.dlq", "payments.authorized.tap"} {
		assert.Contains(t, decls.Queues, queue)
	}
	assert.NotEmpty(t, decls.Publishers)
	assert.True(t, slices.ContainsFunc(decls.Consumers(), func(c *messaging.ConsumerDeclaration) bool {
		return c.Queue == "payments.authorized"
	}), "payments must declare its consumer")

	err := decls.Validate()
	require.Error(t, err)
	assert.NotErrorIs(t, err, messaging.ErrSealingNotLinked)
	assert.ErrorContains(t, err, "sealing runtime not configured before declarations were collected")
	assert.ErrorContains(t, err, `"payment.authorized"`)
}

// Payments cannot join the validated set, but a name conflict needs only the
// names. Payments is declared alone, and none of its exchange or queue names may
// appear in the set the other modules build, with the partner feed off and on.
// This is stricter than Validate, which merges an identical re-declaration, but
// payments shares no name with another module.
func TestPaymentsMessagingNamesStayOutOfTheOtherModulesSet(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config func(*testing.T) *config.Config
	}{
		{name: "partner feed off"},
		{name: "partner feed on", config: partnerFeedOnConfig},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps := newModuleTestDeps(t)
			if tc.config != nil {
				deps.Config = tc.config(t)
			}

			payments := declareModuleMessaging(t, deps, sealedModule)
			others := declareAllMessaging(t, deps, sealedModule)
			// Not a vacuous pass: both sides hold the names they should.
			require.Contains(t, payments.Exchanges, "payment-events")
			require.Contains(t, payments.Queues, "payments.authorized")
			require.Contains(t, others.Exchanges, "product-events")

			for name := range payments.Exchanges {
				assert.NotContains(t, others.Exchanges, name, "exchange %q is declared by payments and another module", name)
			}
			for name := range payments.Queues {
				assert.NotContains(t, others.Queues, name, "queue %q is declared by payments and another module", name)
			}
		})
	}
}

// The same set with one defect added must fail, or the pass above proves nothing:
// these are the two refusals the test exists to bring forward from `make run`.
func TestAllModulesMessagingDeclarationsRefuseASetWideDefect(t *testing.T) {
	deps := newModuleTestDeps(t)

	t.Run("conflicting re-declaration", func(t *testing.T) {
		decls := declareAllMessaging(t, deps, sealedModule)
		// Another module claiming the products exchange with a different type. The
		// incumbent is kept and the conflict is reported, whatever the module order.
		decls.DeclareDirectExchange("product-events")

		err := decls.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), `exchange "product-events": Type kept "topic" vs rejected "direct"`)
	})

	t.Run("unknown exchange type", func(t *testing.T) {
		decls := declareAllMessaging(t, deps, sealedModule)
		decls.RegisterExchange(&messaging.ExchangeDeclaration{Name: "product-audit", Type: "topics", Durable: true})

		err := decls.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), `exchange "product-audit" has unknown type "topics"`)
	})
}
