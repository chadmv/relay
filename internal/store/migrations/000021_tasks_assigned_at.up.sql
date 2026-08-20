-- assigned_at records when the CURRENT assignment began. It is the clock the
-- coordinator-side stale-task watchdog measures its absolute bound from
-- (internal/scheduler/watchdog.go, ListOverdueAssignedTasks).
--
-- Why a new column rather than an existing timestamp: tasks had exactly three,
-- and none of them bounds a `dispatched` row. started_at is written only on the
-- `running` transition, so it is NULL for a task still syncing its workspace;
-- finished_at is NULL until terminal; created_at is JOB SUBMISSION time, so a
-- task that queued six hours behind a busy fleet and was dispatched a minute ago
-- is six hours old by it. Keying the absolute bound on created_at would kill
-- healthy, just-dispatched work.
--
-- NULLABLE, no default. NULL means "holds no assignment". A NOT NULL DEFAULT
-- NOW() would stamp a meaningless value on every never-claimed task and destroy
-- that meaning. The column is written exactly where worker_id is written: set by
-- ClaimTaskForWorker from a Go-supplied parameter, nulled by every statement
-- that nulls worker_id, and untouched by UpdateTaskStatus (whose worker_id
-- argument is a fence, not a value).
ALTER TABLE tasks ADD COLUMN assigned_at TIMESTAMPTZ NULL;

-- Backfill with NOW() deliberately, so every assignment that is in flight at
-- deploy time gets a FRESH clock. Leaving these NULL would be fail-closed but
-- permanently so: the watchdog's absolute arm requires assigned_at IS NOT NULL,
-- so a row that is `dispatched` at deploy and never reaches `running` would be
-- exempt from both arms forever - exactly the case this column exists to cover.
-- This is the only database-clock write of assigned_at, and it happens once,
-- before any watchdog exists.
UPDATE tasks SET assigned_at = NOW() WHERE status IN ('dispatched', 'running');
