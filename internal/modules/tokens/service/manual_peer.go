package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/gaborage/go-bricks/app"
	"github.com/gaborage/go-bricks/jose"
)

// manualPeer is the core the two hand-opened simulators share: the Visa MLE
// (bare JWE) one and the VTS Issuer (JWS-of-JWE) one. It opens one compact under
// the inbound policy, tokenizes the PAN it carries, and seals the reply under
// the outbound policy. Neither mode can be selected by a `jose:` tag, so
// jose.Open/jose.Seal are the only doors to them; the two simulators differ only
// in how a compact reaches them and how the reply leaves.
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
// and never judges it, leaving freshness to the caller; neither simulator
// checks it.
func (p *manualPeer) process(ctx context.Context, compact string) (string, error) {
	plaintext, _, _, err := jose.Open(compact, p.inbound, p.resolver)
	if err != nil {
		return "", fmt.Errorf("open %s request: %w", p.label, err)
	}

	var req struct {
		PAN string `json:"pan"`
	}
	if err := json.Unmarshal(plaintext, &req); err != nil {
		return "", fmt.Errorf("decode %s request payload: %w", p.label, err)
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
