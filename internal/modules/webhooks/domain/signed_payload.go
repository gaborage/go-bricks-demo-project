package domain

// SignedPayload represents a payload signed with an RSA key.
type SignedPayload struct {
	Payload   string `json:"payload"`
	Signature string `json:"signature"` // base64-encoded
	Algorithm string `json:"algorithm"` // e.g. "RS256"
	KeyName   string `json:"keyName"`
}
