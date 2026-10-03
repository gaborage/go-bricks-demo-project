package webhooks

import (
	"testing"

	"github.com/gaborage/go-bricks/app"
	"github.com/gaborage/go-bricks/config"
	"github.com/gaborage/go-bricks/logger"
	"github.com/gaborage/go-bricks/messaging"
	"github.com/gaborage/go-bricks/server"
)

type addedRoute struct {
	method string
	path   string
}

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

func (f *fakeRouteRegistrar) FullPath(path string) string {
	return path
}

func newTestModuleDeps() *app.ModuleDeps {
	return &app.ModuleDeps{
		Logger: logger.New("info", false),
	}
}

func TestNewModule(t *testing.T) {
	m := NewModule()
	if m == nil {
		t.Fatal("NewModule() returned nil")
	}
}

func TestModuleName(t *testing.T) {
	m := NewModule()
	if got := m.Name(); got != "webhooks" {
		t.Errorf("Name() = %q, want %q", got, "webhooks")
	}
}

func TestModuleShutdown(t *testing.T) {
	m := NewModule()
	if err := m.Shutdown(); err != nil {
		t.Errorf("Shutdown() = %v, want nil", err)
	}
}

func TestModuleDeclareMessaging(t *testing.T) {
	m := NewModule()
	decls := messaging.NewDeclarations()

	m.DeclareMessaging(decls)

	if len(decls.Exchanges) != 0 {
		t.Errorf("DeclareMessaging() registered %d exchanges, want 0", len(decls.Exchanges))
	}
}

func TestModuleRegisterJobs(t *testing.T) {
	m := NewModule()
	if err := m.RegisterJobs(nil); err != nil {
		t.Errorf("RegisterJobs() error = %v, want nil", err)
	}
}

func TestModuleInit(t *testing.T) {
	m := NewModule()
	deps := newTestModuleDeps()

	if err := m.Init(deps); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if m.handler == nil {
		t.Error("Init() did not wire handler")
	}
}

func TestModuleRegisterRoutes(t *testing.T) {
	m := NewModule()
	deps := newTestModuleDeps()
	if err := m.Init(deps); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	cfg := &config.Config{}
	hr := server.NewHandlerRegistry(cfg)
	r := &fakeRouteRegistrar{}
	t.Cleanup(server.DefaultRouteRegistry.Clear)

	m.RegisterRoutes(hr, r)

	want := []addedRoute{
		{method: "POST", path: "/webhooks/sign"},
		{method: "POST", path: "/webhooks/verify"},
	}
	if len(r.added) != len(want) {
		t.Fatalf("RegisterRoutes() added %d routes, want %d: %+v", len(r.added), len(want), r.added)
	}
	for i, w := range want {
		if r.added[i] != w {
			t.Errorf("route[%d] = %+v, want %+v", i, r.added[i], w)
		}
	}
}
