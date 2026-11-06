package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/partnerportal"
	"github.com/gaborage/go-bricks-demo-project/internal/routepolicy"
	"github.com/gaborage/go-bricks/app"
	"github.com/gaborage/go-bricks/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const repoRootFromCmd = "../.."

// configPath is the binary's real config file, so these tests also prove it loads
// and validates.
func configPath(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join(repoRootFromCmd, defaultConfigFile))
	require.NoError(t, err)
	_, err = os.Stat(path)
	require.NoError(t, err)
	return path
}

func noEnv(string) (string, bool) { return "", false }

func TestLoadConfig(t *testing.T) {
	cfg, err := loadConfig(configPath(t), noEnv)
	require.NoError(t, err)

	assert.True(t, cfg.Multitenant.Enabled)
	assert.ElementsMatch(t, []string{"acme", "globex", "initech"}, keys(cfg.Multitenant.Tenants))
	assert.Equal(t, 18091, cfg.Server.Port)
	assert.Equal(t, "/api/v1", cfg.Server.Path.Base)
	// Static tenants refuse a root database block; the file has none.
	assert.Empty(t, cfg.Database.Host)
}

// Only the three allowlisted variables reach the config; anything else in the
// environment is ignored.
func TestLoadConfigEnvAllowlist(t *testing.T) {
	env := map[string]string{
		"SERVER_PORT":       "18123",
		"LOG_LEVEL":         "debug",
		"LOG_OUTPUT_FORMAT": "json",
		"DATABASE_HOST":     "db.example.invalid",
		"APP_NAME":          "renamed",
	}
	cfg, err := loadConfig(configPath(t), func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	require.NoError(t, err)

	assert.Equal(t, 18123, cfg.Server.Port)
	assert.Equal(t, "debug", cfg.Log.Level)
	assert.Empty(t, cfg.Database.Host)
	assert.Equal(t, "go-bricks-tenant-family-demo", cfg.App.Name)
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// errRouteTableSeen stops Run after the route-table hook: every framework startup
// check (route conflicts, trespass, global middleware beside a family) has already
// run by then, and no listener has opened.
var errRouteTableSeen = errors.New("route table captured")

// bootUntilRoutes builds the binary's App with module, runs it until the route
// table is final, and returns that table and Run's error. The real simulator policy
// runs first; its verdict is returned separately.
func bootUntilRoutes(t *testing.T, module app.Module) (routes []server.RouteDescriptor, policyErr, runErr error) {
	t.Helper()
	opts := newAppOptions(configPath(t))
	policy := opts.PostRegisterRoutes
	require.NotNil(t, policy)
	opts.PostRegisterRoutes = func(table []server.RouteDescriptor) error {
		routes = table
		policyErr = policy(table)
		return errRouteTableSeen
	}

	application, _, err := app.NewWithOptions(opts)
	require.NoError(t, err)
	require.NoError(t, application.RegisterModule(module))
	runErr = application.Run()
	return routes, policyErr, runErr
}

// The table the binary really registers passes the simulator policy, and holds the
// four partner portal routes.
func TestRouteTablePassesSimulatorPolicy(t *testing.T) {
	routes, policyErr, runErr := bootUntilRoutes(t, partnerportal.NewModule())

	require.ErrorIs(t, runErr, errRouteTableSeen)
	require.NoError(t, policyErr)

	var registered []string
	for i := range routes {
		if routes[i].ModuleName == "partnerportal" {
			registered = append(registered, routes[i].Method+" "+routes[i].Path)
		}
		if routepolicy.IsSimulatorPath(routes[i].Path) {
			assert.Contains(t, routes[i].Tags, routepolicy.SimulatorTag)
		}
	}
	assert.ElementsMatch(t, []string{
		"GET /api/v1/tenants/whoami",
		"GET /api/v1/t/:tenant/whoami",
		"POST /api/v1/__sim/partner-token",
		"GET /api/v1/partner/v2/whoami",
	}, registered)
}

// trespassModule also registers a route under the family's prefix on the plain
// registrar, outside the family.
type trespassModule struct{ *partnerportal.Module }

func (m trespassModule) RegisterRoutes(hr *server.HandlerRegistry, r server.RouteRegistrar) {
	m.Module.RegisterRoutes(hr, r)
	server.GET(hr, r, partnerportal.FamilyPrefix+"/extra",
		func(struct{}, server.HandlerContext) (struct{}, server.IAPIError) { return struct{}{}, nil })
}

func TestTrespassIntoFamilyFailsStartup(t *testing.T) {
	routes, _, runErr := bootUntilRoutes(t, trespassModule{partnerportal.NewModule()})

	require.ErrorIs(t, runErr, server.ErrDuplicateRoute)
	assert.ErrorContains(t, runErr, "inside a tenant-owned route family")
	assert.Nil(t, routes, "startup stopped before the route-table hook")
}

// globalMiddlewareModule installs app-wide middleware beside the family.
type globalMiddlewareModule struct{ *partnerportal.Module }

func (globalMiddlewareModule) GlobalMiddleware() []server.MiddlewareFunc {
	return []server.MiddlewareFunc{func(_ server.HandlerContext, next func() error) error { return next() }}
}

func TestGlobalMiddlewareBesideFamilyFailsStartup(t *testing.T) {
	routes, _, runErr := bootUntilRoutes(t, globalMiddlewareModule{partnerportal.NewModule()})

	require.ErrorIs(t, runErr, server.ErrTenantFamilyWithGlobalMiddleware)
	assert.Nil(t, routes, "startup stopped before the route-table hook")
}
