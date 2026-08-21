---
title: Remove dead 'queued'/'dispatched' status vocabulary from CancelJobTasks and CountActiveJobsForSchedule
type: idea
status: open
created: 2026-07-01
updated: 2026-08-20
priority: low
source: ROADMAP deep-refresh gaps sweep (2026-06-26)
---

# Remove dead 'queued'/'dispatched' status vocabulary from CancelJobTasks and CountActiveJobsForSchedule

## Summary
Two queries filter on status values that the migration 000019 CHECK constraints make unreachable for
their table. It is harmless today (the real states are covered) but it is dead vocabulary that
contradicts the schema and can mislead a reader about what states exist.

## Context
Surfaced by the 2026-06-26 `/roadmap deep` gaps sweep. Migration `000019_status_vocabulary_checks`
constrains task status to `('pending','dispatched','running','done','failed','timed_out')` and job
status to `('pending','running','done','failed','cancelled')`.

## Proposal
- `CancelJobTasks` (`internal/store/query/tasks.sql`) filters tasks on
  `status IN ('pending','queued','running','dispatched')` - `'queued'` is not a valid task status.
- `CountActiveJobsForSchedule` (`internal/store/query/scheduled_jobs.sql`) filters *jobs* on
  `status IN ('pending','queued','running','dispatched')` - `'queued'`/`'dispatched'` are never job
  statuses.

Trim each filter to the vocabulary valid for its table, then `make generate` to regenerate sqlc. No
behavioral change (the reachable states remain covered).

### Scope boundary added 2026-08-14 - INVALID vocabulary only, never merely unreachable vocabulary

This item is about status values that **cannot exist for that table** per the 000019 CHECK
constraints. It is **not** about valid statuses that happen to be unreachable through a particular
statement's other predicates. The distinction now matters, because the 2026-08-14 trailing-window
slice created a live example that a sweep would otherwise "clean up":

`AppendTaskLog`'s fence carries `status IN ('pending','dispatched','running')` as the first arm of a
disjunction. **`'pending'` is provably unreachable there**: every statement that returns a task to
`pending` also sets `worker_id = NULL` in the same UPDATE, and the fence's `t.worker_id = $3`
predicate is a NULL-rejecting plain `=`, so a pending row can never match. It is kept anyway, and
**deliberately**, so the arm stays byte-identical to the allow-lists in `UpdateTaskStatus` and
`IncrementTaskRetryCount` - a divergence between the three would be read as meaningful by the next
person to diff them, and the whole point of those three allow-lists is that they are one partition
written three times.

So: **do not touch `AppendTaskLog`, `UpdateTaskStatus` or `IncrementTaskRetryCount` under this item.**
If a future sweep wants to argue that unreachable-but-valid vocabulary is also worth trimming, that is
a different item with a different (and much weaker) case, and it has to be argued against the
byte-identical-siblings rule rather than around it.

### Evidence added 2026-08-20 - the lockstep guard grew to thirteen sites and still does not name `CancelJobTasks`

No scope change; the two statements this item claims are unchanged. What is new is leverage, and a
reason the item is worth more than its `low` priority suggests.

The 2026-08-20 requeue-fence slice expanded `TestTasksStatusVocabularyIsExactly`
(`internal/store/tasks_status_vocabulary_lockstep_test.go`) from **seven** named statements to
**thirteen**, adding `RequeueTask`, `RequeueTaskByID`, `RequeueWorkerTasks`,
`RequeueWorkerTasksIfEpoch`, `GetActiveTasksForWorker` and `ListGraceCandidates`, with per-site
guidance and a failure message stating that seven of the thirteen fail **open** when a status is
omitted.

`CancelJobTasks` is still not on that list. It appears in the test's preamble only, and as
*motivation* rather than as a site - "CancelJobTasks squashes cancellation onto `failed` today, so
somebody will eventually want the real thing". But it does slice the vocabulary:

```sql
WHERE job_id = $1 AND status IN ('pending', 'queued', 'running', 'dispatched');
```

So the guard whose entire purpose is to force a per-site decision when the vocabulary moves would not
name the one statement whose filter is **already wrong** - and, per its own preamble, the concrete
near-term candidate for a new status is a task-level `cancelled`, which is exactly the change that
would send a reader to `CancelJobTasks` first.

Two consequences for whoever picks this up:

- The fix is now cheaper than when this was filed. The guard's per-site prose exists for twelve of the
  thirteen neighbours; `CancelJobTasks` needs one entry written in the same shape, and the entry
  writes itself once the dead `'queued'` literal is gone.
- Getting there via the guard is the better order: add the site to the guard's list **and** trim the
  literal in the same commit, so the list and the statement are correct at the same moment. Adding it
  to the list while the dead literal is still there would enshrine the wrong filter as reviewed.

Source: `docs/retros/2026-08-20-requeue-task-by-id-fence.md`.

## Acceptance / Done When
- Each query filters only on statuses valid for its table per the 000019 CHECK constraints.
- `make generate` run; the diff is query-only and behavior is unchanged.
- No allow-list in the epoch-fence family (`AppendTaskLog`, `UpdateTaskStatus`,
  `IncrementTaskRetryCount`, `RetryJobTasks`, `SelectRetryableTaskIDs`) is edited.
- **Added 2026-08-20:** `CancelJobTasks` is named in `TestTasksStatusVocabularyIsExactly`'s statement
  list and failure message, with per-site prose in the same shape as its neighbours, in the same
  commit that trims its filter. (`CountActiveJobsForSchedule` is out of that guard's scope - it
  partitions `jobs.status`, not `tasks.status`.)

## Related
- Found in the same sweep as `bug-2026-06-26-retry-resurrects-cancelled-task`, closed 2026-08-12
  (`docs/backlog/closed/`). That work added `TestTasksStatusVocabularyIsExactly`, which asserts the
  `tasks_status_check` vocabulary is exactly the six live values and names every query that
  partitions it - useful leverage for this cleanup, and the test that will go RED if this idea is
  implemented by widening or narrowing the vocabulary rather than only removing dead filters. **That
  guard now names THIRTEEN sites (was six, then seven), and two of them - `AppendTaskLog` and the
  whole "currently assigned" partition - have inverted guidance** - read its comment before touching
  anything it names.
- Source: `internal/store/query/tasks.sql` (`CancelJobTasks`),
  `internal/store/query/scheduled_jobs.sql` (`CountActiveJobsForSchedule`),
  `internal/store/migrations/000019_status_vocabulary_checks.up.sql`. **Citations converted from line
  offsets to symbol names 2026-08-14**: `tasks.sql:181` was stale by roughly 160 lines after the
  fence work, and a line range reddens nothing when it goes wrong.
- The slice that added the sixth site and the scope boundary above:
  `docs/superpowers/specs/2026-08-14-tasklog-terminal-append-bound.md`,
  `docs/retros/2026-08-14-tasklog-terminal-append-bound.md`
- The slice that took the guard from seven sites to thirteen and left `CancelJobTasks` off it:
  [[bug-2026-08-20-requeuetaskbyid-has-no-epoch-or-assignee-fence]],
  `docs/retros/2026-08-20-requeue-task-by-id-fence.md`

## Notes
Cosmetic/consistency only. Remember the sqlc regeneration and the CRLF/LF hygiene noted in CLAUDE.md
after editing `.sql`.
