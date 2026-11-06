// Package routepolicy holds the route-table veto every demo binary boots with: a
// simulator route and its "simulator" tag must agree. cmd/api and
// cmd/tenant-family-demo both install RequireTaggedSimulators as their
// app.Options.PostRegisterRoutes hook, so the rule is one rule, not two copies.
package routepolicy

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/gaborage/go-bricks/server"
)

// The demo serves in-process simulators next to its real API: counterparties for the
// outbound JOSE relays (/__sim/peer/...), a fault injector for the streams lane
// (/__sim/streams/poison) and, in the multi-tenant binary, a token minter
// (/__sim/partner-token). Two markers set them apart, and the two must agree:
//
//   - the /__sim/ path segment, which is what callers and access logs see;
//   - the "simulator" route tag, which is what the route table itself carries
//     (server.RouteDescriptor.Tags) for anything that reads routes as data.
//
// A simulator registered without the tag, or the tag on a route outside /__sim/, is a
// registration slip that nothing else catches: both compile, and both serve.
const (
	simulatorSegment = "__sim"
	// SimulatorTag is the route tag a simulator registers with server.WithTags.
	SimulatorTag = "simulator"
)

var (
	// ErrUntaggedSimulatorRoute is a route under /__sim/ registered without SimulatorTag.
	ErrUntaggedSimulatorRoute = errors.New(`route under /__sim/ lacks the "simulator" tag`)
	// ErrStraySimulatorTag is SimulatorTag on a route outside /__sim/.
	ErrStraySimulatorTag = errors.New(`route outside /__sim/ carries the "simulator" tag`)
)

// RequireTaggedSimulators is the app.Options.PostRegisterRoutes hook (go-bricks
// v0.65.0, #1672). The framework calls it once per Run with every route the App
// registered — module routes, the debug endpoints and the health/ready probes — after
// its own duplicate-route check and before the listener opens, so a returned error
// aborts startup before a mis-marked route serves a request.
//
// A hook can only veto the table; it cannot add, drop or rewrite a route. That is why
// it enforces a marking rule rather than gating simulators by environment: the tokens
// relays call their simulators in-process in every environment, and the activity
// module keeps its own development-only guard, because only the module that owns a
// route can decline to register it.
//
// Every violation is reported, not just the first, so one failed boot names them all.
func RequireTaggedSimulators(routes []server.RouteDescriptor) error {
	var errs []error
	for i := range routes {
		route := &routes[i]
		underSim := IsSimulatorPath(route.Path)
		tagged := slices.Contains(route.Tags, SimulatorTag)
		switch {
		case underSim && !tagged:
			errs = append(errs, routeViolation(ErrUntaggedSimulatorRoute, route))
		case tagged && !underSim:
			errs = append(errs, routeViolation(ErrStraySimulatorTag, route))
		}
	}
	return errors.Join(errs...)
}

// IsSimulatorPath matches __sim as a whole path segment, wherever the base path puts
// it, so /api/v1/__simulated is not a simulator route.
func IsSimulatorPath(path string) bool {
	return slices.Contains(strings.Split(path, "/"), simulatorSegment)
}

// routeViolation names the route by method, full path and owning module (empty for a
// route the framework registered itself).
func routeViolation(reason error, route *server.RouteDescriptor) error {
	if route.ModuleName == "" {
		return fmt.Errorf("%w: %s %s", reason, route.Method, route.Path)
	}
	return fmt.Errorf("%w: %s %s (module %s)", reason, route.Method, route.Path, route.ModuleName)
}
