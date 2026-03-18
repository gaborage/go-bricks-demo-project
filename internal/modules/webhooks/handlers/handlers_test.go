package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/webhooks/domain"
	"github.com/gaborage/go-bricks/config"
	"github.com/gaborage/go-bricks/logger"
	"github.com/gaborage/go-bricks/server"
	"github.com/labstack/echo/v4"
)

type mockSigningService struct {
	signFunc   func(payload string) (*domain.SignedPayload, error)
	verifyFunc func(payload, sig string) (bool, error)
}

func (m *mockSigningService) Sign(payload string) (*domain.SignedPayload, error) {
	if m.signFunc != nil {
		return m.signFunc(payload)
	}
	return nil, errors.New("not implemented")
}

func (m *mockSigningService) Verify(payload, sig string) (bool, error) {
	if m.verifyFunc != nil {
		return m.verifyFunc(payload, sig)
	}
	return false, errors.New("not implemented")
}

func newTestContext() (echo.Context, *httptest.ResponseRecorder) {
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

	t.Run("successful sign", func(t *testing.T) {
		svc := &mockSigningService{
			signFunc: func(payload string) (*domain.SignedPayload, error) {
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

		status, _, _ := result.ResultMeta()
		if status != http.StatusCreated {
			t.Errorf("status = %d, want %d", status, http.StatusCreated)
		}
	})

	t.Run("service error", func(t *testing.T) {
		svc := &mockSigningService{
			signFunc: func(payload string) (*domain.SignedPayload, error) {
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
			verifyFunc: func(payload, sig string) (bool, error) {
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
			verifyFunc: func(payload, sig string) (bool, error) {
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

	t.Run("service error", func(t *testing.T) {
		svc := &mockSigningService{
			verifyFunc: func(payload, sig string) (bool, error) {
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
