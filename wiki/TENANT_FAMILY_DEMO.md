# Tenant Family Demo

`make tenant-family-demo` checks the HTTP tenant features go-bricks offers only
with `multitenant.enabled`. The demo's main app (`cmd/api`) is single-tenant, so
they run in a second binary, `cmd/tenant-family-demo`, which serves one module,
[internal/modules/partnerportal/](../internal/modules/partnerportal/), from its
own config, [cmd/tenant-family-demo/config.yaml](../cmd/tenant-family-demo/config.yaml).

| Feature | Behavior |
|---------|----------|
| Tenant-owned route families (ADR-164) | `server.TenantOwnedGroup` mounts `/api/v1/partner/v2`. Its routes skip the tenant seat and run the family's `TenantPolicy`: `auth.Middleware` authenticates the bearer JWT, then the tenant comes from its `tenant` claim. Client faults are bodyless (`DenyBodyless`). |
| Family startup refusals | A route under the family's prefix registered outside it fails startup (`server.ErrDuplicateRoute`, "inside a tenant-owned route family"). Global middleware beside a family fails startup (`server.ErrTenantFamilyWithGlobalMiddleware`). A family under `/_sys` is refused. |
| Path-provenance existence check (ADR-170) | A tenant read from the request path is written only when the tenant store holds it; an unknown one answers 400 `Invalid tenant`. |
| Invalid tenant (ADR-161) | A missing, malformed or unknown seat tenant answers 400 `Invalid tenant` and logs one `[server.tenant]` WARN with `http.route`, `request_id`, `correlation_id` and a masked path. |
| Tenant-ID grammar | `^[a-z0-9-]{1,64}$`. An id outside it is refused before any lookup, in a header, a path or a claim. |

## Run it

```bash
make tenant-family-demo   # builds bin/tenant-family-demo, boots it on :18091
```

No infrastructure is needed: no docker, migrations or keys. Each tenant's
`database` block is mandatory in multi-tenant config, but no handler touches a
database, so nothing dials it. The tenants point at the local demo Postgres
anyway, so the block is a real one.

The script refuses a port that is already in use; `make run` on :8080 can stay
up. Set `APP_PORT` to move the instance and `LOG_DIR` to keep its log in a fixed
place. The log path is printed in the verdict.

To explore by hand:

```bash
make tenant-family-demo                      # or: go build -o bin/tenant-family-demo ./cmd/tenant-family-demo
./bin/tenant-family-demo &                   # :18091, JSON logs to stdout
B=http://127.0.0.1:18091/api/v1
TOKEN=$(curl -s -X POST -H 'X-Tenant-ID: acme' -H 'Content-Type: application/json' \
  -d '{"tenant":"acme"}' $B/__sim/partner-token | jq -r .data.token)
curl -s -H "Authorization: Bearer $TOKEN" $B/partner/v2/whoami   # {"data":{"tenant":"acme","source":"claim"},...}
curl -s $B/t/acme/whoami                                         # tenant from the path
curl -s -H 'X-Tenant-ID: globex' $B/tenants/whoami               # tenant from the header
```

## Routes

| Route | Tenant from | Notes |
|-------|-------------|-------|
| `GET /api/v1/partner/v2/whoami` | JWT `tenant` claim | Family route, development only. `X-Tenant-ID` is never read. |
| `GET /api/v1/t/:tenant/whoami` | path segment 4 | Behind the seat. Written only when held (ADR-170). |
| `GET /api/v1/tenants/whoami` | `X-Tenant-ID` | Behind the seat. Grammar checked, existence not. |
| `POST /api/v1/__sim/partner-token` | `X-Tenant-ID` (seat) | Token simulator, development only. The body's `tenant` is only the claim to mint. |

The resolver is `composite` with `order: [path, header]`. The path resolver
reads segment 4 of the raw path, base path included, and only under
`/api/v1/t` (`path: {segment: 4, prefix: /api/v1/t}`); elsewhere it passes and
the header decides.

## What it sends and checks

| Request | Checks |
|---------|--------|
| (startup) | one INFO `[server.tenant] a tenant read from the request path is written only when the tenant store holds it (ADR-170) store=*config.TenantStore tenants=3`; `/ready` 200 |
| family, no `Authorization` | 401, `WWW-Authenticate` set, empty body |
| family, token `tenant=acme`, `X-Tenant-ID: globex` | 200, `tenant=acme`, `source=claim` |
| family, token without a `tenant` claim | 403, empty body |
| family, token `tenant=ghost` | 403, empty body |
| family, token `tenant=Acme!` | 403 (grammar) |
| `GET /tenants/whoami`, no header | 400 `Invalid tenant`; WARN `reason="resolver error"` with `http.route=/api/v1/tenants/whoami`, `request_id` = `correlation_id` = `X-Request-ID` |
| `GET /tenants/whoami`, `X-Tenant-ID: ACME` | 400 (grammar) |
| `GET /t/ghost/whoami` | 400 `Invalid tenant`; WARN `reason="unknown tenant"`, `path="/api/v1/t/REDACTED/whoami"`, `http.route=/api/v1/t/:tenant/whoami`, correlated |
| `GET /t/acme/whoami` | 200, `tenant=acme` |
| `GET /t/acme/whoami`, `X-Tenant-ID: globex` | 200, `tenant=acme`: the path wins |
| `GET /tenants/whoami`, `X-Tenant-ID: acme` | 200, `tenant=acme` |
| `GET /tenants/whoami`, `X-Tenant-ID: ghost` | **200, `tenant=ghost`** (see below); its access line carries `tenant=ghost` |
| (whole log) | no `[server.tenant]` rejection line names `ghost` |

The last check covers only the `[server.tenant]` rejection lines. A header
tenant the seat admitted (form-checked, never existence-checked) is the
request's tenant, so the framework writes it as `tenant` on that request's
access line, unheld or not.

## Two things to know

**Header tenants are checked for form only.** The ADR-170 existence check covers
a tenant read from the path. A well-formed but unknown `X-Tenant-ID` is written
as is, and the request reaches its handler, where any per-tenant accessor
(`deps.DB(ctx)` and the like) fails with an unknown-tenant error. The demo shows
this as the framework behaves at this pin and does not paper over it.

**`auth.TenantClaim` checks form only too, so the module adds existence.** A
signed token naming a tenant the deployment does not hold would otherwise
publish that tenant. The family's `TenantFunc` is `heldTenantClaim`
([tenant.go](../internal/modules/partnerportal/tenant.go)): it calls
`auth.TenantClaim("tenant")`, passes its errors through unchanged, and refuses an
unheld id with an error wrapping `multitenant.ErrTenantResolutionFailed`, which
`DenyBodyless` answers with a bodyless 403. The error never carries the id.
`ModuleDeps` has no tenant store, so the module checks against
`config.NewTenantStore(deps.Config)`, the same config-backed store the framework
builds.

## The token simulator

`POST /api/v1/__sim/partner-token` stands in for the partner's identity provider.
It signs with go-bricks `auth/testing`'s issuer, whose RSA key is generated when
the process starts, and the family's verifier trusts that key alone, so tokens
die with the process. The issuer's defaults are `iss https://issuer.test/`,
`aud go-bricks-test` and `kid test-key-1`, and `auth.jwt` in the config matches
them. The body is `{"tenant":"<value>"}`, copied into the claim unchecked, or
`{"omitTenant":true}`. Because it hands out credentials the family trusts, it is
registered only in development, and it carries the `"simulator"` tag the shared
route veto ([internal/routepolicy](../internal/routepolicy/policy.go)) requires.

The family is development-only too: its verifier trusts nothing but that
in-process test issuer. With any other `APP_ENV` the module builds neither the
issuer nor the verifier, registers only the seat's header and path routes, and
logs one INFO that the family needs a real JWKS resolver. A real deployment
would build the verifier from its identity provider's JWKS instead.

## Config loading

The binary does not use `config.Load`, which would read `config.yml` and an
overlay. Its `ConfigLoader` reads `cmd/tenant-family-demo/config.yaml`, relative
to the working directory (or `-config <file>`), applies exactly four environment
variables (`SERVER_PORT`, `LOG_LEVEL`, `LOG_OUTPUT_FORMAT`, `APP_ENV`), then
calls `config.LoadFromMap`, which adds the framework defaults, and
`config.Validate`. The file sits beside the binary, not at the repository root,
so its name never matches the `config.<APP_ENV>.yaml` overlay pattern `cmd/api`
loads. The file sets `app.env: development`; the script pins
`APP_ENV=development` as well.

With static tenants the framework refuses a root `database` or `messaging`
block, so the file has none.

## Go tests

- `internal/modules/partnerportal/module_test.go` runs the family policy through
  `TenantPolicy.ResolveForTest` with real tokens: acme publishes, no token is 401
  with a challenge, and a missing claim, a bad-grammar claim and `ghost` are
  bodyless 403s. It also checks that a single-tenant or nil-config `Init`
  registers nothing, and that outside development only the seat's two routes
  register, with no issuer, verifier, family or simulator.
- `cmd/tenant-family-demo/main_test.go` loads the real config file and boots the
  binary's App in-process with a route-table hook that stops `Run` before any
  listener opens. The table passes the simulator veto; a trespass route fails
  with `server.ErrDuplicateRoute`; a module with global middleware fails with
  `server.ErrTenantFamilyWithGlobalMiddleware`. Booted with `APP_ENV=production`,
  the table holds neither `/__sim/partner-token` nor the family.
