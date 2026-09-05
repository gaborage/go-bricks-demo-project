// Package service contains the payments module's business logic: it mints an
// order id, builds the sealed event and publishes it through the typed handle.
//
// Nothing here spells out sealing. The handle is a
// messaging.EventPublisher[domain.PaymentAuthorized]; sealing is engaged by the
// `seal` tags on that type, inside Publish, once — before the client's retry
// loop, so every attempt and every redelivery carries the same bytes and the
// same signed jti.
package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/payments/domain"
	"github.com/gaborage/go-bricks/logger"
	"github.com/gaborage/go-bricks/messaging"
	"github.com/google/uuid"
)

var (
	// ErrValidation reports a request the service refuses to turn into an event.
	ErrValidation = errors.New("invalid authorization request")
	// ErrPublisherNotDeclared means Authorize ran before DeclareMessaging handed
	// the module's typed handle to SetPublisher. It is a wiring bug, never a
	// request-shaped failure.
	ErrPublisherNotDeclared = errors.New("payments publisher not declared")
)

// AuthorizeRequest is the service-level input. The HTTP layer owns its own
// request struct and maps into this one, so the service stays usable from a
// consumer, a job or a test without an Echo context in sight.
type AuthorizeRequest struct {
	Amount   int64
	Currency string
	Card     domain.CardDetails
}

// PaymentService publishes payment authorizations as sealed events.
//
// The publisher is held behind messaging.EventPublisher[T] rather than as the
// concrete *messaging.Publisher[T]: that is the seam a test replaces with
// messaging/testing.CapturePublisher, asserting the typed event instead of
// re-decoding a frame of sealed bytes.
type PaymentService struct {
	publisher    messaging.EventPublisher[domain.PaymentAuthorized]
	getMessaging func(context.Context) (messaging.AMQPClient, error)
	logger       logger.Logger
}

// NewPaymentService wires the context-aware messaging accessor (deps.Messaging).
// The client is resolved per publish, not captured here, so multi-tenant mode
// picks the right broker connection from the request's context.
func NewPaymentService(getMessaging func(context.Context) (messaging.AMQPClient, error), log logger.Logger) *PaymentService {
	return &PaymentService{
		getMessaging: getMessaging,
		logger:       log,
	}
}

// SetPublisher injects the typed handle. It exists because the framework calls
// Init (which builds this service) BEFORE DeclareMessaging (which declares the
// handle) — the handle cannot be a constructor argument.
func (s *PaymentService) SetPublisher(p messaging.EventPublisher[domain.PaymentAuthorized]) {
	s.publisher = p
}

// Authorize validates the request, mints an order id and publishes the sealed
// PaymentAuthorized event. It returns the event it published so the caller can
// answer with the order id — never with the card.
func (s *PaymentService) Authorize(ctx context.Context, req AuthorizeRequest) (*domain.PaymentAuthorized, error) {
	if err := validate(&req); err != nil {
		return nil, err
	}
	if s.publisher == nil {
		return nil, ErrPublisherNotDeclared
	}

	evt := domain.PaymentAuthorized{
		OrderID:  uuid.New().String(),
		Amount:   req.Amount,
		Currency: strings.ToUpper(req.Currency),
		Card:     req.Card,
	}

	client, err := s.getMessaging(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve messaging client: %w", err)
	}

	// One call. The seal tags on PaymentAuthorized make the plaintext publish of
	// this type unrepresentable — there is no accept-unsealed door.
	if err := s.publisher.Publish(ctx, client, evt); err != nil {
		return nil, fmt.Errorf("publish %s: %w", evt.OrderID, err)
	}

	s.logger.Info().
		Str("orderId", evt.OrderID).
		Int64("amount", evt.Amount).
		Str("currency", evt.Currency).
		Str("cardLast4", evt.Card.Last4()). // last four only: never the PAN
		Msg("Payment authorization published as a sealed event")

	return &evt, nil
}

// validate is the service's own boundary check. The HTTP layer already rejects
// a malformed body through its validate: tags, but the service is a boundary in
// its own right — a consumer or a job calling it gets the same guarantees.
func validate(req *AuthorizeRequest) error {
	if req.Amount <= 0 {
		return fmt.Errorf("%w: amount must be positive minor units", ErrValidation)
	}
	// Length alone is not the rule: `U$D` is three bytes and no currency. This
	// check runs on the raw input BEFORE any case folding: strings.ToUpper
	// folds Unicode (e.g. 'ſ' U+017F LATIN SMALL LETTER LONG S upper-cases to
	// 'S'), so validating the upper-cased form would let a look-alike like
	// "ſSD" (4 raw bytes) through as if it were "SSD" (3 bytes). Authorize's
	// own strings.ToUpper call stays as normalization that happens only after
	// validation passes.
	if len(req.Currency) != 3 || !isAlpha(req.Currency) {
		return fmt.Errorf("%w: currency must be a 3-letter ISO-4217 code", ErrValidation)
	}
	if !isDigits(req.Card.PAN) || len(req.Card.PAN) < 13 || len(req.Card.PAN) > 19 {
		return fmt.Errorf("%w: card PAN must be 13-19 digits", ErrValidation)
	}
	if req.Card.ExpMonth < 1 || req.Card.ExpMonth > 12 {
		return fmt.Errorf("%w: card expiry month out of range", ErrValidation)
	}
	if req.Card.ExpYear < 2000 || req.Card.ExpYear > 2100 {
		return fmt.Errorf("%w: card expiry year out of range", ErrValidation)
	}
	if strings.TrimSpace(req.Card.Holder) == "" {
		return fmt.Errorf("%w: card holder is required", ErrValidation)
	}
	return nil
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// isAlpha reports whether s is made up entirely of ASCII letters, upper or
// lower case (A-Z, a-z). It iterates bytes rather than runes so that a
// multi-byte rune — a non-ASCII look-alike smuggled in before any case
// folding — fails on its individual bytes instead of being read as one
// (possibly in-range) code point.
func isAlpha(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		b := s[i]
		if (b < 'A' || b > 'Z') && (b < 'a' || b > 'z') {
			return false
		}
	}
	return true
}
