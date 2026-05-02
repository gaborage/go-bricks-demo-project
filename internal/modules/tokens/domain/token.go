// Package domain holds the tokens module's domain objects.
package domain

import "time"

// Token is the tokenization result returned to the caller (network-token style).
// Fields mirror what a Visa Token Services style API returns: an opaque token,
// the masked PAN for display, the detected card network, and an expiry.
type Token struct {
	Token     string    `json:"token"`
	MaskedPAN string    `json:"masked_pan"`
	Network   string    `json:"network"`
	Last4     string    `json:"last4"`
	ExpiresAt time.Time `json:"expires_at"`
}
