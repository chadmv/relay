-- Byte bounds on worker_workspaces' four agent-supplied TEXT columns, behind
-- internal/worker's inventoryUpsertParams and at the same four numbers. It buys
-- nothing against today's writers, which all go through that constructor; it
-- buys a writer that does not exist yet - a sweeper, an eviction path, a second
-- provider - failing closed instead of failing an index.
--
-- NOT VALID, DELIBERATELY. Postgres enforces a NOT VALID CHECK on every
-- subsequent INSERT and UPDATE and only skips the backfill scan. A validated
-- constraint scans every existing row and FAILS THE MIGRATION on a violation,
-- and migrations are embedded and run on startup - so one over-long row planted
-- before this lands would be a server that will not boot after upgrade. A
-- control whose deployment can be denied in advance by the data it exists to
-- reject is the wrong control.
--
-- The pre-existing violating set drains on its own: every agent reconnect runs
-- ReplaceWorkerInventory and re-reports through the bounded constructor. A later
-- VALIDATE CONSTRAINT is an optional operator action, is not part of this
-- change, and MAY FAIL while rows from a never-reconnecting worker remain. Do
-- not run it expecting success.
--
-- octet_length, not length: the Go bound is len() on a string, and a rune
-- measure here would be a second implementation of one policy that agrees on
-- ASCII and disagrees on everything else.
--
-- Four constraints rather than one compound predicate, because the constraint
-- NAME is the whole diagnostic available to the writer this exists for - the one
-- that got no Go error naming the column.
--
-- No NUL predicate: a NUL cannot be stored in TEXT at all, so the database
-- already refuses it and a redundant predicate would restate a rule Postgres
-- owns.
ALTER TABLE worker_workspaces
  ADD CONSTRAINT worker_workspaces_source_type_len_check
  CHECK (octet_length(source_type) <= 64) NOT VALID;

ALTER TABLE worker_workspaces
  ADD CONSTRAINT worker_workspaces_source_key_len_check
  CHECK (octet_length(source_key) <= 512) NOT VALID;

ALTER TABLE worker_workspaces
  ADD CONSTRAINT worker_workspaces_short_id_len_check
  CHECK (octet_length(short_id) <= 128) NOT VALID;

ALTER TABLE worker_workspaces
  ADD CONSTRAINT worker_workspaces_baseline_hash_len_check
  CHECK (octet_length(baseline_hash) <= 128) NOT VALID;
