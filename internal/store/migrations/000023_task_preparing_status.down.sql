-- Reverse 000023. THE ORDER BELOW IS THE CORRECTNESS ARGUMENT.
--
-- The UPDATE must precede the narrowed ADD CONSTRAINT, or the constraint add
-- fails against any existing `preparing` row and this migration is unrunnable
-- against a real database. Pinned by
-- TestMigration000023_DownDemotesPreparingRowsAndNarrowsTheConstraint, which
-- seeds such a row precisely so the ordering is the thing under test; against an
-- empty tasks table the statements run in any order.
--
-- Demotion is to `dispatched` and not to `pending`: the row still has a live
-- agent, a worker_id and an assignment_epoch, and `dispatched` is the state that
-- described it truthfully in the narrower vocabulary. Demoting to `pending`
-- would end a live assignment without bumping the epoch.
UPDATE tasks SET status = 'dispatched' WHERE status = 'preparing';

DROP INDEX IF EXISTS idx_tasks_worker_active;

CREATE INDEX idx_tasks_worker_active
  ON tasks(worker_id) WHERE status IN ('dispatched', 'running');

ALTER TABLE tasks DROP CONSTRAINT IF EXISTS tasks_status_check;

ALTER TABLE tasks
  ADD CONSTRAINT tasks_status_check
  CHECK (status IN ('pending','dispatched','running','done','failed','timed_out'));
