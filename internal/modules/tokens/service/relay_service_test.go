package service

import (
	"context"
	"encoding/json"
	"io"
	nethttp "net/http"
	"net/http/httptest"
	"testing"

	"github.com/gaborage/go-bricks/jose"
	jositest "github.com/gaborage/go-bricks/jose/testing"
	kstest "github.com/gaborage/go-bricks/keystore/testing"
	"github.com/gaborage/go-bricks/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRelayServiceRoundTripsThroughJOSETransport asserts that the relay service:
//  1. Seals a tokenization request via the framework's JOSETransport
//  2. POSTs to a partner URL with Content-Type: application/jose
//  3. Unwraps the partner's JOSE-sealed response body
//  4. Returns the inner Token
//
// The "partner" is an httptest server that decrypts+verifies, calls TokenizationService,
// and re-seals the response — playing the same role the in-process simulator
// plays in the running demo. JOSE sealing happens on the bare handler return value,
// before any APIResponse envelope, so the peer seals {"token": ...} directly.
func TestRelayServiceRoundTripsThroughJOSETransport(t *testing.T) {
	ourPriv, _ := jositest.GenerateTestKeyPair(t)
	peerPriv, _ := jositest.GenerateTestKeyPair(t)

	const ourKid, peerKid = "tokens-our", "tokens-peer"
	resolver := jositest.NewTestResolver(map[string]any{
		ourKid:  ourPriv,
		peerKid: peerPriv,
	})

	// Peer-side policies: peer decrypts incoming with peer-priv, verifies with our-pub,
	// then signs with peer-priv and encrypts to our-pub on the way out.
	peerInbound := &jose.Policy{
		Direction: jose.DirectionInbound, DecryptKid: peerKid, VerifyKid: ourKid,
		SigAlg: jose.DefaultSigAlg, KeyAlg: jose.DefaultKeyAlg, Enc: jose.DefaultEnc, Cty: jose.DefaultCty,
	}
	peerOutbound := &jose.Policy{
		Direction: jose.DirectionOutbound, SignKid: peerKid, EncryptKid: ourKid,
		SigAlg: jose.DefaultSigAlg, KeyAlg: jose.DefaultKeyAlg, Enc: jose.DefaultEnc, Cty: jose.DefaultCty,
	}

	tokSvc := NewTokenizationService()
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		require.Equal(t, jose.ContentType, r.Header.Get("Content-Type"))
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		_ = r.Body.Close()

		plaintext, _, _, err := jose.Open(string(body), peerInbound, resolver)
		require.NoError(t, err)

		var inner struct {
			PAN string `json:"pan"`
		}
		require.NoError(t, json.Unmarshal(plaintext, &inner))
		tok, err := tokSvc.Tokenize(inner.PAN)
		require.NoError(t, err)

		raw, err := json.Marshal(map[string]any{"token": tok})
		require.NoError(t, err)

		sealed, err := jose.Seal(raw, peerOutbound, resolver)
		require.NoError(t, err)
		w.Header().Set("Content-Type", jose.ContentType)
		_, _ = w.Write([]byte(sealed))
	}))
	t.Cleanup(server.Close)

	ks := kstest.NewMockKeyStore().
		WithPublicKey(ourKid, &ourPriv.PublicKey).
		WithPrivateKey(ourKid, ourPriv).
		WithPublicKey(peerKid, &peerPriv.PublicKey).
		WithPrivateKey(peerKid, peerPriv)

	relay, err := NewRelayService(&RelayConfig{
		PartnerURL: server.URL,
		KeyStore:   ks,
		SignKid:    ourKid,
		EncryptKid: peerKid,
		VerifyKid:  peerKid,
		DecryptKid: ourKid,
		Logger:     logger.New("disabled", false),
	})
	require.NoError(t, err)

	tok, err := relay.Relay(context.Background(), "4111111111111111")
	require.NoError(t, err)
	assert.Equal(t, "visa", tok.Network)
	assert.Equal(t, "1111", tok.Last4)
}

// TestRelayServiceRejectsMissingKeyStore guards the fail-fast contract — a relay
// without a keystore can never make a successful call, so construction must surface
// that error rather than producing a half-wired client.
func TestRelayServiceRejectsMissingKeyStore(t *testing.T) {
	_, err := NewRelayService(&RelayConfig{
		PartnerURL: "http://example",
		SignKid:    "a", EncryptKid: "b", VerifyKid: "c", DecryptKid: "d",
		Logger: logger.New("disabled", false),
	})
	require.Error(t, err)
}
