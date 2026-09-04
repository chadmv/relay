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
-- ADD CONSTRAINT is not NOT VALID either, so it validates the existing rows. The
-- leading DROP CONSTRAINT takes ACCESS EXCLUSIVE on tasks and the one wrapping
-- transaction holds it to commit, so READS as well as writes are blocked for the
-- whole file - not just by the ADD CONSTRAINT, which takes that lock itself, but
-- from the first statement on. The ADD CONSTRAINT and the CREATE INDEX each scan
-- the whole table, at server startup, and that window grows with the history in
-- tasks, not with how much work is live.
--
-- DO NOT PUT A TRAILING `--` COMMENT ON THE ADD CONSTRAINT LINE, and keep CHECK
-- adjacent to it: tasksStatusVocabularyIn
-- (internal/worker/taskstatus_fence_counters_test.go) parses this file for the
-- status vocabulary.
ALTER TABLE tasks DROP CONSTRAINT IF EXISTS tasks_status_check;

ALTER TABLE tasks
  ADD CONSTRAINT tasks_status_check
  CHECK (status IN ('pending','dispatched','preparing','running','done','failed','timed_out'));

DROP INDEX IF EXISTS idx_tasks_worker_active;

CREATE INDEX idx_tasks_worker_active
  ON tasks(worker_id) WHERE status IN ('dispatched', 'preparing', 'running');
