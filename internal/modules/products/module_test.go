package products

import (
	"reflect"
	"strings"
	"testing"
	"time"

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

func TestParseReportHold(t *testing.T) {
	tests := []struct {
		raw     string
		want    time.Duration
		wantErr bool
	}{
		{raw: "", want: 0},
		{raw: "0s", want: 0},
		{raw: "6s", want: 6 * time.Second},
		{raw: "1m30s", want: 90 * time.Second},
		{raw: "6", wantErr: true}, // a bare number has no unit
		{raw: "soon", wantErr: true},
		{raw: "-1s", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			got, err := parseReportHold(tt.raw)
			if tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), reportHoldKey) {
					t.Fatalf("parseReportHold(%q) error = %v, want one naming %s", tt.raw, err, reportHoldKey)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("parseReportHold(%q) = %v, %v; want %v, nil", tt.raw, got, err, tt.want)
			}
		})
	}
}

func TestReportHoldWithoutConfigIsZero(t *testing.T) {
	got, err := reportHold(nil)
	if err != nil || got != 0 {
		t.Fatalf("reportHold(nil) = %v, %v; want 0, nil", got, err)
	}
}
