package analytics

import (
	"context"
	"testing"

	"github.com/gaborage/go-bricks/app"
	"github.com/gaborage/go-bricks/database"
	"github.com/gaborage/go-bricks/logger"
	"github.com/gaborage/go-bricks/messaging"
)

func TestNewModule(t *testing.T) {
	m := NewModule()
	if m == nil {
		t.Fatal("NewModule() returned nil")
	}
}

func TestModuleName(t *testing.T) {
	m := NewModule()
	if got := m.Name(); got != "analytics" {
		t.Errorf("Name() = %q, want %q", got, "analytics")
	}
}

func TestModuleShutdown(t *testing.T) {
	m := NewModule()
	m.logger = logger.New("info", false)

	if err := m.Shutdown(); err != nil {
		t.Errorf("Shutdown() error = %v, want nil", err)
	}
}

func TestModuleDeclareMessaging(t *testing.T) {
	m := NewModule()
	decls := messaging.NewDeclarations()

	m.DeclareMessaging(decls)

	if len(decls.Exchanges) != 0 {
		t.Errorf("DeclareMessaging() Exchanges = %v, want empty (analytics needs no messaging)", decls.Exchanges)
	}
	if len(decls.Queues) != 0 {
		t.Errorf("DeclareMessaging() Queues = %v, want empty", decls.Queues)
	}
}

func TestModuleRegisterJobs(t *testing.T) {
	m := NewModule()
	if err := m.RegisterJobs(nil); err != nil {
		t.Errorf("RegisterJobs() error = %v, want nil", err)
	}
}

// TestModuleInit exercises Init with a minimal ModuleDeps. ModuleDeps.DBByName is
// just a function field (no live database is required to construct it), so we can
// verify wiring — repository/service/handler creation and the DBByName closure —
// without needing a real Postgres connection or AMQP broker.
func TestModuleInit(t *testing.T) {
	m := NewModule()

	dbByNameCalls := 0
	deps := &app.ModuleDeps{
		Logger: logger.New("info", false),
		DBByName: func(_ context.Context, name string) (database.Interface, error) {
			dbByNameCalls++
			if name != analyticsDBName {
				t.Errorf("DBByName() called with name = %v, want %v", name, analyticsDBName)
			}
			return nil, nil
		},
	}

	if err := m.Init(deps); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	if m.service == nil {
		t.Error("Init() did not initialize service")
	}
	if m.handler == nil {
		t.Error("Init() did not initialize handler")
	}
	if m.repo == nil {
		t.Error("Init() did not initialize repository")
	}
	if m.getAnalyticsDB == nil {
		t.Fatal("Init() did not initialize getAnalyticsDB")
	}

	// Confirm the closure actually delegates to deps.DBByName with the analytics name.
	if _, err := m.getAnalyticsDB(context.Background()); err != nil {
		t.Errorf("getAnalyticsDB() error = %v, want nil", err)
	}
	if dbByNameCalls != 1 {
		t.Errorf("DBByName() called %d times, want 1", dbByNameCalls)
	}
}
