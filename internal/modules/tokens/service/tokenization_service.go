// Package service contains the tokens module's business logic.
//
// TokenizationService is intentionally tiny: it deterministically derives a
// surrogate "network token" from a PAN so the demo's responses are stable for a
// given input. Real tokenization (VTS, MDES) is performed by the card network;
// the framework integration this module showcases is the JOSE pipeline that
// protects the request and response, not the tokenization algorithm itself.
package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"strings"
	"time"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/tokens/domain"
)

// ErrInvalidPAN signals that the supplied PAN failed format/Luhn validation.
var ErrInvalidPAN = errors.New("invalid PAN")

// pseudoTokenSecret is the HMAC key used to derive the demo surrogate token.
// Hardcoded because the demo is reproducible from public inputs only — there is
// no card data at risk. A real tokenizer never works this way.
//
//nolint:gosec // G101: demo-only HMAC key, not a credential.
const pseudoTokenSecret = "go-bricks-demo-tokenization-secret"

// tokenLifetime mirrors typical card-on-file token validity windows.
const tokenLifetime = 90 * 24 * time.Hour

// TokenizationService tokenizes a PAN into a stable demo token + display fields.
type TokenizationService struct {
	now func() time.Time
}

// NewTokenizationService returns a service using time.Now as the clock.
func NewTokenizationService() *TokenizationService {
	return &TokenizationService{now: time.Now}
}

// Tokenize validates the PAN and returns the demo surrogate token.
func (s *TokenizationService) Tokenize(pan string) (*domain.Token, error) {
	pan = strings.TrimSpace(pan)
	if !validPAN(pan) {
		return nil, ErrInvalidPAN
	}

	mac := hmac.New(sha256.New, []byte(pseudoTokenSecret))
	mac.Write([]byte(pan))
	suffix := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(mac.Sum(nil)))[:16]

	last4 := pan[len(pan)-4:]
	masked := strings.Repeat("*", len(pan)-4) + last4

	return &domain.Token{
		Token:     "tok_" + suffix,
		MaskedPAN: masked,
		Network:   detectNetwork(pan),
		Last4:     last4,
		ExpiresAt: s.now().Add(tokenLifetime).UTC(),
	}, nil
}

// validPAN checks digit-only, length 13–19, and a Luhn checksum.
func validPAN(pan string) bool {
	if len(pan) < 13 || len(pan) > 19 {
		return false
	}
	for _, r := range pan {
		if r < '0' || r > '9' {
			return false
		}
	}
	return luhnValid(pan)
}

func luhnValid(pan string) bool {
	sum := 0
	double := false
	for i := len(pan) - 1; i >= 0; i-- {
		d := int(pan[i] - '0')
		if double {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
		double = !double
	}
	return sum%10 == 0
}

// detectNetwork uses the IIN to label the network. Coverage is illustrative.
func detectNetwork(pan string) string {
	switch {
	case strings.HasPrefix(pan, "4"):
		return "visa"
	case len(pan) >= 2 && pan[0] == '5' && pan[1] >= '1' && pan[1] <= '5':
		return "mastercard"
	case strings.HasPrefix(pan, "34"), strings.HasPrefix(pan, "37"):
		return "amex"
	case strings.HasPrefix(pan, "6011"), strings.HasPrefix(pan, "65"):
		return "discover"
	default:
		return "unknown"
	}
}
