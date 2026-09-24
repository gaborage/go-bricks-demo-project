package products

import (
	"reflect"
	"testing"

	"github.com/gaborage/go-bricks/messaging"
)

// product-events is replayed to the broker from the STORED declaration, so the
// stored shape is what a retained broker compares against on redeclare. This
// pins it to the shape the pre-helper literal produced: any drift in Type or a
// flag would surface as PRECONDITION_FAILED (inequivalent arg) on a broker that
// already holds the exchange, and only at boot.
func TestDeclareMessagingProductEventsShapeUnchanged(t *testing.T) {
	decls := messaging.NewDeclarations()
	NewModule().DeclareMessaging(decls)

	if err := decls.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}

	got, ok := decls.Exchanges[productEventsExchange]
	if !ok || got == nil {
		t.Fatalf("exchange %q not declared; have %v", productEventsExchange, decls.Exchanges)
	}

	// The literal DeclareMessaging registered before DeclareTopicExchange.
	legacy := messaging.NewDeclarations()
	legacy.RegisterExchange(&messaging.ExchangeDeclaration{
		Name:    "product-events",
		Type:    "topic",
		Durable: true,
	})
	want := legacy.Exchanges["product-events"]

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stored declaration = %+v, want the pre-helper shape %+v", *got, *want)
	}
	if got.Type != messaging.ExchangeTypeTopic || !got.Durable || got.AutoDelete || got.Internal || got.NoWait || got.Passive {
		t.Fatalf("stored declaration = %+v, want a durable, non-auto-delete, non-internal topic", *got)
	}
	if len(got.Args) != 0 {
		t.Fatalf("stored Args = %v, want none", got.Args)
	}
}
