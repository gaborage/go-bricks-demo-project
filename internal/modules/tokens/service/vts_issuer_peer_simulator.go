package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	nethttp "net/http"
	"strings"

	"github.com/gaborage/go-bricks/app"
	"github.com/gaborage/go-bricks/jose"
	"github.com/gaborage/go-bricks/logger"
)

// maxVTSIssuerBodyBytes caps the request body the simulator reads. A sealed
// token request is about 2 KiB; the framework's own JOSE routes cap at 10 MiB,
// and a demo simulator can afford to be far tighter.
const maxVTSIssuerBodyBytes = 64 << 10

// VTSIssuerPeerSimulator stands in for a Visa Token Service Issuer counterparty
// inside this same process, so the demo can drive the JWS-of-JWE JOSETransport
// end to end.
//
// Like the MLE simulator it opens and seals MANUALLY through jose.Open/jose.Seal:
// the `jose:` tag grammar has no `mode` key (ADR-111), so the framework's route
// middleware cannot speak this shape.
//
// Unlike the other two simulators it is an http.RoundTripper, not a /__sim/
// route. The Issuer body is the compact JWS itself, as application/jose, in both
// directions. A go-bricks route that can carry the "simulator" route tag (the
// /__sim/ convention) is a typed route, and a typed route always JSON-encodes
// what it returns; the raw door (RouteRegistrar.Add) could answer
// application/jose but takes no route options, so it cannot be tagged. Rather than bend the wire shape
// to fit a JSON route, the simulator plugs into the relay client's base-transport
// slot (httpclient.Builder.WithTransport), below the framework's JOSETransport.
// That is the slot a production integration fills with its mTLS transport, so
// everything above it runs exactly as it would against Visa: seal, retry loop,
// peer-labelled metrics, verify-then-decrypt and the plaintext-2xx refusal. Only
// the dial is replaced.
//
// In production you would never own both halves of an integration; this pair
// only coexists because the simulator shares the demo's keystore.
type VTSIssuerPeerSimulator struct {
	peer   *manualPeer
	logger logger.Logger
}

var _ nethttp.RoundTripper = (*VTSIssuerPeerSimulator)(nil)

// VTSIssuerPeerConfig captures the inverse key identities the simulator plays
// with: it verifies OUR signature and decrypts with the PEER key on the way in,
// then encrypts to OUR key and signs with the PEER key on the way out.
type VTSIssuerPeerConfig struct {
	// KeyStore supplies the peer private key and our public key.
	KeyStore app.KeyStore
	// DecryptKid is the peer private-key kid (inverse of the relay's EncryptKid).
	DecryptKid string
	// VerifyKid is our public-key kid (inverse of the relay's SignKid).
	VerifyKid string
	// SignKid is the peer private-key kid (inverse of the relay's VerifyKid).
	SignKid string
	// EncryptKid is our public-key kid (inverse of the relay's DecryptKid).
	EncryptKid string
	// Logger receives one line per refused request: the JOSE code and status,
	// never a body.
	Logger logger.Logger
}

// NewVTSIssuerPeerSimulator builds the simulator, failing fast on a missing
// keystore or an invalid policy pair rather than surfacing the problem on the
// first request.
func NewVTSIssuerPeerSimulator(cfg *VTSIssuerPeerConfig) (*VTSIssuerPeerSimulator, error) {
	if cfg == nil {
		return nil, errors.New("VTS issuer peer simulator requires a configuration")
	}
	if cfg.KeyStore == nil {
		return nil, errors.New("VTS issuer peer simulator requires a configured keystore")
	}
	if cfg.Logger == nil {
		return nil, errors.New("VTS issuer peer simulator requires a logger")
	}

	peer, err := newManualPeer("VTS issuer", cfg.KeyStore,
		NewVTSIssuerInboundPolicy(cfg.DecryptKid, cfg.VerifyKid),
		NewVTSIssuerOutboundPolicy(cfg.SignKid, cfg.EncryptKid))
	if err != nil {
		return nil, err
	}
	return &VTSIssuerPeerSimulator{peer: peer, logger: cfg.Logger}, nil
}

// Process verifies and opens one compact JWS-of-JWE request body, tokenizes the
// PAN it carries, and returns the reply sealed the same way.
//
// Open verifies BEFORE it decrypts: a body whose outer layer is not a compact JWS
// (JOSE_OUTER_NOT_JWS), is signed with anything but PS256
// (JOSE_ALGORITHM_DISALLOWED), or fails verification (JOSE_SIGNATURE_INVALID)
// never reaches the private key. jose reports the inner millisecond iat and never
// judges it, so freshness would be this caller's policy. Like the MLE simulator,
// this one does not check it.
func (s *VTSIssuerPeerSimulator) Process(ctx context.Context, compact string) (string, error) {
	if compact == "" {
		return "", errors.New("VTS issuer request carries no body")
	}
	return s.peer.process(ctx, compact)
}

// peerError is the minimal {code, message} body the framework's JOSE routes
// answer before trust is established. The simulator mirrors it so a refusal looks
// the same here as it would from a jose:-tagged route.
type peerError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// RoundTrip answers one relay request the way the Issuer endpoint would, with no
// network hop. It makes the checks a jose:-tagged route makes before trust — a
// JOSE Content-Type (415), a bounded body (413), a non-empty one (400) — then
// hands the compact to Process and answers the sealed reply as application/jose.
//
// Every refusal is a failure status with a JSON body, which the relay's
// JOSETransport passes through untouched: only a 2xx must have been unwrapped. A
// refused open carries the jose.Error's code and generic message; its Cause never
// leaves the process, and no request or response body is ever logged.
//
// Per the RoundTripper contract the request body is always closed, the request is
// never modified, and an error means no response at all, so every HTTP outcome
// returns a nil error.
func (s *VTSIssuerPeerSimulator) RoundTrip(req *nethttp.Request) (*nethttp.Response, error) {
	if req.Body != nil {
		defer func() { _ = req.Body.Close() }()
	}
	if err := req.Context().Err(); err != nil {
		return nil, err
	}

	if req.Method != nethttp.MethodPost {
		return peerErrorResponse(req, nethttp.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Only POST is served"), nil
	}
	if !jose.IsContentType(req.Header.Get("Content-Type")) {
		return peerErrorResponse(req, nethttp.StatusUnsupportedMediaType, "JOSE_PLAINTEXT_REJECTED", "Request must be application/jose"), nil
	}

	compact, refusal := readVTSIssuerCompact(req)
	if refusal != nil {
		return refusal, nil
	}

	sealed, err := s.Process(req.Context(), compact)
	if err != nil {
		return s.processErrorResponse(req, err), nil
	}
	return peerResponse(req, nethttp.StatusOK, jose.ContentType, []byte(sealed)), nil
}

// readVTSIssuerCompact reads the bounded request body. It returns the trimmed
// compact, or the pre-trust refusal to answer instead.
func readVTSIssuerCompact(req *nethttp.Request) (string, *nethttp.Response) {
	if req.Body == nil {
		return "", peerErrorResponse(req, nethttp.StatusBadRequest, "JOSE_BODY_REQUIRED", "Request body required")
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, maxVTSIssuerBodyBytes+1))
	if err != nil {
		return "", peerErrorResponse(req, nethttp.StatusBadRequest, "JOSE_BODY_REQUIRED", "Failed to read request body")
	}
	if len(body) > maxVTSIssuerBodyBytes {
		return "", peerErrorResponse(req, nethttp.StatusRequestEntityTooLarge, "JOSE_BODY_TOO_LARGE", "Request body exceeds the simulator's size limit")
	}
	compact := strings.TrimSpace(string(body))
	if compact == "" {
		return "", peerErrorResponse(req, nethttp.StatusBadRequest, "JOSE_BODY_REQUIRED", "Request body required")
	}
	return compact, nil
}

// processErrorResponse maps a Process failure onto the pre-trust error body. A
// *jose.Error carries a wire-safe Code and generic Message; anything else is
// either an invalid PAN or the simulator's own fault.
func (s *VTSIssuerPeerSimulator) processErrorResponse(req *nethttp.Request, err error) *nethttp.Response {
	var jerr *jose.Error
	switch {
	case errors.As(err, &jerr):
		status := jerr.Status
		if status == 0 {
			status = nethttp.StatusInternalServerError
		}
		s.logger.Warn().Str("code", jerr.Code).Int("status", status).Msg("VTS issuer simulator refused a request body")
		return peerErrorResponse(req, status, jerr.Code, jerr.Message)
	case errors.Is(err, ErrInvalidPAN):
		return peerErrorResponse(req, nethttp.StatusBadRequest, "BAD_REQUEST", "invalid PAN")
	default:
		s.logger.Error().Err(err).Msg("VTS issuer peer simulator failed")
		return peerErrorResponse(req, nethttp.StatusInternalServerError, "INTERNAL_ERROR", "VTS issuer simulator failed")
	}
}

// peerErrorResponse builds a JSON {code, message} failure response.
func peerErrorResponse(req *nethttp.Request, status int, code, message string) *nethttp.Response {
	// Marshalling two strings cannot fail.
	body, _ := json.Marshal(peerError{Code: code, Message: message})
	return peerResponse(req, status, "application/json", body)
}

// peerResponse builds the *http.Response a network transport would have read
// off the wire.
func peerResponse(req *nethttp.Request, status int, contentType string, body []byte) *nethttp.Response {
	return &nethttp.Response{
		Status:        fmt.Sprintf("%d %s", status, nethttp.StatusText(status)),
		StatusCode:    status,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        nethttp.Header{"Content-Type": []string{contentType}},
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}
}
