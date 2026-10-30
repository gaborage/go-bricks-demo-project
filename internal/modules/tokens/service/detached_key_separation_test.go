package service

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/gaborage/go-bricks/jose"
	jositest "github.com/gaborage/go-bricks/jose/testing"
	josev4 "github.com/go-jose/go-jose/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests pin the key separation between the detached-signature gateway and
// the module's three other modes (nested JWE-of-JWS, Visa MLE bare JWE, VTS
// Issuer JWS-of-JWE), in both directions. The other modes share tokens.our and
// tokens.peer and are held apart only by algorithm and enc pins (see
// TestVTSIssuerInnerJWERefusedOnBareRoute). The gateway gets its own entries so
// that its inner JWE, which authenticates nobody once lifted out of the signed
// envelope, cannot be decrypted by any other route, and so that no other mode's
// body can be opened with its keys. Where a pin refuses a body first, a layered
// case removes the pin and shows the key alone still refuses it.

// Names of the module's other wire shapes, as the subtests call them.
const (
	shapeNested  = "nested JWE-of-JWS"
	shapeBareMLE = "bare MLE JWE"
	shapeVTS     = "VTS Issuer JWS-of-JWE"
)

// widenedBarePolicy is a bare inbound policy with every pin a body of this mode
// would trip removed: A256GCM admitted and a missing kid excused. Only the key
// behind decryptKid is left to refuse it.
func widenedBarePolicy(decryptKid string) *jose.Policy {
	p := NewMLEInboundPolicy(decryptKid)
	p.Enc = josev4.A256GCM
	p.Cty = ""
	p.AcceptMissingKid = true
	return p
}

// splitVTSIssuerBody turns a VTS Issuer JWS-of-JWE body into the detached
// envelope's two halves: the JWS with its payload emptied, and the payload (the
// inner JWE string) itself.
func splitVTSIssuerBody(t *testing.T, compact string) (detached, jwe string) {
	t.Helper()
	parts := strings.Split(compact, ".")
	require.Len(t, parts, 3)
	inner, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err)
	return parts[0] + ".." + parts[2], string(inner)
}

// TestDetachedGatewayRefusesOtherModesBodies: no body of another mode gets
// through the gateway, and none of them is ever decrypted with its key.
func TestDetachedGatewayRefusesOtherModesBodies(t *testing.T) {
	f := newDetachedFixture(t)
	ctx := context.Background()

	mleBody, err := jose.Seal([]byte(testPANPayload), NewMLEOutboundPolicy(testPeerKid), f.vts.mle.resolver)
	require.NoError(t, err)
	vtsDetached, vtsJWE := splitVTSIssuerBody(t, sealRequest(t, f.vts, NewVTSIssuerOutboundPolicy(testOurKid, testPeerKid)))

	// A five-segment body in the signature header never reaches Open.
	for name, body := range map[string]string{
		shapeNested:  nestedSeal(t, f.vts.mle),
		shapeBareMLE: mleBody,
	} {
		t.Run(name+" as the detached signature", func(t *testing.T) {
			_, err := f.gateway.Process(ctx, body, body)
			requireJOSECode(t, err, joseMalformed, 400)
		})
	}

	// The only other body that frames as a detached envelope is a VTS Issuer one
	// split in two. Its PS256 outer alg is refused before any key is touched.
	t.Run(shapeVTS+" split into the envelope", func(t *testing.T) {
		_, err := f.gateway.Process(ctx, vtsDetached, vtsJWE)
		requireJOSECode(t, err, joseAlgDisallowed, 400)
	})

	// Layered: RS512 itself, but signed by an existing mode's key.
	t.Run("RS512 signed by an existing mode's key", func(t *testing.T) {
		v := f.mint(t, testPANPayload)
		for _, kid := range []string{testOurKid, testPeerKid} {
			_, err := f.gateway.Process(ctx, signDetached(t, f.privateKey(t, kid), rs512Header, v.jwe), v.jwe)
			requireJOSECode(t, err, joseSigInvalid, 401)
		}
	})

	// Layered further: a correctly signed, kid-less JWE that was encrypted to an
	// existing mode's key passes every check up to decryption, and the gateway's
	// key cannot open it.
	t.Run("signed correctly but encrypted to an existing mode's key", func(t *testing.T) {
		for _, kid := range []string{testOurKid, testPeerKid} {
			jwe := mintJWE(t, &f.privateKey(t, kid).PublicKey, testPANPayload, josev4.A256GCM, "", nil)
			_, err := f.gateway.Process(ctx, signDetached(t, f.peerPriv, rs512Header, jwe), jwe)
			requireJOSECode(t, err, joseDecryptFailed, 401)
		}
	})
}

// TestDetachedBodiesRefusedOnExistingRoutes: nothing the gateway's peer sends,
// and nothing the gateway answers, opens on another mode's route.
func TestDetachedBodiesRefusedOnExistingRoutes(t *testing.T) {
	f := newDetachedFixture(t)
	ctx := context.Background()
	v := f.mint(t, testPANPayload)
	reply, err := f.gateway.Process(ctx, v.detached, v.jwe)
	require.NoError(t, err)

	t.Run("attached request on the VTS Issuer simulator", func(t *testing.T) {
		// RS512 is off the VTS Issuer policy's PS256 pin, checked before any key.
		_, err := f.vts.vts.Process(ctx, v.attached)
		requireJOSECode(t, err, joseAlgDisallowed, 400)
	})

	// The two jose:-tagged nested routes are covered in the handlers package,
	// which reads their policies from the real struct tags
	// (TestDetachedBodiesRefusedOnTaggedRoutes).

	// The inner JWE and the reply data are bare JWEs, the shape the MLE
	// simulator opens without authenticating anyone.
	for name, jwe := range map[string]string{"request JWE": v.jwe, "reply data": reply} {
		t.Run(name+" on the MLE simulator", func(t *testing.T) {
			_, err := f.vts.mle.simulator.Process(ctx, jwe)
			require.ErrorIs(t, err, jose.ErrMalformed, "A256GCM is off the MLE policy's A128GCM pin")
		})

		// Layered: with the A128GCM pin and the kid requirement removed, the two
		// existing entries still cannot decrypt it. The key is all that is left.
		t.Run(name+" under a widened bare policy on the existing keys", func(t *testing.T) {
			for _, kid := range []string{testOurKid, testPeerKid} {
				plaintext, _, _, err := jose.Open(jwe, widenedBarePolicy(kid), f.vts.mle.resolver)
				requireJOSECode(t, err, joseDecryptFailed, 401)
				assert.Empty(t, plaintext)
			}
		})
	}

	// Control: the same widened policy on the gateway's own decrypt key opens the
	// inner JWE with no signature checked. That is why tokens.detached.our must
	// back no bare-JWE route.
	t.Run("control: the widened policy on the gateway's key opens it", func(t *testing.T) {
		plaintext, _, _, err := jose.Open(v.jwe, widenedBarePolicy(testDetachedOurKid), f.resolver)
		require.NoError(t, err)
		assert.JSONEq(t, testPANPayload, string(plaintext))
	})
}

// TestDetachedKeysCannotOpenOtherModesBodies: a holder of only the gateway's
// private keys cannot decrypt a body of any other mode, even when every kid and
// pin is satisfied by naming the detached key under the existing entry's name.
// Each body is the request a relay sends toward tokens.peer. The nested shape is
// covered against the real tagged routes in the handlers package.
func TestDetachedKeysCannotOpenOtherModesBodies(t *testing.T) {
	f := newDetachedFixture(t)
	ourPub := &f.privateKey(t, testOurKid).PublicKey

	mleBody, err := jose.Seal([]byte(testPANPayload), NewMLEOutboundPolicy(testPeerKid), f.vts.mle.resolver)
	require.NoError(t, err)

	bodies := []struct {
		name   string
		body   string
		policy *jose.Policy
	}{
		{shapeBareMLE, mleBody, NewMLEInboundPolicy(testPeerKid)},
		{shapeVTS, sealRequest(t, f.vts, NewVTSIssuerOutboundPolicy(testOurKid, testPeerKid)), NewVTSIssuerInboundPolicy(testPeerKid, testOurKid)},
	}
	detachedPrivs := map[string]*rsa.PrivateKey{testDetachedOurKid: f.ourPriv, testDetachedPeerKid: f.peerPriv}

	for _, b := range bodies {
		for entry, priv := range detachedPrivs {
			t.Run(b.name+" with "+entry, func(t *testing.T) {
				// tokens.peer resolves to the detached key; tokens.our keeps its real
				// public half, so a signed mode verifies and reaches decryption.
				resolver := jositest.NewTestResolver(map[string]any{testPeerKid: priv, testOurKid: ourPub})
				plaintext, _, _, err := jose.Open(b.body, b.policy, resolver)
				requireJOSECode(t, err, joseDecryptFailed, 401)
				assert.Empty(t, plaintext)
			})
		}
	}
}
