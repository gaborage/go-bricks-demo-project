package routepolicy_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gaborage/go-bricks-demo-project/internal/routepolicy"
	"github.com/gaborage/go-bricks/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func route(method, path, module string, tags ...string) server.RouteDescriptor {
	return server.RouteDescriptor{Method: method, Path: path, ModuleName: module, Tags: tags}
}

// A deliberately mis-marked table, one slip per case. Each violation must be reported
// with the route it concerns, and a well-marked table must pass untouched.
func TestRequireTaggedSimulators(t *testing.T) {
	tests := []struct {
		name     string
		routes   []server.RouteDescriptor
		wantErrs []error
		wantMsgs []string
	}{
		{
			name: "well-marked table",
			routes: []server.RouteDescriptor{
				route(http.MethodGet, "/api/v1/health", ""),
				route(http.MethodPost, "/api/v1/tokens/relay", "tokens"),
				route(http.MethodGet, "/api/v1/legacy/products", "legacy", "legacy"),
				route(http.MethodPost, "/api/v1/__sim/peer/tokens", "tokens", routepolicy.SimulatorTag),
				route(http.MethodPost, "/api/v1/__sim/streams/poison", "activity", "streams", routepolicy.SimulatorTag),
			},
		},
		{
			name:   "empty table",
			routes: nil,
		},
		{
			name:     "simulator route without the tag",
			routes:   []server.RouteDescriptor{route(http.MethodPost, "/api/v1/__sim/peer/tokens", "tokens")},
			wantErrs: []error{routepolicy.ErrUntaggedSimulatorRoute},
			wantMsgs: []string{"POST /api/v1/__sim/peer/tokens (module tokens)"},
		},
		{
			name:     "simulator route with other tags only",
			routes:   []server.RouteDescriptor{route(http.MethodPost, "/api/v1/__sim/streams/poison", "activity", "streams")},
			wantErrs: []error{routepolicy.ErrUntaggedSimulatorRoute},
			wantMsgs: []string{"POST /api/v1/__sim/streams/poison (module activity)"},
		},
		{
			name:     "simulator tag outside /__sim/",
			routes:   []server.RouteDescriptor{route(http.MethodPost, "/api/v1/tokens/relay", "tokens", routepolicy.SimulatorTag)},
			wantErrs: []error{routepolicy.ErrStraySimulatorTag},
			wantMsgs: []string{"POST /api/v1/tokens/relay (module tokens)"},
		},
		{
			// __sim is matched as a whole segment: a lookalike prefix is not a simulator
			// path, so the tag on it is stray and its absence is fine.
			name: "lookalike segment is not a simulator path",
			routes: []server.RouteDescriptor{
				route(http.MethodGet, "/api/v1/__simulated/report", "reports"),
				route(http.MethodGet, "/api/v1/__simulated/audit", "reports", routepolicy.SimulatorTag),
			},
			wantErrs: []error{routepolicy.ErrStraySimulatorTag},
			wantMsgs: []string{"GET /api/v1/__simulated/audit (module reports)"},
		},
		{
			name:     "framework route names no module",
			routes:   []server.RouteDescriptor{route(http.MethodGet, "/__sim/probe", "")},
			wantErrs: []error{routepolicy.ErrUntaggedSimulatorRoute},
			wantMsgs: []string{"GET /__sim/probe"},
		},
		{
			name: "every violation is reported",
			routes: []server.RouteDescriptor{
				route(http.MethodPost, "/api/v1/__sim/peer/tokens", "tokens"),
				route(http.MethodPost, "/api/v1/__sim/peer/mle", "tokens", routepolicy.SimulatorTag),
				route(http.MethodPost, "/api/v1/payments/authorize", "payments", routepolicy.SimulatorTag),
				route(http.MethodPost, "/api/v1/__sim/streams/poison", "activity"),
			},
			wantErrs: []error{routepolicy.ErrUntaggedSimulatorRoute, routepolicy.ErrStraySimulatorTag},
			wantMsgs: []string{
				"POST /api/v1/__sim/peer/tokens (module tokens)",
				"POST /api/v1/payments/authorize (module payments)",
				"POST /api/v1/__sim/streams/poison (module activity)",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := routepolicy.RequireTaggedSimulators(tt.routes)

			if len(tt.wantErrs) == 0 {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			for _, want := range tt.wantErrs {
				assert.ErrorIs(t, err, want)
			}
			for _, msg := range tt.wantMsgs {
				assert.Contains(t, err.Error(), msg)
			}
			// One line per violation, so a well-marked route in the same table (the
			// tagged MLE simulator above) is never reported alongside them.
			assert.Len(t, strings.Split(err.Error(), "\n"), len(tt.wantMsgs))
		})
	}
}
