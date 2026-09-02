# Jobs Lanes (swimlanes-by-status) view - Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The Jobs page gains a second view - five status lanes, each fed by its own capped `GET /v1/jobs?status=<s>&limit=10` - chosen by a Table / Lanes switch that persists to `localStorage`, with the table view's data flow, sorting, filtering and pagination untouched.

**Architecture:** Three new frontend modules (`lanes.ts` pure data, `useJobLanes.ts` one `useQueries`, `JobsLanes.tsx` the presentational view), one new tuple and one new fetch function in `web/src/jobs/api.ts`, one optional `enabled` parameter on `useJobs`, and in `JobsPage.tsx` a `view` state, a view switch, a sixth status chip and one guarded early return that sits **above** the existing `<JobsTable>` element. No Go file, no SQL, no migration, no `make generate`.

**Tech Stack:** React 18, TypeScript 5.7 (`tsc -b`), TanStack Query v5 (`useQueries`, `keepPreviousData`, `enabled`), react-router-dom 7, Tailwind v4 (static source scan - see `web/CLAUDE.md`), vitest 2 + @testing-library/react 16 + user-event 14 + msw 2 (jsdom), Playwright 1.62 for the one real-layout check.

**Spec:** `docs/superpowers/specs/2026-09-01-jobs-lanes-view-design.md` (committed on this branch)
**Backlog item this closes:** `docs/backlog/idea-2026-06-05-jobs-lanes-swimlanes-view.md` - **the engineer does NOT close it. Do not run `/backlog close`.** The conductor closes it after the PR merges.
**In-tree exemplars this slice copies:** `web/src/workers/WorkersPage.tsx` (the `localStorage` view switch), `web/src/workers/useRevokedWorkers.ts` (an `enabled`-gated poll), `web/src/components/holo/Table.tsx:189-193` (the horizontal scroll container), `web/src/jobs/api.ts:76-79` + `web/src/jobs/api.test.ts:218-237` (a filtered list function that must never send `sort`, and the shape of its test).

---

## Slice independence declaration

**This is a single-slice, single-PR, single-session plan. It has no stages and must NOT be handed to `/backlog phases`.**

**Frontend only. There is no backend slice, and no backend work exists to parallelise against.** Every endpoint, query parameter and response field this feature needs already ships: `handleListJobs`'s status branch (`internal/api/jobs.go:459-480`) returns `page[jobResponse]{items, next_cursor, total}` for a single `?status=`, and `parsePage` (`internal/api/pagination.go:239-249`) already accepts `?limit=10`. **Do not dispatch `relay-backend-engineer` for any part of this plan.** No `.sql`, `.proto` or `.go` file is touched, so **`make generate` must not be run** and no `*.sql.go` or `models.go` file may appear in any diff.

Phase 3 is a single frontend lane. The tasks below are **sequential** and the order is load-bearing in three places:

- **Task 1 gates Tasks 2, 3 and 7.** `JOB_STATUSES` and `listJobsByStatus` do not exist as symbols until Task 1 lands, so `lanes.ts` and `useJobLanes.ts` cannot compile before it.
- **Task 3 gates Task 4.** `LaneState` is `JobsLanes`'s entire input contract; its test file imports the type.
- **Task 6 gates Tasks 7 and 8.** All three edit `JobsPage.tsx`, and 7 and 8 build on the `view` state 6 introduces.

---

## MERGE CONTRACT WITH LANE B - read this before touching `JobsPage.tsx`

A sibling branch is concurrently changing `useCursorPager.next` to take the page object instead of `(cursor, rows.length)`. Its edit lands on **line 151** of `web/src/jobs/JobsPage.tsx` at HEAD, inside the `footer` prop of the `<JobsTable ...>` element.

**The contract, in three rules:**

1. **No new file may import `useCursorPager`.** The lanes view constructs, reads and resets no cursor. Nothing in `lanes.ts`, `useJobLanes.ts`, `JobsLanes.tsx` or any new test file may reference it.
2. **Every `JobsPage.tsx` edit lands ABOVE the `<JobsTable ...>` element.** The element and its whole `footer` prop (lines 132-160 at HEAD) come out **byte-identical**.
3. **Do NOT extract the table branch into a new component file, and do NOT re-indent the existing return block.** Either turns lane B's one-line edit into a delete-plus-add conflict. An implementer who finds themselves reaching for either must stop and raise it.

**The exact check, run from the worktree root in Git Bash, in Task 10 and after any `JobsPage.tsx` edit:**

```bash
git fetch origin main
git diff origin/main -- web/src/jobs/JobsPage.tsx | grep -E '^[+-]' \
  | grep -E 'JobsTable|pager\.(next|prev)|canPrev|next 50|SHOWING|CURSOR PAGINATED|rangeText|computePageRange'
```

Expected: **no output** (grep exits 1). Any line printed means an added or removed line touched the frozen region and the edit must be redone.

**Run the positive control immediately after**, so a green result is not just a broken pipeline:

```bash
git diff origin/main -- web/src/jobs/JobsPage.tsx | grep -E '^[+-]' | grep -E 'FILTERS|view'
```

Expected: **several lines**. If this also prints nothing, the diff or the fetch is wrong, not the code.

---

## What I refuted in the spec

I re-derived every claim the spec makes about the tree. It is accurate on all six of its own refutations (I re-checked `internal/api/jobs.go:412-489`, `internal/api/pagination.go:239-249`, `web/src/jobs/api.ts:50-56`, `web/src/jobs/JobsPage.test.tsx:51`, `web/src/workers/WorkersPage.tsx:17-21,47-50` and `web/src/components/holo/Table.tsx:189-193` directly). **Four findings**, three of which change what the engineer must build.

### R1. `useJobLanes(enabled, ...)` is vacuous where the spec puts the hook, and the header's liveness dot goes dead in lanes view

The spec's component tree (Design section) has `JobsLanes` own the `useQueries` call, and `JobsPage` render `<JobsLanes onShowAll={showAll} />`. But `JobsLanes` renders **only** when `view === 'lanes'`, so an `enabled` parameter passed from inside it is always `true` and gates nothing. The parameter and its placement contradict each other.

Worse, it takes the liveness indicator down with it. `JobsPage.tsx:88` drives the live dot from `useJobs`'s `isFetching`, and Decision 6 disables that query in lanes view - so the dot would be permanently `text-fg-dim` beside text reading "live - auto-refreshing", which Decision 6 explicitly claims "stays true in both views". It would be false in exactly the view this slice adds.

**Resolution, adopted below:** `JobsPage` calls `useJobLanes(view === 'lanes')` itself and passes the result down as a prop. That makes `enabled` real and testable (Task 3's disabled test), makes the dot honest (`lanes.some((l) => l.isFetching)` in lanes view), and turns `JobsLanes` into a pure presentational component whose failure-isolation, empty, overflow, accessible-name and keyboard tests need no MSW and no timing at all.

### R2. The hook must return a narrow shape, not the raw `useQueries` results

Following from R1: the spec says `useJobLanes` "returns the raw results array; `JobsLanes` zips it with `LANE_ORDER` by index". A `UseQueryResult` is not hand-buildable in a test without either casting or a real query client, so every `JobsLanes` test would have to go back through MSW and timing. The hook returns `LaneState[]` instead - already zipped, `status` carried on each entry, so `JobsLanes` never indexes into `LANE_ORDER` and cannot mis-zip. The `error && !data` rule from the spec's states table is applied **in the hook** (a failed poll that still has rows keeps showing them), so the view has one condition per state and no policy of its own.

### R3. The spec's "no other line of any existing test changes" cannot hold honestly

Decision 8 asks for exactly one removed clause in `web/src/jobs/JobsPage.test.tsx`. But the test is **named** `does not render the backend-blocked view-switch, My-jobs, or search controls` and carries a comment reading `Omitted per spec: Lanes/Timeline view switch, My jobs pill, free-text search.` Once the switch ships, the name and the comment both assert the opposite of what the test checks - the dominant defect class in this repo. Task 6 removes the one assertion **and** corrects the name and the comment: three lines, one test, no assertion added or weakened beyond the single `/lanes/i` clause. Every other existing test in that file is untouched.

### R4. `text-transform: uppercase` moves the accessible name in Chromium but not in jsdom

The lane heading is uppercased by CSS (matching the hi-fi). jsdom applies no CSS, so testing-library computes the name as `Queued`; Chromium reflects `text-transform` in the accessibility tree and computes `QUEUED`. A Playwright `getByRole('region', { name: 'Queued' })` would therefore pass locally in vitest and fail in the browser for a reason that looks like a missing element. **Every role-name query for a lane in this plan - vitest and Playwright alike - uses a case-insensitive regex**, and `surfaces.ts` carries a one-line comment saying why.

**Checked and found nothing:** no existing lanes/kanban component under `web/src`; no `useSearchParams`/`createSearchParams`/`location.search` anywhere under `web/src` (so Decision 3's premise holds); no consumer of `surfaces()` other than `layout.spec.ts`; `useJobs`'s other call sites do not exist (it is called once, at `JobsPage.tsx:30`); and `web/src/jobs/api.test.ts` is **not** in the frozen-file list, so appending a test to it is allowed.

---

## Gate: the frozen files

These five files must have a **zero-line diff** against `origin/main` when the PR is assembled. Task 10 checks it mechanically.

- `web/src/jobs/JobsTable.test.tsx`
- `web/src/jobs/JobsPage.pager.test.tsx`
- `web/src/jobs/useJobs.test.tsx`
- `web/src/jobs/status.test.ts`
- `web/src/jobs/queryKeyDecoupling.test.tsx`

`web/src/jobs/JobsPage.test.tsx` changes in **exactly one test** (Task 6, three lines, per R3). All other new tests go in new sibling files, following the `JobsPage.pager.test.tsx` precedent.

---

## File structure

**Create (8 files):**

| Path | Responsibility |
| --- | --- |
| `web/src/jobs/lanes.ts` | Pure data: `LANE_LIMIT`, `LANE_ORDER`, `LANE_LABELS`, `LANE_CHIP_KEY`. No React. |
| `web/src/jobs/lanes.test.ts` | Vocabulary and cap guards (AC-1, AC-2). |
| `web/src/jobs/useJobLanes.ts` | `LaneState` + the `useQueries` hook. |
| `web/src/jobs/useJobLanes.test.tsx` | Request shape, key decoupling, disabled gate, per-lane 500 isolation (AC-3, AC-4). |
| `web/src/jobs/JobsLanes.tsx` | The view: scroll container, five `JobLane`s, cards, overflow control. |
| `web/src/jobs/JobsLanes.test.tsx` | Regions, headings, links, empty, error+Retry, overflow, tab order, scroller classes (AC-5, AC-6, AC-7, AC-12). |
| `web/src/jobs/JobsPage.lanes.test.tsx` | Page-level: switch persistence, disabled table query, absent table furniture, overflow routing, the Cancelled chip, the chip-key guard, end-to-end lane failure (AC-8, AC-9, AC-10, AC-13, AC-15). |
| `web/src/jobs/useJobs.enabled.test.tsx` | The `enabled` parameter with itself as the subject. |

Five of those eight are test files, which is the number `npm test`'s collected file count must rise by (Task 10 Step 3).

**Modify (7 files):**

| Path | Change |
| --- | --- |
| `web/src/jobs/api.ts` | `JOB_STATUSES` tuple, `JobStatus` derived from it, `listJobsByStatus`. |
| `web/src/jobs/api.test.ts` | Two appended tests. |
| `web/src/jobs/useJobs.ts` | Fifth optional parameter `enabled = true`. |
| `web/src/jobs/JobsPage.tsx` | `view` state + `loadView`/`chooseView`, the view switch, `pageHeader` extraction, the lanes branch, the sixth `FILTERS` entry, `export` on `FILTERS`, the `useJobs` argument, the stale omission comment. **All above `<JobsTable>`.** |
| `web/src/jobs/JobsPage.test.tsx` | One test: one assertion removed, name and comment corrected (R3). |
| `web/e2e/surfaces.ts` | One `jobs-lanes` surface entry; one stale count reworded. |
| `web/e2e/README.md` | Two stale surface counts. |

---

## Conventions that apply to every task

- **Working directory.** Everything is under `D:/dev/relay/.claude/worktrees/web-f-jobs-lanes`. Never `cd D:/dev/relay`. vitest commands run from `D:/dev/relay/.claude/worktrees/web-f-jobs-lanes/web`; git commands run from the worktree root.
- **Commit with an explicit pathspec. Never `git add -A`, never `git add .`.** `web/dist` is tracked and stale; it must never be staged. After any `npm run build`, run `git checkout -- web/dist/` before committing.
- **After every programmatic file write**, before committing: `git diff --stat` (is the line delta the size you intended?) and `git ls-files --eol <paths>` (every entry must read `i/lf`).
- **No non-ASCII bytes in any new file.** The card's progress text is `3/4 tasks, 75%` with a comma - deliberately not the middle dot the existing footer uses - so no new file in this slice contains a byte above 0x7F and the UTF-8 hazard has no subject here. Do not "restore" a middle dot.
- **Comments.** State a hazard or constraint the code cannot show, in a few lines; cite a test that pins the claim where one exists. No dates, no change history, no counts of things elsewhere, no uniqueness claims about other code. Everything else goes in the commit message.
- **Tailwind v4 scans every file under `web/`, tests and comments included.** Class strings must be literals in the component that means them. A class-shaped literal in a test file emits CSS too - the two places this plan does that are called out where they occur, and both assert about a class the component genuinely owns.
- **Expected-failure discipline.** When a step says "expected: FAIL", read the failure message. If it fails for a different reason than the one stated, stop: the test is not pinning what it claims.

---

## Task 1: `JOB_STATUSES` and `listJobsByStatus`

**Files:**
- Modify: `web/src/jobs/api.ts:3` (the `JobStatus` union) and after line 79 (`listJobsBySchedule`)
- Test: `web/src/jobs/api.test.ts` (append; import list at lines 6-18)

- [ ] **Step 1: Write the failing test**

Append to `web/src/jobs/api.test.ts`:

```ts
test('listJobsByStatus sends status and limit and NEVER sends sort or cursor', async () => {
  let captured: URLSearchParams | undefined
  server.use(
    http.get('/v1/jobs', ({ request }) => {
      captured = new URL(request.url).searchParams
      return HttpResponse.json(emptyPage)
    }),
  )
  await listJobsByStatus('failed', 10)
  // Presence controls FIRST: without them the absence assertions below pass
  // against a function that sends no parameters at all.
  expect(captured?.get('status')).toBe('failed')
  expect(captured?.get('limit')).toBe('10')
  // ?sort= combined with ANY filter is a hard 400, 'sort not supported on filtered
  // list variant' (internal/api/jobs.go), and listJobs sets sort by default on its
  // unfiltered branch, so a copy-paste reintroduces it. A cursor is meaningless on a
  // capped lane and would page it away from the newest rows.
  expect(captured?.has('sort')).toBe(false)
  expect(captured?.has('cursor')).toBe(false)
})

test('JOB_STATUSES is the jobs.status vocabulary and JobStatus derives from it', () => {
  expect([...JOB_STATUSES]).toEqual(['pending', 'running', 'done', 'failed', 'cancelled'])
})
```

And extend the existing import block at the top of the file so it reads:

```ts
import {
  BACKFILL_PAGE_SIZE,
  JOB_STATUSES,
  cancelJob,
  createJob,
  getJobStats,
  getTaskLogs,
  listJobs,
  listJobsBySchedule,
  listJobsByStatus,
  retryJob,
  streamTaskLog,
  type JobsPage,
  type TaskLogEvent,
} from './api'
```

- [ ] **Step 2: Run the test to verify it fails**

```
cd D:/dev/relay/.claude/worktrees/web-f-jobs-lanes/web
npx vitest run src/jobs/api.test.ts
```

Expected: FAIL. The file does not transform - `"listJobsByStatus" is not exported by "src/jobs/api.ts"` (and the same for `JOB_STATUSES`).

- [ ] **Step 3: Write the minimal implementation**

In `web/src/jobs/api.ts`, replace line 3:

```ts
export type JobStatus = 'pending' | 'running' | 'done' | 'failed' | 'cancelled'
```

with:

```ts
// The jobs.status vocabulary, in lifecycle order. JobStatus derives from the tuple
// so the type and the runtime list cannot drift from each other. Nothing compares
// this to the database's own constraint: TestJobsStatusVocabularyIsExactly pins the
// Go side and cannot see this file, so a sixth status added server-side would be
// dropped from the lanes view silently.
export const JOB_STATUSES = ['pending', 'running', 'done', 'failed', 'cancelled'] as const

export type JobStatus = (typeof JOB_STATUSES)[number]
```

and add, immediately after `listJobsBySchedule` (after line 79):

```ts
// One lane of the jobs board: the newest `limit` jobs in one status, plus that
// status's all-time total from the same response, so a lane header and its overflow
// control are computed from one number and cannot disagree.
//
// Sends NO sort and NO cursor. Deliberately not expressed through listJobs, for the
// same reason listJobsBySchedule is not: listJobs sets sort by default on its
// unfiltered branch, and sort combined with a filter is a hard 400. Do not unify
// them. A limit outside [1, 200] is REJECTED with a 400, not clamped
// (internal/api/pagination.go, parsePage), so a caller's cap is a hard ceiling.
export function listJobsByStatus(status: JobStatus, limit: number): Promise<JobsPage> {
  const q = new URLSearchParams({ status, limit: String(limit) })
  return apiFetch<JobsPage>(`/jobs?${q}`)
}
```

- [ ] **Step 4: Run the test to verify it passes**

```
cd D:/dev/relay/.claude/worktrees/web-f-jobs-lanes/web
npx vitest run src/jobs/api.test.ts
```

Expected: PASS, all tests in the file.

- [ ] **Step 5: Type-check, because this changed a widely-imported type**

```
cd D:/dev/relay/.claude/worktrees/web-f-jobs-lanes/web
npx tsc -b
```

Expected: no output. `JobStatus` is structurally identical to the union it replaced, so no consumer changes.

- [ ] **Step 6: Commit**

```bash
cd D:/dev/relay/.claude/worktrees/web-f-jobs-lanes
git add web/src/jobs/api.ts web/src/jobs/api.test.ts
git commit -m "feat: derive JobStatus from a JOB_STATUSES tuple and add listJobsByStatus"
```

---

## Task 2: the lane vocabulary module

**Files:**
- Create: `web/src/jobs/lanes.ts`
- Test: `web/src/jobs/lanes.test.ts`

- [ ] **Step 1: Write the failing test**

Create `web/src/jobs/lanes.test.ts`:

```ts
import { expect, test } from 'vitest'
import { JOB_STATUSES } from './api'
import { LANE_CHIP_KEY, LANE_LABELS, LANE_LIMIT, LANE_ORDER } from './lanes'

test('lane order is exactly the job status vocabulary, in lifecycle order', () => {
  expect([...LANE_ORDER]).toEqual(['pending', 'running', 'done', 'failed', 'cancelled'])
  // Set equality with the vocabulary, checked separately from the order above: a
  // status with no lane is invisible in this view, and a lane for a status the
  // server cannot return is a permanently empty column.
  expect([...LANE_ORDER].sort()).toEqual([...JOB_STATUSES].sort())
})

test('every job status has a lane label and a chip key', () => {
  for (const status of JOB_STATUSES) {
    expect(LANE_LABELS[status]).toBeTruthy()
    expect(LANE_CHIP_KEY[status]).toBeTruthy()
  }
  // The pending lane is labelled Queued, matching the table's own chip. One page
  // must not call one state two things.
  expect(LANE_LABELS.pending).toBe('Queued')
})

test('the per-lane cap is inside the range the server accepts', () => {
  // parsePage REJECTS an out-of-range limit with a 400; it does not clamp.
  expect(LANE_LIMIT).toBeGreaterThanOrEqual(1)
  expect(LANE_LIMIT).toBeLessThanOrEqual(200)
})
```

- [ ] **Step 2: Run the test to verify it fails**

```
cd D:/dev/relay/.claude/worktrees/web-f-jobs-lanes/web
npx vitest run src/jobs/lanes.test.ts
```

Expected: FAIL - `Failed to resolve import "./lanes"`.

- [ ] **Step 3: Write the minimal implementation**

Create `web/src/jobs/lanes.ts`:

```ts
import type { JobStatus } from './api'

// Cards fetched per lane. Fixed rather than user-adjustable in this slice: five
// lanes at this cap is one table page of rows per poll.
export const LANE_LIMIT = 10

// Left-to-right lane order. A presentation choice, so it is listed rather than
// derived - lanes.test.ts pins it against JOB_STATUSES as a set.
export const LANE_ORDER: readonly JobStatus[] = ['pending', 'running', 'done', 'failed', 'cancelled']

// Record<JobStatus, string>, not a partial map: adding a status to JOB_STATUSES
// without a label here is a tsc error rather than a lane that silently disappears.
export const LANE_LABELS: Record<JobStatus, string> = {
  pending: 'Queued',
  running: 'Running',
  done: 'Done',
  failed: 'Failed',
  cancelled: 'Cancelled',
}

// The table chip a lane's overflow control selects. Routing a key that is not in
// FILTERS would make the status lookup fall back to an empty status and show EVERY
// job while the chip row looks filtered - a wrong answer, not a missing one. The
// JobsPage.lanes.test.tsx guard 'every lane chip key names a real table filter for
// its own status' is what reddens for that.
export const LANE_CHIP_KEY: Record<JobStatus, string> = {
  pending: 'queued',
  running: 'running',
  done: 'done',
  failed: 'failed',
  cancelled: 'cancelled',
}
```

- [ ] **Step 4: Run the test to verify it passes**

```
cd D:/dev/relay/.claude/worktrees/web-f-jobs-lanes/web
npx vitest run src/jobs/lanes.test.ts
```

Expected: PASS, 3 tests.

- [ ] **Step 5: Prove the label map's type guard is real**

Temporarily add `'archived'` to the `JOB_STATUSES` tuple in `web/src/jobs/api.ts`, then run:

```
cd D:/dev/relay/.claude/worktrees/web-f-jobs-lanes/web
npx tsc -b
```

Expected: FAIL - `Property 'archived' is missing in type ... but required in type 'Record<JobStatus, string>'`, reported for both `LANE_LABELS` and `LANE_CHIP_KEY`. That is AC-2's compile-time half. **Now revert the tuple by hand** (edit the line back; never `git checkout --` to undo a deliberate mutation, which would also discard uncommitted work in the same file), and re-run `npx tsc -b` expecting no output.

- [ ] **Step 6: Commit**

```bash
cd D:/dev/relay/.claude/worktrees/web-f-jobs-lanes
git add web/src/jobs/lanes.ts web/src/jobs/lanes.test.ts
git commit -m "feat: add the jobs lane vocabulary, cap and chip-key map"
```

---

## Task 3: `useJobLanes`

**Files:**
- Create: `web/src/jobs/useJobLanes.ts`
- Test: `web/src/jobs/useJobLanes.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/jobs/useJobLanes.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { useJobLanes } from './useJobLanes'

// Wire bodies are hand-written, never marshalled through JobsPage or api types: a
// fixture built from the production interface agrees with the decoder by
// construction and cannot detect drift in either direction.
function jobRow(id: string, name: string, status: string) {
  return {
    id,
    name,
    priority: 'normal',
    status,
    submitted_by_email: 'a@x.dev',
    labels: null,
    created_at: '2026-06-05T10:00:00Z',
    updated_at: '2026-06-05T10:00:00Z',
    total_tasks: 4,
    done_tasks: 2,
  }
}

function makeWrapper(client: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={client}>{children}</QueryClientProvider>
  }
}

function newClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

test('each lane requests its own status at the cap and never sends sort or cursor', async () => {
  const seen: URLSearchParams[] = []
  server.use(
    http.get('/v1/jobs', ({ request }) => {
      seen.push(new URL(request.url).searchParams)
      return HttpResponse.json({ items: [], next_cursor: '', total: 0 })
    }),
  )
  renderHook(() => useJobLanes(true, 10, 100_000), { wrapper: makeWrapper(newClient()) })

  await waitFor(() => expect(seen).toHaveLength(5))
  expect(seen.map((p) => p.get('status')).sort()).toEqual([
    'cancelled',
    'done',
    'failed',
    'pending',
    'running',
  ])
  for (const p of seen) {
    expect(p.get('limit')).toBe('10')
    expect(p.has('sort')).toBe(false)
    expect(p.has('cursor')).toBe(false)
  }
})

test('the lanes issue no request while disabled', async () => {
  let calls = 0
  server.use(
    http.get('/v1/jobs', () => {
      calls++
      return HttpResponse.json({ items: [], next_cursor: '', total: 0 })
    }),
  )
  const { result } = renderHook(() => useJobLanes(false, 10, 20), {
    wrapper: makeWrapper(newClient()),
  })
  // Two refetch intervals of real time. Asserting an absence needs a bounded wait,
  // and the interval is the thing that would produce a request if the gate leaked.
  await new Promise((r) => setTimeout(r, 120))
  expect(calls).toBe(0)
  // The lane structure still exists so the view can render its five columns.
  expect(result.current).toHaveLength(5)
  expect(result.current[0].status).toBe('pending')
  expect(result.current[0].items).toEqual([])
})

test('invalidating the jobs list does not refetch the lanes', async () => {
  let calls = 0
  server.use(
    http.get('/v1/jobs', () => {
      calls++
      return HttpResponse.json({ items: [], next_cursor: '', total: 0 })
    }),
  )
  const client = newClient()
  renderHook(() => useJobLanes(true, 10, 100_000), { wrapper: makeWrapper(client) })
  await waitFor(() => expect(calls).toBe(5))

  // invalidateQueries awaits the refetch of every ACTIVE query it matches, so if
  // the lane keys sat under the 'jobs' prefix the counter would already have moved
  // by the time this resolves.
  await act(async () => {
    await client.invalidateQueries({ queryKey: ['jobs'] })
  })
  expect(calls).toBe(5)

  // The control: the same call against the lanes' own prefix MUST move it. Without
  // this, the assertion above passes equally on a hook that mounted nothing.
  await act(async () => {
    await client.invalidateQueries({ queryKey: ['job-lanes'] })
  })
  await waitFor(() => expect(calls).toBe(10))
})

test('a 500 on one lane leaves the other four with their rows', async () => {
  server.use(
    http.get('/v1/jobs', ({ request }) => {
      const status = new URL(request.url).searchParams.get('status') ?? ''
      if (status === 'failed') {
        return HttpResponse.json({ error: 'list jobs failed' }, { status: 500 })
      }
      return HttpResponse.json({
        items: [jobRow(`ID-${status}`, `job-${status}`, status)],
        next_cursor: '',
        total: 3,
      })
    }),
  )
  const { result } = renderHook(() => useJobLanes(true, 10, 100_000), {
    wrapper: makeWrapper(newClient()),
  })

  await waitFor(() => {
    expect(result.current.find((l) => l.status === 'failed')?.error).toBeTruthy()
    expect(result.current.filter((l) => l.status !== 'failed').every((l) => l.items.length === 1)).toBe(
      true,
    )
  })
  for (const lane of result.current.filter((l) => l.status !== 'failed')) {
    expect(lane.total).toBe(3)
    expect(lane.error).toBeNull()
    expect(lane.items[0].name).toBe(`job-${lane.status}`)
  }
})
```

- [ ] **Step 2: Run the test to verify it fails**

```
cd D:/dev/relay/.claude/worktrees/web-f-jobs-lanes/web
npx vitest run src/jobs/useJobLanes.test.tsx
```

Expected: FAIL - `Failed to resolve import "./useJobLanes"`.

- [ ] **Step 3: Write the minimal implementation**

Create `web/src/jobs/useJobLanes.ts`:

```ts
import { keepPreviousData, useQueries } from '@tanstack/react-query'
import { listJobsByStatus, type Job, type JobStatus } from './api'
import { LANE_LIMIT, LANE_ORDER } from './lanes'

// One lane's whole render input. The view gets this instead of a UseQueryResult so
// it cannot mis-zip a results array against LANE_ORDER, and so its tests can state
// a lane's state directly instead of driving a query to reach it.
export interface LaneState {
  status: JobStatus
  items: Job[]
  // The status's all-time total, from the same response as the items.
  total: number
  isLoading: boolean
  isFetching: boolean
  error: Error | null
  refetch: () => void
}

// One capped list per status, polled together on the jobs cadence. `enabled` gates
// all five at once, so leaving the lanes view stops the polling rather than leaving
// five queries running behind the table.
//
// Keys are ['job-lanes', ...] and deliberately NOT under the 'jobs' prefix: a broad
// invalidateQueries(['jobs']) must not fan out into five more requests. Same
// argument as useJobStats' ['job-stats'] key; the guard here is
// useJobLanes.test.tsx's 'invalidating the jobs list does not refetch the lanes'.
export function useJobLanes(enabled: boolean, limit = LANE_LIMIT, intervalMs = 3000): LaneState[] {
  const results = useQueries({
    queries: LANE_ORDER.map((status) => ({
      queryKey: ['job-lanes', status, limit],
      queryFn: () => listJobsByStatus(status, limit),
      enabled,
      refetchInterval: intervalMs,
      placeholderData: keepPreviousData,
    })),
  })

  return LANE_ORDER.map((status, i) => {
    const r = results[i]
    return {
      status,
      items: r.data?.items ?? [],
      total: r.data?.total ?? 0,
      isLoading: r.isLoading && !r.data,
      isFetching: r.isFetching,
      // A failed poll that still has rows keeps showing them: the error surfaces
      // only when there is nothing else to render, matching the table view's own
      // `error && !data` rule.
      error: r.data ? null : ((r.error as Error | null) ?? null),
      refetch: () => {
        void r.refetch()
      },
    }
  })
}
```

- [ ] **Step 4: Run the test to verify it passes**

```
cd D:/dev/relay/.claude/worktrees/web-f-jobs-lanes/web
npx vitest run src/jobs/useJobLanes.test.tsx
npx tsc -b
```

Expected: PASS, 4 tests; `tsc -b` silent.

- [ ] **Step 5: Prove the disabled gate is load-bearing**

Delete the `enabled,` line from the query options object, re-run `npx vitest run src/jobs/useJobLanes.test.tsx`, and expect **`the lanes issue no request while disabled` to FAIL** with `expected 0 to be 5` (or similar). Restore the line by hand (never `git checkout --`: the file is uncommitted) and re-run, expecting PASS. This is the guard for the property Task 7 depends on.

- [ ] **Step 6: Commit**

```bash
cd D:/dev/relay/.claude/worktrees/web-f-jobs-lanes
git add web/src/jobs/useJobLanes.ts web/src/jobs/useJobLanes.test.tsx
git commit -m "feat: add useJobLanes, five capped per-status polls outside the jobs key prefix"
```

---

## Task 4: `JobsLanes` - the view

**Files:**
- Create: `web/src/jobs/JobsLanes.tsx`
- Test: `web/src/jobs/JobsLanes.test.tsx`

### The document-overflow argument for the scroller

This is the app's first horizontally-scrolling multi-column layout, and jsdom performs no layout, so nothing in this task can measure a width. The argument that it does not widen the page, to be checked for real in Task 9:

- `<main class="relative z-0 p-5">` (`web/src/shell/HoloShell.tsx:82`) is a block: its width comes from its containing block, never from its content.
- `JobsPage`'s root is `flex flex-col`, also a block, so its width is `<main>`'s content width. The lanes container is a flex **item** in a **column** flex container, so the horizontal axis is the CROSS axis. `min-width: auto`'s automatic-minimum-size rule applies only in the MAIN axis, so the item's used minimum width is 0 and `align-items: stretch` sizes it to the container's width. `min-w-0` is written on it anyway, as belt and braces against a future `flex-row` wrapper.
- Inside it, `overflow-x-auto` makes the container a scroll container; the row inside is `flex` with `shrink-0` lanes of a fixed width, so five lanes are about 1450px and the excess scrolls **inside** the container.
- The tab stop (`tabIndex={0} role="group"`) follows `components/holo/Table.tsx:189-193`: a scroll region with no focusable descendant is an axe `scrollable-region-focusable` violation, and WebKit does not grant implicit scroller focusability. With all five lanes empty there is no focusable descendant at all, which is exactly that case.
- **The honest limit:** a `scrollWidth <= clientWidth` gate cannot distinguish "fits" from "clipped behind a scroller" (`web/e2e/README.md:100-109`). Task 9 pins that lanes do not widen the document. It does not pin that they are readable. The screenshots are the artifact for that and someone has to open them.

- [ ] **Step 1: Write the failing test**

Create `web/src/jobs/JobsLanes.test.tsx`:

```tsx
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import type { Job } from './api'
import { JobsLanes } from './JobsLanes'
import type { LaneState } from './useJobLanes'

// LaneState is this component's OWN input contract, so building it here simulates
// nothing on the wire and is not the vacuous-fixture case. The wire fixtures live
// in useJobLanes.test.tsx and JobsPage.lanes.test.tsx, hand-written there.
function job(id: string, name: string): Job {
  return {
    id,
    name,
    priority: 'normal',
    status: 'running',
    labels: null,
    created_at: '2026-06-05T10:00:00Z',
    updated_at: '2026-06-05T10:00:00Z',
    total_tasks: 4,
    done_tasks: 3,
  }
}

function lane(over: Partial<LaneState> & { status: LaneState['status'] }): LaneState {
  return { items: [], total: 0, isLoading: false, isFetching: false, error: null, refetch: () => {}, ...over }
}

function fiveEmpty(): LaneState[] {
  return [
    lane({ status: 'pending' }),
    lane({ status: 'running' }),
    lane({ status: 'done' }),
    lane({ status: 'failed' }),
    lane({ status: 'cancelled' }),
  ]
}

function renderLanes(lanes: LaneState[], onShowAll: (s: Job['status']) => void = () => {}) {
  return render(
    <MemoryRouter>
      <JobsLanes lanes={lanes} onShowAll={onShowAll} />
    </MemoryRouter>,
  )
}

// Case-insensitive throughout: the heading is uppercased by CSS, which jsdom does
// not apply and Chromium reflects in the accessible name.
function region(name: string) {
  return screen.getByRole('region', { name: new RegExp(`^${name}$`, 'i') })
}

test('each lane is a labelled region with a heading, and each card links to its job', () => {
  const lanes = fiveEmpty()
  lanes[0] = lane({ status: 'pending', items: [job('A1', 'ingest frames')], total: 1 })
  lanes[1] = lane({ status: 'running', items: [job('B2', 'shot-042 render')], total: 1 })
  renderLanes(lanes)

  for (const name of ['Queued', 'Running', 'Done', 'Failed', 'Cancelled']) {
    expect(within(region(name)).getByRole('heading', { level: 2 })).toBeInTheDocument()
  }
  const card = within(region('Queued')).getByRole('link', { name: /ingest frames/ })
  expect(card).toHaveAttribute('href', '/jobs/A1')
  // Progress is exposed as text, not carried by the bar's width alone.
  expect(card).toHaveTextContent('3/4 tasks, 75%')
  expect(within(region('Queued')).getByText('1 total')).toBeInTheDocument()
})

test('an empty lane keeps its header and shows no jobs, with no skeleton and no error', () => {
  renderLanes(fiveEmpty())
  const queued = region('Queued')
  expect(within(queued).getByText('0 total')).toBeInTheDocument()
  expect(within(queued).getByText('No jobs')).toBeInTheDocument()
  expect(within(queued).queryByRole('button', { name: /retry/i })).toBeNull()
  expect(within(queued).queryByRole('list')).toBeNull()
})

test('a failing lane renders its own error and Retry while the others keep their rows', async () => {
  const refetch = vi.fn()
  renderLanes([
    lane({ status: 'pending', items: [job('A1', 'ingest frames')], total: 1 }),
    lane({ status: 'running', items: [job('B1', 'shot render')], total: 1 }),
    lane({ status: 'done', items: [job('C1', 'nightly etl')], total: 1 }),
    lane({ status: 'failed', error: new Error('list jobs failed'), refetch }),
    lane({ status: 'cancelled', items: [job('D1', 'aborted bake')], total: 1 }),
  ])

  const failed = region('Failed')
  expect(within(failed).getByText('list jobs failed')).toBeInTheDocument()
  await userEvent.click(within(failed).getByRole('button', { name: /retry/i }))
  expect(refetch).toHaveBeenCalledTimes(1)

  for (const [name, jobName] of [
    ['Queued', 'ingest frames'],
    ['Running', 'shot render'],
    ['Done', 'nightly etl'],
    ['Cancelled', 'aborted bake'],
  ] as const) {
    const r = region(name)
    expect(within(r).getByRole('link', { name: new RegExp(jobName) })).toBeInTheDocument()
    expect(within(r).queryByRole('button', { name: /retry/i })).toBeNull()
  }
})

test('a loading lane shows skeletons, not an empty message', () => {
  const lanes = fiveEmpty()
  lanes[0] = lane({ status: 'pending', isLoading: true })
  renderLanes(lanes)
  expect(within(region('Queued')).queryByText('No jobs')).toBeNull()
  expect(within(region('Running')).getByText('No jobs')).toBeInTheDocument()
})

test('overflow shows total minus shown, and is absent when nothing is hidden', async () => {
  const onShowAll = vi.fn()
  const lanes = fiveEmpty()
  lanes[0] = lane({ status: 'pending', items: [job('A1', 'a'), job('A2', 'b')], total: 490 })
  lanes[1] = lane({ status: 'running', items: [job('B1', 'c')], total: 1 })
  renderLanes(lanes, onShowAll)

  const more = within(region('Queued')).getByRole('button', { name: '+ 488 more' })
  expect(within(region('Running')).queryByRole('button', { name: /more/i })).toBeNull()
  await userEvent.click(more)
  expect(onShowAll).toHaveBeenCalledWith('pending')
})

test('tab order is the scroll container, then each lane in document order', async () => {
  const lanes = fiveEmpty()
  lanes[0] = lane({ status: 'pending', items: [job('A1', 'alpha'), job('A2', 'beta')], total: 9 })
  lanes[1] = lane({ status: 'running', items: [job('B1', 'gamma')], total: 1 })
  renderLanes(lanes)

  const scroller = screen.getByRole('group', { name: /scrolls horizontally/i })
  await userEvent.tab()
  expect(document.activeElement).toBe(scroller)
  await userEvent.tab()
  expect(document.activeElement).toHaveAttribute('href', '/jobs/A1')
  await userEvent.tab()
  expect(document.activeElement).toHaveAttribute('href', '/jobs/A2')
  await userEvent.tab()
  expect(document.activeElement).toHaveTextContent('+ 7 more')
  await userEvent.tab()
  expect(document.activeElement).toHaveAttribute('href', '/jobs/B1')
})

test('the lanes sit in one horizontal scroll container with fixed-width lanes', () => {
  renderLanes(fiveEmpty())
  const scroller = screen.getByRole('group', { name: /scrolls horizontally/i })
  // jsdom does no layout, so this pins the classes, not a width. The real widths
  // are measured by web/e2e/layout.spec.ts's jobs-lanes surface.
  expect(scroller).toHaveClass('overflow-x-auto')
  expect(scroller).toHaveAttribute('tabindex', '0')
  expect(region('Queued')).toHaveClass('w-[280px]', 'shrink-0')
})
```

Note on the last test: `w-[280px]` and `shrink-0` are class-shaped literals, so Tailwind's scanner emits them from this file as well as from the component. Both are real utilities that `JobsLanes.tsx` genuinely applies, and this test asserts the component applies them, so deleting them from the component still reddens here.

- [ ] **Step 2: Run the test to verify it fails**

```
cd D:/dev/relay/.claude/worktrees/web-f-jobs-lanes/web
npx vitest run src/jobs/JobsLanes.test.tsx
```

Expected: FAIL - `Failed to resolve import "./JobsLanes"`.

- [ ] **Step 3: Write the minimal implementation**

Create `web/src/jobs/JobsLanes.tsx`:

```tsx
import { Link } from 'react-router-dom'
import { Button } from '../components/Button'
import { GlassPanel, ProgressBar } from '../components/holo'
import type { JobStatus } from './api'
import { LANE_LABELS } from './lanes'
import { progressPct, statusColor } from './status'
import type { LaneState } from './useJobLanes'

// One horizontal scroll region holding every lane. tabIndex + role="group" follow
// components/holo/Table.tsx: a scroll container with no focusable descendant is an
// axe scrollable-region-focusable violation and WebKit grants no implicit scroller
// focusability. With every lane empty there is no focusable descendant at all,
// which is that case. role="group" is not a landmark; it exists so aria-label has
// a role to attach a name to.
const SCROLLER = 'min-w-0 overflow-x-auto'
const ROW = 'flex gap-3 pb-2'
// Fixed width, no grow and no shrink: the lanes are together wider than any
// viewport this app is tested at, so the row scrolls inside SCROLLER instead of
// widening the document. Same width at 320 as at 1280 - no breakpoint, because
// stacking would nest a vertical scroller inside a vertical scroller per lane and
// would mean the widths measured at 320 are not the widths shipped at 1280.
const LANE = 'flex w-[280px] shrink-0 flex-col gap-2 p-3'
// Capped height so a full lane scrolls within itself rather than stretching the page.
const LANE_BODY = 'flex max-h-[520px] flex-col gap-2 overflow-y-auto'
const CARD = 'block rounded-[8px] border border-border bg-white/[0.04] p-2.5 hover:border-accent/60'

export function JobsLanes({
  lanes,
  onShowAll,
}: {
  lanes: LaneState[]
  onShowAll: (status: JobStatus) => void
}) {
  return (
    <div className={SCROLLER} tabIndex={0} role="group" aria-label="Job lanes, scrolls horizontally">
      <div className={ROW}>
        {lanes.map((lane) => (
          <JobLane key={lane.status} lane={lane} onShowAll={onShowAll} />
        ))}
      </div>
    </div>
  )
}

function JobLane({
  lane,
  onShowAll,
}: {
  lane: LaneState
  onShowAll: (status: JobStatus) => void
}) {
  const headingId = `lane-${lane.status}`
  const c = statusColor(lane.status)
  const hidden = lane.total - lane.items.length
  return (
    <GlassPanel as="section" role="region" aria-labelledby={headingId} className={LANE}>
      {/* The header renders in every state, so a lane never disappears from the
          row and the column count is constant. */}
      <div className="flex items-center justify-between gap-2 px-1">
        <div className="flex items-center gap-2">
          <span className={`h-1.5 w-1.5 rounded-full ${c.dot}`} aria-hidden="true" />
          <h2 id={headingId} className="font-mono text-[10.5px] uppercase tracking-[0.18em] text-fg-mute">
            {LANE_LABELS[lane.status]}
          </h2>
        </div>
        {/* "total" is spelled out because the KPI strip's DONE-24H sits just above
            and is a different, 24-hour-scoped number. This one is all-time. */}
        <span className={`flex-none font-mono text-[11px] ${c.text}`}>
          {lane.total.toLocaleString()} total
        </span>
      </div>

      {lane.isLoading ? (
        <div className={LANE_BODY}>
          {Array.from({ length: 3 }).map((_, i) => (
            <GlassPanel key={i} className="h-14" />
          ))}
        </div>
      ) : lane.error ? (
        <div className="flex flex-col items-start gap-2 px-1 text-[12px] text-err">
          {lane.error.message}
          <Button className="w-auto px-3" onClick={() => lane.refetch()}>
            Retry
          </Button>
        </div>
      ) : lane.items.length === 0 ? (
        <div className="px-1 py-4 text-[12px] text-fg-mute">No jobs</div>
      ) : (
        <ul className={LANE_BODY}>
          {lane.items.map((j) => {
            const pct = progressPct(j.done_tasks, j.total_tasks)
            return (
              <li key={j.id}>
                <Link to={`/jobs/${j.id}`} className={CARD}>
                  <span className="block truncate text-[12px] text-fg">{j.name}</span>
                  <ProgressBar className="my-1.5" value={pct} />
                  <span className="font-mono text-[10px] text-fg-mute">
                    {j.done_tasks ?? 0}/{j.total_tasks ?? 0} tasks, {pct}%
                  </span>
                </Link>
              </li>
            )
          })}
        </ul>
      )}

      {hidden > 0 && (
        <button
          type="button"
          onClick={() => onShowAll(lane.status)}
          className="rounded-[8px] border border-dashed border-border px-3 py-2 font-mono text-[11px] text-fg-mute hover:text-fg"
        >
          + {hidden.toLocaleString()} more
        </button>
      )}
    </GlassPanel>
  )
}
```

- [ ] **Step 4: Run the test to verify it passes**

```
cd D:/dev/relay/.claude/worktrees/web-f-jobs-lanes/web
npx vitest run src/jobs/JobsLanes.test.tsx
npx tsc -b
```

Expected: PASS, 7 tests; `tsc -b` silent.

If the tab-order test's first stop is the `/jobs/A1` link rather than the scroll container, that is a real finding about user-event's focus model, not a licence to delete the assertion: keep the four relative-order assertions, drop only the container one, and say so in the commit message.

- [ ] **Step 5: Commit**

```bash
cd D:/dev/relay/.claude/worktrees/web-f-jobs-lanes
git add web/src/jobs/JobsLanes.tsx web/src/jobs/JobsLanes.test.tsx
git commit -m "feat: add the JobsLanes view with per-lane loading, empty, error and overflow states"
```

---

## Task 5: `useJobs` gains `enabled`

**Files:**
- Modify: `web/src/jobs/useJobs.ts`
- Create: `web/src/jobs/useJobs.enabled.test.tsx`
- **Must not change:** `web/src/jobs/useJobs.test.tsx` (frozen)

- [ ] **Step 1: Write the failing test**

Create `web/src/jobs/useJobs.enabled.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { useJobs } from './useJobs'

// Sibling to useJobs.test.tsx, which is gate-frozen for this slice. The `enabled`
// parameter gets its own test with itself as the subject rather than a passing
// mention inside an existing one.
function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>
}

test('issues no request while disabled, and starts once enabled flips', async () => {
  let calls = 0
  server.use(
    http.get('/v1/jobs', () => {
      calls++
      return HttpResponse.json({ items: [], next_cursor: '', total: 0 })
    }),
  )
  const { rerender } = renderHook(({ on }: { on: boolean }) => useJobs('-created_at', '', '', 20, on), {
    wrapper,
    initialProps: { on: false },
  })

  // Two refetch intervals of real time; the interval is what would produce a
  // request if the gate leaked.
  await new Promise((r) => setTimeout(r, 120))
  expect(calls).toBe(0)

  // The control: without it, the assertion above passes on a harness that could
  // never observe a request at all.
  rerender({ on: true })
  await waitFor(() => expect(calls).toBeGreaterThanOrEqual(1))
})
```

- [ ] **Step 2: Run the test to verify it fails**

```
cd D:/dev/relay/.claude/worktrees/web-f-jobs-lanes/web
npx vitest run src/jobs/useJobs.enabled.test.tsx
```

Expected: FAIL - `Expected 4 arguments, but got 5` at transform time, or a runtime failure with `calls` at 1 while disabled.

- [ ] **Step 3: Write the minimal implementation**

Replace `web/src/jobs/useJobs.ts` in full:

```ts
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { listJobs, type JobSort } from './api'

// Polls one page of jobs. keepPreviousData keeps rows visible while a new
// sort/filter/page loads and between polls, so the table never flashes empty.
// intervalMs defaults to 3000; tests inject a small value. enabled gates the poll
// so a page showing a different view can stop fetching a 50-row page nobody is
// looking at; useJobs.enabled.test.tsx is the guard.
export function useJobs(sort: JobSort, status: string, cursor: string, intervalMs = 3000, enabled = true) {
  return useQuery({
    queryKey: ['jobs', sort, status, cursor],
    queryFn: () => listJobs(sort, status, cursor),
    enabled,
    refetchInterval: intervalMs,
    placeholderData: keepPreviousData,
  })
}
```

- [ ] **Step 4: Run both the new test and the frozen ones**

```
cd D:/dev/relay/.claude/worktrees/web-f-jobs-lanes/web
npx vitest run src/jobs/useJobs.enabled.test.tsx src/jobs/useJobs.test.tsx src/jobs/queryKeyDecoupling.test.tsx
```

Expected: PASS, all three files. The default `enabled = true` is what keeps the two frozen files untouched.

- [ ] **Step 5: Confirm the frozen files really are untouched**

```bash
cd D:/dev/relay/.claude/worktrees/web-f-jobs-lanes
git status --porcelain web/src/jobs/useJobs.test.tsx web/src/jobs/queryKeyDecoupling.test.tsx
```

Expected: **no output.** `git diff` alone is not authoritative on this CRLF repo - `core.autocrlf=true` normalizes line-ending churn out of `git diff` while `git status` still reports the file as modified.

- [ ] **Step 6: Commit**

```bash
cd D:/dev/relay/.claude/worktrees/web-f-jobs-lanes
git add web/src/jobs/useJobs.ts web/src/jobs/useJobs.enabled.test.tsx
git commit -m "feat: gate the jobs list poll on an optional enabled parameter"
```

---

## Task 6: the Table / Lanes switch and its persistence

**Files:**
- Modify: `web/src/jobs/JobsPage.tsx` (lines 1-30, 74-97 - all above `<JobsTable>`)
- Modify: `web/src/jobs/JobsPage.test.tsx:46-55` (one test: one assertion removed, name and comment corrected)
- Create: `web/src/jobs/JobsPage.lanes.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/jobs/JobsPage.lanes.test.tsx`:

```tsx
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { afterEach, beforeEach, expect, test } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import { server } from '../test/setup-helpers'
import { renderWithQuery } from '../test/renderWithQuery'
import { JobsPage } from './JobsPage'

// Sibling to JobsPage.test.tsx, which is gate-frozen apart from one narrowed
// assertion, following the JobsPage.pager.test.tsx precedent.

function renderPage() {
  return renderWithQuery(
    <MemoryRouter>
      <JobsPage />
    </MemoryRouter>,
  )
}

const stats = { running: 3, queued: 1, done_24h: 487, failed_24h: 12 }

// Hand-written wire bodies, never marshalled through the api types.
function jobRow(id: string, name: string, status: string) {
  return {
    id,
    name,
    priority: 'normal',
    status,
    submitted_by_email: 'a@x.dev',
    labels: null,
    created_at: '2026-06-05T10:00:00Z',
    updated_at: '2026-06-05T10:00:00Z',
    total_tasks: 4,
    done_tasks: 2,
  }
}

let seen: URLSearchParams[] = []

// Serves both views from one handler: lane requests carry status + limit=10, the
// table's carry limit=50. `seen` is what every request assertion below reads.
function jobsHandler(opts: { failStatus?: string } = {}) {
  return http.get('/v1/jobs', ({ request }) => {
    const p = new URL(request.url).searchParams
    seen.push(p)
    const status = p.get('status')
    if (status === null) return HttpResponse.json({ items: [], next_cursor: '', total: 0 })
    if (status === opts.failStatus) {
      return HttpResponse.json({ error: 'list jobs failed' }, { status: 500 })
    }
    return HttpResponse.json({
      items: [jobRow(`ID-${status}`, `job-${status}`, status)],
      next_cursor: '',
      total: 3,
    })
  })
}

beforeEach(() => {
  seen = []
  server.use(http.get('/v1/jobs/stats', () => HttpResponse.json(stats)), jobsHandler())
})

afterEach(() => localStorage.clear())

test('the view switch persists the choice to localStorage and a remount restores it', async () => {
  const first = renderPage()
  await screen.findByRole('button', { name: 'Lanes' })
  expect(screen.getByRole('button', { name: 'Table' })).toHaveAttribute('aria-pressed', 'true')
  expect(screen.getByRole('button', { name: 'Lanes' })).toHaveAttribute('aria-pressed', 'false')

  await userEvent.click(screen.getByRole('button', { name: 'Lanes' }))
  expect(localStorage.getItem('relay.jobs.view')).toBe('lanes')
  expect(screen.getByRole('button', { name: 'Lanes' })).toHaveAttribute('aria-pressed', 'true')

  first.unmount()
  renderPage()
  expect(await screen.findByRole('button', { name: 'Lanes' })).toHaveAttribute('aria-pressed', 'true')
})

test('a stored value that is not the literal lanes falls back to the table view', async () => {
  localStorage.setItem('relay.jobs.view', 'timeline')
  renderPage()
  expect(await screen.findByRole('button', { name: 'Table' })).toHaveAttribute('aria-pressed', 'true')
})
```

- [ ] **Step 2: Run the test to verify it fails**

```
cd D:/dev/relay/.claude/worktrees/web-f-jobs-lanes/web
npx vitest run src/jobs/JobsPage.lanes.test.tsx
```

Expected: FAIL, both tests - `Unable to find an accessible element with the role "button" and name "Lanes"`.

- [ ] **Step 3: Write the minimal implementation**

Four edits in `web/src/jobs/JobsPage.tsx`, **all above the `<JobsTable>` element**.

**3a.** Export `FILTERS` (lines 13-19). The sixth chip arrives in Task 8; only the `export` keyword changes here:

```tsx
export const FILTERS: { key: string; label: string; status: string }[] = [
  { key: 'all', label: 'All', status: '' },
  { key: 'running', label: 'Running', status: 'running' },
  { key: 'queued', label: 'Queued', status: 'pending' },
  { key: 'done', label: 'Done', status: 'done' },
  { key: 'failed', label: 'Failed', status: 'failed' },
]
```

**3b.** After the `DEFAULT_SORT` line (line 21), add:

```tsx
type View = 'table' | 'lanes'

const VIEW_KEY = 'relay.jobs.view'

// Anything but the literal 'lanes' means the table, so a missing key, a value
// written by a future version, and a storage read that throws all land on the
// shipped default rather than on a blank page.
function loadView(): View {
  try {
    return localStorage.getItem(VIEW_KEY) === 'lanes' ? 'lanes' : 'table'
  } catch {
    return 'table'
  }
}
```

**3c.** Inside `JobsPage`, add the state after line 25 (`const [filter, setFilter] = useState('all')`), and the setter after `pickSort`:

```tsx
  const [view, setView] = useState<View>(loadView)
```

```tsx
  function chooseView(v: View) {
    setView(v)
    try {
      localStorage.setItem(VIEW_KEY, v)
    } catch {
      // A storage failure must not take the click with it: the view still changes
      // for this session, it just does not survive a reload.
    }
  }
```

**3d.** Move the header block (lines 74-97 of the return) into a `pageHeader` const declared immediately **before** the `if (isLoading && !data)` early return, so the lanes branch in Task 7 can render it too, and add the view switch to its right-hand cluster.

**Do this by copying lines 75-97 out of the file rather than retyping them.** Four of those lines contain non-ASCII characters (the KPI separator and the status bullet), and retyping a character that is not on your keyboard is how a Latin-1 byte gets written where UTF-8 is required. Copy, then insert only the new `<div className="flex rounded-full border border-border p-0.5">` block between the live-indicator span and the `+ New job` link:

```tsx
  const pageHeader = (
    /* lines 75-97 of HEAD, copied verbatim, with the block below inserted
       immediately after the closing tag of the live-indicator <span> */
        <div className="flex rounded-full border border-border p-0.5">
          {(['table', 'lanes'] as View[]).map((v) => (
            <button
              key={v}
              type="button"
              aria-pressed={view === v}
              onClick={() => chooseView(v)}
              className={`rounded-full px-3 py-1 text-[12px] ${view === v ? 'bg-accent text-bg' : 'text-fg-mute'}`}
            >
              {v === 'table' ? 'Table' : 'Lanes'}
            </button>
          ))}
        </div>
  )
```

Then, in the main `return`, the 24 lines that used to be there become exactly:

```tsx
      {pageHeader}
```

After writing the file, verify the non-ASCII bytes survived the move: the KPI separator and the bullet must still be the same bytes they are on `origin/main`. `git diff origin/main -- web/src/jobs/JobsPage.tsx` must show those four lines as **moved, not modified** - if any of them appears as a `-`/`+` pair with visually identical text, an encoding change happened and must be undone.

- [ ] **Step 4: Run the new test - and watch the frozen file go RED first**

```
cd D:/dev/relay/.claude/worktrees/web-f-jobs-lanes/web
npx vitest run src/jobs/JobsPage.lanes.test.tsx src/jobs/JobsPage.test.tsx
```

Expected: `JobsPage.lanes.test.tsx` PASSES (2 tests). `JobsPage.test.tsx` FAILS on exactly one test, `does not render the backend-blocked view-switch, My-jobs, or search controls`, at the `queryByRole('button', { name: /lanes/i })` assertion. **That red is the point:** it is the measurement that the assertion really did pin this feature's absence. Do not narrow it before seeing it.

- [ ] **Step 5: Narrow that one test**

In `web/src/jobs/JobsPage.test.tsx`, replace lines 46-55 with:

```tsx
test('does not render the backend-blocked Timeline view, My-jobs, or search controls', async () => {
  server.use(http.get('/v1/jobs', () => HttpResponse.json(page)))
  renderPage()
  await screen.findByText('film-x render')
  // Omitted per spec: Timeline view, My jobs pill, free-text search. The Lanes
  // view shipped; it is covered by JobsPage.lanes.test.tsx.
  expect(screen.queryByRole('button', { name: /timeline/i })).toBeNull()
  expect(screen.queryByRole('button', { name: /my jobs/i })).toBeNull()
  expect(screen.queryByRole('searchbox')).toBeNull()
})
```

That is one removed assertion plus the name and comment lines it made false. No other line in the file changes.

- [ ] **Step 6: Run both files and the pager sibling**

```
cd D:/dev/relay/.claude/worktrees/web-f-jobs-lanes/web
npx vitest run src/jobs/JobsPage.lanes.test.tsx src/jobs/JobsPage.test.tsx src/jobs/JobsPage.pager.test.tsx src/jobs/JobsTable.test.tsx
npx tsc -b
```

Expected: PASS, all four files; `tsc -b` silent.

- [ ] **Step 7: Check the merge contract**

```bash
cd D:/dev/relay/.claude/worktrees/web-f-jobs-lanes
git fetch origin main
git diff origin/main -- web/src/jobs/JobsPage.tsx | grep -E '^[+-]' \
  | grep -E 'JobsTable|pager\.(next|prev)|canPrev|next 50|SHOWING|CURSOR PAGINATED|rangeText|computePageRange'
git diff origin/main -- web/src/jobs/JobsPage.tsx | grep -E '^[+-]' | grep -E 'FILTERS|view'
```

Expected: the first grep prints **nothing**; the second prints **several lines**. Also check the diffstat is proportionate to the edit (`git diff --stat origin/main -- web/src/jobs/JobsPage.tsx`) and that `git ls-files --eol web/src/jobs/JobsPage.tsx` reads `i/lf`.

- [ ] **Step 8: Commit**

```bash
cd D:/dev/relay/.claude/worktrees/web-f-jobs-lanes
git add web/src/jobs/JobsPage.tsx web/src/jobs/JobsPage.test.tsx web/src/jobs/JobsPage.lanes.test.tsx
git commit -m "feat: add a persisted Table/Lanes view switch to the jobs page"
```

---

## Task 7: the lanes branch

**Files:**
- Modify: `web/src/jobs/JobsPage.tsx` (imports; the `useJobs` call; a `useJobLanes` call; the liveness dot; one early return - all above `<JobsTable>`)
- Modify: `web/src/jobs/JobsPage.lanes.test.tsx` (append)

- [ ] **Step 1: Write the failing tests**

Append to `web/src/jobs/JobsPage.lanes.test.tsx`:

```tsx
test('the lanes view renders five lanes and issues no unfiltered jobs request', async () => {
  localStorage.setItem('relay.jobs.view', 'lanes')
  renderPage()

  const queued = await screen.findByRole('region', { name: /^queued$/i })
  expect(within(queued).getByRole('link', { name: /job-pending/ })).toHaveAttribute(
    'href',
    '/jobs/ID-pending',
  )
  for (const name of ['Running', 'Done', 'Failed', 'Cancelled']) {
    expect(screen.getByRole('region', { name: new RegExp(`^${name}$`, 'i') })).toBeInTheDocument()
  }

  // Five lane requests and nothing else. An unfiltered request is the 50-row
  // enriched page the table view polls; in lanes view nobody is looking at it.
  await waitFor(() => expect(seen).toHaveLength(5))
  expect(seen.every((p) => p.get('status') !== null)).toBe(true)
  expect(seen.every((p) => p.get('limit') === '10')).toBe(true)
})

test('table-view controls are absent in lanes view', async () => {
  localStorage.setItem('relay.jobs.view', 'lanes')
  renderPage()
  await screen.findByRole('region', { name: /^queued$/i })
  expect(screen.queryByLabelText('Sort jobs')).toBeNull()
  expect(screen.queryByRole('button', { name: 'All' })).toBeNull()
  expect(screen.queryByRole('button', { name: /prev/i })).toBeNull()
  expect(screen.queryByRole('button', { name: /next/i })).toBeNull()
  expect(screen.queryByTestId('jobs-table')).toBeNull()
})

test('a 500 on one lane leaves the other four rendering their jobs', async () => {
  localStorage.setItem('relay.jobs.view', 'lanes')
  server.use(jobsHandler({ failStatus: 'failed' }))
  renderPage()

  const failed = await screen.findByRole('region', { name: /^failed$/i })
  expect(await within(failed).findByRole('button', { name: /retry/i })).toBeInTheDocument()
  for (const [name, status] of [
    ['Queued', 'pending'],
    ['Running', 'running'],
    ['Done', 'done'],
    ['Cancelled', 'cancelled'],
  ] as const) {
    const r = screen.getByRole('region', { name: new RegExp(`^${name}$`, 'i') })
    expect(await within(r).findByRole('link', { name: new RegExp(`job-${status}`) })).toBeInTheDocument()
  }
})
```

Extend the file's first import to `import { screen, waitFor, within } from '@testing-library/react'`.

- [ ] **Step 2: Run the tests to verify they fail**

```
cd D:/dev/relay/.claude/worktrees/web-f-jobs-lanes/web
npx vitest run src/jobs/JobsPage.lanes.test.tsx
```

Expected: the three new tests FAIL - `Unable to find an accessible element with the role "region" and name /^queued$/i`. The two Task 6 tests still pass.

- [ ] **Step 3: Write the minimal implementation**

In `web/src/jobs/JobsPage.tsx`:

**3a.** Add to the imports (after the `JobsTable` import line):

```tsx
import { JobsLanes } from './JobsLanes'
import { useJobLanes } from './useJobLanes'
import { LANE_CHIP_KEY } from './lanes'
```

and widen the type import to `import type { JobSort, JobStatus } from './api'`.

**3b.** Change the `useJobs` call (line 30 at HEAD) to gate on the view, and add the lanes hook beneath it:

```tsx
  const { data, error, isLoading, isFetching, isPlaceholderData, refetch } = useJobs(
    sort,
    status,
    pager.cursor,
    undefined,
    view === 'table',
  )
  const { data: stats } = useJobStats()
  // Called unconditionally and gated by `enabled`, so the lanes stop polling the
  // moment the page returns to the table rather than running behind it.
  const lanes = useJobLanes(view === 'lanes')
```

`undefined` is passed for `intervalMs` deliberately: the default lives in `useJobs` and must not be duplicated here.

**3c.** Add `showAll` beside `chooseView`:

```tsx
  function showAll(s: JobStatus) {
    // pickFilter also resets the pager and snaps sort back to the default, which is
    // exactly what a freshly filtered table needs.
    pickFilter(LANE_CHIP_KEY[s])
    chooseView('table')
  }
```

**3d.** The liveness dot must be true in both views. Add above `pageHeader`:

```tsx
  // The table query is disabled in lanes view, so its isFetching would leave the
  // dot permanently dark beside text claiming the page is auto-refreshing.
  const polling = view === 'lanes' ? lanes.some((l) => l.isFetching) : isFetching
```

and in `pageHeader` change the one span from `className={isFetching ? 'text-ok' : 'text-fg-dim'}` to `className={polling ? 'text-ok' : 'text-fg-dim'}`.

**3e.** Add the branch immediately after `pageHeader` and **before** `if (isLoading && !data)`:

```tsx
  // Before the table's loading and error early returns, which belong to the table
  // query: in lanes view that query is disabled, and a lane owns its own loading,
  // empty and error states so one lane's 500 cannot blank the page.
  if (view === 'lanes') {
    return (
      <div className="flex flex-col gap-4">
        {pageHeader}
        <JobsLanes lanes={lanes} onShowAll={showAll} />
      </div>
    )
  }
```

- [ ] **Step 4: Run the tests to verify they pass**

```
cd D:/dev/relay/.claude/worktrees/web-f-jobs-lanes/web
npx vitest run src/jobs/JobsPage.lanes.test.tsx src/jobs/JobsPage.test.tsx src/jobs/JobsPage.pager.test.tsx
npx tsc -b
```

Expected: PASS, all three files (5 tests in the lanes file); `tsc -b` silent.

- [ ] **Step 5: Prove the table query really is disabled**

Change the fifth argument of the `useJobs` call from `view === 'table'` to `true`, re-run `npx vitest run src/jobs/JobsPage.lanes.test.tsx`, and expect **`the lanes view renders five lanes and issues no unfiltered jobs request` to FAIL** at `expect(seen).toHaveLength(5)` (it will be 6). Restore the argument by hand and re-run, expecting PASS.

- [ ] **Step 6: Re-check the merge contract, then commit**

```bash
cd D:/dev/relay/.claude/worktrees/web-f-jobs-lanes
git diff origin/main -- web/src/jobs/JobsPage.tsx | grep -E '^[+-]' \
  | grep -E 'JobsTable|pager\.(next|prev)|canPrev|next 50|SHOWING|CURSOR PAGINATED|rangeText|computePageRange'
git add web/src/jobs/JobsPage.tsx web/src/jobs/JobsPage.lanes.test.tsx
git commit -m "feat: render the jobs lanes view and stop the table poll while it is active"
```

Expected: the grep prints nothing before you commit.

---

## Task 8: the Cancelled chip and the overflow route

**Files:**
- Modify: `web/src/jobs/JobsPage.tsx:13-19` (one `FILTERS` entry) and the stale omission comment at lines 99-107
- Modify: `web/src/jobs/JobsPage.lanes.test.tsx` (append)

- [ ] **Step 1: Write the failing tests**

Append to `web/src/jobs/JobsPage.lanes.test.tsx`:

```tsx
test('every lane chip key names a real table filter for its own status', () => {
  const byKey = new Map(FILTERS.map((f) => [f.key, f.status]))
  for (const status of JOB_STATUSES) {
    // A key that is not in FILTERS makes the status lookup fall back to '' and the
    // table shows EVERY job while the chip row looks filtered - a wrong answer, not
    // a missing one.
    expect(byKey.has(LANE_CHIP_KEY[status])).toBe(true)
    expect(byKey.get(LANE_CHIP_KEY[status])).toBe(status)
  }
})

test('the Cancelled chip requests status=cancelled and sends no sort', async () => {
  renderPage()
  await screen.findByRole('button', { name: 'Cancelled' })
  await userEvent.click(screen.getByRole('button', { name: 'Cancelled' }))
  await waitFor(() => expect(seen.some((p) => p.get('status') === 'cancelled')).toBe(true))
  const req = seen.find((p) => p.get('status') === 'cancelled')
  expect(req?.get('limit')).toBe('50')
  expect(req?.has('sort')).toBe(false)
})

test('overflow switches to the table with that status chip selected', async () => {
  localStorage.setItem('relay.jobs.view', 'lanes')
  renderPage()

  const failed = await screen.findByRole('region', { name: /^failed$/i })
  // total 3, one card shown.
  await userEvent.click(await within(failed).findByRole('button', { name: '+ 2 more' }))

  // The table's own request: limit=50 discriminates it from the lane's limit=10.
  await waitFor(() =>
    expect(seen.some((p) => p.get('status') === 'failed' && p.get('limit') === '50')).toBe(true),
  )
  const req = seen.find((p) => p.get('status') === 'failed' && p.get('limit') === '50')
  // A cursor minted under the previous filter is rejected by the server.
  expect(req?.has('cursor')).toBe(false)
  expect(req?.has('sort')).toBe(false)

  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Failed' })).toHaveAttribute('aria-pressed', 'true'),
  )
  expect(localStorage.getItem('relay.jobs.view')).toBe('table')
})
```

Add to the file's imports:

```tsx
import { JOB_STATUSES } from './api'
import { LANE_CHIP_KEY } from './lanes'
import { FILTERS, JobsPage } from './JobsPage'
```

(the third line replaces the existing `import { JobsPage } from './JobsPage'`).

- [ ] **Step 2: Run the tests to verify they fail**

```
cd D:/dev/relay/.claude/worktrees/web-f-jobs-lanes/web
npx vitest run src/jobs/JobsPage.lanes.test.tsx
```

Expected: FAIL. `every lane chip key names a real table filter for its own status` fails at `expect(byKey.has('cancelled')).toBe(true)`; the other two fail on the missing `Cancelled` button. The five earlier tests still pass.

- [ ] **Step 3: Write the minimal implementation**

**3a.** Add the sixth entry to `FILTERS` in `web/src/jobs/JobsPage.tsx`:

```tsx
  { key: 'cancelled', label: 'Cancelled', status: 'cancelled' },
```

**3b.** Replace the now-false omission comment (lines 99-107 at HEAD) with:

```tsx
      {/*
        The hi-fi HoloJobsList also shows a Timeline view, a "My jobs" pill, and a
        free-text search input. All three are backend-blocked and deliberately
        omitted here (a dead list control reads as broken):
          - Timeline view: docs/backlog/idea-2026-06-05-jobs-timeline-view.md
          - My jobs + search: docs/backlog/idea-2026-06-05-my-jobs-toggle-mine-filter.md
        When those land, the remaining filters re-appear with real backing.
      */}
```

- [ ] **Step 4: Run the tests to verify they pass**

```
cd D:/dev/relay/.claude/worktrees/web-f-jobs-lanes/web
npx vitest run src/jobs/JobsPage.lanes.test.tsx src/jobs/JobsPage.test.tsx src/jobs/JobsPage.pager.test.tsx
npx tsc -b
```

Expected: PASS, all three files (8 tests in the lanes file); `tsc -b` silent.

- [ ] **Step 5: Prove the chip-key guard is load-bearing**

Change `LANE_CHIP_KEY.cancelled` in `web/src/jobs/lanes.ts` to `'canceled'` (one letter), re-run `npx vitest run src/jobs/lanes.test.ts src/jobs/JobsPage.lanes.test.tsx`, and expect `every lane chip key names a real table filter for its own status` to FAIL while `lanes.test.ts` stays green - which is exactly why the guard lives at the page level, where both maps are visible. Restore by hand and re-run, expecting PASS.

- [ ] **Step 6: Re-check the merge contract, then commit**

```bash
cd D:/dev/relay/.claude/worktrees/web-f-jobs-lanes
git diff origin/main -- web/src/jobs/JobsPage.tsx | grep -E '^[+-]' \
  | grep -E 'JobsTable|pager\.(next|prev)|canPrev|next 50|SHOWING|CURSOR PAGINATED|rangeText|computePageRange'
git add web/src/jobs/JobsPage.tsx web/src/jobs/JobsPage.lanes.test.tsx
git commit -m "feat: add a Cancelled status chip and route lane overflow to the filtered table"
```

---

## Task 9: the Playwright surface

**Files:**
- Modify: `web/e2e/surfaces.ts` (one entry after `jobs`; one stale count reworded)
- Modify: `web/e2e/README.md:59,83` (two stale surface counts)

Docker and the `relay-postgres` container are up on this machine. `make` is not on PATH; use the MSYS2 copy with the variable forwarding recorded in `web/e2e/README.md:21-40`.

- [ ] **Step 1: Add the surface**

In `web/e2e/surfaces.ts`, insert immediately after the `jobs` entry (which ends at line 135):

```ts
    {
      // THE SAME PATH as `jobs` above, in the other view. `prepare` sets the same
      // preference key the shipped view switch writes, so no state is fabricated
      // that production cannot produce; addInitScript is required because the SPA
      // reads the key during its first render, before any test code could run.
      //
      // WHAT THIS SURFACE ESTABLISHES: five lanes do not widen the document,
      // <header> or <main>. WHAT IT CANNOT: whether the lanes are readable, or how
      // much of the row sits clipped behind its own scroller - a
      // scrollWidth <= clientWidth gate cannot tell "fits" from "clipped", and this
      // view is deliberately a scroller (see README, and the same limit spelled out
      // on schedules-failing). The screenshots are the artifact for that.
      name: 'jobs-lanes',
      path: () => '/jobs',
      population: 'populated',
      prepare: async (p) => {
        await p.addInitScript(() => window.localStorage.setItem('relay.jobs.view', 'lanes'))
      },
      ready: async (p, seed) => {
        // Scoped to the Queued lane, not the bare link: a seeded job never leaves
        // `pending` (no relay-agent runs in slice 1), so a pass here means the
        // populated lane really rendered, rather than an empty lanes view being
        // measured under a populated name.
        //
        // Case-insensitive name: the lane heading is uppercased by CSS, and
        // Chromium reflects text-transform in the accessible name.
        const lane = p.getByRole('region', { name: /^queued$/i })
        await expect(lane.getByRole('link', { name: seed.jobName })).toBeVisible()
      },
    },
```

- [ ] **Step 2: Correct the prose this change falsifies**

In `web/e2e/surfaces.ts`, the `schedules-failing` comment says `all 54 tests still pass`. Adding a surface adds three tests, so that count is now wrong at a glance. Replace that clause with `the whole suite still passes` - a count of tests elsewhere does not belong in a comment.

In `web/e2e/README.md`:

- line 59: `layout.spec.ts` alone contributes `42 (14 surfaces x 3 widths)` -> `45 (15 surfaces x 3 widths)`
- line 83: `out of the 14 entries it does carry` -> `out of the 15 entries it does carry`

Make these as exact-anchor replacements and check the line delta afterwards: `git diff --stat web/e2e/README.md` must show 2 changed lines, not a rewritten file.

- [ ] **Step 3: Type-check the e2e directory**

```
cd D:/dev/relay/.claude/worktrees/web-f-jobs-lanes/web
npx tsc -b
```

Expected: no output. `web/tsconfig.json` includes `e2e` under strict, so a mistyped `prepare` fails here.

- [ ] **Step 4: Run the browser suite**

From Git Bash, at the worktree root:

```bash
cd /d/dev/relay/.claude/worktrees/web-f-jobs-lanes
docker start relay-postgres
rm -rf web/e2e/.run
/c/msys64/usr/bin/make.exe test-e2e \
  OS="$OS" TEMP="$TEMP" TMP="$TMP" \
  GOPATH="$(go env GOPATH)" GOMODCACHE="$(go env GOMODCACHE)" GOCACHE="$(go env GOCACHE)"
```

Expected: green, with three new tests named `jobs-lanes does not overflow horizontally` (320, 375, 1280) and `widths-jobs-lanes-*` JSON attachments recording real numbers. Deleting `web/e2e/.run` first is deliberate: a stale `seed.json` hides collection-order bugs.

If the run comes back with a wall of "element(s) not found" across every surface, the embedded SPA is the tracked placeholder - re-run `make web-build` first (README lines 52-62); do not start debugging the new surface.

- [ ] **Step 5: Open the three screenshots**

`jobs-lanes-320.png`, `jobs-lanes-375.png` and `jobs-lanes-1280.png` in the Playwright output directory. The gate cannot see clipping behind the scroller; a human looking at 320px is the only check that the first lane is usable there. Record what you saw in the commit message - "opened all three; lane 1 fully visible at 320, lanes 2-5 reachable only by horizontal scroll" or whatever is actually true.

- [ ] **Step 6: Restore `web/dist` and commit**

```bash
cd D:/dev/relay/.claude/worktrees/web-f-jobs-lanes
git status --porcelain web/dist
git checkout -- web/dist/
git add web/e2e/surfaces.ts web/e2e/README.md
git commit -m "test: measure the jobs lanes view at 320, 375 and 1280 in a real browser"
```

`make test-e2e` restores the `web/dist/index.html` placeholder itself, but check and restore anyway: **`web/dist` must never be staged.**

---

## Task 10: whole-suite gate and PR

- [ ] **Step 1: The frozen files are byte-identical**

```bash
cd D:/dev/relay/.claude/worktrees/web-f-jobs-lanes
git diff --stat origin/main -- \
  web/src/jobs/JobsTable.test.tsx web/src/jobs/JobsPage.pager.test.tsx \
  web/src/jobs/useJobs.test.tsx web/src/jobs/status.test.ts \
  web/src/jobs/queryKeyDecoupling.test.tsx
git status --porcelain web/src/jobs/JobsTable.test.tsx web/src/jobs/JobsPage.pager.test.tsx \
  web/src/jobs/useJobs.test.tsx web/src/jobs/status.test.ts web/src/jobs/queryKeyDecoupling.test.tsx
```

Expected: **no output from either.** Both are run because on this repo `git diff` normalizes line-ending churn away while `git status` still reports the file as modified.

And `web/src/jobs/JobsPage.test.tsx` shows exactly one changed test:

```bash
git diff origin/main -- web/src/jobs/JobsPage.test.tsx
```

Expected: one hunk - 1 removed assertion plus the corrected test name and comment. Nothing else.

- [ ] **Step 2: The merge contract, with its control**

```bash
git diff origin/main -- web/src/jobs/JobsPage.tsx | grep -E '^[+-]' \
  | grep -E 'JobsTable|pager\.(next|prev)|canPrev|next 50|SHOWING|CURSOR PAGINATED|rangeText|computePageRange'
git diff origin/main -- web/src/jobs/JobsPage.tsx | grep -E '^[+-]' | grep -E 'FILTERS|view'
grep -rn 'useCursorPager' web/src/jobs/lanes.ts web/src/jobs/useJobLanes.ts web/src/jobs/JobsLanes.tsx \
  web/src/jobs/lanes.test.ts web/src/jobs/useJobLanes.test.tsx web/src/jobs/JobsLanes.test.tsx \
  web/src/jobs/JobsPage.lanes.test.tsx web/src/jobs/useJobs.enabled.test.tsx
```

Expected: first grep silent, second grep prints lines, third grep silent. Any hit on the third means a new file imported `useCursorPager` and the contract is broken.

- [ ] **Step 3: Whole vitest suite**

```
cd D:/dev/relay/.claude/worktrees/web-f-jobs-lanes/web
npm test
```

Expected: every file green, and the collected **file count** exactly 5 higher than `origin/main`'s run (the five test files this slice creates), with no file disappearing.

- [ ] **Step 4: Type-check and production build**

```
cd D:/dev/relay/.claude/worktrees/web-f-jobs-lanes/web
npx tsc -b
npm run build
```

Expected: both clean. `npm run build` writes `web/dist`.

- [ ] **Step 5: Confirm the lane classes reached the bundle**

Tailwind v4 scans source, so a class that only ever appears in a computed string emits nothing:

```bash
cd D:/dev/relay/.claude/worktrees/web-f-jobs-lanes
grep -c 'overflow-x-auto' web/dist/assets/*.css
grep -c '280px' web/dist/assets/*.css
```

Expected: non-zero for both. If `280px` is absent, the lane width is being built as a computed string somewhere and the layout will silently be wrong in production while every jsdom test stays green.

- [ ] **Step 6: Restore `web/dist` and verify the tree**

```bash
cd D:/dev/relay/.claude/worktrees/web-f-jobs-lanes
git checkout -- web/dist/
git status --porcelain
git ls-files --eol web/src/jobs/lanes.ts web/src/jobs/useJobLanes.ts web/src/jobs/JobsLanes.tsx \
  web/src/jobs/JobsPage.tsx web/src/jobs/api.ts web/src/jobs/useJobs.ts web/e2e/surfaces.ts
```

Expected: `git status --porcelain` prints **nothing** (everything committed, `web/dist` restored). Every `ls-files --eol` line reads `i/lf`.

- [ ] **Step 7: Open the PR**

Write the body to the scratchpad and pass `--body-file`; a long inline `--body` heredoc trips the classifier.

```bash
cd D:/dev/relay/.claude/worktrees/web-f-jobs-lanes
git push -u origin HEAD
gh pr create --title "feat: jobs lanes view" --body-file <scratchpad-path>
```

The body must state: which test file covers which AC; that the frozen five have a zero-line diff and that `JobsPage.test.tsx` changed in one test, with the reason; the merge contract with lane B, the exact check that was run and its control; that the e2e suite ran green and the three screenshots were opened, with what was seen; and that `web/dist` is unchanged.

- [ ] **Step 8: Do NOT close the backlog item**

`docs/backlog/idea-2026-06-05-jobs-lanes-swimlanes-view.md` stays open. **Do not run `/backlog close`**, do not edit the item's frontmatter, do not `git mv` anything under `docs/backlog/`. The conductor closes it after the merge, and the close note must record that the per-lane cards stepper (default 10, min 3, max 50) is deliberately deferred.

---

## Spec coverage

| # | Criterion | Where |
| --- | --- | --- |
| AC-1 | Lane set is exactly the five job statuses in lifecycle order | Task 2, `lanes.test.ts` "lane order is exactly the job status vocabulary, in lifecycle order" |
| AC-2 | Every status has a lane label; adding one without a label fails tsc | Task 2, `lanes.test.ts` "every job status has a lane label and a chip key" plus Task 2 Step 5's tsc measurement |
| AC-3 | Each lane requests its own status at the cap, no sort, no cursor | Task 1 (`api.test.ts`) and Task 3 (`useJobLanes.test.tsx`, first test) |
| AC-4 | Lane keys sit outside the `'jobs'` prefix | Task 3, "invalidating the jobs list does not refetch the lanes", with its control |
| AC-5 | One lane failing does not blank the others | Task 3 (data layer, real 500), Task 4 (view), Task 7 (end to end through `JobsPage`) |
| AC-6 | An empty lane keeps its header and shows a no-jobs message | Task 4, "an empty lane keeps its header and shows no jobs" |
| AC-7 | Overflow is total minus shown, absent when nothing is hidden | Task 4, "overflow shows total minus shown, and is absent when nothing is hidden" |
| AC-8 | The switch renders, persists, and a remount restores it | Task 6, both tests |
| AC-9 | No unfiltered `GET /v1/jobs` in lanes view | Task 7, first test, with Step 5's mutation proof |
| AC-10 | Overflow switches to the table with that chip selected | Task 8, "overflow switches to the table with that status chip selected" |
| AC-11 | No document, header or main overflow at 320/375/1280 | Task 9 |
| AC-12 | Each lane is a labelled region with a heading; each card links to its job | Task 4, first test |
| AC-13 | Chip row, sort control and pagination footer absent in lanes view | Task 7, "table-view controls are absent in lanes view" |
| AC-14 | The table view is unchanged | Task 10 Steps 1-3 (frozen files, merge contract, whole suite) |
| AC-15 | The Cancelled chip requests `status=cancelled` and sends no sort | Task 8, plus the chip-key guard that pins the whole mapping |

**Not in the spec's AC list, added here:** the keyboard-order test and the scroller class pin (Task 4), the `useJobs` `enabled` test with itself as the subject (Task 5), the `LANE_CHIP_KEY`-to-`FILTERS` guard (Task 8), and the stale-prose corrections in `JobsPage.tsx`, `web/e2e/surfaces.ts` and `web/e2e/README.md` (Tasks 8 and 9).

## Follow-ups to propose to the conductor (do not file them yourself)

1. Cards-per-lane stepper (default 10, min 3, max 50), noting the server rejects rather than clamps a limit above 200.
2. No lockstep guard between `JOB_STATUSES` and migration `000019`; a sixth server-side status would vanish from this view with every test green.
3. Five `COUNT(*)` per poll per open tab in lanes view; sibling to `bug-2026-06-05-index-jobstatuscounts-full-table-scan`.
4. No way to scope the Done lane to a window; `/v1/jobs` has no `?since=`.
5. The all-empty lanes state is not covered by the browser harness, which is the state the scroll wrapper's `tabIndex` exists for.
