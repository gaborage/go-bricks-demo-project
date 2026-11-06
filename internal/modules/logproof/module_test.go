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

// Values for depsFor's optIn: the switch as the environment spells it, or absent.
const (
	switchOn     = "true"
	switchOff    = "false"
	switchAbsent = ""
)

// depsFor builds the deps of a boot in env, with the opt-in switch set to optIn
// (switchAbsent leaves it out). The switch is a custom.* key, so the config is
// loaded from a map the way the framework loads YAML and the environment.
func depsFor(t *testing.T, env, optIn string) *app.ModuleDeps {
	t.Helper()
	data := map[string]any{"app.name": "test", "app.version": "1.0.0", "app.env": env}
	if optIn != switchAbsent {
		data[ConfigKeyEnabled] = optIn
	}
	cfg, err := config.LoadFromMap(data)
	require.NoError(t, err)
	return &app.ModuleDeps{Logger: logger.New("disabled", false), Config: cfg}
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

func TestSimulatorsRegisterInDevelopmentWhenOptedIn(t *testing.T) {
	routes := registeredRoutes(t, depsFor(t, config.EnvDevelopment, switchOn))

	assert.ElementsMatch(t, []addedRoute{
		{method: http.MethodGet, path: handlers.WarnPath},
		{method: http.MethodGet, path: handlers.EscalatePath},
		{method: http.MethodGet, path: handlers.PanicPath},
	}, routes)
}

// Development is the framework default and the Dockerfile's APP_ENV, so the
// environment alone must not expose the panic route: without the opt-in, a
// development boot registers nothing.
func TestSimulatorsAbsentInDevelopmentWithoutOptIn(t *testing.T) {
	for name, optIn := range map[string]string{"switch absent": switchAbsent, "switch false": switchOff} {
		t.Run(name, func(t *testing.T) {
			assert.Empty(t, registeredRoutes(t, depsFor(t, config.EnvDevelopment, optIn)))
		})
	}
}

// The opt-in is not enough either: every other environment, and a missing
// config, registers nothing.
func TestSimulatorsAbsentOutsideDevelopment(t *testing.T) {
	for _, env := range []string{"production", "staging", "test"} {
		t.Run(env, func(t *testing.T) {
			assert.Empty(t, registeredRoutes(t, depsFor(t, env, switchOn)))
		})
	}

	t.Run("nil config", func(t *testing.T) {
		assert.Empty(t, registeredRoutes(t, &app.ModuleDeps{Logger: logger.New("disabled", false)}))
	})
}
