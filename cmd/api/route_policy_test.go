package main

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/activity"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/products"
	"github.com/gaborage/go-bricks/app"
	"github.com/gaborage/go-bricks/jose"
	"github.com/gaborage/go-bricks/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func route(method, path, module string, tags ...string) server.RouteDescriptor {
	return server.RouteDescriptor{Method: method, Path: path, ModuleName: module, Tags: tags}
}

// A deliberately mis-marked table, one slip per case. Each violation must be reported
// with the route it concerns, and a well-marked table must pass untouched.
func TestRequireTaggedSimulators(t *testing.T) {
	tests := []struct {
		name     string
		routes   []server.RouteDescriptor
		wantErrs []error
		wantMsgs []string
	}{
		{
			name: "well-marked table",
			routes: []server.RouteDescriptor{
				route(http.MethodGet, "/api/v1/health", ""),
				route(http.MethodPost, "/api/v1/tokens/relay", "tokens"),
				route(http.MethodGet, "/api/v1/legacy/products", "legacy", "legacy"),
				route(http.MethodPost, "/api/v1/__sim/peer/tokens", "tokens", simulatorTag),
				route(http.MethodPost, "/api/v1/__sim/streams/poison", "activity", "streams", simulatorTag),
			},
		},
		{
			name:   "empty table",
			routes: nil,
		},
		{
			name:     "simulator route without the tag",
			routes:   []server.RouteDescriptor{route(http.MethodPost, "/api/v1/__sim/peer/tokens", "tokens")},
			wantErrs: []error{errUntaggedSimulatorRoute},
			wantMsgs: []string{"POST /api/v1/__sim/peer/tokens (module tokens)"},
		},
		{
			name:     "simulator route with other tags only",
			routes:   []server.RouteDescriptor{route(http.MethodPost, "/api/v1/__sim/streams/poison", "activity", "streams")},
			wantErrs: []error{errUntaggedSimulatorRoute},
			wantMsgs: []string{"POST /api/v1/__sim/streams/poison (module activity)"},
		},
		{
			name:     "simulator tag outside /__sim/",
			routes:   []server.RouteDescriptor{route(http.MethodPost, "/api/v1/tokens/relay", "tokens", simulatorTag)},
			wantErrs: []error{errStraySimulatorTag},
			wantMsgs: []string{"POST /api/v1/tokens/relay (module tokens)"},
		},
		{
			// __sim is matched as a whole segment: a lookalike prefix is not a simulator
			// path, so the tag on it is stray and its absence is fine.
			name: "lookalike segment is not a simulator path",
			routes: []server.RouteDescriptor{
				route(http.MethodGet, "/api/v1/__simulated/report", "reports"),
				route(http.MethodGet, "/api/v1/__simulated/audit", "reports", simulatorTag),
			},
			wantErrs: []error{errStraySimulatorTag},
			wantMsgs: []string{"GET /api/v1/__simulated/audit (module reports)"},
		},
		{
			name:     "framework route names no module",
			routes:   []server.RouteDescriptor{route(http.MethodGet, "/__sim/probe", "")},
			wantErrs: []error{errUntaggedSimulatorRoute},
			wantMsgs: []string{"GET /__sim/probe"},
		},
		{
			name: "every violation is reported",
			routes: []server.RouteDescriptor{
				route(http.MethodPost, "/api/v1/__sim/peer/tokens", "tokens"),
				route(http.MethodPost, "/api/v1/__sim/peer/mle", "tokens", simulatorTag),
				route(http.MethodPost, "/api/v1/payments/authorize", "payments", simulatorTag),
				route(http.MethodPost, "/api/v1/__sim/streams/poison", "activity"),
			},
			wantErrs: []error{errUntaggedSimulatorRoute, errStraySimulatorTag},
			wantMsgs: []string{
				"POST /api/v1/__sim/peer/tokens (module tokens)",
				"POST /api/v1/payments/authorize (module payments)",
				"POST /api/v1/__sim/streams/poison (module activity)",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := requireTaggedSimulators(tt.routes)

			if len(tt.wantErrs) == 0 {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			for _, want := range tt.wantErrs {
				assert.ErrorIs(t, err, want)
			}
			for _, msg := range tt.wantMsgs {
				assert.Contains(t, err.Error(), msg)
			}
			// One line per violation, so a well-marked route in the same table (the
			// tagged MLE simulator above) is never reported alongside them.
			assert.Len(t, strings.Split(err.Error(), "\n"), len(tt.wantMsgs))
		})
	}
}

// main boots with the policy installed. Handing the options' hook one untagged
// simulator proves it is this policy and not a permissive stand-in.
func TestNewAppOptionsInstallsSimulatorRoutePolicy(t *testing.T) {
	opts := newAppOptions()
	require.NotNil(t, opts.PostRegisterRoutes)

	err := opts.PostRegisterRoutes([]server.RouteDescriptor{
		route(http.MethodPost, "/api/v1/__sim/peer/tokens", "tokens"),
	})
	require.ErrorIs(t, err, errUntaggedSimulatorRoute)

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

	require.NoError(t, requireTaggedSimulators(routes))

	var simulators []string
	for i := range routes {
		if isSimulatorPath(routes[i].Path) {
			simulators = append(simulators, routes[i].Method+" "+routes[i].Path)
		}
	}
	assert.Subset(t, simulators, []string{
		"POST /api/v1/__sim/peer/tokens",
		"POST /api/v1/__sim/peer/mle",
		"POST /api/v1/__sim/streams/poison",
	})
}

// demoModulePath is the import-path prefix of the modules this repository owns.
const demoModulePath = "github.com/gaborage/go-bricks-demo-project/"

// registerDemoRoutes builds the route table the demo's own modules register, the way
// the framework's module registry does: Init every enabled module this repository
// owns (in development, so the activity module registers its poison simulator), then
// RegisterRoutes each one through a handler registry that resolves jose: kids against
// the keystore, under the /api/v1 base path config.development.yaml sets. ModuleName
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
