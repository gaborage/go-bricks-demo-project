package handlers

import (
	"context"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/tokens/domain"
	"github.com/gaborage/go-bricks/logger"
	"github.com/gaborage/go-bricks/server"
)

// RelayService is the narrow interface the relay handler depends on.
type RelayService interface {
	Relay(ctx context.Context, pan string) (*domain.Token, error)
}

// RelayRequest is plaintext on the wire — this is the developer-facing entry
// point that exercises the OUTBOUND JOSE path. The seal happens internally
// when the relay service POSTs to the partner URL.
type RelayRequest struct {
	PAN string `json:"pan" validate:"required,numeric,min=13,max=19"`
}

// RelayResponse mirrors the unsealed token payload produced by the partner.
type RelayResponse struct {
	Token *domain.Token `json:"token"`
}

// RelayHandler bridges plaintext HTTP into the JOSE-wrapped outbound path.
type RelayHandler struct {
	svc    RelayService
	logger logger.Logger
}

// NewRelayHandler wires a RelayService into the HTTP layer.
func NewRelayHandler(svc RelayService, l logger.Logger) *RelayHandler {
	return &RelayHandler{svc: svc, logger: l}
}

// Relay handles POST /api/v1/tokens/relay.
func (h *RelayHandler) Relay(req RelayRequest, ctx server.HandlerContext) (*RelayResponse, server.IAPIError) {
	tok, err := h.svc.Relay(ctx.RequestContext(), req.PAN)
	if err != nil {
		h.logger.Error().Err(err).Msg("relay failed")
		return nil, server.NewInternalServerError("relay failed")
	}
	return &RelayResponse{Token: tok}, nil
}

// RegisterRoute attaches the relay endpoint under the partner path namespace.
func (h *RelayHandler) RegisterRoute(hr *server.HandlerRegistry, r server.RouteRegistrar) {
	server.POST(hr, r, "/tokens/relay", h.Relay)
}
