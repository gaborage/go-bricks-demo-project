package main

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/activity"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/logproof"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/products"
	"github.com/gaborage/go-bricks-demo-project/internal/routepolicy"
	"github.com/gaborage/go-bricks/app"
	"github.com/gaborage/go-bricks/config"
	"github.com/gaborage/go-bricks/jose"
	"github.com/gaborage/go-bricks/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func route(method, path, module string, tags ...string) server.RouteDescriptor {
	return server.RouteDescriptor{Method: method, Path: path, ModuleName: module, Tags: tags}
}

// main boots with the policy installed. Handing the options' hook one untagged
// simulator proves it is this policy and not a permissive stand-in.
func TestNewAppOptionsInstallsSimulatorRoutePolicy(t *testing.T) {
	opts := newAppOptions()
	require.NotNil(t, opts.PostRegisterRoutes)

	err := opts.PostRegisterRoutes([]server.RouteDescriptor{
		route(http.MethodPost, "/api/v1/__sim/peer/tokens", "tokens"),
	})
	require.ErrorIs(t, err, routepolicy.ErrUntaggedSimulatorRoute)

	// Nothing else is overridden: config, dependencies and the server stay the ones
	// app.New() would build.
	opts.PostRegisterRoutes = nil
	assert.Equal(t, app.Options{}, *opts)
}

// The table the demo really registers must pass, or the hook would refuse `make run`.
// A /__sim/ route added without the tag fails here, in CI, instead of at boot. The
// Subset check keeps the pass from being vacuous (no simulator registered at all)
// without pinning the list, so a new tagged simulator needs no edit here.
func TestDemoRouteTablePassesSimulatorPolicy(t *testing.T) {
	routes := registerDemoRoutes(t)

	require.NoError(t, routepolicy.RequireTaggedSimulators(routes))

	var simulators []string
	for i := range routes {
		if routepolicy.IsSimulatorPath(routes[i].Path) {
			simulators = append(simulators, routes[i].Method+" "+routes[i].Path)
		}
	}
	assert.Subset(t, simulators, []string{
		"POST /api/v1/__sim/peer/tokens",
		"POST /api/v1/__sim/peer/mle",
		"POST /api/v1/__sim/streams/poison",
		"GET /api/v1/__sim/observability/warn/:token",
		"GET /api/v1/__sim/observability/escalate/:token",
		"GET /api/v1/__sim/observability/panic/:token",
	})
}

// demoModulePath is the import-path prefix of the modules this repository owns.
const demoModulePath = "github.com/gaborage/go-bricks-demo-project/"

// registerDemoRoutes builds the route table the demo's own modules register, the way
// the framework's module registry does: Init every enabled module this repository
// owns (in development with the logproof opt-in, so the activity poison simulator
// and the three logproof simulators register), then
// RegisterRoutes each one through a handler registry that resolves jose: kids against
// the keystore, under the /api/v1 base path config.yml sets. ModuleName
// is attributed by registration span, as the framework does.
//
// Framework modules are skipped: of those, only the scheduler registers routes, all
// under /_sys/job, and their Init needs a validated config, a database or DER files.
// The framework's own health/ready probes and debug endpoints are not in this table
// either; the hook's unit test covers a route with no module.
//
// Descriptors land in the process-global server.DefaultRouteRegistry, which exports
// only Clear. Reading each module's span from its start index keeps the result
// independent of anything registered before, so no cleanup is needed.
func registerDemoRoutes(t *testing.T) []server.RouteDescriptor {
	t.Helper()

	deps := newModuleTestDeps(t)
	// The same development boot, plus the logproof opt-in, so its three simulator
	// routes are in the table the policy judges. The switch is a custom.* key, which
	// only a loaded config carries, so this config comes from a map.
	cfg, err := config.LoadFromMap(map[string]any{
		"app.name":                deps.Config.App.Name,
		"app.version":             deps.Config.App.Version,
		"app.env":                 deps.Config.App.Env,
		"server.host":             deps.Config.Server.Host,
		"server.port":             deps.Config.Server.Port,
		"server.path.base":        deps.Config.Server.Path.Base,
		logproof.ConfigKeyEnabled: true,
	})
	require.NoError(t, err)
	deps.Config = cfg
	hr := server.NewHandlerRegistry(deps.Config, server.WithJOSEResolver(jose.NewKeyStoreResolver(deps.KeyStore)))
	registrar := &basePathRegistrar{base: "/api/v1"}

	var routes []server.RouteDescriptor
	for _, mod := range getModulesToLoad(products.NewModule(), activity.NewModule()) {
		registerer, ok := mod.Module.(app.RouteRegisterer)
		if !mod.Enabled || !ok || !ownedByDemo(mod.Module) {
			continue
		}
		require.NoError(t, mod.Module.Init(deps), "Init %s", mod.Name)

		start := server.DefaultRouteRegistry.Count()
		registerer.RegisterRoutes(hr, registrar)
		for _, registered := range server.DefaultRouteRegistry.Routes()[start:] {
			if registered.ModuleName == "" {
				registered.ModuleName = mod.Module.Name()
			}
			routes = append(routes, registered)
		}
	}
	return routes
}

func ownedByDemo(module app.Module) bool {
	typ := reflect.TypeOf(module)
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	return strings.HasPrefix(typ.PkgPath(), demoModulePath)
}

// basePathRegistrar is the RouteRegistrar test fake server.RegisterHandler documents
// a fallback for. Only FullPath matters here: it is what puts the base path on each
// descriptor. The handlers are adapted and dropped, since nothing is served.
type basePathRegistrar struct {
	base string
}

func (r *basePathRegistrar) Add(string, string, server.Handler, ...server.MiddlewareFunc) {}

func (r *basePathRegistrar) Group(prefix string, _ ...server.MiddlewareFunc) server.RouteRegistrar {
	return &basePathRegistrar{base: r.base + prefix}
}

func (r *basePathRegistrar) Use(...server.MiddlewareFunc) {}

func (r *basePathRegistrar) FullPath(path string) string { return r.base + path }
