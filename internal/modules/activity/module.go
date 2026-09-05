// Package activity demonstrates the go-bricks RabbitMQ **streams** lane (native
// stream protocol, port 5552) on a partitioned super stream.
//
// The module is deliberately both producer and consumer so the demo is
// self-contained: the products module hands it a ProductActivity after every
// successful write, it publishes that onto the product-activity super stream
// keyed by product id, a typed super-stream consumer projects every partition
// back into memory, and GET /api/v1/products/activity renders the projection.
// POST /api/v1/__sim/streams/poison puts a body on a partition that the typed
// consumer cannot decode, to show what the framework does with poison.
//
// This lane is a SHOWCASE, not the reliable path. Product lifecycle events still
// go through the transactional outbox (products/service), which commits the row
// and the event in one database transaction; a stream publish that fails is
// logged and swallowed.
package activity

import (
	"context"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/activity/domain"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/activity/handlers"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/activity/service"
	productservice "github.com/gaborage/go-bricks-demo-project/internal/modules/products/service"
	"github.com/gaborage/go-bricks/app"
	"github.com/gaborage/go-bricks/logger"
	"github.com/gaborage/go-bricks/messaging"

	// Import gate for the stream lane (ADR-091): importing this package — a blank
	// import would do just as well — is what registers the runtime with app, so a
	// configured messaging.streams.uri starts a manager. Without it, a URI in the
	// config fails startup with app.ErrStreamsNotLinked, and a process that never
	// imports it carries none of the vendor stream client. The import that
	// declares topology is also the import that links the lane.
	"github.com/gaborage/go-bricks/messaging/streams"
	"github.com/gaborage/go-bricks/server"
)

// Module implements app.Module and streams.StreamDeclarer.
type Module struct {
	logger    logger.Logger
	service   *service.ActivityService
	handler   *handlers.Handler
	publisher *streams.Publisher

	// simEnabled gates the poison simulator to non-production environments. The
	// tokens module's peer simulator relies on its /__sim/ path prefix alone;
	// this one publishes deliberately malformed bytes onto a real stream, so it
	// fails closed instead.
	simEnabled bool
}

// The framework detects the declarer by TYPE ASSERTION on the registered module,
// not through ModuleDeps — see streams.StreamDeclarer and app/streams_setup.go.
// A typo in the method name would therefore be silent: the module would register
// fine and declare nothing. This assertion makes that a compile error.
var _ streams.StreamDeclarer = (*Module)(nil)

// NewModule returns an unwired Module. Init populates dependencies.
func NewModule() *Module {
	return &Module{}
}

// Name implements app.Module.
func (m *Module) Name() string { return "activity" }

// Init wires the projection service and the HTTP handler.
//
// It runs at RegisterModule time — BEFORE DeclareStreams, which the framework
// calls during app.Run(). That ordering is why the publisher handle reaches the
// service through a setter rather than its constructor.
func (m *Module) Init(deps *app.ModuleDeps) error {
	m.logger = deps.Logger.WithFields(map[string]any{"module": "activity"})

	m.service = service.NewActivityService(m.logger)
	m.handler = handlers.NewHandler(m.service, m.logger)
	m.simEnabled = deps.Config != nil && deps.Config.App.IsDevelopment()

	m.logger.Info().
		Str("superStream", domain.SuperStream).
		Int("partitions", domain.PartitionCount).
		Bool("poisonSimulator", m.simEnabled).
		Msg("Activity module initialized — RabbitMQ super-stream projection")
	return nil
}

// productActivityRecorder adapts the products module's seam onto this module's
// own domain type. The CONSUMER owns the contract — both the interface and its
// payload live in internal/modules/products/service — so products (and the legacy
// module that reuses it) compiles in a build carrying no activity module at all.
// The adaptation belongs here, at the module boundary, which keeps
// activity/service speaking only its own vocabulary.
//
// The action strings are copied verbatim: products' ActivityCreated/Updated/
// Deleted and domain's ActionCreated/Updated/Deleted are the same bare verbs, and
// domain.ProductActivity's `oneof` validate tag is what enforces that on the
// consumer boundary if they ever drift.
type productActivityRecorder struct {
	svc *service.ActivityService
}

// The seam is satisfied structurally, so nothing would catch a signature drift at
// the definition site. This assertion does.
var _ productservice.ActivityRecorder = productActivityRecorder{}

//nolint:gocritic // hugeParam: the value signature is the products ActivityRecorder seam.
func (r productActivityRecorder) RecordActivity(ctx context.Context, evt productservice.ProductActivity) {
	r.svc.RecordActivity(ctx, domain.ProductActivity{
		ProductID:  evt.ProductID,
		Action:     evt.Action,
		Name:       evt.Name,
		Price:      evt.Price,
		OccurredAt: evt.OccurredAt,
	})
}

// Recorder returns the seam the products module publishes through, or an explicit
// nil when this module was never initialized — which is exactly what happens when
// it is disabled in getModulesToLoad, since registerModules skips Init for a
// disabled module.
//
// The return type is the INTERFACE and the nil case is spelled out for a reason:
// handing back a typed-nil *ActivityService would arrive at the products service
// as a NON-nil interface holding a nil pointer, sail past its `s.activity == nil`
// guard, and panic on the first product write.
func (m *Module) Recorder() productservice.ActivityRecorder {
	if m.service == nil {
		return nil
	}
	return productActivityRecorder{svc: m.service}
}

// DeclareStreams declares the whole product-activity topology.
//
// The framework calls it during startup, AFTER every module's Init, validates
// every declaration at once, then starts the consumers and binds the publishers.
// A declaration made here with no messaging.streams.uri configured fails startup
// rather than being silently dropped.
func (m *Module) DeclareStreams(decls *streams.Declarations) {
	// Retention is explicit because streams never shrink on their own; see
	// domain.Retention, and the partition-count trap documented beside it.
	decls.DeclareSuperStream(domain.SuperStream, domain.PartitionCount, &streams.StreamSpec{
		MaxAge: domain.Retention,
	})

	// Producer side. One publisher per target per process — a second declaration
	// on the same super stream panics at startup — so this handle is the only way
	// anything in this process reaches the stream, the poison simulator included.
	m.publisher = decls.DeclareSuperStreamPublisher(&streams.SuperStreamPublisherOptions{
		SuperStream: domain.SuperStream,
	})
	m.service.SetPublisher(m.publisher)

	// Consumer side. The typed door decodes the body into ProductActivity and runs
	// its `validate` tags before the handler sees it; a body that fails either is
	// deterministic poison, skipped without committing its offset (ADR-092).
	//
	// There is no SAC field: super-stream consumption is ALWAYS a single active
	// consumer group, because the promotion callback is the only place a
	// per-partition stored offset can be restored (ADR-059). OffsetFirst is the
	// start position used only when the broker holds NO stored offset for
	// ConsumerName on a partition — a stored offset always wins, so a restart
	// resumes rather than replaying everything.
	//
	// WithMeta, not the plain door: the projection records the partition
	// (msg.Stream) and offset (msg.Offset) each delivery arrived on, which is the
	// whole point of demonstrating a partitioned stream.
	streams.DeclareTypedSuperStreamConsumerWithMeta(decls, &streams.SuperStreamConsumerOptions{
		SuperStream: domain.SuperStream,
		Name:        domain.ConsumerName,
		Start:       streams.OffsetFirst(),
	}, m.service.Project)
}

// RegisterRoutes attaches the projection endpoint, and the poison simulator only
// where simEnabled allows it.
func (m *Module) RegisterRoutes(hr *server.HandlerRegistry, r server.RouteRegistrar) {
	m.handler.RegisterRoutes(hr, r)

	if !m.simEnabled {
		m.logger.Info().Msg("Poison simulator not registered — development environments only")
		return
	}
	m.handler.RegisterSimulatorRoute(hr, r)
}

// DeclareMessaging is a no-op: this module speaks the stream protocol, not
// AMQP 0.9.1. The two lanes are declared through different Declarations types.
func (m *Module) DeclareMessaging(_ *messaging.Declarations) {}

// Shutdown is a no-op — the framework stops the consumers and closes the
// publishers itself, before modules are torn down.
func (m *Module) Shutdown() error { return nil }
