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
package logproof

import (
	"github.com/gaborage/go-bricks-demo-project/internal/modules/logproof/handlers"
	"github.com/gaborage/go-bricks/app"
	"github.com/gaborage/go-bricks/logger"
	"github.com/gaborage/go-bricks/server"
)

// moduleName identifies this module for registration and logging.
const moduleName = "logproof"

// Module implements app.Module and app.RouteRegisterer.
type Module struct {
	logger  logger.Logger
	handler *handlers.Handler

	// simEnabled gates every route to development, the guard the activity
	// module's poison simulator uses. One route here panics on purpose, so the
	// module fails closed in any other environment: no route is registered.
	simEnabled bool
}

var _ app.RouteRegisterer = (*Module)(nil)

// NewModule returns an unwired Module. Init populates dependencies.
func NewModule() *Module {
	return &Module{}
}

// Name implements app.Module.
func (m *Module) Name() string { return moduleName }

// Init wires the handler and reads the environment guard.
func (m *Module) Init(deps *app.ModuleDeps) error {
	m.logger = deps.Logger.WithFields(map[string]any{"module": moduleName})
	m.handler = handlers.NewHandler(m.logger)
	m.simEnabled = deps.Config != nil && deps.Config.App.IsDevelopment()

	m.logger.Info().
		Bool("simulators", m.simEnabled).
		Msg("Logproof module initialized — masked paths and correlated framework lines")
	return nil
}

// RegisterRoutes attaches the simulator routes only where simEnabled allows it.
func (m *Module) RegisterRoutes(hr *server.HandlerRegistry, r server.RouteRegistrar) {
	if !m.simEnabled {
		m.logger.Info().Msg("Logproof simulators not registered — development environments only")
		return
	}
	m.handler.RegisterSimulatorRoutes(hr, r)
}

// Shutdown is a no-op: the module holds no resources.
func (m *Module) Shutdown() error { return nil }
