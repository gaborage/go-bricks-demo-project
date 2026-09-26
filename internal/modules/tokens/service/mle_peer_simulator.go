package service

import (
	"context"
	"errors"

	"github.com/gaborage/go-bricks/app"
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
	peer *manualPeer
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

	peer, err := newManualPeer("MLE", cfg.KeyStore,
		NewMLEInboundPolicy(cfg.DecryptKid), NewMLEOutboundPolicy(cfg.EncryptKid))
	if err != nil {
		return nil, err
	}
	return &MLEPeerSimulator{peer: peer}, nil
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
	return s.peer.process(ctx, encData)
}
