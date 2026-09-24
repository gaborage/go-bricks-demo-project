package main

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/gaborage/go-bricks/server"
)

// The demo serves in-process simulators next to its real API: counterparties for the
// outbound JOSE relays (/__sim/peer/...) and a fault injector for the streams lane
// (/__sim/streams/poison). Two markers set them apart, and the two must agree:
//
//   - the /__sim/ path segment, which is what callers and access logs see;
//   - the "simulator" route tag, which is what the route table itself carries
//     (server.RouteDescriptor.Tags) for anything that reads routes as data.
//
// A simulator registered without the tag, or the tag on a route outside /__sim/, is a
// registration slip that nothing else catches: both compile, and both serve.
const (
	simulatorSegment = "__sim"
	simulatorTag     = "simulator"
)

var (
	errUntaggedSimulatorRoute = errors.New(`route under /__sim/ lacks the "simulator" tag`)
	errStraySimulatorTag      = errors.New(`route outside /__sim/ carries the "simulator" tag`)
)

// requireTaggedSimulators is the app.Options.PostRegisterRoutes hook (go-bricks
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
func requireTaggedSimulators(routes []server.RouteDescriptor) error {
	var errs []error
	for i := range routes {
		route := &routes[i]
		underSim := isSimulatorPath(route.Path)
		tagged := slices.Contains(route.Tags, simulatorTag)
		switch {
		case underSim && !tagged:
			errs = append(errs, routeViolation(errUntaggedSimulatorRoute, route))
		case tagged && !underSim:
			errs = append(errs, routeViolation(errStraySimulatorTag, route))
		}
	}
	return errors.Join(errs...)
}

// isSimulatorPath matches __sim as a whole path segment, wherever the base path puts
// it, so /api/v1/__simulated is not a simulator route.
func isSimulatorPath(path string) bool {
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
