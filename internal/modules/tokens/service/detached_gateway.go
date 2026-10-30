package service

import (
	"context"
	"errors"

	"github.com/gaborage/go-bricks/app"
	"github.com/gaborage/go-bricks/jose"
	josev4 "github.com/go-jose/go-jose/v4"
)

// NewDetachedInboundPolicy returns the gateway's inbound policy for a kid-less
// peer that signs the JWS-of-JWE shape its own way (go-bricks ADR-150): an RS512
// outer signature, no kid on either token, no outer cty.
//
// SealModeJWSofJWEMinimal is open-only and verifies before it decrypts. Its
// signature table is exactly {RS512}, and RS512 stays refused in every other
// mode, so SigAlg must be set explicitly: Validate refuses an unset or any other
// value (JOSE_ALGORITHM_DISALLOWED). The inner JWE keeps every bare-mode check:
// RSA-OAEP-256 + A256GCM only. Cty stays empty, so the inner cty is not pinned.
//
// AcceptMissingKid excuses only a literally ABSENT kid member, on the outer JWS
// and the inner JWE alike. A kid that is present must still equal the entry
// name: a wrong kid, "kid": "" and "kid": null are each JOSE_KID_UNKNOWN. The
// two kids name resolver keys, not wire headers.
func NewDetachedInboundPolicy(decryptKid, verifyKid string) *jose.Policy {
	return &jose.Policy{
		Direction:        jose.DirectionInbound,
		Mode:             jose.SealModeJWSofJWEMinimal,
		DecryptKid:       decryptKid, // our private key: opens the request JWE
		VerifyKid:        verifyKid,  // peer public key: verifies the detached signature
		SigAlg:           josev4.RS512,
		KeyAlg:           jose.DefaultKeyAlg, // RSA-OAEP-256
		Enc:              jose.DefaultEnc,    // A256GCM; pins Open to exactly this
		AcceptMissingKid: true,
	}
}

// NewDetachedReplyPolicy returns the policy that seals the reply's data member:
// a bare JWE to the peer's public key with no kid (OmitKid writes neither the
// protected-header member nor a recipient key id) and no cty or typ. The
// minimal mode is open-only, so the reply carries no signature.
func NewDetachedReplyPolicy(encryptKid string) *jose.Policy {
	return &jose.Policy{
		Direction:  jose.DirectionOutbound,
		Mode:       jose.SealModeBareJWE,
		EncryptKid: encryptKid,
		KeyAlg:     jose.DefaultKeyAlg, // required: Validate refuses an empty KeyAlg
		Enc:        jose.DefaultEnc,    // required: Validate refuses an empty Enc
		OmitKid:    true,
	}
}

// DetachedGateway is the core of POST /tokens/detached, the endpoint a kid-less
// peer calls with a detached-signature envelope: the compact JWE in the JSON
// body's data member, and the RS512 JWS over that exact string in a request
// header with its payload segment emptied (RFC 7515 Appendix F).
//
// ADR-150 keeps this server side in application code: no jose: tag key, route
// option or middleware selects the minimal mode. The gateway therefore composes
// the framework's doors by hand: jose.AttachDetachedJWS rebuilds the attached
// compact, then the shared manualPeer core opens it (jose.Open), tokenizes the
// PAN and seals the reply (jose.Seal).
//
// REPLAY: the gateway keeps no replay ledger. A replayed request re-tokenizes
// the same PAN to the same token, and the reply is sealed to the peer's own key,
// so a replayer learns nothing it did not already hold. A production gateway
// that must refuse replays records SHA-256 of the JWE string, computed only
// AFTER jose.Open returned nil (go-jose's base64url decode is not strict, so two
// strings can decode to the same bytes until the signature has verified),
// through jose.ReplayRecorder.GetOrSet, in one ledger per tenant shared by every
// route of the profile, with a window at least the acceptance horizon.
type DetachedGateway struct {
	peer *manualPeer
}

// DetachedGatewayConfig names the keystore entries the gateway resolves.
type DetachedGatewayConfig struct {
	// KeyStore supplies our private key and the peer's public key.
	KeyStore app.KeyStore
	// DecryptKid is our private-key entry: it opens the request JWE.
	DecryptKid string
	// VerifyKid is the peer public-key entry: it verifies the detached signature.
	VerifyKid string
	// ReplyKid is the peer public-key entry the reply data is sealed to.
	ReplyKid string
}

// NewDetachedGateway validates both policies at construction, so a
// misconfiguration fails startup rather than the first request.
func NewDetachedGateway(cfg *DetachedGatewayConfig) (*DetachedGateway, error) {
	if cfg == nil {
		return nil, errors.New("detached gateway requires a configuration")
	}
	if cfg.KeyStore == nil {
		return nil, errors.New("detached gateway requires a configured keystore")
	}
	peer, err := newManualPeer("detached", cfg.KeyStore,
		NewDetachedInboundPolicy(cfg.DecryptKid, cfg.VerifyKid), NewDetachedReplyPolicy(cfg.ReplyKid))
	if err != nil {
		return nil, err
	}
	return &DetachedGateway{peer: peer}, nil
}

// Process rebuilds the attached compact from the detached signature (the header
// value, scheme already stripped) and the JWE string from the body, opens it,
// tokenizes the PAN it carries and returns the reply sealed as a kid-less bare
// JWE.
//
// Errors: a *jose.Error from AttachDetachedJWS or Open (its Code is the only
// wire-safe part; Cause is not), ErrInvalidPayload or ErrInvalidPAN for an
// opened plaintext that is not a valid {"pan"} request, or an internal failure.
// The jwe string is passed verbatim, never trimmed: its exact bytes are what the
// peer signed.
func (g *DetachedGateway) Process(ctx context.Context, detached, jwe string) (string, error) {
	compact, err := jose.AttachDetachedJWS(detached, jwe)
	if err != nil {
		return "", err
	}
	return g.peer.process(ctx, compact)
}
