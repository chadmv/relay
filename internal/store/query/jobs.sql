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
-- totals; done_24h/failed_24h are windowed on updated_at, which is a faithful
-- finish-time proxy because the only writer of updated_at is UpdateJobStatus and
-- a terminal state is the last transition a job makes (see the design spec).
SELECT
  COUNT(*) FILTER (WHERE status = 'running')                                                              AS running,
  COUNT(*) FILTER (WHERE status = 'pending')                                                              AS queued,
  COUNT(*) FILTER (WHERE status = 'done'                  AND updated_at >= NOW() - INTERVAL '24 hours')  AS done_24h,
  COUNT(*) FILTER (WHERE status IN ('failed','cancelled') AND updated_at >= NOW() - INTERVAL '24 hours')  AS failed_24h
FROM jobs;
