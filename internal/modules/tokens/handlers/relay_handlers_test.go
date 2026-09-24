package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/tokens/domain"
	"github.com/gaborage/go-bricks/logger"
	"github.com/gaborage/go-bricks/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const vtsRelayPath = "/tokens/vts-issuer-relay"

// failingRelay stands in for a relay whose partner call failed. Its error text
// is internal detail that must never reach the HTTP caller.
type failingRelay struct{}

func (*failingRelay) Relay(context.Context, string) (*domain.Token, error) {
	return nil, errors.New("partner returned status 401: internal detail")
}

// registerRelay registers one relay handler through the real server.POST and
// returns the routes it added.
func registerRelay(h *RelayHandler) *testRegistrar {
	reg := newTestRegistrar()
	h.RegisterRoute(server.NewHandlerRegistry(newMLEConfig()), reg)
	return reg
}

// serveVTSRelay registers the VTS Issuer relay for real and drives it.
func serveVTSRelay(t *testing.T, relay RelayService, body string) *httptest.ResponseRecorder {
	t.Helper()

	cfg := newMLEConfig()
	reg := registerRelay(NewVTSIssuerRelayHandler(relay, logger.New("disabled", false)))

	handler, ok := reg.routes[http.MethodPost+" "+vtsRelayPath]
	require.True(t, ok, "route POST %s was not registered", vtsRelayPath)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, vtsRelayPath, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	require.NoError(t, handler(server.NewHandlerContextForTest(rec, req, cfg)))
	return rec
}

// TestRelayHandlersRegisterOneRouteEach pins the path each constructor serves,
// and that the VTS Issuer relay adds no /__sim/ route: its counterparty lives in
// the relay client's transport, so there is no untagged raw route for the route
// table to carry.
func TestRelayHandlersRegisterOneRouteEach(t *testing.T) {
	l := logger.New("disabled", false)
	cases := map[string]struct {
		handler *RelayHandler
		path    string
	}{
		"nested":     {NewRelayHandler(&stubRelay{}, l), "/tokens/relay"},
		"VTS Issuer": {NewVTSIssuerRelayHandler(&stubRelay{}, l), vtsRelayPath},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			reg := registerRelay(tc.handler)
			assert.Len(t, reg.routes, 1)
			assert.Contains(t, reg.routes, http.MethodPost+" "+tc.path)
		})
	}
}

// TestVTSIssuerRelayRouteKeepsAPIResponseEnvelope: the relay is a normal typed
// route, so it answers the standard envelope with the token under data.
func TestVTSIssuerRelayRouteKeepsAPIResponseEnvelope(t *testing.T) {
	rec := serveVTSRelay(t, &stubRelay{}, `{"pan":"4111111111111111"}`)

	assert.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		Data *RelayResponse `json:"data"`
		Meta map[string]any `json:"meta"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.NotNil(t, body.Data)
	require.NotNil(t, body.Data.Token)
	assert.Equal(t, "tok_123", body.Data.Token.Token)
	assert.NotNil(t, body.Meta)
}

// TestVTSIssuerRelayRouteValidatesThePAN keeps the boundary check: a PAN that
// is not 13-19 digits never reaches the relay.
func TestVTSIssuerRelayRouteValidatesThePAN(t *testing.T) {
	rec := serveVTSRelay(t, &failingRelay{}, `{"pan":"not-a-pan"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestVTSIssuerRelayRouteHidesPartnerErrors pins that a failed partner call is a
// generic 500 naming the relay: the relay's error text stays in the server log.
func TestVTSIssuerRelayRouteHidesPartnerErrors(t *testing.T) {
	rec := serveVTSRelay(t, &failingRelay{}, `{"pan":"4111111111111111"}`)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NotContains(t, rec.Body.String(), "internal detail")
	assert.Contains(t, rec.Body.String(), "VTS issuer relay failed")
}
