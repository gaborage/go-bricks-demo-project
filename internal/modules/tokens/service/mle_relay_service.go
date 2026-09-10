package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/tokens/domain"
	"github.com/gaborage/go-bricks/app"
	"github.com/gaborage/go-bricks/httpclient"
	"github.com/gaborage/go-bricks/jose"
	"github.com/gaborage/go-bricks/logger"
	josev4 "github.com/go-jose/go-jose/v4"
)

// MLERelayService is the Visa **Message Level Encryption** counterpart to
// RelayService. Where the nested demo seals JWE(JWS(payload)), MLE puts a single
// compact JWE on the wire — no inner JWS — inside a JSON envelope:
// {"encData":"<compact>"} sent as application/json.
//
// SECURITY: bare-JWE mode authenticates NOTHING about the sender. A successful
// open proves only that the payload was encrypted to our public key, which any
// holder of that (public) key can do. Visa closes that gap out of band with mTLS
// and the X-Pay-Token header; a production wiring pairs this policy pair with
// httpclient's WithTransport(mTLS) rather than shipping it bare, as this demo
// does against an in-process simulator. See go-bricks ADR-107.
type MLERelayService struct {
	client httpclient.Client
	url    string
	logger logger.Logger
}

// MLERelayConfig captures the pieces MLERelayService needs to be built. Only two
// kids: bare mode signs nothing, so an outbound policy may name only an encrypt
// kid and an inbound policy only a decrypt kid.
type MLERelayConfig struct {
	// PartnerURL is the absolute URL of the MLE-protected partner endpoint.
	PartnerURL string
	// KeyStore supplies our private key and the peer's public key.
	KeyStore app.KeyStore
	// EncryptKid is the peer public-key kid — the only key identity the outbound
	// bare policy may declare.
	EncryptKid string
	// DecryptKid is our private-key kid — the only key identity the inbound bare
	// policy may declare.
	DecryptKid string
	// Logger receives request/response telemetry.
	Logger logger.Logger
}

// mleTyp is the JWE protected `typ` header Visa MLE expects.
const mleTyp = "JOSE"

// NewMLEOutboundPolicy returns the outbound half of the MLE policy pair: encrypt
// to the peer, stamp Visa's millisecond `iat`, and declare typ=JOSE. A128GCM is
// admitted only in bare mode (the nested floor stays A256GCM), and it has no
// go-bricks alias, so the go-jose constant is named directly.
func NewMLEOutboundPolicy(encryptKid string) *jose.Policy {
	return &jose.Policy{
		Direction:  jose.DirectionOutbound,
		Mode:       jose.SealModeBareJWE,
		EncryptKid: encryptKid,
		KeyAlg:     jose.DefaultKeyAlg, // RSA-OAEP-256
		Enc:        josev4.A128GCM,
		Cty:        jose.DefaultCty, // application/json
		Typ:        mleTyp,
		// IATMillis stamps `iat` in epoch MILLISECONDS at seal time — the Visa MLE
		// convention, not the seconds-based JWT claim of the same name. Because
		// JOSETransport sits below the retry loop, every retry attempt re-seals and
		// re-stamps it.
		IATMillis: true,
	}
}

// NewMLEInboundPolicy returns the inbound half: decrypt with our private key and
// nothing else. KeyAlg/Enc are read on the way IN too — they narrow what Open
// will accept to exactly the shape agreed with the peer, so an A256GCM token is
// refused here even though bare mode admits it.
func NewMLEInboundPolicy(decryptKid string) *jose.Policy {
	return &jose.Policy{
		Direction:  jose.DirectionInbound,
		Mode:       jose.SealModeBareJWE,
		DecryptKid: decryptKid,
		KeyAlg:     jose.DefaultKeyAlg,
		Enc:        josev4.A128GCM,
		Cty:        jose.DefaultCty,
	}
}

// NewMLERelayService wires an httpclient whose JOSE transport runs the bare-JWE
// policy pair behind Visa's {"encData":...} body envelope. Build validates both
// policies and the envelope pairing itself, so the client comes back fully wired
// or not at all.
func NewMLERelayService(cfg *MLERelayConfig) (*MLERelayService, error) {
	if cfg == nil {
		return nil, errors.New("MLE relay service requires a configuration")
	}
	if cfg.KeyStore == nil {
		return nil, errors.New("MLE relay service requires a configured keystore")
	}
	if cfg.PartnerURL == "" {
		return nil, errors.New("MLE relay service requires a partner URL")
	}

	// Bare mode authenticates nobody — production pairs this client with mTLS
	// (WithTransport) or X-Pay-Token; see the policy note above.
	client, err := httpclient.NewBuilder(cfg.Logger).
		WithJOSE(httpclient.JOSEConfig{
			Outbound: NewMLEOutboundPolicy(cfg.EncryptKid),
			Inbound:  NewMLEInboundPolicy(cfg.DecryptKid),
			Resolver: jose.NewKeyStoreResolver(cfg.KeyStore),
			// VisaMLEEnvelope wraps the outbound compact as
			// {"encData":"<compact>"} application/json, and recognizes the reply by
			// SHAPE rather than Content-Type — anything without a non-empty string
			// encData member (a plaintext error envelope, say) passes through.
			Envelope: httpclient.VisaMLEEnvelope(),
		}).
		Build()
	if err != nil {
		return nil, fmt.Errorf("build MLE relay client: %w", err)
	}

	return &MLERelayService{client: client, url: cfg.PartnerURL, logger: cfg.Logger}, nil
}

// Relay encrypts a tokenization request to the partner URL and decrypts the reply.
//
// Identical call site to the nested relay's: the difference lives entirely in
// how the client was wired, which is the point of the demo. After a successful
// unwrap the transport hands back the PLAINTEXT the peer sealed — {"token": ...}
// — with the encData envelope already stripped.
func (s *MLERelayService) Relay(ctx context.Context, pan string) (*domain.Token, error) {
	return postTokenizeRequest(ctx, s.client, s.url, pan)
}
