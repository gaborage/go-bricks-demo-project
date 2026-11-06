package partnerportal

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gaborage/go-bricks/app"
	authtesting "github.com/gaborage/go-bricks/auth/testing"
	"github.com/gaborage/go-bricks/config"
	"github.com/gaborage/go-bricks/logger"
	"github.com/gaborage/go-bricks/multitenant"
	"github.com/gaborage/go-bricks/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testConfig is a multi-tenant config holding acme, globex and initech, with the
// JWT settings the in-process issuer's defaults satisfy. LoadFromMap does not
// validate, so the tenants need no database block here.
func testConfig(t *testing.T, multitenantEnabled bool) *config.Config {
	t.Helper()
	cfg, err := config.LoadFromMap(map[string]any{
		"app": map[string]any{"name": "partnerportal-test", "env": "development"},
		"multitenant": map[string]any{
			"enabled": multitenantEnabled,
			"tenants": map[string]any{
				"acme":    map[string]any{},
				"globex":  map[string]any{},
				"initech": map[string]any{},
			},
		},
		"auth": map[string]any{"jwt": map[string]any{
			"issuer":     authtesting.DefaultIssuerURL,
			"audience":   []string{authtesting.DefaultAudience},
			"algorithms": []string{"RS256"},
		}},
	})
	require.NoError(t, err)
	return cfg
}

func initModule(t *testing.T, multitenantEnabled bool) (*Module, *config.Config) {
	t.Helper()
	cfg := testConfig(t, multitenantEnabled)
	m := NewModule()
	require.NoError(t, m.Init(&app.ModuleDeps{Logger: logger.New("disabled", false), Config: cfg}))
	t.Cleanup(func() { assert.NoError(t, m.Shutdown()) })
	return m, cfg
}

// countingRegistrar counts every route and group registered on it.
type countingRegistrar struct{ calls int }

func (r *countingRegistrar) Add(string, string, server.Handler, ...server.MiddlewareFunc) { r.calls++ }

func (r *countingRegistrar) Group(string, ...server.MiddlewareFunc) server.RouteRegistrar {
	r.calls++
	return r
}

func (r *countingRegistrar) Use(...server.MiddlewareFunc) { r.calls++ }

func (r *countingRegistrar) FullPath(path string) string { return path }

// Single-tenant, the module builds nothing and registers nothing. TenantOwnedGroup
// would panic on a server with no tenant seat, so this branch is what lets a
// single-tenant app load the module at all.
func TestSingleTenantRegistersNothing(t *testing.T) {
	m, cfg := initModule(t, false)

	assert.False(t, m.enabled)
	assert.Nil(t, m.issuer)
	assert.Nil(t, m.verifier)
	assert.Nil(t, m.tenants)

	reg := &countingRegistrar{}
	require.NotPanics(t, func() { m.RegisterRoutes(server.NewHandlerRegistry(cfg), reg) })
	assert.Zero(t, reg.calls)
}

// resolution is one run of the family policy: the tenant it published, whether Deny
// ran, the fault Deny received and the response written.
type resolution struct {
	tenantID string
	denied   bool
	fault    error
	rec      *httptest.ResponseRecorder
}

// resolve runs the family policy on one request, the way the family runs it before
// its handler, and records the fault Deny received.
func resolve(t *testing.T, m *Module, cfg *config.Config, bearer string) resolution {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/partner/v2/whoami", http.NoBody)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	// X-Tenant-ID never reaches a family: the family skips the seat.
	req.Header.Set("X-Tenant-ID", "globex")
	out := resolution{rec: httptest.NewRecorder()}

	policy := m.familyPolicy()
	require.Nil(t, policy.Deny, "the module relies on the default DenyBodyless")
	policy.Deny = func(c server.HandlerContext, f error) error {
		out.fault = f
		return server.DenyBodyless(c, f)
	}

	var err error
	out.tenantID, out.denied, err = policy.ResolveForTest(server.NewHandlerContextForTest(out.rec, req, cfg))
	require.NoError(t, err)
	return out
}

func TestFamilyPolicy(t *testing.T) {
	m, cfg := initModule(t, true)
	mint := func(extra map[string]any) string { return m.issuer.Mint(authtesting.Claims{Extra: extra}) }

	t.Run("held tenant claim publishes the tenant, header ignored", func(t *testing.T) {
		got := resolve(t, m, cfg, mint(map[string]any{TenantClaimName: "acme"}))
		assert.Equal(t, "acme", got.tenantID)
		assert.False(t, got.denied)
		assert.NoError(t, got.fault)
	})

	t.Run("no bearer token is a 401 with a challenge", func(t *testing.T) {
		got := resolve(t, m, cfg, "")
		assert.Empty(t, got.tenantID)
		assert.True(t, got.denied)
		assert.Equal(t, http.StatusUnauthorized, got.rec.Code)
		assert.NotEmpty(t, got.rec.Header().Get("WWW-Authenticate"))
		assert.Empty(t, got.rec.Body.String())
	})

	refusals := []struct {
		name      string
		token     string
		wantFault error
	}{
		{"claim missing", mint(map[string]any{}), multitenant.ErrTenantResolutionFailed},
		{"claim outside the tenant-ID grammar", mint(map[string]any{TenantClaimName: "Acme!"}), multitenant.ErrInvalidTenantID},
		{"claim names a tenant the store does not hold", mint(map[string]any{TenantClaimName: "ghost"}), errTenantNotHeld},
	}
	for _, tt := range refusals {
		t.Run(tt.name, func(t *testing.T) {
			got := resolve(t, m, cfg, tt.token)
			assert.Empty(t, got.tenantID)
			assert.True(t, got.denied)
			assert.ErrorIs(t, got.fault, tt.wantFault)
			assert.Equal(t, http.StatusForbidden, got.rec.Code)
			assert.Empty(t, got.rec.Body.String())
		})
	}
}

// The existence refusal is a client fault for the family's fault rule, and never
// carries the unadmitted id.
func TestTenantNotHeldFault(t *testing.T) {
	m, cfg := initModule(t, true)
	fault := resolve(t, m, cfg, m.issuer.Mint(authtesting.Claims{Extra: map[string]any{TenantClaimName: "ghost"}})).fault

	require.Error(t, fault)
	assert.True(t, errors.Is(fault, multitenant.ErrTenantResolutionFailed))
	assert.NotContains(t, fault.Error(), "ghost")
}
