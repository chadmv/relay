-- Add `preparing` to the tasks.status vocabulary and to the partial index that
-- covers the currently-assigned partition. BOTH objects move here, in one
-- migration, deliberately: Postgres uses a partial index only where the query
-- predicate implies the index predicate, so widening the statements without
-- widening this index makes it unusable for every one of them - including
-- CountActiveTasksByAllWorkers, which the dispatcher runs every cycle over the
-- whole tasks table. Pinned by
-- TestActiveTaskIndexPredicateNamesTheExpectedStatuses.
--
-- Plain CREATE INDEX (no CONCURRENTLY): golang-migrate wraps each migration in a
-- transaction and CONCURRENTLY cannot run in one, exactly as 000018 notes. The
-- ADD CONSTRAINT is not NOT VALID either, so it validates the existing rows. Both
-- statements therefore take ACCESS EXCLUSIVE on tasks - blocking READS as well as
-- writes - and each scans the whole table, at server startup. The window grows
-- with the history in tasks, not with how much work is live.
--
-- DO NOT PUT A TRAILING `--` COMMENT ON THE ADD CONSTRAINT LINE, and keep CHECK
-- adjacent to it: tasksStatusVocabulary
-- (internal/worker/taskstatus_fence_counters_test.go) parses this file for the
-- status vocabulary, and a definition it cannot see is a definition it silently
-- replaces with an older migration's.
ALTER TABLE tasks DROP CONSTRAINT IF EXISTS tasks_status_check;

ALTER TABLE tasks
  ADD CONSTRAINT tasks_status_check
  CHECK (status IN ('pending','dispatched','preparing','running','done','failed','timed_out'));

DROP INDEX IF EXISTS idx_tasks_worker_active;

CREATE INDEX idx_tasks_worker_active
  ON tasks(worker_id) WHERE status IN ('dispatched', 'preparing', 'running');
