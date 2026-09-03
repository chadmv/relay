-- name: CreateScheduledJob :one
INSERT INTO scheduled_jobs (
    name, owner_id, cron_expr, timezone, job_spec,
    overlap_policy, enabled, next_run_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetScheduledJob :one
SELECT * FROM scheduled_jobs WHERE id = $1;

-- name: ListScheduledJobsPage :many
-- The two optional predicates below are sqlc.narg: a NULL argument means "no
-- filter". A Params field left at its zero value therefore disables that filter
-- for this statement while the other list arms keep filtering, silently and with
-- no error, which is what parseScheduleFilters plus a single spread in
-- handleListScheduledJobs exists to prevent.
--
-- The users join can neither drop nor duplicate a row: scheduled_jobs.owner_id
-- is NOT NULL REFERENCES users(id) and users.id is the primary key. Only sj.* is
-- selected, so sqlc still emits []ScheduledJob and the response mapper, the
-- row-key functions and the arity test are all untouched.
--
-- IT MUST STAY LEFT. Postgres can remove only an outer join, so an inner join
-- forecloses join removal here.
--
-- strpos, not ILIKE: an ILIKE pattern built by concatenating percent signs
-- around the needle makes user input a pattern, so a user typing % matches every
-- row. strpos has no metacharacters and nothing to escape. The cost is that a
-- pg_trgm index can never serve it; adopting one means rewriting this predicate
-- as escaped ILIKE everywhere, which the arm-enumerating tests cover.
SELECT sj.* FROM scheduled_jobs sj
LEFT JOIN users u ON u.id = sj.owner_id
WHERE (sqlc.arg(cursor_set)::bool = FALSE
       OR (sj.created_at, sj.id) < (sqlc.arg(cursor_ts)::timestamptz, sqlc.arg(cursor_id)::uuid))
  AND (sqlc.narg(enabled)::bool IS NULL OR sj.enabled = sqlc.narg(enabled)::bool)
  AND (sqlc.narg(q)::text IS NULL
       OR strpos(lower(sj.name), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(u.email), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(sj.cron_expr), lower(sqlc.narg(q)::text)) > 0)
ORDER BY sj.created_at DESC, sj.id DESC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: CountScheduledJobs :one
-- total is the count of every row matching every active predicate, independent
-- of the cursor. A count that ignored q would label a three-hit search page
-- "1 - 50 of 312".
SELECT COUNT(*) FROM scheduled_jobs sj
LEFT JOIN users u ON u.id = sj.owner_id
WHERE (sqlc.narg(enabled)::bool IS NULL OR sj.enabled = sqlc.narg(enabled)::bool)
  AND (sqlc.narg(q)::text IS NULL
       OR strpos(lower(sj.name), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(u.email), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(sj.cron_expr), lower(sqlc.narg(q)::text)) > 0);

-- name: ListScheduledJobsByOwnerPage :many
SELECT sj.* FROM scheduled_jobs sj
LEFT JOIN users u ON u.id = sj.owner_id
WHERE sj.owner_id = sqlc.arg(owner_id)::uuid
  AND (sqlc.arg(cursor_set)::bool = FALSE
       OR (sj.created_at, sj.id) < (sqlc.arg(cursor_ts)::timestamptz, sqlc.arg(cursor_id)::uuid))
  AND (sqlc.narg(enabled)::bool IS NULL OR sj.enabled = sqlc.narg(enabled)::bool)
  AND (sqlc.narg(q)::text IS NULL
       OR strpos(lower(sj.name), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(u.email), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(sj.cron_expr), lower(sqlc.narg(q)::text)) > 0)
ORDER BY sj.created_at DESC, sj.id DESC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: CountScheduledJobsByOwner :one
SELECT COUNT(*) FROM scheduled_jobs sj
LEFT JOIN users u ON u.id = sj.owner_id
WHERE sj.owner_id = sqlc.arg(owner_id)::uuid
  AND (sqlc.narg(enabled)::bool IS NULL OR sj.enabled = sqlc.narg(enabled)::bool)
  AND (sqlc.narg(q)::text IS NULL
       OR strpos(lower(sj.name), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(u.email), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(sj.cron_expr), lower(sqlc.narg(q)::text)) > 0);

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

-- name: RecordScheduledJobFailure :execrows
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
--
-- IT IS FENCED ON THE GENERATION THE SWEEP ACTUALLY VALIDATED, which is the
-- three columns validateStoredRow reads and nothing else. The sweep takes a
-- pool-backed *store.Queries, so its LIST and its UPDATEs are separate implicit
-- transactions; with `WHERE id = $1` alone, replica B could list schedule S as
-- broken, an operator could repair S through replica A, and B's UPDATE would
-- stamp the stale failure back - leaving a repaired schedule reading FAILING
-- until its next successful fire, up to a month on @monthly. That is the
-- invisibility bug's exact inverse on the same surface, and a false alarm is
-- what teaches an operator to ignore the field. Single-process placement (the
-- sweep runs before the runner goroutine) does not cover it: README documents
-- multi-replica as supported, which is why ListEligibleScheduledJobs is
-- FOR UPDATE SKIP LOCKED.
--
-- THE FENCE IS THE CONTENT, NOT updated_at, and the choice is not a wash.
-- updated_at is a PROXY for "the row I validated", and it is wrong in the
-- direction that costs the most: ReconcileOnStartup runs immediately before this
-- sweep in EVERY process and bumps updated_at on every overdue row, so on a
-- two-replica rolling deploy - exactly the moment a retroactive validation
-- change lands - replica A's reconcile between B's LIST and B's UPDATE would
-- silently suppress B's record, and the sweep would be least reliable precisely
-- when it is needed. Fencing on job_spec, cron_expr and timezone is narrower,
-- immune to unrelated churn, and is literally the question the verdict was
-- about. It costs one equality per row already located by primary key, and
-- job_spec is JSONB, whose equality is semantic - so a re-serialization that did
-- not change the spec correctly does NOT invalidate the verdict.
--
-- AND A RE-RECORD OF AN IDENTICAL MESSAGE IS A NO-OP. Without the last_error
-- predicate this statement stamped last_error_at = NOW() for every broken row on
-- every boot, with no fire attempt behind it - while migration 000022's own
-- comment says "is it still being tried" is readable from last_error_at MOVING.
-- The sweep's audience is long-cadence schedules that are NOT being
-- fire-attempted, so a @monthly schedule last attempted three weeks ago rendered
-- "last failure 2 minutes ago" after any restart, and a crash-looping server
-- manufactured fresh timestamps for rows nothing was trying to fire.
--
-- :execrows, NOT :exec, for the same reason markWorkerOffline returns
-- (rows, error): a fence that said no and a database fault must be
-- distinguishable at the call site. The caller logs the fault, stays silent on
-- the no-op, and logs the one case that is news - a newly recorded failure.
UPDATE scheduled_jobs
SET last_error    = sqlc.arg(last_error),
    last_error_at = NOW(),
    updated_at    = NOW()
WHERE id = sqlc.arg(id)
  AND job_spec = sqlc.arg(job_spec)
  AND cron_expr = sqlc.arg(cron_expr)
  AND timezone = sqlc.arg(timezone)
  AND last_error IS DISTINCT FROM sqlc.arg(last_error);

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
-- THE OUTER PARENTHESES AROUND THE CURSOR DISJUNCTION ARE LOAD-BEARING. Without
-- them,
-- `NOT cursor_set OR keyset AND filter` binds as
-- `NOT cursor_set OR (keyset AND filter)`, so on the FIRST page - where
-- cursor_set is false - the whole WHERE is satisfied before any filter is
-- reached and every row comes back unfiltered. A cursor-bearing request behaves
-- correctly against that bug, so only a no-cursor request discriminates, which
-- is why TestListScheduledJobs_FilterArms_FirstPage sends no cursor.
SELECT sj.* FROM scheduled_jobs sj
LEFT JOIN users u ON u.id = sj.owner_id
WHERE (NOT @cursor_set::bool OR (sj.created_at, sj.id) > (@cursor_ts::timestamptz, @cursor_id::uuid))
  AND (sqlc.narg(enabled)::bool IS NULL OR sj.enabled = sqlc.narg(enabled)::bool)
  AND (sqlc.narg(q)::text IS NULL
       OR strpos(lower(sj.name), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(u.email), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(sj.cron_expr), lower(sqlc.narg(q)::text)) > 0)
ORDER BY sj.created_at ASC, sj.id ASC
LIMIT @page_limit + 1;

-- name: ListScheduledJobsPageByNameDesc :many
SELECT sj.* FROM scheduled_jobs sj
LEFT JOIN users u ON u.id = sj.owner_id
WHERE (NOT @cursor_set::bool OR (sj.name, sj.id) < (@cursor_v::text, @cursor_id::uuid))
  AND (sqlc.narg(enabled)::bool IS NULL OR sj.enabled = sqlc.narg(enabled)::bool)
  AND (sqlc.narg(q)::text IS NULL
       OR strpos(lower(sj.name), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(u.email), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(sj.cron_expr), lower(sqlc.narg(q)::text)) > 0)
ORDER BY sj.name DESC, sj.id DESC
LIMIT @page_limit + 1;

-- name: ListScheduledJobsPageByNameAsc :many
SELECT sj.* FROM scheduled_jobs sj
LEFT JOIN users u ON u.id = sj.owner_id
WHERE (NOT @cursor_set::bool OR (sj.name, sj.id) > (@cursor_v::text, @cursor_id::uuid))
  AND (sqlc.narg(enabled)::bool IS NULL OR sj.enabled = sqlc.narg(enabled)::bool)
  AND (sqlc.narg(q)::text IS NULL
       OR strpos(lower(sj.name), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(u.email), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(sj.cron_expr), lower(sqlc.narg(q)::text)) > 0)
ORDER BY sj.name ASC, sj.id ASC
LIMIT @page_limit + 1;

-- name: ListScheduledJobsPageByNextRunDesc :many
SELECT sj.* FROM scheduled_jobs sj
LEFT JOIN users u ON u.id = sj.owner_id
WHERE (NOT @cursor_set::bool OR (sj.next_run_at, sj.id) < (@cursor_ts::timestamptz, @cursor_id::uuid))
  AND (sqlc.narg(enabled)::bool IS NULL OR sj.enabled = sqlc.narg(enabled)::bool)
  AND (sqlc.narg(q)::text IS NULL
       OR strpos(lower(sj.name), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(u.email), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(sj.cron_expr), lower(sqlc.narg(q)::text)) > 0)
ORDER BY sj.next_run_at DESC, sj.id DESC
LIMIT @page_limit + 1;

-- name: ListScheduledJobsPageByNextRunAsc :many
SELECT sj.* FROM scheduled_jobs sj
LEFT JOIN users u ON u.id = sj.owner_id
WHERE (NOT @cursor_set::bool OR (sj.next_run_at, sj.id) > (@cursor_ts::timestamptz, @cursor_id::uuid))
  AND (sqlc.narg(enabled)::bool IS NULL OR sj.enabled = sqlc.narg(enabled)::bool)
  AND (sqlc.narg(q)::text IS NULL
       OR strpos(lower(sj.name), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(u.email), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(sj.cron_expr), lower(sqlc.narg(q)::text)) > 0)
ORDER BY sj.next_run_at ASC, sj.id ASC
LIMIT @page_limit + 1;

-- name: ListScheduledJobsPageByUpdatedDesc :many
SELECT sj.* FROM scheduled_jobs sj
LEFT JOIN users u ON u.id = sj.owner_id
WHERE (NOT @cursor_set::bool OR (sj.updated_at, sj.id) < (@cursor_ts::timestamptz, @cursor_id::uuid))
  AND (sqlc.narg(enabled)::bool IS NULL OR sj.enabled = sqlc.narg(enabled)::bool)
  AND (sqlc.narg(q)::text IS NULL
       OR strpos(lower(sj.name), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(u.email), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(sj.cron_expr), lower(sqlc.narg(q)::text)) > 0)
ORDER BY sj.updated_at DESC, sj.id DESC
LIMIT @page_limit + 1;

-- name: ListScheduledJobsPageByUpdatedAsc :many
SELECT sj.* FROM scheduled_jobs sj
LEFT JOIN users u ON u.id = sj.owner_id
WHERE (NOT @cursor_set::bool OR (sj.updated_at, sj.id) > (@cursor_ts::timestamptz, @cursor_id::uuid))
  AND (sqlc.narg(enabled)::bool IS NULL OR sj.enabled = sqlc.narg(enabled)::bool)
  AND (sqlc.narg(q)::text IS NULL
       OR strpos(lower(sj.name), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(u.email), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(sj.cron_expr), lower(sqlc.narg(q)::text)) > 0)
ORDER BY sj.updated_at ASC, sj.id ASC
LIMIT @page_limit + 1;

-- name: ListScheduledJobsByOwnerPageByCreatedAsc :many
SELECT sj.* FROM scheduled_jobs sj
LEFT JOIN users u ON u.id = sj.owner_id
WHERE sj.owner_id = @owner_id::uuid
  AND (NOT @cursor_set::bool OR (sj.created_at, sj.id) > (@cursor_ts::timestamptz, @cursor_id::uuid))
  AND (sqlc.narg(enabled)::bool IS NULL OR sj.enabled = sqlc.narg(enabled)::bool)
  AND (sqlc.narg(q)::text IS NULL
       OR strpos(lower(sj.name), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(u.email), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(sj.cron_expr), lower(sqlc.narg(q)::text)) > 0)
ORDER BY sj.created_at ASC, sj.id ASC
LIMIT @page_limit + 1;

-- name: ListScheduledJobsByOwnerPageByNameDesc :many
SELECT sj.* FROM scheduled_jobs sj
LEFT JOIN users u ON u.id = sj.owner_id
WHERE sj.owner_id = @owner_id::uuid
  AND (NOT @cursor_set::bool OR (sj.name, sj.id) < (@cursor_v::text, @cursor_id::uuid))
  AND (sqlc.narg(enabled)::bool IS NULL OR sj.enabled = sqlc.narg(enabled)::bool)
  AND (sqlc.narg(q)::text IS NULL
       OR strpos(lower(sj.name), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(u.email), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(sj.cron_expr), lower(sqlc.narg(q)::text)) > 0)
ORDER BY sj.name DESC, sj.id DESC
LIMIT @page_limit + 1;

-- name: ListScheduledJobsByOwnerPageByNameAsc :many
SELECT sj.* FROM scheduled_jobs sj
LEFT JOIN users u ON u.id = sj.owner_id
WHERE sj.owner_id = @owner_id::uuid
  AND (NOT @cursor_set::bool OR (sj.name, sj.id) > (@cursor_v::text, @cursor_id::uuid))
  AND (sqlc.narg(enabled)::bool IS NULL OR sj.enabled = sqlc.narg(enabled)::bool)
  AND (sqlc.narg(q)::text IS NULL
       OR strpos(lower(sj.name), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(u.email), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(sj.cron_expr), lower(sqlc.narg(q)::text)) > 0)
ORDER BY sj.name ASC, sj.id ASC
LIMIT @page_limit + 1;

-- name: ListScheduledJobsByOwnerPageByNextRunDesc :many
SELECT sj.* FROM scheduled_jobs sj
LEFT JOIN users u ON u.id = sj.owner_id
WHERE sj.owner_id = @owner_id::uuid
  AND (NOT @cursor_set::bool OR (sj.next_run_at, sj.id) < (@cursor_ts::timestamptz, @cursor_id::uuid))
  AND (sqlc.narg(enabled)::bool IS NULL OR sj.enabled = sqlc.narg(enabled)::bool)
  AND (sqlc.narg(q)::text IS NULL
       OR strpos(lower(sj.name), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(u.email), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(sj.cron_expr), lower(sqlc.narg(q)::text)) > 0)
ORDER BY sj.next_run_at DESC, sj.id DESC
LIMIT @page_limit + 1;

-- name: ListScheduledJobsByOwnerPageByNextRunAsc :many
SELECT sj.* FROM scheduled_jobs sj
LEFT JOIN users u ON u.id = sj.owner_id
WHERE sj.owner_id = @owner_id::uuid
  AND (NOT @cursor_set::bool OR (sj.next_run_at, sj.id) > (@cursor_ts::timestamptz, @cursor_id::uuid))
  AND (sqlc.narg(enabled)::bool IS NULL OR sj.enabled = sqlc.narg(enabled)::bool)
  AND (sqlc.narg(q)::text IS NULL
       OR strpos(lower(sj.name), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(u.email), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(sj.cron_expr), lower(sqlc.narg(q)::text)) > 0)
ORDER BY sj.next_run_at ASC, sj.id ASC
LIMIT @page_limit + 1;

-- name: ListScheduledJobsByOwnerPageByUpdatedDesc :many
SELECT sj.* FROM scheduled_jobs sj
LEFT JOIN users u ON u.id = sj.owner_id
WHERE sj.owner_id = @owner_id::uuid
  AND (NOT @cursor_set::bool OR (sj.updated_at, sj.id) < (@cursor_ts::timestamptz, @cursor_id::uuid))
  AND (sqlc.narg(enabled)::bool IS NULL OR sj.enabled = sqlc.narg(enabled)::bool)
  AND (sqlc.narg(q)::text IS NULL
       OR strpos(lower(sj.name), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(u.email), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(sj.cron_expr), lower(sqlc.narg(q)::text)) > 0)
ORDER BY sj.updated_at DESC, sj.id DESC
LIMIT @page_limit + 1;

-- name: ListScheduledJobsByOwnerPageByUpdatedAsc :many
SELECT sj.* FROM scheduled_jobs sj
LEFT JOIN users u ON u.id = sj.owner_id
WHERE sj.owner_id = @owner_id::uuid
  AND (NOT @cursor_set::bool OR (sj.updated_at, sj.id) > (@cursor_ts::timestamptz, @cursor_id::uuid))
  AND (sqlc.narg(enabled)::bool IS NULL OR sj.enabled = sqlc.narg(enabled)::bool)
  AND (sqlc.narg(q)::text IS NULL
       OR strpos(lower(sj.name), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(u.email), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(sj.cron_expr), lower(sqlc.narg(q)::text)) > 0)
ORDER BY sj.updated_at ASC, sj.id ASC
LIMIT @page_limit + 1;

-- name: DisableScheduledJobsByOwner :execrows
UPDATE scheduled_jobs
SET enabled = FALSE,
    updated_at = NOW()
WHERE owner_id = $1
  AND enabled = TRUE;

-- name: ScheduledJobCounts :one
-- The schedules summary strip's census, in ONE statement so every field it
-- returns describes the same snapshot. owner_id is sqlc.narg: NULL means
-- fleet-wide, a value scopes to that owner. The handler decides which, and a
-- caller who is not an admin must never reach the NULL.
--
-- paused is exactly NOT enabled; there is no third state and no paused column.
-- failing is CURRENT STATE and is deliberately NOT windowed: last_error records
-- only the most recent failure, so a schedule that failed many times contributes
-- one, and it is counted in schedules while failed_runs_24h is counted in jobs.
-- Summing the two would produce a number whose loss is invisible where it is
-- read, which is why they are two fields.
--
-- failed_runs_24h is a scalar subquery rather than a sibling statement so it
-- shares this one's snapshot. Its join to scheduled_jobs is what restricts to
-- schedule-spawned jobs AND what supplies the owner scope; a standalone job has
-- a NULL scheduled_job_id and cannot join.
--
-- WINDOWED ON jobs.updated_at, matching JobStatusCounts exactly, so the two
-- "in the last 24 hours" numbers on this product's two summary strips mean the
-- same thing and inherit the same limitation and the same future fix.
-- created_at would answer a different question - runs that STARTED in the
-- window - and would count a job that started 23 hours ago and is still running
-- as neither failed nor not.
--
-- IT EXCLUDES cancelled, unlike jobStatsResponse.failed_24h, which is why the
-- response field is named failed_runs_24h rather than failed_24h. A cancelled
-- job is an operator action, not a schedule fault, and a strip that flags one
-- teaches the operator to ignore the strip.
SELECT
  COUNT(*) FILTER (WHERE enabled)                AS enabled,
  COUNT(*) FILTER (WHERE NOT enabled)            AS paused,
  COUNT(*) FILTER (WHERE last_error IS NOT NULL) AS failing,
  (SELECT COUNT(*)
     FROM jobs j
     JOIN scheduled_jobs sj ON sj.id = j.scheduled_job_id
    WHERE j.status = 'failed'
      AND j.updated_at >= NOW() - INTERVAL '24 hours'
      AND (sqlc.narg(owner_id)::uuid IS NULL
           OR sj.owner_id = sqlc.narg(owner_id)::uuid)) AS failed_runs_24h
FROM scheduled_jobs
WHERE sqlc.narg(owner_id)::uuid IS NULL
   OR owner_id = sqlc.narg(owner_id)::uuid;
