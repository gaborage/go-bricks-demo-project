package products

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/products/job"
	"github.com/gaborage/go-bricks/app"
	"github.com/gaborage/go-bricks/config"
	"github.com/gaborage/go-bricks/database"
	"github.com/gaborage/go-bricks/logger"
	"github.com/gaborage/go-bricks/messaging"
	"github.com/gaborage/go-bricks/server"
)

type addedRoute struct {
	method string
	path   string
}

// fakeRouteRegistrar records the routes a module adds, in order.
type fakeRouteRegistrar struct {
	added []addedRoute
}

func (f *fakeRouteRegistrar) Add(method, path string, _ server.Handler, _ ...server.MiddlewareFunc) {
	f.added = append(f.added, addedRoute{method: method, path: path})
}

func (f *fakeRouteRegistrar) Group(_ string, _ ...server.MiddlewareFunc) server.RouteRegistrar {
	return f
}

func (f *fakeRouteRegistrar) Use(_ ...server.MiddlewareFunc) {}

func (f *fakeRouteRegistrar) FullPath(path string) string { return path }

type registeredJob struct {
	id       string
	job      any
	interval time.Duration
}

// fakeJobRegistrar records FixedRate registrations; the module uses no other
// schedule.
type fakeJobRegistrar struct {
	fixedRate []registeredJob
}

func (f *fakeJobRegistrar) FixedRate(jobID string, j any, interval time.Duration) error {
	f.fixedRate = append(f.fixedRate, registeredJob{id: jobID, job: j, interval: interval})
	return nil
}

func (f *fakeJobRegistrar) DailyAt(string, any, time.Time) error { return nil }

func (f *fakeJobRegistrar) WeeklyAt(string, any, time.Weekday, time.Time) error { return nil }

func (f *fakeJobRegistrar) HourlyAt(string, any, int) error { return nil }

func (f *fakeJobRegistrar) MonthlyAt(string, any, int, time.Time) error { return nil }

var _ app.JobRegistrar = (*fakeJobRegistrar)(nil)

// newTestModuleDeps returns the deps Init reads. The DB and messaging getters
// are never called during Init: it only stores them.
func newTestModuleDeps(cfg *config.Config) *app.ModuleDeps {
	return &app.ModuleDeps{
		Logger:    logger.New("disabled", false),
		Config:    cfg,
		DB:        func(context.Context) (database.Interface, error) { return nil, nil },
		Messaging: func(context.Context) (messaging.AMQPClient, error) { return nil, nil },
	}
}

func TestModuleName(t *testing.T) {
	if got := NewModule().Name(); got != "products" {
		t.Errorf("Name() = %q, want %q", got, "products")
	}
}

func TestModuleShutdown(t *testing.T) {
	if err := NewModule().Shutdown(); err != nil {
		t.Errorf("Shutdown() = %v, want nil", err)
	}
}

func TestModuleInit(t *testing.T) {
	t.Run("wires the service and handler without config", func(t *testing.T) {
		m := NewModule()
		if err := m.Init(newTestModuleDeps(nil)); err != nil {
			t.Fatalf("Init() error = %v", err)
		}
		if m.service == nil || m.handler == nil {
			t.Errorf("Init() service = %v, handler = %v; want both wired", m.service, m.handler)
		}
		if m.reportHold != 0 {
			t.Errorf("reportHold = %v, want 0", m.reportHold)
		}
	})

	t.Run("reads the report hold from config", func(t *testing.T) {
		cfg, err := config.LoadFromMap(map[string]any{reportHoldKey: "6s"})
		if err != nil {
			t.Fatalf("LoadFromMap() error = %v", err)
		}
		m := NewModule()
		if err := m.Init(newTestModuleDeps(cfg)); err != nil {
			t.Fatalf("Init() error = %v", err)
		}
		if m.reportHold != 6*time.Second {
			t.Errorf("reportHold = %v, want 6s", m.reportHold)
		}
	})

	t.Run("refuses a malformed report hold", func(t *testing.T) {
		cfg, err := config.LoadFromMap(map[string]any{reportHoldKey: "soon"})
		if err != nil {
			t.Fatalf("LoadFromMap() error = %v", err)
		}
		if err := NewModule().Init(newTestModuleDeps(cfg)); err == nil || !strings.Contains(err.Error(), reportHoldKey) {
			t.Errorf("Init() error = %v, want one naming %s", err, reportHoldKey)
		}
	})
}

func TestModuleRegisterJobs(t *testing.T) {
	m := NewModule()
	m.reportHold = 6 * time.Second
	reg := &fakeJobRegistrar{}

	if err := m.RegisterJobs(reg); err != nil {
		t.Fatalf("RegisterJobs() error = %v", err)
	}
	if len(reg.fixedRate) != 1 {
		t.Fatalf("RegisterJobs() registered %d FixedRate jobs, want 1", len(reg.fixedRate))
	}

	got := reg.fixedRate[0]
	if got.id != "test-job" || got.interval != 30*time.Second {
		t.Errorf("job = %q every %v, want %q every 30s", got.id, got.interval, "test-job")
	}
	reportJob, ok := got.job.(*job.ReportJob)
	if !ok {
		t.Fatalf("job type = %T, want *job.ReportJob", got.job)
	}
	if reportJob.Hold != 6*time.Second {
		t.Errorf("job Hold = %v, want the module's report hold (6s)", reportJob.Hold)
	}
}

func TestModuleRegisterRoutes(t *testing.T) {
	m := NewModule()
	if err := m.Init(newTestModuleDeps(nil)); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	r := &fakeRouteRegistrar{}
	t.Cleanup(server.DefaultRouteRegistry.Clear)

	m.RegisterRoutes(server.NewHandlerRegistry(&config.Config{}), r)

	const productByID = "/products/:id"
	want := []addedRoute{
		{method: http.MethodGet, path: productByID},
		{method: http.MethodGet, path: "/products"},
		{method: http.MethodPost, path: "/products"},
		{method: http.MethodPut, path: productByID},
		{method: http.MethodDelete, path: productByID},
	}
	if !reflect.DeepEqual(r.added, want) {
		t.Errorf("RegisterRoutes() added %+v, want %+v", r.added, want)
	}
}

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
