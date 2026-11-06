// Package partnerportal demonstrates the HTTP tenant features go-bricks offers only
// with multitenant enabled: a tenant-owned route family (server.TenantOwnedGroup,
// ADR-164) whose tenant comes from a JWT claim, the tenant seat's header and path
// resolution, and the path-provenance existence check (ADR-170).
//
// The demo's main binary (cmd/api) is single-tenant and does not load this module.
// cmd/tenant-family-demo does, from its own multi-tenant config. With multitenant
// disabled the module initializes nothing and registers no route, because
// TenantOwnedGroup panics on a server that installed no tenant seat.
package partnerportal

import (
	"fmt"

	"github.com/gaborage/go-bricks/app"
	"github.com/gaborage/go-bricks/auth"
	authtesting "github.com/gaborage/go-bricks/auth/testing"
	"github.com/gaborage/go-bricks/config"
	"github.com/gaborage/go-bricks/logger"
	"github.com/gaborage/go-bricks/multitenant"
	"github.com/gaborage/go-bricks/server"
)

// Route paths, relative to server.path.base.
const (
	// FamilyPrefix is the tenant-owned family. Every route at or below it must be
	// registered through the family; anything else there fails startup (trespass).
	FamilyPrefix = "/partner/v2"
	// FamilyWhoamiPath is the family's one route, relative to FamilyPrefix.
	FamilyWhoamiPath = "/whoami"
	// HeaderWhoamiPath sits behind the tenant seat; its tenant comes from X-Tenant-ID.
	HeaderWhoamiPath = "/tenants/whoami"
	// PathWhoamiPath sits behind the tenant seat; its tenant comes from the path.
	PathWhoamiPath = "/t/:tenant/whoami"
	// TokenSimulatorPath mints JWTs with the in-process issuer (development only).
	TokenSimulatorPath = "/__sim/partner-token" //nolint:gosec // G101: a route path, not a credential
	// TenantClaimName is the JWT claim the family reads its tenant from.
	TenantClaimName = "tenant"
)

// Values of WhoamiResponse.Source: which input the route resolves its tenant from.
const (
	SourceClaim  = "claim"
	SourceHeader = "header"
	SourcePath   = "path"
)

// Module serves the partner portal routes. Construct it with NewModule.
type Module struct {
	logger logger.Logger
	// enabled is deps.Config.Multitenant.Enabled. Off, every other field stays nil.
	enabled bool
	// development gates the token simulator: it mints credentials the family trusts.
	development bool
	// issuer signs the simulator's tokens with an RSA key generated at Init. The key
	// lives only in this process, so a restart invalidates every minted token.
	issuer *authtesting.Issuer
	// verifier trusts exactly the issuer's public key.
	verifier *auth.Verifier
	// tenants answers whether the deployment holds a tenant id.
	tenants multitenant.TenantChecker
}

// NewModule returns an uninitialized partner portal module.
func NewModule() *Module {
	return &Module{}
}

// Name implements app.Module.
func (m *Module) Name() string {
	return "partnerportal"
}

// Init implements app.Module. With multitenant disabled it records that and returns,
// so a single-tenant app can register the module and serve nothing from it.
func (m *Module) Init(deps *app.ModuleDeps) error {
	m.logger = deps.Logger
	if !deps.Config.Multitenant.Enabled {
		m.logger.Info().Msg("partnerportal: multitenant disabled, registering no routes")
		return nil
	}
	m.enabled = true
	m.development = deps.Config.App.IsDevelopment()

	// config.NewTenantStore builds the same config-backed store the framework builds
	// from deps.Config, so HasTenant here answers as the tenant seat's ADR-170 check
	// does. ModuleDeps carries no tenant store, and this demo's tenants are static.
	m.tenants = config.NewTenantStore(deps.Config)

	m.issuer = authtesting.NewIssuer()
	verifier, err := auth.NewVerifierWithResolver(auth.Config(deps.Config.Auth.JWT), deps.Logger,
		auth.NewStaticKeyResolver(m.issuer.PublicKeys()))
	if err != nil {
		return fmt.Errorf("partnerportal: build JWT verifier: %w", err)
	}
	m.verifier = verifier
	return nil
}

// RegisterRoutes implements app.RouteRegisterer. It registers nothing with
// multitenant disabled.
func (m *Module) RegisterRoutes(hr *server.HandlerRegistry, r server.RouteRegistrar) {
	if !m.enabled {
		return
	}

	// Behind the tenant seat (the app-wide tenant middleware): the composite resolver
	// reads the path first, then X-Tenant-ID.
	server.GET(hr, r, HeaderWhoamiPath, whoami(SourceHeader))
	server.GET(hr, r, PathWhoamiPath, whoami(SourcePath))

	// The simulator sits outside the family, so the seat resolves its tenant from
	// X-Tenant-ID like any header route. The tenant in the request body is only the
	// claim to mint.
	if m.development {
		server.POST(hr, r, TokenSimulatorPath, m.mintToken, server.WithTags("simulator"))
	}

	// The family skips the seat. Its policy runs on each of its routes instead.
	family := server.TenantOwnedGroup(r, FamilyPrefix, m.familyPolicy())
	server.GET(hr, family, FamilyWhoamiPath, whoami(SourceClaim))
}

// familyPolicy authenticates the bearer token, then takes the tenant from its claim.
// Deny is left nil, so DenyBodyless answers every client fault: the verifier's 401
// keeps its WWW-Authenticate header, and any other fault is a bodyless 403.
func (m *Module) familyPolicy() server.TenantPolicy {
	return server.TenantPolicy{
		Authenticate: []server.MiddlewareFunc{auth.Middleware(m.verifier)},
		Tenant:       heldTenantClaim(TenantClaimName, m.tenants),
	}
}

// Shutdown implements app.Module.
func (m *Module) Shutdown() error {
	if m.verifier == nil {
		return nil
	}
	return m.verifier.Close()
}
