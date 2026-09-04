package domain

import (
	"reflect"
	"testing"

	"github.com/gaborage/go-bricks/jose/sealed"
	"github.com/gaborage/go-bricks/messaging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A malformed seal declaration is a RUNTIME startup failure — `go build` stays
// green and Declarations.Validate() rejects it when the app boots. This test
// runs the framework's own scanner over the event type so a tag mistake fails
// here instead of at `make run`.
func TestPaymentAuthorizedSealDeclarationScans(t *testing.T) {
	spec, err := sealed.ScanType(reflect.TypeOf(PaymentAuthorized{}))
	require.NoError(t, err)
	require.NotNil(t, spec, "PaymentAuthorized must carry a seal declaration the codec recognizes")

	assert.Equal(t, "payments-sign", spec.SignLogical)
	assert.Equal(t, "payments-encrypt", spec.EncryptLogical)
	assert.Equal(t, "Card", spec.SubjectField)
	assert.Equal(t, "card", spec.SubjectPath, "the subject's json name is the signed sp entry")
	assert.Equal(t, []string{"card"}, spec.SealedPaths())
}

// The typed publish and consume doors probe with this detector; a type that
// stopped answering yes would silently publish in plaintext.
func TestPaymentAuthorizedIsSealTagged(t *testing.T) {
	assert.True(t, messaging.IsSealTagged(reflect.TypeOf(PaymentAuthorized{})))
	assert.False(t, messaging.IsSealTagged(reflect.TypeOf(CardDetails{})),
		"the subject type itself carries no seal tags — the tags live on the event")
}
