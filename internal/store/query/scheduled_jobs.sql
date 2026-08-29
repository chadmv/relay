-- name: CreateScheduledJob :one
INSERT INTO scheduled_jobs (
    name, owner_id, cron_expr, timezone, job_spec,
    overlap_policy, enabled, next_run_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetScheduledJob :one
SELECT * FROM scheduled_jobs WHERE id = $1;

-- name: ListScheduledJobsPage :many
SELECT * FROM scheduled_jobs
WHERE (sqlc.arg(cursor_set)::bool = FALSE
       OR (created_at, id) < (sqlc.arg(cursor_ts)::timestamptz, sqlc.arg(cursor_id)::uuid))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: CountScheduledJobs :one
SELECT COUNT(*) FROM scheduled_jobs;

-- name: ListScheduledJobsByOwnerPage :many
SELECT * FROM scheduled_jobs
WHERE owner_id = sqlc.arg(owner_id)::uuid
  AND (sqlc.arg(cursor_set)::bool = FALSE
       OR (created_at, id) < (sqlc.arg(cursor_ts)::timestamptz, sqlc.arg(cursor_id)::uuid))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: CountScheduledJobsByOwner :one
SELECT COUNT(*) FROM scheduled_jobs WHERE owner_id = $1;

-- name: UpdateScheduledJob :one
-- handlePatchScheduledJob's write. It rewrites every mutable column, which is
-- where the "PUT handler" impression comes from; the HANDLER is a genuine PATCH
-- (patchScheduledJobRequest is all pointers, an omitted key means leave alone)
-- and builds every value in Go before calling this.
--
-- clear_failure IS A BOOLEAN ARGUMENT, NOT A READ-MODIFY-WRITE, and that is the
-- whole design. The handler reads the row through ownedScheduledJob WITHOUT a
-- lock. Reading last_error into Go and writing it back would let a PATCH carry a
-- stale error forward over a failure a tick recorded in between; expressing it
-- as a CASE means the row's own value is never round-tripped through the
-- application and there is no window.
--
-- THE HANDLER SETS IT FROM TWO ARMS, AND THE SECOND ONE MATTERS. The first is
-- `req.JobSpec != nil || req.CronExpr != nil || req.Timezone != nil` - the three
-- inputs the three recorded failure classes are about. A PATCH of `name`,
-- `overlap_policy` or `enabled` fails it and PRESERVES the record: renaming a
-- schedule must not erase the only signal that it is broken, and on an @monthly
-- schedule nothing would rewrite it for a month.
--
-- The second arm is `schedrunner.ValidateStoredSchedule` over the EFFECTIVE
-- post-patch values. The handler validates PER KEY - job_spec only inside
-- `if req.JobSpec != nil`, cron and timezone only when one of them is supplied -
-- so the first arm alone cleared records about the two inputs the patch never
-- looked at. It is NOT true that everything is "validated before reaching here";
-- only what the request supplied is.
UPDATE scheduled_jobs
SET name           = $2,
    cron_expr      = $3,
    timezone       = $4,
    job_spec       = $5,
    overlap_policy = $6,
    enabled        = $7,
    next_run_at    = $8,
    last_error     = CASE WHEN sqlc.arg(clear_failure)::bool THEN NULL ELSE last_error END,
    last_error_at  = CASE WHEN sqlc.arg(clear_failure)::bool THEN NULL ELSE last_error_at END,
    updated_at     = NOW()
WHERE id = $1
RETURNING *;

-- name: DeleteScheduledJob :execrows
DELETE FROM scheduled_jobs WHERE id = $1;

-- name: ListEligibleScheduledJobs :many
SELECT * FROM scheduled_jobs
 WHERE enabled
   AND next_run_at <= NOW()
 ORDER BY next_run_at ASC
 LIMIT $1
 FOR UPDATE SKIP LOCKED;

-- name: ListOverdueScheduledJobsForCatchup :many
SELECT * FROM scheduled_jobs
 WHERE enabled
   AND next_run_at < NOW();

-- name: AdvanceScheduledJob :exec
-- THE SUCCESS STATEMENT, and the ONLY thing that clears a recorded failure.
-- Called from fireOne after CreateJobFromSpec returned a job, which is the only
-- event that proves the stored spec both validates and inserts.
--
-- Its COALESCE($3, last_job_id) is now vestigial: after the skip path was split
-- out into AdvanceScheduledJobSkipped, this statement's single caller always
-- passes a valid job id. LEAVE IT. Removing it is an unrelated behaviour change.
UPDATE scheduled_jobs
SET next_run_at   = $2,
    last_run_at   = NOW(),
    last_job_id   = COALESCE($3, last_job_id),
    last_error    = NULL,
    last_error_at = NULL,
    updated_at    = NOW()
WHERE id = $1;

-- name: AdvanceScheduledJobSkipped :exec
-- The overlap_policy = 'skip' branch, split out of AdvanceScheduledJob so the
-- clearing rule is not hidden behind a parameter overload.
--
-- IT DELIBERATELY DOES NOT CLEAR. The skip branch returns BEFORE
-- jobspec.Validate runs, so reaching it is no evidence the stored spec is valid.
-- Clearing here would make a poisoned schedule whose predecessor is long-running
-- flicker between "failing" and "healthy" on alternate ticks.
--
-- It DOES stamp last_run_at, preserving the behaviour AdvanceScheduledJob had on
-- this path. That means last_run_at has always meant "the runner reached the end
-- of a fire attempt", not "a job was produced". That is pre-existing and is
-- filed separately; do not change it here.
UPDATE scheduled_jobs
SET next_run_at = $2,
    last_run_at = NOW(),
    updated_at  = NOW()
WHERE id = $1;

-- name: AdvanceScheduledJobAfterFailure :exec
-- The failure statement. Called from TickOnce's fireErr branch, on the OUTER
-- transaction, for the three PERMANENT failure classes only (an undecodable
-- job_spec, an unparseable cron, a spec that fails jobspec.Validate).
--
-- IT MUST NOT BE CALLED FROM INSIDE fireOne. fireOne runs against a nested
-- transaction (a savepoint) that TickOnce ROLLS BACK on failure, so a write
-- issued there is discarded silently - the row would simply never carry an error
-- and the test would fail with no clue why. See internal/schedrunner/runner.go's
-- TickOnce for the write site.
--
-- It does NOT touch last_run_at or last_job_id: no run completed and no job
-- exists to point at. It DOES advance next_run_at, so a poisoned schedule does
-- not hot-loop every tick.
--
-- NOW() rather than a Go clock, matching last_run_at immediately beside it.
-- Within one transaction NOW() is the transaction start time, which for a
-- 100-row tick is at most a few seconds stale. Consistency with the field it
-- sits next to beats that; do not "fix" this to time.Now() in isolation.
UPDATE scheduled_jobs
SET next_run_at   = $2,
    last_error    = $3,
    last_error_at = NOW(),
    updated_at    = NOW()
WHERE id = $1;

-- name: RecordScheduledJobFailure :exec
-- The startup validation sweep's statement (schedrunner.ValidateStoredSpecsOnStartup).
-- Failure fields ONLY.
--
-- next_run_at MUST NOT MOVE HERE. ReconcileOnStartup already owns the
-- never-catch-up policy at boot; a second statement advancing it would skip a
-- fire the operator was entitled to.
--
-- RECORD-ONLY: there is no clearing sibling for the sweep, on purpose. A spec
-- that validates at boot has not been proven to FIRE - the insert could still
-- fail - so clearing on a boot-time pass would assert something the sweep did
-- not observe. Clearing stays the exclusive job of a successful fire and of a
-- PATCH that changed the inputs.
UPDATE scheduled_jobs
SET last_error    = $2,
    last_error_at = NOW(),
    updated_at    = NOW()
WHERE id = $1;

-- name: ListEnabledScheduledJobs :many
-- EVERY enabled schedule, not just the overdue ones. The startup sweep's whole
-- point is the schedules NEITHER existing loop sees: ListEligibleScheduledJobs
-- and ListOverdueScheduledJobsForCatchup both require next_run_at to have
-- passed, so a healthy-looking @monthly schedule broken by a retroactive
-- validation change stays invisible for up to a month after the fix deploys.
-- Ordered by id purely for a deterministic sweep order in tests.
SELECT * FROM scheduled_jobs
 WHERE enabled
 ORDER BY id;

-- name: AdvanceScheduledJobNextRun :exec
UPDATE scheduled_jobs
SET next_run_at = $2,
    updated_at  = NOW()
WHERE id = $1;

-- name: CountActiveJobsForSchedule :one
SELECT COUNT(*) FROM jobs
 WHERE scheduled_job_id = $1
   AND status IN ('pending','queued','running','dispatched');

-- name: ListScheduledJobsPageByCreatedAsc :many
SELECT * FROM scheduled_jobs
WHERE NOT @cursor_set::bool OR (created_at, id) > (@cursor_ts::timestamptz, @cursor_id::uuid)
ORDER BY created_at ASC, id ASC
LIMIT @page_limit + 1;

-- name: ListScheduledJobsPageByNameDesc :many
SELECT * FROM scheduled_jobs
WHERE NOT @cursor_set::bool OR (name, id) < (@cursor_v::text, @cursor_id::uuid)
ORDER BY name DESC, id DESC
LIMIT @page_limit + 1;

-- name: ListScheduledJobsPageByNameAsc :many
SELECT * FROM scheduled_jobs
WHERE NOT @cursor_set::bool OR (name, id) > (@cursor_v::text, @cursor_id::uuid)
ORDER BY name ASC, id ASC
LIMIT @page_limit + 1;

-- name: ListScheduledJobsPageByNextRunDesc :many
SELECT * FROM scheduled_jobs
WHERE NOT @cursor_set::bool OR (next_run_at, id) < (@cursor_ts::timestamptz, @cursor_id::uuid)
ORDER BY next_run_at DESC, id DESC
LIMIT @page_limit + 1;

-- name: ListScheduledJobsPageByNextRunAsc :many
SELECT * FROM scheduled_jobs
WHERE NOT @cursor_set::bool OR (next_run_at, id) > (@cursor_ts::timestamptz, @cursor_id::uuid)
ORDER BY next_run_at ASC, id ASC
LIMIT @page_limit + 1;

-- name: ListScheduledJobsPageByUpdatedDesc :many
SELECT * FROM scheduled_jobs
WHERE NOT @cursor_set::bool OR (updated_at, id) < (@cursor_ts::timestamptz, @cursor_id::uuid)
ORDER BY updated_at DESC, id DESC
LIMIT @page_limit + 1;

-- name: ListScheduledJobsPageByUpdatedAsc :many
SELECT * FROM scheduled_jobs
WHERE NOT @cursor_set::bool OR (updated_at, id) > (@cursor_ts::timestamptz, @cursor_id::uuid)
ORDER BY updated_at ASC, id ASC
LIMIT @page_limit + 1;

-- name: ListScheduledJobsByOwnerPageByCreatedAsc :many
SELECT * FROM scheduled_jobs
WHERE owner_id = @owner_id::uuid
  AND (NOT @cursor_set::bool OR (created_at, id) > (@cursor_ts::timestamptz, @cursor_id::uuid))
ORDER BY created_at ASC, id ASC
LIMIT @page_limit + 1;

-- name: ListScheduledJobsByOwnerPageByNameDesc :many
SELECT * FROM scheduled_jobs
WHERE owner_id = @owner_id::uuid
  AND (NOT @cursor_set::bool OR (name, id) < (@cursor_v::text, @cursor_id::uuid))
ORDER BY name DESC, id DESC
LIMIT @page_limit + 1;

-- name: ListScheduledJobsByOwnerPageByNameAsc :many
SELECT * FROM scheduled_jobs
WHERE owner_id = @owner_id::uuid
  AND (NOT @cursor_set::bool OR (name, id) > (@cursor_v::text, @cursor_id::uuid))
ORDER BY name ASC, id ASC
LIMIT @page_limit + 1;

-- name: ListScheduledJobsByOwnerPageByNextRunDesc :many
SELECT * FROM scheduled_jobs
WHERE owner_id = @owner_id::uuid
  AND (NOT @cursor_set::bool OR (next_run_at, id) < (@cursor_ts::timestamptz, @cursor_id::uuid))
ORDER BY next_run_at DESC, id DESC
LIMIT @page_limit + 1;

-- name: ListScheduledJobsByOwnerPageByNextRunAsc :many
SELECT * FROM scheduled_jobs
WHERE owner_id = @owner_id::uuid
  AND (NOT @cursor_set::bool OR (next_run_at, id) > (@cursor_ts::timestamptz, @cursor_id::uuid))
ORDER BY next_run_at ASC, id ASC
LIMIT @page_limit + 1;

-- name: ListScheduledJobsByOwnerPageByUpdatedDesc :many
SELECT * FROM scheduled_jobs
WHERE owner_id = @owner_id::uuid
  AND (NOT @cursor_set::bool OR (updated_at, id) < (@cursor_ts::timestamptz, @cursor_id::uuid))
ORDER BY updated_at DESC, id DESC
LIMIT @page_limit + 1;

-- name: ListScheduledJobsByOwnerPageByUpdatedAsc :many
SELECT * FROM scheduled_jobs
WHERE owner_id = @owner_id::uuid
  AND (NOT @cursor_set::bool OR (updated_at, id) > (@cursor_ts::timestamptz, @cursor_id::uuid))
ORDER BY updated_at ASC, id ASC
LIMIT @page_limit + 1;

-- name: DisableScheduledJobsByOwner :execrows
UPDATE scheduled_jobs
SET enabled = FALSE,
    updated_at = NOW()
WHERE owner_id = $1
  AND enabled = TRUE;
