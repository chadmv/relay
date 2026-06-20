-- name: CreateTask :one
INSERT INTO tasks (job_id, name, commands, env, requires, timeout_seconds, retries)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetTask :one
SELECT * FROM tasks WHERE id = $1;

-- name: ListTasksByJob :many
SELECT * FROM tasks WHERE job_id = $1 ORDER BY created_at;

-- name: UpdateTaskStatus :one
-- Updates a task's status only if the caller's epoch matches the current
-- assignment. Returns pgx.ErrNoRows if the caller's epoch is stale (zombie
-- status update from a prior assignment).
UPDATE tasks
SET status = $2, worker_id = $3, started_at = $4, finished_at = $5
WHERE id = $1 AND assignment_epoch = $6
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

-- name: AppendTaskLog :exec
-- Inserts a log chunk only if the caller's epoch matches the task's current
-- assignment. Stale chunks (from a reassigned generation) silently insert
-- zero rows.
INSERT INTO task_logs (task_id, stream, content)
SELECT $1, $2, $3
WHERE EXISTS (
    SELECT 1 FROM tasks WHERE id = $1 AND assignment_epoch = $4
);

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
