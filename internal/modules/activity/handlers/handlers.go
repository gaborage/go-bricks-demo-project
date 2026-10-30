// Package handlers exposes the product-activity projection over HTTP, plus the
// demo-only poison simulator that proves what the framework does with a stream
// body its typed consumer cannot decode.
package handlers

import (
	"context"
	"errors"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/activity/domain"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/activity/service"
	"github.com/gaborage/go-bricks/logger"
	"github.com/gaborage/go-bricks/server"
)

const (
	// poisonPayload is deliberately not JSON. The typed consumer decodes every
	// body into domain.ProductActivity before the handler runs, so this one fails
	// at the decode stage and comes back as streams.ErrPayloadUndecodable.
	poisonPayload = "not-json{{{"

	// defaultPoisonProductID is the routing key used when the caller names no
	// product. A super-stream publish REQUIRES a non-empty key, so the simulator
	// always sends one — the poison has to land on a real partition for the demo
	// to show what happens next.
	defaultPoisonProductID = "poison-demo"

	poisonNote = "published a non-JSON body: the typed consumer rejects it as deterministic poison — " +
		"not retried in place, never parked in the hold, offset NOT committed, and consumption continues " +
		"with the next message on that partition"

	// ProductActivityPath is an inline verb (go-bricks v0.73.0, C73.10, echo
	// v5.4.1): the `\:` right after the parameter name ends the name, so the
	// route matches only `<id>:activity` and binds id to the part before the
	// colon. It is its own route beside the products module's GET /products/:id.
	// On v0.72.0 the `\:activity` was part of the parameter name, the route
	// matched any segment, and startup refused the pair as a duplicate.
	//
	// The colon must reach the server as written. A percent-encoded one
	// (/products/42%3Aactivity) is not a verb: it reaches GET /products/:id with
	// id = "42%3Aactivity". A raw string keeps the backslash literal.
	ProductActivityPath = `/products/:id\:activity`
)

// ActivityProjection is the narrow service contract this package depends on.
// Declared here, not in service/, so the handlers compile against an interface
// they own and tests can stub it.
type ActivityProjection interface {
	Snapshot(limit int) service.Snapshot
	ProductSlice(productID string) service.ProductSlice
	PublishRaw(ctx context.Context, routingKey string, data []byte) error
}

// SnapshotRequest is the query surface of GET /products/activity.
type SnapshotRequest struct {
	// Limit caps the recent-event slice. Omitted means the ring's full depth.
	Limit int `query:"limit" validate:"omitempty,min=1,max=50"`
}

// ProductActivityRequest is the path of GET /products/:id\:activity.
type ProductActivityRequest struct {
	// ID is the part of the segment before ":activity". The bound is the one
	// PoisonRequest puts on a routing key, which is what a product id is on the
	// stream. A product id is a UUID, but the projection only ever matches it, so
	// a stricter format check would add nothing.
	ID string `param:"id" validate:"required,max=64"`
}

// PoisonRequest is the body of the poison simulator. ProductID is optional and
// only picks the partition the malformed body lands on.
type PoisonRequest struct {
	ProductID string `json:"productId" validate:"omitempty,max=64"`
}

// PoisonResponse reports where the malformed body went, so the caller knows which
// partition's logs to read.
type PoisonResponse struct {
	SuperStream string `json:"superStream"`
	RoutingKey  string `json:"routingKey"`
	Payload     string `json:"payload"`
	Note        string `json:"note"`
}

// Handler serves the projection and the poison simulator.
type Handler struct {
	svc    ActivityProjection
	logger logger.Logger
}

// NewHandler wires the projection into the HTTP layer.
func NewHandler(svc ActivityProjection, l logger.Logger) *Handler {
	return &Handler{svc: svc, logger: l}
}

// GetActivity handles GET /products/activity — the in-memory projection built by
// the super-stream consumer. The snapshot type IS the wire contract here: it is
// already a defensive copy with camelCase JSON tags, and re-shaping it into a
// second struct would only create a place for the two to drift.
func (h *Handler) GetActivity(req SnapshotRequest, _ server.HandlerContext) (*service.Snapshot, server.IAPIError) {
	snapshot := h.svc.Snapshot(req.Limit)
	return &snapshot, nil
}

// GetProductActivity handles GET /products/:id\:activity, one product's slice of
// the projection. An id with no projected events answers 200 with zero counts,
// not 404: the projection cannot know whether the product exists (see
// service.ProductSlice), so it reports what it has seen and nothing more.
func (h *Handler) GetProductActivity(req ProductActivityRequest, _ server.HandlerContext) (*service.ProductSlice, server.IAPIError) {
	slice := h.svc.ProductSlice(req.ID)
	return &slice, nil
}

// PublishPoison handles POST /__sim/streams/poison — DEMO ONLY, and registered
// only outside production (see the activity module's RegisterRoutes).
//
// It publishes through the module's ONE publisher handle, so the malformed body
// travels the same path a real event does and lands on a real partition.
func (h *Handler) PublishPoison(req PoisonRequest, ctx server.HandlerContext) (*PoisonResponse, server.IAPIError) {
	routingKey := req.ProductID
	if routingKey == "" {
		routingKey = defaultPoisonProductID
	}

	if err := h.svc.PublishRaw(ctx.RequestContext(), routingKey, []byte(poisonPayload)); err != nil {
		if errors.Is(err, service.ErrPublisherUnset) {
			return nil, server.NewServiceUnavailableError("stream publisher is not bound yet")
		}
		h.logger.Error().Err(err).Str("routingKey", routingKey).Msg("Failed to publish poison message")
		return nil, server.NewInternalServerError("failed to publish poison message")
	}

	h.logger.Warn().
		Str("routingKey", routingKey).
		Str("superStream", domain.SuperStream).
		Msg("Published a deliberately malformed body to the product-activity super stream (demo simulator)")

	return &PoisonResponse{
		SuperStream: domain.SuperStream,
		RoutingKey:  routingKey,
		Payload:     poisonPayload,
		Note:        poisonNote,
	}, nil
}

// RegisterRoutes attaches GET /products/activity and GET /products/:id\:activity.
//
// Both coexist with the products module's /products/:id. The router gives the
// static /products/activity segment priority over a parameter, and the inline
// verb is a route of its own (see ProductActivityPath). Registration order
// between the two modules does not matter for either.
func (h *Handler) RegisterRoutes(hr *server.HandlerRegistry, r server.RouteRegistrar) {
	server.GET(hr, r, "/products/activity", h.GetActivity)
	server.GET(hr, r, ProductActivityPath, h.GetProductActivity)
}

// RegisterSimulatorRoute attaches the poison simulator at /__sim/streams/poison,
// mirroring the tokens module's peer simulator: same base group, a /__sim/ prefix
// that makes the demo-only intent obvious in every access log, and the
// "simulator" tag cmd/api requires on every /__sim/ route before it starts.
func (h *Handler) RegisterSimulatorRoute(hr *server.HandlerRegistry, r server.RouteRegistrar) {
	server.POST(hr, r, "/__sim/streams/poison", h.PublishPoison,
		server.WithTags("simulator"),
	)
}
