package payments

import (
	"testing"

	"github.com/gaborage/go-bricks/app"
	inboxtest "github.com/gaborage/go-bricks/inbox/testing"
	kstest "github.com/gaborage/go-bricks/keystore/testing"
	"github.com/gaborage/go-bricks/logger"
	"github.com/gaborage/go-bricks/messaging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// declareModuleMessaging runs what app.Run does for this module before it
// contacts a broker: Init, then DeclareMessaging into a fresh set.
//
// No sealing runtime is configured, so the seal-tagged publisher and consumer
// record a seal error for Validate to report. These tests only read the
// declarations, never Validate; cmd/api/messaging_declarations_test.go
// validates the whole set with key material.
func declareModuleMessaging(t *testing.T) *messaging.Declarations {
	t.Helper()

	m := NewModule()
	require.NoError(t, m.Init(&app.ModuleDeps{
		Logger:   logger.New("disabled", false),
		Inbox:    inboxtest.NewMockInbox(),
		KeyStore: kstest.NewMockKeyStore(),
	}))

	decls := messaging.NewDeclarations()
	m.DeclareMessaging(decls)
	return decls
}

// The payment.authorized publisher is Mandatory (go-bricks v0.69.0, ADR-122):
// a publish the broker returns as unroutable fails the request instead of
// answering 202 for an event no queue received. Dropping the flag compiles,
// boots and passes every other test, and silently reopens the ack-and-drop
// window of a topology repair, so it is pinned here.
func TestPaymentAuthorizedPublisherIsMandatory(t *testing.T) {
	decls := declareModuleMessaging(t)

	var found []*messaging.PublisherDeclaration
	for _, p := range decls.Publishers {
		if p.Exchange == exchangeName && p.RoutingKey == routingKey {
			found = append(found, p)
		}
	}
	require.Len(t, found, 1, "exactly one publisher declares %s/%s", exchangeName, routingKey)

	pub := found[0]
	assert.Equal(t, eventType, pub.EventType)
	assert.True(t, pub.Mandatory, "an unroutable payment.authorized publish must fail, not be dropped and ACKed")
	assert.False(t, pub.Immediate, "Immediate is not part of the decision")
}

// A Mandatory publish succeeds only while a binding on its exchange and
// routing key exists, so the consumer queue's binding must sit in the same
// declaration set, where every redeclare pass replays it.
//
// Its position matters too: the pass replays bindings in declaration order, so
// payments.authorized is re-bound before the tap. A publish landing between
// the two reaches the consumer and is not returned, while a tap-based count
// misses it. The topology-repair demo and load test explain their tap numbers
// with that order; swapping the two declarations would make a 202 able to
// reach only the tap, which nothing consumes.
func TestConsumerQueueBindingIsDeclaredBeforeTheTap(t *testing.T) {
	decls := declareModuleMessaging(t)

	consumerIdx, tapIdx := -1, -1
	for i, b := range decls.Bindings {
		if b.Exchange != exchangeName || b.RoutingKey != routingKey {
			continue
		}
		switch b.Queue {
		case queueName:
			consumerIdx = i
		case tapQueueName:
			tapIdx = i
		}
	}
	require.NotEqual(t, -1, consumerIdx, "%s must be bound to %s on %s", queueName, exchangeName, routingKey)
	require.NotEqual(t, -1, tapIdx, "%s must be bound to %s on %s", tapQueueName, exchangeName, routingKey)
	assert.Less(t, consumerIdx, tapIdx, "the consumer queue's binding must be replayed before the tap's")
}
