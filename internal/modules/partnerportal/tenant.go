package partnerportal

import (
	"fmt"

	"github.com/gaborage/go-bricks/auth"
	"github.com/gaborage/go-bricks/multitenant"
	"github.com/gaborage/go-bricks/server"
)

// errTenantNotHeld is a well-formed tenant claim the deployment does not hold. It
// wraps multitenant.ErrTenantResolutionFailed, which the family's fault rule treats as
// a client fault, so DenyBodyless answers it with a bodyless 403. Its text never
// carries the claim's value: the id is unadmitted, and the multitenant.TenantChecker
// contract forbids echoing it.
var errTenantNotHeld = fmt.Errorf("partnerportal: tenant claim names a tenant this deployment does not hold: %w",
	multitenant.ErrTenantResolutionFailed)

// heldTenantClaim is auth.TenantClaim plus an existence check.
//
// auth.TenantClaim checks the claim's form only: present, a string, inside the
// tenant-ID grammar. A signed token naming a tenant this deployment does not hold
// would otherwise publish that tenant, and every per-tenant accessor would fail later
// with an unknown-tenant error. The tenant seat's path check (ADR-170) does not reach
// a family, which skips the seat, so the family asks the same store itself.
//
// TenantClaim's own errors pass through unchanged: a missing Principal stays a plain
// error (a wiring fault the framework answers 500), and a missing or malformed claim
// stays a client fault for Deny. HasTenant only ever sees an id that passed the grammar.
func heldTenantClaim(claim string, tenants multitenant.TenantChecker) server.TenantFunc {
	fromClaim := auth.TenantClaim(claim)
	return func(c server.HandlerContext) (string, error) {
		tenantID, err := fromClaim(c)
		if err != nil {
			return "", err
		}
		if !tenants.HasTenant(tenantID) {
			return "", errTenantNotHeld
		}
		return tenantID, nil
	}
}
