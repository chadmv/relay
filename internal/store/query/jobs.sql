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
-- The four optional predicates below are sqlc.narg: a NULL argument means "no
-- filter". A Params field left at its zero value therefore disables that filter
-- for this statement while the other list arms keep filtering, silently and
-- with no error. Spread parseJobFilters' output at every call site;
-- TestListJobs_FiltersApplyOnEveryArm is the behavioural guard.
--
-- strpos, not ILIKE: the needle is user input, and % and _ must stay literal
-- characters rather than becoming wildcards. A trigram index cannot serve
-- strpos, so adopting pg_trgm means rewriting this predicate to an escaped
-- ILIKE.
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
  AND (sqlc.narg(q)::text IS NULL
       OR strpos(lower(j.name), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(u.email), lower(sqlc.narg(q)::text)) > 0)
  AND (sqlc.narg(owner_id)::uuid IS NULL OR j.submitted_by = sqlc.narg(owner_id)::uuid)
  AND (sqlc.narg(since)::timestamptz IS NULL OR j.created_at >= sqlc.narg(since)::timestamptz)
  AND (sqlc.narg(until)::timestamptz IS NULL OR j.created_at <  sqlc.narg(until)::timestamptz)
ORDER BY j.created_at DESC, j.id DESC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: CountJobs :one
-- The q predicate needs users.email, and an inner join is not elidable: with
-- it, an unfiltered count hash-joins every jobs row against users for a
-- column it never reads. handleListJobs forks on whether q is present, so
-- the join is paid only by the requests that need it. The join-free twin has
-- no q parameter at all, so routing a q request to it drops the text
-- predicate entirely; TestListJobs_FiltersApplyOnEveryArm is what pins the
-- fork.
SELECT COUNT(*)
FROM jobs j
WHERE (sqlc.narg(owner_id)::uuid IS NULL OR j.submitted_by = sqlc.narg(owner_id)::uuid)
  AND (sqlc.narg(since)::timestamptz IS NULL OR j.created_at >= sqlc.narg(since)::timestamptz)
  AND (sqlc.narg(until)::timestamptz IS NULL OR j.created_at <  sqlc.narg(until)::timestamptz);

-- name: CountJobsWithText :one
SELECT COUNT(*)
FROM jobs j
JOIN users u ON u.id = j.submitted_by
WHERE (sqlc.narg(q)::text IS NULL
       OR strpos(lower(j.name), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(u.email), lower(sqlc.narg(q)::text)) > 0)
  AND (sqlc.narg(owner_id)::uuid IS NULL OR j.submitted_by = sqlc.narg(owner_id)::uuid)
  AND (sqlc.narg(since)::timestamptz IS NULL OR j.created_at >= sqlc.narg(since)::timestamptz)
  AND (sqlc.narg(until)::timestamptz IS NULL OR j.created_at <  sqlc.narg(until)::timestamptz);

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
  AND (sqlc.narg(q)::text IS NULL
       OR strpos(lower(j.name), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(u.email), lower(sqlc.narg(q)::text)) > 0)
  AND (sqlc.narg(owner_id)::uuid IS NULL OR j.submitted_by = sqlc.narg(owner_id)::uuid)
  AND (sqlc.narg(since)::timestamptz IS NULL OR j.created_at >= sqlc.narg(since)::timestamptz)
  AND (sqlc.narg(until)::timestamptz IS NULL OR j.created_at <  sqlc.narg(until)::timestamptz)
ORDER BY j.created_at DESC, j.id DESC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: CountJobsByStatus :one
SELECT COUNT(*)
FROM jobs j
WHERE j.status = sqlc.arg(status)::text
  AND (sqlc.narg(owner_id)::uuid IS NULL OR j.submitted_by = sqlc.narg(owner_id)::uuid)
  AND (sqlc.narg(since)::timestamptz IS NULL OR j.created_at >= sqlc.narg(since)::timestamptz)
  AND (sqlc.narg(until)::timestamptz IS NULL OR j.created_at <  sqlc.narg(until)::timestamptz);

-- name: CountJobsByStatusWithText :one
SELECT COUNT(*)
FROM jobs j
JOIN users u ON u.id = j.submitted_by
WHERE j.status = sqlc.arg(status)::text
  AND (sqlc.narg(q)::text IS NULL
       OR strpos(lower(j.name), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(u.email), lower(sqlc.narg(q)::text)) > 0)
  AND (sqlc.narg(owner_id)::uuid IS NULL OR j.submitted_by = sqlc.narg(owner_id)::uuid)
  AND (sqlc.narg(since)::timestamptz IS NULL OR j.created_at >= sqlc.narg(since)::timestamptz)
  AND (sqlc.narg(until)::timestamptz IS NULL OR j.created_at <  sqlc.narg(until)::timestamptz);

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
  AND (sqlc.narg(q)::text IS NULL
       OR strpos(lower(j.name), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(u.email), lower(sqlc.narg(q)::text)) > 0)
  AND (sqlc.narg(owner_id)::uuid IS NULL OR j.submitted_by = sqlc.narg(owner_id)::uuid)
  AND (sqlc.narg(since)::timestamptz IS NULL OR j.created_at >= sqlc.narg(since)::timestamptz)
  AND (sqlc.narg(until)::timestamptz IS NULL OR j.created_at <  sqlc.narg(until)::timestamptz)
ORDER BY j.created_at DESC, j.id DESC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: CountJobsByScheduledJob :one
SELECT COUNT(*)
FROM jobs j
WHERE j.scheduled_job_id = sqlc.arg(scheduled_job_id)::uuid
  AND (sqlc.narg(owner_id)::uuid IS NULL OR j.submitted_by = sqlc.narg(owner_id)::uuid)
  AND (sqlc.narg(since)::timestamptz IS NULL OR j.created_at >= sqlc.narg(since)::timestamptz)
  AND (sqlc.narg(until)::timestamptz IS NULL OR j.created_at <  sqlc.narg(until)::timestamptz);

-- name: CountJobsByScheduledJobWithText :one
SELECT COUNT(*)
FROM jobs j
JOIN users u ON u.id = j.submitted_by
WHERE j.scheduled_job_id = sqlc.arg(scheduled_job_id)::uuid
  AND (sqlc.narg(q)::text IS NULL
       OR strpos(lower(j.name), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(u.email), lower(sqlc.narg(q)::text)) > 0)
  AND (sqlc.narg(owner_id)::uuid IS NULL OR j.submitted_by = sqlc.narg(owner_id)::uuid)
  AND (sqlc.narg(since)::timestamptz IS NULL OR j.created_at >= sqlc.narg(since)::timestamptz)
  AND (sqlc.narg(until)::timestamptz IS NULL OR j.created_at <  sqlc.narg(until)::timestamptz);

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
WHERE (NOT @cursor_set::bool OR (j.created_at, j.id) > (@cursor_ts::timestamptz, @cursor_id::uuid))
  AND (sqlc.narg(q)::text IS NULL
       OR strpos(lower(j.name), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(u.email), lower(sqlc.narg(q)::text)) > 0)
  AND (sqlc.narg(owner_id)::uuid IS NULL OR j.submitted_by = sqlc.narg(owner_id)::uuid)
  AND (sqlc.narg(since)::timestamptz IS NULL OR j.created_at >= sqlc.narg(since)::timestamptz)
  AND (sqlc.narg(until)::timestamptz IS NULL OR j.created_at <  sqlc.narg(until)::timestamptz)
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
WHERE (NOT @cursor_set::bool OR (j.name, j.id) < (@cursor_v::text, @cursor_id::uuid))
  AND (sqlc.narg(q)::text IS NULL
       OR strpos(lower(j.name), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(u.email), lower(sqlc.narg(q)::text)) > 0)
  AND (sqlc.narg(owner_id)::uuid IS NULL OR j.submitted_by = sqlc.narg(owner_id)::uuid)
  AND (sqlc.narg(since)::timestamptz IS NULL OR j.created_at >= sqlc.narg(since)::timestamptz)
  AND (sqlc.narg(until)::timestamptz IS NULL OR j.created_at <  sqlc.narg(until)::timestamptz)
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
WHERE (NOT @cursor_set::bool OR (j.name, j.id) > (@cursor_v::text, @cursor_id::uuid))
  AND (sqlc.narg(q)::text IS NULL
       OR strpos(lower(j.name), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(u.email), lower(sqlc.narg(q)::text)) > 0)
  AND (sqlc.narg(owner_id)::uuid IS NULL OR j.submitted_by = sqlc.narg(owner_id)::uuid)
  AND (sqlc.narg(since)::timestamptz IS NULL OR j.created_at >= sqlc.narg(since)::timestamptz)
  AND (sqlc.narg(until)::timestamptz IS NULL OR j.created_at <  sqlc.narg(until)::timestamptz)
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
WHERE (NOT @cursor_set::bool OR (j.priority, j.id) < (@cursor_v::text, @cursor_id::uuid))
  AND (sqlc.narg(q)::text IS NULL
       OR strpos(lower(j.name), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(u.email), lower(sqlc.narg(q)::text)) > 0)
  AND (sqlc.narg(owner_id)::uuid IS NULL OR j.submitted_by = sqlc.narg(owner_id)::uuid)
  AND (sqlc.narg(since)::timestamptz IS NULL OR j.created_at >= sqlc.narg(since)::timestamptz)
  AND (sqlc.narg(until)::timestamptz IS NULL OR j.created_at <  sqlc.narg(until)::timestamptz)
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
WHERE (NOT @cursor_set::bool OR (j.priority, j.id) > (@cursor_v::text, @cursor_id::uuid))
  AND (sqlc.narg(q)::text IS NULL
       OR strpos(lower(j.name), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(u.email), lower(sqlc.narg(q)::text)) > 0)
  AND (sqlc.narg(owner_id)::uuid IS NULL OR j.submitted_by = sqlc.narg(owner_id)::uuid)
  AND (sqlc.narg(since)::timestamptz IS NULL OR j.created_at >= sqlc.narg(since)::timestamptz)
  AND (sqlc.narg(until)::timestamptz IS NULL OR j.created_at <  sqlc.narg(until)::timestamptz)
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
WHERE (NOT @cursor_set::bool OR (j.status, j.id) < (@cursor_v::text, @cursor_id::uuid))
  AND (sqlc.narg(q)::text IS NULL
       OR strpos(lower(j.name), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(u.email), lower(sqlc.narg(q)::text)) > 0)
  AND (sqlc.narg(owner_id)::uuid IS NULL OR j.submitted_by = sqlc.narg(owner_id)::uuid)
  AND (sqlc.narg(since)::timestamptz IS NULL OR j.created_at >= sqlc.narg(since)::timestamptz)
  AND (sqlc.narg(until)::timestamptz IS NULL OR j.created_at <  sqlc.narg(until)::timestamptz)
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
WHERE (NOT @cursor_set::bool OR (j.status, j.id) > (@cursor_v::text, @cursor_id::uuid))
  AND (sqlc.narg(q)::text IS NULL
       OR strpos(lower(j.name), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(u.email), lower(sqlc.narg(q)::text)) > 0)
  AND (sqlc.narg(owner_id)::uuid IS NULL OR j.submitted_by = sqlc.narg(owner_id)::uuid)
  AND (sqlc.narg(since)::timestamptz IS NULL OR j.created_at >= sqlc.narg(since)::timestamptz)
  AND (sqlc.narg(until)::timestamptz IS NULL OR j.created_at <  sqlc.narg(until)::timestamptz)
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
WHERE (NOT @cursor_set::bool OR (j.updated_at, j.id) < (@cursor_ts::timestamptz, @cursor_id::uuid))
  AND (sqlc.narg(q)::text IS NULL
       OR strpos(lower(j.name), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(u.email), lower(sqlc.narg(q)::text)) > 0)
  AND (sqlc.narg(owner_id)::uuid IS NULL OR j.submitted_by = sqlc.narg(owner_id)::uuid)
  AND (sqlc.narg(since)::timestamptz IS NULL OR j.created_at >= sqlc.narg(since)::timestamptz)
  AND (sqlc.narg(until)::timestamptz IS NULL OR j.created_at <  sqlc.narg(until)::timestamptz)
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
WHERE (NOT @cursor_set::bool OR (j.updated_at, j.id) > (@cursor_ts::timestamptz, @cursor_id::uuid))
  AND (sqlc.narg(q)::text IS NULL
       OR strpos(lower(j.name), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(u.email), lower(sqlc.narg(q)::text)) > 0)
  AND (sqlc.narg(owner_id)::uuid IS NULL OR j.submitted_by = sqlc.narg(owner_id)::uuid)
  AND (sqlc.narg(since)::timestamptz IS NULL OR j.created_at >= sqlc.narg(since)::timestamptz)
  AND (sqlc.narg(until)::timestamptz IS NULL OR j.created_at <  sqlc.narg(until)::timestamptz)
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

-- name: GetJobNamesByIDs :many
-- Job names for one page of tasks. Mirrors GetUserEmailsByIDs; the handler builds
-- a map and reads it per row. Bounded by the page limit, on the primary key.
SELECT id, name FROM jobs WHERE id = ANY($1::uuid[]);
