package handlers

import (
	"context"
	"errors"
	nethttp "net/http"
	"strings"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/tokens/domain"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/tokens/service"
	"github.com/gaborage/go-bricks/jose"
	"github.com/gaborage/go-bricks/logger"
	"github.com/gaborage/go-bricks/server"
)

// Detached-signature gateway routes and framing, relative to server.path.base.
// The module builds the simulator's absolute gateway URL from DetachedGatewayPath,
// so a moved route moves the URL with it.
const (
	// DetachedGatewayPath is the real endpoint a kid-less peer calls. It is NOT a
	// simulator: no /__sim/ segment and no "simulator" tag.
	DetachedGatewayPath = "/tokens/detached"
	// DetachedPeerSimulatorPath is the in-process peer that calls the gateway.
	DetachedPeerSimulatorPath = "/__sim/peer/detached"
	// DetachedSignatureHeader carries "JWS <detached compact JWS>".
	DetachedSignatureHeader = "X-Peer-Signature"

	// maxSignatureHeaderBytes bounds the header value before any parse.
	maxSignatureHeaderBytes = 8 << 10
	detachedScheme          = "JWS "
)

// The gateway's own reply codes. They are this endpoint's vocabulary toward the
// peer, not go-bricks' APIResponse error codes.
const (
	codeSignatureHeaderInvalid = "SIGNATURE_HEADER_INVALID"
	codeDataMissing            = "DATA_MISSING"
	codeMalformed              = "MALFORMED"
	codeAlgorithmRejected      = "ALGORITHM_REJECTED"
	codeKeyUnknown             = "KEY_UNKNOWN"
	codeSignatureInvalid       = "SIGNATURE_INVALID"
	codeCtyRejected            = "CTY_REJECTED"
	codePayloadInvalid         = "PAYLOAD_INVALID"
	codeRequestRejected        = "REQUEST_REJECTED"
	codeInternal               = "INTERNAL"
)

// detachedConsumerCodes maps a jose refusal code into the gateway's vocabulary.
// A code not listed (JOSE_DECRYPT_FAILED, say) answers REQUEST_REJECTED with the
// status jose chose.
var detachedConsumerCodes = map[string]string{
	"JOSE_MALFORMED":            codeMalformed,
	"JOSE_OUTER_NOT_JWS":        codeMalformed,
	"JOSE_ALGORITHM_DISALLOWED": codeAlgorithmRejected,
	"JOSE_KID_UNKNOWN":          codeKeyUnknown,
	"JOSE_SIGNATURE_INVALID":    codeSignatureInvalid,
	"JOSE_CTY_REJECTED":         codeCtyRejected,
}

// DetachedOpener is the narrow interface the gateway route depends on: the
// detached signature (scheme stripped) and the body's JWE in, the sealed reply
// data out.
type DetachedOpener interface {
	Process(ctx context.Context, detached, jwe string) (string, error)
}

// DetachedPeer is the narrow interface the simulator route depends on.
type DetachedPeer interface {
	Exchange(ctx context.Context, pan string) (*service.DetachedExchange, error)
}

// DetachedRequest is the gateway's body: the compact JWE in a JSON data member.
// It carries no validate tag on purpose: an empty data member is refused by the
// handler, so the reply keeps the gateway's own envelope instead of the
// framework's validation error.
type DetachedRequest struct {
	Data string `json:"data"`
}

// DetachedReply is the gateway's envelope in both outcomes: {"data":"<JWE>"} on
// success, {"code":"<CODE>"} on a refusal. Only data is ever sealed, and only on
// the success path, so every refusal stays plaintext.
type DetachedReply struct {
	Code string `json:"code,omitempty"`
	Data string `json:"data,omitempty"`
}

// DetachedSimRequest is the simulator's plaintext entry point. Same validation
// floor as the relays; the Luhn check is the gateway's (PAYLOAD_INVALID).
type DetachedSimRequest struct {
	PAN string `json:"pan" validate:"required,number,min=13,max=19"`
}

// RedactedForLog masks the PAN the same way TokenizeRequest does.
func (r DetachedSimRequest) RedactedForLog() any { return panLogView(r.PAN) }

// DetachedSimResponse returns the token and the ciphertext that crossed the
// wire, so the demo script can decode the headers and replay the request. It
// never carries the PAN: the token holds only the masked form and last four.
type DetachedSimResponse struct {
	Token *domain.Token        `json:"token"`
	Wire  service.DetachedWire `json:"wire"`
}

// DetachedHandler serves the detached-signature gateway and its peer simulator.
type DetachedHandler struct {
	gateway DetachedOpener
	peer    DetachedPeer
	log     logger.Logger
}

// NewDetachedHandler wires the gateway and the simulator into the HTTP layer.
func NewDetachedHandler(gateway DetachedOpener, peer DetachedPeer, l logger.Logger) *DetachedHandler {
	return &DetachedHandler{gateway: gateway, peer: peer, log: l}
}

// Gateway handles POST /api/v1/tokens/detached, following the framework's
// imperative recipe (wiki/jose.md, "Serving a detached-signature envelope
// peer") minus its tenant lookup: this demo runs single-tenant, so one static
// keystore resolver serves every request.
//
// HTTP framing is the handler's job: jose.AttachDetachedJWS only rebuilds the
// compact. The header is read with Header.Values because Header.Get cannot see
// a duplicate, and its value is bounded before anything parses it.
func (h *DetachedHandler) Gateway(req DetachedRequest, ctx server.HandlerContext) (server.Result[DetachedReply], server.IAPIError) {
	detached, status, ok := detachedSignature(ctx.Request().Header)
	if !ok {
		return h.refuse(status, codeSignatureHeaderInvalid), nil
	}
	// Refused here, not by a validate tag, so the gateway's envelope renders. This
	// also catches a body the binder skipped because its Content-Type was missing
	// or not JSON. Any application/json spelling and any */*+json type binds.
	if req.Data == "" {
		return h.refuse(nethttp.StatusBadRequest, codeDataMissing), nil
	}

	sealed, err := h.gateway.Process(ctx.RequestContext(), detached, req.Data)
	if err != nil {
		return h.fail(err), nil
	}
	return server.Result[DetachedReply]{Data: DetachedReply{Data: sealed}, Status: nethttp.StatusOK}, nil
}

// detachedSignature applies the header framing rules: exactly one value, at
// most maxSignatureHeaderBytes, carrying the "JWS " scheme. It returns the
// detached JWS with the scheme stripped, or the status to refuse with.
func detachedSignature(header nethttp.Header) (detached string, status int, ok bool) {
	values := header.Values(DetachedSignatureHeader)
	if len(values) != 1 { // missing or duplicated
		return "", nethttp.StatusUnauthorized, false
	}
	if len(values[0]) > maxSignatureHeaderBytes {
		return "", nethttp.StatusBadRequest, false
	}
	detached, ok = strings.CutPrefix(values[0], detachedScheme)
	if !ok {
		return "", nethttp.StatusUnauthorized, false
	}
	return detached, 0, true
}

// fail maps a Process error onto the gateway's envelope. For a jose refusal only
// its Code is logged: Cause and Error() can describe the input, and never reach
// a log or the wire. A payload refusal is not logged with its error at all,
// because decoder text can quote the opened plaintext.
func (h *DetachedHandler) fail(err error) server.Result[DetachedReply] {
	var jerr *jose.Error
	switch {
	case errors.As(err, &jerr):
		if jerr.Status == 0 || jerr.Status >= nethttp.StatusInternalServerError {
			// No status (policy and key-lookup failures) or a 5xx one
			// (JOSE_OUTBOUND_FAILED, JOSE_KEYSTORE_UNAVAILABLE, ...): the
			// gateway's own fault, never a refusal of the peer's request.
			h.log.Error().Str("jose_code", jerr.Code).Msg("detached envelope failed")
			return h.reply(nethttp.StatusInternalServerError, codeInternal)
		}
		code, ok := detachedConsumerCodes[jerr.Code]
		if !ok {
			code = codeRequestRejected
		}
		h.log.Warn().Str("jose_code", jerr.Code).Str("code", code).Int("status", jerr.Status).Msg("detached envelope refused")
		return h.reply(jerr.Status, code)
	case errors.Is(err, service.ErrInvalidPayload), errors.Is(err, service.ErrInvalidPAN):
		return h.refuse(nethttp.StatusBadRequest, codePayloadInvalid)
	default:
		h.log.Error().Err(err).Msg("detached envelope failed")
		return h.reply(nethttp.StatusInternalServerError, codeInternal)
	}
}

// refuse logs and answers a refusal the handler itself decided.
func (h *DetachedHandler) refuse(status int, code string) server.Result[DetachedReply] {
	h.log.Warn().Str("code", code).Int("status", status).Msg("detached envelope refused")
	return h.reply(status, code)
}

func (h *DetachedHandler) reply(status int, code string) server.Result[DetachedReply] {
	return server.Result[DetachedReply]{Data: DetachedReply{Code: code}, Status: status}
}

// PeerSimulate handles POST /api/v1/__sim/peer/detached: it runs one exchange
// with the gateway as the kid-less peer and returns the token plus the wire
// artefacts. A gateway refusal is answered with the gateway's code as the error
// code: 400 for PAYLOAD_INVALID (the PAN this caller sent failed the Luhn
// check), 502 otherwise.
func (h *DetachedHandler) PeerSimulate(req DetachedSimRequest, ctx server.HandlerContext) (*DetachedSimResponse, server.IAPIError) {
	exchange, err := h.peer.Exchange(ctx.RequestContext(), req.PAN)
	if err != nil {
		var refusal *service.DetachedRefusal
		if errors.As(err, &refusal) {
			h.log.Warn().Str("code", refusal.Code).Int("status", refusal.Status).Msg("detached gateway refused the simulator's request")
			status := nethttp.StatusBadGateway
			if refusal.Code == codePayloadInvalid {
				status = nethttp.StatusBadRequest
			}
			code := refusal.Code
			if code == "" {
				code = "GATEWAY_REFUSED"
			}
			return nil, server.NewBaseAPIError(code, "detached gateway refused the request", status)
		}
		// A *jose.Error renders its Cause in Error(), so a jose failure is logged by
		// its code alone, as on the gateway.
		var jerr *jose.Error
		if errors.As(err, &jerr) {
			h.log.Error().Str("jose_code", jerr.Code).Msg("detached peer simulator failed")
		} else {
			h.log.Error().Err(err).Msg("detached peer simulator failed")
		}
		return nil, server.NewInternalServerError("detached simulator failed")
	}
	return &DetachedSimResponse{Token: exchange.Token, Wire: exchange.Wire}, nil
}

// RegisterRoutes attaches the gateway and its peer simulator.
//
// The gateway is a typed route on an UNTAGGED path registered WithRawResponse:
// the peer expects its own {"data"}/{"code"} envelope, not go-bricks'
// APIResponse. It cannot be a jose:-tagged route: the tag grammar has no mode
// key, the tagged inbound gate requires Content-Type application/jose, and a
// jose: tag combined with WithRawResponse panics at registration (ADR-150).
// Without the tag, the framework's 10 MiB JOSE read cap does not apply either:
// server.bodylimit alone bounds the body.
//
// The simulator sits under /__sim/ and carries the "simulator" tag, which
// cmd/api's route policy requires in both directions.
func (h *DetachedHandler) RegisterRoutes(hr *server.HandlerRegistry, r server.RouteRegistrar) {
	server.POST(hr, r, DetachedGatewayPath, h.Gateway, server.WithRawResponse())
	server.POST(hr, r, DetachedPeerSimulatorPath, h.PeerSimulate,
		server.WithTags("simulator"),
	)
}
