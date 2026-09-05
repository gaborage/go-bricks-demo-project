package activity

import (
	"context"
	"testing"
	"time"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/activity/domain"
	productservice "github.com/gaborage/go-bricks-demo-project/internal/modules/products/service"
	"github.com/gaborage/go-bricks/app"
	"github.com/gaborage/go-bricks/config"
	"github.com/gaborage/go-bricks/logger"
)

func newTestDeps() *app.ModuleDeps {
	return &app.ModuleDeps{
		Logger: logger.New("error", false),
		Config: &config.Config{App: config.AppConfig{Name: "test", Version: "1.0.0", Env: "test"}},
	}
}

// The seam's failure mode is a typed nil: registerModules skips Init for a
// disabled module, so m.service stays nil, and returning that *ActivityService
// through an interface-typed variable would produce a NON-nil interface holding a
// nil pointer — sailing straight past the products service's `s.activity == nil`
// guard and panicking on the first product write.
//
// This is the cleaner of the two places to prove it. The alternative — teaching
// the products guard to detect a typed nil — would need reflection on the request
// path to compensate for a mistake that only the wiring side can make, and it
// would still leave every other consumer of Recorder() holding the same trap.
// Returning the interface with an explicit nil fixes it at the source, so the
// consumer's guard stays an ordinary `== nil`.
func TestRecorderOnAnUninitializedModuleIsANilInterface(t *testing.T) {
	m := NewModule()

	// Recorder() is declared as the interface, so this comparison is exactly the
	// one the products service performs on `s.activity` — the comparison a typed
	// nil would pass while still panicking on the first product write.
	if got := m.Recorder(); got != nil {
		t.Fatalf("Recorder() before Init = %#v, want an untyped nil interface", got)
	}
}

func TestRecorderAfterInitIsUsable(t *testing.T) {
	m := NewModule()
	if err := m.Init(newTestDeps()); err != nil {
		t.Fatalf("Init: %v", err)
	}

	seam := m.Recorder()
	if seam == nil {
		t.Fatal("Recorder() after Init = nil, want the products seam")
	}

	// No publisher is bound (DeclareStreams has not run), so the recorder logs and
	// swallows. It must not panic: it is called inline on the product write path.
	seam.RecordActivity(context.Background(), productservice.ProductActivity{
		ProductID:  "product-a",
		Action:     productservice.ActivityCreated,
		Name:       "Widget",
		Price:      9.99,
		OccurredAt: time.Now().UTC(),
	})
}

// The adapter copies the action verbatim, so the two sides' constants have to be
// the same strings — domain.ProductActivity's `oneof` validate tag rejects the
// event on the consumer boundary if they ever drift.
func TestSeamActionsMatchTheStreamWireFormat(t *testing.T) {
	pairs := [][2]string{
		{productservice.ActivityCreated, domain.ActionCreated},
		{productservice.ActivityUpdated, domain.ActionUpdated},
		{productservice.ActivityDeleted, domain.ActionDeleted},
	}
	for _, pair := range pairs {
		if pair[0] != pair[1] {
			t.Errorf("products action %q != stream action %q", pair[0], pair[1])
		}
	}
}
