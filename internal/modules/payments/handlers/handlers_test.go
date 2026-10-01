package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/payments/domain"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/payments/service"
	"github.com/gaborage/go-bricks/config"
	"github.com/gaborage/go-bricks/logger"
	"github.com/gaborage/go-bricks/messaging"
	msgtesting "github.com/gaborage/go-bricks/messaging/testing"
	"github.com/gaborage/go-bricks/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testPAN           = "4111111111111111"
	testOrderID       = "11111111-2222-3333-4444-555555555555"
	testCurrency      = "USD"
	errCodeBadRequest = "BAD_REQUEST"
	errCodeInternal   = "INTERNAL_ERROR"
)

// mockService implements PaymentAuthorizer for the handler tests.
type mockService struct {
	authorizeFunc func(ctx context.Context, req service.AuthorizeRequest) (*domain.PaymentAuthorized, error)
	lastRequest   service.AuthorizeRequest
}

func (m *mockService) Authorize(ctx context.Context, req service.AuthorizeRequest) (*domain.PaymentAuthorized, error) {
	m.lastRequest = req
	if m.authorizeFunc != nil {
		return m.authorizeFunc(ctx, req)
	}
	return nil, errors.New("not implemented")
}

func newMockLogger() logger.Logger { return logger.New("info", false) }

func newMockConfig() *config.Config {
	return &config.Config{
		App: config.AppConfig{Name: "test", Version: "1.0.0", Env: "test", Debug: true},
	}
}

func newTestContext(cfg *config.Config) server.HandlerContext {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()
	return server.NewHandlerContextForTest(rec, req, cfg)
}

func validBody() AuthorizePaymentRequest {
	return AuthorizePaymentRequest{
		Amount:   4999,
		Currency: testCurrency,
		Card: CardRequest{
			PAN:      testPAN,
			ExpMonth: 12,
			ExpYear:  2030,
			Holder:   "Ada Lovelace",
		},
	}
}

func TestAuthorizePaymentAccepted(t *testing.T) {
	mockSvc := &mockService{
		authorizeFunc: func(_ context.Context, req service.AuthorizeRequest) (*domain.PaymentAuthorized, error) {
			return &domain.PaymentAuthorized{
				OrderID:  testOrderID,
				Amount:   req.Amount,
				Currency: testCurrency,
				Card:     req.Card,
			}, nil
		},
	}
	handler := NewPaymentHandler(mockSvc, newMockLogger())

	result, apiErr := handler.AuthorizePayment(validBody(), newTestContext(newMockConfig()))
	require.Nil(t, apiErr)

	status, _, _ := result.ResultMeta()
	assert.Equal(t, http.StatusAccepted, status, "authorization is settled asynchronously")

	body := result.Data
	require.NotNil(t, body)
	assert.Equal(t, testOrderID, body.OrderID)
	assert.Equal(t, "authorized", body.Status)
	assert.Equal(t, int64(4999), body.Amount)
	assert.Equal(t, testCurrency, body.Currency)
	assert.Equal(t, "1111", body.CardLast4)
}

// The response is the one place card data could leak back out of the sealed
// path, so it is asserted explicitly rather than left to review.
func TestAuthorizePaymentResponseNeverCarriesThePAN(t *testing.T) {
	mockSvc := &mockService{
		authorizeFunc: func(_ context.Context, req service.AuthorizeRequest) (*domain.PaymentAuthorized, error) {
			return &domain.PaymentAuthorized{OrderID: testOrderID, Amount: req.Amount, Currency: testCurrency, Card: req.Card}, nil
		},
	}
	handler := NewPaymentHandler(mockSvc, newMockLogger())

	result, apiErr := handler.AuthorizePayment(validBody(), newTestContext(newMockConfig()))
	require.Nil(t, apiErr)
	assert.NotContains(t, fmt.Sprintf("%+v", result.Data), testPAN)
}

func TestAuthorizePaymentMapsTheBodyIntoTheServiceRequest(t *testing.T) {
	mockSvc := &mockService{
		authorizeFunc: func(_ context.Context, req service.AuthorizeRequest) (*domain.PaymentAuthorized, error) {
			return &domain.PaymentAuthorized{OrderID: testOrderID, Amount: req.Amount, Currency: req.Currency, Card: req.Card}, nil
		},
	}
	handler := NewPaymentHandler(mockSvc, newMockLogger())

	_, apiErr := handler.AuthorizePayment(validBody(), newTestContext(newMockConfig()))
	require.Nil(t, apiErr)

	assert.Equal(t, int64(4999), mockSvc.lastRequest.Amount)
	assert.Equal(t, testCurrency, mockSvc.lastRequest.Currency)
	assert.Equal(t, testPAN, mockSvc.lastRequest.Card.PAN)
	assert.Equal(t, 12, mockSvc.lastRequest.Card.ExpMonth)
	assert.Equal(t, 2030, mockSvc.lastRequest.Card.ExpYear)
	assert.Equal(t, "Ada Lovelace", mockSvc.lastRequest.Card.Holder)
}

func TestAuthorizePaymentErrorMapping(t *testing.T) {
	tests := []struct {
		name        string
		serviceErr  error
		wantStatus  int
		wantErrCode string
	}{
		{
			name:        "validation error is a bad request",
			serviceErr:  fmt.Errorf("%w: amount must be positive minor units", service.ErrValidation),
			wantStatus:  http.StatusBadRequest,
			wantErrCode: errCodeBadRequest,
		},
		{
			name:        "publish failure is an internal error",
			serviceErr:  errors.New("publish: broker unreachable"),
			wantStatus:  http.StatusInternalServerError,
			wantErrCode: errCodeInternal,
		},
		{
			name:        "undeclared publisher is an internal error",
			serviceErr:  service.ErrPublisherNotDeclared,
			wantStatus:  http.StatusInternalServerError,
			wantErrCode: errCodeInternal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockSvc := &mockService{
				authorizeFunc: func(context.Context, service.AuthorizeRequest) (*domain.PaymentAuthorized, error) {
					return nil, tt.serviceErr
				},
			}
			handler := NewPaymentHandler(mockSvc, newMockLogger())

			_, apiErr := handler.AuthorizePayment(validBody(), newTestContext(newMockConfig()))
			require.NotNil(t, apiErr)
			assert.Equal(t, tt.wantStatus, apiErr.HTTPStatus())
			assert.Equal(t, tt.wantErrCode, apiErr.ErrorCode())
		})
	}
}

// The internal-error path must not echo the underlying failure to the caller.
func TestAuthorizePaymentInternalErrorIsOpaque(t *testing.T) {
	mockSvc := &mockService{
		authorizeFunc: func(context.Context, service.AuthorizeRequest) (*domain.PaymentAuthorized, error) {
			return nil, errors.New("amqp://user:secret@broker:5672 refused")
		},
	}
	handler := NewPaymentHandler(mockSvc, newMockLogger())

	_, apiErr := handler.AuthorizePayment(validBody(), newTestContext(newMockConfig()))
	require.NotNil(t, apiErr)
	assert.NotContains(t, apiErr.Message(), "secret")
}

// orderIDPattern matches the order id the service mints (a random UUID, which
// can hold a run of five or more digits), so the digit-run check below judges
// only what else reached the sink.
var orderIDPattern = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

// With the real service behind the handler, a payment the broker returned as
// unroutable is a 500, never a 202 (go-bricks v0.69.0, ADR-122: the publisher is
// Mandatory). Both error shapes are spelled as messaging/amqp_client.go builds
// them: every attempt returned (publishExhausted), and a deadline that cut the
// retries short after a return (wrapCause). The response stays opaque and the
// log line names the cause without any card data.
func TestAuthorizePaymentUnroutablePublishIsAnInternalError(t *testing.T) {
	tests := map[string]error{
		"attempts exhausted": fmt.Errorf("%w after %d attempts: %w",
			messaging.ErrPublishRetriesExhausted, 5, messaging.ErrPublishUnroutable),
		"deadline during the retries": fmt.Errorf("%w; last attempt: %w",
			context.DeadlineExceeded, messaging.ErrPublishUnroutable),
	}

	for name, publishErr := range tests {
		t.Run(name, func(t *testing.T) {
			log, logs := captureFilteredLogger(t, appFilterConfig())
			capture := msgtesting.NewCapturePublisher[domain.PaymentAuthorized]()
			capture.Fail(publishErr)
			svc := service.NewPaymentService(func(context.Context) (messaging.AMQPClient, error) { return nil, nil }, log)
			svc.SetPublisher(capture)
			handler := NewPaymentHandler(svc, log)

			result, apiErr := handler.AuthorizePayment(validBody(), newTestContext(newMockConfig()))
			require.NotNil(t, apiErr, "a returned publish must never answer 202")
			assert.Equal(t, http.StatusInternalServerError, apiErr.HTTPStatus())
			assert.Equal(t, errCodeInternal, apiErr.ErrorCode())
			status, _, _ := result.ResultMeta()
			assert.NotEqual(t, http.StatusAccepted, status)
			assert.Nil(t, result.Data, "no order is reported for a payment no queue received")
			assert.Len(t, capture.Events(), 1, "the request reached the publisher")

			assert.NotContains(t, apiErr.Message(), "unroutable", "the cause stays in the log, not the response")
			assert.NotContains(t, apiErr.Message(), testPAN)

			line := logs.String()
			require.Contains(t, line, "Failed to authorize payment")
			assert.Contains(t, line, messaging.ErrPublishUnroutable.Error(), "the operator can see why it failed")
			assert.NotContains(t, line, testPAN)
			assert.NotContains(t, line, redactFixtureHolder)
			assert.NotContains(t, line, "expMonth")
			assert.NotContains(t, line, "expYear")
			assert.NotRegexp(t, longDigitRun, orderIDPattern.ReplaceAllString(line, "<orderId>"),
				"no digit run longer than 4 may reach the sink")
		})
	}
}
