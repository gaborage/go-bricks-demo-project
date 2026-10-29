package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/gaborage/go-bricks/app"
	"github.com/gaborage/go-bricks/jose"
)

// ErrInvalidPayload signals that a compact opened cleanly but its plaintext is not
// the {"pan":"..."} JSON object the tokens routes expect. The detached-signature
// gateway answers it as a caller error (400 PAYLOAD_INVALID); the MLE and VTS
// Issuer simulators still treat it as their own failure (500), as they always did.
var ErrInvalidPayload = errors.New("invalid request payload")

// invalidPayloadError marks a decode failure as ErrInvalidPayload while keeping
// the decoder's error in the chain and its text unchanged, so the MLE and VTS
// simulators log exactly what they logged before the sentinel existed.
type invalidPayloadError struct{ err error }

func (e invalidPayloadError) Error() string   { return e.err.Error() }
func (e invalidPayloadError) Unwrap() []error { return []error{ErrInvalidPayload, e.err} }

// manualPeer is the core the hand-opened routes share: the Visa MLE (bare JWE)
// simulator, the VTS Issuer (JWS-of-JWE) simulator and the detached-signature
// gateway (minimal JWS-of-JWE). It opens one compact under the inbound policy,
// tokenizes the PAN it carries, and seals the reply under the outbound policy.
// None of these modes can be selected by a `jose:` tag, so jose.Open/jose.Seal
// are the only doors to them; the three differ only in how a compact reaches
// them and how the reply leaves.
type manualPeer struct {
	// label names the wire shape in errors ("MLE", "VTS issuer").
	label    string
	inbound  *jose.Policy
	outbound *jose.Policy
	resolver jose.KeyResolver
	tokenSvc *TokenizationService
}

// newManualPeer validates both policies up front. Seal/Open validate per call;
// validating here turns a misconfiguration into a startup failure, which is
// what the rest of the module does.
func newManualPeer(label string, ks app.KeyStore, inbound, outbound *jose.Policy) (*manualPeer, error) {
	if err := inbound.Validate(); err != nil {
		return nil, fmt.Errorf("%s peer inbound policy: %w", label, err)
	}
	if err := outbound.Validate(); err != nil {
		return nil, fmt.Errorf("%s peer outbound policy: %w", label, err)
	}
	return &manualPeer{
		label:    label,
		inbound:  inbound,
		outbound: outbound,
		resolver: jose.NewKeyStoreResolver(ks),
		tokenSvc: NewTokenizationService(),
	}, nil
}

// process opens, tokenizes and seals one compact. jose reports an inbound `iat`
// and never judges it, leaving freshness to the caller; none of the three
// callers checks it.
//
// A plaintext that is not JSON fails as ErrInvalidPayload; a PAN that fails
// format or Luhn validation fails as ErrInvalidPAN. Either error may carry
// decoder text drawn from the opened plaintext, so a caller that maps them to a
// reply must not log the error itself.
func (p *manualPeer) process(ctx context.Context, compact string) (string, error) {
	plaintext, _, _, err := jose.Open(compact, p.inbound, p.resolver)
	if err != nil {
		return "", fmt.Errorf("open %s request: %w", p.label, err)
	}

	var req struct {
		PAN string `json:"pan"`
	}
	if err := json.Unmarshal(plaintext, &req); err != nil {
		return "", fmt.Errorf("decode %s request payload: %w", p.label, invalidPayloadError{err})
	}

	tok, err := p.tokenSvc.Tokenize(ctx, req.PAN)
	if err != nil {
		return "", err
	}

	raw, err := json.Marshal(map[string]any{"token": tok})
	if err != nil {
		return "", fmt.Errorf("marshal %s response payload: %w", p.label, err)
	}

	sealed, err := jose.Seal(raw, p.outbound, p.resolver)
	if err != nil {
		return "", fmt.Errorf("seal %s response: %w", p.label, err)
	}
	return sealed, nil
}
