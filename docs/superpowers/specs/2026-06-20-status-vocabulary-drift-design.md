# Status Vocabulary Drift: CHECK Constraints, JobStatusCounts Reconciliation, and Priority Validation

- Date: 2026-06-20
- Author: TPM (autonomous brainstorming run)
- Backlog item: `docs/backlog/bug-2026-06-10-status-vocabulary-drift.md`
- Scope: backend only (store migration + `internal/jobspec`). No frontend.
- Mode: autonomous. Design decisions are made here with rationale; no human gates were used. The vocabulary sets below were derived from the code, not assumed.

## Problem

Six columns are free `TEXT` with no `CHECK` constraint:

- `workers.status`
- `jobs.status`
- `jobs.priority`
- `tasks.status`
- `task_logs.stream`
- `scheduled_jobs.overlap_policy`

The lack of a constraint has already let real drift ship:

1. `JobStatusCounts` (`internal/store/query/jobs.sql:282-292`) buckets job statuses `dispatched`, `queued`, and `timed_out` that **no code path ever writes to `jobs.status`**, so those KPI buckets are permanently dead, while `cancelled` jobs land in no bucket at all.
2. `jobspec.Validate` never checks `Priority`, so a typo like `"hgih"` is stored silently and then sorts as a bogus label.

A `CHECK` constraint is the structural fix, but it carries a sharp risk: a constraint whose value set does not exactly match every value the code writes will either fail to apply (if an existing row violates it) or start rejecting legitimate writes at runtime. The bulk of this spec is therefore the **definitive, code-cited vocabulary for each column**, followed by the constraint, the query reconciliation, and the priority rule.

## Definitive allowed-value sets (with citations)

Each set below is the union of: the column `DEFAULT` in the migration, every literal written by a store query, and every Go literal that feeds a parameterized status write. Citations are `file:line`.

### `tasks.status` -> {pending, dispatched, running, done, failed, timed_out}

`tasks.status` is a **superset** of `jobs.status`. It legitimately includes `dispatched` and `timed_out`; do not assume it shares the jobs vocabulary.

| Value | Written by | Citation |
| --- | --- | --- |
| `pending` | column DEFAULT; `IncrementTaskRetryCount`; `RequeueTask`; `RequeueTaskByID`; `RequeueWorkerTasks`; `RequeueWorkerTasksIfEpoch` | `internal/store/migrations/000001_initial.up.sql:61`; `internal/store/query/tasks.sql:23,93,121,190,203` |
| `dispatched` | `ClaimTaskForWorker` (literal `'dispatched'`) | `internal/store/query/tasks.sql:82` |
| `running` | `handler.go` maps `TASK_STATUS_RUNNING` -> `"running"`, then `UpdateTaskStatus`/`UpdateTaskStatusEpoch` | `internal/worker/handler.go:425`; `internal/store/query/tasks.sql:17,155` |
| `done` | `handler.go` maps `TASK_STATUS_DONE` -> `"done"` | `internal/worker/handler.go:427` |
| `failed` | `handler.go` maps `TASK_STATUS_FAILED` and `TASK_STATUS_PREPARE_FAILED` -> `"failed"`; `FailDependentTasks`; `CancelJobTasks` (literal `'failed'`) | `internal/worker/handler.go:429,437`; `internal/store/query/tasks.sql:72,177` |
| `timed_out` | `handler.go` maps `TASK_STATUS_TIMED_OUT` -> `"timed_out"` | `internal/worker/handler.go:431` |

Note the proto-enum default arm in `handler.go:438-440` returns without writing, so no other value reaches the DB. `UpdateTaskStatusEpoch` (`tasks.sql:155`) takes its value from the same `statusStr` mapping in `handler.go`, so it adds no new values.

There is no `'queued'` and no `'cancelled'` for tasks: cancellation routes through `CancelJobTasks` which sets task status to `'failed'` (`tasks.sql:177`). The `CancelJobTasks` and `RequeueTaskByID` WHERE clauses reference `'queued'` defensively (`tasks.sql:181,126`) but `queued` is never written, so it is intentionally **excluded** from the allowed set.

### `jobs.status` -> {pending, running, done, failed, cancelled}

| Value | Written by | Citation |
| --- | --- | --- |
| `pending` | column DEFAULT | `internal/store/migrations/000001_initial.up.sql:43` |
| `running` | `UpdateJobStatusFromTasks` CASE -> `'running'` | `internal/store/query/jobs.sql:98` |
| `done` | `UpdateJobStatusFromTasks` CASE -> `'done'` | `internal/store/query/jobs.sql:99` |
| `failed` | `UpdateJobStatusFromTasks` CASE ELSE -> `'failed'` (this is also where a task `timed_out` folds into job `failed`) | `internal/store/query/jobs.sql:100` |
| `cancelled` | `UpdateJobStatus` called from the cancel handler with literal `"cancelled"` (the only Go caller of `UpdateJobStatus`) | `internal/api/jobs.go:746-748` |

`UpdateJobStatus` (`jobs.sql:85`) takes `$2`; its sole Go call site writes `"cancelled"`. `UpdateJobStatusFromTasks` (`jobs.sql:94-107`) is the only other writer and emits only `running`/`done`/`failed`. Therefore `jobs.status` never holds `dispatched`, `queued`, or `timed_out` - exactly the drift the KPI query encodes.

### `jobs.priority` -> {low, normal, high}

| Value | Source | Citation |
| --- | --- | --- |
| `normal` | column DEFAULT; `jobcreate` substitutes `"normal"` for empty `Priority` | `internal/store/migrations/000001_initial.up.sql:42`; `internal/jobcreate/jobcreate.go:36-39` |
| `low`, `normal`, `high` | Documented accepted set for the `priority` request field | `README.md:588` (`normal` (default), `high`, or `low`) |

The code does **not** currently enumerate priorities anywhere - `jobcreate` passes the string straight through after defaulting empty to `normal`. The only authoritative statement of the intended set is the README. The set to enforce is `{low, normal, high}`. Empty is accepted at the spec layer and defaults to `normal` downstream (see Priority validation rule below).

### `workers.status` -> {online, offline, stale, revoked}

| Value | Written by | Citation |
| --- | --- | --- |
| `offline` | column DEFAULT; `MarkWorkerOfflineIfEpoch` (literal); `UpdateWorkerStatus`/`SetWorkerStatus` from Go | `internal/store/migrations/000001_initial.up.sql:33`; `internal/store/query/workers.sql:51`; `internal/worker/handler.go` teardown path |
| `online` | `RegisterWorkerConnection` (literal); sweeper `transition(...,"online")` via `SetWorkerStatus` | `internal/store/query/workers.sql:38`; `internal/metrics/sweep.go:78,90` |
| `stale` | sweeper `transition(...,"stale")` via `SetWorkerStatus` | `internal/metrics/sweep.go:76,90` |
| `revoked` | `ClearWorkerAgentToken` (literal `'revoked'`) | `internal/store/query/workers.sql:79` |

`UpdateWorkerStatus` (`workers.sql:26`) and `SetWorkerStatus` (`workers.sql:106`) take `$2`. Their Go callers pass only `online`/`offline`/`stale` (`internal/metrics/sweep.go:75-90`, `internal/worker/handler.go` teardown). The full set is the four above.

### `task_logs.stream` -> {stdout, stderr}

| Value | Written by | Citation |
| --- | --- | --- |
| `stdout` | `handleTaskLog` default branch (also the collapse target for the proto PREPARE stream, which is not STDERR) | `internal/worker/handler.go:508` |
| `stderr` | `handleTaskLog` when `chunk.Stream == LOG_STREAM_STDERR` | `internal/worker/handler.go:509-510` |

`AppendTaskLog` (`tasks.sql:48-56`) is the only insert into `task_logs`; its `Stream` parameter comes solely from `handler.go:508-515`, which can only produce `stdout` or `stderr`.

### `scheduled_jobs.overlap_policy` -> {skip, allow}

| Value | Source | Citation |
| --- | --- | --- |
| `skip` | column DEFAULT; documented default | `internal/store/migrations/000006_scheduled_jobs.up.sql:8`; `README.md:1282` |
| `allow` | documented accepted value | `README.md:1282` (`skip` (default) or `allow`) |

## Per-column decision: is existing-row cleanup needed before the constraint?

A constraint `ADD` fails if any existing row violates it. Decision rule: if every writer in the code (including the column DEFAULT) only ever produced values inside the allowed set, no production row can violate it, so **no cleanup**. If any writer could have produced an out-of-set value historically, the migration must normalize first.

| Column | Cleanup needed? | Reasoning |
| --- | --- | --- |
| `tasks.status` | No | Every writer is a literal or the bounded `handler.go` mapping. No path produces anything outside the set. |
| `jobs.status` | No | Only `UpdateJobStatus('cancelled')`, `UpdateJobStatusFromTasks` (running/done/failed), and DEFAULT pending. Bounded. |
| `jobs.priority` | **Yes, conditionally** | Because `jobspec.Validate` never validated priority, a typo could have been persisted historically. The migration must normalize any `jobs.priority` not in `{low,normal,high}` to `'normal'` before adding the constraint. This is the one column with a real risk of pre-existing drift. |
| `workers.status` | No | Only the four bounded writers above. |
| `task_logs.stream` | No | Only `stdout`/`stderr` from a single bounded site. |
| `scheduled_jobs.overlap_policy` | No | Written only from the validated schedule handlers; DEFAULT skip. (See risk note: confirm the schedule create/update handler validates against `{skip,allow}` before relying on "no cleanup".) |

Cleanup statement for `jobs.priority` (in the up migration, before the `ADD CONSTRAINT`):

```sql
UPDATE jobs SET priority = 'normal'
WHERE priority NOT IN ('low','normal','high');
```

This is safe and idempotent: it only touches rows that would otherwise break the constraint, and `normal` is the existing semantic default.

## Design

### Part 1: Migration `000019_status_vocabulary_checks`

Next migration number is **000019** (000018 is the latest; confirmed by listing `internal/store/migrations/`). Both `up` and `down` are required.

`000019_status_vocabulary_checks.up.sql`:

```sql
-- Normalize any historically-drifted priority before constraining (jobspec
-- never validated priority, so a typo could be persisted). All other columns
-- have only bounded writers and need no cleanup.
UPDATE jobs SET priority = 'normal'
WHERE priority NOT IN ('low','normal','high');

ALTER TABLE workers
  ADD CONSTRAINT workers_status_check
  CHECK (status IN ('online','offline','stale','revoked'));

ALTER TABLE jobs
  ADD CONSTRAINT jobs_status_check
  CHECK (status IN ('pending','running','done','failed','cancelled'));

ALTER TABLE jobs
  ADD CONSTRAINT jobs_priority_check
  CHECK (priority IN ('low','normal','high'));

ALTER TABLE tasks
  ADD CONSTRAINT tasks_status_check
  CHECK (status IN ('pending','dispatched','running','done','failed','timed_out'));

ALTER TABLE task_logs
  ADD CONSTRAINT task_logs_stream_check
  CHECK (stream IN ('stdout','stderr'));

ALTER TABLE scheduled_jobs
  ADD CONSTRAINT scheduled_jobs_overlap_policy_check
  CHECK (overlap_policy IN ('skip','allow'));
```

`000019_status_vocabulary_checks.down.sql`:

```sql
ALTER TABLE scheduled_jobs DROP CONSTRAINT IF EXISTS scheduled_jobs_overlap_policy_check;
ALTER TABLE task_logs      DROP CONSTRAINT IF EXISTS task_logs_stream_check;
ALTER TABLE tasks          DROP CONSTRAINT IF EXISTS tasks_status_check;
ALTER TABLE jobs           DROP CONSTRAINT IF EXISTS jobs_priority_check;
ALTER TABLE jobs           DROP CONSTRAINT IF EXISTS jobs_status_check;
ALTER TABLE workers        DROP CONSTRAINT IF EXISTS workers_status_check;
```

The down drops only the constraints. It does **not** restore the priority values it normalized (data normalization is not reversible and the old typos carried no meaning), and it does **not** restore the old `JobStatusCounts` query - see the separation note below.

Notes:
- `golang-migrate` wraps each migration in a transaction; plain `ALTER TABLE ... ADD CONSTRAINT` is fine (unlike `CREATE INDEX CONCURRENTLY`, which migration 000018 explicitly avoided for that reason - `000018_hot_path_indexes.up.sql:1-4`).
- `IF EXISTS` on the down keeps it idempotent, matching the 000018 convention.
- `ADD CONSTRAINT` takes an `ACCESS EXCLUSIVE` lock and a full table scan to validate. At current relay scale this is acceptable. If a future deployment has very large `tasks`/`task_logs` tables, the implementer may split into `ADD CONSTRAINT ... NOT VALID` + `VALIDATE CONSTRAINT`; this is not required now and is noted only as a scaling escape hatch.

### Part 2: Reconcile `JobStatusCounts`

The query lives in `internal/store/query/jobs.sql:282-292` and is regenerated by sqlc into `internal/store/jobs.sql.go`. Reconcile the SELECT vocabulary to the real `jobs.status` set.

Before / after bucket definitions:

| Bucket | Before (filter) | After (filter) | Why |
| --- | --- | --- | --- |
| `running` | `status IN ('running','dispatched')` | `status = 'running'` | `dispatched` is never a job status; it is dead. |
| `queued` | `status IN ('queued','pending')` | `status = 'pending'` | `queued` is never a job status. `pending` is the real "waiting" state. |
| `done_24h` | `status = 'done' AND window` | unchanged | `done` is correct. |
| `failed_24h` | `status IN ('failed','timed_out') AND window` | `status IN ('failed','cancelled') AND window` | `timed_out` is never a job status (it folds into `failed`); `cancelled` is a real terminal failure outcome currently counted nowhere. |

Decision - where does `cancelled` go? Two options: (a) add a dedicated `cancelled_24h` bucket, or (b) fold `cancelled` into `failed_24h`. **Recommend (b): fold into `failed_24h`.** Rationale: the KPI strip (`jobStatsResponse` in `internal/api/jobs.go:142-147`) has exactly four fields and the dashboard renders four counters; adding a fifth bucket is a frontend change, and this work is scoped backend-only. `cancelled` is a non-success terminal outcome, so counting it alongside `failed` keeps the "did not succeed in the last 24h" KPI honest without a schema/response/UI change. If the product later wants cancellations broken out, that is a separate, frontend-touching backlog item.

Reconciled query body:

```sql
SELECT
  COUNT(*) FILTER (WHERE status = 'running')                                                              AS running,
  COUNT(*) FILTER (WHERE status = 'pending')                                                              AS queued,
  COUNT(*) FILTER (WHERE status = 'done'                  AND updated_at >= NOW() - INTERVAL '24 hours')  AS done_24h,
  COUNT(*) FILTER (WHERE status IN ('failed','cancelled') AND updated_at >= NOW() - INTERVAL '24 hours')  AS failed_24h
FROM jobs;
```

The response field names (`running`, `queued`, `done_24h`, `failed_24h`) and the `jobStatsResponse` struct stay the same; only the underlying filters change. `queued` keeps its public name even though it now counts `pending` - renaming the JSON field would be an API-contract break and is out of scope.

After editing the `.sql` file, run `make generate` and apply the CLAUDE.md sqlc CRLF hygiene (`git diff --ignore-all-space`, revert LF-only hunks).

### Part 3: Validate `Priority` in `jobspec.Validate`

`jobspec.Validate` lives in `internal/jobspec/jobspec.go:71` (the `internal/api/job_spec.go` aliases re-export it; per the single-job-spec-pipeline invariant, every ingestion path - REST `POST /v1/jobs`, CLI, MCP, and schedrunner - flows through this one function, and `jobcreate.CreateJobFromSpec` also calls it at `internal/jobcreate/jobcreate.go:32`). Adding the check here covers all paths for free.

Priority validation rule:

```go
// Priority is optional. Empty is allowed (jobcreate defaults it to "normal").
// A non-empty value must be one of the known levels; this rejects typos that
// would otherwise be stored silently and break the priority CHECK constraint.
switch spec.Priority {
case "", "low", "normal", "high":
    // ok
default:
    return fmt.Errorf("invalid priority %q: must be low, normal, or high", spec.Priority)
}
```

Placement: at the top of `Validate`, alongside the existing name/tasks checks (before the per-task loop). Empty stays valid so existing clients that omit priority are unaffected; `jobcreate` continues to substitute `"normal"` for empty (`jobcreate.go:36-39`), so the value reaching the constrained column is always in `{low,normal,high}`.

This keeps the validator's allowed set and the migration's `jobs_priority_check` set identical - the central correctness requirement: the application-layer guard and the database guard must agree exactly.

## The migration / query separation (explicit)

Two different mechanisms, two different files, intentionally not coupled:

- **CHECK constraints** are schema and live in the migration (`000019`). The down migration drops them.
- **`JobStatusCounts`** is a query in `internal/store/query/jobs.sql`, regenerated by sqlc into `*.sql.go`. It is **not** in any migration. Reconciling it is a code change, versioned with the code, not the schema. The 000019 down migration therefore does **not** (and cannot) restore the old query - rolling back the schema and rolling back application code are independent operations. This is called out so the implementer does not try to embed query SQL in the down migration.

## Interaction with migration 000018 (just shipped)

`JobStatusCounts` got a covering index, `idx_jobs_status_updated` on `jobs(status, updated_at)`, in `000018_hot_path_indexes.up.sql:16-17`. This reconciliation changes **only the query's SELECT filter vocabulary**, not the columns it touches (`status`, `updated_at`). The index remains exactly as appropriate after the change. No index work is in scope here; do not modify 000018.

## Invariants check

- **Single job-spec pipeline.** Priority validation is added to the one shared `jobspec.Validate`, so REST/CLI/MCP/schedrunner all inherit it. No parallel validation path is introduced.
- **Epoch fence / single bounded sender / identity-checked teardown / no interior pointers / single JSON entry point.** Untouched. This change is additive schema + one query filter + one validation branch; it does not alter status-write call sites, locking, streams, or request decoding.

## Testing

- **Migration round-trip (integration).** Apply `000019` up then down on a seeded DB; assert constraints exist after up and are gone after down. Seed a `jobs.priority` typo row before up and assert the up normalizes it to `normal` (the only cleanup path).
- **Constraint rejection (integration).** Attempt a direct `INSERT`/`UPDATE` of an out-of-set value into each of the six columns and assert it errors. This is the regression guard the bug says was missing.
- **`JobStatusCounts` (integration).** Update the existing `internal/api/jobs_stats_integration_test.go`. See the critical risk below: that test currently seeds `dispatched`, `queued`, and `timed_out` directly into `jobs.status`, which the new `jobs_status_check` will reject. The test must be rewritten to seed only valid job statuses and to assert the new bucketing (`running` counts only `running`; `queued` counts only `pending`; `failed_24h` counts `failed` + `cancelled`).
- **Priority validation (unit).** In `internal/jobspec/jobspec_test.go`: empty -> ok, `low`/`normal`/`high` -> ok, `"hgih"` -> error. This needs no DB.

## Out of scope

- Frontend / dashboard changes (no new KPI bucket; `cancelled` folds into `failed_24h`).
- Renaming the `queued` response field (would break the API contract).
- Converting columns to native Postgres `ENUM` types (a `CHECK` is simpler to evolve - adding a value is one `ALTER`, no type juggling - and matches the existing free-`TEXT` storage the Go layer already maps).
- Index changes (000018 already covers the query).

## Risks the implementer must double-check

1. **The stats integration test will break the migration's own test run.** `internal/api/jobs_stats_integration_test.go:49-57` inserts `dispatched`, `queued`, `timed_out`, and `cancelled` jobs directly via SQL. With `jobs_status_check` applied, the `dispatched`/`queued`/`timed_out` inserts will fail with a constraint violation. **Rewriting this test is required scope, not optional** - it is the load-bearing reason the reconciliation and the constraint must land together in one change. Re-derive its expected counts from the new bucketing.
2. **Confirm `scheduled_jobs.overlap_policy` is validated at the handler before trusting "no cleanup."** The README documents `{skip,allow}` and the DEFAULT is `skip`, but verify the schedule create/update handler (`internal/api/scheduled_jobs.go`) rejects other values. If it does not, either add that validation or add a normalization `UPDATE` for `overlap_policy` to the up migration, mirroring the priority cleanup. The constraint must not be able to fail on an existing row.
3. **Priority set must match in two places.** The `jobspec.Validate` switch and the `jobs_priority_check` constraint both encode `{low,normal,high}`. If a future change adds a level (e.g. `urgent`), both must move together or writes will pass validation and then be rejected by the database. Note this coupling in a code comment at both sites.
4. **No status-string constant exists.** These vocabularies are scattered string literals across `internal/worker`, `internal/store/query`, `internal/metrics`, and `internal/api`. This spec does not introduce a shared constants package (out of scope, and the invariant set already governs the write paths). If the implementer is tempted to centralize them, that is a separate backlog item - flag it, do not fold it in.
