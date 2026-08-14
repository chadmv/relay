-- name: CreateJob :one
INSERT INTO jobs (name, priority, submitted_by, labels, scheduled_job_id)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetJob :one
SELECT * FROM jobs WHERE id = $1;

-- name: GetJobWithEmail :one
SELECT j.*, u.email AS submitted_by_email
FROM jobs j
JOIN users u ON u.id = j.submitted_by
WHERE j.id = $1;

-- name: ListJobsWithEmailPage :many
SELECT j.*, u.email AS submitted_by_email,
       ts.total_tasks, ts.done_tasks, ts.started_at, ts.finished_at,
       sj.name AS scheduled_job_name
FROM jobs j
JOIN users u ON u.id = j.submitted_by
LEFT JOIN LATERAL (
  SELECT COUNT(*)                                  AS total_tasks,
         COUNT(*) FILTER (WHERE t.status = 'done') AS done_tasks,
         MIN(t.started_at)::timestamptz            AS started_at,
         MAX(t.finished_at)::timestamptz           AS finished_at
  FROM tasks t WHERE t.job_id = j.id
) ts ON TRUE
LEFT JOIN scheduled_jobs sj ON sj.id = j.scheduled_job_id
WHERE (sqlc.arg(cursor_set)::bool = FALSE
       OR (j.created_at, j.id) < (sqlc.arg(cursor_ts)::timestamptz, sqlc.arg(cursor_id)::uuid))
ORDER BY j.created_at DESC, j.id DESC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: CountJobs :one
SELECT COUNT(*) FROM jobs;

-- name: ListJobsByStatusWithEmailPage :many
SELECT j.*, u.email AS submitted_by_email,
       ts.total_tasks, ts.done_tasks, ts.started_at, ts.finished_at,
       sj.name AS scheduled_job_name
FROM jobs j
JOIN users u ON u.id = j.submitted_by
LEFT JOIN LATERAL (
  SELECT COUNT(*)                                  AS total_tasks,
         COUNT(*) FILTER (WHERE t.status = 'done') AS done_tasks,
         MIN(t.started_at)::timestamptz            AS started_at,
         MAX(t.finished_at)::timestamptz           AS finished_at
  FROM tasks t WHERE t.job_id = j.id
) ts ON TRUE
LEFT JOIN scheduled_jobs sj ON sj.id = j.scheduled_job_id
WHERE j.status = sqlc.arg(status)::text
  AND (sqlc.arg(cursor_set)::bool = FALSE
       OR (j.created_at, j.id) < (sqlc.arg(cursor_ts)::timestamptz, sqlc.arg(cursor_id)::uuid))
ORDER BY j.created_at DESC, j.id DESC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: CountJobsByStatus :one
SELECT COUNT(*) FROM jobs WHERE status = $1;

-- name: ListJobsByScheduledJobWithEmailPage :many
SELECT j.*, u.email AS submitted_by_email,
       ts.total_tasks, ts.done_tasks, ts.started_at, ts.finished_at,
       sj.name AS scheduled_job_name
FROM jobs j
JOIN users u ON u.id = j.submitted_by
LEFT JOIN LATERAL (
  SELECT COUNT(*)                                  AS total_tasks,
         COUNT(*) FILTER (WHERE t.status = 'done') AS done_tasks,
         MIN(t.started_at)::timestamptz            AS started_at,
         MAX(t.finished_at)::timestamptz           AS finished_at
  FROM tasks t WHERE t.job_id = j.id
) ts ON TRUE
LEFT JOIN scheduled_jobs sj ON sj.id = j.scheduled_job_id
WHERE j.scheduled_job_id = sqlc.arg(scheduled_job_id)::uuid
  AND (sqlc.arg(cursor_set)::bool = FALSE
       OR (j.created_at, j.id) < (sqlc.arg(cursor_ts)::timestamptz, sqlc.arg(cursor_id)::uuid))
ORDER BY j.created_at DESC, j.id DESC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: CountJobsByScheduledJob :one
SELECT COUNT(*) FROM jobs WHERE scheduled_job_id = $1;

-- name: UpdateJobStatus :one
UPDATE jobs
SET status = $2, updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: RecomputeJobStatus :one
-- Atomically recomputes a job's status from its tasks in a single statement,
-- so concurrent last-task completions can never strand the job in 'running'.
-- Returns the new status. Returns pgx.ErrNoRows if the job has no tasks
-- (the subquery's aggregate is empty), matching the old helper's "" behavior.
UPDATE jobs j
SET status = sub.next, updated_at = NOW()
FROM (
    SELECT CASE
        WHEN COUNT(*) FILTER (WHERE status NOT IN ('done','failed','timed_out')) > 0 THEN 'running'
        WHEN COUNT(*) FILTER (WHERE status = 'done') = COUNT(*) THEN 'done'
        ELSE 'failed'
    END AS next
    FROM tasks
    WHERE job_id = $1
    HAVING COUNT(*) > 0
) sub
WHERE j.id = $1
RETURNING j.status;

-- name: DeleteJob :exec
DELETE FROM jobs WHERE id = $1;

-- name: ListJobsByScheduledJob :many
-- Internal use only (schedrunner tests). Not paginated.
SELECT j.*, u.email AS submitted_by_email
FROM jobs j
JOIN users u ON u.id = j.submitted_by
WHERE j.scheduled_job_id = $1
ORDER BY j.created_at DESC;

-- name: ListJobsWithEmailPageByCreatedAsc :many
SELECT j.*, u.email AS submitted_by_email,
       ts.total_tasks, ts.done_tasks, ts.started_at, ts.finished_at,
       sj.name AS scheduled_job_name
FROM jobs j
JOIN users u ON u.id = j.submitted_by
LEFT JOIN LATERAL (
  SELECT COUNT(*)                                  AS total_tasks,
         COUNT(*) FILTER (WHERE t.status = 'done') AS done_tasks,
         MIN(t.started_at)::timestamptz            AS started_at,
         MAX(t.finished_at)::timestamptz           AS finished_at
  FROM tasks t WHERE t.job_id = j.id
) ts ON TRUE
LEFT JOIN scheduled_jobs sj ON sj.id = j.scheduled_job_id
WHERE NOT @cursor_set::bool OR (j.created_at, j.id) > (@cursor_ts::timestamptz, @cursor_id::uuid)
ORDER BY j.created_at ASC, j.id ASC
LIMIT @page_limit + 1;

-- name: ListJobsWithEmailPageByNameDesc :many
SELECT j.*, u.email AS submitted_by_email,
       ts.total_tasks, ts.done_tasks, ts.started_at, ts.finished_at,
       sj.name AS scheduled_job_name
FROM jobs j
JOIN users u ON u.id = j.submitted_by
LEFT JOIN LATERAL (
  SELECT COUNT(*)                                  AS total_tasks,
         COUNT(*) FILTER (WHERE t.status = 'done') AS done_tasks,
         MIN(t.started_at)::timestamptz            AS started_at,
         MAX(t.finished_at)::timestamptz           AS finished_at
  FROM tasks t WHERE t.job_id = j.id
) ts ON TRUE
LEFT JOIN scheduled_jobs sj ON sj.id = j.scheduled_job_id
WHERE NOT @cursor_set::bool OR (j.name, j.id) < (@cursor_v::text, @cursor_id::uuid)
ORDER BY j.name DESC, j.id DESC
LIMIT @page_limit + 1;

-- name: ListJobsWithEmailPageByNameAsc :many
SELECT j.*, u.email AS submitted_by_email,
       ts.total_tasks, ts.done_tasks, ts.started_at, ts.finished_at,
       sj.name AS scheduled_job_name
FROM jobs j
JOIN users u ON u.id = j.submitted_by
LEFT JOIN LATERAL (
  SELECT COUNT(*)                                  AS total_tasks,
         COUNT(*) FILTER (WHERE t.status = 'done') AS done_tasks,
         MIN(t.started_at)::timestamptz            AS started_at,
         MAX(t.finished_at)::timestamptz           AS finished_at
  FROM tasks t WHERE t.job_id = j.id
) ts ON TRUE
LEFT JOIN scheduled_jobs sj ON sj.id = j.scheduled_job_id
WHERE NOT @cursor_set::bool OR (j.name, j.id) > (@cursor_v::text, @cursor_id::uuid)
ORDER BY j.name ASC, j.id ASC
LIMIT @page_limit + 1;

-- name: ListJobsWithEmailPageByPriorityDesc :many
SELECT j.*, u.email AS submitted_by_email,
       ts.total_tasks, ts.done_tasks, ts.started_at, ts.finished_at,
       sj.name AS scheduled_job_name
FROM jobs j
JOIN users u ON u.id = j.submitted_by
LEFT JOIN LATERAL (
  SELECT COUNT(*)                                  AS total_tasks,
         COUNT(*) FILTER (WHERE t.status = 'done') AS done_tasks,
         MIN(t.started_at)::timestamptz            AS started_at,
         MAX(t.finished_at)::timestamptz           AS finished_at
  FROM tasks t WHERE t.job_id = j.id
) ts ON TRUE
LEFT JOIN scheduled_jobs sj ON sj.id = j.scheduled_job_id
WHERE NOT @cursor_set::bool OR (j.priority, j.id) < (@cursor_v::text, @cursor_id::uuid)
ORDER BY j.priority DESC, j.id DESC
LIMIT @page_limit + 1;

-- name: ListJobsWithEmailPageByPriorityAsc :many
SELECT j.*, u.email AS submitted_by_email,
       ts.total_tasks, ts.done_tasks, ts.started_at, ts.finished_at,
       sj.name AS scheduled_job_name
FROM jobs j
JOIN users u ON u.id = j.submitted_by
LEFT JOIN LATERAL (
  SELECT COUNT(*)                                  AS total_tasks,
         COUNT(*) FILTER (WHERE t.status = 'done') AS done_tasks,
         MIN(t.started_at)::timestamptz            AS started_at,
         MAX(t.finished_at)::timestamptz           AS finished_at
  FROM tasks t WHERE t.job_id = j.id
) ts ON TRUE
LEFT JOIN scheduled_jobs sj ON sj.id = j.scheduled_job_id
WHERE NOT @cursor_set::bool OR (j.priority, j.id) > (@cursor_v::text, @cursor_id::uuid)
ORDER BY j.priority ASC, j.id ASC
LIMIT @page_limit + 1;

-- name: ListJobsWithEmailPageByStatusDesc :many
SELECT j.*, u.email AS submitted_by_email,
       ts.total_tasks, ts.done_tasks, ts.started_at, ts.finished_at,
       sj.name AS scheduled_job_name
FROM jobs j
JOIN users u ON u.id = j.submitted_by
LEFT JOIN LATERAL (
  SELECT COUNT(*)                                  AS total_tasks,
         COUNT(*) FILTER (WHERE t.status = 'done') AS done_tasks,
         MIN(t.started_at)::timestamptz            AS started_at,
         MAX(t.finished_at)::timestamptz           AS finished_at
  FROM tasks t WHERE t.job_id = j.id
) ts ON TRUE
LEFT JOIN scheduled_jobs sj ON sj.id = j.scheduled_job_id
WHERE NOT @cursor_set::bool OR (j.status, j.id) < (@cursor_v::text, @cursor_id::uuid)
ORDER BY j.status DESC, j.id DESC
LIMIT @page_limit + 1;

-- name: ListJobsWithEmailPageByStatusAsc :many
SELECT j.*, u.email AS submitted_by_email,
       ts.total_tasks, ts.done_tasks, ts.started_at, ts.finished_at,
       sj.name AS scheduled_job_name
FROM jobs j
JOIN users u ON u.id = j.submitted_by
LEFT JOIN LATERAL (
  SELECT COUNT(*)                                  AS total_tasks,
         COUNT(*) FILTER (WHERE t.status = 'done') AS done_tasks,
         MIN(t.started_at)::timestamptz            AS started_at,
         MAX(t.finished_at)::timestamptz           AS finished_at
  FROM tasks t WHERE t.job_id = j.id
) ts ON TRUE
LEFT JOIN scheduled_jobs sj ON sj.id = j.scheduled_job_id
WHERE NOT @cursor_set::bool OR (j.status, j.id) > (@cursor_v::text, @cursor_id::uuid)
ORDER BY j.status ASC, j.id ASC
LIMIT @page_limit + 1;

-- name: ListJobsWithEmailPageByUpdatedDesc :many
SELECT j.*, u.email AS submitted_by_email,
       ts.total_tasks, ts.done_tasks, ts.started_at, ts.finished_at,
       sj.name AS scheduled_job_name
FROM jobs j
JOIN users u ON u.id = j.submitted_by
LEFT JOIN LATERAL (
  SELECT COUNT(*)                                  AS total_tasks,
         COUNT(*) FILTER (WHERE t.status = 'done') AS done_tasks,
         MIN(t.started_at)::timestamptz            AS started_at,
         MAX(t.finished_at)::timestamptz           AS finished_at
  FROM tasks t WHERE t.job_id = j.id
) ts ON TRUE
LEFT JOIN scheduled_jobs sj ON sj.id = j.scheduled_job_id
WHERE NOT @cursor_set::bool OR (j.updated_at, j.id) < (@cursor_ts::timestamptz, @cursor_id::uuid)
ORDER BY j.updated_at DESC, j.id DESC
LIMIT @page_limit + 1;

-- name: ListJobsWithEmailPageByUpdatedAsc :many
SELECT j.*, u.email AS submitted_by_email,
       ts.total_tasks, ts.done_tasks, ts.started_at, ts.finished_at,
       sj.name AS scheduled_job_name
FROM jobs j
JOIN users u ON u.id = j.submitted_by
LEFT JOIN LATERAL (
  SELECT COUNT(*)                                  AS total_tasks,
         COUNT(*) FILTER (WHERE t.status = 'done') AS done_tasks,
         MIN(t.started_at)::timestamptz            AS started_at,
         MAX(t.finished_at)::timestamptz           AS finished_at
  FROM tasks t WHERE t.job_id = j.id
) ts ON TRUE
LEFT JOIN scheduled_jobs sj ON sj.id = j.scheduled_job_id
WHERE NOT @cursor_set::bool OR (j.updated_at, j.id) > (@cursor_ts::timestamptz, @cursor_id::uuid)
ORDER BY j.updated_at ASC, j.id ASC
LIMIT @page_limit + 1;

-- name: JobStatusCounts :one
-- Fleet-wide job counts for the dashboard KPI strip. running/queued are current
-- totals; done_24h/failed_24h are windowed on updated_at as a finish-time proxy.
-- updated_at has TWO writers, not one. An earlier version of this comment
-- claimed "the only writer of updated_at is UpdateJobStatus"; that was already
-- false when written, because RecomputeJobStatus also stamps NOW()
-- unconditionally on every call, after every task status transition. So
-- updated_at means "time of the last task-level event", not "time of the last
-- job-status transition".
-- The proxy still holds, on this narrower invariant: a job only HAS status
-- 'done' or 'failed' when its last task event was the one that finished it, and
-- a terminal task is unwritable (UpdateTaskStatus and IncrementTaskRetryCount
-- both carry `status IN ('pending','dispatched','running')`), so no later task
-- event can move updated_at while the job sits in a terminal bucket.
-- POST /v1/jobs/{id}/retry does not falsify it either: a retried job leaves both
-- buckets the instant it becomes 'running', and re-enters the appropriate bucket
-- when it finishes again with an updated_at equal to that new finish. The only
-- effect is a transient undercount while it re-runs, which is defensible on the
-- merits and self-corrects. Accepted in writing - see decision 8 of
-- docs/superpowers/specs/2026-08-13-job-retry-endpoint.md.
-- docs/backlog/bug-2026-06-05-jobs-stats-24h-updated-at-proxy.md stays OPEN; its
-- predicted trigger condition did not fire.
SELECT
  COUNT(*) FILTER (WHERE status = 'running')                                                              AS running,
  COUNT(*) FILTER (WHERE status = 'pending')                                                              AS queued,
  COUNT(*) FILTER (WHERE status = 'done'                  AND updated_at >= NOW() - INTERVAL '24 hours')  AS done_24h,
  COUNT(*) FILTER (WHERE status IN ('failed','cancelled') AND updated_at >= NOW() - INTERVAL '24 hours')  AS failed_24h
FROM jobs;

-- name: GetJobForUpdate :one
-- GetJob plus a row lock. Both multi-statement writers over jobs+tasks -
-- handleCancelJob and handleRetryJob - take this FIRST, before touching any task
-- row. Two properties depend on it, and neither is optional:
--   * A cancel and a retry on the same job serialize, in both orders. Without
--     it, a cancel whose CancelJobTasks ran against a pre-retry snapshot matches
--     nothing and then stamps the job `cancelled` over a retry's freshly
--     `pending` tasks - and GetEligibleTasks does not consult job status, so the
--     farm runs work on a cancelled job.
--   * One lock order (job, then tasks) for both handlers. handleCancelJob was
--     tasks-then-job before the retry endpoint; the two orders together are an
--     ABBA deadlock pair reachable by two ordinary operator actions.
-- No other path holds locks across statements: handleTaskStatus and the
-- dispatcher write autocommit.
-- Do not "optimize" either handler back to GetJob.
-- Both handlers do still call plain GetJob first, unlocked and before opening
-- their transaction, purely to run the owner-or-admin gate: a stranger must not
-- be able to queue for this lock. That read decides nothing else. Every gate on
-- a mutable column reads the row returned HERE, and so does every write.
SELECT * FROM jobs WHERE id = $1 FOR UPDATE;
