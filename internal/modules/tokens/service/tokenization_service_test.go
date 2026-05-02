package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTokenizeHappyPath(t *testing.T) {
	svc := NewTokenizationService()
	svc.now = func() time.Time { return time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC) }

	tok, err := svc.Tokenize("4111111111111111")
	require.NoError(t, err)
	assert.Equal(t, "visa", tok.Network)
	assert.Equal(t, "1111", tok.Last4)
	assert.Equal(t, "************1111", tok.MaskedPAN)
	assert.True(t, len(tok.Token) > 4 && tok.Token[:4] == "tok_")
	assert.Equal(t, time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC), tok.ExpiresAt)
}

func TestTokenizeDeterministicForSamePAN(t *testing.T) {
	svc := NewTokenizationService()
	a, err := svc.Tokenize("4111111111111111")
	require.NoError(t, err)
	b, err := svc.Tokenize("4111111111111111")
	require.NoError(t, err)
	assert.Equal(t, a.Token, b.Token, "tokenization must be stable for the same PAN")
}

func TestTokenizeNetworkDetection(t *testing.T) {
	svc := NewTokenizationService()
	cases := map[string]string{
		"4111111111111111": "visa",
		"5555555555554444": "mastercard",
		"378282246310005":  "amex",
		"6011111111111117": "discover",
		"3000000000000004": "unknown",
	}
	for pan, want := range cases {
		got, err := svc.Tokenize(pan)
		require.NoError(t, err, "pan %s", pan)
		assert.Equal(t, want, got.Network, "pan %s", pan)
	}
}

func TestTokenizeRejectsInvalidInput(t *testing.T) {
	svc := NewTokenizationService()
	bad := []string{
		"",                     // empty
		"4111",                 // too short
		"41111111111111111111", // too long
		"4111-1111-1111-1111",  // non-digit
		"4111111111111112",     // Luhn-invalid
	}
	for _, pan := range bad {
		_, err := svc.Tokenize(pan)
		assert.ErrorIs(t, err, ErrInvalidPAN, "pan %q should be rejected", pan)
	}
}
