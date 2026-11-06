package partnerportal

import (
	authtesting "github.com/gaborage/go-bricks/auth/testing"
	"github.com/gaborage/go-bricks/multitenant"
	"github.com/gaborage/go-bricks/server"
)

// WhoamiRequest is empty: every whoami route reads its tenant from the context the
// framework published, never from a field the handler binds.
type WhoamiRequest struct{}

// WhoamiResponse echoes the tenant in the request context and the input the route
// resolves it from.
type WhoamiResponse struct {
	Tenant string `json:"tenant"`
	Source string `json:"source"`
}

// whoami returns a handler that echoes multitenant.GetTenant. Every route it serves
// is behind the seat or the family, so a request with no tenant never reaches it.
func whoami(source string) server.HandlerFunc[WhoamiRequest, WhoamiResponse] {
	return func(_ WhoamiRequest, c server.HandlerContext) (WhoamiResponse, server.IAPIError) {
		tenantID, _ := multitenant.GetTenant(c.RequestContext())
		return WhoamiResponse{Tenant: tenantID, Source: source}, nil
	}
}

// MintTokenRequest asks the simulator for a token. Tenant is copied into the tenant
// claim as given, unchecked, so the demo can mint a well-formed claim, an unknown
// tenant and a claim outside the grammar alike. OmitTenant mints a token without the
// claim.
type MintTokenRequest struct {
	Tenant     string `json:"tenant" validate:"max=128"`
	OmitTenant bool   `json:"omitTenant"`
}

// MintTokenResponse carries the compact JWT.
type MintTokenResponse struct {
	Token string `json:"token"`
}

// mintToken is the token simulator. It stands in for the partner's identity
// provider, so the demo needs no external issuer. Demo-only: it mints a credential
// the family trusts for whoever asks, which is why it is registered only in
// development. The signing key is generated per process.
func (m *Module) mintToken(req MintTokenRequest, _ server.HandlerContext) (MintTokenResponse, server.IAPIError) {
	extra := map[string]any{}
	if !req.OmitTenant {
		extra[TenantClaimName] = req.Tenant
	}
	return MintTokenResponse{Token: m.issuer.Mint(authtesting.Claims{Extra: extra})}, nil
}
