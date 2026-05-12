# Flyway JSON output samples

Reference fixtures captured by `scripts/capture-flyway-samples.sh` against
real Postgres + Flyway runs. They exist to feed the Runner-contract refactor
in [`gaborage/go-bricks#376`](https://github.com/gaborage/go-bricks/issues/376),
which will replace the current `CombinedOutput()` grep with a structured
parser. The samples here are the real-world data that parser must handle.

## Versions captured

| Component | Version |
|---|---|
| Flyway CLI | **11.20.3** (Docker image `flyway/flyway:11-alpine`) |
| Postgres | 18-alpine |
| Capture date | 2026-05-12 |
| Driver in output | `postgresql-jdbc` (Flyway's bundled PG driver) |

The Flyway version is recorded inside every sample under `flywayVersion`.
Re-running the capture script against a different image tag may produce
small shape differences — Flyway has historically renamed fields between
minor versions (e.g. `migrationsExecuted` vs. `applied`).

## Files

| File | Scenario | Notable fields |
|---|---|---|
| [`migrate-fresh.json`](migrate-fresh.json) | Drop schema, run `migrate` — applies V1 + V2 from scratch | `migrations[]`, `migrationsExecuted=2`, `success=true`, `targetSchemaVersion="2"` |
| [`migrate-noop.json`](migrate-noop.json) | Re-run `migrate` immediately after fresh — nothing pending | `migrationsExecuted=0`, `migrations[]` is empty |
| [`migrate-incremental.json`](migrate-incremental.json) | Add a V3 file on top of V1+V2, re-run `migrate` | Single entry in `migrations[]`, `initialSchemaVersion="2"` |
| [`migrate-failed.json`](migrate-failed.json) | Add a V4 with intentionally broken SQL, run `migrate` | `error.errorCode="FAILED_VERSIONED_MIGRATION"`, `error.sqlState="42703"`, full stack trace |
| [`info.json`](info.json) | `flyway info` with V1, V2, V3 applied | Per-migration `state` (`Success` / `Pending` / …), `installedOnUTC`, `installedBy` |
| [`validate-clean.json`](validate-clean.json) | `validate` against fresh-applied V1+V2 | `validationSuccessful=true`, `validateCount=2` |
| [`validate-checksum-mismatch.json`](validate-checksum-mismatch.json) | Modify V1 on disk after applying it, run `validate` | `invalidMigrations[].errorDetails.errorCode="CHECKSUM_MISMATCH"`, both checksums printed |

## Parser-design notes

A few non-obvious shape quirks the upstream parser will need to handle:

* **Failures may exit 0.** Flyway with `-outputType=json` emits a structured
  envelope on validation failure and exits 0. Process exit code is *not* a
  reliable success signal — the parser must inspect `success` /
  `validationSuccessful` / the presence of `error` / `errorDetails` in the
  payload.
* **`error` vs. `errorDetails`.** Migrate failures populate top-level
  `error.{errorCode,message,cause,…}`. Validate failures wrap each offender
  under `invalidMigrations[].errorDetails`. Both shapes coexist in the wild.
* **Connection failures emit a thin envelope.** When Flyway can't even
  connect (wrong URL / missing config file), the response is just
  `{"error": {"errorCode":"CONFIGURATION", "message": …}}` with no
  `operation` or `flywayVersion` fields. The capture script's first pass
  exercised this path accidentally and `samples/flyway-output/migrate-fresh.json`
  in the initial commit reflected it — the parser should treat absence of
  `operation` as the marker for the pre-connect error class.
* **Stack traces include line breaks.** `error.cause.stackTrace` is a single
  JSON string with embedded `\n`. Parsers that surface the message to logs
  should normalize whitespace before printing.

## Reproducing

```bash
# Boot postgres + bootstrap roles/schemas (idempotent)
make docker-up
make migrate-multitenant-init

# Capture all 7 scenarios into samples/flyway-output/
make migrate-multitenant-samples
```

The script targets the `acme` tenant only (samples are about Flyway's output
shape, not multi-tenant orchestration). It's safe to re-run — it backs up
and restores `V1__create_orders_table.sql` via an `EXIT` trap so the file
stays clean between runs.
