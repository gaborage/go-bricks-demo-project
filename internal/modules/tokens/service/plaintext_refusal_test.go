package service

import (
	"context"
	nethttp "net/http"
	"net/http/httptest"
	"testing"

	"github.com/gaborage/go-bricks/httpclient"
	"github.com/gaborage/go-bricks/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests pin the go-bricks v0.65.0 fail-closed rule (#1637) on every relay
// client: a partner that answers 2xx with a body the Inbound policy never opened
// is refused, never handed to the caller. A ciphertext stripped in transit is
// otherwise indistinguishable from a legitimate plaintext reply. They also pin
// WithPeerName (#1648): the refusal names the partner it came from.

const (
	nestedPeerName = "tokens-peer-sim"
	mlePeerName    = "visa-mle-peer-sim"

	// plaintextTokenBody is a well-formed token reply that nothing sealed. It
	// carries no PAN, only the display fields the tokenizer returns.
	plaintextTokenBody = `{"token":{"token":"tok_plaintext","masked_pan":"************1111","network":"visa","last4":"1111"}}`
)

// plaintextPeer answers every request with a 200 application/json body, as a
// partner that dropped its JOSE layer (or a proxy that stripped it) would.
func plaintextPeer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	peer := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(peer.Close)
	return peer
}

// TestRelayServiceRefusesPlaintextSuccess covers the nested JWE-of-JWS relay: with
// no Envelope, only an application/jose 2xx is opened, so a JSON 200 is refused.
func TestRelayServiceRefusesPlaintextSuccess(t *testing.T) {
	peer := plaintextPeer(t, plaintextTokenBody)

	relay, err := NewRelayService(&RelayConfig{
		PartnerURL: peer.URL,
		KeyStore:   newMLEFixture(t).keystore, // holds both halves of tokens-our and tokens-peer
		SignKid:    testOurKid,
		EncryptKid: testPeerKid,
		VerifyKid:  testPeerKid,
		DecryptKid: testOurKid,
		PeerName:   nestedPeerName,
		Logger:     logger.New("disabled", false),
	})
	require.NoError(t, err)

	tok, err := relay.Relay(context.Background(), testPAN)
	require.ErrorIs(t, err, httpclient.ErrJOSEPlaintextResponse)
	assert.ErrorContains(t, err, `peer: "`+nestedPeerName+`"`, "WithPeerName must name the partner in the refusal")
	assert.Nil(t, tok, "an unauthenticated 2xx body must never reach the caller")
}

// TestMLERelayRefusesPlaintextSuccess covers the bare-JWE relay behind Visa's
// encData envelope. VisaMLEEnvelope recognizes a reply by SHAPE, so a 2xx body
// without a non-empty top-level encData is refused whatever its Content-Type. The
// second case is the one TestMLEPeerRouteAnswersWithTopLevelEncData guards
// against: a peer route that lost WithRawResponse buries encData under "data".
func TestMLERelayRefusesPlaintextSuccess(t *testing.T) {
	cases := map[string]string{
		"plaintext token":            plaintextTokenBody,
		"encData buried in envelope": `{"data":{"encData":"eyJhbGciOiJSU0EtT0FFUC0yNTYifQ.a.b.c.d"},"meta":{}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			peer := plaintextPeer(t, body)

			relay, err := NewMLERelayService(&MLERelayConfig{
				PartnerURL: peer.URL,
				KeyStore:   newMLEFixture(t).keystore,
				EncryptKid: testPeerKid,
				DecryptKid: testOurKid,
				PeerName:   mlePeerName,
				Logger:     logger.New("disabled", false),
			})
			require.NoError(t, err)

			tok, err := relay.Relay(context.Background(), testPAN)
			require.ErrorIs(t, err, httpclient.ErrJOSEPlaintextResponse)
			assert.ErrorContains(t, err, `peer: "`+mlePeerName+`"`)
			assert.Nil(t, tok)
		})
	}
}

// TestVTSIssuerRelayRefusesPlaintextSuccess covers the JWS-of-JWE relay. Like the
// nested relay it sets no Envelope, so a 2xx that is not application/jose is
// refused before the verify-then-decrypt path ever runs. The peer is dialed over
// HTTP here (no Transport), as a production partner would be.
func TestVTSIssuerRelayRefusesPlaintextSuccess(t *testing.T) {
	peer := plaintextPeer(t, plaintextTokenBody)

	tok, err := newVTSRelay(t, newVTSFixture(t), peer.URL, nil).Relay(context.Background(), testPAN)
	require.ErrorIs(t, err, httpclient.ErrJOSEPlaintextResponse)
	assert.ErrorContains(t, err, `peer: "`+vtsPeerName+`"`)
	assert.Nil(t, tok)
}
