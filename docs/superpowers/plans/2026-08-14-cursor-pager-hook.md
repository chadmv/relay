# useCursorPager extraction - Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extract the cursor-stack pagination block that is duplicated across seven SPA list surfaces into one hook, `web/src/lib/useCursorPager.ts`, and migrate all seven onto it in a single change, with a zero-line diff to every existing test file.

**Architecture:** One new hook file plus one new test file. Seven `.tsx` surfaces lose four `useState` calls and two-to-three local functions each and gain one `const pager = useCursorPager()`. The hook owns `cursor` / `stack` / `startOffset` / `offsets` with exactly the shipped update mechanics (plain setters, no functional updaters, no `useCallback`) and exposes `canPrev` instead of the stack. Nothing else moves: not `toggleSort`, not `statusTone`, not the footer span, not `computePageRange`.

**Tech Stack:** TypeScript 5.7, React 18, TanStack Query v5 (untouched by the hook), Vitest 2.1 + Testing Library 16 (`renderHook` + `act`), jsdom. No new dependency. **Zero Go, zero SQL, zero proto, zero migration.**

**Slice independence:** **Single slice, frontend only.** There is no backend slice, no endpoint change and no wire-shape change, so Phase 3 has nothing to parallelize - dispatch `relay-frontend-engineer` alone. Tasks 3-9 touch disjoint files and are therefore *technically* independent of one another once Task 2 lands, but they are deliberately sequenced (mechanical first, deviants last) and must be executed by one engineer in the stated order so that a gate failure is attributable to one file.

**Spec:** `docs/superpowers/specs/2026-08-14-cursor-pager-hook.md` (authoritative).
**Backlog item:** `docs/backlog/idea-2026-08-13-cursor-pager-hook.md`. Read it only for context; **its central premise is refuted** (see "The item is wrong" below). Closing it is a Phase 6 conductor step via `/backlog close idea-2026-08-13-cursor-pager-hook` - never hand-edit `status:`.

---

## READ THIS FIRST: the gate, stated before task 1

**No existing test file may change by a single line.**

This is a behaviour-preserving refactor. The existing suites are the specification; the refactor is the thing under test. The repo rule (`reference_refactor_gate_byte_identical_tests`) is that **an assertion that needs adjusting IS the finding**, not an obstacle to it. Either the refactor changed behaviour, or the test was green because of a defect the refactor removed. Both outcomes **stop the work**.

There is no third branch. There is no "the selector just needed one more `await`". There is no "the text match was always a bit loose". If a test goes red or needs an edit:

1. **Stop. Do not edit the test.**
2. Revert the surface edit that reddened it.
3. Write down which assertion, on which surface, and what the observed-versus-expected values were.
4. Escalate to the conductor. That report is a first-class deliverable of this slice - possibly the most valuable one.

The temptation arrives around file six of seven, when everything else is clean and one assertion is *almost* passing. That is what this section exists for.

### The gate is enumeration-free, and that is deliberate

The spec enumerates twelve test files. Twelve is correct (verified below), but a hand-enumerated set can be wrong, and this one was assembled without recording four other candidates that meet its own admission criterion. So the primary gate does **not** depend on the enumeration:

**Primary gate - no test file other than the new one appears in the diff at all:**

```powershell
cd D:/dev/relay/.claude/worktrees/sad-mccarthy-6053a2
git diff --name-only $BASE -- web/src | Select-String '\.test\.tsx?$'
```

Expected output: exactly one line, `web/src/lib/useCursorPager.test.ts`, and nothing else. (`$BASE` is captured in Task 1.)

**Corroborating gate - the twelve named files each print `0<TAB>0`:**

```powershell
git diff --numstat $BASE -- `
  web/src/jobs/JobsPage.test.tsx `
  web/src/workers/WorkersPage.test.tsx `
  web/src/schedules/SchedulesPage.test.tsx `
  web/src/admin/users/UsersTab.test.tsx `
  web/src/admin/enrollments/EnrollmentsTab.test.tsx `
  web/src/admin/reservations/ReservationsTab.test.tsx `
  web/src/admin/invites/InvitesTab.test.tsx `
  web/src/admin/invites/inviteTokenSecrecy.test.tsx `
  web/src/admin/enrollments/enrollmentTokenSecrecy.test.tsx `
  web/src/admin/AdminPage.test.tsx `
  web/src/app/AdminRoute.test.tsx `
  web/src/App.test.tsx
```

Expected: **empty output** (git prints nothing for unchanged paths). If any line appears, its numstat must read `0<TAB>0`; anything else is the finding.

Run both at the end of **every** migration task (Tasks 3-9), not only at the end. The command output is the acceptance evidence; a reviewer's impression is not.

### Why twelve, and the four files the spec did not account for

Verified at HEAD by grepping every `*.test.tsx` for a top-level import of a migrated surface, of `AppRoutes`, of `AdminPage`, or of `App`:

**Tier 1 - directly mount a migrated surface and drive paging (7):** `JobsPage.test.tsx`, `WorkersPage.test.tsx`, `SchedulesPage.test.tsx`, `UsersTab.test.tsx`, `EnrollmentsTab.test.tsx`, `ReservationsTab.test.tsx`, `InvitesTab.test.tsx`.

**Tier 2 - mount a migrated surface without driving paging (5):** `inviteTokenSecrecy.test.tsx` (imports `InvitesTab` at `:13`), `enrollmentTokenSecrecy.test.tsx` (imports `EnrollmentsTab` at `:14`), `AdminPage.test.tsx` (imports `AdminPage` at `:9`), `AdminRoute.test.tsx` (imports `AppRoutes` at `:9`, and `renderApp` visits `/admin/users` and `/jobs`), `App.test.tsx` (renders `<App />`, which lands on the jobs page - it asserts `findByText('OVERVIEW')`).

**Four more files import and render `AppRoutes`, which is the exact criterion that admitted `AdminRoute.test.tsx`, and the spec never mentions them.** Each was checked; none mounts a migrated surface, so the twelve stand:

| File | Route it renders | Migrated surface mounted? |
|---|---|---|
| `web/src/jobs/logSecrecy.test.tsx` | `/jobs/j1/tasks/t1` (`:212`) | no - task-log page |
| `web/src/jobs/NewJobPage.test.tsx` | `/jobs/new` (`:29`, `:203`) | no |
| `web/src/jobs/TaskLogPage.test.tsx` | a task path (`:36`) | no |
| `web/src/profile/ProfileRoutes.test.tsx` | `/profile/...` (`:30`) | no |

Also confirmed excluded, matching the spec: `web/src/jobs/queryKeyDecoupling.test.tsx` (`renderHook` on `useJobs`/`useJobStats` only, no component - verified by reading its imports at `:11-18`), every `use*.test.tsx`, every `api.test.ts`, `web/src/jobs/pageRange.test.ts`.

---

## The item is wrong, and so is the spec in three places

### The backlog item's central premise is refuted

The item says all seven copies are character-for-character faithful and that `SchedulesPage` "differs only in identifiers". **Four are byte-identical; three are not, and `SchedulesPage` is a different algorithm.** Do not plan, review or reason from the item. The spec's section 1.2 is right and this plan follows it.

### Three places where the *spec* is wrong (verified at HEAD in this worktree)

The spec is an artifact too. These were found by re-deriving its claims rather than copying them forward.

**S1. The spec's RED prediction for hook test 3 is wrong in both position and value.** Spec section 8.1 says a hook that pushes the *next* cursor instead of the *current* one "passes the forward walk and fails here, at the second `prev`, with `cursor === 'CUR1'` instead of `''`". Neither wrong hook behaves that way:

- Push-next while keeping canonical pop-and-assign: after `next('CUR1',50)`, `next('CUR2',50)` the stack is `['CUR1','CUR2']`, so the **first** `prev` pops `'CUR2'` and assigns it - the test fails at the first `prev` with `'CUR2'`, not at the second with `'CUR1'`.
- The full `SchedulesPage` variant (derived cursor + push-next + pop-and-discard): `cursor` becomes `string | undefined`, which does not type-check against `CursorPager.cursor: string`, so it fails at `tsc` first; forced through with a cast it fails at the **second** `prev` with `undefined`, not `'CUR1'`.

The test still catches both. Only the spec's prediction of *how* is wrong. Task 2 states the corrected predictions and requires them to be observed, not assumed.

**S2. The spec's justification for hook test 5 is wrong: forgetting `setOffsets([])` in `resetPaging` is unobservable, and no test can catch it.** Spec 8.1 claims asserting only `cursor` "passes against a reset that forgets `offsets`, which is precisely the failure mode that produces a wrong footer range". It does not. `offsets` is popped only while `stack` is non-empty, and `next` pushes exactly one offset per stack entry, so `offsets.length === staleCount + stack.length` and a stale prefix is dead weight the pops never reach. Walk it: reset leaves `offsets=[0,50]`, `stack=[]`; `next('CUR1',50)` gives `offsets=[0,50,0]`, `startOffset=50`; `prev()` pops `0` and restores `startOffset=0` - correct. The three-field assertion in test 5 is still right, but for different reasons: the mutations it actually discriminates are a missing `setStartOffset(0)` (the real wrong-footer-range bug) and a missing `setStack([])`. Task 2 records this honestly rather than claiming coverage that does not exist.

**S3. The bug citation is wrong, and it is about to be copied into a new source comment.** The spec, the item and three shipped source files all cite `bug-2026-06-21-jobs-pagination-footer-absolute-range`. The closed items are `docs/backlog/closed/bug-2026-06-05-jobs-pagination-footer-absolute-range.md` and `docs/backlog/closed/bug-2026-06-21-schedules-pagination-footer-absolute-range.md` - two different items, and the citation conflates their dates. The hook's comment in Task 2 cites both correctly. This is the project's dominant defect class (wrong prose about correct code) and it would have been laundered into the one comment that outlives all seven copies.

**Minor line drift** (recorded so nobody re-derives it, none of it changes the work): `UsersTab.tsx`'s `{!filtering && (` is at `:190`, not `:191`. `SchedulesPage.test.tsx`'s third pager block is `:195-260`, not `:198-257`. `WorkersPage.test.tsx` has two more pager tests than the spec's ranges name, at `:139` and `:149`. `InvitesTab.tsx`'s comment block starts at `:13`; the pager sentences within it are `:16-21` as stated.

**One place the spec is right and stronger than it says:** typing `next`'s first parameter as `string | undefined` does not merely save a `?? ''` at seven call sites - it makes `tsc` *enforce* the falsy guard, because `string | undefined` cannot be passed to `setCursor`. Deleting the guard is a compile error, not just an untested regression.

---

## Everything that is out of scope

Non-negotiable. Each is something a well-meaning engineer or reviewer will suggest folding in, and each defeats the gate.

1. **`statusTone` and the three status modules.** `inviteStatus.ts`, `enrollmentStatus.ts` and `reservationStatus.ts` are **not opened**. Invites maps `EXPIRED -> err`; enrollments maps `EXPIRED -> muted`. That difference is deliberate and documented on both sides. No test would catch a harmonizing merge, because each module's own tone test would be rewritten to match. **The pager is the pager.**
2. **`toggleSort`.** Five copies typed over five per-module sort unions (`WorkerSort`, `UserSort`, `EnrollmentSort`, `ReservationSort`, `InviteSort`). A shared version needs a generic plus a cast at every call site - a type-level design question that does not belong inside a change whose premise is that nothing changes. It stays at five copies. Task 6 corrects the comment that accounts for them and nothing else.
3. **`formatExpiryLabel` / `EXPIRING_WINDOW_MS`.** Two consumers; the extract-before-the-third trigger has not fired. `inviteStatus.ts:5-9` and `:76-77` already name the destination and the trigger in source. Do not touch them.
4. **Sort-header wiring** (`sort` + `onSort` into five tables, five field unions).
5. **The `isPlaceholderData` prev/next disabling.** It reads off the query, not the pager, and `WorkersPage` reads it off a nested query object (`revoked.isPlaceholderData`). Stays at the call sites.
6. **The footer's composite span.** All seven differ; `SchedulesPage:179` renders `{x}-{y} of {total}` raw where the other six use `toLocaleString()`. That inconsistency is a follow-up proposal, **not** a fix to smuggle in - changing rendered text would break the gate.
7. **The control row** above each table.
8. **`computePageRange`, `web/src/lib/pageRange.ts`, and the `web/src/jobs/pageRange.ts` re-export shim.** Unchanged, not moved, not deleted, not "tidied".
9. **Any file not in the change set below.** No table component, no `Table` primitive, no `Chip`, no `web/dist`.

---

## File structure

Exactly nine source paths change, plus `docs/`.

| File | Change | Responsibility |
|---|---|---|
| `web/src/lib/useCursorPager.ts` | **create** | the one cursor-stack page-walk; owns four pieces of state, exposes six members, imports only `react` |
| `web/src/lib/useCursorPager.test.ts` | **create** | seven direct hook tests |
| `web/src/admin/users/UsersTab.tsx` | modify | mechanical; three `resetPaging` call sites; `{!filtering}` footer wrapper untouched |
| `web/src/admin/enrollments/EnrollmentsTab.tsx` | modify | mechanical; **two** `prev` render sites (footer + empty-state hatch) |
| `web/src/admin/reservations/ReservationsTab.tsx` | modify | mechanical; **two** `prev` render sites (footer + empty-state hatch) |
| `web/src/admin/invites/InvitesTab.tsx` | modify | mechanical; plus the extraction-debt comment edit (`FOURTH` -> `FIFTH`) |
| `web/src/jobs/JobsPage.tsx` | modify | no named `resetPaging`; two inlined reset sites become hook calls |
| `web/src/workers/WorkersPage.tsx` | modify | prefixed identifiers; hook call must stay above the `section === 'decommissioned'` early return; **never** calls `resetPaging` |
| `web/src/schedules/SchedulesPage.tsx` | modify | **a different algorithm**, not a rename; first-page cursor changes `undefined` -> `''` |

`web/src/lib/` is the right home: it already houses `useNow.ts` and `useDebouncedValue.ts`, two render-free behaviour-only hooks with `.test.ts` siblings. `web/src/components/` is for things that render; the pager renders nothing.

### Nine `prev`/`canPrev` render sites, not seven

A migration that scans for "the footer pattern" silently drops two of these. Count them off:

| Surface | Footer pair | Extra site |
|---|---|---|
| `JobsPage` | `:185`, `:193` | - |
| `WorkersPage` | `:135`, `:143` (inside the decommissioned branch) | - |
| `SchedulesPage` | `:185`, `:193` | - |
| `UsersTab` | `:195`, `:203` | the whole pair is wrapped in `{!filtering && (...)}` at `:190` |
| `EnrollmentsTab` | `:144`, `:152` | **second, undisabled `prev` at `:118-128`** (empty-state escape hatch) |
| `ReservationsTab` | footer pair below `:179` | **second, undisabled `prev` at `:150-160`** (empty-state escape hatch) |
| `InvitesTab` | `:142`, `:150` | none, and `:118-123` is a comment explaining why the hatch would be dead code |

---

## Task 1: Pre-flight - capture the base, verify the two load-bearing normalizations

No code. No commit. Everything here is evidence that goes in the PR body.

**Files:** none modified.

- [ ] **Step 1: Confirm a clean tree and capture the merge base**

```powershell
cd D:/dev/relay/.claude/worktrees/sad-mccarthy-6053a2
git status --porcelain
$BASE = (git merge-base HEAD origin/main).Trim()
$BASE
```

Expected: `git status --porcelain` prints nothing. `$BASE` prints a sha; at the time of writing that is `ee88de0`. **Record the sha.** Every gate command in this plan uses `$BASE`; if the shell is restarted, re-capture it.

- [ ] **Step 2: Capture the baseline suite count**

```powershell
cd D:/dev/relay/.claude/worktrees/sad-mccarthy-6053a2/web; npm test
```

Expected: all green. **Record the total test count.** The conductor's memory says 1102 as of PR #131 - do not trust that number, use the one you just measured. After Task 2 the count must be exactly `baseline + 7`, and after Tasks 3-9 it must not move at all. A count that moves by anything other than +7 means something rendered differently and is a finding.

- [ ] **Step 3: Verify the two normalizations that make the `SchedulesPage` migration a refactor rather than a behaviour change**

This is the single highest-risk edit in the slice, and it is safe only because both of these hold. Read the actual lines:

```powershell
Get-Content web/src/schedules/api.ts | Select-Object -Index 40
Get-Content web/src/schedules/useSchedules.ts | Select-Object -Index 9
```

Expected, exactly:

```
  if (cursor) q.set('cursor', cursor)
    queryKey: ['schedules', sort, cursor ?? ''],
```

Why they matter: after migration `SchedulesPage` passes `''` on the first page where it passes `undefined` today. `''` and `undefined` are both falsy, so `api.ts:41` omits the `cursor` query parameter either way, and `useSchedules.ts:10` normalizes the cache key to `''` either way. **If either line has changed** - `cursor !== undefined`, or a key without `?? ''` - **stop and escalate**: the migration would then be a behaviour change, `SchedulesPage.test.tsx:113` would legitimately go red, and Decision 4 needs revisiting. Neither line may be edited by this slice.

- [ ] **Step 4: Read the assertion that is most at risk, so you recognize it if it moves**

`web/src/schedules/SchedulesPage.test.tsx:113`:

```ts
  await waitFor(() => expect(cursors.filter((c) => c === null).length).toBeGreaterThanOrEqual(2))
```

`cursors` collects `new URL(request.url).searchParams.get('cursor')` on every request. `null` means the parameter was **absent**. The test requires it absent at least twice: once on the first load and once after `prev` returns to the first page. After migration the first page sends `''`, which `api.ts:41` still omits, so the count is still 2. **If this assertion goes red, that is the finding. Do not touch the test.**

- [ ] **Step 5: Record the pre-flight evidence**

Write down: the `$BASE` sha, the baseline test count, and confirmation that both normalization lines read as expected. No commit.

---

## Task 2: The hook and its seven tests

**Files:**
- Create: `web/src/lib/useCursorPager.ts`
- Create: `web/src/lib/useCursorPager.test.ts`

> **The test bodies below are a sketch, not a specification.** This project treats plan-supplied tests as untrusted (`reference_plan_supplied_tests_untrusted`): a test body copied from a plan is a guess until it has been run and shown to discriminate. Step 5 is mandatory - every assertion must be proven to redden against a named mutation, or explicitly recorded as characterization-only. "It matches the plan" is not verification.

- [ ] **Step 1: Write the failing tests**

Create `web/src/lib/useCursorPager.test.ts`:

```ts
import { act, renderHook } from '@testing-library/react'
import { expect, test } from 'vitest'
import { useCursorPager } from './useCursorPager'

// One act() per transition, never two calls inside one act(): result.current is
// re-read after each act, so a second call inside the same act would close over
// the pre-update render's state and silently test the wrong thing.

test('starts on the first page', () => {
  const { result } = renderHook(() => useCursorPager())
  expect(result.current.cursor).toBe('')
  expect(result.current.startOffset).toBe(0)
  expect(result.current.canPrev).toBe(false)
})

test('next advances the cursor and accumulates the real page size', () => {
  const { result } = renderHook(() => useCursorPager())
  act(() => {
    result.current.next('CUR1', 50)
  })
  expect(result.current.cursor).toBe('CUR1')
  expect(result.current.startOffset).toBe(50)
  expect(result.current.canPrev).toBe(true)
  act(() => {
    result.current.next('CUR2', 50)
  })
  expect(result.current.cursor).toBe('CUR2')
  expect(result.current.startOffset).toBe(100)
})

test('prev walks back to the cursor of the page we came from', () => {
  const { result } = renderHook(() => useCursorPager())
  act(() => {
    result.current.next('CUR1', 50)
  })
  act(() => {
    result.current.next('CUR2', 50)
  })
  act(() => {
    result.current.prev()
  })
  expect(result.current.cursor).toBe('CUR1')
  expect(result.current.startOffset).toBe(50)
  expect(result.current.canPrev).toBe(true)
  act(() => {
    result.current.prev()
  })
  expect(result.current.cursor).toBe('')
  expect(result.current.startOffset).toBe(0)
  expect(result.current.canPrev).toBe(false)
})

test('a page with no next_cursor is a no-op', () => {
  const { result } = renderHook(() => useCursorPager())
  act(() => {
    result.current.next('CUR1', 50)
  })
  act(() => {
    result.current.next('', 13)
  })
  expect(result.current.cursor).toBe('CUR1')
  expect(result.current.startOffset).toBe(50)
})

test('next(undefined, n) is a no-op', () => {
  const { result } = renderHook(() => useCursorPager())
  act(() => {
    result.current.next(undefined, 50)
  })
  expect(result.current.cursor).toBe('')
  expect(result.current.startOffset).toBe(0)
  expect(result.current.canPrev).toBe(false)
})

test('paging back off a partial last page restores the previous offset, not pageSize * depth', () => {
  const { result } = renderHook(() => useCursorPager())
  act(() => {
    result.current.next('CUR1', 50)
  })
  act(() => {
    result.current.next('CUR2', 13)
  })
  expect(result.current.startOffset).toBe(63)
  act(() => {
    result.current.prev()
  })
  expect(result.current.startOffset).toBe(50)
})

test('resetPaging returns to the first page', () => {
  let renders = 0
  const { result } = renderHook(() => {
    renders++
    return useCursorPager()
  })
  act(() => {
    result.current.next('CUR1', 50)
  })
  act(() => {
    result.current.next('CUR2', 50)
  })
  act(() => {
    result.current.resetPaging()
  })
  expect(result.current.cursor).toBe('')
  expect(result.current.startOffset).toBe(0)
  expect(result.current.canPrev).toBe(false)
  // prev on the first page is a no-op, and the guard returns BEFORE touching
  // state: without it the pops fall back to ''/0 and produce identical values,
  // so the render count is the only observable difference.
  const before = renders
  act(() => {
    result.current.prev()
  })
  expect(renders).toBe(before)
  expect(result.current.cursor).toBe('')
  expect(result.current.startOffset).toBe(0)
  expect(result.current.canPrev).toBe(false)
})
```

That is seven `test(...)` blocks. The spec's list of seven maps onto them one-for-one, with "prev on the first page is a no-op" folded into the reset test because it needs the same fixture and the same render counter.

- [ ] **Step 2: Run them to verify they fail**

```powershell
cd D:/dev/relay/.claude/worktrees/sad-mccarthy-6053a2/web; npx vitest run src/lib/useCursorPager.test.ts
```

Expected: FAIL - `Failed to resolve import "./useCursorPager"` (or `No "useCursorPager" export is defined`). All seven fail.

- [ ] **Step 3: Write the hook**

Create `web/src/lib/useCursorPager.ts`:

```ts
import { useState } from 'react'

// One page-walk over a cursor-paginated list endpoint. Seven list surfaces used to
// carry a copy of this each (JobsPage, WorkersPage, SchedulesPage, and the four
// admin tabs); this is the single owner.
//
// The server returns only `next_cursor`, never a previous one, so walking BACK means
// remembering where we came from. `stack` holds the cursors of the pages we paged
// forward FROM, so `stack.length` is the current page index and `[]` is the first
// page. `offsets` holds the real row offset each of those pages started at, and
// `startOffset` is the rows accumulated before the CURRENT page. startOffset grows by
// the ACTUAL page size on each forward step rather than by a fixed limit, so a partial
// final page keeps the footer's absolute range honest - that has already been shipped
// as a bug twice, on jobs and on schedules:
//   docs/backlog/closed/bug-2026-06-05-jobs-pagination-footer-absolute-range.md
//   docs/backlog/closed/bug-2026-06-21-schedules-pagination-footer-absolute-range.md
//
// next/prev/resetPaging use plain setters, NOT functional updaters: cursor, stack,
// offsets and startOffset are all read from the current render and React batches the
// updates in one event. Mixing a functional stack updater with plain offset setters
// would desync the two stacks under StrictMode. This is the merged form of the warning
// that used to sit separately in JobsPage, SchedulesPage and UsersTab. Do not "tidy"
// it into a single useState holding one object with one functional updater: that
// changes the update mechanics of seven shipped surfaces, and the current shape has
// no known defect.
export interface CursorPager {
  /** Cursor of the current page. '' is the first page. */
  cursor: string
  /** Rows accumulated before the current page. Feed to computePageRange. */
  startOffset: number
  /**
   * True when there is a page to go back to. Consumers get this boolean rather
   * than the stack itself: a consumer holding the array could mutate it
   * (`stack.pop()` on a React state array is a live footgun) and desync `offsets`
   * from `cursor` behind the hook's back. Value out, mutation only through these
   * methods.
   */
  canPrev: boolean
  /**
   * Advance one page. `nextCursor` is the response's `next_cursor`; a falsy value
   * (there is no further page) is a no-op. The parameter admits `undefined` on
   * purpose: every call site reads it off a possibly-undefined query result, and
   * the union makes tsc ENFORCE the falsy guard - deleting it is a compile error,
   * not merely an untested regression. `pageSize` is the ACTUAL number of rows on
   * the page being left, never the request limit.
   */
  next: (nextCursor: string | undefined, pageSize: number) => void
  /** Go back one page. A no-op on the first page. */
  prev: () => void
  /**
   * Return to the first page. Consumers MUST call this whenever the query's sort
   * key or its filters change: the server 400s a cursor issued under a different
   * sort ("cursor sort key does not match requested sort", internal/api/pagination.go).
   * The hook deliberately does not watch a sort argument - the surfaces reset on
   * six different trigger conditions (sort, status filter, include_archived, a
   * debounced email), and a single `sort` dependency does not model that.
   */
  resetPaging: () => void
}

// There is deliberately no `canNext`. Every surface computes it as
// `!data?.next_cursor`, which is a fact about the query result, not about the pager,
// and moving it in would make this hook depend on seven different response shapes.
export function useCursorPager(): CursorPager {
  const [cursor, setCursor] = useState('')
  const [stack, setStack] = useState<string[]>([])
  const [startOffset, setStartOffset] = useState(0)
  const [offsets, setOffsets] = useState<number[]>([])

  function next(nextCursor: string | undefined, pageSize: number) {
    if (!nextCursor) return
    setStack([...stack, cursor])
    setCursor(nextCursor)
    setOffsets([...offsets, startOffset])
    setStartOffset(startOffset + pageSize)
  }

  function prev() {
    if (stack.length === 0) return
    const copy = [...stack]
    const back = copy.pop() ?? ''
    setStack(copy)
    setCursor(back)
    const offsetsCopy = [...offsets]
    const prevOffset = offsetsCopy.pop() ?? 0
    setOffsets(offsetsCopy)
    setStartOffset(prevOffset)
  }

  function resetPaging() {
    setCursor('')
    setStack([])
    setStartOffset(0)
    // Clearing offsets is NOT observable and no test covers it: offsets is popped
    // only while stack is non-empty, and next pushes exactly one offset per stack
    // entry, so a stale prefix is dead weight the pops never reach. Kept anyway,
    // byte-for-byte with the seven originals, so the state stays honest and a future
    // reader is not left wondering which of the four was left behind on purpose.
    setOffsets([])
  }

  // Not wrapped in useCallback on purpose. All seven surfaces declared these as plain
  // function declarations in the component body, so a fresh identity per render IS the
  // shipped behaviour. Memoizing here would be a change, not a cleanup.
  return { cursor, startOffset, canPrev: stack.length > 0, next, prev, resetPaging }
}
```

- [ ] **Step 4: Run them to verify they pass**

```powershell
cd D:/dev/relay/.claude/worktrees/sad-mccarthy-6053a2/web; npx vitest run src/lib/useCursorPager.test.ts
```

Expected: PASS, 7 tests green.

- [ ] **Step 5: Prove each test discriminates (mutation proofs)**

Apply each mutation to `useCursorPager.ts` **one at a time**, run the file, record which tests redden and with what values, then **revert the mutation** before applying the next. The tests are permanent, so the discriminating inputs survive into the suite (`reference_mutation_proof_must_leave_a_test`).

| # | Mutation | Predicted RED |
|---|---|---|
| M1 | In `next`, `setStack([...stack, cursor])` -> `setStack([...stack, nextCursor])` | `prev walks back...` fails at the **first** `prev`: `cursor` is `'CUR2'`, expected `'CUR1'`. (Note: **not** at the second `prev` with `'CUR1'`, which is what the spec predicts - see S1.) |
| M2 | In `next`, `setStartOffset(startOffset + pageSize)` -> `setStartOffset(startOffset + 50)` | **Only** `paging back off a partial last page...` fails: `startOffset` is `100`, expected `63`. Every other test uses 50-row pages and stays green - this is what earns that test its place. |
| M3 | Delete `if (!nextCursor) return` from `next` | First a **tsc error** (`string \| undefined` is not assignable to `setCursor`'s `string`) - that alone is the proof the type carries the guard. Force it through with `setCursor(nextCursor!)`, then `a page with no next_cursor is a no-op` fails (`cursor` is `''`) and `next(undefined, n) is a no-op` fails (`cursor` is `undefined`). |
| M4 | In `resetPaging`, delete `setStartOffset(0)` | `resetPaging returns to the first page` fails: `startOffset` is `100`, expected `0`. This is the real "wrong footer range on a correct page of rows" bug. |
| M5 | In `resetPaging`, delete `setStack([])` | `resetPaging returns to the first page` fails: `canPrev` is `true`, expected `false`. |
| M6 | Delete `if (stack.length === 0) return` from `prev` | `resetPaging returns to the first page` fails on `expect(renders).toBe(before)` - the pops fall back to `''`/`0` so every *value* is identical, and the extra render caused by `setStack(copy)` receiving a fresh array reference is the only observable difference. **If this mutation does not redden the render-count assertion**, delete that assertion and its two comment lines, and record test 7 as characterization-only for the guard - do not leave a non-discriminating assertion in the suite pretending to be coverage. |
| M7 | In `resetPaging`, delete `setOffsets([])` | **Nothing reddens, and nothing can.** This is the expected result, not a gap in the tests - see S2 above and the comment in `resetPaging`. Record it; do not invent a test for it. |

Record the observed failure message for M1-M6 and the observed all-green for M7. **Revert every mutation.** Re-run the file and confirm 7 green before committing.

- [ ] **Step 6: Confirm the type-checker is happy**

```powershell
cd D:/dev/relay/.claude/worktrees/sad-mccarthy-6053a2/web; npx tsc -b
```

Expected: no output (success).

- [ ] **Step 7: Commit**

```bash
cd /d/dev/relay/.claude/worktrees/sad-mccarthy-6053a2
git add web/src/lib/useCursorPager.ts web/src/lib/useCursorPager.test.ts
git commit -m "feat(web): useCursorPager hook - one owner for the cursor-stack page walk"
```

---

## Task 3: Migrate `UsersTab` (mechanical, three reset call sites)

First real consumer, chosen because it exercises the most of the API: three `resetPaging` triggers and a conditionally-rendered footer pair.

**Files:**
- Modify: `web/src/admin/users/UsersTab.tsx` (imports `:6`; state `:34-40`; query `:45-50`; `resetPaging` `:63-68`; `pickSort`/`pickIncludeArchived`/`pickEmail` `:70-85`; `next`/`prev` `:87-109`; range `:132`; footer `:190-209`)
- Test (must not change): `web/src/admin/users/UsersTab.test.tsx`

**A note on `pageSize` that applies to every migration task in this plan:** the shipped `next` reads `data.items.length`. Each surface already computes `const users = data?.items ?? []` (or the equivalent) in scope at the footer. Pass that local array's length. It is provably equal to `data.items.length` at every point `next` gets past its `!nextCursor` guard - if `data` is undefined there is no `next_cursor` and `next` returns early - and it avoids a second `data?.` chain in JSX.

- [ ] **Step 1: Establish the baseline - the existing suite is the test**

```powershell
cd D:/dev/relay/.claude/worktrees/sad-mccarthy-6053a2/web; npx vitest run src/admin/users/UsersTab.test.tsx
```

Expected: PASS. Record the test count for this file. It must be identical after the edit.

- [ ] **Step 2: Add the import**

Insert immediately after `import { computePageRange } from '../../lib/pageRange'` (line 6):

```ts
import { useCursorPager } from '../../lib/useCursorPager'
```

- [ ] **Step 3: Replace the state block**

Delete lines 34-40 (the three-line comment plus the four `useState` calls):

```tsx
  // Cursor of the current page (''=first). The stack holds the cursors we paged
  // forward from; offsets tracks the real row offset so partial pages stay
  // correct. Same pattern as JobsPage.
  const [cursor, setCursor] = useState('')
  const [stack, setStack] = useState<string[]>([])
  const [startOffset, setStartOffset] = useState(0)
  const [offsets, setOffsets] = useState<number[]>([])
```

Replace with exactly one line, in the same position (this keeps hook order identical - `sort`, `includeArchived`, `emailInput`, `useDebouncedValue`, **pager**, `creating`, `confirm`, `resetting`):

```tsx
  const pager = useCursorPager()
```

- [ ] **Step 4: Point the query at the hook's cursor**

In the `useAdminUsers` call (lines 45-50), change the third argument:

```tsx
  const { data, error, isLoading, isPlaceholderData, refetch } = useAdminUsers(
    sort,
    includeArchived,
    pager.cursor,
    email,
  )
```

- [ ] **Step 5: Delete the local `resetPaging` and repoint its three callers**

Delete lines 63-68:

```tsx
  function resetPaging() {
    setCursor('')
    setStack([])
    setStartOffset(0)
    setOffsets([])
  }
```

Then change the three call sites, keeping every comment where it is:

```tsx
  function pickSort(field: UserSortField) {
    setSort(toggleSort(field, sort))
    // The server rejects a cursor whose sort key does not match (internal/api/pagination.go).
    pager.resetPaging()
  }

  function pickIncludeArchived(v: boolean) {
    setIncludeArchived(v)
    // Different row set and total, so the old cursor is meaningless.
    pager.resetPaging()
  }

  function pickEmail(v: string) {
    setEmailInput(v)
    pager.resetPaging()
  }
```

- [ ] **Step 6: Delete the local `next`/`prev` and their comment**

Delete lines 87-109 entirely (the three-line "Plain setters, not functional updaters" comment and both function bodies). That reasoning now lives in `useCursorPager.ts`; do not leave a copy behind.

- [ ] **Step 7: Repoint the range computation and the footer**

Line 132 becomes:

```tsx
  const { x, y } = computePageRange(pager.startOffset, users.length)
```

The footer pair (inside the existing `{!filtering && (` wrapper, which is **unchanged**):

```tsx
          {!filtering && (
            <div className="flex gap-2">
              <button
                type="button"
                onClick={pager.prev}
                disabled={!pager.canPrev || isPlaceholderData}
                className="rounded-full border border-border px-3 py-1 text-[11px] text-fg-mute disabled:opacity-40"
              >
                ← prev
              </button>
              <button
                type="button"
                onClick={() => pager.next(data?.next_cursor, users.length)}
                disabled={!data?.next_cursor || isPlaceholderData}
                className="rounded-full border border-border px-3 py-1 text-[11px] text-fg-mute disabled:opacity-40"
              >
                next 50 →
              </button>
            </div>
          )}
```

- [ ] **Step 8: Run the file's tests and the type-checker**

```powershell
cd D:/dev/relay/.claude/worktrees/sad-mccarthy-6053a2/web; npx vitest run src/admin/users/UsersTab.test.tsx; npx tsc -b
```

Expected: PASS with the same test count as Step 1, and `tsc` silent. Any red assertion: **stop, revert, escalate.**

- [ ] **Step 9: Run the gate**

```powershell
cd D:/dev/relay/.claude/worktrees/sad-mccarthy-6053a2
git diff --name-only $BASE -- web/src | Select-String '\.test\.tsx?$'
```

Expected: exactly `web/src/lib/useCursorPager.test.ts`.

- [ ] **Step 10: Commit**

```bash
cd /d/dev/relay/.claude/worktrees/sad-mccarthy-6053a2
git add web/src/admin/users/UsersTab.tsx
git commit -m "refactor(web): UsersTab uses useCursorPager"
```

---

## Task 4: Migrate `EnrollmentsTab` (two `prev` render sites)

**Files:**
- Modify: `web/src/admin/enrollments/EnrollmentsTab.tsx` (imports `:4`; state `:25-31`; query `:38`; `resetPaging` `:41-46`; `pickSort` `:48-53`; `next`/`prev` `:55-74`; range `:84`; empty-state hatch `:118-128`; footer `:140-157`)
- Test (must not change): `web/src/admin/enrollments/EnrollmentsTab.test.tsx`, `web/src/admin/enrollments/enrollmentTokenSecrecy.test.tsx`

- [ ] **Step 1: Establish the baseline**

```powershell
cd D:/dev/relay/.claude/worktrees/sad-mccarthy-6053a2/web; npx vitest run src/admin/enrollments/EnrollmentsTab.test.tsx src/admin/enrollments/enrollmentTokenSecrecy.test.tsx
```

Expected: PASS. Record the counts.

- [ ] **Step 2: Add the import**

Insert immediately after `import { computePageRange } from '../../lib/pageRange'` (line 4):

```ts
import { useCursorPager } from '../../lib/useCursorPager'
```

- [ ] **Step 3: Replace the state block**

Delete lines 25-31 (comment plus four `useState`):

```tsx
  // Cursor of the current page (''=first); stack holds the cursors we paged
  // forward from; offsets tracks the real row offset so partial pages stay
  // correct. Same pattern as UsersTab / JobsPage.
  const [cursor, setCursor] = useState('')
  const [stack, setStack] = useState<string[]>([])
  const [startOffset, setStartOffset] = useState(0)
  const [offsets, setOffsets] = useState<number[]>([])
```

Replace with:

```tsx
  const pager = useCursorPager()
```

- [ ] **Step 4: Point the query at the hook's cursor**

Line 38:

```tsx
  const { data, error, isLoading, isPlaceholderData, refetch } = useAgentEnrollments(sort, pager.cursor)
```

- [ ] **Step 5: Delete `resetPaging` and repoint `pickSort`**

Delete lines 41-46 (the whole `function resetPaging() { ... }`). Then:

```tsx
  function pickSort(field: EnrollmentSortField) {
    setSort(toggleSort(field, sort))
    // The server rejects a cursor whose sort key does not match
    // (internal/api/pagination.go:272-286).
    pager.resetPaging()
  }
```

- [ ] **Step 6: Delete the local `next`/`prev`**

Delete lines 55-74 entirely.

- [ ] **Step 7: Repoint the range, the empty-state hatch, and the footer**

Line 84:

```tsx
  const { x, y } = computePageRange(pager.startOffset, enrollments.length)
```

The empty-state escape hatch (keep its four-line comment above the `body =` assignment untouched):

```tsx
        {pager.canPrev && (
          <div className="flex justify-center">
            <button
              type="button"
              onClick={pager.prev}
              className="rounded-full border border-border px-3 py-1 text-[11px] text-fg-mute"
            >
              ← prev
            </button>
          </div>
        )}
```

The footer pair:

```tsx
          <div className="flex gap-2">
            <button
              type="button"
              onClick={pager.prev}
              disabled={!pager.canPrev || isPlaceholderData}
              className="rounded-full border border-border px-3 py-1 text-[11px] text-fg-mute disabled:opacity-40"
            >
              ← prev
            </button>
            <button
              type="button"
              onClick={() => pager.next(data?.next_cursor, enrollments.length)}
              disabled={!data?.next_cursor || isPlaceholderData}
              className="rounded-full border border-border px-3 py-1 text-[11px] text-fg-mute disabled:opacity-40"
            >
              next 50 →
            </button>
          </div>
```

Both `prev` sites must be migrated. The hatch one stays **undisabled** - it has no `disabled` attribute today and must not gain one.

- [ ] **Step 8: Run the files' tests and the type-checker**

```powershell
cd D:/dev/relay/.claude/worktrees/sad-mccarthy-6053a2/web; npx vitest run src/admin/enrollments/EnrollmentsTab.test.tsx src/admin/enrollments/enrollmentTokenSecrecy.test.tsx; npx tsc -b
```

Expected: PASS with the same counts as Step 1, `tsc` silent.

- [ ] **Step 9: Run the gate**

```powershell
cd D:/dev/relay/.claude/worktrees/sad-mccarthy-6053a2
git diff --name-only $BASE -- web/src | Select-String '\.test\.tsx?$'
```

Expected: exactly `web/src/lib/useCursorPager.test.ts`.

- [ ] **Step 10: Commit**

```bash
cd /d/dev/relay/.claude/worktrees/sad-mccarthy-6053a2
git add web/src/admin/enrollments/EnrollmentsTab.tsx
git commit -m "refactor(web): EnrollmentsTab uses useCursorPager"
```

---

## Task 5: Migrate `ReservationsTab` (two `prev` render sites)

**Files:**
- Modify: `web/src/admin/reservations/ReservationsTab.tsx` (imports `:5`; state `:50-56`; query `:64`; `resetPaging` `:71-76`; `pickSort` `:78-83`; `next`/`prev` `:85-104`; range `:118`; empty-state hatch `:150-160`; footer below `:179`)
- Test (must not change): `web/src/admin/reservations/ReservationsTab.test.tsx`

- [ ] **Step 1: Establish the baseline**

```powershell
cd D:/dev/relay/.claude/worktrees/sad-mccarthy-6053a2/web; npx vitest run src/admin/reservations/ReservationsTab.test.tsx
```

Expected: PASS. Record the count.

- [ ] **Step 2: Add the import**

Insert immediately after `import { computePageRange } from '../../lib/pageRange'` (line 5):

```ts
import { useCursorPager } from '../../lib/useCursorPager'
```

- [ ] **Step 3: Replace the state block**

Delete lines 50-56 (comment plus four `useState`):

```tsx
  // Cursor of the current page (''=first); stack holds the cursors we paged forward
  // from; offsets tracks the real row offset so partial pages stay correct. Same
  // pattern as EnrollmentsTab / UsersTab / JobsPage.
  const [cursor, setCursor] = useState('')
  const [stack, setStack] = useState<string[]>([])
  const [startOffset, setStartOffset] = useState(0)
  const [offsets, setOffsets] = useState<number[]>([])
```

Replace with:

```tsx
  const pager = useCursorPager()
```

- [ ] **Step 4: Point the query at the hook's cursor**

Line 64:

```tsx
  const { data, error, isLoading, isPlaceholderData, refetch } = useReservations(sort, pager.cursor)
```

- [ ] **Step 5: Delete `resetPaging` and repoint `pickSort`**

Delete lines 71-76. Then:

```tsx
  function pickSort(field: ReservationSortField) {
    setSort(toggleSort(field, sort))
    // The server rejects a cursor whose sort key does not match
    // (internal/api/pagination.go:272-286).
    pager.resetPaging()
  }
```

- [ ] **Step 6: Delete the local `next`/`prev`**

Delete lines 85-104 entirely.

- [ ] **Step 7: Repoint the range, the empty-state hatch, and the footer**

Line 118:

```tsx
  const { x, y } = computePageRange(pager.startOffset, reservations.length)
```

The empty-state escape hatch (its two-line comment above `body =` stays; note it cites `EnrollmentsTab.tsx:113-130`, a line reference that shifts when Task 4 lands - **leave it alone**, retargeting stale cross-file line numbers is not this slice's scope and would widen the diff):

```tsx
        {pager.canPrev && (
          <div className="flex justify-center">
            <button
              type="button"
              onClick={pager.prev}
              className="rounded-full border border-border px-3 py-1 text-[11px] text-fg-mute"
            >
              ← prev
            </button>
          </div>
        )}
```

The footer pair (below line 179, same shape as the other admin tabs):

```tsx
          <div className="flex gap-2">
            <button
              type="button"
              onClick={pager.prev}
              disabled={!pager.canPrev || isPlaceholderData}
              className="rounded-full border border-border px-3 py-1 text-[11px] text-fg-mute disabled:opacity-40"
            >
              ← prev
            </button>
            <button
              type="button"
              onClick={() => pager.next(data?.next_cursor, reservations.length)}
              disabled={!data?.next_cursor || isPlaceholderData}
              className="rounded-full border border-border px-3 py-1 text-[11px] text-fg-mute disabled:opacity-40"
            >
              next 50 →
            </button>
          </div>
```

Do not change the surrounding classNames or the span text.

- [ ] **Step 8: Run the file's tests and the type-checker**

```powershell
cd D:/dev/relay/.claude/worktrees/sad-mccarthy-6053a2/web; npx vitest run src/admin/reservations/ReservationsTab.test.tsx; npx tsc -b
```

Expected: PASS with the same count as Step 1, `tsc` silent.

- [ ] **Step 9: Run the gate**

```powershell
cd D:/dev/relay/.claude/worktrees/sad-mccarthy-6053a2
git diff --name-only $BASE -- web/src | Select-String '\.test\.tsx?$'
```

Expected: exactly `web/src/lib/useCursorPager.test.ts`.

- [ ] **Step 10: Commit**

```bash
cd /d/dev/relay/.claude/worktrees/sad-mccarthy-6053a2
git add web/src/admin/reservations/ReservationsTab.tsx
git commit -m "refactor(web): ReservationsTab uses useCursorPager"
```

---

## Task 6: Migrate `InvitesTab`, and correct the extraction-debt comment

The only comment in `web/src` that names this work. Its pager half becomes false the moment this task lands; its `toggleSort` half stays true (Decision 3 leaves that debt open) **and carries an off-by-one**. Since the sentence is being rewritten anyway, leaving a known-false number in it would be worse than not touching it.

**Files:**
- Modify: `web/src/admin/invites/InvitesTab.tsx` (imports `:4`; debt comment `:13-21`; state `:31-37`; query `:44`; `resetPaging` `:47-52`; `pickSort` `:54-59`; `next`/`prev` `:61-80`; range `:93`; footer `:138-155`)
- Test (must not change): `web/src/admin/invites/InvitesTab.test.tsx`, `web/src/admin/invites/inviteTokenSecrecy.test.tsx`

- [ ] **Step 1: Establish the baseline**

```powershell
cd D:/dev/relay/.claude/worktrees/sad-mccarthy-6053a2/web; npx vitest run src/admin/invites/InvitesTab.test.tsx src/admin/invites/inviteTokenSecrecy.test.tsx
```

Expected: PASS. Record the counts.

- [ ] **Step 2: Add the import**

Insert immediately after `import { computePageRange } from '../../lib/pageRange'` (line 4):

```ts
import { useCursorPager } from '../../lib/useCursorPager'
```

- [ ] **Step 3: Rewrite the extraction-debt comment**

Replace lines 13-21 in full:

```tsx
// Same shape as EnrollmentsTab's toggleSort (EnrollmentsTab.tsx:16-21): clicking
// the active column flips its direction, clicking the other selects it ascending.
//
// FIFTH copy of this helper - WorkersPage, UsersTab, EnrollmentsTab and
// ReservationsTab are the first four. (This note previously read FOURTH, an
// off-by-one it inherited from the invites spec and plan, which were written before
// the copy count moved.) Still not extracted: each copy is typed over its own
// per-module sort union, so a shared version needs a generic plus a cast at every
// call site - a type-level design question deliberately kept out of the cursor-pager
// extraction, whose whole premise was that nothing changes.
//
// The pager half of this debt is discharged: see web/src/lib/useCursorPager.ts.
function toggleSort(field: InviteSortField, current: InviteSort): InviteSort {
```

Verify the count yourself before writing it: grep `web/src` for `function toggleSort` and confirm there are exactly five, in `WorkersPage.tsx:22`, `UsersTab.tsx:17`, `EnrollmentsTab.tsx:16`, `ReservationsTab.tsx:21` and this one. If the count is not five, the comment is wrong again and that is a finding.

- [ ] **Step 4: Replace the state block**

Delete lines 31-37 (comment plus four `useState`):

```tsx
  // Cursor of the current page (''=first); stack holds the cursors we paged
  // forward from; offsets tracks the real row offset so partial pages stay
  // correct. Same pattern as UsersTab / EnrollmentsTab.
  const [cursor, setCursor] = useState('')
  const [stack, setStack] = useState<string[]>([])
  const [startOffset, setStartOffset] = useState(0)
  const [offsets, setOffsets] = useState<number[]>([])
```

Replace with:

```tsx
  const pager = useCursorPager()
```

- [ ] **Step 5: Point the query at the hook's cursor**

Line 44:

```tsx
  const { data, error, isLoading, isPlaceholderData, refetch } = useInvites(sort, pager.cursor)
```

- [ ] **Step 6: Delete `resetPaging`, repoint `pickSort`, delete `next`/`prev`**

Delete lines 47-52 and lines 61-80. Then:

```tsx
  function pickSort(field: InviteSortField) {
    setSort(toggleSort(field, sort))
    // The server rejects a cursor whose sort key does not match
    // (internal/api/pagination.go:272-286).
    pager.resetPaging()
  }
```

- [ ] **Step 7: Repoint the range and the footer**

Line 93:

```tsx
  const { x, y } = computePageRange(pager.startOffset, invites.length)
```

Footer pair:

```tsx
          <div className="flex gap-2">
            <button
              type="button"
              onClick={pager.prev}
              disabled={!pager.canPrev || isPlaceholderData}
              className="rounded-full border border-border px-3 py-1 text-[11px] text-fg-mute disabled:opacity-40"
            >
              ← prev
            </button>
            <button
              type="button"
              onClick={() => pager.next(data?.next_cursor, invites.length)}
              disabled={!data?.next_cursor || isPlaceholderData}
              className="rounded-full border border-border px-3 py-1 text-[11px] text-fg-mute disabled:opacity-40"
            >
              next 50 →
            </button>
          </div>
```

The `:118-123` comment explaining why this tab deliberately has **no** empty-state hatch is untouched, and no hatch is added.

- [ ] **Step 8: Run the files' tests and the type-checker**

```powershell
cd D:/dev/relay/.claude/worktrees/sad-mccarthy-6053a2/web; npx vitest run src/admin/invites/InvitesTab.test.tsx src/admin/invites/inviteTokenSecrecy.test.tsx; npx tsc -b
```

Expected: PASS with the same counts as Step 1, `tsc` silent.

- [ ] **Step 9: Run the gate**

```powershell
cd D:/dev/relay/.claude/worktrees/sad-mccarthy-6053a2
git diff --name-only $BASE -- web/src | Select-String '\.test\.tsx?$'
```

Expected: exactly `web/src/lib/useCursorPager.test.ts`.

- [ ] **Step 10: Commit**

```bash
cd /d/dev/relay/.claude/worktrees/sad-mccarthy-6053a2
git add web/src/admin/invites/InvitesTab.tsx
git commit -m "refactor(web): InvitesTab uses useCursorPager, and its debt comment reads FIFTH"
```

---

## Task 7: Migrate `JobsPage` (deviant 1 - no named `resetPaging`, two inlined reset sites)

The four mechanical tabs are done and the hook's shape is proven against real consumers. The remaining three each differ from the canonical block and each gets its own reasoning.

**`JobsPage`'s deviation:** it has **no** `resetPaging` function. It inlines the same four setters twice - in `pickFilter` (`:43-46`) and `pickSort` (`:52-55`) - and never names them. Both become `pager.resetPaging()`. The surrounding statements keep their positions relative to the reset.

**Files:**
- Modify: `web/src/jobs/JobsPage.tsx` (imports `:8`; state `:25-34`; query `:38`; `pickFilter` `:41-48`; `pickSort` `:50-56`; comment + `next`/`prev` `:58-83`; range `:108`; footer `:182-197`)
- Test (must not change): `web/src/jobs/JobsPage.test.tsx`, `web/src/App.test.tsx`

- [ ] **Step 1: Establish the baseline**

```powershell
cd D:/dev/relay/.claude/worktrees/sad-mccarthy-6053a2/web; npx vitest run src/jobs/JobsPage.test.tsx src/App.test.tsx
```

Expected: PASS. Record the counts.

- [ ] **Step 2: Add the import**

Insert immediately after `import { computePageRange } from '../lib/pageRange'` (line 8):

```ts
import { useCursorPager } from '../lib/useCursorPager'
```

- [ ] **Step 3: Replace the state block, including both comments**

Delete lines 25-34:

```tsx
  // Cursor of the current page (''=first). The stack holds the cursors of the
  // pages we paged forward from, so prev can pop back (server returns only
  // next_cursor).
  const [cursor, setCursor] = useState('')
  const [stack, setStack] = useState<string[]>([])
  // Accumulated actual-row offset to the start of the current page. Grows by
  // the real page size on each forward page, shrinks on back. Tracks in parallel
  // with stack so partial pages stay correct.
  const [startOffset, setStartOffset] = useState(0)
  const [offsets, setOffsets] = useState<number[]>([])
```

Replace with:

```tsx
  const pager = useCursorPager()
```

Both comments' content is already in `useCursorPager.ts`; do not leave a copy here.

- [ ] **Step 4: Point the query at the hook's cursor**

Line 38:

```tsx
  const { data, error, isLoading, isFetching, isPlaceholderData, refetch } = useJobs(sort, status, pager.cursor)
```

- [ ] **Step 5: Replace the two inlined reset sites**

```tsx
  function pickFilter(key: string) {
    setFilter(key)
    pager.resetPaging()
    if (key !== 'all') setSort(DEFAULT_SORT) // server rejects sort + status
  }

  function pickSort(s: JobSort) {
    setSort(s)
    pager.resetPaging()
  }
```

Note the ordering: in `pickFilter` the reset stays **between** `setFilter` and the conditional `setSort`, exactly where the four setters were. In `pickSort` the reset stays **after** `setSort`. Do not reorder.

- [ ] **Step 6: Delete the comment and both local functions**

Delete lines 58-83 entirely - the four-line "next/prev use plain setters" comment and both `next` and `prev`. That comment now lives in the hook.

- [ ] **Step 7: Repoint the range and the footer**

Line 108:

```tsx
  const { x, y } = computePageRange(pager.startOffset, jobs.length)
```

Footer pair:

```tsx
            <div className="flex gap-2">
              <button
                type="button"
                onClick={pager.prev}
                disabled={!pager.canPrev || isPlaceholderData}
                className="rounded-full border border-border px-3 py-1 text-[11px] text-fg-mute disabled:opacity-40"
              >
                ← prev
              </button>
              <button
                type="button"
                onClick={() => pager.next(data?.next_cursor, jobs.length)}
                disabled={!data?.next_cursor || isPlaceholderData}
                className="rounded-full border border-border px-3 py-1 text-[11px] text-fg-mute disabled:opacity-40"
              >
                next 50 →
              </button>
            </div>
```

`jobs` is `data?.items ?? []` and is in scope here. When `data` is undefined, `next` returns at its `!nextCursor` guard before `pageSize` is used.

- [ ] **Step 8: Run the files' tests and the type-checker**

```powershell
cd D:/dev/relay/.claude/worktrees/sad-mccarthy-6053a2/web; npx vitest run src/jobs/JobsPage.test.tsx src/App.test.tsx; npx tsc -b
```

Expected: PASS with the same counts as Step 1. `JobsPage.test.tsx` has five footer-range tests and a forward/back cursor walk; all must stay green untouched.

- [ ] **Step 9: Run the gate**

```powershell
cd D:/dev/relay/.claude/worktrees/sad-mccarthy-6053a2
git diff --name-only $BASE -- web/src | Select-String '\.test\.tsx?$'
```

Expected: exactly `web/src/lib/useCursorPager.test.ts`.

- [ ] **Step 10: Commit**

```bash
cd /d/dev/relay/.claude/worktrees/sad-mccarthy-6053a2
git add web/src/jobs/JobsPage.tsx
git commit -m "refactor(web): JobsPage uses useCursorPager"
```

---

## Task 8: Migrate `WorkersPage` (deviant 2 - prefixed identifiers, no reset at all, placement constraint)

**`WorkersPage`'s deviations, all three:**

1. Every identifier is `revoked`-prefixed, and the functions are `revokedNext`/`revokedPrev`. It will not turn up in a grep for a bare `stack`.
2. **It has no reset, and must not gain one.** The revoked-workers list carries no sort control and no filter, so nothing invalidates its cursor. `resetPaging` is called **zero** times here and that is correct.
3. **Placement constraint.** The pager UI only exists inside the `section === 'decommissioned'` branch (`:102-154`), but the state is created unconditionally at `:41-44`, above every early return. `const revokedPager = useCursorPager()` **must stay above the `if (section === 'decommissioned')` at line 102.** Putting it inside the branch breaks the rules of hooks and changes hook order on every tab switch. React fails loudly here rather than silently, but it is the one place in this slice where "move the state into a hook" has a placement rule.

**Files:**
- Modify: `web/src/workers/WorkersPage.tsx` (imports `:10`; state `:40-44`; query `:48`; `revokedNext`/`revokedPrev` `:55-74`; range `:105`; footer `:131-148`)
- Test (must not change): `web/src/workers/WorkersPage.test.tsx`

- [ ] **Step 1: Establish the baseline**

```powershell
cd D:/dev/relay/.claude/worktrees/sad-mccarthy-6053a2/web; npx vitest run src/workers/WorkersPage.test.tsx
```

Expected: PASS. Record the count. Five of these tests exercise the decommissioned pager (`:139`, `:149`, `:159`, `:207` and the section render at `:129`).

- [ ] **Step 2: Add the import**

Insert immediately after `import { computePageRange } from '../lib/pageRange'` (line 10):

```ts
import { useCursorPager } from '../lib/useCursorPager'
```

- [ ] **Step 3: Replace the state block**

Delete lines 40-44 (the comment plus four `useState`):

```tsx
  // Revoked-workers pagination state (mirrors JobsPage cursor-stack pattern).
  const [revokedCursor, setRevokedCursor] = useState('')
  const [revokedStack, setRevokedStack] = useState<string[]>([])
  const [revokedStartOffset, setRevokedStartOffset] = useState(0)
  const [revokedOffsets, setRevokedOffsets] = useState<number[]>([])
```

Replace with exactly one line, in that same position - after `section` and before `useWorkers`, which is above every early return:

```tsx
  const revokedPager = useCursorPager()
```

The comment goes and is replaced by nothing: the hook's name says what the four `useState` calls needed a sentence to say, and "mirrors JobsPage" is no longer true in the sense it meant.

- [ ] **Step 4: Point the query at the hook's cursor**

Line 48:

```tsx
  const revoked = useRevokedWorkers(section === 'decommissioned', revokedPager.cursor)
```

- [ ] **Step 5: Delete both local functions**

Delete lines 55-74 entirely (`revokedNext` and `revokedPrev`). Add nothing in their place - there is no reset here.

- [ ] **Step 6: Repoint the range and the footer**

Line 105 (inside the decommissioned branch):

```tsx
    const { x, y } = computePageRange(revokedPager.startOffset, revokedWorkers.length)
```

Footer pair:

```tsx
              <div className="flex gap-2">
                <button
                  type="button"
                  onClick={revokedPager.prev}
                  disabled={!revokedPager.canPrev || revoked.isPlaceholderData}
                  className="rounded-full border border-border px-3 py-1 text-[11px] text-fg-mute disabled:opacity-40"
                >
                  &larr; prev
                </button>
                <button
                  type="button"
                  onClick={() => revokedPager.next(revoked.data?.next_cursor, revokedWorkers.length)}
                  disabled={!revoked.data?.next_cursor || revoked.isPlaceholderData}
                  className="rounded-full border border-border px-3 py-1 text-[11px] text-fg-mute disabled:opacity-40"
                >
                  next 50 &rarr;
                </button>
              </div>
```

Note this surface uses `&larr;`/`&rarr;` HTML entities where the others use literal arrows, and reads `isPlaceholderData` off the nested `revoked` object. Both stay exactly as they are.

- [ ] **Step 7: Run the file's tests and the type-checker**

```powershell
cd D:/dev/relay/.claude/worktrees/sad-mccarthy-6053a2/web; npx vitest run src/workers/WorkersPage.test.tsx; npx tsc -b
```

Expected: PASS with the same count as Step 1. A "Rendered fewer hooks than expected" or "change in the order of Hooks" error means the hook call ended up below the early return - move it back above line 102.

- [ ] **Step 8: Run the gate**

```powershell
cd D:/dev/relay/.claude/worktrees/sad-mccarthy-6053a2
git diff --name-only $BASE -- web/src | Select-String '\.test\.tsx?$'
```

Expected: exactly `web/src/lib/useCursorPager.test.ts`.

- [ ] **Step 9: Commit**

```bash
cd /d/dev/relay/.claude/worktrees/sad-mccarthy-6053a2
git add web/src/workers/WorkersPage.tsx
git commit -m "refactor(web): WorkersPage revoked list uses useCursorPager"
```

---

## Task 9: Migrate `SchedulesPage` (deviant 3 - a different algorithm, not a rename)

**This is the highest-risk edit in the slice. Read all of this before touching the file.**

The backlog item says `SchedulesPage` "calls the stack `cursorStack` and the functions `goNext`/`goPrev`. The bodies are the same; only the identifiers differ." **That is false.** A mechanical rename would produce wrong code, and a reviewer told "only identifiers differ" would wave it through. The two algorithms:

| | canonical (the other six) | `SchedulesPage` today |
|---|---|---|
| `cursor` | its own `useState('')` | **derived**: `cursorStack[cursorStack.length - 1]` (`:31`) |
| what forward pushes | the **current** cursor | the **next** cursor (`:63`) |
| cursor on the first page | `''` | `undefined` |
| what back pops | pops and **assigns** the popped value | pops and **discards** it (`:71`) |
| state pieces | 4 | 3 |

Both maintain `stack.length === pageIndex` and `offsets.length === pageIndex`, so externally they agree on every page - except for the first-page cursor.

**The one observable difference, and why it is invisible.** After migration the first page passes `''` where it passes `undefined` today. That is invisible for exactly two reasons, both re-verified in Task 1 Step 3 and neither of which may be edited:

- `web/src/schedules/api.ts:41` guards with `if (cursor) q.set('cursor', cursor)`. Both `''` and `undefined` are falsy, so the `cursor` query parameter is **absent** either way.
- `web/src/schedules/useSchedules.ts:10` uses `queryKey: ['schedules', sort, cursor ?? '']`, so the cache key is `''` either way.

**If `SchedulesPage.test.tsx:113` goes red, that is the finding.** It asserts `cursors.filter((c) => c === null).length >= 2` - the raw query parameter must be absent on the first load and again after `prev`. Do not touch it, do not "just add `?? ''` somewhere". Revert, and report which of the two normalizations stopped holding.

**Files:**
- Modify: `web/src/schedules/SchedulesPage.tsx` (imports `:7`; state `:29-35`; query `:38`; `chooseSort` `:49-54`; comment + `goNext`/`goPrev` `:56-77`; range `:122`; footer `:182-199`)
- Test (must not change): `web/src/schedules/SchedulesPage.test.tsx`

- [ ] **Step 1: Establish the baseline**

```powershell
cd D:/dev/relay/.claude/worktrees/sad-mccarthy-6053a2/web; npx vitest run src/schedules/SchedulesPage.test.tsx
```

Expected: PASS. Record the count. Four of these drive the pager: `:94` (the cursor walk with the at-risk assertion), `:116` (in-flight disabling), `:210` (partial last page), `:234` (range restored on back).

- [ ] **Step 2: Add the import**

Insert immediately after `import { computePageRange } from '../lib/pageRange'` (line 7):

```ts
import { useCursorPager } from '../lib/useCursorPager'
```

- [ ] **Step 3: Replace the three-piece state with the hook**

Delete lines 29-35:

```tsx
  // Cursor stack: [] is the first page; each entry is the cursor for a deeper page.
  const [cursorStack, setCursorStack] = useState<string[]>([])
  const cursor = cursorStack[cursorStack.length - 1]
  // Accumulated actual-row offset to the start of the current page. Mirrors
  // cursorStack depth so partial pages stay correct (same pattern as JobsPage).
  const [startOffset, setStartOffset] = useState(0)
  const [offsets, setOffsets] = useState<number[]>([])
```

Replace with:

```tsx
  const pager = useCursorPager()
```

`const [pendingId, setPendingId] = useState<string | null>(null)` stays immediately below it, so hook order is unchanged: `sort`, pager's four, `pendingId`, `useSchedules`, `useScheduleActions`, the tick `useState`, the tick `useEffect`.

- [ ] **Step 4: Point the query at the hook's cursor**

Line 38:

```tsx
  const { data, error, isLoading, isPlaceholderData, refetch } = useSchedules(sort, pager.cursor)
```

This is the line where `undefined` becomes `''`. `useSchedules(sort: ScheduleSort, cursor?: string, ...)` accepts `string` fine; no signature change, no `?? ''`, no cast.

- [ ] **Step 5: Replace the inlined reset in `chooseSort`**

```tsx
  function chooseSort(next: ScheduleSort) {
    setSort(next)
    pager.resetPaging() // restart paging when the sort changes
  }
```

The `// restart paging when the sort changes` comment moves onto the `resetPaging` call. The parameter is still named `next`; it is a local `ScheduleSort` and does not collide with anything now that the local `goNext` is gone.

- [ ] **Step 6: Delete the comment and both local functions**

Delete lines 56-77 entirely - the four-line StrictMode comment plus `goNext` and `goPrev`. That comment is the sharpest of the three and its text is already in `useCursorPager.ts`.

- [ ] **Step 7: Repoint the range and the footer**

Line 122:

```tsx
  const { x, y } = computePageRange(pager.startOffset, schedules.length)
```

Footer pair - note this surface writes `disabled` **before** `onClick`, the opposite of the other six. Keep that order to keep the diff minimal:

```tsx
            <div className="flex gap-1.5">
              <button
                type="button"
                disabled={!pager.canPrev || isPlaceholderData}
                onClick={pager.prev}
                className="rounded-full border border-border px-3 py-1 text-[11px] text-fg-mute disabled:opacity-40"
              >
                ← prev
              </button>
              <button
                type="button"
                disabled={!data?.next_cursor || isPlaceholderData}
                onClick={() => pager.next(data?.next_cursor, schedules.length)}
                className="rounded-full border border-border px-3 py-1 text-[11px] text-fg-mute disabled:opacity-40"
              >
                next 50 →
              </button>
            </div>
```

Do **not** touch the `SHOWING <span className="text-fg">{x}-{y} of {total}</span>` span at line 179. It is the only one of the seven that skips `toLocaleString()`. That is a real inconsistency, it is a follow-up proposal, and fixing it here would change rendered text and break the gate.

- [ ] **Step 8: Verify no variant survives**

```powershell
cd D:/dev/relay/.claude/worktrees/sad-mccarthy-6053a2
git grep -n "cursorStack\|goNext\|goPrev" -- web/src
git grep -n "setStartOffset\|setOffsets\|setCursorStack\|useState<string\[\]>" -- web/src
```

Expected: the first command returns **nothing**. The second returns only hits inside `web/src/lib/useCursorPager.ts`.

- [ ] **Step 9: Run the file's tests and the type-checker**

```powershell
cd D:/dev/relay/.claude/worktrees/sad-mccarthy-6053a2/web; npx vitest run src/schedules/SchedulesPage.test.tsx; npx tsc -b
```

Expected: PASS with the same count as Step 1, `tsc` silent. **If `next/prev pagination walks the cursor` fails, stop and escalate** - re-read Task 1 Step 3 and report which normalization moved.

- [ ] **Step 10: Run the gate**

```powershell
cd D:/dev/relay/.claude/worktrees/sad-mccarthy-6053a2
git diff --name-only $BASE -- web/src | Select-String '\.test\.tsx?$'
```

Expected: exactly `web/src/lib/useCursorPager.test.ts`.

- [ ] **Step 11: Commit**

```bash
cd /d/dev/relay/.claude/worktrees/sad-mccarthy-6053a2
git add web/src/schedules/SchedulesPage.tsx
git commit -m "refactor(web): SchedulesPage uses useCursorPager, retiring its cursorStack variant"
```

---

## Task 10: Final gate, full suite, build, change-set audit

**Files:** none modified (unless `web/dist` needs reverting).

- [ ] **Step 1: Full suite**

```powershell
cd D:/dev/relay/.claude/worktrees/sad-mccarthy-6053a2/web; npm test
```

Expected: all green, and the total is **exactly** `baseline + 7` from Task 1 Step 2. Any other number means something rendered differently - investigate before proceeding, do not rationalize it.

- [ ] **Step 2: Build**

```powershell
cd D:/dev/relay/.claude/worktrees/sad-mccarthy-6053a2/web; npm run build
```

Expected: `tsc -b && vite build` both succeed.

- [ ] **Step 3: Revert the build artifacts**

`web/dist` is tracked but stale from the scaffold, so a build dirties it and it is **not** part of this change set.

```powershell
cd D:/dev/relay/.claude/worktrees/sad-mccarthy-6053a2
git checkout -- web/dist/
git status --porcelain
```

Expected: `git status --porcelain` prints nothing.

- [ ] **Step 4: The primary gate**

```powershell
git diff --name-only $BASE -- web/src | Select-String '\.test\.tsx?$'
```

Expected: exactly one line, `web/src/lib/useCursorPager.test.ts`. **Paste the raw output into the PR body.**

- [ ] **Step 5: The corroborating twelve-file numstat**

```powershell
git diff --numstat $BASE -- `
  web/src/jobs/JobsPage.test.tsx `
  web/src/workers/WorkersPage.test.tsx `
  web/src/schedules/SchedulesPage.test.tsx `
  web/src/admin/users/UsersTab.test.tsx `
  web/src/admin/enrollments/EnrollmentsTab.test.tsx `
  web/src/admin/reservations/ReservationsTab.test.tsx `
  web/src/admin/invites/InvitesTab.test.tsx `
  web/src/admin/invites/inviteTokenSecrecy.test.tsx `
  web/src/admin/enrollments/enrollmentTokenSecrecy.test.tsx `
  web/src/admin/AdminPage.test.tsx `
  web/src/app/AdminRoute.test.tsx `
  web/src/App.test.tsx
```

Expected: **empty output**. **Paste the raw output (or the absence of it) into the PR body.**

- [ ] **Step 6: The change-set audit**

```powershell
git diff --name-only $BASE
```

Expected, and nothing else:

```
docs/superpowers/plans/2026-08-14-cursor-pager-hook.md
docs/superpowers/specs/2026-08-14-cursor-pager-hook.md
web/src/admin/enrollments/EnrollmentsTab.tsx
web/src/admin/invites/InvitesTab.tsx
web/src/admin/reservations/ReservationsTab.tsx
web/src/admin/users/UsersTab.tsx
web/src/jobs/JobsPage.tsx
web/src/lib/useCursorPager.test.ts
web/src/lib/useCursorPager.ts
web/src/schedules/SchedulesPage.tsx
web/src/workers/WorkersPage.tsx
```

(Plus whatever the conductor has already committed under `docs/`.) In particular there must be **no** `inviteStatus.ts`, **no** `enrollmentStatus.ts`, **no** `reservationStatus.ts`, **no** `pageRange.ts`, **no** table or `Chip` component, and **no** `web/dist`.

- [ ] **Step 7: Confirm the status modules are byte-identical**

```powershell
git diff --numstat $BASE -- `
  web/src/admin/invites/inviteStatus.ts `
  web/src/admin/enrollments/enrollmentStatus.ts `
  web/src/admin/reservations/reservationStatus.ts
```

Expected: empty output. Then spot-check the two that matter, because this is the one difference a harmonizing edit would erase with no test going red: `inviteStatus.ts:59-64` must still map `EXPIRED -> 'err'`, and `enrollmentStatus.ts:29-33` must still map `EXPIRED -> 'muted'`.

- [ ] **Step 8: Confirm the untouched comments stayed untouched**

```powershell
git diff --numstat $BASE -- web/src/schedules/ScheduleDetailPage.tsx web/src/profile/tabs.ts web/src/jobs/pageRange.ts web/src/jobs/pageRange.test.ts web/src/lib/pageRange.ts
```

Expected: empty output. `inviteStatus.ts:5-9` and `:76-77` (the `web/src/lib/expiry.ts` destination comment) and `ScheduleDetailPage.tsx:30-34` (the detail-page-triad item's comment) belong to other items.

- [ ] **Step 9: No commit needed**

Nothing changed in this task. If `git status --porcelain` is non-empty, find out why before proceeding.

---

## Verification (Phase 4) - what the reviewers must be told

Per the spec's section 8.3, **the integration slot is not reassigned to a real browser here.** The 2026-08-13/14 lesson about a real-browser lane on a zero-Go diff applies to *rendering* changes; this slice has no layout, no paint, no focus, no key events and no network-shape change, so a browser would confirm only that unchanged pages still load. Spend the slot on a fourth review lens instead.

One lens brief is specific to this slice and must be handed over verbatim:

> Confirm the twelve-file zero-diff from the numstat output in the PR body, then independently confirm that `web/src/admin/invites/inviteStatus.ts` and `web/src/admin/enrollments/enrollmentStatus.ts` are byte-identical to the merge base and still map `EXPIRED` to `err` and `muted` respectively. That difference is deliberate, is documented on both sides, and would be erased by a harmonizing edit with no test going red.

A second brief worth handing over:

> The backlog item claims all seven pager copies are verbatim and that `SchedulesPage` differs only in identifiers. Both claims are false. Review `SchedulesPage.tsx` against the spec's section 1.2 table, not against the item.

---

## Acceptance criteria

1. `web/src/lib/useCursorPager.ts` exists, exports `useCursorPager` returning `{cursor, startOffset, canPrev, next, prev, resetPaging}`, imports only from `react`, and imports nothing from `@tanstack/react-query` or any feature module.
2. The hook does not return the cursor stack or the offsets array.
3. All seven surfaces use it. `git grep "useState<string\[\]>" -- web/src` and `git grep "setStartOffset\|setOffsets" -- web/src` return hits only inside `useCursorPager.ts`.
4. `cursorStack`, `goNext` and `goPrev` do not appear anywhere in `web/src`. No eighth variant exists.
5. **Primary gate:** `git diff --name-only $BASE -- web/src | Select-String '\.test\.tsx?$'` returns exactly `web/src/lib/useCursorPager.test.ts`. **Corroborating gate:** the twelve-file numstat is empty. Both outputs are in the PR body.
6. `useCursorPager.test.ts` holds seven tests covering: first page, forward walk, backward walk, a `next_cursor`-less page, `next(undefined, n)`, a partial last page walked back, and `resetPaging` plus `prev`-on-first-page. Each was run RED first, and mutations M1-M6 were each observed to redden a named test (with M6's render-count assertion dropped and recorded if it does not discriminate). M7 was observed to redden nothing, as predicted.
7. `inviteStatus.ts`, `enrollmentStatus.ts` and `reservationStatus.ts` do not appear in the change set at all, and `EXPIRED` still maps to `err` in invites and `muted` in enrollments.
8. `web/src/admin/invites/InvitesTab.tsx` no longer claims an un-extracted seventh pager copy; its `toggleSort` accounting survives and reads **FIFTH**, with the correction noted.
9. `inviteStatus.ts:5-9` / `:76-77`, `ScheduleDetailPage.tsx:30-34`, `profile/tabs.ts:13-15`, `web/src/jobs/pageRange.ts` and `web/src/lib/pageRange.ts` are unchanged.
10. The change set is exactly the nine source paths in the File structure table, plus `docs/`. No `web/dist`.
11. `npm test` green, total exactly `baseline + 7`; `npm run build` green.
12. Phase 6: the conductor closes `idea-2026-08-13-cursor-pager-hook` with `/backlog close idea-2026-08-13-cursor-pager-hook`, and the Resolution records four things - that `SchedulesPage` was a different algorithm rather than a renamed one; that `toggleSort` was deliberately left at five copies; that the `formatExpiryLabel` / `EXPIRING_WINDOW_MS` half was deliberately **not** done and its trigger (a third status module) lives in `inviteStatus.ts:5-9`; and the gate result.

---

## Risks

| Risk | Mitigation |
|---|---|
| **The gate gets negotiated at file six of seven.** The single largest risk. | Procedural, not technical: the gate is stated before Task 1, it is re-run at the end of *every* migration task rather than once at the end, and the acceptance evidence is command output pasted into the PR body rather than a claim. |
| **`SchedulesPage.test.tsx:113` goes red.** | Both normalizations are verified in Task 1 Step 3, before any code moves, and named again in Task 9. If it reddens anyway, the correct response is to stop - the migration would be a behaviour change, not a refactor. |
| **A reviewer reads the backlog item instead of the spec** and concludes the seven copies are verbatim, rubber-stamping `SchedulesPage`. | Task 9's table, the "The item is wrong" section, the review brief above, and hook test 3, which fails on the wrong-push bug. |
| **`WorkersPage`'s hook call lands below the early return.** | Called out in Task 8's deviation list and in its Step 7 expected-failure note. React fails loudly ("change in the order of Hooks"), so this cannot ship silently. |
| **The two empty-state `prev` hatches get dropped.** They are the only `prev` sites outside a footer. | The nine-render-site table in File structure, plus explicit steps in Tasks 4 and 5. `EnrollmentsTab.test.tsx` and `ReservationsTab.test.tsx` cover them, so this also fails loudly. |
| **Someone harmonizes `statusTone` while in the admin directory.** No test would catch it. | Out-of-scope item 1, acceptance criterion 7, Task 10 Step 7, and the dedicated review lens brief. |
| **The suite count moves by something other than +7.** | Checked explicitly in Task 10 Step 1 against the Task 1 baseline; it is a cheap signal independent of the diff gate. |
| **`idea-2026-08-12-detail-page-state-triad-primitive` runs concurrently.** Both are zero-diff-gated refactors over overlapping test directories, so a red run would be hard to attribute. | Confirmed at spec time as open, untouched, no spec, no plan, no branch. Do not start it while this is in flight. |

---

## Follow-ups to propose at Phase 6 (proposals for human accept - do NOT file automatically)

| Proposal | Why |
|---|---|
| `idea-2026-08-14-toggle-sort-generic` (low) - lift the five `toggleSort` copies into one generic helper. | Out-of-scope item 2 leaves it at five copies. The `InvitesTab` comment keeps carrying the debt, but a comment is not a queue. Unblocked once the pager lands; the only real question is the generic's shape. |
| `bug-2026-08-14-schedules-footer-range-not-localized` (low) - `SchedulesPage.tsx:179` renders `{x}-{y} of {total}` where the other six render `x.toLocaleString()`. | A real inconsistency found while verifying the footer for scope exclusion. This slice must **not** fix it (it changes rendered text and breaks the gate), and it goes invisible again the moment nobody is looking at all seven footers side by side. |
| Amend `idea-2026-08-12-detail-page-state-triad-primitive` with "do not run concurrently with the cursor-pager extraction; both are zero-diff-gated refactors over overlapping test directories." | That item's Related section cross-links this one but frames it as "worth reading together". The concurrency hazard is sharper than that and belongs in the item, not only in a spec. |
| Correct the `bug-2026-06-21-jobs-pagination-footer-absolute-range` citation in the three shipped source comments that carry it (see S3). | The item is `bug-2026-06-05-jobs-...`; `bug-2026-06-21-...` is the *schedules* one. The hook's own comment cites both correctly, but the stale citation survives elsewhere. Not fixed here because those comments are being deleted by this slice anyway or live in files outside the change set - worth a sweep. |

Deliberately **not** proposed: a `canNext` on the hook (it is a fact about the query, not the pager); a merged single-`useState` internal shape (three shipped comments defended the current one and there is no defect); extracting the footer span (six genuine variations); the `formatExpiryLabel` / `EXPIRING_WINDOW_MS` extraction (two consumers - the extract-before-the-third trigger has not fired, and `inviteStatus.ts` already carries both the destination and the trigger in source, which is a more durable carrier than a backlog file); anything touching `statusTone`.
