# Hot-path indexes consolidation migration

- Date: 2026-06-20
- Status: design approved (autonomous)
- Scope: backend / store only. No API, no CLI, no frontend, no Go code changes beyond the regenerated sqlc query layer (and even that is unchanged - this is a pure DDL migration; sqlc output does not depend on indexes).
- Type: single golang-migrate migration `000018_hot_path_indexes`.

## Mode note

This spec was produced in autonomous mode. No human answered the brainstorming gates; the co-scheduling and CONCURRENTLY decisions below were made by the PM and are recorded with rationale. A human should still review before implementation.

## Problem

The 2026-06-10 full-codebase review found that several hot-path queries have no supporting index while three other indexes merely duplicate a UNIQUE constraint (pure write amplification). Two separate backlog items invite a single consistency migration:

- `docs/backlog/feature-2026-06-10-hot-path-indexes.md` (primary) - add three missing indexes, drop three redundant ones.
- `docs/backlog/bug-2026-06-05-index-jobstatuscounts-full-table-scan.md` (related) - index the `/v1/jobs/stats` KPI aggregate, and the note observes `/v1/workers/stats` shipped the same way.

This migration consolidates all of it into one DDL change. It is indexes-only. It does not touch the status vocabulary / CHECK-constraints work (`status-vocabulary-drift`), which is a separate Now item and the next iteration.

## Verification performed against the code

All claims below were checked against the actual source, not the backlog text. The backlog's file reference `000005_agent_enrollments.up.sql` is stale; the real file is `000005_agent_auth.up.sql`.

### Indexes to ADD - predicates verified against queries

1. `idx_task_deps_depends_on ON task_dependencies(depends_on_task_id)`
   - Serves `FailDependentTasks` (`internal/store/query/tasks.sql`): the recursive CTE seed `WHERE depends_on_task_id = $failed_task_id` and the recursive join `td.depends_on_task_id = b.task_id`.
   - Also serves the `ON DELETE CASCADE` FK from `task_dependencies.depends_on_task_id -> tasks(id)`. The table's PRIMARY KEY is `(task_id, depends_on_task_id)`, which indexes the *leading* `task_id` but cannot serve lookups keyed on `depends_on_task_id`. So this column is genuinely unindexed today.

2. `idx_tasks_worker_active ON tasks(worker_id) WHERE status IN ('dispatched','running')`
   - Predicate matches, verbatim, the WHERE clause of every consumer in `internal/store/query/tasks.sql`:
     - `GetActiveTasksForWorker` - `WHERE worker_id = $1 AND status IN ('dispatched','running')`.
     - `RequeueWorkerTasks` - `WHERE worker_id = $1 AND status IN ('dispatched','running')`.
     - `RequeueWorkerTasksIfEpoch` - same plus an `EXISTS` guard on `workers`.
     - `ListGraceCandidates` - `JOIN tasks t ON t.worker_id = w.id WHERE t.status IN ('dispatched','running')`.
     - `CountActiveTasksByAllWorkers` - `WHERE worker_id IS NOT NULL AND status IN ('dispatched','running') GROUP BY worker_id` (runs every dispatch cycle).
   - `tasks.worker_id` is an FK (`REFERENCES workers(id) ON DELETE SET NULL`) and is otherwise unindexed. The partial predicate keeps the index small (only non-terminal tasks) while covering exactly the active-task hot path. A partial index is correct here because every consumer filters on the same `status IN ('dispatched','running')` set.

3. `idx_task_logs_task_id_id ON task_logs(task_id, id)`
   - Serves `GetTaskLogsPage` (`internal/store/query/tasks.sql`): `WHERE task_id = $1 AND id > $2 ORDER BY id LIMIT $3`. The composite `(task_id, id)` lets Postgres seek to the keyset cursor and stream rows already in `id` order - no sort.
   - Supersedes the existing single-column `idx_task_logs_task_id` (000001:100), which only covers the `task_id` equality and forces a sort for the keyset page. `task_logs` is the highest-volume table, so the composite earns its keep and the single-column index is dropped to avoid carrying both.
   - Note: `GetTaskLogs` (`ORDER BY id` for the full set) and `CountTaskLogs` (`COUNT(*) WHERE task_id = $1`) are both still fully served by the leading `task_id` column of the new composite, so dropping `idx_task_logs_task_id` regresses nothing.

### Indexes to DROP - each confirmed redundant with a UNIQUE constraint

A column-level `UNIQUE` (and `ALTER TABLE ... ADD COLUMN ... UNIQUE`) creates a btree index that already serves equality lookups. The three explicit indexes below duplicate that btree and only add write amplification. All application lookups on these columns are equality (`= $1` / `= $2`), confirmed across `query/tokens.sql`, `query/agent_enrollments.sql`, `query/invites.sql`, and `query/workers.sql`.

1. `idx_api_tokens_token_hash` (000001:101) on `api_tokens(token_hash)`
   - Superseded by: `api_tokens.token_hash TEXT NOT NULL UNIQUE` (000001:16).
   - Lookup site: `tokens.sql` `WHERE t.token_hash = $1`. Equality - the UNIQUE index serves it. Safe to drop.

2. `ix_agent_enrollments_token_hash` (000005:11) on `agent_enrollments(token_hash)`
   - Superseded by: `agent_enrollments.token_hash TEXT NOT NULL UNIQUE` (000005:3).
   - Lookup site: `agent_enrollments.sql` `WHERE token_hash = $1`. Equality. Safe to drop.

3. `ix_workers_agent_token_hash` (000005:14-15) on `workers(agent_token_hash) WHERE agent_token_hash IS NOT NULL`
   - Superseded by: the UNIQUE constraint from `ALTER TABLE workers ADD COLUMN agent_token_hash TEXT UNIQUE` (000005:13), which creates a full btree on `agent_token_hash`.
   - Lookup site: `workers.sql` `WHERE agent_token_hash = $1 AND status != 'revoked'`. The UNIQUE index serves the equality; `status != 'revoked'` is a residual filter on at most one row (uniqueness guarantees a single match), so the dropped partial index gave no advantage. Safe to drop.

## Co-scheduling decision (FOLD BOTH)

Decision: fold the JobStatusCounts index AND the WorkerStatusCounts index into this same migration.

Both source items invite it, both are additive index-only-scan optimizations, and both target unfiltered full-table aggregate queries that the dashboard polls every ~3s per active user. Folding keeps the two stats KPIs consistent and avoids a second near-identical migration for no benefit. The risk is negligible: new indexes are additive and change no existing query plan adversely.

Important correction to the original backlog wording: the JobStatusCounts index must NOT be partial. The backlog proposed "a partial index on `jobs(status, updated_at)`", but `JobStatusCounts` has no overall WHERE clause - it counts every row with `FILTER` expressions in a single pass. A partial index would exclude rows the aggregate must count, so it cannot serve the query. The right shape is a plain covering index whose columns are exactly the ones the query reads, enabling an index-only scan (narrower tuples, no heap fetch) in place of a sequential heap scan.

Verified query columns:

- `JobStatusCounts` (`internal/store/query/jobs.sql`) reads only `status` and `updated_at`. Covering index: `jobs(status, updated_at)`.
- `WorkerStatusCounts` (`internal/store/query/workers.sql`) reads only `status` and `disabled_at`. Covering index: `workers(status, disabled_at)`.

Neither column pair is already covered: 000013 created `idx_jobs_status_id (status DESC, id DESC)` and `idx_jobs_updated_id (updated_at DESC, id DESC)` but no single index over `(status, updated_at)`; similarly `idx_workers_status_id` covers `status` only.

When this ships, close `docs/backlog/bug-2026-06-05-index-jobstatuscounts-full-table-scan.md` (git mv to `docs/backlog/closed/`) since both index changes it requested are delivered here.

## Final index set

ADD (5):

```sql
CREATE INDEX idx_task_deps_depends_on
  ON task_dependencies(depends_on_task_id);

CREATE INDEX idx_tasks_worker_active
  ON tasks(worker_id) WHERE status IN ('dispatched', 'running');

CREATE INDEX idx_task_logs_task_id_id
  ON task_logs(task_id, id);

CREATE INDEX idx_jobs_status_updated
  ON jobs(status, updated_at);

CREATE INDEX idx_workers_status_disabled
  ON workers(status, disabled_at);
```

DROP (4 - three redundant-with-UNIQUE plus the superseded log index):

```sql
DROP INDEX IF EXISTS idx_task_logs_task_id;          -- 000001:100, superseded by idx_task_logs_task_id_id
DROP INDEX IF EXISTS idx_api_tokens_token_hash;      -- 000001:101, dup of UNIQUE(token_hash)
DROP INDEX IF EXISTS ix_agent_enrollments_token_hash;-- 000005:11,  dup of UNIQUE(token_hash)
DROP INDEX IF EXISTS ix_workers_agent_token_hash;    -- 000005:14,  dup of UNIQUE(agent_token_hash)
```

## Migration mechanics

- Number: `000018_hot_path_indexes` (next free after 000017). Both `.up.sql` and `.down.sql` are required (golang-migrate; migrations embedded and run on startup).
- CONCURRENTLY: NO. Use plain `CREATE INDEX` / `DROP INDEX` inside the default transaction. golang-migrate wraps each migration in a transaction, and `CREATE INDEX CONCURRENTLY` cannot run in one. Every existing index migration (000011, 000013) uses plain `CREATE INDEX`; this matches convention. The project is young and these tables are small, so the brief table lock on index build is acceptable. CONCURRENTLY is an availability optimization for large hot tables under live write load - revisit only if a future migration adds an index to a large `tasks`/`task_logs` table in production.
- `DROP INDEX IF EXISTS` is used for the drops for idempotency, matching the 000011 down-migration style.

### Up migration (000018_hot_path_indexes.up.sql)

Contains the five `CREATE INDEX` and four `DROP INDEX IF EXISTS` statements above, in that order (creates before drops so the new `idx_task_logs_task_id_id` exists before the old `idx_task_logs_task_id` is removed).

### Down migration (000018_hot_path_indexes.down.sql)

Reverses exactly: drop the five added indexes, recreate the four dropped ones with their original definitions.

```sql
-- Drop indexes added by the up migration.
DROP INDEX IF EXISTS idx_task_deps_depends_on;
DROP INDEX IF EXISTS idx_tasks_worker_active;
DROP INDEX IF EXISTS idx_task_logs_task_id_id;
DROP INDEX IF EXISTS idx_jobs_status_updated;
DROP INDEX IF EXISTS idx_workers_status_disabled;

-- Recreate indexes the up migration dropped, with their original definitions.
CREATE INDEX idx_task_logs_task_id ON task_logs(task_id);                       -- 000001:100
CREATE INDEX idx_api_tokens_token_hash ON api_tokens(token_hash);               -- 000001:101
CREATE INDEX ix_agent_enrollments_token_hash ON agent_enrollments(token_hash);  -- 000005:11
CREATE INDEX ix_workers_agent_token_hash
  ON workers(agent_token_hash) WHERE agent_token_hash IS NOT NULL;              -- 000005:14-15
```

## Invariants check

- Epoch fence: not touched. This migration adds/removes indexes only; no query, status write, or epoch logic changes. The new `idx_tasks_worker_active` only accelerates already-epoch-correct queries.
- Single job-spec pipeline / one sender per stream / identity-checked teardown / no interior pointers / single JSON entry point: none touched; no Go code changes.

## System-design lens

- Load / scalability: every added index targets a query that runs per dispatch cycle, per reconnect, or per ~3s dashboard poll - exactly the paths that degrade first as the fleet and job history grow. The drops remove write amplification on the auth hot path (every token check inserts/updates touch fewer index trees).
- Failure mode: additive DDL. If the migration fails mid-way it rolls back in its transaction (no CONCURRENTLY), leaving the schema at 000017. The down migration restores the exact prior index set, so a rollback is clean.
- Threat model: unchanged. Token-hash lookups still go through the UNIQUE-constraint index; dropping the duplicate indexes does not weaken uniqueness or lookup behavior.

## Out of scope

- Status vocabulary / CHECK constraints (`status-vocabulary-drift`) - separate Now item, next iteration. This migration must not add CHECK constraints.
- Any API, CLI, or frontend change.
- CONCURRENTLY / zero-downtime index builds - explicitly deferred per the decision above.

## Success criteria

1. `internal/store/migrations/000018_hot_path_indexes.up.sql` and `.down.sql` exist with the statements above.
2. `make test-integration` runs migrations cleanly up and down (the embedded migrations apply on container startup; a clean integration run proves up applies).
3. The four dropped indexes are gone and the five new ones exist after up; the down migration restores the original four and removes the five.
4. No `*.sql.go` / `models.go` regeneration is needed (no query or schema-type change), so `make generate` produces no diff.

## Follow-up backlog

- On ship, git mv `docs/backlog/feature-2026-06-10-hot-path-indexes.md` and `docs/backlog/bug-2026-06-05-index-jobstatuscounts-full-table-scan.md` to `docs/backlog/closed/` (both fully delivered).
