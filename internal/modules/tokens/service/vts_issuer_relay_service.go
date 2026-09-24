package service

import (
	"context"
	"errors"
	"fmt"
	nethttp "net/http"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/tokens/domain"
	"github.com/gaborage/go-bricks/app"
	"github.com/gaborage/go-bricks/httpclient"
	"github.com/gaborage/go-bricks/jose"
	"github.com/gaborage/go-bricks/logger"
	josev4 "github.com/go-jose/go-jose/v4"
)

// VTSIssuerRelayService is the third wire shape of the tokens module: the Visa
// Token Service **Issuer** API's JWS(JWE(payload)). Where the nested demo signs
// and then encrypts, this shape encrypts first and signs the compact JWE, so the
// signature is the OUTER layer and is verified before anything is decrypted
// (go-bricks v0.65.0, #1610/#1623, ADR-111).
//
// On the wire the body is the compact JWS itself, as application/jose, in both
// directions: three segments whose payload is the five-segment inner JWE,
// verbatim. There is no JSON envelope, so unlike the MLE relay this client sets
// no httpclient Envelope.
//
// The demo's counterparty is not a /__sim/ route but a VTSIssuerPeerSimulator in
// the client's base-transport slot (see VTSIssuerRelayConfig.Transport, and the
// simulator's doc for why). The JOSE wiring is what a production client builds;
// only that slot differs.
type VTSIssuerRelayService struct {
	client httpclient.Client
	url    string
	logger logger.Logger
}

// VTSIssuerRelayConfig captures the pieces VTSIssuerRelayService needs. It names
// all four kids, like the nested relay: this mode signs, so both directions carry
// a signature and an encryption identity.
type VTSIssuerRelayConfig struct {
	// PartnerURL is the absolute URL of the VTS Issuer endpoint.
	PartnerURL string
	// KeyStore supplies our private key and the peer's public key.
	KeyStore app.KeyStore
	// SignKid is our private-key kid (signs the outer JWS).
	SignKid string
	// EncryptKid is the peer public-key kid (encrypts the inner JWE).
	EncryptKid string
	// VerifyKid is the peer public-key kid (verifies the reply's outer JWS).
	VerifyKid string
	// DecryptKid is our private-key kid (decrypts the reply's inner JWE).
	DecryptKid string
	// PeerName is the low-cardinality logical name of the partner; see
	// RelayConfig.PeerName.
	PeerName string
	// Transport is the base RoundTripper below the JOSE layer
	// (httpclient.Builder.WithTransport). Nil dials PartnerURL over the network.
	// The module passes the in-process VTSIssuerPeerSimulator; a production wiring
	// passes its mTLS transport here, and nothing else about the client changes.
	Transport nethttp.RoundTripper
	// Logger receives request/response telemetry.
	Logger logger.Logger
}

// vtsIssuerTyp is the inner JWE protected `typ` the VTS Issuer profile expects.
// The outer JWS `typ` is fixed to "JOSE" by the mode itself; Policy.Typ only
// reaches the inner layer.
const vtsIssuerTyp = "JOSE"

// NewVTSIssuerOutboundPolicy returns the outbound half of the VTS Issuer policy
// pair: encrypt to the peer, then sign the compact JWE with our key.
//
// SigAlg is PS256 on purpose, and explicit on purpose: Visa requires PS256, and
// the package default (jose.DefaultSigAlg) is RS256. Both are on the framework
// allowlist, so httpclient.Builder.Build would quietly fill in RS256 if this line
// were missing, and the first rejection would come from the partner at runtime.
//
// Cty is left unset: this mode writes no inner cty, and Seal drops one that the
// builder fills in by default.
func NewVTSIssuerOutboundPolicy(signKid, encryptKid string) *jose.Policy {
	return &jose.Policy{
		Direction:  jose.DirectionOutbound,
		Mode:       jose.SealModeJWSofJWE,
		SignKid:    signKid,
		EncryptKid: encryptKid,
		SigAlg:     josev4.PS256,
		KeyAlg:     jose.DefaultKeyAlg, // RSA-OAEP-256
		Enc:        josev4.A256GCM,     // the only content encryption this mode admits
		Typ:        vtsIssuerTyp,
		// Inner JWE iat in epoch MILLISECONDS; the outer JWS iat is always stamped in
		// SECONDS by the mode. Re-stamped on every retry, because JOSETransport sits
		// below the retry loop.
		IATMillis: true,
	}
}

// NewVTSIssuerInboundPolicy returns the inbound half: verify the outer JWS
// against the peer's key, then decrypt the inner JWE with ours.
//
// On the way in SigAlg is a pin, not a default: Open refuses any outer alg other
// than exactly PS256 (JOSE_ALGORITHM_DISALLOWED), stricter than the nested path's
// package-wide allowlist. Enc pins A256GCM the same way.
func NewVTSIssuerInboundPolicy(decryptKid, verifyKid string) *jose.Policy {
	return &jose.Policy{
		Direction:  jose.DirectionInbound,
		Mode:       jose.SealModeJWSofJWE,
		DecryptKid: decryptKid,
		VerifyKid:  verifyKid,
		SigAlg:     josev4.PS256,
		KeyAlg:     jose.DefaultKeyAlg,
		Enc:        josev4.A256GCM,
	}
}

// NewVTSIssuerRelayService wires an httpclient whose JOSE transport runs the
// JWS-of-JWE policy pair. Build validates both policies, so the client comes back
// fully wired or not at all.
//
// Key separation (ADR-111): an inner JWE lifted out of a signed body would decrypt
// on a bare-JWE route that shares its DecryptKid, where nothing authenticates the
// sender. This demo reuses tokens-our/tokens-peer across all three shapes, and
// what keeps that safe is the Enc pin on the bare policies: they accept A128GCM
// only, and this mode's inner JWE is A256GCM, so a lifted inner JWE fails to
// parse there. Production gives each mode its own kids.
func NewVTSIssuerRelayService(cfg *VTSIssuerRelayConfig) (*VTSIssuerRelayService, error) {
	if cfg == nil {
		return nil, errors.New("VTS issuer relay service requires a configuration")
	}
	if cfg.KeyStore == nil {
		return nil, errors.New("VTS issuer relay service requires a configured keystore")
	}
	if cfg.PartnerURL == "" {
		return nil, errors.New("VTS issuer relay service requires a partner URL")
	}

	// No Envelope: the compact JWS is the body and application/jose the
	// Content-Type both ways. Inbound is set, so a 2xx that is not application/jose
	// is refused as httpclient.ErrJOSEPlaintextResponse (go-bricks v0.65.0, #1637).
	builder := httpclient.NewBuilder(cfg.Logger).
		WithPeerName(cfg.PeerName).
		WithJOSE(httpclient.JOSEConfig{
			Outbound: NewVTSIssuerOutboundPolicy(cfg.SignKid, cfg.EncryptKid),
			Inbound:  NewVTSIssuerInboundPolicy(cfg.DecryptKid, cfg.VerifyKid),
			Resolver: jose.NewKeyStoreResolver(cfg.KeyStore),
		})
	// The base slot is innermost whatever the call order: JOSE wraps it, so the
	// transport below only ever sees sealed bodies.
	if cfg.Transport != nil {
		builder = builder.WithTransport(cfg.Transport)
	}
	client, err := builder.Build()
	if err != nil {
		return nil, fmt.Errorf("build VTS issuer relay client: %w", err)
	}

	return &VTSIssuerRelayService{client: client, url: cfg.PartnerURL, logger: cfg.Logger}, nil
}

// Relay encrypts-then-signs a tokenization request to the partner URL, then
// verifies-then-decrypts the reply. Same call site as the other two relays: the
// wire shape lives entirely in how the client was wired.
func (s *VTSIssuerRelayService) Relay(ctx context.Context, pan string) (*domain.Token, error) {
	return postTokenizeRequest(ctx, s.client, s.url, pan)
}
