package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/tokens/domain"
	"github.com/gaborage/go-bricks/config"
	"github.com/gaborage/go-bricks/logger"
	"github.com/gaborage/go-bricks/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	peerCompact  = "eyJhbGciOiJSU0EtT0FFUC0yNTYifQ.peer.reply.compact.jwe"
	mlePeerPath  = "/__sim/peer/mle"
	mleRelayPath = "/tokens/mle-relay"
)

// stubPeer answers with a fixed compact JWE — the wire SHAPE is what these tests
// pin, not the crypto (mle_relay_service_test.go covers that).
type stubPeer struct{}

func (*stubPeer) Process(context.Context, string) (string, error) { return peerCompact, nil }

// stubRelay stands in for the outbound bare-JWE relay.
type stubRelay struct{}

func (*stubRelay) Relay(context.Context, string) (*domain.Token, error) {
	return &domain.Token{Token: "tok_123", Network: "visa", Last4: "1111"}, nil
}

// testRegistrar is the RouteRegistrar test fake RegisterHandler documents a
// fallback for: it does not implement the unexported echoAdder seam, so the
// framework adapts the typed handler onto server.Handler and hands it here.
// Registering through the real server.POST is the point — the route OPTIONS
// (WithRawResponse) only take effect on this path.
type testRegistrar struct {
	routes map[string]server.Handler
}

func newTestRegistrar() *testRegistrar {
	return &testRegistrar{routes: make(map[string]server.Handler)}
}

func (tr *testRegistrar) Add(method, path string, handler server.Handler, _ ...server.MiddlewareFunc) {
	tr.routes[method+" "+path] = handler
}

func (tr *testRegistrar) Group(_ string, _ ...server.MiddlewareFunc) server.RouteRegistrar { return tr }

func (tr *testRegistrar) Use(_ ...server.MiddlewareFunc) {}

func (tr *testRegistrar) FullPath(path string) string { return path }

func newMLEConfig() *config.Config {
	return &config.Config{
		App: config.AppConfig{Name: "test", Version: "1.0.0", Env: "test"},
	}
}

// serveMLE registers the MLE routes for real and drives one of them, returning
// the recorded response.
func serveMLE(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	cfg := newMLEConfig()
	h := NewMLEHandler(&stubRelay{}, &stubPeer{}, logger.New("disabled", false))
	reg := newTestRegistrar()
	h.RegisterRoutes(server.NewHandlerRegistry(cfg), reg)

	handler, ok := reg.routes[method+" "+path]
	require.True(t, ok, "route %s %s was not registered", method, path)

	req := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	require.NoError(t, handler(server.NewHandlerContextForTest(rec, req, cfg)))
	return rec
}

// TestMLEPeerRouteAnswersWithTopLevelEncData pins the raw-response contract the
// peer route is registered with. httpclient.VisaMLEEnvelope().Unwrap recognizes
// a reply BY SHAPE — a non-empty top-level encData member. Wrapped in the
// standard APIResponse envelope the ciphertext would sit under "data", the
// unwrapper would find nothing to open, and the client would hand the caller
// ciphertext instead of the plaintext token. Drop WithRawResponse from
// RegisterRoutes and this test fails.
func TestMLEPeerRouteAnswersWithTopLevelEncData(t *testing.T) {
	rec := serveMLE(t, http.MethodPost, mlePeerPath, `{"encData":"eyJhbGciOiJSU0EtT0FFUC0yNTYifQ.request"}`)

	assert.Equal(t, http.StatusOK, rec.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))

	assert.Equal(t, peerCompact, body["encData"], "encData must sit at the TOP level")
	assert.NotContains(t, body, "data", "the APIResponse envelope would bury encData under data")
	assert.NotContains(t, body, "meta")
}

// TestMLERelayRouteKeepsAPIResponseEnvelope is the contrast case: the relay is a
// normal demo endpoint, so it DOES carry the envelope. Without it the assertion
// above would pass for a handler that simply never got wrapped.
func TestMLERelayRouteKeepsAPIResponseEnvelope(t *testing.T) {
	rec := serveMLE(t, http.MethodPost, mleRelayPath, `{"pan":"4111111111111111"}`)

	assert.Equal(t, http.StatusOK, rec.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))

	require.Contains(t, body, "data")
	assert.Contains(t, body, "meta")
}
