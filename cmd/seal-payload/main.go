// seal-payload is a developer tool that produces the compact JOSE serialization
// required by the JOSE-protected demo endpoints (e.g., POST /api/v1/tokens).
//
// It plays the role of the *peer* (Visa, in production): it signs the supplied
// JSON with the peer's private key (tokens-peer) and encrypts the resulting JWS
// to our public key (tokens-our). The output is a compact JWE-of-JWS string
// suitable for `curl --data-binary @- ... -H 'Content-Type: application/jose'`.
//
// Usage:
//
//	echo '{"pan":"4111111111111111"}' | go run ./cmd/seal-payload
//	echo '{"pan":"4111111111111111"}' | go run ./cmd/seal-payload | curl ...
//
// Flags:
//
//	--our-pub    path to our public key DER         (default certs/tokens_our_public.der)
//	--peer-priv  path to the peer private key DER   (default certs/tokens_peer_private.der)
//
// The defaults match `make generate-keys`. In a real integration the peer would
// own its private key and you would only have the peer's public key — this
// helper inverts that relationship purely so a developer can drive the demo
// from one machine.
package main

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/gaborage/go-bricks/jose"
)

const (
	defaultOurPub   = "certs/tokens_our_public.der"
	defaultPeerPriv = "certs/tokens_peer_private.der"

	// Kids must match the kid names declared in the tokens module's jose: tags.
	// Centralized here so a kid rename in handlers/handlers.go shows up as a
	// runtime "JOSE_KID_UNKNOWN" in the demo until this constant is updated.
	ourKid  = "tokens-our"
	peerKid = "tokens-peer"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "seal-payload:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		ourPubPath   = flag.String("our-pub", defaultOurPub, "path to our public key (DER, PKIX)")
		peerPrivPath = flag.String("peer-priv", defaultPeerPriv, "path to the peer private key (DER, PKCS8 or PKCS1)")
	)
	flag.Parse()

	plaintext, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}
	if len(plaintext) == 0 {
		return errors.New("empty stdin: pipe a JSON payload, e.g. echo '{\"pan\":\"4111111111111111\"}' | seal-payload")
	}
	if !json.Valid(plaintext) {
		return errors.New("stdin is not valid JSON")
	}

	ourPub, err := loadPublicKey(*ourPubPath)
	if err != nil {
		return fmt.Errorf("load --our-pub: %w", err)
	}
	peerPriv, err := loadPrivateKey(*peerPrivPath)
	if err != nil {
		return fmt.Errorf("load --peer-priv: %w", err)
	}

	resolver := &literalResolver{
		publics:  map[string]*rsa.PublicKey{ourKid: ourPub},
		privates: map[string]*rsa.PrivateKey{peerKid: peerPriv},
	}

	policy := &jose.Policy{
		Direction:  jose.DirectionOutbound,
		SignKid:    peerKid,
		EncryptKid: ourKid,
		SigAlg:     jose.DefaultSigAlg,
		KeyAlg:     jose.DefaultKeyAlg,
		Enc:        jose.DefaultEnc,
		Cty:        jose.DefaultCty,
	}
	if err := policy.Validate(); err != nil {
		return fmt.Errorf("policy: %w", err)
	}

	compact, err := jose.Seal(plaintext, policy, resolver)
	if err != nil {
		return err
	}

	if _, err := os.Stdout.WriteString(compact); err != nil {
		return err
	}
	// Trailing newline so `curl --data-binary @-` doesn't trip when shells echo
	// the captured output, but compact JWE itself ends without one.
	fmt.Fprintln(os.Stdout)
	return nil
}

// literalResolver is a tiny in-memory KeyResolver that returns wrapped jose.Errors
// when a kid is missing — matching the contract of jose.KeyStoreResolver.
type literalResolver struct {
	publics  map[string]*rsa.PublicKey
	privates map[string]*rsa.PrivateKey
}

func (r *literalResolver) PublicKey(kid string) (*rsa.PublicKey, error) {
	pk, ok := r.publics[kid]
	if !ok {
		return nil, fmt.Errorf("public key %q not loaded", kid)
	}
	return pk, nil
}

func (r *literalResolver) PrivateKey(kid string) (*rsa.PrivateKey, error) {
	pk, ok := r.privates[kid]
	if !ok {
		return nil, fmt.Errorf("private key %q not loaded", kid)
	}
	return pk, nil
}

func loadPublicKey(path string) (*rsa.PublicKey, error) {
	der, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pub, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, fmt.Errorf("parse PKIX: %w", err)
	}
	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("expected *rsa.PublicKey, got %T", pub)
	}
	return rsaPub, nil
}

func loadPrivateKey(path string) (*rsa.PrivateKey, error) {
	der, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if k, err := x509.ParsePKCS8PrivateKey(der); err == nil {
		rsaKey, ok := k.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("PKCS8 parsed but not RSA (got %T)", k)
		}
		return rsaKey, nil
	}
	rsaKey, err := x509.ParsePKCS1PrivateKey(der)
	if err != nil {
		return nil, fmt.Errorf("neither PKCS8 nor PKCS1 parse succeeded: %w", err)
	}
	return rsaKey, nil
}
