package service

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"testing"

	kstest "github.com/gaborage/go-bricks/keystore/testing"
)

func testKeyStore(t *testing.T) *kstest.MockKeyStore {
	t.Helper()
	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return kstest.NewMockKeyStore().
		WithPrivateKey("webhook-signing", privKey).
		WithPublicKey("webhook-signing", &privKey.PublicKey)
}

func TestSignAndVerify(t *testing.T) {
	ctx := context.Background()
	ks := testKeyStore(t)
	svc := NewSigningService(ks)

	payload := `{"event":"product.created","id":"123"}`

	signed, err := svc.Sign(ctx, payload)
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}

	if signed.Algorithm != "RS256" {
		t.Errorf("Algorithm = %q, want RS256", signed.Algorithm)
	}
	if signed.KeyName != "webhook-signing" {
		t.Errorf("KeyName = %q, want webhook-signing", signed.KeyName)
	}
	if signed.Payload != payload {
		t.Errorf("Payload mismatch")
	}

	valid, err := svc.Verify(ctx, signed.Payload, signed.Signature)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if !valid {
		t.Error("Verify() = false, want true")
	}
}

func TestVerifyInvalidSignature(t *testing.T) {
	ctx := context.Background()
	ks := testKeyStore(t)
	svc := NewSigningService(ks)

	valid, err := svc.Verify(ctx, "some payload", "aW52YWxpZA==") // "invalid" in base64
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if valid {
		t.Error("Verify() = true for invalid signature, want false")
	}
}

func TestVerifyMalformedBase64(t *testing.T) {
	ctx := context.Background()
	ks := testKeyStore(t)
	svc := NewSigningService(ks)

	_, err := svc.Verify(ctx, "payload", "!!!not-base64!!!")
	if err == nil {
		t.Fatal("Verify() expected error for malformed base64")
	}
	if !errors.Is(err, ErrMalformedSignature) {
		t.Errorf("Verify() error = %v, want ErrMalformedSignature", err)
	}
}

func TestSignMissingKey(t *testing.T) {
	ctx := context.Background()
	ks := kstest.NewMockKeyStore() // no keys registered
	svc := NewSigningService(ks)

	_, err := svc.Sign(ctx, "payload")
	if err == nil {
		t.Fatal("Sign() expected error for missing key")
	}
}

func TestVerifyMissingKey(t *testing.T) {
	ctx := context.Background()
	ks := kstest.NewMockKeyStore() // no keys registered
	svc := NewSigningService(ks)

	_, err := svc.Verify(ctx, "payload", "c2lnbmF0dXJl")
	if err == nil {
		t.Fatal("Verify() expected error for missing key")
	}
}
