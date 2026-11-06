// Package logproof makes three always-on go-bricks logging behaviors visible
// (go-bricks main 0eb5f27f, C73.29-C73.33):
//
//   - Inbound paths render from the matched route template. The access line's
//     url.path and message, the span's url.path and every framework WARN and
//     ERROR path print /…/REDACTED for each parameter, never the request bytes;
//     http.route keeps the raw template (ADR-167).
//   - Framework WARN and ERROR lines about a request (Panic recovered, unhandled
//     error, the tenant, IP pre-guard and forwarded-client-cert WARNs) carry
//     http.route, request_id and correlation_id, plus trace_id/span_id under a
//     valid span context.
//   - A WARN logged through the request context, or HandlerContext.EscalateSeverity,
//     raises the request's access line to level=warn result_code=WARN instead
//     of dropping it.
//
// The module owns only the routes those behaviors need and that no other module
// has: a handler that logs a WARN, one that escalates, one that panics. Masking
// on an ordinary route is shown on the legacy module's GET /legacy/products/:id.
// See scripts/log-correlation-demo.sh (make log-correlation-demo).
//
// The routes are OFF by default. Two guards must both pass: the explicit opt-in
// custom.logproof.enabled (env CUSTOM_LOGPROOF_ENABLED=true) and a development
// environment. The environment alone is not enough: development is the framework
// default and the Dockerfile's APP_ENV, so a deploy that kept it would otherwise
// expose an unauthenticated route that panics on every call.
package logproof

import (
	"github.com/gaborage/go-bricks-demo-project/internal/modules/logproof/handlers"
	"github.com/gaborage/go-bricks/app"
	"github.com/gaborage/go-bricks/logger"
	"github.com/gaborage/go-bricks/server"
)

// moduleName identifies this module for registration and logging.
const moduleName = "logproof"

// ConfigKeyEnabled opts in to the simulator routes. A custom.* key so the
// framework's config loader carries it: YAML under `custom:` or, with the env
// mapping (lower-case, "_" -> "."), CUSTOM_LOGPROOF_ENABLED=true.
const ConfigKeyEnabled = "custom.logproof.enabled"

// Module implements app.Module and app.RouteRegisterer.
type Module struct {
	logger  logger.Logger
	handler *handlers.Handler

	// simEnabled is true only when ConfigKeyEnabled is set AND the environment is
	// development. One route here panics on purpose, so the module fails closed:
	// with either guard off, no route is registered.
	simEnabled bool
}

var _ app.RouteRegisterer = (*Module)(nil)

// NewModule returns an unwired Module. Init populates dependencies.
func NewModule() *Module {
	return &Module{}
}

// Name implements app.Module.
func (m *Module) Name() string { return moduleName }

// Init wires the handler and reads both guards.
func (m *Module) Init(deps *app.ModuleDeps) error {
	m.logger = deps.Logger.WithFields(map[string]any{"module": moduleName})
	m.handler = handlers.NewHandler(m.logger)
	m.simEnabled = deps.Config != nil &&
		deps.Config.Bool(ConfigKeyEnabled, false) &&
		deps.Config.App.IsDevelopment()

	m.logger.Info().
		Bool("simulators", m.simEnabled).
		Msg("Logproof module initialized — masked paths and correlated framework lines")
	return nil
}

// RegisterRoutes attaches the simulator routes only where simEnabled allows it.
func (m *Module) RegisterRoutes(hr *server.HandlerRegistry, r server.RouteRegistrar) {
	if !m.simEnabled {
		m.logger.Info().
			Str("switch", ConfigKeyEnabled).
			Msg("Logproof simulators not registered — needs CUSTOM_LOGPROOF_ENABLED=true in a development environment")
		return
	}
	m.handler.RegisterSimulatorRoutes(hr, r)
}

// Shutdown is a no-op: the module holds no resources.
func (m *Module) Shutdown() error { return nil }
