-- Add `preparing` to the tasks.status vocabulary and to the partial index that
-- covers the currently-assigned partition. BOTH objects move here, in one
-- migration, deliberately: Postgres uses a partial index only where the query
-- predicate implies the index predicate, so widening the statements without
-- widening this index makes it unusable for every one of them - including
-- CountActiveTasksByAllWorkers, which the dispatcher runs every cycle over the
-- whole tasks table. Pinned by
-- TestActiveTaskIndexPredicateMatchesTheAssignmentPartition.
--
-- Plain CREATE INDEX (no CONCURRENTLY): golang-migrate wraps each migration in a
-- transaction and CONCURRENTLY cannot run in one, exactly as 000018 notes. The
-- build takes a lock that blocks writes to tasks, and migrations run at server
-- startup - but the index is partial over the currently-assigned rows only, so
-- the build is bounded by live work rather than by history.
--
-- THE THREE-LINE ALTER SHAPE IS READ BY A TEST. tasksStatusVocabulary
-- (internal/worker/taskstatus_fence_counters_test.go) matches
-- `ADD CONSTRAINT tasks_status_check\s+CHECK \(status IN \(([^)]*)\)` across the
-- up-migrations. Anything other than whitespace between the constraint name and
-- CHECK - a trailing `--` comment on that line is the realistic case - makes the
-- parse miss this file, and the guard then reads an earlier migration's narrower
-- vocabulary while passing. That is a fail-open.
ALTER TABLE tasks DROP CONSTRAINT IF EXISTS tasks_status_check;

ALTER TABLE tasks
  ADD CONSTRAINT tasks_status_check
  CHECK (status IN ('pending','dispatched','preparing','running','done','failed','timed_out'));

DROP INDEX IF EXISTS idx_tasks_worker_active;

CREATE INDEX idx_tasks_worker_active
  ON tasks(worker_id) WHERE status IN ('dispatched', 'preparing', 'running');
