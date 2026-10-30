package tokens

import (
	"net/http"
	"testing"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/tokens/handlers"
	"github.com/gaborage/go-bricks/app"
	"github.com/gaborage/go-bricks/config"
	"github.com/gaborage/go-bricks/jose"
	jositest "github.com/gaborage/go-bricks/jose/testing"
	kstest "github.com/gaborage/go-bricks/keystore/testing"
	"github.com/gaborage/go-bricks/logger"
	"github.com/gaborage/go-bricks/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The server.path.base config.yml sets, and the framework's
// default server.host.
const (
	apiBase      = "/api/v1"
	wildcardHost = "0.0.0.0"
)

// testApp is the app section of every config these tests build.
var testApp = config.AppConfig{Name: "test", Version: "1.0.0", Env: config.EnvDevelopment}

func TestSelfBaseURL(t *testing.T) {
	tests := []struct {
		name string
		srv  config.ServerConfig
		want string
	}{
		{
			name: "development defaults",
			srv:  config.ServerConfig{Host: wildcardHost, Port: 8080, Path: config.PathConfig{Base: apiBase}},
			want: "http://localhost:8080/api/v1",
		},
		{
			name: "overridden port",
			srv:  config.ServerConfig{Host: wildcardHost, Port: 18081, Path: config.PathConfig{Base: apiBase}},
			want: "http://localhost:18081/api/v1",
		},
		{
			name: "empty host and base path at the root",
			srv:  config.ServerConfig{Port: 9000},
			want: "http://localhost:9000",
		},
		{
			name: "slash-only base path",
			srv:  config.ServerConfig{Port: 9000, Path: config.PathConfig{Base: "/"}},
			want: "http://localhost:9000",
		},
		{
			name: "base path without leading slash and with trailing slash",
			srv:  config.ServerConfig{Port: 9000, Path: config.PathConfig{Base: "svc/v2/"}},
			want: "http://localhost:9000/svc/v2",
		},
		{
			name: "specific IPv4 host",
			srv:  config.ServerConfig{Host: "127.0.0.1", Port: 8081, Path: config.PathConfig{Base: apiBase}},
			want: "http://127.0.0.1:8081/api/v1",
		},
		{
			name: "specific IPv6 host is bracketed",
			srv:  config.ServerConfig{Host: "::1", Port: 8081},
			want: "http://[::1]:8081",
		},
		{
			name: "IPv6 wildcard dials localhost",
			srv:  config.ServerConfig{Host: "::", Port: 8081},
			want: "http://localhost:8081",
		},
		{
			name: "TLS listener",
			srv:  config.ServerConfig{Port: 8443, TLS: config.ServerTLSConfig{Enabled: true}},
			want: "https://localhost:8443",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := selfBaseURL(&config.Config{Server: tt.srv})
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSelfBaseURLRefusesUnaddressableListener(t *testing.T) {
	_, err := selfBaseURL(nil)
	require.Error(t, err)

	for _, port := range []int{0, -1, 65536} {
		_, err := selfBaseURL(&config.Config{Server: config.ServerConfig{Port: port}})
		assert.Error(t, err, "port %d", port)
	}
}

// TestInitAddressesSimulatorsOnConfiguredListener is the regression for the
// hardcoded localhost:8080: the relays must follow server.port and
// server.path.base, and the paths must be the ones the simulators register.
func TestInitAddressesSimulatorsOnConfiguredListener(t *testing.T) {
	priv, pub := jositest.GenerateTestKeyPair(t)
	ks := kstest.NewMockKeyStore()
	for _, kid := range []string{OurKid, PeerKid, DetachedOurKid, DetachedPeerKid} {
		ks.WithPrivateKey(kid, priv).WithPublicKey(kid, pub)
	}

	m := NewModule()
	require.NoError(t, m.Init(&app.ModuleDeps{
		Logger: logger.New("disabled", false),
		Config: &config.Config{
			App:    testApp,
			Server: config.ServerConfig{Host: wildcardHost, Port: 18081, Path: config.PathConfig{Base: apiBase}},
		},
		KeyStore: ks,
	}))

	assert.Equal(t, "http://localhost:18081"+apiBase+handlers.PeerSimulatorPath, m.peerSimulatorURL)
	assert.Equal(t, "http://localhost:18081"+apiBase+handlers.MLEPeerSimulatorPath, m.mlePeerSimulatorURL)
	assert.Equal(t, "http://localhost:18081/api/v1/__sim/peer/tokens", m.peerSimulatorURL)
	assert.Equal(t, "http://localhost:18081/api/v1/tokens/detached", m.detachedGatewayURL,
		"the detached simulator calls the real gateway route on the same listener")
}

// routeRecorder is a server.RouteRegistrar that records "METHOD path" for each
// route added, without standing up an Echo router.
type routeRecorder struct {
	routes []string
}

func (r *routeRecorder) Add(method, path string, _ server.Handler, _ ...server.MiddlewareFunc) {
	r.routes = append(r.routes, method+" "+path)
}

func (r *routeRecorder) Group(_ string, _ ...server.MiddlewareFunc) server.RouteRegistrar { return r }

func (r *routeRecorder) Use(_ ...server.MiddlewareFunc) {}

func (r *routeRecorder) FullPath(path string) string { return path }

// newTestKeys returns a keystore holding every entry the module resolves, plus a
// JOSE resolver over the same private keys for registering jose:-tagged routes.
// The detached entries get their own pairs, as they do in every config.
func newTestKeys(t *testing.T) (*kstest.MockKeyStore, jose.KeyResolver) {
	t.Helper()
	resolverKeys := map[string]any{}
	ks := kstest.NewMockKeyStore()
	for _, kid := range []string{OurKid, PeerKid, DetachedOurKid, DetachedPeerKid} {
		priv, pub := jositest.GenerateTestKeyPair(t)
		resolverKeys[kid] = priv
		ks.WithPrivateKey(kid, priv).WithPublicKey(kid, pub)
	}
	return ks, jositest.NewTestResolver(resolverKeys)
}

// newTestModuleDeps returns the deps Init needs: a keystore and a server
// config the relays can address their in-process simulators on.
func newTestModuleDeps(ks *kstest.MockKeyStore) *app.ModuleDeps {
	return &app.ModuleDeps{
		Logger: logger.New("disabled", false),
		Config: &config.Config{
			App:    testApp,
			Server: config.ServerConfig{Host: wildcardHost, Port: 8080, Path: config.PathConfig{Base: apiBase}},
		},
		KeyStore: ks,
	}
}

func TestModuleName(t *testing.T) {
	assert.Equal(t, "tokens", NewModule().Name())
}

func TestModuleNoOpLifecycle(t *testing.T) {
	m := NewModule()
	assert.NotPanics(t, func() { m.DeclareMessaging(nil) })
	assert.NoError(t, m.RegisterJobs(nil))
	assert.NoError(t, m.Shutdown())
}

func TestModuleInitRequiresKeyStore(t *testing.T) {
	ks, _ := newTestKeys(t)
	deps := newTestModuleDeps(ks)
	deps.KeyStore = nil

	err := NewModule().Init(deps)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "keystore")
}

func TestModuleInitRequiresConfig(t *testing.T) {
	ks, _ := newTestKeys(t)
	deps := newTestModuleDeps(ks)
	deps.Config = nil

	err := NewModule().Init(deps)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deps.Config")
}

func TestModuleInitWiresHandlers(t *testing.T) {
	ks, _ := newTestKeys(t)
	m := NewModule()

	require.NoError(t, m.Init(newTestModuleDeps(ks)))
	assert.NotNil(t, m.handler)
	assert.NotNil(t, m.relayHandler)
	assert.NotNil(t, m.mleHandler)
	assert.NotNil(t, m.vtsHandler)
	assert.NotNil(t, m.detachedHandler)
}

// TestModuleInitRequiresDetachedEntries pins the fail-fast decision: a keystore
// without the gateway's dedicated entries refuses startup, naming each missing
// half and the variable a deployed environment sets for it. Only the existing
// tokens.our/tokens.peer pairs are present here, which is exactly a deploy that
// has not provisioned the new secrets yet.
func TestModuleInitRequiresDetachedEntries(t *testing.T) {
	priv, pub := jositest.GenerateTestKeyPair(t)

	t.Run("both entries missing", func(t *testing.T) {
		ks := kstest.NewMockKeyStore().
			WithPrivateKey(OurKid, priv).WithPublicKey(OurKid, pub).
			WithPrivateKey(PeerKid, priv).WithPublicKey(PeerKid, pub)

		err := NewModule().Init(newTestModuleDeps(ks))
		require.Error(t, err)
		for _, want := range []string{
			`entry "tokens.detached.our" has no usable private half`,
			`entry "tokens.detached.our" has no usable public half`,
			`entry "tokens.detached.peer" has no usable private half`,
			`entry "tokens.detached.peer" has no usable public half`,
			"KEYSTORE_KEYS_TOKENS_DETACHED_OUR_PRIVATE_VALUE",
			"KEYSTORE_KEYS_TOKENS_DETACHED_OUR_PUBLIC_VALUE",
			"KEYSTORE_KEYS_TOKENS_DETACHED_PEER_PRIVATE_VALUE",
			"KEYSTORE_KEYS_TOKENS_DETACHED_PEER_PUBLIC_VALUE",
			"make generate-keys",
		} {
			assert.Contains(t, err.Error(), want)
		}
	})

	t.Run("one half missing", func(t *testing.T) {
		ks := kstest.NewMockKeyStore()
		for _, kid := range []string{OurKid, PeerKid, DetachedOurKid} {
			ks.WithPrivateKey(kid, priv).WithPublicKey(kid, pub)
		}
		ks.WithPublicKey(DetachedPeerKid, pub) // a real gateway's shape: the peer's public half only

		err := NewModule().Init(newTestModuleDeps(ks))
		require.Error(t, err)
		assert.Contains(t, err.Error(), `entry "tokens.detached.peer" has no usable private half (set KEYSTORE_KEYS_TOKENS_DETACHED_PEER_PRIVATE_VALUE)`)
		assert.NotContains(t, err.Error(), "TOKENS_DETACHED_OUR")
	})
}

// TestModuleRegisterRoutes registers every route for real. The bootstrap wires a
// jose.KeyStoreResolver into the registry whenever deps.KeyStore is set; the
// test passes one too, because the partner and simulator routes carry jose: tags.
func TestModuleRegisterRoutes(t *testing.T) {
	ks, resolver := newTestKeys(t)
	m := NewModule()
	require.NoError(t, m.Init(newTestModuleDeps(ks)))

	hr := server.NewHandlerRegistry(&config.Config{
		App: testApp,
	}, server.WithJOSEResolver(resolver))
	rec := &routeRecorder{}
	t.Cleanup(server.DefaultRouteRegistry.Clear)

	m.RegisterRoutes(hr, rec)

	assert.ElementsMatch(t, []string{
		http.MethodPost + " /tokens",
		http.MethodPost + " " + handlers.PeerSimulatorPath,
		http.MethodPost + " /tokens/relay",
		http.MethodPost + " /tokens/mle-relay",
		http.MethodPost + " " + handlers.MLEPeerSimulatorPath,
		http.MethodPost + " /tokens/vts-issuer-relay",
		http.MethodPost + " " + handlers.DetachedGatewayPath,
		http.MethodPost + " " + handlers.DetachedPeerSimulatorPath,
	}, rec.routes)
}
