package service

import (
	"context"
	"crypto/rsa"
	"encoding/json"
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

// detachedScheme prefixes the detached JWS in the signature header.
const detachedScheme = "JWS "

// DetachedPeerSimulator plays the kid-less CALLER of the detached-signature
// gateway, inside this same process. The roles are the inverse of the three
// relays: there the demo is the client and the simulator the partner server;
// here the demo's real endpoint is the server and the simulator the partner.
//
// For each exchange it:
//  1. seals {"pan":...} as a bare JWE to the gateway's public key with no kid
//     (jose.Seal, SealModeBareJWE + OmitKid);
//  2. signs that exact JWE string with RS512 and the peer's private key, writing
//     no kid, and serializes the signature DETACHED (header..signature);
//  3. POSTs {"data":"<jwe>"} to the gateway over real loopback HTTP, with the
//     signature in the signature header as "JWS <detached>", so binding, the
//     body limit and the raw response all run as they would for a remote peer;
//  4. opens the reply's data member, a kid-less bare JWE sealed to the peer.
//
// Step 2 is the demo's one raw go-jose signing call outside a test. No
// go-bricks door mints this mode: SealModeJWSofJWEMinimal is open-only (an
// outbound minimal mode is an ADR-150 non-goal), jose.Seal refuses RS512 in
// every other mode, and the seal-payload CLI has no flag for it. The
// framework's own test fixture plays this peer with go-jose the same way.
//
// In production you would never own both halves of an integration; this pair
// only coexists because the simulator shares the demo's keystore.
type DetachedPeerSimulator struct {
	client          httpclient.Client
	gatewayURL      string
	signatureHeader string
	signKey         *rsa.PrivateKey
	requestPolicy   *jose.Policy
	replyPolicy     *jose.Policy
	resolver        jose.KeyResolver
}

// DetachedPeerConfig captures the inverse key identities the simulator plays
// with: it encrypts to the gateway's public key, signs with the peer's private
// key, and opens the reply with the peer's private key.
type DetachedPeerConfig struct {
	// GatewayURL is the absolute URL of the gateway route.
	GatewayURL string
	// KeyStore supplies our public key and the peer's private key.
	KeyStore app.KeyStore
	// EncryptKid is the gateway's public-key entry (inverse of its DecryptKid).
	EncryptKid string
	// SignKid is the peer private-key entry (inverse of the gateway's VerifyKid).
	SignKid string
	// DecryptKid is the peer private-key entry that opens the reply (inverse of
	// the gateway's ReplyKid).
	DecryptKid string
	// SignatureHeader names the request header that carries the detached JWS.
	SignatureHeader string
	// PeerName labels the simulator's outbound HTTP metrics.
	PeerName string
	// Logger receives the client's request telemetry; bodies are never logged.
	Logger logger.Logger
}

// DetachedExchange is what one round trip produced: the token, and the three
// ciphertext artefacts that crossed the wire, so a caller can decode their
// headers or replay and tamper with the request. None of them carries the PAN.
type DetachedExchange struct {
	Token *domain.Token
	Wire  DetachedWire
}

// DetachedWire holds the wire artefacts of one exchange, verbatim.
type DetachedWire struct {
	// SignatureHeader is the full signature header value, "JWS header..signature".
	SignatureHeader string `json:"signatureHeader"`
	// RequestBody is the request body, {"data":"<jwe>"}.
	RequestBody string `json:"requestBody"`
	// ReplyBody is the gateway's reply body, {"data":"<kid-less JWE>"}.
	ReplyBody string `json:"replyBody"`
}

// DetachedRefusal is the gateway answering a failure status. It carries only
// the status and the gateway's own reply code: the gateway's envelope has no
// message, and nothing else of the reply is surfaced.
type DetachedRefusal struct {
	Status int
	Code   string
}

func (r *DetachedRefusal) Error() string {
	return fmt.Sprintf("detached gateway refused the request: status %d, code %q", r.Status, r.Code)
}

// NewDetachedPeerSimulator resolves the signing key and validates both
// policies up front, failing fast rather than on the first exchange.
func NewDetachedPeerSimulator(cfg *DetachedPeerConfig) (*DetachedPeerSimulator, error) {
	if cfg == nil {
		return nil, errors.New("detached peer simulator requires a configuration")
	}
	if cfg.KeyStore == nil {
		return nil, errors.New("detached peer simulator requires a configured keystore")
	}
	if cfg.GatewayURL == "" {
		return nil, errors.New("detached peer simulator requires a gateway URL")
	}
	if cfg.SignatureHeader == "" {
		return nil, errors.New("detached peer simulator requires a signature header name")
	}

	signKey, err := cfg.KeyStore.PrivateKey(cfg.SignKid)
	if err != nil {
		return nil, fmt.Errorf("detached peer simulator signing key %q: %w", cfg.SignKid, err)
	}

	requestPolicy := NewDetachedReplyPolicy(cfg.EncryptKid) // the same kid-less bare seal, toward the gateway
	if err := requestPolicy.Validate(); err != nil {
		return nil, fmt.Errorf("detached peer request policy: %w", err)
	}
	replyPolicy := newDetachedReplyOpenPolicy(cfg.DecryptKid)
	if err := replyPolicy.Validate(); err != nil {
		return nil, fmt.Errorf("detached peer reply policy: %w", err)
	}

	// A plain client: WithJOSE has no detached-signature envelope, so the
	// simulator frames the request itself. No retries (the default), so one
	// exchange is one request.
	client, err := httpclient.NewBuilder(cfg.Logger).
		WithPeerName(cfg.PeerName).
		Build()
	if err != nil {
		return nil, fmt.Errorf("build detached peer client: %w", err)
	}

	return &DetachedPeerSimulator{
		client:          client,
		gatewayURL:      cfg.GatewayURL,
		signatureHeader: cfg.SignatureHeader,
		signKey:         signKey,
		requestPolicy:   requestPolicy,
		replyPolicy:     replyPolicy,
		resolver:        jose.NewKeyStoreResolver(cfg.KeyStore),
	}, nil
}

// newDetachedReplyOpenPolicy is how the peer opens the reply's data member: a
// bare JWE with no kid, A256GCM only.
func newDetachedReplyOpenPolicy(decryptKid string) *jose.Policy {
	return &jose.Policy{
		Direction:        jose.DirectionInbound,
		Mode:             jose.SealModeBareJWE,
		DecryptKid:       decryptKid,
		KeyAlg:           jose.DefaultKeyAlg,
		Enc:              jose.DefaultEnc,
		AcceptMissingKid: true,
	}
}

// Exchange runs one tokenization request through the gateway as the peer
// would. A failure status from the gateway is returned as *DetachedRefusal.
func (s *DetachedPeerSimulator) Exchange(ctx context.Context, pan string) (*DetachedExchange, error) {
	payload, err := json.Marshal(map[string]string{"pan": pan})
	if err != nil {
		return nil, fmt.Errorf("marshal detached request payload: %w", err)
	}

	jwe, err := jose.Seal(payload, s.requestPolicy, s.resolver)
	if err != nil {
		return nil, fmt.Errorf("seal detached request: %w", err)
	}
	detached, err := s.signDetached(jwe)
	if err != nil {
		return nil, err
	}

	body, err := json.Marshal(map[string]string{"data": jwe})
	if err != nil {
		return nil, fmt.Errorf("marshal detached request body: %w", err)
	}
	signatureHeader := detachedScheme + detached

	resp, err := s.client.Post(ctx, &httpclient.Request{
		URL: s.gatewayURL,
		Headers: map[string]string{
			"Content-Type":    "application/json",
			s.signatureHeader: signatureHeader,
		},
		Body: body,
	})
	if resp == nil {
		return nil, fmt.Errorf("detached gateway call failed: %w", err)
	}
	if resp.StatusCode != nethttp.StatusOK {
		return nil, gatewayRefusal(resp)
	}

	token, err := s.openReply(resp.Body)
	if err != nil {
		return nil, err
	}
	return &DetachedExchange{
		Token: token,
		Wire: DetachedWire{
			SignatureHeader: signatureHeader,
			RequestBody:     string(body),
			ReplyBody:       string(resp.Body),
		},
	}, nil
}

// signDetached signs the exact JWE compact string with RS512 and returns the
// detached serialization, header..signature. NewSigner with nil options writes
// a protected header of {"alg":"RS512"} alone: no kid, no cty.
func (s *DetachedPeerSimulator) signDetached(jwe string) (string, error) {
	signer, err := josev4.NewSigner(josev4.SigningKey{Algorithm: josev4.RS512, Key: s.signKey}, nil)
	if err != nil {
		return "", fmt.Errorf("build RS512 signer: %w", err)
	}
	signed, err := signer.Sign([]byte(jwe))
	if err != nil {
		return "", fmt.Errorf("sign detached request: %w", err)
	}
	detached, err := signed.DetachedCompactSerialize()
	if err != nil {
		return "", fmt.Errorf("serialize detached signature: %w", err)
	}
	return detached, nil
}

// openReply opens the reply's data member and decodes the token it carries.
func (s *DetachedPeerSimulator) openReply(body []byte) (*domain.Token, error) {
	var reply struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(body, &reply); err != nil {
		return nil, fmt.Errorf("decode detached gateway reply: %w", err)
	}
	if reply.Data == "" {
		return nil, errors.New("detached gateway reply carries no data")
	}

	plaintext, _, _, err := jose.Open(reply.Data, s.replyPolicy, s.resolver)
	if err != nil {
		return nil, fmt.Errorf("open detached gateway reply: %w", err)
	}
	var unsealed struct {
		Token *domain.Token `json:"token"`
	}
	if err := json.Unmarshal(plaintext, &unsealed); err != nil {
		return nil, fmt.Errorf("decode detached gateway reply payload: %w", err)
	}
	if unsealed.Token == nil {
		return nil, errors.New("detached gateway reply missing token")
	}
	return unsealed.Token, nil
}

// gatewayRefusal reads the gateway's {"code"} envelope off a failure reply.
func gatewayRefusal(resp *httpclient.Response) *DetachedRefusal {
	var envelope struct {
		Code string `json:"code"`
	}
	// A reply that is not the gateway's envelope (the framework's own binder
	// error, say) leaves Code empty; the status still says what happened.
	_ = json.Unmarshal(resp.Body, &envelope)
	return &DetachedRefusal{Status: resp.StatusCode, Code: envelope.Code}
}
