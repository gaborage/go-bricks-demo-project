// Package handlers provides the payments module's HTTP surface.
//
// The request structs carry `validate:` tags, NOT `binding:` — go-bricks reads
// only `validate:` (server.Validator wraps a bare go-playground/validator), so a
// rule spelled `binding:"required"` is inert and the field would reach the
// handler unchecked.
package handlers

import (
	"context"
	"errors"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/payments/domain"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/payments/service"
	"github.com/gaborage/go-bricks/logger"
	"github.com/gaborage/go-bricks/server"
)

// statusAuthorized is the lifecycle state the caller is told the order reached.
const statusAuthorized = "authorized"

// CardRequest is the card half of the authorization body. Its PAN validation
// matches the tokens module's: a bare 13-19 digit string, tagged `number`
// (^[0-9]+$) rather than `numeric`, which would also accept a leading sign and
// a decimal point.
//
// DEMO DATA ONLY — use a synthetic test PAN (4111111111111111).
type CardRequest struct {
	PAN      string `json:"pan" validate:"required,number,min=13,max=19"`
	ExpMonth int    `json:"expMonth" validate:"required,gte=1,lte=12"`
	ExpYear  int    `json:"expYear" validate:"required,gte=2000,lte=2100"`
	Holder   string `json:"holder" validate:"required"`
}

// CardRequest is safe to hand to a filtered logger whole; see RedactedForLog.
var _ logger.Redactor = CardRequest{}

// cardDetails maps the HTTP card onto the sealed Subject the service publishes.
func (c CardRequest) cardDetails() domain.CardDetails {
	return domain.CardDetails{
		PAN:      c.PAN,
		ExpMonth: c.ExpMonth,
		ExpYear:  c.ExpYear,
		Holder:   c.Holder,
	}
}

// RedactedForLog implements logger.Redactor (go-bricks v0.65.0, ADR-110) with
// the domain card's view, {last4}: a decoded card, or the
// AuthorizePaymentRequest that holds it, handed whole to a filtered logger's
// Interface or WithFields never renders the PAN, the expiry or the holder's
// name. The `pan` needle alone would mask the PAN but leave the rest in clear.
//
// A backstop, not a licence to log bodies: the hook is not consulted at Err,
// through Msgf or by an unfiltered logger. VALUE receiver on purpose: a
// pointer-receiver method would leave a bare CardRequest unrecognized, and the
// filter would walk it field by field.
func (c CardRequest) RedactedForLog() any { return c.cardDetails().RedactedForLog() }

// AuthorizePaymentRequest is the POST /payments/authorize body. Amount is in
// minor units (cents) — an integer, so no float rounding ever reaches money.
type AuthorizePaymentRequest struct {
	Amount   int64       `json:"amount" validate:"required,gt=0"`
	Currency string      `json:"currency" validate:"required,len=3,alpha"`
	Card     CardRequest `json:"card"`
}

// AuthorizePaymentResponse answers with the order id and the last four digits
// only. The PAN reached the broker inside the sealed Subject and must not come
// back out through the HTTP response.
type AuthorizePaymentResponse struct {
	OrderID   string `json:"orderId"`
	Status    string `json:"status"`
	Amount    int64  `json:"amount"`
	Currency  string `json:"currency"`
	CardLast4 string `json:"cardLast4"`
}

// PaymentAuthorizer is the narrow service contract the handler depends on,
// declared here so the package compiles against an interface it owns.
type PaymentAuthorizer interface {
	Authorize(ctx context.Context, req service.AuthorizeRequest) (*domain.PaymentAuthorized, error)
}

// PaymentHandler serves the payments module's routes.
type PaymentHandler struct {
	service PaymentAuthorizer
	logger  logger.Logger
}

// NewPaymentHandler wires the service into the HTTP layer.
func NewPaymentHandler(s PaymentAuthorizer, l logger.Logger) *PaymentHandler {
	return &PaymentHandler{service: s, logger: l}
}

// AuthorizePayment handles POST /api/v1/payments/authorize.
//
// It answers 202 Accepted rather than 201 Created: the authorization is
// published as an event and settled by the consumer, so at the moment the
// response is written the order id exists but the downstream work has not
// finished. The response body therefore reports a status rather than a
// resource.
func (h *PaymentHandler) AuthorizePayment(req AuthorizePaymentRequest, ctx server.HandlerContext) (server.Result[*AuthorizePaymentResponse], server.IAPIError) {
	evt, err := h.service.Authorize(ctx.RequestContext(), service.AuthorizeRequest{
		Amount:   req.Amount,
		Currency: req.Currency,
		Card:     req.Card.cardDetails(),
	})
	if err != nil {
		if errors.Is(err, service.ErrValidation) {
			return server.Result[*AuthorizePaymentResponse]{}, server.NewBadRequestError(err.Error())
		}
		// No card fragment in the error path either: log the currency and amount,
		// never the request struct.
		h.logger.Error().Err(err).
			Int64("amount", req.Amount).
			Str("currency", req.Currency).
			Msg("Failed to authorize payment")
		return server.Result[*AuthorizePaymentResponse]{}, server.NewInternalServerError("Failed to authorize payment")
	}

	return server.Accepted(&AuthorizePaymentResponse{
		OrderID:   evt.OrderID,
		Status:    statusAuthorized,
		Amount:    evt.Amount,
		Currency:  evt.Currency,
		CardLast4: evt.Card.Last4(),
	}), nil
}

// RegisterPaymentRoutes attaches the module's routes under the /api/v1 base path.
func (h *PaymentHandler) RegisterPaymentRoutes(hr *server.HandlerRegistry, r server.RouteRegistrar) {
	server.POST(hr, r, "/payments/authorize", h.AuthorizePayment)
}
