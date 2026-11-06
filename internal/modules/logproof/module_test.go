package logproof

import (
	"net/http"
	"testing"

	"github.com/gaborage/go-bricks/app"
	"github.com/gaborage/go-bricks/config"
	"github.com/gaborage/go-bricks/logger"
	"github.com/gaborage/go-bricks/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/logproof/handlers"
)

type addedRoute struct{ method, path string }

// recordingRegistrar records every route the typed adapters add through it.
type recordingRegistrar struct{ added []addedRoute }

func (r *recordingRegistrar) Add(method, path string, _ server.Handler, _ ...server.MiddlewareFunc) {
	r.added = append(r.added, addedRoute{method: method, path: path})
}

func (r *recordingRegistrar) Group(string, ...server.MiddlewareFunc) server.RouteRegistrar { return r }
func (r *recordingRegistrar) Use(...server.MiddlewareFunc)                                 {}
func (r *recordingRegistrar) FullPath(path string) string                                  { return path }

func depsFor(env string) *app.ModuleDeps {
	return &app.ModuleDeps{
		Logger: logger.New("disabled", false),
		Config: &config.Config{App: config.AppConfig{Name: "test", Version: "1.0.0", Env: env}},
	}
}

func registeredRoutes(t *testing.T, deps *app.ModuleDeps) []addedRoute {
	t.Helper()
	m := NewModule()
	require.NoError(t, m.Init(deps))
	reg := &recordingRegistrar{}
	m.RegisterRoutes(server.NewHandlerRegistry(deps.Config), reg)
	return reg.added
}

func TestModuleName(t *testing.T) {
	assert.Equal(t, "logproof", NewModule().Name())
}

func TestModuleShutdown(t *testing.T) {
	assert.NoError(t, NewModule().Shutdown())
}

func TestSimulatorsRegisterInDevelopment(t *testing.T) {
	routes := registeredRoutes(t, depsFor(config.EnvDevelopment))

	assert.ElementsMatch(t, []addedRoute{
		{method: http.MethodGet, path: handlers.WarnPath},
		{method: http.MethodGet, path: handlers.EscalatePath},
		{method: http.MethodGet, path: handlers.PanicPath},
	}, routes)
}

// The panic route must not exist where the binary is deployed, so every other
// environment, and a missing config, registers nothing.
func TestSimulatorsAbsentOutsideDevelopment(t *testing.T) {
	for _, env := range []string{"production", "staging", "test"} {
		t.Run(env, func(t *testing.T) {
			assert.Empty(t, registeredRoutes(t, depsFor(env)))
		})
	}

	t.Run("nil config", func(t *testing.T) {
		assert.Empty(t, registeredRoutes(t, &app.ModuleDeps{Logger: logger.New("disabled", false)}))
	})
}
