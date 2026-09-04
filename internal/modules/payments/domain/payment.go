// Package domain holds the payments module's event model.
//
// PaymentAuthorized is the sealed AMQP event (ADR-097): the framework's typed
// publish and consume doors read the `seal` tags below and engage sealing on
// their own — the call site never touches go-jose. On the wire the message is a
// JWE-of-JWS whose signature covers the whole document while exactly one
// declared Subject (Card) travels encrypted; OrderID, Amount and Currency stay
// clear so a broker operator can still route and inspect them.
package domain

// CardDetails is the sealed Subject of PaymentAuthorized: it is the only member
// that leaves this process encrypted.
//
// DEMO DATA ONLY. The demo publishes synthetic test PANs (4111...). Never put a
// real cardholder number through this path: sealing protects the payload in
// transit and at rest on the broker, it does not make the process PCI-compliant.
type CardDetails struct {
	// PAN is validated as a bare digit string, the same rule the tokens module
	// applies to its own PAN input. The tag is `number` (^[0-9]+$), NOT `numeric`
	// — the latter also accepts a leading +/- and a decimal point, so it would
	// admit "-41111111111111" and "4111.11111111111". min/max stay rune-length
	// checks on the string either way. The consumer re-validates after opening
	// the envelope, so a malformed Subject parks on the DLQ instead of reaching
	// code.
	PAN      string `json:"pan" validate:"required,number,min=13,max=19"`
	ExpMonth int    `json:"expMonth" validate:"required,gte=1,lte=12"`
	ExpYear  int    `json:"expYear" validate:"required,gte=2000,lte=2100"`
	Holder   string `json:"holder" validate:"required"`
}

// Last4 returns the final four digits of the PAN, or "" when the PAN is too
// short to have them. This is the ONLY card fragment any log line may carry —
// PCI hygiene applies to demo data too, so no caller ever logs CardDetails
// itself.
func (c CardDetails) Last4() string {
	if len(c.PAN) < 4 {
		return ""
	}
	return c.PAN[len(c.PAN)-4:]
}

// PaymentAuthorized is the event published when an authorization succeeds.
//
// The sentinel names two Logical kids, never key generations: rotation swaps
// keystore entries (payments-sign-v2, ...) and never edits this tag. Both sides
// of the integration share this struct and derive their own role from it — the
// producer signs with the payments-sign PRIVATE key and encrypts to the
// payments-encrypt PUBLIC key; the consumer verifies with the payments-sign
// PUBLIC key and decrypts with the payments-encrypt PRIVATE key. This demo is
// both, so its keystore provisions both halves of both families.
//
// Card carries seal:"subject" and so must always be present on the wire: no
// omitempty, no json:"-", no embedding. The framework refuses the declaration at
// startup otherwise.
type PaymentAuthorized struct {
	_        struct{}    `seal:"sign=payments-sign,encrypt=payments-encrypt"`
	OrderID  string      `json:"orderId" validate:"required"`
	Amount   int64       `json:"amount" validate:"required,gt=0"` // minor units (cents)
	Currency string      `json:"currency" validate:"required,len=3,alpha"`
	Card     CardDetails `json:"card" seal:"subject"`
}
