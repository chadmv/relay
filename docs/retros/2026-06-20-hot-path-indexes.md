---
date: 2026-06-20
topic: hot-path-indexes
branch: claude/dazzling-easley-b7e79e
range: bb4ee6b (migration + integration test)
pr: 2026-06-20 / hot-path-indexes
merge: 2026-06-20 / hot-path-indexes
---

# Session Retro: 2026-06-20 - Hot-Path Indexes Consolidation

**TL;DR:** Shipped migration `000018_hot_path_indexes` (up/down), closing two
backlog items in one indexes-only migration: `feature-2026-06-10-hot-path-indexes`
(primary) and `bug-2026-06-05-index-jobstatuscounts-full-table-scan` (folded in).
The migration ADDS 5 supporting indexes and DROPS 4 redundant ones. The two
high-value lessons: a backlog proposal asked for a PARTIAL index that would have
been silently wrong (it would exclude rows a `FILTER` aggregate must count, so we
shipped a COVERING index), and drop-safety was established by reading every query
touching the dropped columns rather than trusting the backlog text. Verification
was deliberately proportionate: `pg_indexes` existence checks plus a real
down-to-v17-then-up round-trip, not brittle query-plan assertions.

## What Was Built

One golang-migrate migration on the schema only - no Go, no sqlc, no source
changes. Plain `CREATE INDEX` / `DROP INDEX` (no `CONCURRENTLY`, which cannot run
inside the transaction golang-migrate wraps each migration in), matching the
existing index migrations.

ADD (5):
- `idx_task_deps_depends_on ON task_dependencies(depends_on_task_id)` - serves the
  `FailDependentTasks` recursive CTE and the FK cascade on task deletion.
- `idx_tasks_worker_active ON tasks(worker_id) WHERE status IN ('dispatched','running')` -
  partial index serving `RequeueWorkerTasks`, `GetActiveTasksForWorker`,
  `ListGraceCandidates`, and `CountActiveTasksByAllWorkers` (the last runs every
  dispatch cycle).
- `idx_task_logs_task_id_id ON task_logs(task_id, id)` - composite for
  `GetTaskLogsPage` keyset paging on the highest-volume table.
- `idx_jobs_status_updated ON jobs(status, updated_at)` - covering index for
  `JobStatusCounts` (the `/v1/jobs/stats` KPI strip, polled ~3s per active user).
- `idx_workers_status_disabled ON workers(status, disabled_at)` - the symmetric
  index for `WorkerStatusCounts` (`/v1/workers/stats`).

DROP (4), each confirmed redundant:
- `idx_task_logs_task_id` - superseded by the leading column of the new composite.
- `idx_api_tokens_token_hash`, `ix_agent_enrollments_token_hash`,
  `ix_workers_agent_token_hash` - each duplicates the btree that a UNIQUE
  constraint already creates, so they were pure write amplification.

The down migration restores the 4 dropped indexes byte-faithfully, including the
workers index's partial `WHERE agent_token_hash IS NOT NULL`. The integration test
(`internal/store/hot_path_indexes_integration_test.go`) asserts the 5-present /
4-absent index set and runs a real down(to v17)->up round-trip, following the
established `sort_indexes` / `migrate_down` test patterns.

## Key Decisions

- **Co-schedule both items into one migration.** Both backlog items invited it
  (each cross-references the other), both are additive index-only optimizations on
  the same hot tables, and folding them avoids a second migration churning the same
  tables. The `WorkerStatusCounts` index was added alongside the `JobStatusCounts`
  one because they are the symmetric KPI pair and the backlog item already flagged
  the symmetry.
- **Covering, not partial, for the status-count indexes.** The
  `index-jobstatuscounts` backlog item proposed a PARTIAL index. That is wrong for
  this query: `JobStatusCounts` has no overall `WHERE` - it counts every row via
  `COUNT(*) FILTER (...)`, so a partial index would exclude rows the aggregate must
  count. Shipped a plain covering index `(status, updated_at)` instead; both KPI
  queries read only those two columns, enabling an index-only scan in place of the
  sequential heap scan.
- **Drop indexes after creating the new composite, with `IF EXISTS`.** Ordering the
  `DROP idx_task_logs_task_id` after `CREATE idx_task_logs_task_id_id` means the
  column is never briefly unindexed mid-migration; `IF EXISTS` keeps each drop
  idempotent.
- **Proportionate DDL verification.** Used `pg_indexes` existence checks plus a real
  migrate-down-then-up round-trip rather than `EXPLAIN`/query-plan assertions, which
  are brittle (planner choices shift with row counts and PG versions) and were not
  the risk here. The round-trip's re-up leg specifically catches duplicate
  index-name collisions, the real failure mode for an add/drop migration.

## Problems Encountered

No implementation problems - the work landed clean. Both verifiers (code reviewer
and integration tester) independently reported 0 findings, and the full store
integration suite passed 36/36. The one substantive correction was made at design
time, not in review: catching that the backlog-proposed partial index was wrong
for a `FILTER` aggregate before any code was written.

## Improvement Goals

- **Validate a backlog proposal's index SHAPE against the query's filter semantics
  before implementing.** The `index-jobstatuscounts` item confidently specified a
  partial index, but a `FILTER` aggregate has no overall `WHERE` and a partial
  index would have silently undercounted. The proposal's *target* (index the KPI
  scan) was right; its *shape* was wrong. Read the actual query's `WHERE`/`FILTER`
  structure, not the backlog text, when choosing partial vs covering. This is a
  specific instance of the standing rule that a backlog proposal is not a contract.
- **Confirm drop-safety by reading every consumer, never by inference from the
  backlog.** Each dropped index was certified redundant by reading every query that
  touches its columns and confirming the lookup is served by a UNIQUE-constraint
  btree or the new composite's leading column - not assumed from the "duplicates a
  UNIQUE constraint" label in the item. For add/drop migrations the re-up leg of a
  down->up round-trip is the cheap, decisive guard against name collisions; prefer
  it over query-plan assertions.

## Backlog Triage

- **`bug-2026-06-10-status-vocabulary-drift` remains a clean standalone Now item -
  unaffected, left open.** That item's proposal had suggested its CHECK constraints
  could ride "one migration that can also carry the indexes." The index migration
  shipped WITHOUT the CHECK constraints (correctly - CHECK constraints are a
  behavioral data-integrity change with their own reconciliation work on
  `JobStatusCounts`'s dead buckets and `jobspec` priority validation, not an
  index-only optimization), so the two were deliberately not co-scheduled. The item
  is unchanged and still the next store-schema Now item; not re-filed.
- **Declined to file an index-only-scan verification follow-up.** Considered
  capturing a task to `EXPLAIN`-verify that `JobStatusCounts` / `WorkerStatusCounts`
  actually get an index-only scan from the new covering indexes. Declined: low
  value relative to its cost. The index columns exactly cover the queries' column
  references, the planner's choice is data-dependent (an index-only scan also needs
  a reasonably current visibility map, i.e. recent `VACUUM`), and a one-shot
  `EXPLAIN` assertion is exactly the brittle verification we deliberately avoided in
  this migration. If KPI latency is ever observed to be a problem in practice, that
  is the trigger to investigate - not a speculative backlog item now.
- **Declined to file a VACUUM/ANALYZE operational note.** Postgres autovacuum keeps
  the visibility map current on these tables under normal operation; there is no
  relay-specific tuning to record, and a generic "run ANALYZE after big migrations"
  note would be noise. Not filed.

**Net: 0 new items filed. 2 items closed this cycle** (both already moved to
`docs/backlog/closed/` with resolution notes):
`feature-2026-06-10-hot-path-indexes` and
`bug-2026-06-05-index-jobstatuscounts-full-table-scan`.

## Files Most Touched

- `internal/store/migrations/000018_hot_path_indexes.up.sql` - ADD 5, DROP 4.
- `internal/store/migrations/000018_hot_path_indexes.down.sql` - DROP the 5 added,
  restore the 4 dropped byte-faithfully (including the workers partial `WHERE`).
- `internal/store/hot_path_indexes_integration_test.go` - 5-present / 4-absent
  index-set assertion plus a real down(v17)->up round-trip.
- `docs/superpowers/specs/2026-06-20-hot-path-indexes-design.md` - design spec.
- `docs/backlog/closed/feature-2026-06-10-hot-path-indexes.md` - closed with
  resolution note.
- `docs/backlog/closed/bug-2026-06-05-index-jobstatuscounts-full-table-scan.md` -
  closed (folded in) with the partial-vs-covering correction recorded.
