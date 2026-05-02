// Package tokens demonstrates the go-bricks JOSE middleware via a Visa Token
// Services–style POST /tokens endpoint. Both inbound (decrypt + verify) and
// outbound (sign + encrypt) directions are exercised, plus an httpclient
// JOSETransport relay against an in-process peer simulator.
package tokens

import (
	"errors"
	"fmt"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/tokens/handlers"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/tokens/service"
	"github.com/gaborage/go-bricks/app"
	"github.com/gaborage/go-bricks/logger"
	"github.com/gaborage/go-bricks/messaging"
	"github.com/gaborage/go-bricks/server"
)

// Kid names used throughout the module. Centralized so the module + cmd/seal-payload
// stay in lockstep without stringly-typed drift.
const (
	OurKid  = "tokens-our"
	PeerKid = "tokens-peer"
)

// Module wires the partner-facing /tokens route, the in-process peer simulator,
// and the relay endpoint that exercises httpclient.JOSETransport.
type Module struct {
	handler      *handlers.Handler
	relayHandler *handlers.RelayHandler
	logger       logger.Logger
}

// NewModule returns an unwired Module. Init populates dependencies.
func NewModule() *Module {
	return &Module{}
}

// Name implements app.Module.
func (m *Module) Name() string { return "tokens" }

// Init wires the tokenization service, the JOSE-tagged handlers, and the
// outbound relay service. The keystore module MUST be registered before this
// one — that's what populates deps.KeyStore and the JOSE resolver.
func (m *Module) Init(deps *app.ModuleDeps) error {
	m.logger = deps.Logger.WithFields(map[string]any{"module": "tokens"})

	if deps.KeyStore == nil {
		return errors.New("tokens module requires a registered keystore module")
	}

	tokenSvc := service.NewTokenizationService()
	m.handler = handlers.NewHandler(tokenSvc, m.logger)

	relaySvc, err := service.NewRelayService(&service.RelayConfig{
		PartnerURL: peerSimulatorURL,
		KeyStore:   deps.KeyStore,
		SignKid:    OurKid,
		EncryptKid: PeerKid,
		VerifyKid:  PeerKid,
		DecryptKid: OurKid,
		Logger:     m.logger,
	})
	if err != nil {
		return fmt.Errorf("init relay service: %w", err)
	}
	m.relayHandler = handlers.NewRelayHandler(relaySvc)

	m.logger.Info().
		Str("partner_url", peerSimulatorURL).
		Msg("tokens module initialized — JOSE-protected /tokens + relay + peer simulator")
	return nil
}

// RegisterRoutes attaches the partner route, the relay route, and the
// peer simulator. All three live under the same /api/v1 base group; the
// simulator path is prefixed with /__sim/ to make its demo-only nature obvious.
func (m *Module) RegisterRoutes(hr *server.HandlerRegistry, r server.RouteRegistrar) {
	m.handler.RegisterPartnerRoute(hr, r)
	m.handler.RegisterSimulatorRoute(hr, r)
	m.relayHandler.RegisterRoute(hr, r)
}

// DeclareMessaging is a no-op — the module only speaks HTTP.
func (m *Module) DeclareMessaging(_ *messaging.Declarations) {}

// RegisterJobs is a no-op — the module owns no scheduled work.
func (m *Module) RegisterJobs(_ app.JobRegistrar) error { return nil }

// Shutdown is a no-op — nothing the runtime owns needs explicit teardown.
func (m *Module) Shutdown() error { return nil }

// peerSimulatorURL is the absolute URL the relay service POSTs to. The simulator
// runs inside this same process under /api/v1/__sim/peer/tokens — but the
// outbound httpclient is a fully external caller from the loopback's
// perspective, so the URL must be absolute. Demo-only.
const peerSimulatorURL = "http://localhost:8080/api/v1/__sim/peer/tokens"
