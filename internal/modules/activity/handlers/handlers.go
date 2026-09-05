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
)

// ActivityProjection is the narrow service contract this package depends on.
// Declared here, not in service/, so the handlers compile against an interface
// they own and tests can stub it.
type ActivityProjection interface {
	Snapshot(limit int) service.Snapshot
	PublishRaw(ctx context.Context, routingKey string, data []byte) error
}

// SnapshotRequest is the query surface of GET /products/activity.
type SnapshotRequest struct {
	// Limit caps the recent-event slice. Omitted means the ring's full depth.
	Limit int `query:"limit" validate:"omitempty,min=1,max=50"`
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

// RegisterRoutes attaches GET /products/activity.
//
// The static /products/activity segment coexists with the products module's
// /products/:id: the router gives a static segment priority over a parameter, so
// registration order between the two modules does not matter.
func (h *Handler) RegisterRoutes(hr *server.HandlerRegistry, r server.RouteRegistrar) {
	server.GET(hr, r, "/products/activity", h.GetActivity)
}

// RegisterSimulatorRoute attaches the poison simulator at /__sim/streams/poison,
// mirroring the tokens module's peer simulator: same base group, a /__sim/ prefix
// that makes the demo-only intent obvious in every access log.
func (h *Handler) RegisterSimulatorRoute(hr *server.HandlerRegistry, r server.RouteRegistrar) {
	server.POST(hr, r, "/__sim/streams/poison", h.PublishPoison)
}
