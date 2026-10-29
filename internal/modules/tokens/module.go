// Package tokens demonstrates the go-bricks JOSE middleware via a Visa Token
// Services–style POST /tokens endpoint. Both inbound (decrypt + verify) and
// outbound (sign + encrypt) directions are exercised, plus httpclient
// JOSETransport relays in all three seal modes (nested JWE-of-JWS, Visa MLE
// bare JWE, VTS Issuer JWS-of-JWE) against in-process peer simulators, and a
// detached-signature gateway that serves a kid-less peer (minimal JWS-of-JWE,
// go-bricks ADR-150) called by its own in-process simulator.
package tokens

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/tokens/handlers"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/tokens/service"
	"github.com/gaborage/go-bricks/app"
	"github.com/gaborage/go-bricks/config"
	"github.com/gaborage/go-bricks/logger"
	"github.com/gaborage/go-bricks/messaging"
	"github.com/gaborage/go-bricks/server"
)

// Kid names used throughout the module. Centralized so the module stays free of
// stringly-typed drift. scripts/seal-payload.sh (make seal-payload / seal-mle)
// repeats them for the framework seal-payload CLI and must follow any rename.
const (
	OurKid  = "tokens.our"
	PeerKid = "tokens.peer"
)

// Keystore entries of the detached-signature gateway. They are dedicated: the
// gateway's decrypt key must back no other route, and no body of the other three
// modes is encrypted to or signed by them (key separation, go-bricks ADR-150).
// The peer sends no kid, so these name resolver keys, never wire headers.
// scripts/detached-envelope-demo.sh repeats them and the DER file names.
const (
	DetachedOurKid  = "tokens.detached.our"
	DetachedPeerKid = "tokens.detached.peer"
)

// Module wires the partner-facing /tokens route, the in-process peer simulator,
// and the relay endpoint that exercises httpclient.JOSETransport.
type Module struct {
	handler         *handlers.Handler
	relayHandler    *handlers.RelayHandler
	mleHandler      *handlers.MLEHandler
	vtsHandler      *handlers.RelayHandler
	detachedHandler *handlers.DetachedHandler
	logger          logger.Logger

	// Absolute URLs of the in-process peer simulators, and of the gateway the
	// detached simulator calls, built in Init from deps.Config.Server so every
	// loopback call follows the listener's port and base path.
	peerSimulatorURL    string
	mlePeerSimulatorURL string
	detachedGatewayURL  string
}

var _ app.MessagingDeclarer = (*Module)(nil)

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
	// First, so a deploy that lacks the dedicated entries fails with this message
	// rather than with whatever the first request would have hit.
	if err := requireDetachedKeys(deps.KeyStore); err != nil {
		return err
	}

	selfURL, err := selfBaseURL(deps.Config)
	if err != nil {
		return err
	}
	m.peerSimulatorURL = selfURL + handlers.PeerSimulatorPath
	m.mlePeerSimulatorURL = selfURL + handlers.MLEPeerSimulatorPath
	m.detachedGatewayURL = selfURL + handlers.DetachedGatewayPath

	tokenSvc := service.NewTokenizationService()
	m.handler = handlers.NewHandler(tokenSvc, m.logger)

	relaySvc, err := service.NewRelayService(&service.RelayConfig{
		PartnerURL: m.peerSimulatorURL,
		KeyStore:   deps.KeyStore,
		SignKid:    OurKid,
		EncryptKid: PeerKid,
		VerifyKid:  PeerKid,
		DecryptKid: OurKid,
		PeerName:   peerSimulatorName,
		Logger:     m.logger,
	})
	if err != nil {
		return fmt.Errorf("init relay service: %w", err)
	}
	m.relayHandler = handlers.NewRelayHandler(relaySvc, m.logger)

	if err := m.initMLE(deps); err != nil {
		return err
	}
	if err := m.initVTSIssuer(deps); err != nil {
		return err
	}
	if err := m.initDetached(deps); err != nil {
		return err
	}

	m.logger.Info().
		Str("partner_url", m.peerSimulatorURL).
		Str("mle_partner_url", m.mlePeerSimulatorURL).
		Str("vts_issuer_partner_url", vtsIssuerPeerURL).
		Str("detached_gateway_url", m.detachedGatewayURL).
		Msg("tokens module initialized — JOSE-protected /tokens + relay + MLE relay + VTS Issuer relay + detached gateway + peer simulators")
	return nil
}

// initMLE wires the Visa Message Level Encryption half of the module: the
// bare-JWE relay and the counterparty it calls. Both reuse the SAME keypairs as
// the nested demo — the kid names an identity, not a wire shape.
func (m *Module) initMLE(deps *app.ModuleDeps) error {
	mleRelay, err := service.NewMLERelayService(&service.MLERelayConfig{
		PartnerURL: m.mlePeerSimulatorURL,
		KeyStore:   deps.KeyStore,
		EncryptKid: PeerKid, // bare outbound declares an encrypt kid and nothing else
		DecryptKid: OurKid,  // bare inbound declares a decrypt kid and nothing else
		PeerName:   mlePeerSimulatorName,
		Logger:     m.logger,
	})
	if err != nil {
		return fmt.Errorf("init MLE relay service: %w", err)
	}

	// Inverse identities: the simulator decrypts with the peer key and encrypts
	// back to ours.
	mlePeer, err := service.NewMLEPeerSimulator(&service.MLEPeerConfig{
		KeyStore:   deps.KeyStore,
		DecryptKid: PeerKid,
		EncryptKid: OurKid,
	})
	if err != nil {
		return fmt.Errorf("init MLE peer simulator: %w", err)
	}

	m.mleHandler = handlers.NewMLEHandler(mleRelay, mlePeer, m.logger)
	return nil
}

// initVTSIssuer wires the Visa Token Service Issuer half of the module: the
// JWS-of-JWE relay and its counterparty. The kids are the same four as the
// nested relay, because this mode signs and encrypts in both directions.
//
// The counterparty is handed to the relay as its base transport rather than
// served under /__sim/: the Issuer wire is a bare compact on application/jose,
// which only an untagged raw route could answer (see
// service.VTSIssuerPeerSimulator). The relay's JOSE wiring is unchanged by that;
// production passes its mTLS transport in the same slot.
func (m *Module) initVTSIssuer(deps *app.ModuleDeps) error {
	// Inverse identities: the simulator verifies our signature and decrypts with
	// the peer key, then encrypts to us and signs with the peer key.
	vtsPeer, err := service.NewVTSIssuerPeerSimulator(&service.VTSIssuerPeerConfig{
		KeyStore:   deps.KeyStore,
		DecryptKid: PeerKid,
		VerifyKid:  OurKid,
		SignKid:    PeerKid,
		EncryptKid: OurKid,
		Logger:     m.logger,
	})
	if err != nil {
		return fmt.Errorf("init VTS issuer peer simulator: %w", err)
	}

	vtsRelay, err := service.NewVTSIssuerRelayService(&service.VTSIssuerRelayConfig{
		PartnerURL: vtsIssuerPeerURL,
		KeyStore:   deps.KeyStore,
		SignKid:    OurKid,
		EncryptKid: PeerKid,
		VerifyKid:  PeerKid,
		DecryptKid: OurKid,
		PeerName:   vtsIssuerPeerName,
		Transport:  vtsPeer,
		Logger:     m.logger,
	})
	if err != nil {
		return fmt.Errorf("init VTS issuer relay service: %w", err)
	}

	m.vtsHandler = handlers.NewVTSIssuerRelayHandler(vtsRelay, m.logger)
	return nil
}

// initDetached wires the detached-signature gateway and the kid-less peer that
// calls it. The roles invert relative to the relays: the gateway is the demo's
// real endpoint, and the simulator plays the partner CLIENT, calling the gateway
// over loopback HTTP.
//
// Both use the two dedicated entries, never tokens.our/tokens.peer: the gateway
// decrypts with tokens.detached.our and verifies with tokens.detached.peer; the
// simulator encrypts to the first and signs with the second; the reply is sealed
// to tokens.detached.peer. A real gateway holds only our private half and the
// peer's public half; all four are present here because the simulator runs
// in-process.
func (m *Module) initDetached(deps *app.ModuleDeps) error {
	gateway, err := service.NewDetachedGateway(&service.DetachedGatewayConfig{
		KeyStore:   deps.KeyStore,
		DecryptKid: DetachedOurKid,
		VerifyKid:  DetachedPeerKid,
		ReplyKid:   DetachedPeerKid,
	})
	if err != nil {
		return fmt.Errorf("init detached gateway: %w", err)
	}

	peer, err := service.NewDetachedPeerSimulator(&service.DetachedPeerConfig{
		GatewayURL:      m.detachedGatewayURL,
		KeyStore:        deps.KeyStore,
		EncryptKid:      DetachedOurKid,
		SignKid:         DetachedPeerKid,
		DecryptKid:      DetachedPeerKid,
		SignatureHeader: handlers.DetachedSignatureHeader,
		PeerName:        detachedGatewayName,
		Logger:          m.logger,
	})
	if err != nil {
		return fmt.Errorf("init detached peer simulator: %w", err)
	}

	m.detachedHandler = handlers.NewDetachedHandler(gateway, peer, m.logger)
	return nil
}

// requireDetachedKeys refuses startup unless both halves of both dedicated
// entries resolve. The gateway and its simulator use all four; failing here
// names every missing half and the variable a deployed environment sets for it,
// instead of letting the boot pass and the gateway answer 500 later.
func requireDetachedKeys(ks app.KeyStore) error {
	privateHalf := func(name string) error { _, err := ks.PrivateKey(name); return err }
	publicHalf := func(name string) error { _, err := ks.PublicKey(name); return err }

	var missing []error
	for _, kid := range []string{DetachedOurKid, DetachedPeerKid} {
		for _, half := range []struct {
			name   string
			lookup func(string) error
		}{{"private", privateHalf}, {"public", publicHalf}} {
			if err := half.lookup(kid); err != nil {
				missing = append(missing, fmt.Errorf("entry %q has no usable %s half (set %s): %w",
					kid, half.name, keystoreValueEnv(kid, half.name), err))
			}
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("tokens module: the detached-signature gateway requires keystore entries %s and %s, both halves each; "+
		"provide them as base64 DER in a deployed environment, or run `make generate-keys` in development: %w",
		DetachedOurKid, DetachedPeerKid, errors.Join(missing...))
}

// keystoreValueEnv is the environment variable that delivers one half of a
// keystore entry as base64 DER: the config path upper-cased, dots to underscores.
func keystoreValueEnv(kid, half string) string {
	return "KEYSTORE_KEYS_" + strings.ToUpper(strings.ReplaceAll(kid, ".", "_")) + "_" + strings.ToUpper(half) + "_VALUE"
}

// RegisterRoutes attaches the partner route, the three relay routes, the
// detached-signature gateway, and the three HTTP peer simulators. All live under
// the same /api/v1 base group; the simulator paths are prefixed with /__sim/ to
// make their demo-only nature obvious. The VTS Issuer counterparty has no route
// (see initVTSIssuer).
func (m *Module) RegisterRoutes(hr *server.HandlerRegistry, r server.RouteRegistrar) {
	m.handler.RegisterPartnerRoute(hr, r)
	m.handler.RegisterSimulatorRoute(hr, r)
	m.relayHandler.RegisterRoute(hr, r)
	m.mleHandler.RegisterRoutes(hr, r)
	m.vtsHandler.RegisterRoute(hr, r)
	m.detachedHandler.RegisterRoutes(hr, r)
}

// DeclareMessaging is a no-op — the module only speaks HTTP.
func (m *Module) DeclareMessaging(_ *messaging.Declarations) {
	// messaging not needed
}

// RegisterJobs is a no-op — the module owns no scheduled work.
func (m *Module) RegisterJobs(_ app.JobRegistrar) error { return nil }

// Shutdown is a no-op — nothing the runtime owns needs explicit teardown.
func (m *Module) Shutdown() error { return nil }

// selfBaseURL returns the absolute URL of this process's own route root: the
// scheme, host and port the server listens on, plus server.path.base. The nested
// and MLE relays POST to peer simulators that run inside this same process, but
// the outbound httpclient is an external caller from the loopback's perspective,
// so their URLs must be absolute. They used to hardcode localhost:8080, so a boot
// on any other port (SERVER_PORT) sent both relays to the wrong listener.
//
// A wildcard or empty server.host is dialed as localhost. server.port must be a
// real port: config validation refuses 0 at boot, and a relay cannot address an
// ephemeral port that is only known after the listener binds. Demo-only.
func selfBaseURL(cfg *config.Config) (string, error) {
	if cfg == nil {
		return "", errors.New("tokens module requires deps.Config to address its in-process peer simulators")
	}
	srv := cfg.Server
	if srv.Port < 1 || srv.Port > 65535 {
		return "", fmt.Errorf("tokens module: server.port %d cannot address the in-process peer simulators", srv.Port)
	}

	host := strings.Trim(srv.Host, "[]")
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "localhost"
	}
	scheme := "http"
	if srv.TLS.Enabled {
		scheme = "https"
	}

	u := url.URL{
		Scheme: scheme,
		Host:   net.JoinHostPort(host, strconv.Itoa(srv.Port)),
		Path:   normalizeBasePath(srv.Path.Base),
	}
	return u.String(), nil
}

// normalizeBasePath mirrors how the go-bricks server mounts server.path.base:
// a leading slash is added, trailing slashes are dropped, and "" or "/" means
// routes sit at the root.
func normalizeBasePath(base string) string {
	base = strings.TrimRight(base, "/")
	if base == "" {
		return ""
	}
	if !strings.HasPrefix(base, "/") {
		base = "/" + base
	}
	return base
}

// vtsIssuerPeerURL is what the VTS Issuer relay addresses. No socket is ever
// opened for it: the in-process simulator is the client's base transport, so
// the URL only supplies the request line and the server.address metric label.
// The .invalid TLD (RFC 6761) never resolves, so a client that lost that
// transport fails at DNS instead of reaching a real host. Demo-only.
const vtsIssuerPeerURL = "http://vts-issuer-peer-sim.invalid/tokens"

// Peer names for the relay clients (httpclient.Builder.WithPeerName, go-bricks
// v0.65.0 #1648). Each one labels its client's outbound metrics with a
// low-cardinality partner name, and names the partner when the transport refuses
// a plaintext 2xx. One name per counterparty: the two simulator routes and the
// VTS Issuer transport. detachedGatewayName labels the other direction: the
// detached simulator's plain client calling the gateway.
const (
	peerSimulatorName    = "tokens-peer-sim"
	mlePeerSimulatorName = "visa-mle-peer-sim"
	vtsIssuerPeerName    = "visa-vts-issuer-peer-sim"
	detachedGatewayName  = "tokens-detached-gateway"
)
