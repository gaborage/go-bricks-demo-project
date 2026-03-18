package service

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"fmt"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/webhooks/domain"
	"github.com/gaborage/go-bricks/app"
)

const (
	algorithm = "RS256"
	keyName   = "webhook-signing"
)

// SigningService demonstrates the go-bricks KeyStore by signing and
// verifying payloads using named RSA key pairs loaded at startup.
type SigningService struct {
	keyStore app.KeyStore
}

// NewSigningService creates a new signing service backed by the given KeyStore.
func NewSigningService(ks app.KeyStore) *SigningService {
	return &SigningService{keyStore: ks}
}

// Sign produces an RSA-SHA256 signature for the given payload.
func (s *SigningService) Sign(payload string) (*domain.SignedPayload, error) {
	privKey, err := s.keyStore.PrivateKey(keyName)
	if err != nil {
		return nil, fmt.Errorf("failed to get private key %q: %w", keyName, err)
	}

	hash := sha256.Sum256([]byte(payload))
	sig, err := rsa.SignPKCS1v15(rand.Reader, privKey, crypto.SHA256, hash[:])
	if err != nil {
		return nil, fmt.Errorf("failed to sign payload: %w", err)
	}

	return &domain.SignedPayload{
		Payload:   payload,
		Signature: base64.StdEncoding.EncodeToString(sig),
		Algorithm: algorithm,
		KeyName:   keyName,
	}, nil
}

// Verify checks whether the base64-encoded signature is valid for the payload.
func (s *SigningService) Verify(payload, signatureB64 string) (bool, error) {
	pubKey, err := s.keyStore.PublicKey(keyName)
	if err != nil {
		return false, fmt.Errorf("failed to get public key %q: %w", keyName, err)
	}

	sig, err := base64.StdEncoding.DecodeString(signatureB64)
	if err != nil {
		return false, fmt.Errorf("invalid base64 signature: %w", err)
	}

	hash := sha256.Sum256([]byte(payload))
	err = rsa.VerifyPKCS1v15(pubKey, crypto.SHA256, hash[:], sig)
	if err != nil {
		return false, nil // invalid signature, not an error
	}

	return true, nil
}
