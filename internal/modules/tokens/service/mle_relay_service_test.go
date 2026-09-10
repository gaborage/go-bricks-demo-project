package service

import (
	"context"
	"encoding/json"
	"io"
	nethttp "net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gaborage/go-bricks/jose"
	jositest "github.com/gaborage/go-bricks/jose/testing"
	kstest "github.com/gaborage/go-bricks/keystore/testing"
	"github.com/gaborage/go-bricks/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testOurKid  = "tokens-our"
	testPeerKid = "tokens-peer"
	testPAN     = "4111111111111111"
	testPeerURL = "http://example"
)

// mleFixture builds the two keypairs, a keystore holding both halves of each,
// and the peer-side simulator that plays the counterparty.
type mleFixture struct {
	keystore  *kstest.MockKeyStore
	resolver  jose.KeyResolver
	simulator *MLEPeerSimulator
}

func newMLEFixture(t *testing.T) *mleFixture {
	t.Helper()
	ourPriv, _ := jositest.GenerateTestKeyPair(t)
	peerPriv, _ := jositest.GenerateTestKeyPair(t)

	ks := kstest.NewMockKeyStore().
		WithPublicKey(testOurKid, &ourPriv.PublicKey).
		WithPrivateKey(testOurKid, ourPriv).
		WithPublicKey(testPeerKid, &peerPriv.PublicKey).
		WithPrivateKey(testPeerKid, peerPriv)

	sim, err := NewMLEPeerSimulator(&MLEPeerConfig{
		KeyStore:   ks,
		DecryptKid: testPeerKid,
		EncryptKid: testOurKid,
	})
	require.NoError(t, err)

	return &mleFixture{
		keystore: ks,
		resolver: jositest.NewTestResolver(map[string]any{
			testOurKid:  ourPriv,
			testPeerKid: peerPriv,
		}),
		simulator: sim,
	}
}

// TestMLERelayRoundTripsThroughVisaEnvelope asserts the full Visa MLE shape:
//  1. The request body on the wire is {"encData":"<compact>"} as application/json
//  2. That compact is a BARE JWE — five segments, no inner JWS — carrying
//     typ=JOSE and a millisecond iat
//  3. The peer's reply in the same envelope is opened back to plaintext
func TestMLERelayRoundTripsThroughVisaEnvelope(t *testing.T) {
	fx := newMLEFixture(t)

	var sawContentType string
	peerInbound := NewMLEInboundPolicy(testPeerKid)

	peer := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		sawContentType = r.Header.Get("Content-Type")

		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		_ = r.Body.Close()

		var envelope struct {
			EncData string `json:"encData"`
		}
		require.NoError(t, json.Unmarshal(body, &envelope))
		require.NotEmpty(t, envelope.EncData, "outbound body must be the Visa MLE envelope")

		// Inspect the protected header the outbound policy wrote. Opening here
		// with the peer's own policy is exactly what the simulator does; doing it
		// twice is cheap and lets the assertions see the header.
		_, _, hdr, err := jose.Open(envelope.EncData, peerInbound, fx.resolver)
		require.NoError(t, err)
		assert.Equal(t, mleTyp, hdr.JWE.Typ, "Visa MLE expects typ=JOSE")
		assert.Equal(t, "A128GCM", hdr.JWE.Enc)
		assert.Equal(t, jose.Header{}, hdr.JWS, "bare mode carries no inner JWS")
		// IATMillis is epoch MILLISECONDS, so it is ~1000x a seconds timestamp.
		assert.InDelta(t, time.Now().UnixMilli(), hdr.JWE.IATMillis, float64(time.Minute.Milliseconds()))

		compact, err := fx.simulator.Process(r.Context(), envelope.EncData)
		require.NoError(t, err)

		reply, err := json.Marshal(map[string]string{"encData": compact})
		require.NoError(t, err)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(reply)
	}))
	t.Cleanup(peer.Close)

	relay, err := NewMLERelayService(&MLERelayConfig{
		PartnerURL: peer.URL,
		KeyStore:   fx.keystore,
		EncryptKid: testPeerKid,
		DecryptKid: testOurKid,
		Logger:     logger.New("disabled", false),
	})
	require.NoError(t, err)

	tok, err := relay.Relay(context.Background(), testPAN)
	require.NoError(t, err)
	assert.Equal(t, "application/json", sawContentType, "MLE travels as JSON, not application/jose")
	assert.Equal(t, "visa", tok.Network)
	assert.Equal(t, "1111", tok.Last4)
}

// nestedSeal produces the JWE-of-JWS shape the OTHER half of this module speaks:
// our signature inside a JWE encrypted to the peer. The outer JWE declares
// cty=JWS, which is exactly what the bare guard keys on.
func nestedSeal(t *testing.T, fx *mleFixture) string {
	t.Helper()
	nested := &jose.Policy{
		Direction:  jose.DirectionOutbound,
		SignKid:    testOurKid,
		EncryptKid: testPeerKid,
		SigAlg:     jose.DefaultSigAlg,
		KeyAlg:     jose.DefaultKeyAlg,
		Enc:        jose.DefaultEnc, // A256GCM
		Cty:        jose.DefaultCty,
	}
	compact, err := jose.Seal([]byte(`{"pan":"`+testPAN+`"}`), nested, fx.resolver)
	require.NoError(t, err)
	return compact
}

// TestMLEBareOpenRefusesNestedToken pins the downgrade guard end to end: a peer
// still sending the nested shape is refused by the simulator, which runs the
// production-shaped inbound policy (A128GCM only) — the A256GCM nested token
// never even parses.
func TestMLEBareOpenRefusesNestedToken(t *testing.T) {
	fx := newMLEFixture(t)

	plaintext, err := fx.simulator.Process(context.Background(), nestedSeal(t, fx))
	// go-jose's ParseEncrypted refuses the off-allowlist A256GCM before any key
	// material is touched, which the framework maps to ErrMalformed — the token
	// is rejected at PARSE, not at the cty guard the sibling test isolates.
	require.ErrorIs(t, err, jose.ErrMalformed)
	assert.Empty(t, plaintext, "a refused token must yield no payload")
}

// TestMLEBareOpenRefusesCtyJWS isolates the cty rule itself: widened to the full
// bare-mode content-encryption allowlist the nested token parses and decrypts,
// so the only thing left to refuse it is the unconditional cty=JWS guard. That
// guard is what stops an unverified inner compact JWS from surfacing as if it
// were the payload — bare mode verifies no signature.
func TestMLEBareOpenRefusesCtyJWS(t *testing.T) {
	fx := newMLEFixture(t)

	widened := NewMLEInboundPolicy(testPeerKid)
	widened.Enc = jose.DefaultEnc // A256GCM — bare mode admits it too

	plaintext, _, _, err := jose.Open(nestedSeal(t, fx), widened, fx.resolver)
	require.ErrorIs(t, err, jose.ErrCtyRejected)
	assert.Empty(t, plaintext, "the inner compact JWS must never reach the caller")
}

// TestMLEPeerSimulatorRejectsEmptyEnvelope guards the boundary check on the
// simulator's only input.
func TestMLEPeerSimulatorRejectsEmptyEnvelope(t *testing.T) {
	fx := newMLEFixture(t)
	_, err := fx.simulator.Process(context.Background(), "")
	require.Error(t, err)
}

// TestNewMLERelayServiceRejectsMissingKeyStore keeps the fail-fast contract the
// nested relay already has: no keys, no client.
func TestNewMLERelayServiceRejectsMissingKeyStore(t *testing.T) {
	_, err := NewMLERelayService(&MLERelayConfig{
		PartnerURL: testPeerURL,
		EncryptKid: testPeerKid,
		DecryptKid: testOurKid,
		Logger:     logger.New("disabled", false),
	})
	require.Error(t, err)
}

// TestNewMLERelayServiceSurfacesPolicyError pins that Build validates the bare
// policy pair: an outbound bare policy with no encrypt kid has no key identity
// at all and must fail construction.
func TestNewMLERelayServiceSurfacesPolicyError(t *testing.T) {
	fx := newMLEFixture(t)
	_, err := NewMLERelayService(&MLERelayConfig{
		PartnerURL: testPeerURL,
		KeyStore:   fx.keystore,
		EncryptKid: "",
		DecryptKid: testOurKid,
		Logger:     logger.New("disabled", false),
	})
	require.ErrorContains(t, err, "build MLE relay client")
}
