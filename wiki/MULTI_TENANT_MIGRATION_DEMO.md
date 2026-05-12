# Multi-tenant Flyway migrations with `go-bricks-migrate`

This walkthrough demonstrates **schema-per-tenant** Flyway migrations driven
by the framework's `go-bricks-migrate` CLI (shipped in
[`gaborage/go-bricks#387`](https://github.com/gaborage/go-bricks/pull/387)
and tagged in `v0.31.0`).

The companion single-tenant flow at
[`FLYWAY_MIGRATIONS.md`](../FLYWAY_MIGRATIONS.md) remains untouched — both
patterns coexist in this repo.

## What this demo shows

* Three tenants (`acme`, `globex`, `initech`) sharing a single Postgres
  instance, isolated by per-tenant **schemas**.
* Each tenant's `flyway_schema_history` table lives inside its own schema,
  so Flyway never sees another tenant's state.
* Per-tenant credentials resolved from a static YAML file
  (`config.multitenant.yaml`) via `go-bricks-migrate --credentials-from=config-file`.
* Identical migration files (`migrations/multitenant/V*.sql`) applied to
  every tenant — no per-tenant SQL forks.

> The framework also supports the **AWS Secrets Manager** credential source
> (`--credentials-from=aws-secrets-manager`, the default). That path is
> tracked as a follow-up demo so this issue can land without LocalStack
> plumbing. See
> [`gaborage/go-bricks` ADR-018](https://github.com/gaborage/go-bricks/blob/main/wiki/adr-018-multi-tenant-migration-cli.md)
> for the production architecture.

## How schema isolation actually works

This is the most surprising piece of the design, so it gets called out
first.

The framework's `migration/flyway.go` only propagates **five** environment
variables to the Flyway subprocess: `DB_HOST`, `DB_PORT`, `DB_USER`,
`DB_PASSWORD`, `DB_NAME`. The `database.postgresql.schema` field exists in
the config struct but is **not** exported to Flyway, so you can't drive
schema-per-tenant by setting it in the fleet YAML.

Workaround used in this demo: **per-tenant Postgres roles with a role-level
`search_path`**.

```sql
CREATE ROLE acme LOGIN PASSWORD 'acme_pw';
CREATE SCHEMA acme AUTHORIZATION acme;
ALTER ROLE acme SET search_path TO acme;
GRANT CONNECT ON DATABASE postgres TO acme;
```

When `go-bricks-migrate` connects as `acme/acme_pw` to the shared `postgres`
database, the JDBC session picks up `search_path=acme` automatically.
Flyway, when its `flyway.schemas` config is **unset** (see
[`flyway/flyway-multitenant.conf`](../flyway/flyway-multitenant.conf)),
resolves the default schema via the session's `current_schema()` — which
honors `search_path`. So every tenant's tables and their
`flyway_schema_history` end up inside the role's home schema.

The bootstrap SQL at
[`etc/docker/postgres/multitenant-init.sql`](../etc/docker/postgres/multitenant-init.sql)
applies this idempotently for all three tenants.

## File layout

```text
config.multitenant.yaml                       Fleet config (3 tenants)
flyway/flyway-multitenant.conf                Flyway config (no flyway.schemas)
migrations/multitenant/
    V1__create_orders_table.sql
    V2__add_status_column.sql
scripts/
    flyway-docker.sh                          Wrapper: flyway-in-Docker as --flyway-path
    capture-flyway-samples.sh                 Captures samples/ JSON fixtures
    multitenant-reset.sh                      Drops + recreates every tenant schema
etc/docker/postgres/multitenant-init.sql      Roles + schemas bootstrap
samples/flyway-output/                        JSON fixtures for go-bricks#376
```

## Make targets

```bash
make migrate-multitenant-install   # go install the go-bricks-migrate CLI
make migrate-multitenant-init      # Bootstrap roles + schemas (idempotent)
make migrate-multitenant-up        # Apply migrations to every tenant
make migrate-multitenant-info      # Show status for every tenant
make migrate-multitenant-validate  # Validate (no apply) for every tenant
make migrate-multitenant-reset     # Drop + recreate every tenant schema
make migrate-multitenant-samples   # Capture JSON fixtures (feeds go-bricks#376)
```

## Walkthrough

### 1. Install the CLI

```bash
make migrate-multitenant-install
```

> **Why not `go install …@latest`?** The framework's `tools/migration/go.mod`
> uses a `replace github.com/gaborage/go-bricks => ../../` directive so the
> CLI builds against the in-repo framework changes. Go's module proxy
> rejects replace directives on `go install`, so the target clones the
> framework into a temp dir and runs `go install` from inside the submodule
> where replaces are honored. Set `GO_BRICKS_PATH=/path/to/go-bricks` to
> skip the clone if you have a checkout already.

### 2. Boot Postgres and bootstrap tenant roles

```bash
make docker-up                  # boots the existing demo postgres container
make migrate-multitenant-init   # creates acme/globex/initech roles + schemas
```

Verify:

```sql
-- postgres
SELECT rolname, rolconfig
  FROM pg_roles
 WHERE rolname IN ('acme','globex','initech');

--  rolname |       rolconfig
-- ---------+-----------------------
--  acme    | {search_path=acme}
--  globex  | {search_path=globex}
--  initech | {search_path=initech}
```

### 3. Apply migrations to the fleet

```bash
make migrate-multitenant-up
```

Equivalent CLI invocation:

```bash
go-bricks-migrate migrate \
    --source-config config.multitenant.yaml \
    --credentials-from config-file \
    --flyway-config flyway/flyway-multitenant.conf \
    --migrations-dir migrations/multitenant \
    --flyway-path scripts/flyway-docker.sh \
    --continue-on-error
```

Expected progress (text mode — add `--json` for NDJSON):

```text
  acme (postgresql) ... ok (1.87s)
  globex (postgresql) ... ok (1.17s)
  initech (postgresql) ... ok (1.16s)

Migrate summary: 3 tenants total, 0 failed
```

Inspect the result:

```sql
SELECT table_schema, table_name
  FROM information_schema.tables
 WHERE table_schema IN ('acme','globex','initech')
 ORDER BY table_schema, table_name;

--  table_schema |      table_name
-- --------------+-----------------------
--  acme         | flyway_schema_history
--  acme         | orders
--  globex       | flyway_schema_history
--  globex       | orders
--  initech      | flyway_schema_history
--  initech      | orders
```

Each tenant's history table tracks its own state — modifying a migration
mid-flight will only break the tenant whose `flyway_schema_history`
recorded the original checksum, not the rest of the fleet (modulo
`--continue-on-error`, which keeps the loop going across per-tenant
failures).

### 4. Run `info` and `validate`

`info` shows what's applied vs. pending per tenant:

```bash
make migrate-multitenant-info
# Info summary: 3 tenants total, 0 failed
```

`validate` checks that on-disk migrations match what's been applied — no
SQL is executed:

```bash
make migrate-multitenant-validate
# Validate summary: 3 tenants total, 0 failed
```

### 5. Reset between runs

```bash
make migrate-multitenant-reset
```

Drops and recreates every tenant's schema. The role + search_path stay
intact, so the next `make migrate-multitenant-up` will succeed without
re-running `migrate-multitenant-init`.

## Adding a tenant

1. **Add a Postgres role and schema** by appending the tenant ID to the
   `tenant_roles` array in
   [`etc/docker/postgres/multitenant-init.sql`](../etc/docker/postgres/multitenant-init.sql):

   ```sql
   tenant_roles CONSTANT TEXT[] := ARRAY['acme', 'globex', 'initech', 'umbrella'];
   ```

   Re-run `make migrate-multitenant-init` — it's idempotent for the
   existing tenants and will only create what's missing.

2. **Add the tenant to the fleet YAML** in
   [`config.multitenant.yaml`](../config.multitenant.yaml):

   ```yaml
   multitenant:
     tenants:
       # ...existing tenants...
       umbrella:
         database:
           type: postgresql
           host: localhost
           port: 5432
           database: postgres
           username: umbrella
           password: umbrella_pw
   ```

3. Run `make migrate-multitenant-up` — the new tenant gets V1+V2 applied;
   existing tenants are a no-op.

## Adding a new migration to the fleet

1. Drop a new `Vn__description.sql` into `migrations/multitenant/`. Use the
   next sequential version number (V3, V4, …). Same SQL is applied to
   every tenant.
2. (Optional) `make migrate-multitenant-validate` to confirm checksums of
   already-applied migrations are intact.
3. `make migrate-multitenant-up` — only the new migration runs per tenant.

> **Beware schema-qualified DDL.** Because Flyway picks up the default
> schema from `search_path`, **don't** qualify your tables with a schema
> prefix in the SQL files. `CREATE TABLE orders (...)` is right;
> `CREATE TABLE acme.orders (...)` would create tables in `acme` regardless
> of which tenant you're migrating, breaking isolation.

## Going further

* **`--parallel`**: pass `--parallel N` to migrate up to N tenants
  concurrently (framework caps internally at 32). Watch for Postgres
  connection-storm risk on real fleets.
* **`--json`**: NDJSON progress events for CI/CD pipelines. Useful for
  driving the next step (`go-bricks#376` parser) once it lands.
* **AWS Secrets Manager**: swap `--credentials-from=config-file` for
  `--credentials-from=aws-secrets-manager` and supply `--secrets-prefix`.
  Per-tenant secrets are looked up at `<prefix><tenant_id>`. The CLI also
  honors AWS SDK env vars (`AWS_REGION`, `AWS_PROFILE`). A LocalStack-based
  demo of this path is tracked as a follow-up.
* **JSON output samples** for the upstream `go-bricks#376` Runner-contract
  refactor live in [`samples/flyway-output/`](../samples/flyway-output/);
  see its [README](../samples/flyway-output/README.md) for the parser-design
  notes.

## Out of scope

* **Oracle**: tracked as a separate issue
  ([`gaborage/go-bricks#385`](https://github.com/gaborage/go-bricks/issues/385)).
* **Audit-event delivery**: the multi-tenant migration audit log goes
  through the OTel seam by default; compliance-grade durability is an
  opt-in `AuditSink` interface tracked under
  [`gaborage/go-bricks#382`](https://github.com/gaborage/go-bricks/issues/382)
  and described in
  [ADR-019](https://github.com/gaborage/go-bricks/blob/main/wiki/adr-019-migration-audit-delivery.md).
* **Database-per-tenant (vs. schema-per-tenant)**: trivial extension —
  swap each `database: postgres` line for a unique `database: tenant_db`,
  drop the per-tenant role bootstrap, and let Flyway default `flyway.schemas`
  to `public` in each database. Not demonstrated here because the
  upstream gap that motivated the role+search_path workaround only
  matters for the schema-per-tenant case.
