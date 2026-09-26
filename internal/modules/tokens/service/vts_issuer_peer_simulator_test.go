package service

import (
	"context"
	"encoding/json"
	"io"
	nethttp "net/http"
	"strings"
	"testing"

	"github.com/gaborage/go-bricks/jose"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests drive VTSIssuerPeerSimulator as the http.RoundTripper the relay
// client sees. It is the Issuer endpoint's whole HTTP boundary, so it makes the
// checks a jose:-tagged route makes before trust, and every refusal must leave
// through the same minimal {code, message} body.

// closeRecorder is a request body that remembers whether it was closed: the
// RoundTripper contract makes closing it the transport's job, on every path.
type closeRecorder struct {
	io.Reader
	closed bool
}

func (c *closeRecorder) Close() error {
	c.closed = true
	return nil
}

// peerReply is one fully read, closed simulator response.
type peerReply struct {
	status        int
	contentType   string
	contentLength int64
	body          []byte
}

// sendToPeer sends one request through the simulator, reads and closes the
// response, and asserts the request body was closed too.
func sendToPeer(t *testing.T, sim *VTSIssuerPeerSimulator, req *nethttp.Request) peerReply {
	t.Helper()
	resp, err := sim.RoundTrip(req)
	require.NoError(t, err, "every HTTP outcome is a response, never a transport error")
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return peerReply{
		status:        resp.StatusCode,
		contentType:   resp.Header.Get("Content-Type"),
		contentLength: resp.ContentLength,
		body:          body,
	}
}

// peerRoundTrip POSTs (or sends method) one body through the simulator.
func peerRoundTrip(t *testing.T, sim *VTSIssuerPeerSimulator, method, contentType, body string) peerReply {
	t.Helper()
	reqBody := &closeRecorder{Reader: strings.NewReader(body)}
	req, err := nethttp.NewRequestWithContext(context.Background(), method, testPeerURL, reqBody)
	require.NoError(t, err)
	req.Header.Set("Content-Type", contentType)

	reply := sendToPeer(t, sim, req)
	assert.True(t, reqBody.closed, "RoundTrip must close the request body")
	return reply
}

// decodePeerError reads a refusal body, requiring exactly the {code, message}
// pair and nothing else.
func decodePeerError(t *testing.T, reply peerReply) peerError {
	t.Helper()
	assert.Equal(t, "application/json", reply.contentType)

	var fields map[string]any
	require.NoError(t, json.Unmarshal(reply.body, &fields))
	assert.Len(t, fields, 2, "a pre-trust refusal carries code and message only")

	var body peerError
	require.NoError(t, json.Unmarshal(reply.body, &body))
	return body
}

// TestVTSIssuerPeerTransportAnswersBareCompact pins the reason the simulator is a
// transport and not a typed route: the reply is the compact itself, as
// application/jose, with no JSON quoting and no envelope. Surrounding whitespace
// on the request is tolerated, as on the framework's JOSE routes.
func TestVTSIssuerPeerTransportAnswersBareCompact(t *testing.T) {
	fx := newVTSFixture(t)
	compact := sealRequest(t, fx, NewVTSIssuerOutboundPolicy(testOurKid, testPeerKid))

	reply := peerRoundTrip(t, fx.vts, nethttp.MethodPost, jose.ContentType, compact+"\n")

	require.Equal(t, nethttp.StatusOK, reply.status)
	assert.True(t, jose.IsContentType(reply.contentType))
	assert.Equal(t, int64(len(reply.body)), reply.contentLength)
	require.Equal(t, 2, strings.Count(string(reply.body), "."), "a compact JWS, not an envelope")

	plaintext, _, _, err := jose.Open(string(reply.body), NewVTSIssuerInboundPolicy(testOurKid, testPeerKid), fx.mle.resolver)
	require.NoError(t, err)
	var unsealed struct {
		Token struct {
			Last4 string `json:"last4"`
		} `json:"token"`
	}
	require.NoError(t, json.Unmarshal(plaintext, &unsealed))
	assert.Equal(t, "1111", unsealed.Token.Last4)
}

// TestVTSIssuerPeerTransportRefusesPlaintext mirrors a jose:-tagged route's first
// check: a non-JOSE Content-Type is refused before the body is read.
func TestVTSIssuerPeerTransportRefusesPlaintext(t *testing.T) {
	fx := newVTSFixture(t)
	reply := peerRoundTrip(t, fx.vts, nethttp.MethodPost, "application/json", `{"pan":"`+testPAN+`"}`)

	assert.Equal(t, nethttp.StatusUnsupportedMediaType, reply.status)
	assert.Equal(t, "JOSE_PLAINTEXT_REJECTED", decodePeerError(t, reply).Code)
}

// TestVTSIssuerPeerTransportServesOnlyPost: the Issuer endpoint is a POST.
func TestVTSIssuerPeerTransportServesOnlyPost(t *testing.T) {
	fx := newVTSFixture(t)
	reply := peerRoundTrip(t, fx.vts, nethttp.MethodPut, jose.ContentType, "a.b.c")

	assert.Equal(t, nethttp.StatusMethodNotAllowed, reply.status)
	assert.Equal(t, "METHOD_NOT_ALLOWED", decodePeerError(t, reply).Code)
}

// TestVTSIssuerPeerTransportBoundsTheBody covers the body guards: over the cap is
// 413, empty or whitespace-only is 400, and neither reaches the private key.
func TestVTSIssuerPeerTransportBoundsTheBody(t *testing.T) {
	cases := map[string]struct {
		body   string
		status int
		code   string
	}{
		"oversize": {strings.Repeat("a", maxVTSIssuerBodyBytes+1), nethttp.StatusRequestEntityTooLarge, "JOSE_BODY_TOO_LARGE"},
		"empty":    {"", nethttp.StatusBadRequest, "JOSE_BODY_REQUIRED"},
		"blank":    {" \n", nethttp.StatusBadRequest, "JOSE_BODY_REQUIRED"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fx := newVTSFixture(t)
			reply := peerRoundTrip(t, fx.vts, nethttp.MethodPost, jose.ContentType, tc.body)

			assert.Equal(t, tc.status, reply.status)
			assert.Equal(t, tc.code, decodePeerError(t, reply).Code)
		})
	}
}

// TestVTSIssuerPeerTransportRefusesNilBody: a request with no body at all gets
// the same answer as an empty one.
func TestVTSIssuerPeerTransportRefusesNilBody(t *testing.T) {
	fx := newVTSFixture(t)
	req, err := nethttp.NewRequestWithContext(context.Background(), nethttp.MethodPost, testPeerURL, nethttp.NoBody)
	require.NoError(t, err)
	req.Body = nil
	req.Header.Set("Content-Type", jose.ContentType)

	reply := sendToPeer(t, fx.vts, req)
	assert.Equal(t, nethttp.StatusBadRequest, reply.status)
	assert.Equal(t, "JOSE_BODY_REQUIRED", decodePeerError(t, reply).Code)
}

// TestVTSIssuerPeerTransportMapsJOSERefusal pins what a refused open puts on the
// wire: the jose.Error's status, code and generic message, nothing more. A
// tampered signature fails verification, so the payload never reaches the key.
func TestVTSIssuerPeerTransportMapsJOSERefusal(t *testing.T) {
	fx := newVTSFixture(t)
	parts := strings.Split(sealRequest(t, fx, NewVTSIssuerOutboundPolicy(testOurKid, testPeerKid)), ".")
	require.Len(t, parts, 3)
	parts[2] = strings.Repeat("A", len(parts[2])) // a signature that cannot verify

	reply := peerRoundTrip(t, fx.vts, nethttp.MethodPost, jose.ContentType, strings.Join(parts, "."))

	assert.Equal(t, nethttp.StatusUnauthorized, reply.status)
	assert.Equal(t, "JOSE_SIGNATURE_INVALID", decodePeerError(t, reply).Code)
}

// TestVTSIssuerPeerTransportMapsInvalidPAN: a well-sealed request whose PAN fails
// validation is the caller's error, as the other simulators answer it.
func TestVTSIssuerPeerTransportMapsInvalidPAN(t *testing.T) {
	fx := newVTSFixture(t)
	compact := sealPayload(t, fx, NewVTSIssuerOutboundPolicy(testOurKid, testPeerKid), `{"pan":"not-a-pan"}`)

	reply := peerRoundTrip(t, fx.vts, nethttp.MethodPost, jose.ContentType, compact)

	assert.Equal(t, nethttp.StatusBadRequest, reply.status)
	assert.Equal(t, "BAD_REQUEST", decodePeerError(t, reply).Code)
}

// TestVTSIssuerPeerTransportHonoursCancellation: a request whose context is
// already done gets no response, like a network transport, and its body is
// still closed.
func TestVTSIssuerPeerTransportHonoursCancellation(t *testing.T) {
	fx := newVTSFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	body := &closeRecorder{Reader: strings.NewReader("a.b.c")}
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, testPeerURL, body)
	require.NoError(t, err)
	req.Header.Set("Content-Type", jose.ContentType)

	resp, err := fx.vts.RoundTrip(req) //nolint:bodyclose // a canceled request gets no response to close
	require.ErrorIs(t, err, context.Canceled)
	assert.Nil(t, resp)
	assert.True(t, body.closed)
}
