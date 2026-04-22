-- name: CreateTask :one
INSERT INTO tasks (job_id, name, command, env, requires, timeout_seconds, retries)
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
SET retry_count = retry_count + 1, status = 'pending', worker_id = NULL, started_at = NULL, finished_at = NULL
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
    UNION ALL
    SELECT td.task_id FROM task_dependencies td
    JOIN blocked b ON td.depends_on_task_id = b.task_id
)
UPDATE tasks
SET status = 'failed', finished_at = NOW()
WHERE status = 'pending'
  AND id IN (SELECT task_id FROM blocked);

-- name: CountActiveTasksForWorker :one
SELECT COUNT(*) FROM tasks
WHERE worker_id = $1 AND status IN ('dispatched', 'running');

-- name: RequeueWorkerTasks :exec
-- Re-queue dispatched/running tasks for a worker that has disconnected.
UPDATE tasks
SET status = 'pending', worker_id = NULL, started_at = NULL
WHERE worker_id = $1 AND status IN ('dispatched', 'running');

-- name: RequeueAllActiveTasks :exec
-- Called on coordinator startup to recover from an unclean shutdown.
UPDATE tasks
SET status = 'pending', worker_id = NULL, started_at = NULL
WHERE status IN ('dispatched', 'running');

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
UPDATE tasks
SET status = 'pending', worker_id = NULL, started_at = NULL
WHERE id = $1 AND status = 'dispatched';

-- name: GetActiveTasksForWorker :many
-- Returns all non-terminal tasks currently assigned to a given worker.
-- Used at reconcile time to compare server's view with the agent's
-- running_tasks report.
SELECT id, assignment_epoch
FROM tasks
WHERE worker_id = $1 AND status IN ('dispatched', 'running')
ORDER BY id;

-- name: ListWorkersWithActiveTasks :many
-- Returns the distinct set of worker IDs with at least one non-terminal
-- task assigned. Used at server startup to seed grace timers.
SELECT DISTINCT worker_id
FROM tasks
WHERE worker_id IS NOT NULL AND status IN ('dispatched', 'running');

-- name: RequeueTaskByID :exec
-- Revert a single task back to 'pending' regardless of current status.
-- Used by the reconcile path when the coordinator has a task assigned
-- that the agent didn't report as running.
UPDATE tasks
SET status = 'pending',
    worker_id = NULL,
    started_at = NULL,
    finished_at = NULL
WHERE id = $1 AND status IN ('dispatched', 'running');

-- name: NotifyTaskSubmitted :exec
-- Wakes any LISTENers on relay_task_submitted. Payload is empty; listeners
-- coalesce into a single dispatch trigger.
SELECT pg_notify('relay_task_submitted', '');

-- name: NotifyTaskCompleted :exec
-- Wakes any LISTENers on relay_task_completed.
SELECT pg_notify('relay_task_completed', '');
