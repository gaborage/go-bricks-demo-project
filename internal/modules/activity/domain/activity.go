// Package domain holds the event type carried on the product-activity super
// stream, plus the topology names the publisher, the consumer and the HTTP
// projection all have to agree on.
package domain

import "time"

// Topology. Named once here — not in module.go — because the projection reports
// them on GET /products/activity, and a second copy is how a demo ends up
// consuming a stream it does not publish to.
//
// TRAP (framework): re-declaring a super stream with a DIFFERENT partition count
// is silently ignored by the client — unlike a plain stream's retention mismatch,
// which surfaces as precondition-failed and aborts startup. Changing
// PartitionCount therefore neither reshapes the broker topology nor fails; it
// only changes which partition the murmur3 routing sends a key to. Change the
// count by declaring a NEW super stream. See go-bricks wiki/streams.md ("Super
// streams") and ADR-059.
const (
	// SuperStream is the super stream name; the broker backs it with the
	// individual streams product-activity-0 … product-activity-2.
	SuperStream = "product-activity"

	// PartitionCount is fixed at declaration time — see the trap above.
	PartitionCount = 3

	// ConsumerName is the offset-tracking key AND the group identity. The broker
	// stores one committed offset per partition under this name, so renaming it
	// replays the whole stream from Start.
	ConsumerName = "product-activity-projector"

	// Retention caps how far back a restart can replay. Streams NEVER shrink on
	// their own: without an explicit spec the super stream grows until the disk
	// does, so retention is declared, not defaulted.
	Retention = 24 * time.Hour
)

// Action values carried by ProductActivity.Action. They are the bare verbs of
// the product.* outbox event types the products module already publishes, so the
// projection reads as a tally rather than as a second event catalogue.
const (
	ActionCreated = "created"
	ActionUpdated = "updated"
	ActionDeleted = "deleted"
)

// ProductActivity is one product lifecycle event on the product-activity super
// stream.
//
// The `validate` tags are enforced on the CONSUMER boundary by the framework, not
// by the publisher: DeclareTypedSuperStreamConsumerWithMeta decodes the body into
// this struct and runs go-playground validation before the handler is called. A
// body that fails either step is deterministic poison — never retried in place,
// never parked in the hold, offset not committed (ADR-092) — which is exactly
// what POST /__sim/streams/poison demonstrates.
type ProductActivity struct {
	ProductID  string    `json:"productId" validate:"required"`
	Action     string    `json:"action" validate:"required,oneof=created updated deleted"`
	Name       string    `json:"name"`
	Price      float64   `json:"price" validate:"gte=0"`
	OccurredAt time.Time `json:"occurredAt" validate:"required"`
}

// New builds an activity event stamped at the current UTC instant.
func New(productID, action, name string, price float64) ProductActivity {
	return ProductActivity{
		ProductID:  productID,
		Action:     action,
		Name:       name,
		Price:      price,
		OccurredAt: time.Now().UTC(),
	}
}
