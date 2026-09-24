package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	nethttp "net/http"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/tokens/domain"
	"github.com/gaborage/go-bricks/app"
	"github.com/gaborage/go-bricks/httpclient"
	"github.com/gaborage/go-bricks/jose"
	"github.com/gaborage/go-bricks/logger"
)

// RelayService demonstrates outbound JOSE wrapping via the framework's
// httpclient.JOSETransport. It accepts a plaintext PAN, seals the request,
// POSTs to a partner URL (here: the in-process simulator), unwraps the
// JOSE-sealed response, and returns the resulting Token.
//
// Architectural note: every outbound retry produces a freshly-sealed payload —
// the JOSETransport sits below the httpclient retry loop so iat/jti claims are
// regenerated per attempt. This matches what protocols like Visa Token Services
// require and is one of the reasons the framework owns this layer.
type RelayService struct {
	client httpclient.Client
	url    string
	logger logger.Logger
}

// RelayConfig captures the pieces RelayService needs to be built.
type RelayConfig struct {
	// PartnerURL is the absolute URL of the JOSE-protected partner endpoint.
	PartnerURL string
	// KeyStore supplies our signing key and the peer's public key for encryption.
	KeyStore app.KeyStore
	// SignKid is our private-key kid (used to sign outbound JWS).
	SignKid string
	// EncryptKid is the peer public-key kid (used to encrypt the outer JWE).
	EncryptKid string
	// VerifyKid is the peer public-key kid (used to verify response JWS).
	VerifyKid string
	// DecryptKid is our private-key kid (used to decrypt response JWE).
	DecryptKid string
	// PeerName is the low-cardinality logical name of the partner. It labels the
	// client's outbound metrics and names the partner in a refused plaintext 2xx
	// (httpclient.ErrJOSEPlaintextResponse and its WARN, go-bricks v0.65.0, #1648),
	// so an operator running several JOSE integrations can tell which one regressed.
	PeerName string
	// Logger receives request/response telemetry.
	Logger logger.Logger
}

// NewRelayService wires an httpclient with WithJOSE around the given config.
// It fails fast if the keystore is missing — a relay client without keys
// cannot make a single successful call.
func NewRelayService(cfg *RelayConfig) (*RelayService, error) {
	if cfg == nil {
		return nil, errors.New("relay service requires a configuration")
	}
	if cfg.KeyStore == nil {
		return nil, errors.New("relay service requires a configured keystore")
	}
	if cfg.PartnerURL == "" {
		return nil, errors.New("relay service requires a partner URL")
	}

	resolver := jose.NewKeyStoreResolver(cfg.KeyStore)
	outbound := &jose.Policy{
		Direction:  jose.DirectionOutbound,
		SignKid:    cfg.SignKid,
		EncryptKid: cfg.EncryptKid,
		SigAlg:     jose.DefaultSigAlg,
		KeyAlg:     jose.DefaultKeyAlg,
		Enc:        jose.DefaultEnc,
		Cty:        jose.DefaultCty,
	}
	inbound := &jose.Policy{
		Direction:  jose.DirectionInbound,
		DecryptKid: cfg.DecryptKid,
		VerifyKid:  cfg.VerifyKid,
		SigAlg:     jose.DefaultSigAlg,
		KeyAlg:     jose.DefaultKeyAlg,
		Enc:        jose.DefaultEnc,
		Cty:        jose.DefaultCty,
	}
	// As of go-bricks v0.57.0 Build validates both JOSE policies itself (and
	// rejects unsafe transport composition), so the client either comes back
	// fully wired or not at all — no separate policy pre-check needed here.
	//
	// Inbound is set, so a 2xx the partner answers in plaintext is refused as
	// httpclient.ErrJOSEPlaintextResponse rather than handed back unauthenticated
	// (go-bricks v0.65.0, #1637). AllowPlaintextSuccess stays unset on purpose.
	client, err := httpclient.NewBuilder(cfg.Logger).
		WithPeerName(cfg.PeerName).
		WithJOSE(httpclient.JOSEConfig{
			Outbound: outbound,
			Inbound:  inbound,
			Resolver: resolver,
		}).
		Build()
	if err != nil {
		return nil, fmt.Errorf("build relay client: %w", err)
	}

	return &RelayService{client: client, url: cfg.PartnerURL, logger: cfg.Logger}, nil
}

// Relay seals a tokenization request to the partner URL and unwraps the response.
//
// The request goes out as a plaintext {"pan":...} body; the JOSETransport below
// the retry loop is what turns it into a compact JWE(JWS(...)) and opens the
// reply. Nothing here touches go-jose.
func (s *RelayService) Relay(ctx context.Context, pan string) (*domain.Token, error) {
	return postTokenizeRequest(ctx, s.client, s.url, pan)
}

// postTokenizeRequest is the wire half both relay services share: POST the
// plaintext {"pan":...} body and decode the plaintext the transport hands back.
// The two services differ only in how their client is wired — nested JWE-of-JWS
// versus bare-JWE behind Visa's encData envelope — so the HTTP call itself is
// written once.
//
// The transport names the Content-Type in both shapes (application/jose, or
// whatever the envelope declares), so no header is set here.
func postTokenizeRequest(ctx context.Context, client httpclient.Client, url, pan string) (*domain.Token, error) {
	body, err := json.Marshal(map[string]string{"pan": pan})
	if err != nil {
		return nil, fmt.Errorf("marshal relay body: %w", err)
	}

	resp, err := client.Post(ctx, &httpclient.Request{URL: url, Body: body})
	if err != nil {
		return nil, fmt.Errorf("partner call failed: %w", err)
	}
	if resp.StatusCode != nethttp.StatusOK {
		return nil, fmt.Errorf("partner returned status %d", resp.StatusCode)
	}

	// JOSE responses are sealed BEFORE the standard APIResponse envelope is
	// applied (see go-bricks/server/jose.go: json.Marshal(data) seals the raw
	// handler return value). So the decrypted body is the bare PeerSimResponse
	// shape — {"token": {...}} — not {"data": {"token": ...}, "meta": {...}}.
	var unsealed struct {
		Token *domain.Token `json:"token"`
	}
	if err := json.Unmarshal(resp.Body, &unsealed); err != nil {
		return nil, fmt.Errorf("decode relay response: %w", err)
	}
	if unsealed.Token == nil {
		return nil, errors.New("partner response missing token")
	}
	return unsealed.Token, nil
}
