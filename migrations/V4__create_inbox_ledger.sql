-- V4: Create the consumer-side inbox (dedup) ledger
-- Source: go-bricks inbox/store_postgres.go — postgresCreateTableSQL and
-- postgresCreateProcessedIndexSQL, reproduced verbatim with the table name resolved to the
-- framework DEFAULT (inbox.DefaultTableName = "gobricks_inbox", inbox/config.go).
--
-- Why the demo owns this DDL: inbox.autocreatetable is false here, matching the outbox
-- precedent (V3 / config.development.yaml). Autocreate is opt-in in the framework and only ever
-- CREATEs a missing table, so a managed migration is the explicit, replayable path: run
-- `make migrate` before `make run`. With the inbox enabled and this table absent, Init fails
-- fast (verifyStartupDatabase probes DeleteProcessed before the Unix epoch — a no-op write that
-- still proves table + DELETE privilege) rather than booting green and failing per delivery.
--
-- The payments sealed-events consumer dedups through deps.Inbox.ProcessOnce, which writes exactly
-- one row per (tenant_id, event_id) inside the handler's transaction: ON CONFLICT DO NOTHING makes
-- a redelivery a zero-row insert instead of a 23505 that would poison the transaction. Single-tenant
-- mode leaves tenant_id at the column DEFAULT '' (multitenant.GetTenant returns "").
--
-- The per-tenant hold tables (gobricks_inbox_hold, gobricks_inbox_hold_tenant) are INTENTIONALLY
-- OMITTED: the hold parks a tenant's failed STREAM deliveries and requires inbox.tenancy: shared,
-- while this demo is single-tenant on the AMQP lane with inbox.hold disabled. Their DDL lives in
-- go-bricks wiki/outbox.md if the showcase ever grows a streams lane.

CREATE TABLE IF NOT EXISTS gobricks_inbox (
    tenant_id     VARCHAR(255) NOT NULL DEFAULT '',
    event_id      VARCHAR(255) NOT NULL,
    processed_at  TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, event_id)
);

-- Retention sweep index: the daily `inbox-cleanup` job deletes rows with
-- processed_at < now() - inbox.retentionperiod (168h default — the replay window).
CREATE INDEX IF NOT EXISTS idx_gobricks_inbox_processed ON gobricks_inbox (processed_at);
