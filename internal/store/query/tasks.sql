-- name: CreateTask :one
INSERT INTO tasks (job_id, name, commands, env, requires, timeout_seconds, retries)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetTask :one
SELECT * FROM tasks WHERE id = $1;

-- name: ListTasksByJob :many
SELECT * FROM tasks WHERE job_id = $1 ORDER BY created_at;

-- name: UpdateTaskStatus :one
-- Updates a task's status only if BOTH fence predicates hold: the task is
-- currently assigned to the caller's worker (identity), and the caller's epoch
-- matches the current assignment (currency). The epoch answers "is this
-- generation current"; the worker id answers "are you who you say you are".
-- Neither substitutes for the other - do not delete either.
-- This statement no longer writes worker_id; the argument is a fence, not a
-- value. That makes the old contract ("callers MUST pass the task's existing
-- worker_id through, because clearing it would strand a live agent forever")
-- structural rather than documented: the statement can no longer clear the
-- column at all. It does not bump assignment_epoch either, so a terminal task
-- keeps its assignee and trailing log chunks from the agent that just finished
-- still pass AppendTaskLog's fence.
-- The worker_id comparison must stay a plain `=`. tasks.worker_id is NULLABLE,
-- so `=` makes a never-claimed task reject every update, which is the hole this
-- predicate closes, and makes a caller that lost its identity (a zero-value
-- pgtype.UUID binds SQL NULL) fail closed. `IS NOT DISTINCT FROM` would let a
-- NULL parameter match a NULL worker_id and re-open it. Do not "fix the NULL
-- bug" here.
-- Both callers are fenced by the same statement deliberately.
-- Dispatcher.failClaimedTask passes claimed.WorkerID from ClaimTaskForWorker,
-- which is non-NULL by construction, so the predicate is tautological there -
-- that is the point, and it fails closed and loudly (that caller already logs
-- any error including pgx.ErrNoRows). A separate un-fenced query for the
-- server-internal path would leave a second, unfenced writer to tasks.status
-- that a future caller could pick by mistake, and a sentinel meaning "skip the
-- check" would be reachable by any caller that merely failed to resolve its
-- identity. See docs/superpowers/specs/2026-08-12-taskstatus-update-assignee-fence.md.
-- This predicate is NOT sufficient on its own: handleTaskStatus's retry branch
-- calls IncrementTaskRetryCount (bare `WHERE id = $1`) and returns before ever
-- reaching this statement, so the identity check also lives in Go, ahead of
-- every side effect. Do not delete that one as redundant with this one.
UPDATE tasks
SET status = sqlc.arg(status),
    started_at = sqlc.arg(started_at),
    finished_at = sqlc.arg(finished_at)
WHERE id = sqlc.arg(id)
  AND assignment_epoch = sqlc.arg(assignment_epoch)
  AND worker_id = sqlc.arg(worker_id)
RETURNING *;

-- name: IncrementTaskRetryCount :one
UPDATE tasks
SET retry_count = retry_count + 1, status = 'pending', worker_id = NULL, started_at = NULL, finished_at = NULL,
    assignment_epoch = assignment_epoch + 1
WHERE id = $1
RETURNING *;

-- name: GetEligibleTasks :many
-- Tasks that are pending and have no unfinished dependencies.
SELECT t.* FROM tasks t
WHERE t.status = 'pending'
  AND NOT EXISTS (
    SELECT 1 FROM task_dependencies td
    JOIN tasks dep ON dep.id = td.depends_on_task_id
    WHERE td.task_id = t.id
      AND dep.status != 'done'
  )
ORDER BY t.created_at;

-- name: CreateTaskDependency :exec
INSERT INTO task_dependencies (task_id, depends_on_task_id)
VALUES ($1, $2)
ON CONFLICT DO NOTHING;

-- name: GetTaskDependencies :many
SELECT depends_on_task_id FROM task_dependencies WHERE task_id = $1;

-- name: AppendTaskLog :one
-- Inserts a log chunk only if BOTH fence predicates hold: the task is currently
-- assigned to the sending worker (identity), and the caller's epoch matches the
-- task's current assignment (currency). The epoch answers "is this generation
-- current"; the worker id answers "are you who you say you are". Neither
-- substitutes for the other, and neither is redundant - do not delete either.
-- Returns the inserted row's id (the seq the polling endpoint pages by) plus
-- created_at plus the task's job_id - all from one round trip, because this runs
-- synchronously on the agent's gRPC recv goroutine and a second query here would
-- delay that worker's status and telemetry ingest too.
-- A chunk failing EITHER predicate - a stale chunk from a reassigned or
-- cancelled generation, or a chunk from an agent that is not this task's
-- assignee - matches no fence row, inserts nothing, and returns zero rows ->
-- pgx.ErrNoRows. Callers must treat ErrNoRows as "one or both checks failed:
-- drop silently, do not publish" and any other error as a real failure worth
-- logging. The two cases are deliberately indistinguishable here; see the spec.
-- The worker_id comparison must stay a plain `=`. tasks.worker_id is NULLABLE,
-- so `=` makes a never-claimed task (worker_id NULL) reject every append, which
-- is exactly the hole this predicate closes, and makes a caller that lost its
-- own identity (a zero-value pgtype.UUID binds SQL NULL) fail closed. Rewriting
-- it as `IS NOT DISTINCT FROM` would let a NULL parameter match a NULL
-- worker_id and re-open that hole. Do not "fix the NULL bug" here.
-- The tasks alias and the qualified column references are load-bearing: without
-- them sqlc's analyzer cannot resolve "id" across the two CTEs and fails with
-- 'column reference "id" is ambiguous'. Only job_id is selected because that is
-- all the publish needs; the fence's job is to yield exactly one row, or none.
WITH fence AS (
    SELECT t.job_id FROM tasks t
    WHERE t.id = sqlc.arg(task_id)
      AND t.assignment_epoch = sqlc.arg(assignment_epoch)
      AND t.worker_id = sqlc.arg(worker_id)
), ins AS (
    INSERT INTO task_logs (task_id, stream, content)
    SELECT sqlc.arg(task_id), sqlc.arg(stream), sqlc.arg(content) FROM fence
    RETURNING id, created_at
)
SELECT ins.id, ins.created_at, fence.job_id FROM ins, fence;

-- name: GetTaskLogs :many
SELECT * FROM task_logs WHERE task_id = $1 ORDER BY id;

-- name: FailDependentTasks :exec
-- Mark all tasks that transitively depend on a failed task as failed.
-- Uses a recursive CTE to walk the full dependency chain.
-- Call this after marking a task as failed.
WITH RECURSIVE blocked AS (
    SELECT task_id FROM task_dependencies WHERE depends_on_task_id = sqlc.arg(failed_task_id)::uuid
    UNION
    SELECT td.task_id FROM task_dependencies td
    JOIN blocked b ON td.depends_on_task_id = b.task_id
)
UPDATE tasks
SET status = 'failed', finished_at = NOW()
WHERE status = 'pending'
  AND id IN (SELECT task_id FROM blocked);

-- name: ClaimTaskForWorker :one
-- Atomically transition a pending task to 'dispatched' on the given worker.
-- Increments assignment_epoch so subsequent status updates from prior
-- generations can be rejected. Returns pgx.ErrNoRows if the task is no longer
-- pending (another dispatcher already claimed it, or the row vanished).
UPDATE tasks
SET status = 'dispatched',
    worker_id = $2,
    assignment_epoch = assignment_epoch + 1
WHERE id = $1 AND status = 'pending'
RETURNING *;

-- name: RequeueTask :exec
-- Revert a single task from 'dispatched' back to 'pending'.
-- Used when the registry send fails after the task has been claimed.
-- Bumps assignment_epoch so a late update from the prior assignment is fenced out.
UPDATE tasks
SET status = 'pending', worker_id = NULL, started_at = NULL,
    assignment_epoch = assignment_epoch + 1
WHERE id = $1 AND status = 'dispatched';

-- name: GetActiveTasksForWorker :many
-- Returns all non-terminal tasks currently assigned to a given worker.
-- Used at reconcile time to compare server's view with the agent's
-- running_tasks report.
SELECT id, assignment_epoch
FROM tasks
WHERE worker_id = $1 AND status IN ('dispatched', 'running')
ORDER BY id;

-- name: ListGraceCandidates :many
-- Returns id and disconnected_at for each worker that has at least one
-- non-terminal task assigned. Used at server startup to seed grace timers
-- with the correct remaining duration based on persisted disconnect time.
SELECT DISTINCT w.id, w.disconnected_at, w.connection_epoch
FROM workers w
JOIN tasks t ON t.worker_id = w.id
WHERE t.status IN ('dispatched', 'running');

-- name: RequeueTaskByID :exec
-- Revert a single task back to 'pending' regardless of current status.
-- Used by the reconcile path when the coordinator has a task assigned
-- that the agent didn't report as running.
-- Bumps assignment_epoch so a late update from the prior assignment is fenced out.
UPDATE tasks
SET status = 'pending',
    worker_id = NULL,
    started_at = NULL,
    finished_at = NULL,
    assignment_epoch = assignment_epoch + 1
WHERE id = $1 AND status IN ('dispatched', 'running');

-- name: NotifyTaskSubmitted :exec
-- Wakes any LISTENers on relay_task_submitted. Payload is empty; listeners
-- coalesce into a single dispatch trigger.
SELECT pg_notify('relay_task_submitted', '');

-- name: NotifyTaskCompleted :exec
-- Wakes any LISTENers on relay_task_completed.
SELECT pg_notify('relay_task_completed', '');

-- name: CountActiveTasksByAllWorkers :many
-- Per-worker count of non-terminal tasks. Used by the dispatcher to compute
-- available slots in one query rather than N per cycle.
SELECT worker_id, count(*)::bigint AS active
FROM tasks
WHERE worker_id IS NOT NULL
  AND status IN ('dispatched', 'running')
GROUP BY worker_id;

-- name: CreateTaskWithSource :one
INSERT INTO tasks (job_id, name, commands, env, requires, timeout_seconds, retries, source)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: UpdateTaskStatusEpoch :one
-- Updates a task's status only if the caller's epoch matches the current
-- assignment_epoch. Returns pgx.ErrNoRows if the epoch is stale.
UPDATE tasks
SET status = sqlc.arg(status)
WHERE id = sqlc.arg(id) AND assignment_epoch = sqlc.arg(epoch)
RETURNING *;

-- name: GetTaskLogsPage :many
-- Returns up to $3 log rows for the task with id > $2, ordered ascending.
SELECT id, task_id, stream, content, created_at
FROM task_logs
WHERE task_id = $1 AND id > $2
ORDER BY id
LIMIT $3;

-- name: CountTaskLogs :one
SELECT COUNT(*) FROM task_logs WHERE task_id = $1;

-- name: CancelJobTasks :exec
-- Mark every non-terminal task of a job as failed when the job is cancelled.
-- Bumps assignment_epoch so any in-flight status update or log chunk from the
-- assigned agent is rejected by the epoch fence. Unlike UpdateTaskStatus this
-- does not fence on the caller's epoch: the cancel handler does not track each
-- task's current generation, and cancellation ends the assignment regardless.
UPDATE tasks
SET status = 'failed',
    worker_id = NULL,
    finished_at = NOW(),
    assignment_epoch = assignment_epoch + 1
WHERE job_id = $1 AND status IN ('pending', 'queued', 'running', 'dispatched');

-- name: RequeueWorkerTasks :many
-- Re-queue dispatched/running tasks for a worker that has disconnected or is
-- being disabled. Bumps assignment_epoch so a stale status update or log chunk
-- from the (possibly still-connected) agent is rejected by the epoch fence.
-- Returns the affected task ids; the disable path uses them to send cancels,
-- the disconnect/grace paths discard them.
UPDATE tasks
SET status = 'pending',
    worker_id = NULL,
    started_at = NULL,
    assignment_epoch = assignment_epoch + 1
WHERE worker_id = $1 AND status IN ('dispatched', 'running')
RETURNING id;

-- name: RequeueWorkerTasksIfEpoch :many
-- Re-queue dispatched/running tasks for a disconnected worker, but only if the
-- worker's connection_epoch still matches the epoch the caller owned. If a fresh
-- connection has superseded it, the EXISTS guard fails and zero tasks move.
-- Bumps assignment_epoch on each requeued task (task-level fence preserved).
UPDATE tasks
SET status = 'pending',
    worker_id = NULL,
    started_at = NULL,
    assignment_epoch = assignment_epoch + 1
WHERE worker_id = $1 AND status IN ('dispatched', 'running')
  AND EXISTS (SELECT 1 FROM workers w WHERE w.id = $1 AND w.connection_epoch = $2)
RETURNING id;
