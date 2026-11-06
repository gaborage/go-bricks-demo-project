// Package handlers serves the logproof module's two simulator routes. Each one
// exists to make a framework log line appear, so a reader can check what that
// line carries: the masked path, the correlation fields, the access line's level.
package handlers

import (
	"github.com/gaborage/go-bricks/logger"
	"github.com/gaborage/go-bricks/server"
	"github.com/rs/zerolog"
)

// Route templates, relative to the API base path. The token segment stands in
// for any request value a path can carry (a card number, an email): the
// framework must never print it, only the template with REDACTED in its place.
const (
	WarnPath     = "/__sim/observability/warn/:token"
	EscalatePath = "/__sim/observability/escalate/:token"
	PanicPath    = "/__sim/observability/panic/:token"

	// RequestIDHeader is the header the framework's request-id middleware sets
	// on every response before the handler runs.
	RequestIDHeader = "X-Request-ID"

	// SimulatorTag is the tag cmd/api/route_policy.go requires on every /__sim/ route.
	SimulatorTag = "simulator"

	// WarnMessage is the WARN the warn route logs through the request context.
	WarnMessage = "logproof: handler WARN logged through the request context"

	// PanicMessage is the constant panic value. It never carries the token: in
	// development app.debug is on, and the Panic recovered line then prints the
	// panic value.
	PanicMessage = "logproof: deliberate handler panic"
)

// TokenRequest binds the path parameter. The handlers never log it.
type TokenRequest struct {
	Token string `param:"token" validate:"required"`
}

// ProofResponse tells the caller which access-line level to look for. It holds
// the route template, never the token.
type ProofResponse struct {
	Route         string `json:"route"`
	AccessLevel   string `json:"accessLevel"`
	EscalatedWith string `json:"escalatedWith"`
}

// Handler serves the simulator routes.
type Handler struct {
	logger logger.Logger
}

// NewHandler returns a Handler that logs through l.
func NewHandler(l logger.Logger) *Handler {
	return &Handler{logger: l}
}

// Warn logs one WARN through the request context and answers 200. The
// framework's severity hook rides on that context, so the WARN raises the
// request's access line to level=warn result_code=WARN (go-bricks C73.33)
// instead of dropping it.
//
// A consumer line gets trace_id/span_id from WithContext under a valid span,
// but no request id: the framework stamps request_id and correlation_id only on
// its own lines. The handler copies the response's X-Request-ID (set by the
// request-id middleware before any handler runs) so this line joins its access
// line on the same key, and logs the route template, never the path.
func (h *Handler) Warn(_ TokenRequest, ctx server.HandlerContext) (*ProofResponse, server.IAPIError) {
	h.logger.WithContext(ctx.RequestContext()).Warn().
		Str("http.route", ctx.RouteTemplate()).
		Str("request_id", ctx.ResponseWriter().Header().Get(RequestIDHeader)).
		Msg(WarnMessage)
	return &ProofResponse{Route: ctx.RouteTemplate(), AccessLevel: "warn", EscalatedWith: "request-context WARN"}, nil
}

// Escalate logs nothing and calls HandlerContext.EscalateSeverity. The access
// line is still written, at level=warn result_code=WARN (C73.33).
func (h *Handler) Escalate(_ TokenRequest, ctx server.HandlerContext) (*ProofResponse, server.IAPIError) {
	ctx.EscalateSeverity(zerolog.WarnLevel)
	return &ProofResponse{Route: ctx.RouteTemplate(), AccessLevel: "warn", EscalatedWith: "EscalateSeverity"}, nil
}

// Panic panics with a constant value. The framework recovers it, answers 500
// and logs Panic recovered with http.route, request_id and correlation_id
// (C73.31); the access line prints the masked path (C73.29).
func (h *Handler) Panic(_ TokenRequest, _ server.HandlerContext) (*ProofResponse, server.IAPIError) {
	panic(PanicMessage)
}

// RegisterSimulatorRoutes attaches the three routes. The caller decides whether
// they exist at all: the module registers them in development only.
func (h *Handler) RegisterSimulatorRoutes(hr *server.HandlerRegistry, r server.RouteRegistrar) {
	server.GET(hr, r, WarnPath, h.Warn, server.WithTags(SimulatorTag))
	server.GET(hr, r, EscalatePath, h.Escalate, server.WithTags(SimulatorTag))
	server.GET(hr, r, PanicPath, h.Panic, server.WithTags(SimulatorTag))
}
