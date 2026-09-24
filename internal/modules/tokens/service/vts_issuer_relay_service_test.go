package service

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gaborage/go-bricks/httpclient"
	"github.com/gaborage/go-bricks/jose"
	"github.com/gaborage/go-bricks/logger"
	josev4 "github.com/go-jose/go-jose/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const vtsPeerName = "visa-vts-issuer-peer-sim"

// vtsFixture reuses the MLE fixture's keystore and resolver (both halves of
// tokens-our and tokens-peer) and adds the VTS Issuer simulator. Keeping the MLE
// simulator alongside is deliberate: the key-separation test needs both.
type vtsFixture struct {
	mle *mleFixture
	vts *VTSIssuerPeerSimulator
}

func newVTSFixture(t *testing.T) *vtsFixture {
	t.Helper()
	mle := newMLEFixture(t)
	return &vtsFixture{mle: mle, vts: newVTSPeer(t, mle, testOurKid)}
}

// newVTSPeer builds the simulator with the module's inverse identities, except
// for the kid it verifies the caller's signature with, which a test can vary.
func newVTSPeer(t *testing.T, mle *mleFixture, verifyKid string) *VTSIssuerPeerSimulator {
	t.Helper()
	sim, err := NewVTSIssuerPeerSimulator(&VTSIssuerPeerConfig{
		KeyStore:   mle.keystore,
		DecryptKid: testPeerKid,
		VerifyKid:  verifyKid,
		SignKid:    testPeerKid,
		EncryptKid: testOurKid,
		Logger:     logger.New("disabled", false),
	})
	require.NoError(t, err)
	return sim
}

// newVTSRelay builds the relay as module.go does, with the given base transport
// (nil dials partnerURL).
func newVTSRelay(t *testing.T, fx *vtsFixture, partnerURL string, transport nethttp.RoundTripper) *VTSIssuerRelayService {
	t.Helper()
	relay, err := NewVTSIssuerRelayService(&VTSIssuerRelayConfig{
		PartnerURL: partnerURL,
		KeyStore:   fx.mle.keystore,
		SignKid:    testOurKid,
		EncryptKid: testPeerKid,
		VerifyKid:  testPeerKid,
		DecryptKid: testOurKid,
		PeerName:   vtsPeerName,
		Transport:  transport,
		Logger:     logger.New("disabled", false),
	})
	require.NoError(t, err)
	return relay
}

// sealRequest seals a tokenization request the way the relay does (our
// signature over a JWE encrypted to the peer), with the policy handed in so a
// test can vary one field.
func sealRequest(t *testing.T, fx *vtsFixture, p *jose.Policy) string {
	t.Helper()
	return sealPayload(t, fx, p, `{"pan":"`+testPAN+`"}`)
}

// sealPayload seals an arbitrary request payload under p.
func sealPayload(t *testing.T, fx *vtsFixture, p *jose.Policy, payload string) string {
	t.Helper()
	compact, err := jose.Seal([]byte(payload), p, fx.mle.resolver)
	require.NoError(t, err)
	return compact
}

// wireRecorder sits between the JOSE layer and the simulator, the way a network
// capture would, and records what crossed the wire in each direction.
type wireRecorder struct {
	next nethttp.RoundTripper

	requestContentType  string
	requestBody         string
	responseContentType string
	responseStatus      int
}

func (w *wireRecorder) RoundTrip(req *nethttp.Request) (*nethttp.Response, error) {
	body, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	if err != nil {
		return nil, err
	}
	w.requestContentType = req.Header.Get("Content-Type")
	w.requestBody = string(body)

	forwarded := req.Clone(req.Context())
	forwarded.Body = io.NopCloser(strings.NewReader(w.requestBody))
	forwarded.ContentLength = int64(len(body))

	resp, err := w.next.RoundTrip(forwarded)
	if err != nil {
		return nil, err
	}
	w.responseContentType = resp.Header.Get("Content-Type")
	w.responseStatus = resp.StatusCode
	return resp, nil
}

// TestVTSIssuerRelayRoundTripsJWSofJWE asserts the full VTS Issuer shape:
//  1. The request goes out as application/jose, and the body is the compact
//     itself: a three-segment JWS, with no JSON envelope
//  2. The outer JWS is PS256, typ JOSE, cty JWE, signed with our kid, and reports
//     no millisecond iat (the outer iat is seconds)
//  3. The inner JWE is A256GCM, typ JOSE, encrypted to the peer kid, with no cty
//     and a millisecond iat
//  4. The peer's reply comes back as application/jose and is verified and
//     opened to plaintext
//
// It runs the module's wiring: the simulator is the relay's base transport, with
// a recorder between it and the JOSE layer standing in for a network capture.
func TestVTSIssuerRelayRoundTripsJWSofJWE(t *testing.T) {
	fx := newVTSFixture(t)
	wire := &wireRecorder{next: fx.vts}

	tok, err := newVTSRelay(t, fx, testPeerURL, wire).Relay(context.Background(), testPAN)
	require.NoError(t, err)
	assert.Equal(t, "visa", tok.Network)
	assert.Equal(t, "1111", tok.Last4)

	assert.Equal(t, jose.ContentType, wire.requestContentType)
	assert.Equal(t, nethttp.StatusOK, wire.responseStatus)
	assert.Equal(t, jose.ContentType, wire.responseContentType, "the reply is a bare compact too, not JSON")
	require.Equal(t, 2, strings.Count(wire.requestBody, "."), "the body is a compact JWS, not an envelope")

	// Open the captured request with the peer's own policy, as the simulator
	// does, so the assertions can see both headers.
	_, _, hdr, err := jose.Open(wire.requestBody, NewVTSIssuerInboundPolicy(testPeerKid, testOurKid), fx.mle.resolver)
	require.NoError(t, err)
	assert.Equal(t, "PS256", hdr.JWS.Alg, "Visa requires PS256; the package default is RS256")
	assert.Equal(t, "JWE", hdr.JWS.Cty)
	assert.Equal(t, vtsIssuerTyp, hdr.JWS.Typ)
	assert.Equal(t, testOurKid, hdr.JWS.Kid)
	assert.Zero(t, hdr.JWS.IATMillis, "the outer iat is seconds and is never reported as millis")
	assert.Equal(t, "A256GCM", hdr.JWE.Enc)
	assert.Equal(t, vtsIssuerTyp, hdr.JWE.Typ)
	assert.Equal(t, testPeerKid, hdr.JWE.Kid)
	assert.Empty(t, hdr.JWE.Cty, "the inner JWE carries no cty, even though Build fills Policy.Cty")
	assert.InDelta(t, time.Now().UnixMilli(), hdr.JWE.IATMillis, float64(time.Minute.Milliseconds()))
}

// TestVTSIssuerRelayDialsWithoutTransport is the production path: with no
// Transport the relay dials PartnerURL, and the same JOSE wiring round-trips
// against a real HTTP peer.
func TestVTSIssuerRelayDialsWithoutTransport(t *testing.T) {
	fx := newVTSFixture(t)

	peer := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		_ = r.Body.Close()

		reply, err := fx.vts.Process(r.Context(), string(body))
		require.NoError(t, err)
		w.Header().Set("Content-Type", jose.ContentType)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(peer.Close)

	tok, err := newVTSRelay(t, fx, peer.URL, nil).Relay(context.Background(), testPAN)
	require.NoError(t, err)
	assert.Equal(t, "1111", tok.Last4)
}

// TestVTSIssuerRelaySurfacesPeerRefusal: a peer that refuses the request answers
// a failure status with a plaintext {code, message} body. The JOSE layer passes
// that through (only a 2xx must have been unwrapped), and the relay fails
// without a token. Here the simulator expects the peer's own signature, so our
// kid is unknown to it.
func TestVTSIssuerRelaySurfacesPeerRefusal(t *testing.T) {
	fx := newVTSFixture(t)
	wire := &wireRecorder{next: newVTSPeer(t, fx.mle, testPeerKid)}

	tok, err := newVTSRelay(t, fx, testPeerURL, wire).Relay(context.Background(), testPAN)
	require.Error(t, err)
	assert.NotErrorIs(t, err, httpclient.ErrJOSEPlaintextResponse, "a failure status is not a plaintext-success violation")
	assert.Nil(t, tok)
	assert.Equal(t, nethttp.StatusUnauthorized, wire.responseStatus)
	assert.Equal(t, "application/json", wire.responseContentType)
}

// TestVTSIssuerOpenRefusesTamperedBody pins verify-before-decrypt: altering one
// character of the signed payload (the inner JWE) fails at the signature, so the
// altered ciphertext never reaches the peer's private key.
func TestVTSIssuerOpenRefusesTamperedBody(t *testing.T) {
	fx := newVTSFixture(t)

	parts := strings.Split(sealRequest(t, fx, NewVTSIssuerOutboundPolicy(testOurKid, testPeerKid)), ".")
	require.Len(t, parts, 3)
	payload := []byte(parts[1])
	mid := len(payload) / 2
	if payload[mid] == 'A' {
		payload[mid] = 'B'
	} else {
		payload[mid] = 'A'
	}
	parts[1] = string(payload)

	reply, err := fx.vts.Process(context.Background(), strings.Join(parts, "."))
	require.ErrorIs(t, err, jose.ErrSignatureInvalid)
	assert.Empty(t, reply)
}

// TestVTSIssuerOpenPinsPS256 pins the stricter-than-allowlist rule: RS256 is on
// the framework allowlist, but the inbound policy declared PS256, so Open refuses
// an RS256 body on its peeked header, before any key is resolved.
func TestVTSIssuerOpenPinsPS256(t *testing.T) {
	fx := newVTSFixture(t)

	rs256 := NewVTSIssuerOutboundPolicy(testOurKid, testPeerKid)
	rs256.SigAlg = josev4.RS256

	reply, err := fx.vts.Process(context.Background(), sealRequest(t, fx, rs256))
	require.ErrorIs(t, err, jose.ErrAlgorithmDisallowed)
	assert.Empty(t, reply)
}

// TestVTSIssuerOpenRefusesOtherShapes pins that a five-segment body is poison in
// this mode, never a fallback: both of the module's other shapes are refused with
// JOSE_OUTER_NOT_JWS.
func TestVTSIssuerOpenRefusesOtherShapes(t *testing.T) {
	fx := newVTSFixture(t)

	bare, err := jose.Seal([]byte(`{"pan":"`+testPAN+`"}`), NewMLEOutboundPolicy(testPeerKid), fx.mle.resolver)
	require.NoError(t, err)

	for name, compact := range map[string]string{
		"nested JWE-of-JWS": nestedSeal(t, fx.mle),
		"bare MLE JWE":      bare,
	} {
		t.Run(name, func(t *testing.T) {
			reply, err := fx.vts.Process(context.Background(), compact)
			require.ErrorIs(t, err, jose.ErrMalformed)
			var jerr *jose.Error
			require.True(t, errors.As(err, &jerr))
			assert.Equal(t, "JOSE_OUTER_NOT_JWS", jerr.Code)
			assert.Empty(t, reply)
		})
	}
}

// TestVTSIssuerInnerJWERefusedOnBareRoute pins the key-separation reasoning in
// NewVTSIssuerRelayService. The inner JWE of a VTS Issuer request is encrypted to
// tokens-peer, and so is every MLE request: the MLE simulator decrypts with the
// same kid and authenticates nobody. Lifted out of its signed wrapper and replayed
// there, that inner JWE is refused only because the bare policy pins A128GCM. The
// control case widens the pin and shows the lifted JWE would then open, which is
// why production gives each mode its own kids.
func TestVTSIssuerInnerJWERefusedOnBareRoute(t *testing.T) {
	fx := newVTSFixture(t)

	parts := strings.Split(sealRequest(t, fx, NewVTSIssuerOutboundPolicy(testOurKid, testPeerKid)), ".")
	require.Len(t, parts, 3)
	innerBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err)
	inner := string(innerBytes) // the JWS payload is the compact JWE, verbatim
	require.Equal(t, 4, strings.Count(inner, "."))

	reply, err := fx.mle.simulator.Process(context.Background(), inner)
	require.ErrorIs(t, err, jose.ErrMalformed, "A256GCM is off the MLE policy's A128GCM pin")
	assert.Empty(t, reply)

	widened := NewMLEInboundPolicy(testPeerKid)
	widened.Enc = josev4.A256GCM
	plaintext, _, _, err := jose.Open(inner, widened, fx.mle.resolver)
	require.NoError(t, err, "without the pin, the unsigned inner JWE decrypts on a bare route")
	assert.JSONEq(t, `{"pan":"`+testPAN+`"}`, string(plaintext))
}

// TestVTSIssuerPeerSimulatorRejectsEmptyBody guards the boundary check on the
// simulator's only input.
func TestVTSIssuerPeerSimulatorRejectsEmptyBody(t *testing.T) {
	fx := newVTSFixture(t)
	_, err := fx.vts.Process(context.Background(), "")
	require.Error(t, err)
}

// TestNewVTSIssuerPeerSimulatorRejectsInvalidPolicy keeps the fail-fast contract:
// a JWS-of-JWE inbound policy needs both a decrypt and a verify kid.
func TestNewVTSIssuerPeerSimulatorRejectsInvalidPolicy(t *testing.T) {
	fx := newVTSFixture(t)
	_, err := NewVTSIssuerPeerSimulator(&VTSIssuerPeerConfig{
		KeyStore:   fx.mle.keystore,
		DecryptKid: testPeerKid,
		VerifyKid:  "",
		SignKid:    testPeerKid,
		EncryptKid: testOurKid,
		Logger:     logger.New("disabled", false),
	})
	require.ErrorIs(t, err, jose.ErrPolicyMismatch)
	assert.ErrorContains(t, err, "VTS issuer peer inbound policy")
}

// TestNewVTSIssuerPeerSimulatorRequiresLogger: the simulator is its own HTTP
// boundary, so it must be able to report the refusals it answers.
func TestNewVTSIssuerPeerSimulatorRequiresLogger(t *testing.T) {
	fx := newVTSFixture(t)
	_, err := NewVTSIssuerPeerSimulator(&VTSIssuerPeerConfig{
		KeyStore:   fx.mle.keystore,
		DecryptKid: testPeerKid,
		VerifyKid:  testOurKid,
		SignKid:    testPeerKid,
		EncryptKid: testOurKid,
	})
	require.ErrorContains(t, err, "requires a logger")
}

// TestNewVTSIssuerRelayServiceRejectsMissingKeyStore keeps the fail-fast contract
// the other relays have: no keys, no client.
func TestNewVTSIssuerRelayServiceRejectsMissingKeyStore(t *testing.T) {
	_, err := NewVTSIssuerRelayService(&VTSIssuerRelayConfig{
		PartnerURL: testPeerURL,
		SignKid:    testOurKid,
		EncryptKid: testPeerKid,
		VerifyKid:  testPeerKid,
		DecryptKid: testOurKid,
		Logger:     logger.New("disabled", false),
	})
	require.Error(t, err)
}

// TestNewVTSIssuerRelayServiceSurfacesPolicyError pins that Build validates the
// JWS-of-JWE pair: this mode signs, so an outbound policy without a sign kid must
// fail construction rather than the first partner call.
func TestNewVTSIssuerRelayServiceSurfacesPolicyError(t *testing.T) {
	fx := newVTSFixture(t)
	_, err := NewVTSIssuerRelayService(&VTSIssuerRelayConfig{
		PartnerURL: testPeerURL,
		KeyStore:   fx.mle.keystore,
		SignKid:    "",
		EncryptKid: testPeerKid,
		VerifyKid:  testPeerKid,
		DecryptKid: testOurKid,
		Logger:     logger.New("disabled", false),
	})
	require.ErrorContains(t, err, "build VTS issuer relay client")
}
