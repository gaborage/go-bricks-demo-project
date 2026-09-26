// Package handlers wires the tokens module's HTTP surface and declares the
// JOSE policies that protect every payload on the wire.
//
// The struct tags on TokenizeRequest / TokenizeResponse drive the framework's
// JOSE middleware. Tagging both request and response is required — the server
// panics at registration time if only one side carries jose: tags.
package handlers

import (
	"context"
	"errors"
	"time"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/tokens/domain"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/tokens/service"
	"github.com/gaborage/go-bricks/jose"
	"github.com/gaborage/go-bricks/logger"
	"github.com/gaborage/go-bricks/server"
)

// Maximum age (relative to a verified iat claim) before we reject a payload as
// replayed. Visa skews tend to be tighter than this; we err generous for the
// demo so a developer's clock drift doesn't break the happy path.
const maxClaimAge = 5 * time.Minute

// TokenizeRequest is the JOSE-protected POST body of the partner-facing /tokens route.
//
// jose tag: inbound = decrypt with our private key (tokens-our), verify the JWS with
// the peer's public key (tokens-peer). The framework decrypts + verifies before our
// handler is invoked — the handler sees a plain Go struct.
type TokenizeRequest struct {
	_   struct{} `jose:"decrypt=tokens-our,verify=tokens-peer"`
	PAN string   `json:"pan" validate:"required,number,min=13,max=19"`
}

// Every request in this package that carries a PAN masks itself when a
// filtered logger is handed the whole struct.
var (
	_ logger.Redactor = TokenizeRequest{}
	_ logger.Redactor = PeerSimRequest{}
	_ logger.Redactor = RelayRequest{}
	_ logger.Redactor = MLERelayRequest{}
)

// RedactedForLog implements logger.Redactor (go-bricks v0.65.0, ADR-110): a
// filtered logger's Interface and WithFields render the last four digits in
// place of the struct, so a handler that logs the decrypted request whole never
// writes the PAN. It backs up log.sensitivefields, which masks the field only
// while it is still NAMED pan. The hook is not consulted at Err, through Msgf
// or by an unfiltered logger. VALUE receiver on purpose: a pointer-receiver
// method would leave a bare request unrecognized and walked field by field.
func (r TokenizeRequest) RedactedForLog() any { return panLogView(r.PAN) }

// panLogView is the log-safe shape shared by the package's PAN-bearing
// requests: the last four digits and nothing else. Its key must never contain
// "pan", or the needle list would mask the result it filters again.
func panLogView(pan string) map[string]any {
	last4 := ""
	if len(pan) >= 4 {
		last4 = pan[len(pan)-4:]
	}
	return map[string]any{"last4": last4}
}

// TokenizeResponse is the JOSE-protected response. Outbound = sign with our private
// key (tokens-our) + encrypt to the peer's public key (tokens-peer). The framework
// seals before returning the bytes on the wire.
type TokenizeResponse struct {
	_     struct{}      `jose:"sign=tokens-our,encrypt=tokens-peer"`
	Token *domain.Token `json:"token"`
}

// PeerSimRequest mirrors TokenizeRequest with the *inverse* JOSE policies — it
// is the in-process simulator that lets the demo drive an outbound JOSETransport
// call (POST /__sim/peer/tokens) end-to-end without a real partner endpoint.
//
// In production code you would never declare both halves of an integration; this
// pair only coexists because the simulator is the same process that owns both
// sides of the keystore.
type PeerSimRequest struct {
	_   struct{} `jose:"decrypt=tokens-peer,verify=tokens-our"`
	PAN string   `json:"pan" validate:"required,number,min=13,max=19"`
}

// RedactedForLog masks the PAN the same way TokenizeRequest does.
func (r PeerSimRequest) RedactedForLog() any { return panLogView(r.PAN) }

// PeerSimResponse is the simulator's outbound seal — peer signs, peer encrypts to us.
type PeerSimResponse struct {
	_     struct{}      `jose:"sign=tokens-peer,encrypt=tokens-our"`
	Token *domain.Token `json:"token"`
}

// TokenizationService is the narrow interface the handler depends on. Defined
// here (not in service/) so handlers compile against an interface they own,
// keeping the package importable in tests with a stub.
type TokenizationService interface {
	Tokenize(ctx context.Context, pan string) (*domain.Token, error)
}

// Handler serves the partner-facing /tokens route and the in-process peer simulator.
type Handler struct {
	svc    TokenizationService
	logger logger.Logger
}

// NewHandler wires the tokenization service into the HTTP layer.
func NewHandler(svc TokenizationService, l logger.Logger) *Handler {
	return &Handler{svc: svc, logger: l}
}

// CreateToken handles POST /api/v1/tokens — the partner-facing JOSE route.
//
// By the time this runs, the framework has decrypted the JWE, verified the JWS
// against tokens-peer, and bound the inner JSON into req. Application code
// only sees plain structs.
func (h *Handler) CreateToken(req TokenizeRequest, ctx server.HandlerContext) (*TokenizeResponse, server.IAPIError) {
	if err := h.enforceClaimFreshness(ctx); err != nil {
		return nil, err
	}

	tok, err := h.svc.Tokenize(ctx.RequestContext(), req.PAN)
	if err != nil {
		if errors.Is(err, service.ErrInvalidPAN) {
			return nil, server.NewBadRequestError("invalid PAN")
		}
		h.logger.Error().Err(err).Msg("tokenization failed")
		return nil, server.NewInternalServerError("tokenization failed")
	}
	return &TokenizeResponse{Token: tok}, nil
}

// PeerSimulate handles POST /__sim/peer/tokens — the inverse-policy route that
// stands in for a Visa-style counterparty. It exists purely so the relay
// service has somewhere to send its JOSE-sealed outbound request.
func (h *Handler) PeerSimulate(req PeerSimRequest, ctx server.HandlerContext) (*PeerSimResponse, server.IAPIError) {
	tok, err := h.svc.Tokenize(ctx.RequestContext(), req.PAN)
	if err != nil {
		if errors.Is(err, service.ErrInvalidPAN) {
			return nil, server.NewBadRequestError("invalid PAN")
		}
		return nil, server.NewInternalServerError("simulator tokenization failed")
	}
	return &PeerSimResponse{Token: tok}, nil
}

// enforceClaimFreshness rejects requests whose verified iat claim falls outside
// ±maxClaimAge of now. Future-dated iat is symmetric clock skew, but it also
// covers preplay: an attacker who captures a sealed payload and replays it
// later cannot extend its validity by post-dating the claim.
// The framework verifies the signature; applications enforce timing.
func (h *Handler) enforceClaimFreshness(ctx server.HandlerContext) server.IAPIError {
	claims := jose.ClaimsFromContext(ctx.RequestContext())
	if claims == nil || claims.IssuedAt.IsZero() {
		return nil
	}
	skew := time.Since(claims.IssuedAt)
	if skew > maxClaimAge {
		return server.NewUnauthorizedError("payload too old")
	}
	if skew < -maxClaimAge {
		return server.NewUnauthorizedError("payload issued in the future")
	}
	return nil
}

// RegisterPartnerRoute attaches POST /tokens under the standard /api/v1 base path.
func (h *Handler) RegisterPartnerRoute(hr *server.HandlerRegistry, r server.RouteRegistrar) {
	server.POST(hr, r, "/tokens", h.CreateToken)
}

// Peer simulator routes, relative to server.path.base. The module builds the
// relays' absolute partner URLs from these same constants, so a moved route
// moves the URL with it.
const (
	PeerSimulatorPath    = "/__sim/peer/tokens"
	MLEPeerSimulatorPath = "/__sim/peer/mle"
)

// RegisterSimulatorRoute attaches the peer simulator at PeerSimulatorPath.
// Hosted under the same registrar (so it lives under /api/v1) for routing
// simplicity; the path prefix makes the demo intent obvious. The "simulator"
// tag is required: cmd/api refuses startup when a /__sim/ route lacks it.
func (h *Handler) RegisterSimulatorRoute(hr *server.HandlerRegistry, r server.RouteRegistrar) {
	server.POST(hr, r, PeerSimulatorPath, h.PeerSimulate,
		server.WithTags("simulator"),
	)
}
