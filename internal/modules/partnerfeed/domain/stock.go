// Package domain holds the partner's stock event and the topology names the
// module, its tests and scripts/external-exchange-demo.sh all have to agree on.
package domain

import "time"

// Topology. ExchangeName is the one name here this service does NOT own: the
// partner service declares partner-events with whatever shape it chooses, and
// this service only references it (DeclareExternalExchange, ADR-119). Everything
// else — the queue, its dead-letter pair and the binding — belongs to this
// service and is declared normally.
//
// TRAP (framework): no module in this process may also declare partner-events.
// Every module shares ONE declaration set, and a name that is both declared
// locally and marked external fails Validate at startup with "declared locally
// and marked external in the same declaration set". That is why the demo does
// not reuse product-events or payment-events: both are owned in-process.
const (
	// ExchangeName is the partner-owned exchange. Verified with a passive
	// exchange.declare on every declare pass, never created.
	ExchangeName = "partner-events"

	// RoutingKey is bound as an exact key, not a pattern. A passive declare
	// verifies existence only — the owner's exchange TYPE is invisible to it —
	// and an exact key routes the same through a direct or a topic exchange,
	// where a wildcard would silently match nothing on a direct one.
	RoutingKey = "partner.stock.updated"

	// EventType names the contract on the consumer declaration.
	EventType = "partner.stock.updated"

	// QueueName is owned here. DeclareQueueWithDLQ derives its dead-letter pair
	// from it: exchange "<queue>.dlx" and parking queue "<queue>.dlq".
	QueueName = "partnerfeed.stock.updated"

	// ConsumerTag identifies this consumer on the broker.
	ConsumerTag = "partnerfeed-stock-updated"
)

// StockUpdated is the partner's stock-level event as the partner publishes it
// on partner-events. This service owns the contract no more than it owns the
// exchange, so the `validate` tags are the only thing standing between a
// partner's mistake and this service's state: the framework decodes and
// validates every delivery BEFORE the handler runs, and a body that fails either
// step is nacked without requeue and parks on the queue's DLQ.
//
// Quantity is a pointer on purpose. With a plain int an event that simply
// omitted the field would decode to 0 and read as "out of stock"; as a pointer
// an absent field stays nil, which validation refuses, while an explicit 0
// passes.
type StockUpdated struct {
	EventID    string    `json:"eventId" validate:"required,max=64"`
	PartnerID  string    `json:"partnerId" validate:"required,max=64"`
	SKU        string    `json:"sku" validate:"required,max=64"`
	Quantity   *int      `json:"quantity" validate:"required,gte=0"`
	OccurredAt time.Time `json:"occurredAt" validate:"required"`
}
