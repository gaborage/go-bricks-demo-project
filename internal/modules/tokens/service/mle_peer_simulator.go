package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/gaborage/go-bricks/app"
	"github.com/gaborage/go-bricks/jose"
)

// MLEPeerSimulator stands in for a Visa MLE counterparty inside this same
// process, so the demo can drive a real outbound JOSETransport end to end.
//
// It opens and seals MANUALLY, unlike the nested JWE-of-JWS simulator next door
// which gets both directions from `jose:` struct tags. There is no `mode` key in
// the tag grammar (ADR-107), so a server inbound route cannot select bare mode —
// jose.Open/jose.Seal are the only doors to it. That is a framework constraint,
// not a stylistic choice.
//
// In production you would never own both halves of an integration; this pair
// only coexists because the simulator shares the demo's keystore.
type MLEPeerSimulator struct {
	inbound  *jose.Policy
	outbound *jose.Policy
	resolver jose.KeyResolver
	tokenSvc *TokenizationService
}

// MLEPeerConfig captures the inverse key identities the simulator plays with:
// it decrypts with the PEER private key and encrypts back to OUR public key.
type MLEPeerConfig struct {
	// KeyStore supplies the peer private key and our public key.
	KeyStore app.KeyStore
	// DecryptKid is the peer private-key kid (inverse of the relay's EncryptKid).
	DecryptKid string
	// EncryptKid is our public-key kid (inverse of the relay's DecryptKid).
	EncryptKid string
}

// NewMLEPeerSimulator builds the simulator, failing fast on a missing keystore
// or an invalid policy pair rather than surfacing the problem on first request.
func NewMLEPeerSimulator(cfg *MLEPeerConfig) (*MLEPeerSimulator, error) {
	if cfg == nil {
		return nil, errors.New("MLE peer simulator requires a configuration")
	}
	if cfg.KeyStore == nil {
		return nil, errors.New("MLE peer simulator requires a configured keystore")
	}

	inbound := NewMLEInboundPolicy(cfg.DecryptKid)
	outbound := NewMLEOutboundPolicy(cfg.EncryptKid)
	// Seal/Open validate per call; validating here turns a misconfiguration into
	// a startup failure, which is what the rest of the module does.
	if err := inbound.Validate(); err != nil {
		return nil, fmt.Errorf("MLE peer inbound policy: %w", err)
	}
	if err := outbound.Validate(); err != nil {
		return nil, fmt.Errorf("MLE peer outbound policy: %w", err)
	}

	return &MLEPeerSimulator{
		inbound:  inbound,
		outbound: outbound,
		resolver: jose.NewKeyStoreResolver(cfg.KeyStore),
		tokenSvc: NewTokenizationService(),
	}, nil
}

// Process opens one compact JWE lifted out of an {"encData":...} envelope,
// tokenizes the PAN it carries, and returns the reply as a fresh compact JWE for
// the caller to put back into an envelope.
//
// SECURITY: a successful open here proves only that the payload was encrypted to
// the peer's public key — bare mode carries no signature, so the sender is
// unauthenticated. A real MLE endpoint authenticates it at the transport (mTLS)
// or with X-Pay-Token before trusting anything below.
func (s *MLEPeerSimulator) Process(ctx context.Context, encData string) (string, error) {
	if encData == "" {
		return "", errors.New("MLE envelope carries no encData")
	}

	plaintext, _, _, err := jose.Open(encData, s.inbound, s.resolver)
	if err != nil {
		return "", fmt.Errorf("open MLE request: %w", err)
	}

	var req struct {
		PAN string `json:"pan"`
	}
	if err := json.Unmarshal(plaintext, &req); err != nil {
		return "", fmt.Errorf("decode MLE request payload: %w", err)
	}

	tok, err := s.tokenSvc.Tokenize(ctx, req.PAN)
	if err != nil {
		return "", err
	}

	raw, err := json.Marshal(map[string]any{"token": tok})
	if err != nil {
		return "", fmt.Errorf("marshal MLE response payload: %w", err)
	}

	compact, err := jose.Seal(raw, s.outbound, s.resolver)
	if err != nil {
		return "", fmt.Errorf("seal MLE response: %w", err)
	}
	return compact, nil
}
