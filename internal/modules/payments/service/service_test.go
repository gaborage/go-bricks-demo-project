package service

import (
	"context"
	"errors"
	"testing"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/payments/domain"
	"github.com/gaborage/go-bricks/logger"
	"github.com/gaborage/go-bricks/messaging"
	msgtesting "github.com/gaborage/go-bricks/messaging/testing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testPAN      = "4111111111111111"
	testCurrency = "usd"
)

// noMessagingClient stands in for deps.Messaging. CapturePublisher ignores the
// client entirely — nothing reaches a broker — so a nil client is honest here.
func noMessagingClient(context.Context) (messaging.AMQPClient, error) { return nil, nil }

func newTestService(t *testing.T) (*PaymentService, *msgtesting.CapturePublisher[domain.PaymentAuthorized]) {
	t.Helper()
	capture := msgtesting.NewCapturePublisher[domain.PaymentAuthorized]()
	svc := NewPaymentService(noMessagingClient, logger.New("info", false))
	svc.SetPublisher(capture)
	return svc, capture
}

func validRequest() AuthorizeRequest {
	return AuthorizeRequest{
		Amount:   4999,
		Currency: testCurrency,
		Card: domain.CardDetails{
			PAN:      testPAN,
			ExpMonth: 12,
			ExpYear:  2030,
			Holder:   "Ada Lovelace",
		},
	}
}

func TestAuthorizePublishesTheEvent(t *testing.T) {
	svc, capture := newTestService(t)

	evt, err := svc.Authorize(t.Context(), validRequest())
	require.NoError(t, err)
	require.NotNil(t, evt)

	published, ok := capture.Last()
	require.True(t, ok, "Authorize must publish exactly one event")
	assert.Len(t, capture.Events(), 1)

	assert.Equal(t, evt.OrderID, published.OrderID)
	assert.NotEmpty(t, published.OrderID, "the service mints the order id")
	assert.Equal(t, int64(4999), published.Amount)
	assert.Equal(t, "USD", published.Currency, "currency is normalized to upper case")
	assert.Equal(t, testPAN, published.Card.PAN)
	assert.Equal(t, 12, published.Card.ExpMonth)
	assert.Equal(t, 2030, published.Card.ExpYear)
	assert.Equal(t, "Ada Lovelace", published.Card.Holder)
}

func TestAuthorizeMintsAFreshOrderIDPerCall(t *testing.T) {
	svc, capture := newTestService(t)

	first, err := svc.Authorize(t.Context(), validRequest())
	require.NoError(t, err)
	second, err := svc.Authorize(t.Context(), validRequest())
	require.NoError(t, err)

	assert.NotEqual(t, first.OrderID, second.OrderID)
	assert.Len(t, capture.Events(), 2)
}

func TestAuthorizeRejectsInvalidRequests(t *testing.T) {
	tests := map[string]func(r *AuthorizeRequest){
		"zero amount":     func(r *AuthorizeRequest) { r.Amount = 0 },
		"negative amount": func(r *AuthorizeRequest) { r.Amount = -1 },
		"short currency":  func(r *AuthorizeRequest) { r.Currency = "US" },
		// Three bytes, so a length-only check would let this through.
		"non-alpha currency": func(r *AuthorizeRequest) { r.Currency = "U$D" },
		// ToUpper would fold this to SSD, so folding-before-validation would accept it.
		"unicode currency": func(r *AuthorizeRequest) { r.Currency = "ſSD" },
		"non-numeric PAN":  func(r *AuthorizeRequest) { r.Card.PAN = "abcd-efgh-ijkl" },
		// Both of these are 13-19 characters, so only the digits-only `number`
		// tag rejects them — `numeric` would let them through.
		"signed PAN":         func(r *AuthorizeRequest) { r.Card.PAN = "-41111111111111" },
		"decimal PAN":        func(r *AuthorizeRequest) { r.Card.PAN = "4111.11111111111" },
		"short PAN":          func(r *AuthorizeRequest) { r.Card.PAN = "411111111111" },
		"long PAN":           func(r *AuthorizeRequest) { r.Card.PAN = "41111111111111111111" },
		"expiry month zero":  func(r *AuthorizeRequest) { r.Card.ExpMonth = 0 },
		"expiry month 13":    func(r *AuthorizeRequest) { r.Card.ExpMonth = 13 },
		"expiry year absurd": func(r *AuthorizeRequest) { r.Card.ExpYear = 1999 },
		"blank holder":       func(r *AuthorizeRequest) { r.Card.Holder = "   " },
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			svc, capture := newTestService(t)
			req := validRequest()
			mutate(&req)

			evt, err := svc.Authorize(t.Context(), req)
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrValidation)
			assert.Nil(t, evt)
			assert.Empty(t, capture.Events(), "a refused request must not reach the broker")
		})
	}
}

func TestAuthorizeWithoutADeclaredPublisher(t *testing.T) {
	svc := NewPaymentService(noMessagingClient, logger.New("info", false))

	evt, err := svc.Authorize(t.Context(), validRequest())
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPublisherNotDeclared)
	assert.Nil(t, evt)
}

func TestAuthorizePropagatesPublishFailure(t *testing.T) {
	svc, capture := newTestService(t)
	boom := errors.New("broker unreachable")
	capture.Fail(boom)

	evt, err := svc.Authorize(t.Context(), validRequest())
	require.Error(t, err)
	assert.ErrorIs(t, err, boom)
	assert.Nil(t, evt)
}

func TestAuthorizePropagatesMessagingResolutionFailure(t *testing.T) {
	boom := errors.New("no messaging configured")
	svc := NewPaymentService(func(context.Context) (messaging.AMQPClient, error) { return nil, boom }, logger.New("info", false))
	svc.SetPublisher(msgtesting.NewCapturePublisher[domain.PaymentAuthorized]())

	evt, err := svc.Authorize(t.Context(), validRequest())
	require.Error(t, err)
	assert.ErrorIs(t, err, boom)
	assert.Nil(t, evt)
}

func TestCardDetailsLast4(t *testing.T) {
	assert.Equal(t, "1111", domain.CardDetails{PAN: testPAN}.Last4())
	assert.Empty(t, domain.CardDetails{PAN: "123"}.Last4())
	assert.Empty(t, domain.CardDetails{}.Last4())
}
