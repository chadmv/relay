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
-- A task's status machine is one-way into a terminal state. The status
-- predicate makes that structural: an already-finished task cannot be written
-- again, so a second terminal message from its own assignee at the same epoch -
-- which both other predicates legitimately accept, because a terminal
-- transition neither bumps the epoch nor clears worker_id - cannot flip a `done`
-- task to `failed` and cascade FailDependentTasks across its still-pending
-- downstream.
-- CANONICAL STATEMENT OF THE TERMINALITY RULE. IncrementTaskRetryCount carries
-- the identical predicate and cross-references this paragraph rather than
-- repeating it; change both or neither.
--   * It is an ALLOW-LIST, not the complement deny-list, and that choice is
--     load-bearing even though the two are exactly equivalent against today's
--     vocabulary (migration 000019's tasks_status_check pins it to the six
--     values pending/dispatched/running/done/failed/timed_out). A deny-list
--     fails OPEN on the next status added: a task-level `cancelled` is a
--     plausible near-term addition, because CancelJobTasks currently squashes
--     cancellation onto `failed`, and under `NOT IN (terminal)` such a status
--     would be silently writable and would re-open the resurrection this
--     predicate closes. The allow-list fails closed instead - a new status is
--     unwritable until somebody decides it should be. Every other status
--     predicate in this file is already an allow-list; these two now match.
--   * The set is the complement of the terminal set RecomputeJobStatus counts
--     (see the RecomputeJobStatus statement in jobs.sql - by name, because line
--     numbers rot); keep the two in lockstep. TestTasksStatusVocabularyIsExactly
--     goes RED when the vocabulary changes and names every site to revisit.
--   * The fix for the duplicate-terminal case is this predicate and NOT an epoch
--     bump on terminal transitions: the assignment must survive completion so a
--     trailing log chunk from the agent that just finished still passes
--     AppendTaskLog's fence. See
--     TestUpdateTaskStatus_TerminalTransitionDoesNotEndTheAssignmentSoTrailingLogsStillPersist.
-- Dispatcher.failClaimedTask's target is `dispatched` by construction
-- (ClaimTaskForWorker requires `status='pending'`), so this predicate is
-- tautological there, exactly like the worker predicate above and for the same
-- reason: one statement with no exceptions to remember.
-- Both callers are fenced by the same statement deliberately;
-- Dispatcher.failClaimedTask passes claimed.WorkerID from ClaimTaskForWorker,
-- where the predicate is tautological by design. For why that beats a second
-- un-fenced query or a "skip the check" sentinel, see
-- docs/superpowers/specs/2026-08-12-taskstatus-update-assignee-fence.md.
-- The fence binds a WORKER identity, not a connection: two concurrent streams
-- registered for the same worker row both satisfy it. That matches AppendTaskLog
-- and is what keeps reconnect-within-the-grace-window working, so it is not a
-- regression - but do not read this predicate as connection-scoped.
-- handleTaskStatus's retry branch calls IncrementTaskRetryCount and returns
-- before ever reaching this statement, so this predicate never sees that path.
-- That statement now carries the same three predicates, so the two together
-- cover every production writer. The Go identity gate in handleTaskStatus stays
-- as well: after the retry statement was fenced it is no longer the correctness
-- control, but it still answers a different question ("may this sender drive
-- this task's status machine at all") one round trip earlier. It does NOT save
-- a log line - handleTaskStatus drops pgx.ErrNoRows from both write sites
-- silently - and the round-trip saving is one statement instead of two, since
-- GetTask has already run before the gate. Do not delete either as redundant
-- with the other, but do not oversell the Go one either.
UPDATE tasks
SET status = sqlc.arg(status),
    started_at = sqlc.arg(started_at),
    finished_at = sqlc.arg(finished_at)
WHERE id = sqlc.arg(id)
  AND assignment_epoch = sqlc.arg(assignment_epoch)
  AND worker_id = sqlc.arg(worker_id)
  AND status IN ('pending', 'dispatched', 'running')
RETURNING *;

-- name: IncrementTaskRetryCount :one
-- Burns one retry on a task whose CURRENT generation just failed, and returns it
-- to the queue. Three predicates, each answering a different question; none is
-- redundant with the others and none may be deleted:
--   assignment_epoch - CURRENCY. The caller decided to retry from a row it read
--     earlier; this proves that generation is still the current one. It is what
--     makes the retry atomic with respect to that read, so a cancel or a requeue
--     landing in the caller's TOCTOU window wins instead of being clobbered, and
--     what makes the retry exactly-once per generation when two callers race:
--     under READ COMMITTED the second UPDATE re-evaluates its WHERE against the
--     already-updated row, sees epoch N+1 and a NULL worker_id, and affects zero
--     rows.
--   worker_id      - IDENTITY. The task must still be assigned to the caller's
--     worker. Must stay a plain `=`: tasks.worker_id is NULLABLE, so `=` makes a
--     never-claimed task (worker_id NULL, epoch 0 - a free guess) reject every
--     retry, and makes a caller that lost its identity (a zero-value pgtype.UUID
--     binds SQL NULL) fail closed. `IS NOT DISTINCT FROM` would re-open exactly
--     that. Do not "fix the NULL bug" here. Same rule as UpdateTaskStatus and
--     AppendTaskLog.
--   status         - TERMINALITY. A finished task has no generation to fail.
--     Without this, a task's own assignee can send DONE at epoch N and then
--     FAILED at epoch N - both gates legitimately pass, because a terminal
--     transition deliberately does NOT bump the epoch and does not clear
--     worker_id - and resurrect a completed task while its dependents are
--     already running. This predicate is identical to UpdateTaskStatus's, and
--     the rule is stated once, there: why it is an allow-list rather than the
--     equivalent deny-list, which set it must stay in lockstep with, and why the
--     fix is a predicate and never an epoch bump on terminal transitions. Change
--     both or neither.
-- pgx.ErrNoRows means "one of the three failed": drop, do not recompute the job
-- status, do not wake the dispatcher.
-- This statement is for the AGENT-DRIVEN retry only, and its preconditions are
-- the exact opposite of an operator re-run. POST /v1/jobs/{id}/retry must NOT
-- call it: that endpoint reopens tasks that ARE terminal and has no worker
-- identity to supply, so both the status and the worker predicate would reject
-- every call, and the symptom would be an endpoint that silently does nothing.
-- It has its own statement, RetryJobTasks (below in this file), with an explicit
-- `status IN ('failed','timed_out')` allow-list and its own epoch bump - the
-- operator analogue of RequeueTaskByID, not of this. The separation is enforced
-- by TestIncrementTaskRetryCountHasNoCallerOutsideTheAgentPath, which fails if
-- this identifier appears in any non-test Go file outside
-- internal/worker/handler.go.
UPDATE tasks
SET retry_count = retry_count + 1,
    status = 'pending',
    worker_id = NULL,
    started_at = NULL,
    finished_at = NULL,
    assignment_epoch = assignment_epoch + 1
WHERE id = sqlc.arg(id)
  AND assignment_epoch = sqlc.arg(assignment_epoch)
  AND worker_id = sqlc.arg(worker_id)
  AND status IN ('pending', 'dispatched', 'running')
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
-- Revert a single ASSIGNED task back to 'pending': the WHERE clause matches only
-- `dispatched` and `running`, so a task that has already finished - or that was
-- already requeued by someone else - is left exactly as it is.
-- Used by the reconcile path when the coordinator has a task assigned that the
-- agent didn't report as running. Candidates come from GetActiveTasksForWorker,
-- which filters on the same two statuses, so the predicate is normally redundant
-- with the caller; it is the backstop for the window between that read and this
-- write, and it is what keeps reconcile from being able to resurrect a terminal
-- task.
-- Bumps assignment_epoch so a late update from the prior assignment is fenced out.
-- The bump is inside the same UPDATE as the WHERE, so it happens only for rows
-- that actually matched - the "conditionally end the assignment" branch of the
-- epoch fence, never an unconditional bump.
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
-- TEST-ONLY. No assignee fence - this is the one remaining writer to
-- tasks.status that does not demand a worker id, so it fences currency without
-- identity and a never-claimed task at epoch 0 accepts a write from anybody.
-- Do NOT call this from production code: use UpdateTaskStatus, which fences on
-- both. It has no production callers today and must not gain one.
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

-- name: SelectRetryableTaskIDs :many
-- The UNGUARDED selection for POST /v1/jobs/{id}/retry: exactly the rows
-- RetryJobTasks would reopen if no dependent blocked it. Its only purpose is to
-- let the handler tell three outcomes apart: nothing matched the mode (empty
-- here), the dependents guard blocked everything (non-empty here, empty there),
-- and a concurrent write (a strict subset there). Do NOT add the dependents
-- guard to this statement - that collapses the second case into the first and
-- reports "no failed tasks" for a job that has several.
-- The status predicate must stay byte-identical to RetryJobTasks's; change both
-- or neither.
-- TestTasksStatusVocabularyIsExactly names this statement.
SELECT id FROM tasks
WHERE job_id = sqlc.arg(job_id)
  AND (status IN ('failed','timed_out')
       OR (sqlc.arg(include_done)::bool AND status = 'done'))
ORDER BY created_at;

-- name: RetryJobTasks :many
-- OPERATOR re-run of a terminal job's tasks: the analogue of RequeueTaskByID,
-- and explicitly NOT of IncrementTaskRetryCount. Its preconditions are the exact
-- inverse of that statement's: it reopens tasks that ARE terminal and has no
-- worker identity to bind, so that statement's status and worker predicates
-- would reject every call. See the note at IncrementTaskRetryCount.
--
-- Epoch fence: this satisfies the invariant's "conditionally end the assignment"
-- branch. The bump is inside the same UPDATE as the WHERE, so it happens only
-- for rows that actually matched - never unconditionally, which would satisfy
-- the rule vacuously.
--
-- No fence on a CALLER epoch and no worker predicate, deliberately, for the same
-- reason CancelJobTasks has none: an operator has no generation and no worker
-- identity to prove. What replaces the identity predicate is the status
-- allow-list. A terminal row has no live assignment, so there is no agent to
-- evict; a `dispatched` or `running` task is unreachable by this statement in
-- either mode.
--
-- THAT ARGUMENT HAS A DEPENDENCY, and it is not visible from this WHERE clause:
-- it holds because of WHO writes each terminal status, not because the statuses
-- are terminal. `timed_out` has exactly one writer, and it is the assignee
-- itself: the agent gives each task's subprocess a context deadline built from
-- timeout_sec (internal/agent/runner.go, newRunner), and when cmd.Wait returns
-- with ctx.Err() == context.DeadlineExceeded - so the process is already dead
-- and its proctree already cleaned up - the runner reports TASK_STATUS_TIMED_OUT
-- as its final status. internal/worker/handler.go maps that to 'timed_out' and
-- writes it through UpdateTaskStatus, fenced on the sender's epoch and worker
-- id. So a `timed_out` row, like a `failed` or `done` one, records an agent
-- saying "I am finished with this", after it stopped executing. There is nothing
-- left to evict.
-- What would break this is a SERVER-SIDE watchdog: something that stamps
-- `timed_out` on a task the agent is still happily running, because the row
-- looks stale from the coordinator's side. That row would be terminal AND still
-- executing, and this statement would become a duplicate-execution primitive -
-- the retry reopens it, the dispatcher hands it to a second worker, and the
-- first agent's eventual completion is fenced out by the epoch bump, so nothing
-- reports the collision. Whoever adds a coordinator-side terminal writer must
-- revisit this clause, not just the status vocabulary test.
--
-- THE STATUS ALLOW-LIST MUST STAY IN THIS WHERE CLAUSE, on tasks' own columns.
-- Do not "simplify" it to `t.id IN (SELECT id FROM selected)`. Under READ
-- COMMITTED a blocked UPDATE re-evaluates its ROW-LEVEL qual against the updated
-- tuple (EvalPlanQual); it does not re-execute CTEs. With the status test only
-- in the CTE, a second concurrent retry re-updates rows the first already
-- reopened and double-bumps assignment_epoch.
-- This is pinned by
-- TestRetryJobTasks_RowLevelPredicate_ConcurrentSecondRetryDoesNotDoubleBumpEpoch.
-- This is the multi-row analogue of the reasoning in IncrementTaskRetryCount's
-- `assignment_epoch` note.
--
-- The allow-list is an ALLOW-LIST for the same reason UpdateTaskStatus's is:
-- a new status must be un-retryable until somebody decides otherwise.
-- TestTasksStatusVocabularyIsExactly names this statement.
--
-- The dependents guard is all-or-nothing by construction: the NOT EXISTS is
-- uncorrelated, so it is true for every row or for none. Partial application is
-- unrepresentable, which is what keeps a retry from stranding a reopened task
-- behind a dependency that stayed terminal. `dep.status <> 'pending'` is a
-- NEGATION on purpose: this predicate authorizes BLOCKING, so failing closed
-- means blocking on any status added later. The allow-list spelling would fail
-- open here. `pending` is the only status that proves a dependent was never
-- claimed (ClaimTaskForWorker requires status='pending').
--
-- A dependent that is itself in `selected` does not block: FailDependentTasks
-- cascade-fails a failing task's pending dependents, so on any healthy failed
-- job every selected task has failed dependents. Without the NOT IN (selected)
-- exclusion this statement would refuse every ordinary retry.
--
-- retry_count = 0 is a stated decision, not a copied SET clause: its one
-- behavioral consumer is `terminal && task.RetryCount < task.Retries` in
-- handleTaskStatus (internal/worker/handler.go), so leaving the counter at its
-- exhausted value hands the operator a re-run with zero agent retries - exactly
-- what they pressed Retry to escape.
--
-- UNION (not UNION ALL) in the recursive term dedupes, so the walk terminates
-- even if a cycle is ever introduced. Edges are intra-job by construction
-- (jobcreate resolves depends_on within one spec).
WITH RECURSIVE selected AS (
    SELECT id FROM tasks
    WHERE job_id = sqlc.arg(job_id)
      AND (status IN ('failed','timed_out')
           OR (sqlc.arg(include_done)::bool AND status = 'done'))
), descendants AS (
    SELECT td.task_id AS id
    FROM task_dependencies td
    WHERE td.depends_on_task_id IN (SELECT id FROM selected)
  UNION
    SELECT td.task_id
    FROM task_dependencies td
    JOIN descendants dd ON dd.id = td.depends_on_task_id
)
UPDATE tasks t
SET status           = 'pending',
    worker_id        = NULL,
    started_at       = NULL,
    finished_at      = NULL,
    retry_count      = 0,
    assignment_epoch = t.assignment_epoch + 1
WHERE t.job_id = sqlc.arg(job_id)
  AND (t.status IN ('failed','timed_out')
       OR (sqlc.arg(include_done)::bool AND t.status = 'done'))
  AND NOT EXISTS (
        SELECT 1
        FROM descendants d
        JOIN tasks dep ON dep.id = d.id
        WHERE dep.status <> 'pending'
          AND d.id NOT IN (SELECT id FROM selected)
      )
RETURNING t.id;
