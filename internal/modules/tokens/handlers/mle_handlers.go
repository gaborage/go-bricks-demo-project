package handlers

import (
	"context"
	"errors"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/tokens/domain"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/tokens/service"
	"github.com/gaborage/go-bricks/logger"
	"github.com/gaborage/go-bricks/server"
)

// MLERelayService is the narrow interface the MLE relay handler depends on.
type MLERelayService interface {
	Relay(ctx context.Context, pan string) (*domain.Token, error)
}

// MLEPeerSimulator is the narrow interface the peer route depends on: one
// compact JWE in, one compact JWE out.
type MLEPeerSimulator interface {
	Process(ctx context.Context, encData string) (string, error)
}

// MLERelayRequest is plaintext on the wire — the developer-facing entry point
// that drives the OUTBOUND bare-JWE path. Same validation floor as the nested
// relay: the boundary check does not get weaker because the crypto shape changed.
type MLERelayRequest struct {
	PAN string `json:"pan" validate:"required,number,min=13,max=19"`
}

// MLERelayResponse mirrors the plaintext the partner sealed back to us.
type MLERelayResponse struct {
	Token *domain.Token `json:"token"`
}

// MLEEnvelope is Visa Message Level Encryption's wire object in both directions:
// a JSON body whose single encData member carries the compact JWE. It is bound
// as an ordinary typed request rather than read as raw bytes because the
// envelope IS plain JSON — only its payload is ciphertext.
type MLEEnvelope struct {
	EncData string `json:"encData" validate:"required"`
}

// MLEHandler serves the MLE relay entry point and the in-process peer simulator.
type MLEHandler struct {
	relay MLERelayService
	peer  MLEPeerSimulator
	log   logger.Logger
}

// NewMLEHandler wires the MLE relay and simulator into the HTTP layer.
func NewMLEHandler(relay MLERelayService, peer MLEPeerSimulator, l logger.Logger) *MLEHandler {
	return &MLEHandler{relay: relay, peer: peer, log: l}
}

// Relay handles POST /api/v1/tokens/mle-relay.
func (h *MLEHandler) Relay(req MLERelayRequest, ctx server.HandlerContext) (*MLERelayResponse, server.IAPIError) {
	tok, err := h.relay.Relay(ctx.RequestContext(), req.PAN)
	if err != nil {
		h.log.Error().Err(err).Msg("MLE relay failed")
		return nil, server.NewInternalServerError("MLE relay failed")
	}
	return &MLERelayResponse{Token: tok}, nil
}

// PeerSimulate handles POST /api/v1/__sim/peer/mle — the MLE counterparty.
//
// Unlike the nested simulator this route carries NO jose: tags: the tag grammar
// has no `mode` key, so an inbound server route cannot select bare mode. The
// simulator therefore opens and seals by hand through jose.Open/jose.Seal.
func (h *MLEHandler) PeerSimulate(req MLEEnvelope, ctx server.HandlerContext) (*MLEEnvelope, server.IAPIError) {
	compact, err := h.peer.Process(ctx.RequestContext(), req.EncData)
	if err != nil {
		if errors.Is(err, service.ErrInvalidPAN) {
			return nil, server.NewBadRequestError("invalid PAN")
		}
		h.log.Error().Err(err).Msg("MLE peer simulator failed")
		return nil, server.NewInternalServerError("MLE simulator failed")
	}
	return &MLEEnvelope{EncData: compact}, nil
}

// RegisterRoutes attaches the MLE relay entry point and the peer simulator.
//
// The simulator is registered WithRawResponse: the peer must answer with the
// bare {"encData":...} object Visa specifies. Wrapping it in the standard
// APIResponse envelope would bury encData under "data", and the client-side
// VisaMLEEnvelope recognizes replies by shape — it would find no top-level
// encData, and the relay client would refuse the 200 as
// httpclient.ErrJOSEPlaintextResponse (go-bricks v0.65.0, #1637).
func (h *MLEHandler) RegisterRoutes(hr *server.HandlerRegistry, r server.RouteRegistrar) {
	server.POST(hr, r, "/tokens/mle-relay", h.Relay)
	server.POST(hr, r, "/__sim/peer/mle", h.PeerSimulate,
		server.WithRawResponse(),
		server.WithTags("simulator"),
	)
}
