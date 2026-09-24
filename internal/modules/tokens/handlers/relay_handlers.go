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
	PAN string `json:"pan" validate:"required,number,min=13,max=19"`
}

// RedactedForLog masks the PAN the same way TokenizeRequest does: this body is
// plaintext on the wire, so it is the likeliest one to be logged whole.
func (r RelayRequest) RedactedForLog() any { return panLogView(r.PAN) }

// RelayResponse mirrors the unsealed token payload produced by the partner.
type RelayResponse struct {
	Token *domain.Token `json:"token"`
}

// RelayHandler bridges plaintext HTTP into the JOSE-wrapped outbound path. One
// type serves every relay whose entry point is a bare RelayRequest: the wire
// shape lives in how the RelayService's client was wired, never here.
type RelayHandler struct {
	svc    RelayService
	logger logger.Logger
	// path is the route the relay is served on.
	path string
	// failure is both the log message and the generic 500 message; the relay's
	// error itself stays in the server log.
	failure string
}

// NewRelayHandler wires the nested JWE-of-JWS RelayService into the HTTP layer.
func NewRelayHandler(svc RelayService, l logger.Logger) *RelayHandler {
	return &RelayHandler{svc: svc, logger: l, path: "/tokens/relay", failure: "relay failed"}
}

// NewVTSIssuerRelayHandler wires the JWS-of-JWE (Visa Token Service Issuer)
// RelayService into the HTTP layer. Same request and response contract as the
// nested relay.
//
// Unlike the other two relays it has no simulator route beside it: the VTS
// Issuer counterparty is the relay client's base transport (see
// service.VTSIssuerPeerSimulator for why a /__sim/ route cannot carry this wire
// shape).
func NewVTSIssuerRelayHandler(svc RelayService, l logger.Logger) *RelayHandler {
	return &RelayHandler{svc: svc, logger: l, path: "/tokens/vts-issuer-relay", failure: "VTS issuer relay failed"}
}

// Relay handles POST /api/v1/tokens/relay (or the path the constructor chose).
func (h *RelayHandler) Relay(req RelayRequest, ctx server.HandlerContext) (*RelayResponse, server.IAPIError) {
	tok, err := h.svc.Relay(ctx.RequestContext(), req.PAN)
	if err != nil {
		h.logger.Error().Err(err).Msg(h.failure)
		return nil, server.NewInternalServerError(h.failure)
	}
	return &RelayResponse{Token: tok}, nil
}

// RegisterRoute attaches the relay endpoint under the partner path namespace.
func (h *RelayHandler) RegisterRoute(hr *server.HandlerRegistry, r server.RouteRegistrar) {
	server.POST(hr, r, h.path, h.Relay)
}
