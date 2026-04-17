package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/webhooks/domain"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/webhooks/service"
	"github.com/gaborage/go-bricks/config"
	"github.com/gaborage/go-bricks/logger"
	"github.com/gaborage/go-bricks/server"
	"github.com/labstack/echo/v5"
)

type mockSigningService struct {
	signFunc   func(ctx context.Context, payload string) (*domain.SignedPayload, error)
	verifyFunc func(ctx context.Context, payload, sig string) (bool, error)
}

func (m *mockSigningService) Sign(ctx context.Context, payload string) (*domain.SignedPayload, error) {
	if m.signFunc != nil {
		return m.signFunc(ctx, payload)
	}
	return nil, errors.New("not implemented")
}

func (m *mockSigningService) Verify(ctx context.Context, payload, sig string) (bool, error) {
	if m.verifyFunc != nil {
		return m.verifyFunc(ctx, payload, sig)
	}
	return false, errors.New("not implemented")
}

func newTestContext() (*echo.Context, *httptest.ResponseRecorder) {
	e := echo.New()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()
	return e.NewContext(req, rec), rec
}

func newMockConfig() *config.Config {
	return &config.Config{
		App: config.AppConfig{Name: "test", Version: "1.0.0", Env: "test"},
	}
}

func TestSignPayload(t *testing.T) {
	log := logger.New("info", false)
	cfg := newMockConfig()

	t.Run("successful sign returns 200", func(t *testing.T) {
		svc := &mockSigningService{
			signFunc: func(_ context.Context, payload string) (*domain.SignedPayload, error) {
				return &domain.SignedPayload{
					Payload:   payload,
					Signature: "dGVzdA==",
					Algorithm: "RS256",
					KeyName:   "webhook-signing",
				}, nil
			},
		}

		handler := &WebhookHandler{service: svc, logger: log}
		echoCtx, _ := newTestContext()
		ctx := server.HandlerContext{Echo: echoCtx, Config: cfg}

		result, apiErr := handler.SignPayload(SignRequest{Payload: []byte(`{"event":"test"}`)}, ctx)
		if apiErr != nil {
			t.Fatalf("SignPayload() error = %v", apiErr)
		}
		if result == nil {
			t.Fatal("SignPayload() returned nil")
		}
		if result.Algorithm != "RS256" {
			t.Errorf("Algorithm = %q, want RS256", result.Algorithm)
		}
	})

	t.Run("service error", func(t *testing.T) {
		svc := &mockSigningService{
			signFunc: func(_ context.Context, payload string) (*domain.SignedPayload, error) {
				return nil, errors.New("key not found")
			},
		}

		handler := &WebhookHandler{service: svc, logger: log}
		echoCtx, _ := newTestContext()
		ctx := server.HandlerContext{Echo: echoCtx, Config: cfg}

		_, apiErr := handler.SignPayload(SignRequest{Payload: []byte(`{}`)}, ctx)
		if apiErr == nil {
			t.Fatal("SignPayload() expected error")
		}
		if apiErr.HTTPStatus() != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", apiErr.HTTPStatus(), http.StatusInternalServerError)
		}
	})
}

func TestVerifyPayload(t *testing.T) {
	log := logger.New("info", false)
	cfg := newMockConfig()

	t.Run("valid signature", func(t *testing.T) {
		svc := &mockSigningService{
			verifyFunc: func(_ context.Context, payload, sig string) (bool, error) {
				return true, nil
			},
		}

		handler := &WebhookHandler{service: svc, logger: log}
		echoCtx, _ := newTestContext()
		ctx := server.HandlerContext{Echo: echoCtx, Config: cfg}

		resp, apiErr := handler.VerifyPayload(VerifyRequest{Payload: "test", Signature: "c2ln"}, ctx)
		if apiErr != nil {
			t.Fatalf("VerifyPayload() error = %v", apiErr)
		}
		if !resp.Valid {
			t.Error("VerifyPayload() valid = false, want true")
		}
	})

	t.Run("invalid signature", func(t *testing.T) {
		svc := &mockSigningService{
			verifyFunc: func(_ context.Context, payload, sig string) (bool, error) {
				return false, nil
			},
		}

		handler := &WebhookHandler{service: svc, logger: log}
		echoCtx, _ := newTestContext()
		ctx := server.HandlerContext{Echo: echoCtx, Config: cfg}

		resp, apiErr := handler.VerifyPayload(VerifyRequest{Payload: "test", Signature: "bad"}, ctx)
		if apiErr != nil {
			t.Fatalf("VerifyPayload() error = %v", apiErr)
		}
		if resp.Valid {
			t.Error("VerifyPayload() valid = true, want false")
		}
	})

	t.Run("malformed signature returns 400", func(t *testing.T) {
		svc := &mockSigningService{
			verifyFunc: func(_ context.Context, payload, sig string) (bool, error) {
				return false, fmt.Errorf("%w: bad base64", service.ErrMalformedSignature)
			},
		}

		handler := &WebhookHandler{service: svc, logger: log}
		echoCtx, _ := newTestContext()
		ctx := server.HandlerContext{Echo: echoCtx, Config: cfg}

		_, apiErr := handler.VerifyPayload(VerifyRequest{Payload: "test", Signature: "!!!"}, ctx)
		if apiErr == nil {
			t.Fatal("VerifyPayload() expected error")
		}
		if apiErr.HTTPStatus() != http.StatusBadRequest {
			t.Errorf("status = %d, want %d", apiErr.HTTPStatus(), http.StatusBadRequest)
		}
	})

	t.Run("internal error returns 500", func(t *testing.T) {
		svc := &mockSigningService{
			verifyFunc: func(_ context.Context, payload, sig string) (bool, error) {
				return false, errors.New("key not found")
			},
		}

		handler := &WebhookHandler{service: svc, logger: log}
		echoCtx, _ := newTestContext()
		ctx := server.HandlerContext{Echo: echoCtx, Config: cfg}

		_, apiErr := handler.VerifyPayload(VerifyRequest{Payload: "test", Signature: "c2ln"}, ctx)
		if apiErr == nil {
			t.Fatal("VerifyPayload() expected error")
		}
		if apiErr.HTTPStatus() != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", apiErr.HTTPStatus(), http.StatusInternalServerError)
		}
	})
}

// --- Integration-style tests exercising JSON binding via Echo ---

func TestSignPayloadIntegration(t *testing.T) {
	log := logger.New("info", false)
	cfg := newMockConfig()

	svc := &mockSigningService{
		signFunc: func(_ context.Context, payload string) (*domain.SignedPayload, error) {
			return &domain.SignedPayload{
				Payload:   payload,
				Signature: "c2lnbmVk",
				Algorithm: "RS256",
				KeyName:   "webhook-signing",
			}, nil
		},
	}
	handler := NewWebhookHandler(svc, log)

	e := echo.New()
	body := strings.NewReader(`{"payload":{"event":"product.created"}}`)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/webhooks/sign", body)
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	echoCtx := e.NewContext(req, rec)

	ctx := server.HandlerContext{Echo: echoCtx, Config: cfg}

	// Bind request manually to simulate framework binding
	var signReq SignRequest
	if err := echoCtx.Bind(&signReq); err != nil {
		t.Fatalf("Bind() error = %v", err)
	}

	result, apiErr := handler.SignPayload(signReq, ctx)
	if apiErr != nil {
		t.Fatalf("SignPayload() error = %v", apiErr)
	}
	if result == nil {
		t.Fatal("result is nil")
	}
	if result.Algorithm != "RS256" {
		t.Errorf("Algorithm = %q, want RS256", result.Algorithm)
	}
	if result.Signature != "c2lnbmVk" {
		t.Errorf("Signature = %q, want c2lnbmVk", result.Signature)
	}
}

func TestVerifyPayloadIntegration(t *testing.T) {
	log := logger.New("info", false)
	cfg := newMockConfig()

	t.Run("valid signature via JSON binding", func(t *testing.T) {
		svc := &mockSigningService{
			verifyFunc: func(_ context.Context, payload, sig string) (bool, error) {
				if payload != "test-data" || sig != "dGVzdA==" {
					t.Errorf("unexpected args: payload=%q sig=%q", payload, sig)
				}
				return true, nil
			},
		}
		handler := NewWebhookHandler(svc, log)

		e := echo.New()
		body := strings.NewReader(`{"payload":"test-data","signature":"dGVzdA=="}`)
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/webhooks/verify", body)
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		echoCtx := e.NewContext(req, rec)

		var verifyReq VerifyRequest
		if err := echoCtx.Bind(&verifyReq); err != nil {
			t.Fatalf("Bind() error = %v", err)
		}

		ctx := server.HandlerContext{Echo: echoCtx, Config: cfg}
		resp, apiErr := handler.VerifyPayload(verifyReq, ctx)
		if apiErr != nil {
			t.Fatalf("VerifyPayload() error = %v", apiErr)
		}
		if !resp.Valid {
			t.Error("valid = false, want true")
		}
	})

	t.Run("malformed signature via JSON binding returns 400", func(t *testing.T) {
		svc := &mockSigningService{
			verifyFunc: func(_ context.Context, payload, sig string) (bool, error) {
				return false, fmt.Errorf("%w: bad base64", service.ErrMalformedSignature)
			},
		}
		handler := NewWebhookHandler(svc, log)

		e := echo.New()
		body := strings.NewReader(`{"payload":"data","signature":"!!!"}`)
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/webhooks/verify", body)
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		echoCtx := e.NewContext(req, rec)

		var verifyReq VerifyRequest
		if err := echoCtx.Bind(&verifyReq); err != nil {
			t.Fatalf("Bind() error = %v", err)
		}

		ctx := server.HandlerContext{Echo: echoCtx, Config: cfg}
		_, apiErr := handler.VerifyPayload(verifyReq, ctx)
		if apiErr == nil {
			t.Fatal("expected error")
		}
		if apiErr.HTTPStatus() != http.StatusBadRequest {
			t.Errorf("status = %d, want %d", apiErr.HTTPStatus(), http.StatusBadRequest)
		}
	})
}
