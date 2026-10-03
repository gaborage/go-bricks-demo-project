package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/tokens/domain"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/tokens/service"
	"github.com/gaborage/go-bricks/jose"
	jositest "github.com/gaborage/go-bricks/jose/testing"
	"github.com/gaborage/go-bricks/logger"
	"github.com/gaborage/go-bricks/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockTokenizationService struct {
	tokenizeFunc func(ctx context.Context, pan string) (*domain.Token, error)
}

func (m *mockTokenizationService) Tokenize(ctx context.Context, pan string) (*domain.Token, error) {
	if m.tokenizeFunc != nil {
		return m.tokenizeFunc(ctx, pan)
	}
	return nil, errors.New("not implemented")
}

// newHandlerCtx builds a HandlerContext whose request context optionally carries
// jose.Claims, as the JOSE middleware would bind them before the handler runs.
func newHandlerCtx(claims *jose.Claims) server.HandlerContext {
	ctx := context.Background()
	if claims != nil {
		ctx = jose.WithClaims(ctx, claims)
	}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/tokens", nil)
	rec := httptest.NewRecorder()
	return server.NewHandlerContextForTest(rec, req, newMLEConfig())
}

const validPAN = "4111111111111111"

func TestCreateToken(t *testing.T) {
	log := logger.New("info", false)

	t.Run("happy path returns token", func(t *testing.T) {
		svc := &mockTokenizationService{
			tokenizeFunc: func(_ context.Context, pan string) (*domain.Token, error) {
				return &domain.Token{Token: "tok_abc", MaskedPAN: "****1111", Network: "visa", Last4: "1111"}, nil
			},
		}
		h := NewHandler(svc, log)

		resp, apiErr := h.CreateToken(TokenizeRequest{PAN: validPAN}, newHandlerCtx(nil))
		require.Nil(t, apiErr)
		require.NotNil(t, resp)
		assert.Equal(t, "tok_abc", resp.Token.Token)
	})

	t.Run("invalid PAN returns 400", func(t *testing.T) {
		svc := &mockTokenizationService{
			tokenizeFunc: func(_ context.Context, _ string) (*domain.Token, error) {
				return nil, service.ErrInvalidPAN
			},
		}
		h := NewHandler(svc, log)

		resp, apiErr := h.CreateToken(TokenizeRequest{PAN: "bad"}, newHandlerCtx(nil))
		require.Nil(t, resp)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusBadRequest, apiErr.HTTPStatus())
	})

	t.Run("service error returns 500", func(t *testing.T) {
		svc := &mockTokenizationService{
			tokenizeFunc: func(_ context.Context, _ string) (*domain.Token, error) {
				return nil, errors.New("boom")
			},
		}
		h := NewHandler(svc, log)

		resp, apiErr := h.CreateToken(TokenizeRequest{PAN: validPAN}, newHandlerCtx(nil))
		require.Nil(t, resp)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusInternalServerError, apiErr.HTTPStatus())
	})

	t.Run("stale claim rejects before calling service", func(t *testing.T) {
		svc := &mockTokenizationService{
			tokenizeFunc: func(_ context.Context, _ string) (*domain.Token, error) {
				t.Fatal("service should not be called when claim freshness fails")
				return nil, nil
			},
		}
		h := NewHandler(svc, log)
		claims := &jose.Claims{IssuedAt: time.Now().Add(-10 * time.Minute)}

		resp, apiErr := h.CreateToken(TokenizeRequest{PAN: validPAN}, newHandlerCtx(claims))
		require.Nil(t, resp)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusUnauthorized, apiErr.HTTPStatus())
	})

	t.Run("fresh claim allows the call through", func(t *testing.T) {
		svc := &mockTokenizationService{
			tokenizeFunc: func(_ context.Context, pan string) (*domain.Token, error) {
				return &domain.Token{Token: "tok_fresh"}, nil
			},
		}
		h := NewHandler(svc, log)
		claims := &jose.Claims{IssuedAt: time.Now()}

		resp, apiErr := h.CreateToken(TokenizeRequest{PAN: validPAN}, newHandlerCtx(claims))
		require.Nil(t, apiErr)
		require.NotNil(t, resp)
		assert.Equal(t, "tok_fresh", resp.Token.Token)
	})
}

func TestPeerSimulate(t *testing.T) {
	log := logger.New("info", false)

	t.Run("happy path returns token", func(t *testing.T) {
		svc := &mockTokenizationService{
			tokenizeFunc: func(_ context.Context, pan string) (*domain.Token, error) {
				return &domain.Token{Token: "tok_peer"}, nil
			},
		}
		h := NewHandler(svc, log)

		resp, apiErr := h.PeerSimulate(PeerSimRequest{PAN: validPAN}, newHandlerCtx(nil))
		require.Nil(t, apiErr)
		require.NotNil(t, resp)
		assert.Equal(t, "tok_peer", resp.Token.Token)
	})

	t.Run("invalid PAN returns 400", func(t *testing.T) {
		svc := &mockTokenizationService{
			tokenizeFunc: func(_ context.Context, _ string) (*domain.Token, error) {
				return nil, service.ErrInvalidPAN
			},
		}
		h := NewHandler(svc, log)

		resp, apiErr := h.PeerSimulate(PeerSimRequest{PAN: "bad"}, newHandlerCtx(nil))
		require.Nil(t, resp)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusBadRequest, apiErr.HTTPStatus())
	})

	t.Run("service error returns 500", func(t *testing.T) {
		svc := &mockTokenizationService{
			tokenizeFunc: func(_ context.Context, _ string) (*domain.Token, error) {
				return nil, errors.New("boom")
			},
		}
		h := NewHandler(svc, log)

		resp, apiErr := h.PeerSimulate(PeerSimRequest{PAN: validPAN}, newHandlerCtx(nil))
		require.Nil(t, resp)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusInternalServerError, apiErr.HTTPStatus())
	})
}

func TestEnforceClaimFreshness(t *testing.T) {
	log := logger.New("info", false)
	svc := &mockTokenizationService{}
	h := NewHandler(svc, log)

	tests := map[string]struct {
		claims  *jose.Claims
		wantErr bool
		status  int
	}{
		"no claims in context": {
			claims:  nil,
			wantErr: false,
		},
		"zero IssuedAt is treated as unset": {
			claims:  &jose.Claims{},
			wantErr: false,
		},
		"fresh claim passes": {
			claims:  &jose.Claims{IssuedAt: time.Now()},
			wantErr: false,
		},
		"claim just inside the window passes": {
			claims:  &jose.Claims{IssuedAt: time.Now().Add(-maxClaimAge + time.Second)},
			wantErr: false,
		},
		"stale claim is rejected": {
			claims:  &jose.Claims{IssuedAt: time.Now().Add(-maxClaimAge - time.Minute)},
			wantErr: true,
			status:  http.StatusUnauthorized,
		},
		"future-dated claim is rejected": {
			claims:  &jose.Claims{IssuedAt: time.Now().Add(maxClaimAge + time.Minute)},
			wantErr: true,
			status:  http.StatusUnauthorized,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			apiErr := h.enforceClaimFreshness(newHandlerCtx(tc.claims))
			if !tc.wantErr {
				assert.Nil(t, apiErr)
				return
			}
			require.NotNil(t, apiErr)
			assert.Equal(t, tc.status, apiErr.HTTPStatus())
		})
	}
}

// TestRegisterRoutes registers both JOSE-tagged routes through the real
// server.POST. A jose: tag needs a resolver in the registry, as the bootstrap
// provides whenever deps.KeyStore is set; without one registration panics.
func TestRegisterRoutes(t *testing.T) {
	ourPriv, _ := jositest.GenerateTestKeyPair(t)
	peerPriv, _ := jositest.GenerateTestKeyPair(t)
	resolver := jositest.NewTestResolver(map[string]any{
		"tokens-our":  ourPriv,
		"tokens-peer": peerPriv,
	})

	hr := server.NewHandlerRegistry(newMLEConfig(), server.WithJOSEResolver(resolver))
	h := NewHandler(&mockTokenizationService{}, logger.New("disabled", false))
	reg := newTestRegistrar()
	t.Cleanup(server.DefaultRouteRegistry.Clear)

	h.RegisterPartnerRoute(hr, reg)
	h.RegisterSimulatorRoute(hr, reg)

	assert.Len(t, reg.routes, 2)
	assert.Contains(t, reg.routes, http.MethodPost+" /tokens")
	assert.Contains(t, reg.routes, http.MethodPost+" "+PeerSimulatorPath)
}
