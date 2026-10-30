package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gaborage/go-bricks/jose"
	"github.com/gaborage/go-bricks/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testSignatureHeader = "X-Peer-Signature"

// gatewayStub serves the gateway's wire contract over real HTTP with the real
// DetachedGateway behind it: header framing, then Process, then the {"data"} or
// {"code"} envelope. The full framework route is exercised in the handlers
// package; this one keeps the simulator's own tests free of it.
func gatewayStub(t *testing.T, gw *DetachedGateway) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		var req struct {
			Data string `json:"data"`
		}
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &req))
		detached, _ := strings.CutPrefix(r.Header.Get(testSignatureHeader), "JWS ")

		w.Header().Set("Content-Type", "application/json")
		sealed, err := gw.Process(r.Context(), detached, req.Data)
		var jerr *jose.Error
		switch {
		case errors.As(err, &jerr):
			w.WriteHeader(jerr.Status)
			_, _ = w.Write([]byte(`{"code":"` + jerr.Code + `"}`))
		case err != nil:
			w.WriteHeader(nethttp.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"code":"INTERNAL"}`))
		default:
			_ = json.NewEncoder(w).Encode(map[string]string{"data": sealed})
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newDetachedSim builds the simulator as module.go does, against url, signing
// with signKid.
func newDetachedSim(t *testing.T, f *detachedFixture, url, signKid string) *DetachedPeerSimulator {
	t.Helper()
	sim, err := NewDetachedPeerSimulator(&DetachedPeerConfig{
		GatewayURL:      url,
		KeyStore:        f.vts.mle.keystore,
		EncryptKid:      testDetachedOurKid,
		SignKid:         signKid,
		DecryptKid:      testDetachedPeerKid,
		SignatureHeader: testSignatureHeader,
		PeerName:        "tokens-detached-gateway",
		Logger:          logger.New("disabled", false),
	})
	require.NoError(t, err)
	return sim
}

// TestDetachedPeerSimulatorExchange pins the wire the simulator produces: a
// kid-less RS512 detached signature over the exact data string, a kid-less
// request JWE, and a reply it opens to the token.
func TestDetachedPeerSimulatorExchange(t *testing.T) {
	f := newDetachedFixture(t)
	srv := gatewayStub(t, f.gateway)

	exchange, err := newDetachedSim(t, f, srv.URL, testDetachedPeerKid).Exchange(context.Background(), testPAN)
	require.NoError(t, err)
	assert.Equal(t, "1111", exchange.Token.Last4)

	wire := exchange.Wire
	for name, artefact := range map[string]string{
		"signatureHeader": wire.SignatureHeader, "requestBody": wire.RequestBody, "replyBody": wire.ReplyBody,
	} {
		assert.NotContains(t, artefact, testPAN, "%s must carry no PAN", name)
	}

	detached, ok := strings.CutPrefix(wire.SignatureHeader, "JWS ")
	require.True(t, ok, "the header carries the JWS scheme")
	segments := strings.Split(detached, ".")
	require.Len(t, segments, 3)
	assert.Empty(t, segments[1], "the payload segment is detached")
	outer := protectedHeader(t, detached)
	assert.Equal(t, map[string]any{"alg": "RS512"}, outer, "no kid and no cty on the outer JWS")

	var request struct {
		Data string `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(wire.RequestBody), &request))
	require.Equal(t, 4, strings.Count(request.Data, "."), "data is a compact JWE")
	inner := protectedHeader(t, request.Data)
	assert.Equal(t, "RSA-OAEP-256", inner["alg"])
	assert.Equal(t, "A256GCM", inner["enc"])
	assert.NotContains(t, inner, "kid")

	// The signature covers the exact data string: reattached, it opens.
	attached, err := jose.AttachDetachedJWS(detached, request.Data)
	require.NoError(t, err)
	plaintext, _, _, err := jose.Open(attached, NewDetachedInboundPolicy(testDetachedOurKid, testDetachedPeerKid), f.resolver)
	require.NoError(t, err)
	assert.JSONEq(t, testPANPayload, string(plaintext))

	var reply struct {
		Data string `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(wire.ReplyBody), &reply))
	assert.NotEmpty(t, f.openReply(t, reply.Data))
}

// A gateway refusal surfaces as *DetachedRefusal carrying the status and the
// gateway's code, nothing more. Here the simulator signs with the wrong key.
func TestDetachedPeerSimulatorSurfacesRefusal(t *testing.T) {
	f := newDetachedFixture(t)
	srv := gatewayStub(t, f.gateway)

	exchange, err := newDetachedSim(t, f, srv.URL, testDetachedOurKid).Exchange(context.Background(), testPAN)
	require.Error(t, err)
	assert.Nil(t, exchange)

	var refusal *DetachedRefusal
	require.True(t, errors.As(err, &refusal))
	assert.Equal(t, &DetachedRefusal{Status: nethttp.StatusUnauthorized, Code: joseSigInvalid}, refusal)
	assert.NotContains(t, err.Error(), testPAN)
}

func TestNewDetachedPeerSimulatorFailsFast(t *testing.T) {
	f := newDetachedFixture(t)
	valid := DetachedPeerConfig{
		GatewayURL:      testPeerURL,
		KeyStore:        f.vts.mle.keystore,
		EncryptKid:      testDetachedOurKid,
		SignKid:         testDetachedPeerKid,
		DecryptKid:      testDetachedPeerKid,
		SignatureHeader: testSignatureHeader,
		Logger:          logger.New("disabled", false),
	}

	_, err := NewDetachedPeerSimulator(nil)
	require.Error(t, err)

	for name, mutate := range map[string]func(*DetachedPeerConfig){
		"no keystore":         func(c *DetachedPeerConfig) { c.KeyStore = nil },
		"no gateway URL":      func(c *DetachedPeerConfig) { c.GatewayURL = "" },
		"no signature header": func(c *DetachedPeerConfig) { c.SignatureHeader = "" },
		"unknown signing key": func(c *DetachedPeerConfig) { c.SignKid = "tokens.detached.absent" },
		"no encrypt kid":      func(c *DetachedPeerConfig) { c.EncryptKid = "" },
		"no decrypt kid":      func(c *DetachedPeerConfig) { c.DecryptKid = "" },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := valid
			mutate(&cfg)
			_, err := NewDetachedPeerSimulator(&cfg)
			require.Error(t, err)
		})
	}
}

// TestManualPeerInvalidPayloadIsBehaviourNeutral pins the one change the
// detached gateway made to the core the MLE and VTS Issuer simulators share: a
// plaintext that is not JSON now also matches ErrInvalidPayload, while the
// error text and every other match stay what they were. The two simulators map
// only ErrInvalidPAN to a 400, so they still answer this case 500.
func TestManualPeerInvalidPayloadIsBehaviourNeutral(t *testing.T) {
	fx := newMLEFixture(t)
	const notJSON = "not json"
	decodeErr := json.Unmarshal([]byte(notJSON), &struct{}{})
	require.Error(t, decodeErr)

	compact, err := jose.Seal([]byte(notJSON), NewMLEOutboundPolicy(testPeerKid), fx.resolver)
	require.NoError(t, err)

	reply, err := fx.simulator.Process(context.Background(), compact)
	assert.Empty(t, reply)
	require.ErrorIs(t, err, ErrInvalidPayload)
	assert.NotErrorIs(t, err, ErrInvalidPAN)
	assert.EqualError(t, err, "decode MLE request payload: "+decodeErr.Error(), "the text is the one logged before the sentinel existed")
	var syntaxErr *json.SyntaxError
	assert.True(t, errors.As(err, &syntaxErr), "the decoder's error stays in the chain")
	var jerr *jose.Error
	assert.False(t, errors.As(err, &jerr))

	// The VTS Issuer simulator answers it as its own failure, as before.
	vts := newVTSFixture(t)
	reply2 := peerRoundTrip(t, vts.vts, nethttp.MethodPost, jose.ContentType,
		sealPayload(t, vts, NewVTSIssuerOutboundPolicy(testOurKid, testPeerKid), notJSON))
	assert.Equal(t, nethttp.StatusInternalServerError, reply2.status)
	assert.Equal(t, "INTERNAL_ERROR", decodePeerError(t, reply2).Code)
}
