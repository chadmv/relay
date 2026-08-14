# Job retry action (Retry failed / Retry all) - Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Put two Retry pills in the job-detail header of a finished job, wired to `POST /v1/jobs/{id}/retry?task=failed|all`, with a confirm step, a three-key invalidation, and a 409 surface that tells the endpoint's three distinct conflicts apart.

**Architecture:** Frontend only. One new pure module (`web/src/jobs/retryError.ts`) classifies the endpoint's error vocabulary; `web/src/jobs/api.ts` gains `retryJob`; `web/src/jobs/useJobActions.ts` gains a second mutation next to `cancel`; `web/src/jobs/JobActions.tsx` gains two pills, two confirm dialogs, a classified error banner and a success line. `web/src/jobs/JobDetailPage.tsx` changes by exactly one stale comment. No new query key, no new component file beyond the classifier, no cache seeding.

**Tech Stack:** TypeScript 5.7, React 18, TanStack Query v5, Vitest 2.1 + Testing Library 16 + user-event 14 + jsdom, MSW 2.7. No new dependency. **Zero Go.**

**Slice independence:** single-slice, frontend only. There is no backend slice; `POST /v1/jobs/{id}/retry` shipped in PR #127. Phase 3 has nothing to parallelize - dispatch `relay-frontend-engineer` alone.

**Backlog item closed by this slice:** `docs/backlog/feature-2026-07-01-job-retry-action.md`. Close it with `/backlog close feature-2026-07-01-job-retry-action`, which `git mv`s the file into `docs/backlog/closed/`; never hand-edit `status:`.

---

## READ THIS FIRST: the item versus the shipped code

The item was amended on 2026-08-13 and is **substantially accurate** - every claim about the wire contract holds against `handleRetryJob`. Four discrepancies, none of which change the size of the work, but two of which change the design.

| Item's claim | Verdict | Evidence (symbol, not line) |
|---|---|---|
| `?task=failed\|all` is required, no default; absent/empty/repeated/unrecognized is 400 | **Confirmed, exact** | `handleRetryJob` reads `r.URL.Query()["task"]` and rejects `len(vals) != 1` before any DB work. `Query().Get()` is deliberately not used, so `?task=failed&task=all` is a 400, not a silent first-wins. |
| `failed` reopens `failed` **and** `timed_out` | **Confirmed** | `SelectRetryableTaskIDs` / `RetryJobTasks` both carry `status IN ('failed','timed_out') OR (include_done AND status = 'done')`. |
| Deny is 404 | **Confirmed** | `jobOwnerOr404`, called before `Begin`. |
| Success is 200 with the job plus `tasks_retried >= 1` | **Confirmed** | `retryJobResponse` embeds `jobResponse` and adds `TasksRetried`; a zero-match is a 409, so the field is never 0 on a 200. |
| Three distinct 409s | **Confirmed** | see the vocabulary table below. |
| The 200 reports `total_tasks: 0` / `done_tasks: 0` | **Confirmed, and WORSE than the item says** | `handleRetryJob` builds its body with `toJobResponse(job, "", nil, nil)`. `applyJobEnrichment` is never called, so `total_tasks`/`done_tasks` are zero - **and** `tasks` is `nil` (omitted by `omitempty`) and `submitted_by_email` is `""` (also omitted). Seeding `['job', id]` from this body would blank the entire task table and the submitter line, not just two counters. |
| "Use the same three-key invalidation on success (`['job', id]` + `['jobs']` + `['job-stats']`)" | **Confirmed as sufficient** | task status lives *inside* the `['job', id]` payload - there is no separate task query key (see `useJob`) - and log lines never enter TanStack at all (`useTaskLogStream` holds them in hook state). Three keys is the complete set. |
| "Design: `design_handoff_relay_holo/reference/screens/job-detail.js`" | **Incomplete - the hi-fi is NOT silent** | The authoritative hi-fi (`design_handoff_relay_holo/hifi3-holo-pages.jsx`, the job-detail header row next to `Spec`/`Logs`) has a ghost-pill `Retry` sitting beside a danger-toned `Abort`. See Decision 6. |
| "cancel has no mode parameter and no rich conflict vocabulary" - implying retry needs new machinery | **Confirmed, but the pattern still transfers** | `handleCancelJob` emits exactly one 409 string. Retry emits five distinct 4xx strings. The mutation/confirm/invalidate skeleton transfers verbatim; only the error surface is new. |

**A fifth discrepancy is in the code, not the item, and this slice must fix it:** `JobDetailPage` carries the comment *"No Retry/Abort header pill - there is no per-job retry endpoint"*. That sentence has been false since PR #127. This is the project's dominant defect class (wrong prose about correct code) sitting in the exact file this slice edits. Task 6 fixes it.

### The endpoint's exact error vocabulary (quoted from `handleRetryJob`)

`apiFetch` turns the `{"error": "..."}` envelope into `new ApiError(status, code, "<status> <code>")`, so **`ApiError.code` is the server's sentence verbatim** and `ApiError.message` is that sentence with the numeric status glued on the front. The classifier keys on `code`.

| Status | Server sentence (verbatim) | Meaning |
|---|---|---|
| 400 | `query parameter "task" is required and must be exactly "failed" or "all"` | frontend bug; unreachable from the UI this slice ships |
| 404 | `job not found` | absent, or caller is neither owner nor admin |
| 409 | `job was cancelled; retry is not available for a cancelled job` | permanent for this job |
| 409 | `job is not finished; retry is available for a done or failed job` | temporary; the job is in flight |
| 409 | `no tasks matched task=failed; this job has no failed or timed_out tasks` | dead end for this mode |
| 409 | `no tasks matched task=all; this job has no finished tasks` | dead end for this mode |
| 409 | `no tasks were reopened: a selected task has dependents that have already run, or the job changed while the request was in flight; nothing was applied` | blocked by dependents |
| 409 | `the job changed while the retry was in flight; nothing was applied - try again` | raced; retryable |

Note the blocked sentence **hedges** ("or the job changed"), so the frontend hint must not assert dependents as a certainty.

---

## Design

### Decision 1 - Two labelled pills, not one pill plus a mode picker

**Ship `Retry failed` and `Retry all` as two separate ghost pills**, each opening its own confirm dialog, each binding the query parameter at click time.

- `?task` has **no server-side default on purpose**. A single pill would need a picker inside the dialog, and that picker needs either a pre-selected radio (a default - exactly what the server refuses to have) or a confirm button disabled until a choice is made (more state, one more failure mode, one more test).
- Two pills mean the wire value is bound to the label the user read. There is no state between intent and request.
- **Project precedent settles it:** the hi-fi shows *one* `Abort` pill and the shipped header renders *two* buttons (`Cancel` + `Force cancel`) with one mutation and a call-site argument (`useJobActions`, `JobActions`). Expanding one hi-fi pill into two labelled ones for a binary mode is the established move here.
- Accepted cost: a `failed` job shows four pills (`Retry failed`, `Retry all`, `Cancel`, `Force cancel`) because a failed job is both retryable and still cancellable server-side. The header row is `flex-wrap`, so this wraps rather than setting a width floor. Order is retry-first so the danger-toned `Force cancel` stays last in the group.
- Dialog confirm labels are `Retry failed tasks` and `Retry all tasks` - deliberately **not** the same strings as the pills, mirroring the existing `Cancel` pill / `Cancel job` confirm-label disambiguation, so a test can click either without `within(dialog)` and jsdom's missing `inert` support cannot make the query ambiguous.

### Decision 2 - The 409 surface renders in the page-level banner, and that is safe here

Three reasons that are not interchangeable, so they must not collapse into one string:

| kind | rendered hint (frontend-owned, in addition to the server sentence) |
|---|---|
| `none-matched` | "Nothing was changed." - dead end |
| `blocked` | "Nothing was applied. Retry all also reopens the tasks that depend on these, which is usually what unblocks it." (failed mode only) |
| `raced` | "Nothing was applied. Retry the action." - retryable |

The blocked hint is **verified against the SQL**, not guessed: `RetryJobTasks`'s guard is `NOT EXISTS (descendant with status <> 'pending' AND id NOT IN selected)`. A `done` descendant blocks in `failed` mode and stops blocking in `all` mode, because `all` puts it in `selected`. In `all` mode the hint drops the suggestion.

**Where it renders.** The confirm dialog closes *before* the mutation fires (`runConfirmed` calls `setConfirm(null)` in the same handler, exactly as cancel does today), so when the error lands there is no dialog and no scrim mounted, and the banner is the topmost thing on the page. This is the one shape that satisfies the standing lesson "an overlay owns its own error surface" without moving the banner into the dialog. **Do not change `runConfirmed` to keep the dialog open while the request is in flight** - that would put a scrim over the error box and reintroduce the silent-button bug verbatim.

**Prose drift is handled, not ignored.** The classifier matches on stable sentence prefixes and, when nothing matches, falls back to rendering `ApiError.message` (the server's own text with its status). It never emits a generic "something went wrong", so even under drift the three reasons stay distinguishable by the server's own wording. Task 7 adds a contract test that reads `internal/api/jobs.go` and reddens the moment a prefix stops existing.

### Decision 3 - Invalidate. Never seed. Three keys, unchanged from cancel

The 200 body is `toJobResponse(job, "", nil, nil)`: zeroed `total_tasks`/`done_tasks`, **absent `tasks`**, absent `submitted_by_email`. Writing it into `['job', id]` would empty the task table, the DAG, the progress strip and the submitter line on the page the user is looking at. `retryJob` therefore returns only `{ tasks_retried }` - the one field on that body which is trustworthy and which no cache consumes.

Keys to invalidate, and why each is needed:

- `['job', id]` - the detail payload is where task statuses live; the reopened tasks flip to `pending` there. `useJob` polls at 3 s, and the invalidation makes the refetch immediate.
- `['jobs']` - list rows show status and the enrichment progress counters, both of which just moved.
- `['job-stats']` - decoupled from `['jobs']` (see `queryKeyDecoupling.test.tsx`); a `done|failed` job becoming `running` changes `running` and drops out of a 24 h bucket.

Explicitly **not** invalidated: there is no task-list query key (tasks arrive inside `['job', id]`), and task logs are not in TanStack at all - `useTaskLogStream` holds lines in component state, and `JobDetailPage` recomputes `live` from the task status it reads out of the refetched `['job', id]`, so the log tail resumes on its own. `['schedules', 'runs', id]` is left alone for the same reason the shipped cancel action leaves it alone; changing that is a separate decision about a different page.

### Decision 4 - Hidden unless the job is `done` or `failed`

Allow-list, positively spelled: `job.status === 'done' || job.status === 'failed'`. Everything else (`pending`, `running`, `cancelled`) is a deterministic 409 from `handleRetryJob`'s status switch - a control that is always visible and always fails is a dead control, which this project does not ship (`JobDetailPage.test.tsx` already asserts that rule for the un-backed hi-fi pills). Hidden, not disabled: a disabled pill on every running job is permanent chrome that explains nothing, and the cancel pair already establishes "hide when the server will refuse".

The allow-list spelling is not cosmetic. It is the frontend reading of the invariant that `RetryJobTasks`'s own status predicate obeys: a status added to `JobStatus` later must be **un-retryable until somebody decides otherwise**. The deny-list spelling (`status !== 'cancelled' && ...`) fails open on the next status added.

### Decision 5 - Yes, confirm

Retry spends farm capacity and, in `all` mode, re-runs work that already succeeded. Cancel confirms for a strictly smaller commitment. The dialog is also the only place with room for the two facts the pill labels cannot carry: that `failed` includes timed-out tasks, and that `all` re-runs successful ones.

### Decision 6 - The hi-fi is NOT silent, and this slice diverges from it deliberately

`design_handoff_relay_holo/hifi3-holo-pages.jsx` shows a ghost-pill `Retry` in the job-detail header action group. This slice honours the tone and placement (ghost `PillButton`, in the existing `data-testid="job-actions"` slot) and diverges on two points, both flagged for the reviewer:

1. **Two pills, not one** - the hi-fi has no notion of `failed` vs `all`; see Decision 1 and the `Abort` -> `Cancel`/`Force cancel` precedent.
2. **Availability** - the hi-fi draws the pill on a *running* job (its mock header shows `RUNNING`). The endpoint refuses that with a 409, so the pill is hidden there. The hi-fi's `Retry` was drawn before the endpoint existed.

No new visual primitive is introduced.

---

## File structure

| File | Change | Responsibility |
|---|---|---|
| `web/src/jobs/api.ts` | modify (add `RetryMode`, `RetryResult`, `retryJob`) | the one place that knows the retry URL shape |
| `web/src/jobs/retryError.ts` | **create** | pure classification of the endpoint's error vocabulary; no React, no imports from components |
| `web/src/jobs/useJobActions.ts` | modify (add `retry` next to `cancel`) | mutation + invalidation policy |
| `web/src/jobs/JobActions.tsx` | modify | pills, confirm copy, error banner, success line |
| `web/src/jobs/JobDetailPage.tsx` | modify (comment only) | remove the false "there is no per-job retry endpoint" claim |
| `web/src/jobs/api.test.ts` | append 2 tests | wire shape |
| `web/src/jobs/retryError.test.ts` | **create** | classifier behaviour |
| `web/src/jobs/retryErrorContract.test.ts` | **create** | the prefixes still exist in `internal/api/jobs.go` |
| `web/src/jobs/useJobActions.test.tsx` | append 4 tests | query param + invalidation |
| `web/src/jobs/JobActions.test.tsx` | append 11 tests | availability, confirm gating, three 409 surfaces, success |
| `web/src/jobs/JobDetailPage.test.tsx` | append 1 test, edit 1 comment | the pills reach the reserved slot |

**No test file's existing lines are modified except one comment in `JobDetailPage.test.tsx`.** Every existing assertion stays byte-identical, including `queryByRole('button', { name: /^retry$/i })` in `does not fabricate unbacked timing or the live-log affordances` - that test's fixture job is `running`, so the pills are correctly absent, and the pills are named `Retry failed`/`Retry all` which the anchored regex does not match either way.

---

## Task 1: `retryJob` on the API client

**Files:**
- Modify: `web/src/jobs/api.ts` (add after `cancelJob`)
- Test: `web/src/jobs/api.test.ts` (append; add `retryJob` to the existing import list)

- [ ] **Step 1: Write the failing tests**

Append to `web/src/jobs/api.test.ts`, and add `retryJob` to the `from './api'` import block:

```ts
test('retryJob POSTs /jobs/{id}/retry?task=failed with no request body', async () => {
  let search = ''
  let method = ''
  let body = ''
  server.use(
    http.post('/v1/jobs/j1/retry', async ({ request }) => {
      search = new URL(request.url).search
      method = request.method
      body = await request.text()
      return HttpResponse.json({ id: 'j1', status: 'running', tasks_retried: 3 })
    }),
  )
  const res = await retryJob('j1', 'failed')
  expect(method).toBe('POST')
  expect(search).toBe('?task=failed')
  // handleRetryJob never calls readJSON; a body would be a contract violation.
  expect(body).toBe('')
  expect(res.tasks_retried).toBe(3)
})

test('retryJob sends ?task=all for the all mode', async () => {
  let search = ''
  server.use(
    http.post('/v1/jobs/j1/retry', ({ request }) => {
      search = new URL(request.url).search
      return HttpResponse.json({ id: 'j1', status: 'running', tasks_retried: 9 })
    }),
  )
  await retryJob('j1', 'all')
  expect(search).toBe('?task=all')
})
```

- [ ] **Step 2: Run them to verify they fail**

```powershell
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f/web; npx vitest run src/jobs/api.test.ts
```

Expected: FAIL - `No "retryJob" export is defined on the "./api" module`.

- [ ] **Step 3: Write the minimal implementation**

Add to `web/src/jobs/api.ts`, directly below `cancelJob`:

```ts
// The two retry modes accepted by POST /v1/jobs/{id}/retry. `failed` reopens
// tasks in `failed` AND `timed_out`; `all` widens that to `done` as well
// (internal/store/query/tasks.sql, RetryJobTasks). There is no third value and no
// default: handleRetryJob 400s an absent, empty, repeated or unrecognized ?task
// rather than guessing, because a misread here means "re-ran everything".
export type RetryMode = 'failed' | 'all'

// What the caller is allowed to believe about a 200 from the retry endpoint.
//
// The response body is a full jobResponse plus this field, but it is built with
// `toJobResponse(job, "", nil, nil)` (internal/api/jobs.go, handleRetryJob), so
// `total_tasks`/`done_tasks` are ZERO, `tasks` is absent and `submitted_by_email`
// is absent. Writing that body into the ['job', id] cache would blank the task
// table on the page the user is looking at. `tasks_retried` is the only field
// that means anything here, and it is always >= 1 (a zero-match is a 409, never a
// successful no-op), so it is the only field this type exposes.
// See docs/backlog/bug-2026-08-13-single-job-responses-report-zero-total-tasks.md.
export interface RetryResult {
  tasks_retried: number
}

// Re-runs a finished job's tasks. Sends NO body - handleRetryJob never calls
// readJSON and ?task= is a query parameter, matching ?force= on cancelJob.
export function retryJob(id: string, mode: RetryMode): Promise<RetryResult> {
  const q = new URLSearchParams({ task: mode })
  return apiFetch<RetryResult>(`/jobs/${id}/retry?${q}`, { method: 'POST' })
}
```

- [ ] **Step 4: Run them to verify they pass**

```powershell
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f/web; npx vitest run src/jobs/api.test.ts
```

Expected: PASS, whole file green.

- [ ] **Step 5: Commit**

```bash
git add web/src/jobs/api.ts web/src/jobs/api.test.ts
git commit -m "feat(web): retryJob API client for POST /v1/jobs/{id}/retry"
```

---

## Task 2: the 409 classifier

**Files:**
- Create: `web/src/jobs/retryError.ts`
- Test: `web/src/jobs/retryError.test.ts`

- [ ] **Step 1: Write the failing test**

Create `web/src/jobs/retryError.test.ts`:

```ts
import { expect, test } from 'vitest'
import { ApiError } from '../lib/api'
import { classifyRetryFailure } from './retryError'

function conflict(sentence: string) {
  return new ApiError(409, sentence, `409 ${sentence}`)
}

test('a nothing-matched 409 is a dead end, not a retryable failure', () => {
  const f = classifyRetryFailure(
    conflict('no tasks matched task=failed; this job has no failed or timed_out tasks'),
    'failed',
  )
  expect(f.kind).toBe('none-matched')
  expect(f.message).toContain('no failed or timed_out tasks')
  expect(f.hint).toBe('Nothing was changed.')
})

test('a blocked 409 in failed mode points at Retry all, which is what unblocks it', () => {
  const f = classifyRetryFailure(
    conflict(
      'no tasks were reopened: a selected task has dependents that have already run, ' +
        'or the job changed while the request was in flight; nothing was applied',
    ),
    'failed',
  )
  expect(f.kind).toBe('blocked')
  expect(f.hint).toContain('Retry all')
})

test('a blocked 409 in all mode does NOT suggest Retry all', () => {
  const f = classifyRetryFailure(
    conflict(
      'no tasks were reopened: a selected task has dependents that have already run, ' +
        'or the job changed while the request was in flight; nothing was applied',
    ),
    'all',
  )
  expect(f.kind).toBe('blocked')
  expect(f.hint).not.toContain('Retry all')
})

test('a raced 409 says the action can be repeated', () => {
  const f = classifyRetryFailure(
    conflict('the job changed while the retry was in flight; nothing was applied - try again'),
    'failed',
  )
  expect(f.kind).toBe('raced')
  expect(f.hint).toContain('Retry the action.')
})

test('the three 409 kinds never share a rendered string', () => {
  const kinds = [
    classifyRetryFailure(conflict('no tasks matched task=all; this job has no finished tasks'), 'all'),
    classifyRetryFailure(conflict('no tasks were reopened: a selected task has dependents that have already run, or the job changed while the request was in flight; nothing was applied'), 'all'),
    classifyRetryFailure(conflict('the job changed while the retry was in flight; nothing was applied - try again'), 'all'),
  ]
  const rendered = kinds.map((k) => `${k.message} ${k.hint}`)
  expect(new Set(rendered).size).toBe(3)
})

test('a cancelled job is a permanent refusal', () => {
  const f = classifyRetryFailure(conflict('job was cancelled; retry is not available for a cancelled job'), 'failed')
  expect(f.kind).toBe('cancelled')
})

test('an unfinished job is a wait-and-try-later refusal', () => {
  const f = classifyRetryFailure(conflict('job is not finished; retry is available for a done or failed job'), 'failed')
  expect(f.kind).toBe('not-finished')
})

test('a 404 reads as absent-or-not-yours, never as a server fault', () => {
  const f = classifyRetryFailure(new ApiError(404, 'job not found', '404 job not found'), 'failed')
  expect(f.kind).toBe('denied')
  expect(f.hint).toContain('owner')
})

test('an unrecognized error falls back to the server text, never to a generic string', () => {
  const f = classifyRetryFailure(new ApiError(500, 'db error', '500 db error'), 'failed')
  expect(f.kind).toBe('unknown')
  expect(f.message).toBe('500 db error')
})

test('a non-ApiError still renders its own message', () => {
  const f = classifyRetryFailure(new Error('network down'), 'failed')
  expect(f.kind).toBe('unknown')
  expect(f.message).toBe('network down')
})
```

- [ ] **Step 2: Run it to verify it fails**

```powershell
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f/web; npx vitest run src/jobs/retryError.test.ts
```

Expected: FAIL - `Failed to resolve import "./retryError"`.

- [ ] **Step 3: Write the implementation**

Create `web/src/jobs/retryError.ts`:

```ts
import { ApiError } from '../lib/api'
import type { RetryMode } from './api'

// The three conflicts POST /v1/jobs/{id}/retry can report are NOT interchangeable:
// nothing-matched is a dead end, blocked-by-dependents is permanent for this job
// in this mode, and raced means "do it again". Rendering all three as one generic
// failure would hand the operator no next step, so this module turns the server's
// sentence into a kind plus a frontend-owned hint.
//
// Classification keys on ApiError.code, which apiFetch fills with the server's
// {"error": ...} string VERBATIM (ApiError.message is that string with the numeric
// status prefixed). The prefixes below are copied from handleRetryJob in
// internal/api/jobs.go and are pinned by retryErrorContract.test.ts, which reads
// that file and reddens if a prefix stops existing.
//
// Unrecognized input NEVER collapses to a generic string: it falls through to the
// server's own text, so the reasons stay distinguishable even if the prose drifts
// ahead of this file.
export const RETRY_ERROR_PREFIXES = {
  noneMatched: 'no tasks matched',
  blocked: 'no tasks were reopened',
  raced: 'the job changed',
  cancelled: 'job was cancelled',
  notFinished: 'job is not finished',
} as const

export type RetryFailureKind =
  | 'none-matched'
  | 'blocked'
  | 'raced'
  | 'cancelled'
  | 'not-finished'
  | 'denied'
  | 'unknown'

export interface RetryFailure {
  kind: RetryFailureKind
  /** The sentence to show. The server's own wording whenever there is one. */
  message: string
  /** Frontend-owned next step. Empty when there is nothing useful to add. */
  hint: string
}

export function classifyRetryFailure(err: unknown, mode?: RetryMode): RetryFailure {
  if (!(err instanceof ApiError)) {
    const message = err instanceof Error ? err.message : 'Retry failed.'
    return { kind: 'unknown', message, hint: '' }
  }

  if (err.status === 404) {
    return {
      kind: 'denied',
      message: 'This job is not available to retry.',
      hint: 'It may have been removed, or you may not be its owner.',
    }
  }

  if (err.status === 409) {
    const p = RETRY_ERROR_PREFIXES
    if (err.code.startsWith(p.noneMatched)) {
      return { kind: 'none-matched', message: err.code, hint: 'Nothing was changed.' }
    }
    if (err.code.startsWith(p.blocked)) {
      // The server sentence hedges ("or the job changed"), so the hint must not
      // assert dependents as a certainty. The Retry all suggestion is verified
      // against RetryJobTasks: its guard ignores a descendant that is itself in
      // `selected`, and task=all puts every finished descendant in `selected`.
      return {
        kind: 'blocked',
        message: err.code,
        hint:
          mode === 'failed'
            ? 'Nothing was applied. Retry all also reopens the tasks that depend on these, which is usually what unblocks it.'
            : 'Nothing was applied.',
      }
    }
    if (err.code.startsWith(p.raced)) {
      return { kind: 'raced', message: err.code, hint: 'Nothing was applied. Retry the action.' }
    }
    if (err.code.startsWith(p.cancelled)) {
      return { kind: 'cancelled', message: err.code, hint: 'A cancelled job cannot be retried.' }
    }
    if (err.code.startsWith(p.notFinished)) {
      return { kind: 'not-finished', message: err.code, hint: 'Wait for the job to finish, then retry it.' }
    }
  }

  return { kind: 'unknown', message: err.message, hint: '' }
}
```

- [ ] **Step 4: Run it to verify it passes**

```powershell
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f/web; npx vitest run src/jobs/retryError.test.ts
```

Expected: PASS, 10 tests.

- [ ] **Step 5: Commit**

```bash
git add web/src/jobs/retryError.ts web/src/jobs/retryError.test.ts
git commit -m "feat(web): classify the retry endpoint's three distinct 409 conflicts"
```

---

## Task 3: the `retry` mutation

**Files:**
- Modify: `web/src/jobs/useJobActions.ts`
- Test: `web/src/jobs/useJobActions.test.tsx` (append)

- [ ] **Step 1: Write the failing tests**

Append to `web/src/jobs/useJobActions.test.tsx`:

```tsx
test('retry POSTs /jobs/{id}/retry?task=failed', async () => {
  const client = newClient()
  let task: string | null = null
  server.use(
    http.post(`/v1/jobs/${ID}/retry`, ({ request }) => {
      task = new URL(request.url).searchParams.get('task')
      return HttpResponse.json({ id: ID, status: 'running', tasks_retried: 2 })
    }),
  )
  const { result } = renderHook(() => useJobActions(ID), { wrapper: makeWrapper(client) })
  await result.current.retry.mutateAsync('failed')

  expect(task).toBe('failed')
})

test('retry POSTs ?task=all when mutated with all', async () => {
  const client = newClient()
  let task: string | null = null
  server.use(
    http.post(`/v1/jobs/${ID}/retry`, ({ request }) => {
      task = new URL(request.url).searchParams.get('task')
      return HttpResponse.json({ id: ID, status: 'running', tasks_retried: 7 })
    }),
  )
  const { result } = renderHook(() => useJobActions(ID), { wrapper: makeWrapper(client) })
  await result.current.retry.mutateAsync('all')

  expect(task).toBe('all')
})

test('a successful retry invalidates all THREE keys: [job,id], [jobs], and [job-stats]', async () => {
  const client = newClient()
  const spy = vi.spyOn(client, 'invalidateQueries')
  server.use(
    http.post(`/v1/jobs/${ID}/retry`, () =>
      HttpResponse.json({ id: ID, status: 'running', tasks_retried: 1 }),
    ),
  )
  const { result } = renderHook(() => useJobActions(ID), { wrapper: makeWrapper(client) })
  await result.current.retry.mutateAsync('failed')

  await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: ['job', ID] }))
  await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: ['jobs'] }))
  await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: ['job-stats'] }))
})

test('a 409 retry rejects and invalidates nothing', async () => {
  const client = newClient()
  const spy = vi.spyOn(client, 'invalidateQueries')
  server.use(
    http.post(`/v1/jobs/${ID}/retry`, () =>
      HttpResponse.json(
        { error: 'no tasks matched task=failed; this job has no failed or timed_out tasks' },
        { status: 409 },
      ),
    ),
  )
  const { result } = renderHook(() => useJobActions(ID), { wrapper: makeWrapper(client) })

  await expect(result.current.retry.mutateAsync('failed')).rejects.toBeTruthy()
  expect(spy).not.toHaveBeenCalledWith({ queryKey: ['job', ID] })
})
```

- [ ] **Step 2: Run them to verify they fail**

```powershell
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f/web; npx vitest run src/jobs/useJobActions.test.tsx
```

Expected: FAIL - `Cannot read properties of undefined (reading 'mutateAsync')` on `result.current.retry`.

- [ ] **Step 3: Write the implementation**

Rewrite `web/src/jobs/useJobActions.ts` as:

```ts
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { cancelJob, retryJob, type RetryMode } from './api'

// Cancel and retry mutations for the job-detail actions bar. Follows the
// invalidate-on-success strategy of useWorkerActions. Key invariants:
//  - ONE mutation per action; the mode is its variable (cancel.mutate(false|true),
//    retry.mutate('failed'|'all')). The only observable difference is the query
//    param the request carries.
//  - onSuccess invalidates THREE keys: ['job', id], ['jobs'], and ['job-stats'].
//    ['job-stats'] is decoupled from ['jobs'] (see queryKeyDecoupling.test.tsx),
//    so the bare ['jobs'] invalidation alone would leave the KPI strip stale.
//  - ['job', id] IS invalidated (a cancelled job is still viewable); the caller
//    stays on the detail page. This is the opposite of worker revoke.
//  - No optimistic update; useJob polls ['job', id] every 3s and the invalidate
//    triggers an immediate refetch.
//  - RETRY INVALIDATES, IT DOES NOT SEED. The 200 body is built with
//    toJobResponse(job, "", nil, nil) (internal/api/jobs.go, handleRetryJob), so
//    total_tasks/done_tasks are 0 and `tasks` is absent entirely. Writing it into
//    ['job', id] would blank the task table, the DAG and the progress strip. See
//    RetryResult in api.ts.
//  - There is no fourth key. Task statuses live INSIDE the ['job', id] payload
//    (useJob), and log lines never enter TanStack at all (useTaskLogStream keeps
//    them in component state), so nothing else caches what a retry changes.
export function useJobActions(id: string) {
  const qc = useQueryClient()

  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ['job', id] })
    qc.invalidateQueries({ queryKey: ['jobs'] })
    qc.invalidateQueries({ queryKey: ['job-stats'] })
  }

  const cancel = useMutation({
    mutationFn: (force: boolean) => cancelJob(id, force),
    onSuccess: invalidate,
  })

  const retry = useMutation({
    mutationFn: (mode: RetryMode) => retryJob(id, mode),
    onSuccess: invalidate,
  })

  return { cancel, retry }
}
```

- [ ] **Step 4: Run them to verify they pass**

```powershell
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f/web; npx vitest run src/jobs/useJobActions.test.tsx
```

Expected: PASS - 8 tests, including the four pre-existing cancel tests, whose bodies were **not** touched. The pre-existing `onSuccess invalidates all THREE keys` test is the proof that extracting `invalidate` preserved cancel's behaviour.

- [ ] **Step 5: Commit**

```bash
git add web/src/jobs/useJobActions.ts web/src/jobs/useJobActions.test.tsx
git commit -m "feat(web): retry mutation with the same three-key invalidation as cancel"
```

---

## Task 4: the pills, the availability rule, and the confirm dialogs

**Files:**
- Modify: `web/src/jobs/JobActions.tsx`
- Test: `web/src/jobs/JobActions.test.tsx` (append)

- [ ] **Step 1: Write the failing tests**

Append to `web/src/jobs/JobActions.test.tsx`:

```tsx
test('a done job shows Retry failed and Retry all, and no cancel pills', () => {
  renderActions({ ...JOB, status: 'done' })
  expect(screen.getByRole('button', { name: 'Retry failed' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Retry all' })).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Cancel' })).not.toBeInTheDocument()
})

test('a failed job shows the retry pills AND the cancel pills (server allows both)', () => {
  renderActions({ ...JOB, status: 'failed' })
  expect(screen.getByRole('button', { name: 'Retry failed' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Retry all' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Cancel' })).toBeInTheDocument()
})

test('a running job shows NO retry pills (the server 409s an unfinished job)', () => {
  renderActions(JOB)
  expect(screen.queryByRole('button', { name: 'Retry failed' })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Retry all' })).not.toBeInTheDocument()
})

test('a cancelled job shows NO retry pills (the server refuses it permanently)', () => {
  renderActions({ ...JOB, status: 'cancelled' })
  expect(screen.queryByRole('button', { name: 'Retry failed' })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Retry all' })).not.toBeInTheDocument()
})

test('Retry failed confirms first, then POSTs ?task=failed', async () => {
  let task: string | null = null
  server.use(
    http.post(`/v1/jobs/${ID}/retry`, ({ request }) => {
      task = new URL(request.url).searchParams.get('task')
      return HttpResponse.json({ tasks_retried: 2 })
    }),
  )
  renderActions({ ...JOB, status: 'failed' })
  await userEvent.click(screen.getByRole('button', { name: 'Retry failed' }))
  await userEvent.click(screen.getByRole('button', { name: 'Retry failed tasks' }))
  await waitFor(() => expect(task).toBe('failed'))
})

test('Retry all confirms first, then POSTs ?task=all', async () => {
  let task: string | null = null
  server.use(
    http.post(`/v1/jobs/${ID}/retry`, ({ request }) => {
      task = new URL(request.url).searchParams.get('task')
      return HttpResponse.json({ tasks_retried: 5 })
    }),
  )
  renderActions({ ...JOB, status: 'done' })
  await userEvent.click(screen.getByRole('button', { name: 'Retry all' }))
  await userEvent.click(screen.getByRole('button', { name: 'Retry all tasks' }))
  await waitFor(() => expect(task).toBe('all'))
})

test('dismissing the retry confirm dialog fires no request', async () => {
  let hits = 0
  server.use(
    http.post(`/v1/jobs/${ID}/retry`, () => {
      hits++
      return HttpResponse.json({ tasks_retried: 1 })
    }),
  )
  renderActions({ ...JOB, status: 'done' })
  await userEvent.click(screen.getByRole('button', { name: 'Retry all' }))
  await userEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Cancel' }))
  await new Promise((r) => setTimeout(r, 20))
  expect(hits).toBe(0)
})

test('the retry confirm copy names what each mode re-runs', async () => {
  renderActions({ ...JOB, status: 'failed' })
  await userEvent.click(screen.getByRole('button', { name: 'Retry failed' }))
  const dialog = screen.getByRole('dialog')
  // "failed" silently includes timed_out server-side; the copy must say so.
  expect(within(dialog).getByText(/timed out/i)).toBeInTheDocument()
})
```

- [ ] **Step 2: Run them to verify they fail**

```powershell
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f/web; npx vitest run src/jobs/JobActions.test.tsx
```

Expected: FAIL - `Unable to find an accessible element with the role "button" and name "Retry failed"`.

- [ ] **Step 3: Write the implementation**

Rewrite `web/src/jobs/JobActions.tsx` as (this is the complete file; the error banner arrives in Task 5, and the `retryFailure` line below is deliberately not yet rendered):

```tsx
import { useState } from 'react'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { PillButton } from '../components/holo'
import { useJobActions } from './useJobActions'
import type { JobDetail } from './api'

type Pending = null | 'cancel' | 'force' | 'retry-failed' | 'retry-all'

// Job-detail header action bar. Owns the cancel pair, the retry pair, the confirm
// dialog, and the inline error. A cancelled or retried job stays viewable, so on
// success we do NOT navigate; the ['job', id] invalidation flips the status pill on
// refetch.
export function JobActions({ job }: { job: JobDetail }) {
  const { cancel, retry } = useJobActions(job.id)
  const [confirm, setConfirm] = useState<Pending>(null)

  // Hide the buttons only for states the server treats as terminal for cancel
  // (cancelled/done). `failed` is NOT terminal server-side, so it stays
  // cancellable and keeps its buttons.
  const terminal = job.status === 'cancelled' || job.status === 'done'

  // Retry availability, spelled as an ALLOW-LIST of the exact two statuses
  // handleRetryJob admits. Everything else - pending, running, cancelled - is a
  // deterministic 409 from its status switch, and a control that is always visible
  // and always fails is a dead control. The allow-list spelling is not cosmetic:
  // a status added to JobStatus later must be un-retryable until somebody decides
  // otherwise, which is the frontend reading of the rule RetryJobTasks's own
  // status predicate follows. A deny-list here would fail open.
  const retryable = job.status === 'done' || job.status === 'failed'

  const actionError = cancel.error as Error | null

  function openConfirm(which: Exclude<Pending, null>) {
    // Both mutations are reset, so a stale banner from the OTHER action cannot
    // outlive the next confirm.
    cancel.reset()
    retry.reset()
    setConfirm(which)
  }

  function runConfirmed() {
    if (confirm === 'cancel') cancel.mutate(false)
    else if (confirm === 'force') cancel.mutate(true)
    else if (confirm === 'retry-failed') retry.mutate('failed')
    else if (confirm === 'retry-all') retry.mutate('all')
    // The dialog closes BEFORE the response lands, on purpose: the error banner
    // below lives on the page, and an open dialog would put its scrim on top of
    // it, so the button would look like it did nothing. Do not "improve" this by
    // holding the dialog open while the request is in flight.
    setConfirm(null)
  }

  const confirmCopy: Record<Exclude<Pending, null>, { title: string; body: string; label: string; destructive?: boolean }> = {
    cancel: {
      title: `Cancel ${job.name}?`,
      body: 'Running tasks are asked to stop and the job is marked cancelled. Tasks that have not started are dropped.',
      // "Cancel job" (not "Cancel") avoids ambiguity with the dialog's own
      // "Cancel" dismiss button.
      label: 'Cancel job',
      destructive: true,
    },
    force: {
      title: `Force cancel ${job.name}?`,
      body: 'Running tasks are force-killed immediately and the job is marked cancelled. Use this when a graceful cancel is not stopping the work.',
      label: 'Force cancel',
      destructive: true,
    },
    'retry-failed': {
      title: `Retry failed tasks of ${job.name}?`,
      body: 'Every task that failed or timed out is queued again and the job goes back to running. Tasks that already succeeded are left alone.',
      // Distinct from the pill label, like "Cancel job" above, so a test (and a
      // screen reader) can tell the trigger from the confirmation.
      label: 'Retry failed tasks',
    },
    'retry-all': {
      title: `Retry all tasks of ${job.name}?`,
      body: 'Every finished task is queued again, including the ones that already succeeded, and the job goes back to running. This re-runs work that is already done and spends farm capacity on it.',
      label: 'Retry all tasks',
    },
  }

  return (
    <div className="flex flex-col gap-2">
      {(retryable || !terminal) && (
        <div className="flex items-center gap-2">
          {retryable && (
            <>
              <PillButton variant="ghost" disabled={retry.isPending} onClick={() => openConfirm('retry-failed')}>
                Retry failed
              </PillButton>
              <PillButton variant="ghost" disabled={retry.isPending} onClick={() => openConfirm('retry-all')}>
                Retry all
              </PillButton>
            </>
          )}
          {!terminal && (
            <>
              <PillButton variant="ghost" disabled={cancel.isPending} onClick={() => openConfirm('cancel')}>
                Cancel
              </PillButton>
              <PillButton variant="danger" disabled={cancel.isPending} onClick={() => openConfirm('force')}>
                Force cancel
              </PillButton>
            </>
          )}
        </div>
      )}

      {actionError ? (
        <div className="rounded-card border border-err/40 bg-err/10 px-4 py-2 text-[12px] text-err">
          {actionError.message}
        </div>
      ) : null}

      {confirm && (
        <ConfirmDialog
          title={confirmCopy[confirm].title}
          body={confirmCopy[confirm].body}
          confirmLabel={confirmCopy[confirm].label}
          destructive={confirmCopy[confirm].destructive}
          onConfirm={runConfirmed}
          onCancel={() => setConfirm(null)}
        />
      )}
    </div>
  )
}
```

- [ ] **Step 4: Run them to verify they pass**

```powershell
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f/web; npx vitest run src/jobs/JobActions.test.tsx
```

Expected: PASS - all pre-existing cancel tests plus the eight new ones. The pre-existing `a done job hides both buttons` and `a failed job STILL shows both buttons` tests are the byte-identical proof that the cancel branch's behaviour is unchanged.

- [ ] **Step 5: Commit**

```bash
git add web/src/jobs/JobActions.tsx web/src/jobs/JobActions.test.tsx
git commit -m "feat(web): Retry failed / Retry all pills on a finished job's header"
```

---

## Task 5: the classified 409 banner and the success line

**Files:**
- Modify: `web/src/jobs/JobActions.tsx`
- Test: `web/src/jobs/JobActions.test.tsx` (append)

- [ ] **Step 1: Write the failing tests**

Append to `web/src/jobs/JobActions.test.tsx`:

```tsx
// Helper: click Retry failed and confirm it, for the error-surface tests below.
async function retryFailedAndConfirm(status: 'done' | 'failed' = 'failed') {
  renderActions({ ...JOB, status })
  await userEvent.click(screen.getByRole('button', { name: 'Retry failed' }))
  await userEvent.click(screen.getByRole('button', { name: 'Retry failed tasks' }))
}

test('a nothing-matched 409 says nothing changed, not a generic failure', async () => {
  server.use(
    http.post(`/v1/jobs/${ID}/retry`, () =>
      HttpResponse.json(
        { error: 'no tasks matched task=failed; this job has no failed or timed_out tasks' },
        { status: 409 },
      ),
    ),
  )
  await retryFailedAndConfirm()
  expect(await screen.findByText(/no failed or timed_out tasks/)).toBeInTheDocument()
  expect(screen.getByText('Nothing was changed.')).toBeInTheDocument()
})

test('a blocked-by-dependents 409 points the operator at Retry all', async () => {
  server.use(
    http.post(`/v1/jobs/${ID}/retry`, () =>
      HttpResponse.json(
        {
          error:
            'no tasks were reopened: a selected task has dependents that have already run, ' +
            'or the job changed while the request was in flight; nothing was applied',
        },
        { status: 409 },
      ),
    ),
  )
  await retryFailedAndConfirm()
  expect(await screen.findByText(/dependents that have already run/)).toBeInTheDocument()
  expect(screen.getByText(/Retry all also reopens/)).toBeInTheDocument()
})

test('a raced 409 tells the operator to repeat the action', async () => {
  server.use(
    http.post(`/v1/jobs/${ID}/retry`, () =>
      HttpResponse.json(
        { error: 'the job changed while the retry was in flight; nothing was applied - try again' },
        { status: 409 },
      ),
    ),
  )
  await retryFailedAndConfirm()
  expect(await screen.findByText(/nothing was applied - try again/)).toBeInTheDocument()
  expect(screen.getByText(/Retry the action\./)).toBeInTheDocument()
})

test('the retry error is visible with NO dialog mounted over it', async () => {
  server.use(
    http.post(`/v1/jobs/${ID}/retry`, () =>
      HttpResponse.json(
        { error: 'the job changed while the retry was in flight; nothing was applied - try again' },
        { status: 409 },
      ),
    ),
  )
  await retryFailedAndConfirm()
  await screen.findByText(/nothing was applied - try again/)
  // An error rendered while the dialog is still open sits behind its own scrim
  // and the button reads as doing nothing. The dialog must already be gone.
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  // Still on the page: the pills are mounted, so the action can be repeated.
  expect(screen.getByRole('button', { name: 'Retry failed' })).toBeInTheDocument()
})

test('a successful retry reports how many tasks were re-queued', async () => {
  server.use(http.post(`/v1/jobs/${ID}/retry`, () => HttpResponse.json({ tasks_retried: 3 })))
  await retryFailedAndConfirm()
  expect(await screen.findByText('Retried 3 tasks.')).toBeInTheDocument()
})

test('a single retried task is reported in the singular', async () => {
  server.use(http.post(`/v1/jobs/${ID}/retry`, () => HttpResponse.json({ tasks_retried: 1 })))
  await retryFailedAndConfirm()
  expect(await screen.findByText('Retried 1 task.')).toBeInTheDocument()
})

test('the stats query refetches after a successful retry (three-key invalidation)', async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  let statsCalls = 0
  server.use(
    http.get('/v1/jobs/stats', () => {
      statsCalls++
      return HttpResponse.json({ running: 0, queued: 0, done_24h: 0, failed_24h: 0 })
    }),
    http.post(`/v1/jobs/${ID}/retry`, () => HttpResponse.json({ tasks_retried: 2 })),
  )
  // Mount useJobStats so ['job-stats'] has an ACTIVE observer; invalidateQueries
  // only refetches observed queries by default, so a bare fetchQuery seed would
  // make this pass vacuously.
  const { result: stats } = renderHook(() => useJobStats(100_000), {
    wrapper: ({ children }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>,
  })
  await waitFor(() => expect(stats.current.status).toBe('success'))
  expect(statsCalls).toBe(1)

  render(
    <QueryClientProvider client={client}>
      <JobActions job={{ ...JOB, status: 'failed' }} />
    </QueryClientProvider>,
  )
  await userEvent.click(screen.getByRole('button', { name: 'Retry failed' }))
  await userEvent.click(screen.getByRole('button', { name: 'Retry failed tasks' }))

  await waitFor(() => expect(statsCalls).toBe(2))
})
```

- [ ] **Step 2: Run them to verify they fail**

```powershell
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f/web; npx vitest run src/jobs/JobActions.test.tsx
```

Expected: FAIL - `Unable to find an element with the text: Nothing was changed.` (the first new test). The three-key test passes already, because Task 3 wired the invalidation; it is kept as the component-level end-to-end proof.

- [ ] **Step 3: Write the implementation**

In `web/src/jobs/JobActions.tsx`, add the import:

```tsx
import { classifyRetryFailure } from './retryError'
```

replace the single `actionError` line with:

```tsx
  const actionError = cancel.error as Error | null
  // `retry.variables` is TanStack's record of the argument the last mutate() call
  // carried, i.e. the mode this failure belongs to. It is defined whenever
  // retry.error is, and reset() clears both together, so no separate state is
  // needed to give the blocked hint its mode.
  const retryFailure = retry.error ? classifyRetryFailure(retry.error, retry.variables) : null
```

and replace the `{actionError ? ... : null}` block with:

```tsx
      {retryFailure ? (
        <div className="rounded-card border border-err/40 bg-err/10 px-4 py-2 text-[12px] text-err">
          <div>{retryFailure.message}</div>
          {retryFailure.hint ? <div className="mt-1 text-fg-mute">{retryFailure.hint}</div> : null}
        </div>
      ) : actionError ? (
        <div className="rounded-card border border-err/40 bg-err/10 px-4 py-2 text-[12px] text-err">
          {actionError.message}
        </div>
      ) : null}

      {/* The success line and the banner both sit OUTSIDE the availability gate on
          purpose: a successful retry flips the job to `running`, which hides the
          retry pills on the very refetch this success triggered. Rendering the
          confirmation inside that gate would unmount it immediately. */}
      {retry.data ? (
        <div className="rounded-card border border-ok/40 bg-ok/10 px-4 py-2 text-[12px] text-ok">
          Retried {retry.data.tasks_retried} {retry.data.tasks_retried === 1 ? 'task' : 'tasks'}.
        </div>
      ) : null}
```

- [ ] **Step 4: Run them to verify they pass**

```powershell
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f/web; npx vitest run src/jobs/JobActions.test.tsx
```

Expected: PASS - the whole file, including the pre-existing cancel-409 test `a 409 surfaces an inline error banner and does not navigate`, whose markup is unchanged.

- [ ] **Step 5: Commit**

```bash
git add web/src/jobs/JobActions.tsx web/src/jobs/JobActions.test.tsx
git commit -m "feat(web): distinct retry conflict surfaces and a retried-task count"
```

---

## Task 6: wire-up proof on the page, and the false comment

**Files:**
- Modify: `web/src/jobs/JobDetailPage.tsx` (comment only)
- Test: `web/src/jobs/JobDetailPage.test.tsx` (append one test, edit one comment)

- [ ] **Step 1: Write the failing test**

Append to `web/src/jobs/JobDetailPage.test.tsx`:

```tsx
test('an owner sees the retry pills in the reserved slot for a done job', async () => {
  server.use(http.get(`/v1/jobs/${ID}`, () => HttpResponse.json({ ...JOB, status: 'done' })))
  renderDetail()
  await screen.findByText('shot-042 render')
  const slot = screen.getByTestId('job-actions')
  expect(within(slot).getByRole('button', { name: 'Retry failed' })).toBeInTheDocument()
  expect(within(slot).getByRole('button', { name: 'Retry all' })).toBeInTheDocument()
})

test('a non-owner non-admin sees no retry pills on a done job', async () => {
  server.use(http.get(`/v1/jobs/${ID}`, () => HttpResponse.json({ ...JOB, status: 'done' })))
  renderDetail({ id: 'other', email: 'a@b.co', name: 'A', is_admin: false })
  await screen.findByText('shot-042 render')
  const slot = screen.getByTestId('job-actions')
  expect(within(slot).queryByRole('button', { name: 'Retry failed' })).not.toBeInTheDocument()
  expect(within(slot).queryByRole('button', { name: 'Retry all' })).not.toBeInTheDocument()
})
```

- [ ] **Step 2: Run them to verify they fail**

```powershell
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f/web; npx vitest run src/jobs/JobDetailPage.test.tsx
```

Expected: the first new test FAILS with `Unable to find an accessible element with the role "button" and name "Retry failed"` **if and only if** Tasks 4-5 were skipped. With Tasks 4-5 done, both pass immediately - these two tests pin the `canManage` gating and the slot placement (structure), they do not drive new behaviour. Run them and record that they are green; do not fabricate a RED.

- [ ] **Step 3: Fix the false comment in the page and the stale comment in the test**

In `web/src/jobs/JobDetailPage.tsx`, replace the header block comment that currently reads *"the reserved JobActions slot (ml-auto). No Retry/Abort header pill - there is no per-job retry endpoint and "Abort" is just cancel; the real Cancel/Force cancel live in JobActions."* with:

```tsx
      {/* Breadcrumb + header row: back link, id, name, inline status; the reserved
          JobActions slot (ml-auto). The hi-fi's "Abort" pill is just cancel, and its
          single "Retry" pill became two - Retry failed / Retry all - because
          POST /v1/jobs/{id}/retry requires ?task= and has no default. All four live
          in JobActions, which hides each pair for the statuses the server refuses. */}
```

In `web/src/jobs/JobDetailPage.test.tsx`, inside `does not fabricate unbacked timing or the live-log affordances`, replace only the comment lines above the four assertions with:

```tsx
  // Omitted per spec (no backend field): elapsed and ETA. The hi-fi's Retry/Abort
  // pills are absent HERE because this fixture job is `running`: Abort was never
  // built (it is just cancel), and the retry pills are shown only for a done or
  // failed job, which is the exact set POST /v1/jobs/{id}/retry accepts. A dead
  // control reads as broken.
```

The four `expect` lines below it stay **byte-identical**, and they stay true: this fixture is `running`, and the anchored `/^retry$/i` matcher would not match `Retry failed` or `Retry all` in any case.

- [ ] **Step 4: Run the file to verify it is green**

```powershell
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f/web; npx vitest run src/jobs/JobDetailPage.test.tsx
```

Expected: PASS, whole file.

- [ ] **Step 5: Commit**

```bash
git add web/src/jobs/JobDetailPage.tsx web/src/jobs/JobDetailPage.test.tsx
git commit -m "feat(web): retry pills reach the job-detail header slot"
```

---

## Task 7: pin the classifier's prefixes to the Go handler

**Files:**
- Test: `web/src/jobs/retryErrorContract.test.ts` (create)

Rationale: every sentence in `retryError.ts` was copied out of `handleRetryJob` by a planner, and plan-supplied test strings are guesses until something checks them. MSW tests cannot catch a copy error, because the test and the classifier share the same guessed string. This test reads the Go source and reddens when a prefix stops existing - which is also what makes it safe for a future backend author to reword an error and find out immediately.

- [ ] **Step 1: Write the failing test**

Create `web/src/jobs/retryErrorContract.test.ts`:

```ts
import { readFileSync } from 'node:fs'
import { expect, test } from 'vitest'
import { RETRY_ERROR_PREFIXES } from './retryError'

// Reads the Go handler this module classifies. Vitest runs under Node, so
// node:fs is available even with the jsdom environment; import.meta.url resolves
// against this file, so the path holds no matter where vitest is invoked from.
const JOBS_GO = new URL('../../../internal/api/jobs.go', import.meta.url)

test('every prefix classifyRetryFailure matches still exists in handleRetryJob', () => {
  // readFileSync throws if the path is wrong - which is the intended failure. A
  // try/catch that skipped would turn this contract into decoration.
  const src = readFileSync(JOBS_GO, 'utf8')
  expect(src).toContain('func (s *Server) handleRetryJob(')
  for (const [name, prefix] of Object.entries(RETRY_ERROR_PREFIXES)) {
    expect(src, `retryError.ts prefix "${name}" no longer appears in internal/api/jobs.go`).toContain(prefix)
  }
})
```

- [ ] **Step 2: Run it**

```powershell
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f/web; npx vitest run src/jobs/retryErrorContract.test.ts
```

Expected: PASS. To prove it is not vacuous, temporarily change `raced: 'the job changed'` in `retryError.ts` to `raced: 'the job wobbled'` and re-run: expect FAIL naming `raced`. **Revert that edit before continuing** - the discriminating input lives in the loop, not in a one-off tweak, so nothing needs to be left behind.

- [ ] **Step 3: Commit**

```bash
git add web/src/jobs/retryErrorContract.test.ts
git commit -m "test(web): pin the retry error prefixes to internal/api/jobs.go"
```

---

## Task 8: full suite and close the item

- [ ] **Step 1: Run the whole web suite**

```powershell
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f/web; npm test
```

Expected: PASS. Baseline before this slice is 1068 tests (not 1059 - three slices merged since that
figure was taken). This slice adds 34 (2+10+4+8+7+2+1), so expect **1102 passing** and zero
failures. A different total is a finding, not a rounding error - report it, and state which task's
count was off rather than adjusting the expectation to match the run.

- [ ] **Step 2: Confirm the working tree contains exactly the intended files**

```powershell
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f; git status --porcelain
```

Expected: nothing but the eleven files this plan names. **If `web/dist/` appears, it is stale scaffold output and must be reverted** - no task in this plan runs `npm run build`, so a dirty `web/dist` means something ran a build:

```powershell
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f; git checkout -- web/dist/
```

- [ ] **Step 3: Close the backlog item (conductor step)**

```
/backlog close feature-2026-07-01-job-retry-action
```

This `git mv`s the file into `docs/backlog/closed/`, stamps the frontmatter and appends a Resolution note. Never hand-edit `status:`.

---

## Test inventory: what each test proves

**Behaviour (a real defect makes it red):**

| Test | Behaviour it proves |
|---|---|
| `retryJob POSTs /jobs/{id}/retry?task=failed with no request body` | URL, method, and the no-body contract |
| `retryJob sends ?task=all for the all mode` | the mode reaches the wire |
| all ten `retryError.test.ts` tests | the three conflicts are classified apart and never collapse |
| `the three 409 kinds never share a rendered string` | the non-interchangeability rule itself, as a property |
| `retry POSTs ?task=failed` / `?task=all` (hook) | the call-site argument survives the mutation layer |
| `a successful retry invalidates all THREE keys` | the invalidation policy |
| `a 409 retry rejects and invalidates nothing` | no cache churn on failure |
| the four availability tests | the dead-control rule, per status |
| `Retry failed confirms first, then POSTs ?task=failed` (+ `all`) | confirm gating AND the param, end to end |
| `dismissing the retry confirm dialog fires no request` | the confirm is a real gate |
| the three 409-surface tests | each reason renders its own message and hint |
| `the retry error is visible with NO dialog mounted over it` | the scrim lesson |
| `a successful retry reports how many tasks were re-queued` (+ singular) | `tasks_retried` is read from the response and nothing else is |
| `the stats query refetches after a successful retry` | invalidation reaches a live observer |
| `every prefix ... still exists in handleRetryJob` | the classifier is not built on guessed prose |

**Structure only (pins wiring, would not catch a logic defect):**

| Test | What it pins |
|---|---|
| `an owner sees the retry pills in the reserved slot for a done job` | `JobActions` is mounted in `data-testid="job-actions"` |
| `a non-owner non-admin sees no retry pills on a done job` | the `canManage` gate covers the new pills too |
| `the retry confirm copy names what each mode re-runs` | copy content, not logic |

**No test in this plan depends on layout.** jsdom does no layout, so nothing asserts widths, wrapping, overflow, or the four-pill row on a `failed` job. If the four-pill density needs checking, that is a browser-lane observation, not a unit test.

## Mutation table: which single test reddens

| Mutation | The one test that reddens |
|---|---|
| drop `?task` from `retryJob` (send a bare POST) | `retryJob POSTs /jobs/{id}/retry?task=failed with no request body` |
| hardcode `task=failed` in `retryJob`, ignoring `mode` | `retryJob sends ?task=all for the all mode` |
| add a JSON body to `retryJob` | `retryJob POSTs ... with no request body` |
| drop `['job-stats']` from `invalidate` | hook: `a successful retry invalidates all THREE keys` (the component-level stats test reddens too; the hook one is the specific probe) |
| seed the cache instead of invalidating (`qc.setQueryData(['job', id], data)`) | none - **this is why `retryJob` returns `RetryResult`, not `JobDetail`: the type makes the mutation not compile.** The typed narrowing is the control here, not a test |
| swap the `blocked` and `raced` prefixes | `a raced 409 tells the operator to repeat the action` |
| return one shared message for all 409s | `the three 409 kinds never share a rendered string` |
| pass no `mode` to `classifyRetryFailure` | `a blocked 409 in failed mode points at Retry all, which is what unblocks it` |
| widen `retryable` to a deny-list (`status !== 'cancelled'`) | `a running job shows NO retry pills` |
| show the pills on a cancelled job | `a cancelled job shows NO retry pills` |
| fire the mutation from `onClick` instead of from the dialog | `dismissing the retry confirm dialog fires no request` |
| keep the dialog open across the request (`setConfirm(null)` moved into `onSuccess`) | `the retry error is visible with NO dialog mounted over it` |
| move the success line inside the `retryable` gate | none in jsdom (the component is rendered with a static `job` prop and never re-polls) - this is the ONE design decision the unit suite cannot catch, and it is the browser lane's job |
| reword a Go error string without touching `retryError.ts` | `every prefix classifyRetryFailure matches still exists in handleRetryJob` |

## Phase 4 verification lanes

- **Zero Go changes**, so the Go integration lane has nothing to exercise. Reassign that slot to a **real-browser lane against a live `relay-server`**, as on the other zero-Go slices in this batch.
- The browser lane must cover the two things jsdom structurally cannot:
  1. **The success line surviving the status flip.** Retry a `failed` job, confirm, and watch: `Retried N tasks.` must stay visible while the pills disappear as the job goes `running`. jsdom cannot catch a regression here because `JobActions` is unit-tested with a static `job` prop.
  2. **A real 409, from the real handler.** Click `Retry failed` on a `done` job with no failed tasks and confirm the rendered sentence matches what the server actually sent - the unit suite only ever sees the plan's copy of that sentence.
  3. Secondary: the four-pill header row on a `failed` job at a 375 px viewport, since the app-wide narrow-viewport overflow item is still open and this slice adds two pills to the measured header.
- `/code-review` plus the three code-reviewer lenses apply as usual. Point the invariants lens at Decision 3 (the zeroed-enrichment trap) and Decision 4 (allow-list spelling); point the correctness lens at `retry.variables` as the mode source and at the `runConfirmed` ordering.
