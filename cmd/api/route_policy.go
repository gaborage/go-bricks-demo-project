package main

import (
	"github.com/gaborage/go-bricks-demo-project/internal/routepolicy"
	"github.com/gaborage/go-bricks/app"
)

// newAppOptions returns the framework overrides main boots with. Config loading and
// every dependency still come from configuration (a nil ConfigLoader means
// config.Load); the only addition is the route-table veto, which refuses startup
// when a simulator route and its tag disagree.
//
// The rule itself lives in internal/routepolicy, because cmd/tenant-family-demo
// boots with the same veto: one rule for every binary that serves simulators.
func newAppOptions() *app.Options {
	return &app.Options{
		PostRegisterRoutes: routepolicy.RequireTaggedSimulators,
	}
}
