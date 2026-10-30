package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/tokens/domain"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/tokens/service"
	"github.com/gaborage/go-bricks/jose"
	jositest "github.com/gaborage/go-bricks/jose/testing"
	kstest "github.com/gaborage/go-bricks/keystore/testing"
	"github.com/gaborage/go-bricks/logger"
	"github.com/gaborage/go-bricks/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	stubSealed     = "eyJhbGciOiJSU0EtT0FFUC0yNTYifQ.reply.sealed.data.jwe"
	stubDetached   = "eyJhbGciOiJSUzUxMiJ9..c2lnbmF0dXJl"
	stubSignature  = detachedScheme + stubDetached
	stubDataBody   = `{"data":"eyJhbGciOiJSU0EtT0FFUC0yNTYifQ.request"}`
	detachedOurKid = "tokens.detached.our"
	detachedPeer   = "tokens.detached.peer"
	causeMarker    = "cause-text-that-must-not-be-logged"
	ctJSON         = "application/json"
	envData        = "data"
	envCode        = "code"
)

// stubOpener stands in for the gateway core: it records the call and answers
// with a fixed reply or error.
type stubOpener struct {
	sealed   string
	err      error
	calls    int
	detached string
	jwe      string
}

func (s *stubOpener) Process(_ context.Context, detached, jwe string) (string, error) {
	s.calls++
	s.detached, s.jwe = detached, jwe
	return s.sealed, s.err
}

// stubDetachedPeer stands in for the simulator.
type stubDetachedPeer struct {
	err error
}

func (s *stubDetachedPeer) Exchange(context.Context, string) (*service.DetachedExchange, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &service.DetachedExchange{
		Token: &domain.Token{Token: "tok_123", MaskedPAN: "************1111", Last4: "1111"},
		Wire:  service.DetachedWire{SignatureHeader: stubSignature, RequestBody: stubDataBody, ReplyBody: `{"data":"` + stubSealed + `"}`},
	}, nil
}

// registerDetached registers the detached routes for real, through server.POST,
// so the route options (WithRawResponse) take effect.
func registerDetached(t *testing.T, h *DetachedHandler) *testRegistrar {
	t.Helper()
	reg := newTestRegistrar()
	h.RegisterRoutes(server.NewHandlerRegistry(newMLEConfig()), reg)
	return reg
}

// serveDetached drives one registered route with the given Content-Type, body
// and signature header values (each added as its own header line).
func serveDetached(t *testing.T, reg *testRegistrar, path, contentType, body string, signatures ...string) *httptest.ResponseRecorder {
	t.Helper()
	handler, ok := reg.routes[http.MethodPost+" "+path]
	require.True(t, ok, "route POST %s was not registered", path)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, path, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for _, s := range signatures {
		req.Header.Add(DetachedSignatureHeader, s)
	}
	rec := httptest.NewRecorder()
	require.NoError(t, handler(server.NewHandlerContextForTest(rec, req, newMLEConfig())))
	return rec
}

// decodeEnvelope decodes the gateway's raw {"code"}/{"data"} reply.
func decodeEnvelope(t *testing.T, rec *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	var body map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), "body: %s", rec.Body.String())
	return body
}

// newStubDetachedHandler builds the handler over a stub core and simulator.
func newStubDetachedHandler(opener *stubOpener) *DetachedHandler {
	return NewDetachedHandler(opener, &stubDetachedPeer{}, logger.New("disabled", false))
}

// The success reply is the gateway's own envelope at the top level, never the
// APIResponse envelope: WithRawResponse is what the peer depends on.
func TestDetachedGatewayAnswersRawEnvelope(t *testing.T) {
	opener := &stubOpener{sealed: stubSealed}
	h := newStubDetachedHandler(opener)
	rec := serveDetached(t, registerDetached(t, h), DetachedGatewayPath, ctJSON, stubDataBody, stubSignature)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, map[string]string{envData: stubSealed}, decodeEnvelope(t, rec), "only data, at the top level")
	assert.Equal(t, stubDetached, opener.detached, "the scheme is stripped before the core sees it")
	assert.Equal(t, "eyJhbGciOiJSU0EtT0FFUC0yNTYifQ.request", opener.jwe, "the JWE is passed verbatim")
}

// TestDetachedGatewayFraming pins the handler's own refusals, decided before the
// core is called.
func TestDetachedGatewayFraming(t *testing.T) {
	oversize := detachedScheme + strings.Repeat("a", maxSignatureHeaderBytes)
	tests := []struct {
		name        string
		contentType string
		body        string
		signatures  []string
		wantStatus  int
		wantCode    string
	}{
		{"missing signature header", ctJSON, stubDataBody, nil, http.StatusUnauthorized, codeSignatureHeaderInvalid},
		{"duplicated signature header", ctJSON, stubDataBody, []string{stubSignature, stubSignature}, http.StatusUnauthorized, codeSignatureHeaderInvalid},
		{"oversize signature header", ctJSON, stubDataBody, []string{oversize}, http.StatusBadRequest, codeSignatureHeaderInvalid},
		{"signature header without the JWS scheme", ctJSON, stubDataBody, []string{stubDetached}, http.StatusUnauthorized, codeSignatureHeaderInvalid},
		{"signature header with another scheme", ctJSON, stubDataBody, []string{"Bearer " + stubDetached}, http.StatusUnauthorized, codeSignatureHeaderInvalid},
		{"empty data", ctJSON, `{"data":""}`, []string{stubSignature}, http.StatusBadRequest, codeDataMissing},
		{"no data member", ctJSON, `{}`, []string{stubSignature}, http.StatusBadRequest, codeDataMissing},
		{"non-JSON Content-Type skips the body", "text/plain", stubDataBody, []string{stubSignature}, http.StatusBadRequest, codeDataMissing},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opener := &stubOpener{sealed: stubSealed}
			h := newStubDetachedHandler(opener)
			rec := serveDetached(t, registerDetached(t, h), DetachedGatewayPath, tt.contentType, tt.body, tt.signatures...)

			assert.Equal(t, tt.wantStatus, rec.Code)
			assert.Equal(t, map[string]string{envCode: tt.wantCode}, decodeEnvelope(t, rec), "a refusal carries only its code")
			assert.Zero(t, opener.calls, "the core is never reached")
		})
	}
}

// Any application/json spelling and any */*+json type binds the body (go-bricks
// 424a51e9); on v0.72.0 both answered 400 "Invalid request data".
func TestDetachedGatewayBindsJSONMediaTypes(t *testing.T) {
	for _, contentType := range []string{"application/vnd.peer+json", "Application/JSON", "application/json; charset=utf-8"} {
		t.Run(contentType, func(t *testing.T) {
			opener := &stubOpener{sealed: stubSealed}
			h := newStubDetachedHandler(opener)
			rec := serveDetached(t, registerDetached(t, h), DetachedGatewayPath, contentType, stubDataBody, stubSignature)
			assert.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, 1, opener.calls)
		})
	}
}

func joseRefusal(code string, status int) error {
	return fmt.Errorf("open detached request: %w", &jose.Error{
		Sentinel: errors.New("sentinel"), Code: code, Status: status, Message: "refused", Cause: errors.New(causeMarker),
	})
}

// TestDetachedGatewayRefusalMapping pins the gateway's vocabulary: each core
// error and the status and code it answers.
func TestDetachedGatewayRefusalMapping(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"malformed", joseRefusal("JOSE_MALFORMED", 400), 400, codeMalformed},
		{"outer not a JWS", joseRefusal("JOSE_OUTER_NOT_JWS", 400), 400, codeMalformed},
		{"algorithm disallowed", joseRefusal("JOSE_ALGORITHM_DISALLOWED", 400), 400, codeAlgorithmRejected},
		{"kid unknown", joseRefusal("JOSE_KID_UNKNOWN", 401), 401, codeKeyUnknown},
		{"signature invalid", joseRefusal("JOSE_SIGNATURE_INVALID", 401), 401, codeSignatureInvalid},
		{"cty rejected", joseRefusal("JOSE_CTY_REJECTED", 400), 400, codeCtyRejected},
		{"an unlisted jose code keeps its status", joseRefusal("JOSE_DECRYPT_FAILED", 401), 401, codeRequestRejected},
		{"a key lookup failure carries no status", joseRefusal("JOSE_KID_UNKNOWN", 0), 500, codeInternal},
		{"a 5xx jose status is the gateway's fault", joseRefusal("JOSE_OUTBOUND_FAILED", 500), 500, codeInternal},
		{"an unavailable keystore is the gateway's fault", joseRefusal("JOSE_KEYSTORE_UNAVAILABLE", 503), 500, codeInternal},
		{"plaintext is not JSON", fmt.Errorf("decode: %w", service.ErrInvalidPayload), 400, codePayloadInvalid},
		{"PAN fails validation", service.ErrInvalidPAN, 400, codePayloadInvalid},
		{"anything else", errors.New("seal failed"), 500, codeInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newStubDetachedHandler(&stubOpener{err: tt.err})
			rec := serveDetached(t, registerDetached(t, h), DetachedGatewayPath, ctJSON, stubDataBody, stubSignature)
			assert.Equal(t, tt.wantStatus, rec.Code)
			assert.Equal(t, map[string]string{envCode: tt.wantCode}, decodeEnvelope(t, rec), "an error reply stays plaintext and carries no message")
		})
	}
}

// A jose refusal is logged by its code alone, and a payload refusal not with its
// error at all: decoder text can quote the opened plaintext.
func TestDetachedGatewayLogsOnlyCodes(t *testing.T) {
	payloadErr := fmt.Errorf("decode detached request payload: %w: invalid character in %s", service.ErrInvalidPayload, redactFixturePAN)
	for name, err := range map[string]error{
		"jose refusal":    joseRefusal("JOSE_SIGNATURE_INVALID", 401),
		"key lookup":      joseRefusal("JOSE_KID_UNKNOWN", 0),
		"payload refusal": payloadErr,
		"invalid PAN":     service.ErrInvalidPAN,
		"framing refusal": nil,
	} {
		t.Run(name, func(t *testing.T) {
			log, buf := captureFilteredLogger(t, logger.DefaultFilterConfig())
			h := NewDetachedHandler(&stubOpener{err: err, sealed: stubSealed}, &stubDetachedPeer{}, log)
			signatures := []string{stubSignature}
			if err == nil {
				signatures = nil
			}
			serveDetached(t, registerDetached(t, h), DetachedGatewayPath, ctJSON, stubDataBody, signatures...)

			line := buf.String()
			require.NotEmpty(t, line, "every refusal is logged")
			assert.NotContains(t, line, causeMarker)
			assert.NotContains(t, line, redactFixturePAN)
			assert.NotContains(t, line, "eyJ", "no ciphertext or header value is logged")
			var jerr *jose.Error
			if errors.As(err, &jerr) {
				assert.Contains(t, line, `"jose_code":"`+jerr.Code+`"`)
			}
		})
	}
}

// The simulator logs a jose failure (opening the reply, say) by its code alone:
// a *jose.Error renders its Cause in Error().
func TestDetachedSimulatorLogsOnlyJOSECode(t *testing.T) {
	log, buf := captureFilteredLogger(t, logger.DefaultFilterConfig())
	h := NewDetachedHandler(&stubOpener{}, &stubDetachedPeer{err: joseRefusal("JOSE_DECRYPT_FAILED", 401)}, log)
	rec := serveDetached(t, registerDetached(t, h), DetachedPeerSimulatorPath, ctJSON, `{"pan":"`+redactFixturePAN+`"}`)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	line := buf.String()
	assert.Contains(t, line, `"jose_code":"JOSE_DECRYPT_FAILED"`)
	assert.NotContains(t, line, causeMarker)
	assert.NotContains(t, line, redactFixturePAN)
}

// The simulator answers a gateway refusal with the gateway's code: 400 when its
// caller's PAN failed the gateway's check, 502 otherwise.
func TestDetachedSimulatorMapsGatewayRefusal(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"payload refused", &service.DetachedRefusal{Status: 400, Code: codePayloadInvalid}, http.StatusBadRequest, codePayloadInvalid},
		{"signature refused", &service.DetachedRefusal{Status: 401, Code: codeSignatureInvalid}, http.StatusBadGateway, codeSignatureInvalid},
		{"refusal without a code", &service.DetachedRefusal{Status: 400}, http.StatusBadGateway, "GATEWAY_REFUSED"},
		{"simulator failure", errors.New("dial failed"), http.StatusInternalServerError, "INTERNAL_ERROR"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := NewDetachedHandler(&stubOpener{}, &stubDetachedPeer{err: tt.err}, logger.New("disabled", false))
			rec := serveDetached(t, registerDetached(t, h), DetachedPeerSimulatorPath, ctJSON, `{"pan":"`+redactFixturePAN+`"}`)

			assert.Equal(t, tt.wantStatus, rec.Code)
			var body struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			assert.Equal(t, tt.wantCode, body.Error.Code)
			assert.NotContains(t, rec.Body.String(), redactFixturePAN)
		})
	}
}

// detachedE2E is the real gateway and the real simulator, both behind the
// framework's route wrapper, with the simulator calling the gateway over real
// loopback HTTP as it does in the running app.
type detachedE2E struct {
	reg *testRegistrar
	url string
}

func newDetachedE2E(t *testing.T) *detachedE2E {
	t.Helper()
	ourPriv, ourPub := jositest.GenerateTestKeyPair(t)
	peerPriv, peerPub := jositest.GenerateTestKeyPair(t)
	ks := kstest.NewMockKeyStore().
		WithPrivateKey(detachedOurKid, ourPriv).WithPublicKey(detachedOurKid, ourPub).
		WithPrivateKey(detachedPeer, peerPriv).WithPublicKey(detachedPeer, peerPub)

	var reg *testRegistrar
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler, ok := reg.routes[r.Method+" "+r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		require.NoError(t, handler(server.NewHandlerContextForTest(w, r, newMLEConfig())))
	}))
	t.Cleanup(srv.Close)

	gateway, err := service.NewDetachedGateway(&service.DetachedGatewayConfig{
		KeyStore: ks, DecryptKid: detachedOurKid, VerifyKid: detachedPeer, ReplyKid: detachedPeer,
	})
	require.NoError(t, err)
	peer, err := service.NewDetachedPeerSimulator(&service.DetachedPeerConfig{
		GatewayURL:      srv.URL + DetachedGatewayPath,
		KeyStore:        ks,
		EncryptKid:      detachedOurKid,
		SignKid:         detachedPeer,
		DecryptKid:      detachedPeer,
		SignatureHeader: DetachedSignatureHeader,
		PeerName:        "tokens-detached-gateway",
		Logger:          logger.New("disabled", false),
	})
	require.NoError(t, err)

	reg = registerDetached(t, NewDetachedHandler(gateway, peer, logger.New("disabled", false)))
	return &detachedE2E{reg: reg, url: srv.URL}
}

// postGateway sends one request to the gateway over HTTP.
func (e *detachedE2E) postGateway(t *testing.T, signature, body string) (status int, envelope map[string]string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, e.url+DetachedGatewayPath, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", ctJSON)
	req.Header.Set(DetachedSignatureHeader, signature)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &envelope), "body: %s", raw)
	return resp.StatusCode, envelope
}

// TestDetachedEndToEnd runs the demo's flow: the simulator route mints and sends,
// the gateway route opens and seals, the simulator opens the reply. The wire the
// simulator returns is then replayed verbatim, and tampered with.
func TestDetachedEndToEnd(t *testing.T) {
	e := newDetachedE2E(t)

	rec := serveDetached(t, e.reg, DetachedPeerSimulatorPath, ctJSON, `{"pan":"`+redactFixturePAN+`"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.NotContains(t, rec.Body.String(), redactFixturePAN, "neither the token nor the wire carries the PAN")

	var sim struct {
		Data DetachedSimResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &sim))
	assert.Equal(t, "1111", sim.Data.Token.Last4)
	wire := sim.Data.Wire
	require.True(t, strings.HasPrefix(wire.SignatureHeader, detachedScheme))

	// Replay: no ledger, so the same request is served again, with a fresh seal.
	status, replay := e.postGateway(t, wire.SignatureHeader, wire.RequestBody)
	assert.Equal(t, http.StatusOK, status)
	require.NotEmpty(t, replay[envData])
	var first map[string]string
	require.NoError(t, json.Unmarshal([]byte(wire.ReplyBody), &first))
	assert.NotEqual(t, first[envData], replay[envData], "every seal draws a new CEK and IV")

	// The signature covers the exact data string.
	var request map[string]string
	require.NoError(t, json.Unmarshal([]byte(wire.RequestBody), &request))
	tampered, err := json.Marshal(map[string]string{envData: request[envData] + "A"})
	require.NoError(t, err)
	status, refusal := e.postGateway(t, wire.SignatureHeader, string(tampered))
	assert.Equal(t, http.StatusUnauthorized, status)
	assert.Equal(t, map[string]string{envCode: codeSignatureInvalid}, refusal)

	// The reply's own JWE, valid but never signed, is refused the same way.
	swapped, err := json.Marshal(map[string]string{envData: first[envData]})
	require.NoError(t, err)
	status, refusal = e.postGateway(t, wire.SignatureHeader, string(swapped))
	assert.Equal(t, http.StatusUnauthorized, status)
	assert.Equal(t, map[string]string{envCode: codeSignatureInvalid}, refusal)
}

// A PAN that passes the simulator's format check but fails the gateway's Luhn
// check comes back as the gateway's PAYLOAD_INVALID.
func TestDetachedEndToEndInvalidPAN(t *testing.T) {
	e := newDetachedE2E(t)
	rec := serveDetached(t, e.reg, DetachedPeerSimulatorPath, ctJSON, `{"pan":"4111111111111112"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), `"code":"`+codePayloadInvalid+`"`)
}
