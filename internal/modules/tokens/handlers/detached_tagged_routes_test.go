package handlers

import (
	"context"
	"crypto/rsa"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/tokens/domain"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/tokens/service"
	"github.com/gaborage/go-bricks/jose"
	jositest "github.com/gaborage/go-bricks/jose/testing"
	"github.com/gaborage/go-bricks/logger"
	"github.com/gaborage/go-bricks/server"
	josev4 "github.com/go-jose/go-jose/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	tokensOurKid  = "tokens.our"
	tokensPeerKid = "tokens.peer"
)

// taggedRouteKeys holds the module's four entries, each its own pair.
type taggedRouteKeys map[string]*rsa.PrivateKey

func newTaggedRouteKeys(t *testing.T) taggedRouteKeys {
	t.Helper()
	keys := taggedRouteKeys{}
	for _, kid := range []string{tokensOurKid, tokensPeerKid, detachedOurKid, detachedPeer} {
		priv, _ := jositest.GenerateTestKeyPair(t)
		keys[kid] = priv
	}
	return keys
}

func (k taggedRouteKeys) resolver() jose.KeyResolver {
	all := map[string]any{}
	for kid, priv := range k {
		all[kid] = priv
	}
	return jositest.NewTestResolver(all)
}

// attachedDetachedRequest mints what the gateway opens once it has reattached
// the signature: a kid-less bare JWE to tokens.detached.our, signed RS512 by
// tokens.detached.peer, in attached form.
func (k taggedRouteKeys) attachedDetachedRequest(t *testing.T) string {
	t.Helper()
	jwe, err := jose.Seal([]byte(`{"pan":"`+redactFixturePAN+`"}`), service.NewDetachedReplyPolicy(detachedOurKid), k.resolver())
	require.NoError(t, err)
	signer, err := josev4.NewSigner(josev4.SigningKey{Algorithm: josev4.RS512, Key: k[detachedPeer]}, nil)
	require.NoError(t, err)
	signed, err := signer.Sign([]byte(jwe))
	require.NoError(t, err)
	attached, err := signed.CompactSerialize()
	require.NoError(t, err)
	return attached
}

// scanPolicy reads a route's policy from its jose: struct tag, exactly as the
// framework's middleware does, so these tests cannot drift from the routes.
func scanPolicy(t *testing.T, v any, dir jose.Direction) *jose.Policy {
	t.Helper()
	p, err := jose.ScanType(reflect.TypeOf(v), dir)
	require.NoError(t, err)
	require.NotNil(t, p, "%T carries no jose: tag", v)
	return p
}

// TestDetachedBodiesRefusedOnTaggedRoutes pins the key separation against the
// two jose:-tagged nested routes, with each policy read from the route's own
// struct tags. The gateway's request opens on neither, and neither route's own
// body opens with the gateway's keys.
func TestDetachedBodiesRefusedOnTaggedRoutes(t *testing.T) {
	keys := newTaggedRouteKeys(t)
	attached := keys.attachedDetachedRequest(t)

	routes := []struct {
		route   string
		request any // the route's inbound tag
		sender  any // the tag a caller of this route seals with
	}{
		{"POST /tokens", TokenizeRequest{}, PeerSimResponse{}},
		{"POST " + PeerSimulatorPath, PeerSimRequest{}, TokenizeResponse{}},
	}
	for _, r := range routes {
		inbound := scanPolicy(t, r.request, jose.DirectionInbound)

		t.Run(r.route+": the gateway's request", func(t *testing.T) {
			plaintext, _, _, err := jose.Open(attached, inbound, keys.resolver())
			require.ErrorIs(t, err, jose.ErrMalformed, "a three-segment body is not a nested JWE")
			assert.Empty(t, plaintext)
		})

		body, err := jose.Seal([]byte(`{"pan":"`+redactFixturePAN+`"}`), scanPolicy(t, r.sender, jose.DirectionOutbound), keys.resolver())
		require.NoError(t, err)
		for _, detachedKid := range []string{detachedOurKid, detachedPeer} {
			t.Run(r.route+": its own body under "+detachedKid, func(t *testing.T) {
				// The route's decrypt kid resolves to the detached key; its verify
				// kid keeps its real public half.
				resolver := jositest.NewTestResolver(map[string]any{
					inbound.DecryptKid: keys[detachedKid],
					inbound.VerifyKid:  &keys[inbound.VerifyKid].PublicKey,
				})
				plaintext, _, _, err := jose.Open(body, inbound, resolver)
				var jerr *jose.Error
				require.True(t, errors.As(err, &jerr), "want a *jose.Error, got %v", err)
				assert.Equal(t, "JOSE_DECRYPT_FAILED", jerr.Code)
				assert.Empty(t, plaintext)
			})
		}
	}
}

// The real /tokens route, middleware included, refuses the gateway's request
// before its handler runs.
func TestDetachedRequestRefusedByTheTokensRoute(t *testing.T) {
	keys := newTaggedRouteKeys(t)
	called := false
	tokenizer := &mockTokenizationService{tokenizeFunc: func(context.Context, string) (*domain.Token, error) {
		called = true
		return nil, errors.New("must not be reached")
	}}

	hr := server.NewHandlerRegistry(newMLEConfig(), server.WithJOSEResolver(keys.resolver()))
	reg := newTestRegistrar()
	t.Cleanup(server.DefaultRouteRegistry.Clear)
	NewHandler(tokenizer, logger.New("disabled", false)).RegisterPartnerRoute(hr, reg)

	handler, ok := reg.routes[http.MethodPost+" /tokens"]
	require.True(t, ok)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/tokens", strings.NewReader(keys.attachedDetachedRequest(t)))
	req.Header.Set("Content-Type", jose.ContentType)
	rec := httptest.NewRecorder()
	_ = handler(server.NewHandlerContextForTest(rec, req, newMLEConfig()))

	assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "JOSE_MALFORMED")
	assert.False(t, called, "the handler never runs")
}
