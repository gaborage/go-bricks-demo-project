// Package payments demonstrates go-bricks sealed AMQP messages (ADR-097): an
// event type whose `seal` tags make the framework encrypt one declared Subject
// and sign the whole document on the way out, and verify + decrypt on the way
// in — with no crypto at any call site.
//
// The module is deliberately both producer and consumer so the demo is
// self-contained: POST /api/v1/payments/authorize publishes a sealed
// payment.authorized event, a consumer on payments.authorized opens it and
// records it exactly once through the inbox ledger, and a second, consumerless
// queue keeps a copy in the broker so the sealed bytes can be inspected.
package payments

import (
	"context"
	"errors"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/payments/domain"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/payments/handlers"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/payments/service"
	"github.com/gaborage/go-bricks/app"
	"github.com/gaborage/go-bricks/database"
	"github.com/gaborage/go-bricks/logger"
	"github.com/gaborage/go-bricks/messaging"

	// Import gate for payload sealing: messaging only PROBES for `seal` tags, the
	// codec that scans and seals lives here. Without this blank import a
	// seal-tagged declaration fails Declarations.Validate() at startup with
	// messaging.ErrSealingNotLinked. The gate keeps go-jose out of builds that
	// never seal; it is not a size optimization for this one.
	_ "github.com/gaborage/go-bricks/messaging/sealed"
	"github.com/gaborage/go-bricks/server"
)

// Topology. Named once so the producer, both queues and the consumer cannot
// drift apart.
const (
	exchangeName = "payment-events"
	routingKey   = "payment.authorized"
	eventType    = "payment.authorized"
	queueName    = "payments.authorized"
	// tapQueueName is a second queue bound to the same exchange and routing key
	// that NO consumer attaches to, so a published message stays in the broker
	// for the sealed-bytes proof to fetch.
	tapQueueName = "payments.authorized.tap"
	consumerTag  = "payments-authorized"

	// Bounds for the consumerless tap. Nothing drains it, so it MUST be capped:
	// an unbounded durable queue grows until the node raises a disk or memory
	// alarm, and that alarm blocks every publisher on the broker, not just this
	// demo's. 100 messages is far more than the proof needs (it purges the queue
	// on each run), and the TTL retires copies left behind by an aborted run.
	//
	// Plain ints on purpose: amqp091 encodes Go int as AMQP 'I' (int32). Widening
	// either to int64 changes the wire type and trips RabbitMQ's declare-
	// equivalence check against a queue already declared with the int32 form.
	tapMaxLength  = 100     // x-max-length, drop-head overflow (broker default)
	tapMessageTTL = 3600000 // x-message-ttl, milliseconds (1h)
)

// Module implements app.Module.
type Module struct {
	logger     logger.Logger
	inbox      app.InboxProcessor
	service    *service.PaymentService
	handler    *handlers.PaymentHandler
	authorized *messaging.Publisher[domain.PaymentAuthorized]
}

// NewModule returns an unwired Module. Init populates dependencies.
func NewModule() *Module {
	return &Module{}
}

// Name implements app.Module.
func (m *Module) Name() string { return "payments" }

// Init wires the service and handler.
//
// Both dependency checks fail startup rather than the first delivery: the
// consumer dedups through the inbox ledger, and sealing resolves its keys from
// the keystore, so a deployment missing either module is a wiring mistake that
// must surface here.
func (m *Module) Init(deps *app.ModuleDeps) error {
	m.logger = deps.Logger.WithFields(map[string]any{"module": "payments"})

	if deps.Inbox == nil {
		return errors.New("payments module requires a registered inbox module (inbox.NewModule(), before this one)")
	}
	if deps.KeyStore == nil {
		return errors.New("payments module requires a registered keystore module (keystore.NewModule(), before this one)")
	}
	m.inbox = deps.Inbox

	m.service = service.NewPaymentService(deps.Messaging, m.logger)
	m.handler = handlers.NewPaymentHandler(m.service, m.logger)

	m.logger.Info().Msg("Payments module initialized — sealed payment.authorized events")
	return nil
}

// RegisterRoutes attaches POST /payments/authorize.
func (m *Module) RegisterRoutes(hr *server.HandlerRegistry, r server.RouteRegistrar) {
	m.handler.RegisterPaymentRoutes(hr, r)
}

// DeclareMessaging declares the whole payment-events topology.
//
// It runs AFTER Init, which is why the typed publisher handle reaches the
// service through a setter rather than its constructor.
func (m *Module) DeclareMessaging(decls *messaging.Declarations) {
	decls.DeclareTopicExchange(exchangeName)

	// Producer side. The handle is bound to this destination once; Publish seals
	// from the seal tags on PaymentAuthorized without being asked.
	m.authorized = messaging.DeclareTypedPublisher[domain.PaymentAuthorized](decls, &messaging.PublisherOptions{
		Exchange:    exchangeName,
		RoutingKey:  routingKey,
		EventType:   eventType,
		Description: "Sealed payment authorization events (JWE-of-JWS)",
	})
	m.service.SetPublisher(m.authorized)

	// Consumer side. A message that fails any open rule — bad signature, unknown
	// key generation, wrong event type inside the envelope — is poison: nacked
	// without requeue, so it parks on the DLQ this queue declares.
	queue := decls.DeclareQueueWithDLQ(queueName, nil)
	decls.DeclareBinding(queue.Name, exchangeName, routingKey)

	// Consumerless tap: exists only for the broker-visibility proof, so it is
	// bounded — nothing consumes it, and an uncapped durable queue is a broker
	// outage waiting to happen (see tapMaxLength / tapMessageTTL above).
	//
	// NewQueue + RegisterQueue rather than DeclareQueue: there is no
	// DeclareQueueWithArgs, and RegisterQueue deep-copies Args while
	// DeclareQueue registers immediately — so args set on the value DeclareQueue
	// returns never reach the broker. They must be set before registering.
	//
	// A tap queue left over from a run before these bounds existed will fail
	// declaration with 406 PRECONDITION_FAILED; delete it once and it comes back
	// bounded.
	tap := messaging.NewQueue(tapQueueName)
	tap.Args["x-max-length"] = tapMaxLength
	tap.Args["x-message-ttl"] = tapMessageTTL
	decls.RegisterQueue(tap)
	decls.DeclareBinding(tap.Name, exchangeName, routingKey)

	// A seal-tagged type REQUIRES the WithMeta door: the dedup key lives on
	// Metadata, and the meta-less door refuses the declaration at startup.
	messaging.DeclareTypedConsumerWithMeta(decls, &messaging.ConsumerOptions{
		Queue:     queue.Name,
		Consumer:  consumerTag,
		EventType: eventType,
	}, m.onPaymentAuthorized)
}

// onPaymentAuthorized runs once the framework has verified the signature,
// decrypted the Subject and validated the decoded struct — evt.Card is
// plaintext here and nowhere else.
//
//nolint:gocritic // hugeParam: the signature is the framework's typed-consumer contract, func(ctx, T, Metadata) error.
func (m *Module) onPaymentAuthorized(ctx context.Context, evt domain.PaymentAuthorized, meta messaging.Metadata) error {
	// For a sealed delivery the key is "<sign family>:<jti>", composed from the
	// verified envelope rather than a publisher-written header, so it cannot be
	// forged into a skip+ACK. It never errors on this door; returning the error
	// keeps the fail-closed shape if the type ever loses its seal tags.
	key, err := meta.DedupKey()
	if err != nil {
		return err
	}

	// ctx must derive from the handler's: the sealed door marks it, and the
	// ledger admits a "<family>:<jti>" key only under that marker.
	return m.inbox.ProcessOnce(ctx, key, func(_ context.Context, _ database.Tx) error {
		envelope, sealed := meta.Sealed()
		m.logger.Info().
			Str("orderId", evt.OrderID).
			Int64("amount", evt.Amount).
			Str("currency", evt.Currency).
			Str("cardLast4", evt.Card.Last4()). // last four ONLY — never the PAN
			Bool("sealed", sealed).
			Str("jti", envelope.JTI).
			Str("signKid", envelope.SignKid).
			Str("envelopeEventType", envelope.EventType).
			Str("dedupKey", key).
			Msg("Payment authorization consumed exactly once")
		// The demo persists nothing: the ledger row committed by ProcessOnce is
		// the whole point — a redelivery short-circuits and never runs this again.
		return nil
	})
}

// Shutdown is a no-op — nothing this module owns needs explicit teardown.
func (m *Module) Shutdown() error { return nil }
