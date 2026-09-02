# The pager chain: four items, one branch, one PR - Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Land four backlog items on one branch as four commits in a fixed order: make `useCursorPager.next` take the page it is leaving, extract one generic `toggleSort`, localize the schedules footer's row range, and replace three stale line-number citations with symbol references.

**Architecture:** Frontend only. Commit 1 changes one hook signature and rewires seven call sites. Commit 2 extracts one five-line helper and deletes five copies. Commit 3 changes one JSX expression in one page. Commit 4 changes three comments and appends a paragraph to `web/CLAUDE.md`. Commits 1 and 2 are behaviour-preserving refactors licensed by a zero-line diff to every test file that existed at the merge base; commits 3 and 4 deliberately unfreeze three of those files, one commit at a time.

**Tech Stack:** TypeScript, React 18, vitest + jsdom, @testing-library/react, msw, TanStack Query v5. No new dependency, no Go, no SQL, no proto, no migration, no endpoint change.

**Spec:** `docs/superpowers/specs/2026-09-01-pager-chain-design.md`

---

## Slice independence declaration

- **Frontend only. There is no backend slice.** The diff touches `web/` and `docs/` and nothing else. No `.go`, no `.sql`, no `.proto`, no migration, so no `make generate` step exists anywhere in this plan and no Go lane needs to run.
- **The four commits are STRICTLY SEQUENTIAL and must not be parallelised or reordered.** The order is load-bearing, not stylistic: commits 1 and 2 are licensed by a zero-line diff to every test file that existed at the merge base, and commits 3 and 4 deliberately edit three of those files. Running any of them out of order, or in parallel worktrees, destroys the per-commit gate evidence that is the whole point of the chain. Spec section 6, escalation 5, records this call.
- **One engineer, one worktree, one PR.** Phase 3 has a single lane here. Do not dispatch two engineers.

## This is a single-session plan. Do NOT file backlog phases for it

This plan is one branch, one PR, four commits, one session. It has no multi-session units, so it defines no `## Stage N` sections and the conductor should NOT run `/backlog phases` on it. The headings below are `## Commit N`, which are the units of one PR, not schedulable stages.

## Standing rules for every task in this plan

- **The working directory is `D:/dev/relay/.claude/worktrees/web-b-pager-chain`.** Every path below is relative to it. Never `cd D:/dev/relay` and never touch another worktree.
- **All vitest and tsc commands run from `web/`.** Written below as `cd web` followed by the command; from PowerShell use `cd web; npx vitest run ...`.
- **Every commit uses an explicit pathspec.** Never `git add -A`, never `git add .`, never `git commit -a`. `web/dist/` must never be staged: it is tracked but stale, and `npm run build` rewrites it. After any `npm run build`, run `git checkout -- web/dist/` before staging anything.
- **The engineer must NOT run `/backlog close`.** The four items in spec section 5.1 are closed by the conductor after the PR merges. Do not edit `status:` in any backlog file, and do not `git mv` anything under `docs/backlog/`.
- **Comment policy (root `CLAUDE.md`, "Comments") applies to every comment this plan writes.** No dates, no change history, no session narrative, no counts of things elsewhere, no uniqueness or completeness claims about other code, no censuses. Comments state a hazard the code cannot show and may cite the test that pins it. This plan DELETES several comment blocks that violate the policy; it introduces none that do.
- **CRLF and encoding check after every programmatic edit** (root `CLAUDE.md`, "Line endings"). Before each commit: check the diffstat against the size of the change you intended, and run `git ls-files --eol` on the touched paths. Every one must read `i/lf`. All text this plan writes is ASCII except where it copies an existing file's characters verbatim; if you type a non-ASCII character, verify the file still decodes as UTF-8.

---

## Task 0: pre-flight

**Files:** none modified.

- [ ] **Step 1: Record the merge base SHA. Every gate command below uses it.**

```bash
git merge-base origin/main HEAD
```

Write the SHA down. Call it `<BASE>` and paste the literal SHA into every command below that says `<BASE>`. Do not substitute `origin/main` itself: if `origin/main` advances while this branch is in flight, a diff against the moving tip mixes other people's changes into this branch's gate evidence and the gate silently stops meaning what it says. If `origin/main` has not moved, `<BASE>` and `origin/main` name the same commit and the two forms agree.

- [ ] **Step 2: Confirm the frozen twelve are clean right now**

```bash
git diff --numstat <BASE> -- \
  web/src/jobs/JobsPage.test.tsx \
  web/src/workers/WorkersPage.test.tsx \
  web/src/schedules/SchedulesPage.test.tsx \
  web/src/admin/users/UsersTab.test.tsx \
  web/src/admin/enrollments/EnrollmentsTab.test.tsx \
  web/src/admin/reservations/ReservationsTab.test.tsx \
  web/src/admin/invites/InvitesTab.test.tsx \
  web/src/jobs/JobsPage.pager.test.tsx \
  web/src/schedules/SchedulesPage.pager.test.tsx \
  web/src/workers/WorkersPage.pager.test.tsx \
  web/src/admin/users/UsersTab.pager.test.tsx \
  web/src/admin/reservations/ReservationsTab.pager.test.tsx
```

Expected: no output. Call this exact command `GATE-12` below.

- [ ] **Step 3: Confirm the whole web suite is green before anything changes**

```bash
cd web
npx vitest run
```

Expected: all files pass. Record the "Test Files N passed / Tests M passed" line. Criterion 4.E compares against it at the end.

- [ ] **Step 4: Confirm `web/dist` is not dirty**

```bash
git status --porcelain web/dist
```

Expected: no output. If there is output, `git checkout -- web/dist/` and re-check.

---

## Commit 1 - `useCursorPager.next` takes the page

Closes: `idea-2026-08-14-cursor-pager-next-takes-the-page` (the conductor runs the close, not the engineer).

**Files:**
- Modify: `web/src/lib/useCursorPager.ts` (whole file rewritten below; 115 lines at HEAD)
- Modify: `web/src/lib/useCursorPager.test.ts` (whole file rewritten below; 153 lines at HEAD)
- Modify: `web/src/jobs/JobsPage.tsx:151`
- Modify: `web/src/workers/WorkersPage.tsx:118`
- Modify: `web/src/schedules/SchedulesPage.tsx:164`
- Modify: `web/src/admin/users/UsersTab.tsx:166`
- Modify: `web/src/admin/enrollments/EnrollmentsTab.tsx:117`
- Modify: `web/src/admin/reservations/ReservationsTab.tsx:164`
- Modify: `web/src/admin/invites/InvitesTab.tsx:119`
- Create: `web/src/workers/WorkersPage.revokedPager.test.tsx`

Note on `web/src/workers/WorkersPage.tsx:8`: the `type SortField` import is NOT touched at this commit. It is touched at commit 2, when deleting the local `toggleSort` makes it unused. Do not pre-empt that here.

### Task 1.1: adapt the hook's own tests to the new call shape

`web/src/lib/useCursorPager.test.ts` is the ONE test file licensed to change at this commit, because the hook's API is the thing being changed. Every existing test keeps its property and its name; only the call shape changes, plus one rename and one addition.

- [ ] **Step 1: Replace the whole of `web/src/lib/useCursorPager.test.ts` with this**

```ts
import { act, renderHook } from '@testing-library/react'
import { expect, test } from 'vitest'
import { useCursorPager } from './useCursorPager'

// One act() per transition, never two calls inside one act(): result.current is
// re-read after each act, so a second call inside the same act would close over
// the pre-update render's state and silently test the wrong thing.

// A real array of the stated length, never an object with a `length` property: the
// hook reads items.length, and a fake that only carries `length` would let a
// mutation reading some other property survive.
function page(next_cursor: string, size: number) {
  return { next_cursor, items: Array.from({ length: size }, (_, i) => i) }
}

test('starts on the first page', () => {
  const { result } = renderHook(() => useCursorPager())
  expect(result.current.cursor).toBe('')
  expect(result.current.startOffset).toBe(0)
  expect(result.current.canPrev).toBe(false)
})

test('next advances the cursor and accumulates the real page size', () => {
  const { result } = renderHook(() => useCursorPager())
  act(() => {
    result.current.next(page('CUR1', 50))
  })
  expect(result.current.cursor).toBe('CUR1')
  expect(result.current.startOffset).toBe(50)
  expect(result.current.canPrev).toBe(true)
  act(() => {
    result.current.next(page('CUR2', 50))
  })
  expect(result.current.cursor).toBe('CUR2')
  expect(result.current.startOffset).toBe(100)
})

// The page carries the cursor and the rows together, so a caller cannot state a size
// that disagrees with the page it is leaving. 7 discriminates: an accumulation that
// adds a constant page size instead of items.length reports 50 here.
test("the offset advances by the page's own row count, not by a caller-supplied number", () => {
  const { result } = renderHook(() => useCursorPager())
  act(() => {
    result.current.next(page('CUR1', 7))
  })
  expect(result.current.cursor).toBe('CUR1')
  expect(result.current.startOffset).toBe(7)
})

test('prev walks back to the cursor of the page we came from', () => {
  const { result } = renderHook(() => useCursorPager())
  act(() => {
    result.current.next(page('CUR1', 50))
  })
  act(() => {
    result.current.next(page('CUR2', 50))
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
    result.current.next(page('CUR1', 50))
  })
  act(() => {
    result.current.next(page('', 13))
  })
  expect(result.current.cursor).toBe('CUR1')
  expect(result.current.startOffset).toBe(50)
})

test('next(undefined) is a no-op', () => {
  const { result } = renderHook(() => useCursorPager())
  act(() => {
    result.current.next(undefined)
  })
  expect(result.current.cursor).toBe('')
  expect(result.current.startOffset).toBe(0)
  expect(result.current.canPrev).toBe(false)
})

test('paging back off a partial last page restores the previous offset, not pageSize * depth', () => {
  const { result } = renderHook(() => useCursorPager())
  act(() => {
    result.current.next(page('CUR1', 50))
  })
  act(() => {
    result.current.next(page('CUR2', 13))
  })
  expect(result.current.startOffset).toBe(63)
  act(() => {
    result.current.prev()
  })
  expect(result.current.startOffset).toBe(50)
})

// Three pages with two DISTINCT partial sizes (13 then 50 then 7), so neither of the
// two wrong formulas below can coincide with the right answer by accident:
//   - `copy.length * 50` (stack depth times a fixed page size): with a two-page walk
//     of (50, 13) the first `prev` already diverges (50 vs the naive 1*50), but that
//     collision is with the SAME number this test also uses for pageSize, which is
//     exactly the coincidence that let a naive formula hide. Three distinct sizes
//     remove any pageSize value that could double as "the" pageSize.
//   - `startOffset - 50` (subtract a fixed page size from the running total instead of
//     popping the real offsets stack): on a two-page walk of (13, 50), the second
//     page's real size (50) happens to equal the constant being subtracted, so
//     restoring 63 - 50 = 13 matches the correct answer BY COINCIDENCE. A third page
//     of a third size breaks that coincidence.
test('paging back through three partial pages restores each real offset, not a fixed-page-size guess', () => {
  const { result } = renderHook(() => useCursorPager())
  act(() => {
    result.current.next(page('CUR1', 13))
  })
  act(() => {
    result.current.next(page('CUR2', 50))
  })
  act(() => {
    result.current.next(page('CUR3', 7))
  })
  expect(result.current.startOffset).toBe(70)
  act(() => {
    result.current.prev()
  })
  expect(result.current.startOffset).toBe(63)
  act(() => {
    result.current.prev()
  })
  expect(result.current.startOffset).toBe(13)
})

test('resetPaging returns to the first page', () => {
  let renders = 0
  const { result } = renderHook(() => {
    renders++
    return useCursorPager()
  })
  act(() => {
    result.current.next(page('CUR1', 50))
  })
  act(() => {
    result.current.next(page('CUR2', 50))
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

- [ ] **Step 2: Run the hook's tests and see them fail for the right reason**

```bash
cd web
npx vitest run src/lib/useCursorPager.test.ts
```

Expected: FAIL. The old hook takes `(nextCursor, pageSize)`, so the page object arrives as `nextCursor`. It is truthy, so the guard passes, `cursor` becomes an object rather than a string, and `pageSize` is `undefined`, so `startOffset` becomes `NaN`. The failures should read like `expected NaN to be 50` and `expected { next_cursor: 'CUR1', items: [...] } to be 'CUR1'`. `starts on the first page` still passes: it makes no `next` call.

If instead you see only `next(undefined) is a no-op` failing, stop: the file was not replaced.

### Task 1.2: change the hook

- [ ] **Step 1: Replace the whole of `web/src/lib/useCursorPager.ts` with this**

Three comment blocks lose content here, per spec Decision 1.6: the header's census of what the seven surfaces used to carry, `resetPaging`'s count of reset call sites, the `canNext` note's claim about every surface, and the provenance lines inside the StrictMode warning and the `resetPaging` body comment. Every hazard those blocks carried survives. The deleted reasoning lives in `docs/superpowers/specs/2026-08-14-cursor-pager-hook.md` and `docs/retros/2026-08-14-cursor-pager-hook.md`.

```ts
import { useState } from 'react'

// One page-walk over a cursor-paginated list endpoint.
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
// would desync the two stacks under StrictMode. Do not "tidy" it into a single
// useState holding one object with one functional updater: that changes the update
// mechanics of every surface that uses this hook, and the current shape has no known
// defect.

/**
 * One page of a cursor-paginated list response, as the pager needs to see it.
 *
 * `next_cursor` is REQUIRED, not optional. Every list response declares it, and with
 * the field required a response type that loses or renames it is a compile error at
 * the call site; with it optional the property would simply read `undefined`, `next`
 * would become a silent permanent no-op and the next button would be permanently
 * disabled with nothing red. `items` is read only for its length, and is `readonly`
 * because the pager does not own the array.
 */
export interface CursorPage {
  next_cursor: string
  items: readonly unknown[]
}

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
   * Advance one page, given the page being LEFT. A page whose `next_cursor` is falsy
   * (there is no further page) is a no-op, and so is `undefined`. The parameter
   * admits `undefined` on purpose: every call site reads it off a possibly-undefined
   * query result, and the union makes tsc ENFORCE the falsy guard - delete the guard
   * and `page.next_cursor` stops compiling, so it is a compile error rather than an
   * untested regression. The offset advances by `page.items.length`, the ACTUAL rows
   * on the page being left, never a request limit.
   */
  next: (page: CursorPage | undefined) => void
  /** Go back one page. A no-op on the first page. */
  prev: () => void
  /**
   * Return to the first page. Consumers MUST call this whenever the query's sort
   * key or its filters change: the server 400s a cursor issued under a different
   * sort ("cursor sort key does not match requested sort", internal/api/pagination.go).
   * The hook deliberately does not watch a sort argument - a surface can reset on a
   * sort, on a status filter, on include_archived or on a debounced search box, and a
   * single `sort` dependency does not model that.
   */
  resetPaging: () => void
}

// There is deliberately no `canNext`. Whether a further page exists is a fact about
// the query result, not about the pager, so moving it in would make this hook depend
// on each surface's response shape.
export function useCursorPager(): CursorPager {
  const [cursor, setCursor] = useState('')
  const [stack, setStack] = useState<string[]>([])
  const [startOffset, setStartOffset] = useState(0)
  const [offsets, setOffsets] = useState<number[]>([])

  function next(page: CursorPage | undefined) {
    if (!page?.next_cursor) return
    setStack([...stack, cursor])
    setCursor(page.next_cursor)
    setOffsets([...offsets, startOffset])
    setStartOffset(startOffset + page.items.length)
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
    // Clearing offsets is not observable: offsets is popped only while stack is
    // non-empty, and next pushes exactly one offset per stack entry, so a stale
    // prefix is dead weight the pops never reach. Kept so the state stays honest and
    // a future reader is not left wondering which piece was left behind on purpose.
    setOffsets([])
  }

  // Not wrapped in useCallback on purpose. The surfaces this hook replaced declared
  // these as plain function declarations in the component body, so a fresh identity
  // per render IS the shipped behaviour. Memoizing here would be a change, not a
  // cleanup.
  return { cursor, startOffset, canPrev: stack.length > 0, next, prev, resetPaging }
}
```

- [ ] **Step 2: Run the hook's tests and see them pass**

```bash
cd web
npx vitest run src/lib/useCursorPager.test.ts
```

Expected: PASS, 9 tests.

- [ ] **Step 3: Run the whole suite and see the seven surfaces go RED. This is a free positive control - do not skip it.**

```bash
cd web
npx vitest run
```

Expected: FAIL across the surface suites. Each call site still passes `data?.next_cursor` (a string) as `page`, so `page?.next_cursor` reads `undefined` and `next` is now a permanent no-op: clicking next changes nothing, so `findByText('sched-50')` and its siblings time out.

**Record which suites went red and paste the list into the verification report.** It is direct evidence that the seven wirings are exercised by tests at all, which is the question a zero-diff gate cannot answer. If any of the seven surfaces stays green here, say so: that surface's next button is unconstrained and it is a finding, not a convenience.

### Task 1.3: rewire the seven call sites

Each site loses exactly one argument. Nothing else in any of the seven files changes at this commit. In particular the `disabled={!data?.next_cursor || isPlaceholderData}` expression on each next button is untouched, and the rows variable at each site stays as it is because it still feeds `computePageRange` and the table.

- [ ] **Step 1: Make the seven one-line edits**

`web/src/jobs/JobsPage.tsx:151`

```tsx
                onClick={() => pager.next(data)}
```

`web/src/workers/WorkersPage.tsx:118`

```tsx
                  onClick={() => revokedPager.next(revoked.data)}
```

`web/src/schedules/SchedulesPage.tsx:164`

```tsx
                onClick={() => pager.next(data)}
```

`web/src/admin/users/UsersTab.tsx:166`

```tsx
                onClick={() => pager.next(data)}
```

`web/src/admin/enrollments/EnrollmentsTab.tsx:117`

```tsx
              onClick={() => pager.next(data)}
```

`web/src/admin/reservations/ReservationsTab.tsx:164`

```tsx
              onClick={() => pager.next(data)}
```

`web/src/admin/invites/InvitesTab.tsx:119`

```tsx
              onClick={() => pager.next(data)}
```

- [ ] **Step 2: Run the whole suite and see it green**

```bash
cd web
npx vitest run
```

Expected: PASS. Same "Test Files N passed" total as Task 0 Step 3, and one more test than Task 0 (the new hook test).

- [ ] **Step 3: Typecheck**

```bash
cd web
npx tsc -b
```

Expected: no output. A second argument left at any call site is now an arity error, which is criterion 1.B's other half.

- [ ] **Step 4: Confirm no call site passes a row count**

```bash
git grep -n "pager\.next(\|Pager\.next(" -- web/src
```

Expected: exactly seven lines, none with a comma inside the parentheses.

### Task 1.4: add the guard for the wrong-page substitution

The single-argument form removes one error class and creates another: a caller can no longer pass a wrong SIZE, but on a surface with two paginated-shaped query results it can pass a wrong PAGE, and that typechecks. `WorkersPage` is the only surface in this diff with two such results, and its existing sibling `WorkersPage.pager.test.tsx` would kill that substitution only because its active-workers fixture happens to carry an empty `next_cursor` - which makes the kill a property of a fixture in a frozen file, not of the wiring. This file gives the substitution a dedicated subject that does not depend on that coincidence.

This is a NEW file, so the section 0.1 gate command (`--diff-filter=M`) never sees it and the freeze is untouched. It is green at this commit by construction; its discriminating power is demonstrated by mutation M1 in Task 1.5, not by a RED against HEAD. Say exactly that in the verification report.

- [ ] **Step 1: Create `web/src/workers/WorkersPage.revokedPager.test.tsx`**

```tsx
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { afterEach, expect, test } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import { server } from '../test/setup-helpers'
import { renderWithQuery } from '../test/renderWithQuery'
import { WorkersPage } from './WorkersPage'

// The decommissioned pager must advance on the REVOKED query's page. Both queries on
// this surface return the same page shape, so handing it the active workers page
// instead typechecks. The active fixture therefore carries its own non-empty
// next_cursor: with an empty one the substitution would be caught only by becoming a
// silent no-op, which is a property of the fixture rather than of the wiring. The
// assertion is on the cursor the revoked endpoint actually receives.

const activeWorker = {
  id: 'w1',
  name: 'render-01',
  hostname: 'h',
  cpu_cores: 16,
  ram_gb: 128,
  gpu_count: 1,
  gpu_model: 'RTX 4090',
  os: 'linux',
  max_slots: 4,
  labels: null,
  status: 'online',
  last_seen_at: '2026-06-03T12:00:00Z',
}

function revokedWorker(id: string, name: string) {
  return {
    id,
    name,
    hostname: `${name}-host`,
    cpu_cores: 4,
    ram_gb: 16,
    gpu_count: 0,
    gpu_model: '',
    os: 'linux',
    max_slots: 1,
    labels: null,
    status: 'revoked',
    revoked_at: '2026-01-02T03:04:05Z',
  }
}

function makeRevoked(count: number, startId = 0) {
  return Array.from({ length: count }, (_, i) => revokedWorker(`rw${startId + i}`, `gone-${startId + i}`))
}

afterEach(() => localStorage.clear())

test('the decommissioned next button advances the revoked cursor, not the active workers cursor', async () => {
  const cursors: string[] = []
  server.use(
    http.get('/v1/workers', () =>
      HttpResponse.json({ items: [activeWorker], next_cursor: 'ACTIVE_CUR', total: 1 }),
    ),
  )
  server.use(
    http.get('/v1/workers/stats', () =>
      HttpResponse.json({ online: 1, stale: 0, offline: 0, disabled: 0, total: 1 }),
    ),
  )
  server.use(
    http.get('/v1/workers/revoked', ({ request }) => {
      const cur = new URL(request.url).searchParams.get('cursor') ?? ''
      cursors.push(cur)
      return HttpResponse.json(
        cur === 'REVOKED_CUR'
          ? { items: makeRevoked(13, 50), next_cursor: '', total: 63 }
          : { items: makeRevoked(50, 0), next_cursor: 'REVOKED_CUR', total: 63 },
      )
    }),
  )

  renderWithQuery(
    <MemoryRouter>
      <WorkersPage />
    </MemoryRouter>,
  )
  await screen.findByText('render-01')
  await userEvent.click(screen.getByRole('button', { name: 'Decommissioned' }))
  await screen.findByText('gone-0')

  await userEvent.click(screen.getByRole('button', { name: /next/i }))
  await screen.findByText('gone-50')

  expect(cursors).toContain('REVOKED_CUR')
  expect(cursors).not.toContain('ACTIVE_CUR')
  expect(await screen.findByText(/51-63 of 63/i)).toBeInTheDocument()
})
```

- [ ] **Step 2: Run it and see it pass**

```bash
cd web
npx vitest run src/workers/WorkersPage.revokedPager.test.tsx
```

Expected: PASS, 1 test.

### Task 1.5: the mutation obligation (spec section 1.6)

A zero-diff gate proves you did not weaken the tests. It says nothing about whether the tests constrain the thing you changed. Two mutations, M1 and M2, run now, on the uncommitted working tree.

**Procedure, and why each part of it is there:**

- The mutations run in THIS lane's worktree, which is private to this lane. Never run them in `D:/dev/relay` or in another lane's worktree (`feedback_mutation_testing_needs_isolated_tree`).
- **Save a byte copy of the file before mutating and restore FROM THAT COPY.** Never `git checkout -- <file>` to revert: the working tree holds this commit's uncommitted change, and `git checkout --` would discard the very guard under test (`feedback_never_git_checkout_to_revert_a_mutation`).
- **Verify the mutation actually applied, by comparing against the saved copy**, before believing any "survived" result (`reference_verify_the_mutation_applied`). A silently unapplied edit reports survival.
- **M2 is the harness control.** If M2 also survives, the mutations did not apply and both results are void (`reference_mutation_battery_needs_green_baseline`).

- [ ] **Step 1: Establish the green baseline and a backup directory**

```powershell
cd web; npx vitest run; cd ..
New-Item -ItemType Directory -Force "$env:TEMP\relay-mutation" | Out-Null
```

Bash equivalent for the second line: `mkdir -p /tmp/relay-mutation`.

Expected: suite green. A red baseline voids everything below.

- [ ] **Step 2: M1 - save, mutate, verify applied**

```powershell
Copy-Item web/src/workers/WorkersPage.tsx "$env:TEMP\relay-mutation\WorkersPage.tsx" -Force
```

Now edit `web/src/workers/WorkersPage.tsx:118` from

```tsx
                  onClick={() => revokedPager.next(revoked.data)}
```

to

```tsx
                  onClick={() => revokedPager.next(data)}
```

Verify it applied:

```powershell
Compare-Object (Get-Content "$env:TEMP\relay-mutation\WorkersPage.tsx") (Get-Content web/src/workers/WorkersPage.tsx)
```

Expected: exactly two rows, one `<=` carrying `revokedPager.next(revoked.data)` and one `=>` carrying `revokedPager.next(data)`. If it prints nothing, the edit did not apply and any result below is meaningless.

- [ ] **Step 3: M1 - run the subjects**

```bash
cd web
npx vitest run src/workers/WorkersPage.test.tsx src/workers/WorkersPage.pager.test.tsx src/workers/WorkersPage.revokedPager.test.tsx
```

Expected: RED. `WorkersPage.revokedPager.test.tsx` must fail (the revoked endpoint receives `ACTIVE_CUR`, `gone-50` never renders). `WorkersPage.pager.test.tsx` is expected to fail too, because its active fixture's empty `next_cursor` turns the substitution into a no-op.

Record which files died and which survived. If `WorkersPage.revokedPager.test.tsx` survives, stop and report: the guard added in Task 1.4 does not do its job and the plan is wrong about it.

- [ ] **Step 4: M1 - restore from the copy and prove the restore**

```powershell
Copy-Item "$env:TEMP\relay-mutation\WorkersPage.tsx" web/src/workers/WorkersPage.tsx -Force
Compare-Object (Get-Content "$env:TEMP\relay-mutation\WorkersPage.tsx") (Get-Content web/src/workers/WorkersPage.tsx)
```

Expected: `Compare-Object` prints nothing. Then re-run the three files from Step 3 and see them GREEN. That green is the control that says the restore worked.

- [ ] **Step 5: M2 - save, mutate, verify applied**

```powershell
Copy-Item web/src/lib/useCursorPager.ts "$env:TEMP\relay-mutation\useCursorPager.ts" -Force
```

Now edit the accumulation in `next` from

```ts
    setStartOffset(startOffset + page.items.length)
```

to

```ts
    setStartOffset(startOffset + 50)
```

Verify:

```powershell
Compare-Object (Get-Content "$env:TEMP\relay-mutation\useCursorPager.ts") (Get-Content web/src/lib/useCursorPager.ts)
```

Expected: exactly two rows.

- [ ] **Step 6: M2 - run the subjects**

```bash
cd web
npx vitest run src/lib/useCursorPager.test.ts src/jobs/JobsPage.test.tsx src/schedules/SchedulesPage.test.tsx
```

Expected: RED in all three. In `useCursorPager.test.ts`: the three-page three-size walk and `the offset advances by the page's own row count`. In `JobsPage.test.tsx`: `pagination footer shows correct range for partial last page`. In `SchedulesPage.test.tsx`: `pagination footer shows correct absolute range on partial last page after paging forward`.

If M2 survives anywhere it was predicted to die, the harness is broken and the M1 result is void.

- [ ] **Step 7: M2 - restore from the copy and prove the restore**

```powershell
Copy-Item "$env:TEMP\relay-mutation\useCursorPager.ts" web/src/lib/useCursorPager.ts -Force
Compare-Object (Get-Content "$env:TEMP\relay-mutation\useCursorPager.ts") (Get-Content web/src/lib/useCursorPager.ts)
```

Expected: nothing. Then:

```bash
cd web
npx vitest run
```

Expected: fully GREEN. Nothing from either mutation may reach the commit.

### Task 1.6: gate evidence for commit 1, captured IMMEDIATELY before committing

- [ ] **Step 1: Run GATE-12 against the working tree**

Run the exact command from Task 0 Step 2 (`git diff --numstat <BASE> -- <the twelve files>`).

Expected: **no output.** One rev means "compare that rev to the working tree", so this covers what is about to be committed. If anything prints, a frozen file was edited and the commit must not be made: fix the leak, do not adjust the file.

- [ ] **Step 2: Run the enumeration-free gate against the working tree**

```bash
git diff --numstat --diff-filter=M <BASE> -- web/src | grep -E "\.test\.tsx?$"
```

PowerShell form:

```powershell
git diff --numstat --diff-filter=M <BASE> -- web/src | Select-String -Pattern "\.test\.tsx?$"
```

Expected: **exactly one line**, for `web/src/lib/useCursorPager.test.ts`. `--diff-filter=M` means the new `WorkersPage.revokedPager.test.tsx` correctly does not appear.

Paste both outputs into the verification report, labelled "commit 1". A tip-only run cannot tell commit 3's licensed edit from a commit 1 leak, which is why this is captured here and not at the end.

- [ ] **Step 3: Line-ending and diffstat sanity**

```bash
git diff --stat -- web/src
git ls-files --eol web/src/lib/useCursorPager.ts web/src/lib/useCursorPager.test.ts web/src/jobs/JobsPage.tsx web/src/workers/WorkersPage.tsx web/src/schedules/SchedulesPage.tsx web/src/admin/users/UsersTab.tsx web/src/admin/enrollments/EnrollmentsTab.tsx web/src/admin/reservations/ReservationsTab.tsx web/src/admin/invites/InvitesTab.tsx
```

Expected: the diffstat shows one line changed in each of the seven surfaces plus the two `lib` files rewritten. Every `git ls-files --eol` row reads `i/lf`. A wildly larger insertion count than intended means a line-ending or encoding accident, not a code change.

### Task 1.7: commit 1

- [ ] **Step 1: Confirm `web/dist` is clean, then stage with an explicit pathspec**

```bash
git status --porcelain web/dist
git add web/src/lib/useCursorPager.ts web/src/lib/useCursorPager.test.ts \
  web/src/jobs/JobsPage.tsx web/src/workers/WorkersPage.tsx \
  web/src/schedules/SchedulesPage.tsx web/src/admin/users/UsersTab.tsx \
  web/src/admin/enrollments/EnrollmentsTab.tsx \
  web/src/admin/reservations/ReservationsTab.tsx \
  web/src/admin/invites/InvitesTab.tsx \
  web/src/workers/WorkersPage.revokedPager.test.tsx
git status --porcelain
```

Expected from `git status --porcelain`: the ten paths above staged, nothing else staged, and in particular nothing under `web/dist/`.

- [ ] **Step 2: Write the commit message to a file and commit with `-F`**

Write this to `commit1.txt` in your scratchpad (a long inline `-m` heredoc is fragile across shells):

```
refactor(web): useCursorPager.next takes the page it is leaving

next(nextCursor, pageSize) becomes next(page). The hook derives both the cursor
and the row count from one object, so a caller can no longer state a page size
that disagrees with the page it is leaving - the value both shipped pagination
footer bugs were about, and one the 2026-08-14 mutation sweep proved
unconstrained at two of the seven call sites.

CursorPage.next_cursor is REQUIRED, which refutes the backlog item's own sketch.
All seven response types declare the field required. With it optional, a response
that renames or drops next_cursor still satisfies the parameter, next becomes a
silent permanent no-op and the next button is permanently disabled with nothing
red. Required makes that rename a compile error at all seven call sites.

The single-argument form removes the wrong-size error and admits a wrong-page
one, so WorkersPage.revokedPager.test.tsx pins that the decommissioned pager
advances on the revoked query's page. Mutation results for the wrong-page
substitution and for dropping the accumulation are in the PR body.

The hook's comment blocks lose their file censuses, counts and provenance under
the CLAUDE.md comment policy; every hazard they carried survives.
```

```bash
git commit -F <path-to>/commit1.txt
```

- [ ] **Step 3: Record the post-commit evidence**

```bash
git rev-parse HEAD
git diff --numstat --diff-filter=M <BASE> HEAD -- web/src | grep -E "\.test\.tsx?$"
```

Expected: one line, `web/src/lib/useCursorPager.test.ts`. Paste the SHA and the output into the verification report.

---

## Commit 2 - one generic `toggleSort`

Closes: `idea-2026-08-14-toggle-sort-generic` (the conductor runs the close, not the engineer).

**Files:**
- Create: `web/src/lib/toggleSort.ts`
- Create: `web/src/lib/toggleSort.test.ts`
- Modify: `web/src/workers/WorkersPage.tsx:8` (drop the now-unused `type SortField` import), `:10` area (add the import), `:23-28` (delete)
- Modify: `web/src/admin/users/UsersTab.tsx:6` area (add the import), `:16-23` (delete)
- Modify: `web/src/admin/enrollments/EnrollmentsTab.tsx:4` area (add the import), `:14-21` (delete)
- Modify: `web/src/admin/reservations/ReservationsTab.tsx:5` area (add the import), `:20-27` (delete)
- Modify: `web/src/admin/invites/InvitesTab.tsx:4` area (add the import), `:14-31` (delete)

`web/src/lib/` is the established home for behaviour-only helpers with a `.test` sibling (`pageRange.ts`, `useNow.ts`, `useDebouncedValue.ts`, `useCursorPager.ts`), and naming the file after its export follows `pageRange.ts`.

### Task 2.1: write the helper's tests first

- [ ] **Step 1: Create `web/src/lib/toggleSort.test.ts`**

```ts
import { expect, test } from 'vitest'
import { toggleSort } from './toggleSort'

test('clicking the active ascending column flips it to descending', () => {
  expect(toggleSort('name', 'name')).toBe('-name')
})

test('clicking the active descending column flips it back to ascending', () => {
  expect(toggleSort('name', '-name')).toBe('name')
})

test('clicking a different column selects it ascending from an ascending current', () => {
  expect(toggleSort('email', 'name')).toBe('email')
})

// Discriminates an implementation that carries the leading minus sign across a column
// change: that returns '-email' here while still returning 'email' from the ascending
// current above, so only this input separates the two.
test('clicking a different column selects it ascending from a descending current', () => {
  expect(toggleSort('email', '-name')).toBe('email')
})

// Discriminates equality on the stripped string from a startsWith comparison:
// 'created_at' starts with 'created', so a startsWith treats the column as already
// active and returns '-created'.
test('a field whose name is a prefix of the current field is not treated as active', () => {
  expect(toggleSort('created', 'created_at')).toBe('created')
})
```

- [ ] **Step 2: Run it and see it fail**

```bash
cd web
npx vitest run src/lib/toggleSort.test.ts
```

Expected: FAIL to collect, with a resolution error for `./toggleSort`.

### Task 2.2: prove tests 4 and 5 discriminate, with a deliberately wrong helper

Plan-supplied test bodies are guesses until they have been seen to fail for the reason claimed (`reference_plan_supplied_tests_untrusted`). Tests 1 to 3 are satisfied by an obviously wrong implementation; this step shows that tests 4 and 5 are not.

- [ ] **Step 1: Create `web/src/lib/toggleSort.ts` with the WRONG body, temporarily**

```ts
export function toggleSort<S extends string>(field: string, current: S): S {
  const stripped = current.replace('-', '')
  const next = stripped.startsWith(field)
    ? current.startsWith('-')
      ? field
      : `-${field}`
    : current.startsWith('-')
      ? `-${field}`
      : field
  return next as S
}
```

- [ ] **Step 2: Run and confirm exactly tests 4 and 5 fail**

```bash
cd web
npx vitest run src/lib/toggleSort.test.ts
```

Expected: 3 passed, 2 failed. `clicking a different column selects it ascending from a descending current` fails with `expected '-email' to be 'email'`, and `a field whose name is a prefix of the current field is not treated as active` fails with `expected '-created' to be 'created'`.

If a different pair fails, stop and re-read the wrong body: the negative control is not doing what the plan says.

### Task 2.3: ship the real helper

The single-return shape is deliberate. Written as an `if`/`return` pair the generic needs TWO casts, because `field` is a plain `string` and `string` is not assignable to `S` in either branch. The single-return form has exactly one cast, and gives the hazard comment one place to sit.

- [ ] **Step 1: Replace `web/src/lib/toggleSort.ts` with this**

```ts
// The cast asserts on behalf of the caller. `field` is a plain string, so the returned
// value is a member of S only while the field argument is drawn from the union S is
// built over; a field that is not reports as S and is not. toggleSort.test.ts pins the
// four transitions and the prefix case, not that property.
export function toggleSort<S extends string>(field: string, current: S): S {
  const next =
    current.replace('-', '') === field
      ? current.startsWith('-')
        ? field
        : `-${field}`
      : field
  return next as S
}
```

- [ ] **Step 2: Run and see all five pass**

```bash
cd web
npx vitest run src/lib/toggleSort.test.ts
```

Expected: PASS, 5 tests.

### Task 2.4: delete the five copies and their comment blocks

Every one of the comment blocks is a cross-file reference whose subject stops existing, so all of them are deleted rather than rewritten. `InvitesTab.tsx`'s block additionally carries a count of other files, its own change history, and review narrative, which is three separate comment-policy violations in one block. Note that `WorkersPage.tsx`'s copy carries no comment block at all, so there are five declarations and four blocks.

- [ ] **Step 1: `web/src/workers/WorkersPage.tsx`**

Change the import on line 8 from

```tsx
import { WorkersTable, type SortField } from './WorkersTable'
```

to

```tsx
import { WorkersTable } from './WorkersTable'
```

`SortField` was referenced only by the local `toggleSort`, and `web/tsconfig.json` sets `noUnusedLocals: true`, so leaving the import is a `tsc -b` error. The `onSort` callback parameter is contextually typed by `WorkersTable`'s prop and needs no annotation.

Add the helper import immediately after the `pageRange` import (line 10), keeping the `lib` group alphabetical:

```tsx
import { computePageRange } from '../lib/pageRange'
import { toggleSort } from '../lib/toggleSort'
import { useCursorPager } from '../lib/useCursorPager'
```

Delete lines 23 to 28 in full, that is the whole `function toggleSort(field: SortField, current: WorkerSort): WorkerSort { ... }` declaration and the blank line that followed it. Leave `loadView` and `countByStatus` alone. `onSort={(f) => setSort((cur) => toggleSort(f, cur))}` on line 220 is unchanged.

- [ ] **Step 2: `web/src/admin/users/UsersTab.tsx`**

Add the import after the `pageRange` import (line 6):

```tsx
import { computePageRange } from '../../lib/pageRange'
import { toggleSort } from '../../lib/toggleSort'
import { useCursorPager } from '../../lib/useCursorPager'
```

Delete lines 16 to 23, that is this comment block and the declaration under it:

```tsx
// Same shape as WorkersPage's toggleSort: clicking the active column flips its
// direction, clicking another column selects it ascending.
function toggleSort(field: UserSortField, current: UserSort): UserSort {
  if (current.replace('-', '') === field) {
    return (current.startsWith('-') ? field : `-${field}`) as UserSort
  }
  return field
}
```

`UserSortField` and `UserSort` both stay in the type import: `pickSort(field: UserSortField)` and `useState<UserSort>` still use them.

- [ ] **Step 3: `web/src/admin/enrollments/EnrollmentsTab.tsx`**

Add the import after the `pageRange` import (line 4):

```tsx
import { computePageRange } from '../../lib/pageRange'
import { toggleSort } from '../../lib/toggleSort'
import { useCursorPager } from '../../lib/useCursorPager'
```

Delete lines 14 to 21, comment block included:

```tsx
// Same shape as UsersTab's toggleSort (web/src/admin/users/UsersTab.tsx): clicking
// the active column flips its direction, clicking the other selects it ascending.
function toggleSort(field: EnrollmentSortField, current: EnrollmentSort): EnrollmentSort {
  if (current.replace('-', '') === field) {
    return (current.startsWith('-') ? field : `-${field}`) as EnrollmentSort
  }
  return field
}
```

- [ ] **Step 4: `web/src/admin/reservations/ReservationsTab.tsx`**

Add the import after the `pageRange` import (line 5):

```tsx
import { computePageRange } from '../../lib/pageRange'
import { toggleSort } from '../../lib/toggleSort'
import { useCursorPager } from '../../lib/useCursorPager'
```

Delete lines 20 to 27, comment block included:

```tsx
// Same shape as EnrollmentsTab's toggleSort (EnrollmentsTab.tsx): clicking the
// active column flips its direction, clicking another selects it ascending.
function toggleSort(field: ReservationSortField, current: ReservationSort): ReservationSort {
  if (current.replace('-', '') === field) {
    return (current.startsWith('-') ? field : `-${field}`) as ReservationSort
  }
  return field
}
```

Leave `confirmDeleteBody` and its comment block (lines 29 to 47 at HEAD) completely alone. It is the subject of commit 4 and must not move here.

- [ ] **Step 5: `web/src/admin/invites/InvitesTab.tsx`**

Add the import after the `pageRange` import (line 4):

```tsx
import { computePageRange } from '../../lib/pageRange'
import { toggleSort } from '../../lib/toggleSort'
import { useCursorPager } from '../../lib/useCursorPager'
```

Delete lines 14 to 31 in full, which is this entire block:

```tsx
// Same shape as EnrollmentsTab's toggleSort (EnrollmentsTab.tsx): clicking
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
  if (current.replace('-', '') === field) {
    return (current.startsWith('-') ? field : `-${field}`) as InviteSort
  }
  return field
}
```

### Task 2.5: prove the five surfaces are unchanged

The five surfaces drive sorting through their rendered table headers, so a helper that is not behaviour-identical reddens them. That is the positive control this commit gets for free and commit 1 did not.

- [ ] **Step 1: Run the five surfaces' suites**

```bash
cd web
npx vitest run src/workers/WorkersPage.test.tsx src/admin/users/UsersTab.test.tsx src/admin/enrollments/EnrollmentsTab.test.tsx src/admin/reservations/ReservationsTab.test.tsx src/admin/invites/InvitesTab.test.tsx
```

Expected: PASS, with the same test counts as Task 0 Step 3.

- [ ] **Step 2: Run the whole suite**

```bash
cd web
npx vitest run
```

Expected: PASS.

- [ ] **Step 3: Typecheck**

```bash
cd web
npx tsc -b
```

Expected: no output. This is where an unused `SortField` import or a lost narrow type would surface.

- [ ] **Step 4: The three greps behind criteria 2.A, 2.D and 2.F**

```bash
git grep -n "function toggleSort" -- web/src
git grep -n "FIFTH" -- web/src
git grep -n "ts-expect-error" -- web/src/workers/WorkersPage.tsx web/src/admin/users/UsersTab.tsx web/src/admin/enrollments/EnrollmentsTab.tsx web/src/admin/reservations/ReservationsTab.tsx web/src/admin/invites/InvitesTab.tsx
```

Expected: the first prints exactly one line, `web/src/lib/toggleSort.ts`. The second and third print nothing.

### Task 2.6: gate evidence for commit 2, captured IMMEDIATELY before committing

- [ ] **Step 1: Run GATE-12 against the working tree**

Run the exact command from Task 0 Step 2.

Expected: **no output.**

- [ ] **Step 2: Run the enumeration-free gate against the working tree**

```bash
git diff --numstat --diff-filter=M <BASE> -- web/src | grep -E "\.test\.tsx?$"
```

Expected: **exactly one line**, still for `web/src/lib/useCursorPager.test.ts`, which is commit 1's licensed change carried forward. Nothing new may appear. `useCursorPager.test.ts` was licensed at commit 1 only; this commit adds no exception of its own.

Paste both outputs into the verification report, labelled "commit 2".

- [ ] **Step 3: Line-ending and diffstat sanity**

```bash
git diff --stat HEAD -- web/src
git ls-files --eol web/src/workers/WorkersPage.tsx web/src/admin/users/UsersTab.tsx web/src/admin/enrollments/EnrollmentsTab.tsx web/src/admin/reservations/ReservationsTab.tsx web/src/admin/invites/InvitesTab.tsx
```

Expected: five files with deletions clearly outnumbering insertions (a comment block plus a five-line function out, one import line in, and for `WorkersPage.tsx` one import line changed). Every `--eol` row reads `i/lf`.

### Task 2.7: commit 2

- [ ] **Step 1: Stage with an explicit pathspec**

```bash
git status --porcelain web/dist
git add web/src/lib/toggleSort.ts web/src/lib/toggleSort.test.ts \
  web/src/workers/WorkersPage.tsx web/src/admin/users/UsersTab.tsx \
  web/src/admin/enrollments/EnrollmentsTab.tsx \
  web/src/admin/reservations/ReservationsTab.tsx \
  web/src/admin/invites/InvitesTab.tsx
git status --porcelain
```

Expected: seven paths staged, nothing else, nothing under `web/dist/`.

- [ ] **Step 2: Write the message and commit**

Write to `commit2.txt` in your scratchpad:

```
refactor(web): one generic toggleSort in web/src/lib

Five identical local copies become one helper. The field parameter is a plain
string and the current sort is the generic, so there is exactly one cast, in the
helper, where it can be documented and tested.

Typing the field against each module's sort-field union was considered and is
refuted by WorkersPage: its column union is SortField in WorkersTable.tsx, not a
WorkerSortField in api.ts, and WorkerSort additionally carries created_at, which
has no column header. A template-literal generic relating the two would need
either a widened SortField or a narrowed WorkerSort, both behaviour changes to a
surface whose test file is frozen for this branch. The residual weakness - a
typo'd field produces an S-typed value that is not a member of S - is exactly the
weakness the five local copies already had, unchanged.

All the comment blocks are deleted rather than rewritten: each was a cross-file
reference whose subject stops existing, and the invites one also carried a count
of other files, its own change history and review narrative.
```

```bash
git commit -F <path-to>/commit2.txt
```

- [ ] **Step 3: Record the post-commit evidence**

```bash
git rev-parse HEAD
git diff --numstat --diff-filter=M <BASE> HEAD -- web/src | grep -E "\.test\.tsx?$"
```

Expected: one line, `web/src/lib/useCursorPager.test.ts`. Paste the SHA and the output into the verification report.

---

## Commit 3 - the schedules footer's row range

Closes: `bug-2026-08-14-schedules-footer-range-not-localized` (the conductor runs the close, not the engineer).

**Files:**
- Modify: `web/src/schedules/SchedulesPage.tsx:92` (add `rangeText`) and `:149` (use it)
- Modify: `web/src/schedules/SchedulesPage.test.tsx` (append two tests after line 260)

`web/src/schedules/SchedulesPage.test.tsx` is unfrozen for exactly this commit and no other. Every other test file that existed at the merge base stays frozen for the rest of the branch.

**Scope fence.** `total` at this surface is `data?.total ?? schedules.length`, which differs from the six siblings' `?? 0`. That difference is deliberate (the ENABLED/PAUSED strip reads the same `total`) and is NOT in scope. Do not harmonize it. Do not touch any other surface: the option of moving all seven to an explicit en-US locale was weighed and rejected in spec Decision 3.2, on four grounds including that a bare `toLocaleString()` on a user-facing count is the correct product behaviour for a non-US reader.

### Task 3.1: write the two failing tests

They go in `SchedulesPage.test.tsx` beside the three existing footer tests, because that is where the footer's coverage already lives and because this commit's licence to edit that one file is the reason it is sequenced third.

- [ ] **Step 1: Insert both tests immediately after line 260 of `web/src/schedules/SchedulesPage.test.tsx`**

That is after the closing `})` of `pagination footer restores prior range when paging back` and before `test('clicking Disable PATCHes the schedule', ...)`. Insert only; delete nothing.

```tsx
test('the footer thousands-separates a four-digit total', async () => {
  server.use(
    http.get('/v1/scheduled-jobs', () =>
      HttpResponse.json({ items: makeSchedules(50), next_cursor: '', total: 2341 }),
    ),
  )
  renderWithQuery(
    <MemoryRouter>
      <SchedulesPage />
    </MemoryRouter>,
  )
  await screen.findByText('sched-0')
  expect(await screen.findByText('1-50 of 2,341')).toBeInTheDocument()
})

// A literal, never (2341).toLocaleString(): comparing the component's output against
// the same call it makes would pass on any runner, including one with no group
// separator, and assert nothing.
//
// The total is 1234 rather than 0 so the assertion discriminates on both axes at once.
// A range with no zero-rows branch reads `0-0 of 1,234`, a zero-rows branch that does
// not format reads `0 of 1234`, and today's output is `0-0 of 1234`. All three differ
// from `0 of 1,234`, so a half-applied change cannot pass. A total of 0 would leave
// the formatting half unpinned.
test('the footer renders a bare count and no range when the page has no rows', async () => {
  server.use(
    http.get('/v1/scheduled-jobs', () =>
      HttpResponse.json({ items: [], next_cursor: '', total: 1234 }),
    ),
  )
  renderWithQuery(
    <MemoryRouter>
      <SchedulesPage />
    </MemoryRouter>,
  )
  await screen.findByText('No schedules yet.')
  expect(await screen.findByText('0 of 1,234')).toBeInTheDocument()
})
```

The empty case does render a footer: `SchedulesTable` returns the "No schedules yet." panel followed by the footer node when `schedules.length === 0`.

- [ ] **Step 2: Run them and see them fail for the right reason**

```bash
cd web
npx vitest run src/schedules/SchedulesPage.test.tsx
```

Expected: 2 failed, the rest passed. Both fail as `TestingLibraryElementError: Unable to find an element with the text: ...`. The rendered text today is `1-50 of 2341` and `0-0 of 1234`.

If either passes, stop: the assertion is not discriminating and the source change is not the change this commit claims.

### Task 3.2: change the footer

- [ ] **Step 1: Add `rangeText` in `web/src/schedules/SchedulesPage.tsx`**

Line 92 at HEAD is `const { x, y } = computePageRange(pager.startOffset, schedules.length)`. Leave it, and add the const directly beneath it, so lines 91 to 93 become:

```tsx
  const total = data?.total ?? schedules.length
  const { x, y } = computePageRange(pager.startOffset, schedules.length)
  const rangeText =
    schedules.length === 0
      ? `0 of ${total.toLocaleString()}`
      : `${x.toLocaleString()}-${y.toLocaleString()} of ${total.toLocaleString()}`
  const actionError = (runNow.error ?? setEnabled.error) as Error | null
```

This is the same shape the other six surfaces already use.

- [ ] **Step 2: Use it in the footer**

Change line 149 from

```tsx
              SHOWING <span className="text-fg">{x}-{y} of {total}</span>
```

to

```tsx
              SHOWING <span className="text-fg">{rangeText}</span>
```

- [ ] **Step 3: Run the file and see it green**

```bash
cd web
npx vitest run src/schedules/SchedulesPage.test.tsx
```

Expected: PASS. In particular the three existing footer tests, which assert totals of 120 and 63, must still pass untouched: both are under four digits, so they render identically before and after.

- [ ] **Step 4: Run the whole suite and typecheck**

```bash
cd web
npx vitest run
npx tsc -b
```

Expected: PASS, no tsc output.

### Task 3.3: gate evidence for commit 3, captured IMMEDIATELY before committing

- [ ] **Step 1: Run GATE-12 against the working tree**

Run the exact command from Task 0 Step 2.

Expected: **exactly one line**, for `web/src/schedules/SchedulesPage.test.tsx`, and its numstat must show **insertions and zero deletions** (criterion 3.E). A non-zero deletion count means one of the three existing footer tests was edited, which is the finding: it would mean the change is not the formatting change it claims to be.

- [ ] **Step 2: Run the enumeration-free gate against the working tree**

```bash
git diff --numstat --diff-filter=M <BASE> -- web/src | grep -E "\.test\.tsx?$"
```

Expected: **exactly two lines**: `web/src/lib/useCursorPager.test.ts` (commit 1's licence, carried forward) and `web/src/schedules/SchedulesPage.test.tsx` (this commit's licence).

Paste both outputs into the verification report, labelled "commit 3".

- [ ] **Step 3: Confirm no `toLocaleString` round trip and no other surface moved**

```bash
git grep -n "toLocaleString" -- web/src/schedules/SchedulesPage.test.tsx
git diff --name-only HEAD -- web/src
```

Expected: the first prints nothing (criterion 3.C). The second prints exactly two paths, `web/src/schedules/SchedulesPage.tsx` and `web/src/schedules/SchedulesPage.test.tsx`.

- [ ] **Step 4: Line endings**

```bash
git ls-files --eol web/src/schedules/SchedulesPage.tsx web/src/schedules/SchedulesPage.test.tsx
```

Expected: both read `i/lf`.

### Task 3.4: commit 3

- [ ] **Step 1: Stage with an explicit pathspec**

```bash
git status --porcelain web/dist
git add web/src/schedules/SchedulesPage.tsx web/src/schedules/SchedulesPage.test.tsx
git status --porcelain
```

- [ ] **Step 2: Write the message and commit**

Write to `commit3.txt` in your scratchpad:

```
fix(web): the schedules footer formats its row range like the other six

SchedulesPage rendered {x}-{y} of {total} raw and had no zero-rows branch, so a
four-digit total lost its separator and an empty page read "0-0 of N". It now
builds the same rangeText the other six paginated surfaces build.

The empty case is a real rendered-text change, not a nicety: SchedulesTable
renders the footer beneath its "No schedules yet." panel, so a zero-row schedules
page shipped "0-0 of N".

The alternative of moving all seven surfaces to an explicit en-US locale was
rejected. It invents a third convention while fixing a bug whose content is that
one surface deviates from six; a bare toLocaleString on a user-facing count is
the correct product behaviour for a reader whose locale groups differently; and
it would unfreeze five more test files on this branch. The two new assertions are
locale-dependent by construction, exactly like the eight that already ship. That
is deliberate, and the determinism question is filed as its own item rather than
answered here - read these tests as a deferral, not an endorsement.
```

```bash
git commit -F <path-to>/commit3.txt
```

- [ ] **Step 3: Record the post-commit evidence**

```bash
git rev-parse HEAD
git diff --numstat --diff-filter=M <BASE> HEAD -- web/src | grep -E "\.test\.tsx?$"
```

Expected: two lines. Paste the SHA and the output into the verification report.

---

## Commit 4 - three stale citations become symbol and phrase references

Closes: `bug-2026-08-14-stale-citations-in-gate-frozen-test-files` (the conductor runs the close, not the engineer).

**Files:**
- Modify: `web/src/admin/reservations/ReservationsTab.test.tsx:136-140` (comment only)
- Modify: `web/src/admin/enrollments/EnrollmentsTab.test.tsx:262-263` (comment only)
- Modify: `web/CLAUDE.md` (append one paragraph)

Two test files are unfrozen for exactly this commit, and only for comment lines. No assertion, no fixture, no import moves.

**The item's prescribed symbol does not exist.** The item says to cite `deleteWarning()`. The function that builds the dialog body in `ReservationsTab.tsx` is `confirmDeleteBody` (line 36 at HEAD). Writing `deleteWarning()` would replace a stale line number with a symbol that resolves to nothing, which is strictly worse than a line number that used to be true. Cite `confirmDeleteBody`.

**The enrollments target is one of two conventions.** `UsersTab` has two distinct reset conventions and `ReservationsTab.tsx` already cites the other one (`resetPassword.reset()` before `setResetting`). The enrollments test is about reopening the create PANEL, so its target is `create.reset()` (`UsersTab.tsx:207`, the `+ Create user` toggle, and `:221`, the form's Cancel). The replacement must name `create.reset()` explicitly or it points at the wrong one of two.

**Not in scope, deliberately.** `EnrollmentsTab.test.tsx` carries a fourth line-number citation, `internal/api/pagination.go:272-286`. It was checked and is accurate at HEAD, so it is not an instance of this bug. It is an instance of a broader shape, and folding it in would widen a three-citation fix into an unbounded sweep. Leave it.

### Task 4.1: replace the two ReservationsTab citations

Both live in one comment block, so this is one edit.

- [ ] **Step 1: Replace lines 136 to 140 of `web/src/admin/reservations/ReservationsTab.test.tsx`**

From:

```tsx
  // Positive control on the same instrument, on a phrase that exists ONLY in the
  // dialog body (ReservationsTab.tsx:45). The previous control was
  // /general dispatch pool/i, which also matches the tab's own footnote at
  // ReservationsTab.tsx:253 - so it stayed green under exactly the scope error it
  // existed to catch.
```

To:

```tsx
  // Positive control on the same instrument, on a phrase carried only by the ACTIVE
  // branch of confirmDeleteBody in ReservationsTab.tsx. A control phrase must not
  // also appear in the tab's own explanatory footnote: one that does stays green
  // under exactly the scope error this control exists to catch.
```

Nothing else in the file changes. Line 141's assertion, `expect(html).toMatch(/tasks already running on them are unaffected/i)`, is untouched.

Two notes on the replacement. The phrase "the tab's own explanatory footnote" is a claim that there is one, and there is, in the same file the reader already has open, which is what makes it checkable rather than a claim about the complement. Do not widen it to something like "the only footnote in web/src". And the reasoning is kept but restated as a rule rather than as history, per the comment policy: what a previous control was is change history, while what a control phrase must not do is the hazard.

### Task 4.2: replace the EnrollmentsTab citation

- [ ] **Step 1: Replace lines 262 to 263 of `web/src/admin/enrollments/EnrollmentsTab.test.tsx`**

From:

```tsx
  // Reopening the panel clears the stale error - the reset()-before-reopen
  // convention from UsersTab.tsx:238-245.
```

To:

```tsx
  // Reopening the panel clears the stale error - the create.reset()-before-reopen
  // convention in UsersTab.
```

### Task 4.3: verify the replacements resolve, and that only comments moved

Each of the three new comments is a fresh assertion and must be verified as if new, not merely checked as a faithful edit of the old one (`reference_correcting_a_uniqueness_claim`).

- [ ] **Step 1: Confirm both symbols exist (criterion 4.B, the one the item's own prescription would have failed)**

```bash
git grep -n "confirmDeleteBody" -- web/src/admin/reservations/ReservationsTab.tsx
git grep -n "create.reset()" -- web/src/admin/users/UsersTab.tsx
git grep -n "deleteWarning" -- web/src
```

Expected: the first prints the declaration and the call site. The second prints two lines (the `+ Create user` toggle and the form's Cancel). The third prints **nothing** - if it prints anything, the wrong symbol was written into a comment.

- [ ] **Step 2: Confirm the three old citations are gone (criterion 4.A)**

```bash
git grep -n "ReservationsTab.tsx:45\|ReservationsTab.tsx:253\|UsersTab.tsx:238-245" -- web/src
```

Expected: nothing.

- [ ] **Step 3: Confirm the diff is comment-only (criterion 4.C)**

```bash
git diff HEAD -- web/src
```

Read it line by line. Every changed line must begin with `//` or sit inside a comment block. State this explicitly in the verification report: "the suite is green" cannot distinguish a comment edit from an assertion edit.

- [ ] **Step 4: Run the two files, then the whole suite**

```bash
cd web
npx vitest run src/admin/reservations/ReservationsTab.test.tsx src/admin/enrollments/EnrollmentsTab.test.tsx
npx vitest run
npx tsc -b
```

Expected: PASS, with the same "Test Files N passed / Tests M passed" totals as the end of commit 3 (criterion 4.E: a comment edit changes no test count).

### Task 4.4: the conventions note in `web/CLAUDE.md`

Chosen over a bullet in the root `CLAUDE.md`: `web/CLAUDE.md` is the frontend conventions doc and lands where anyone working in `web/` will read it, while promoting the rule repo-wide is a change to the project's instruction file that pairs with the sweep of surviving citations and belongs to that follow-up.

- [ ] **Step 1: Append this paragraph to `web/CLAUDE.md`**

Append after the existing final paragraph (which ends `...serves the previous bundle.`), separated by one blank line:

```markdown
**Cite a symbol or a phrase, not a file and a line.** A `File.tsx:123` reference inside a comment
is invalidated by any unrelated edit above line 123, and nothing reports it: no test covers a
comment and no compiler checks one, so it quietly becomes a pointer at the wrong code. Name the
function, the constant, or a distinctive phrase from the text instead - a symbol travels with its
file, and a rename is visible to a search. Where the target sits in the same file as the comment, a
phrase the reader can find without leaving the file is enough.
```

- [ ] **Step 2: Check the encoding and line endings of the edited doc**

```bash
git diff --stat -- web/CLAUDE.md
git ls-files --eol web/CLAUDE.md
```

Expected: the diffstat shows roughly seven insertions and zero deletions, matching the paragraph you wrote, and `--eol` reads `i/lf`. The paragraph is pure ASCII; if the diff shows a much larger insertion count, a line-ending rewrite happened and must be undone before committing.

### Task 4.5: gate evidence for commit 4, captured IMMEDIATELY before committing

- [ ] **Step 1: Run GATE-12 against the working tree**

Run the exact command from Task 0 Step 2.

Expected: **exactly three lines**: `web/src/schedules/SchedulesPage.test.tsx` (commit 3's licence, carried forward), `web/src/admin/reservations/ReservationsTab.test.tsx` and `web/src/admin/enrollments/EnrollmentsTab.test.tsx`.

- [ ] **Step 2: Run the enumeration-free gate against the working tree**

```bash
git diff --numstat --diff-filter=M <BASE> -- web/src | grep -E "\.test\.tsx?$"
```

Expected: **exactly four lines**: the three above plus `web/src/lib/useCursorPager.test.ts`.

Paste both outputs into the verification report, labelled "commit 4".

### Task 4.6: commit 4

- [ ] **Step 1: Stage with an explicit pathspec**

```bash
git status --porcelain web/dist
git add web/src/admin/reservations/ReservationsTab.test.tsx \
  web/src/admin/enrollments/EnrollmentsTab.test.tsx \
  web/CLAUDE.md
git status --porcelain
```

- [ ] **Step 2: Write the message and commit**

Write to `commit4.txt` in your scratchpad:

```
docs(web): three stale line citations become symbol and phrase references

All three were re-verified stale: the dialog-body phrase moved to :46, the
footnote citation ran past EOF (the file is 243 lines and the footnote begins at
:223), and the UsersTab reset convention is at :207 and :221, not :238-245.

The backlog item prescribed citing deleteWarning(), and no such symbol exists.
The function is confirmDeleteBody. Writing the prescribed name would have
replaced a stale line number with one that resolves to nothing.

UsersTab has two reset conventions and ReservationsTab.tsx already cites the
other one, so the enrollments comment names create.reset() explicitly.

Comment-only diffs in both test files; no assertion, fixture or import moved.
web/CLAUDE.md gains the rule these three are instances of.
```

```bash
git commit -F <path-to>/commit4.txt
```

- [ ] **Step 3: Record the post-commit evidence**

```bash
git rev-parse HEAD
git diff --numstat --diff-filter=M <BASE> HEAD -- web/src | grep -E "\.test\.tsx?$"
git diff <commit-3-sha> HEAD -- web/src
```

Expected: four lines from the second command. The third command is the comment-only review from Task 4.3 Step 3 in its final form; confirm again that every changed line is a comment line, and say so in the report.

---

## Task 5: the whole-branch gate, before the PR

- [ ] **Step 1: Whole suite**

```bash
cd web
npx vitest run
```

Expected: PASS. Compare "Test Files N passed / Tests M passed" against Task 0 Step 3. The branch adds **two test files** (`web/src/workers/WorkersPage.revokedPager.test.tsx` and `web/src/lib/toggleSort.test.ts`) and **nine tests** (1 new hook test, 1 guard test, 5 helper tests, 2 schedules footer tests; none removed).

- [ ] **Step 2: Typecheck**

```bash
cd web
npx tsc -b
```

Expected: no output.

- [ ] **Step 3: Production build**

```bash
cd web
npm run build
```

Expected: `tsc -b` clean then a successful `vite build`.

- [ ] **Step 4: Discard the rebuilt bundle. It is tracked but not maintained per PR.**

```bash
git checkout -- web/dist/
git status --porcelain
```

Expected: `git status --porcelain` prints nothing. If `web/dist` still shows as modified, repeat the checkout. Nothing under `web/dist/` may reach this branch.

- [ ] **Step 5: The twelve-file corroborating table, base to tip**

Run GATE-12 one final time, now with an explicit tip: `git diff --numstat <BASE> HEAD -- <the twelve files>`.

Expected: exactly three lines, and only these:

| File | Expected | Licensed at |
|---|---|---|
| `web/src/schedules/SchedulesPage.test.tsx` | insertions only, zero deletions | commit 3 |
| `web/src/admin/reservations/ReservationsTab.test.tsx` | small, comment lines only | commit 4 |
| `web/src/admin/enrollments/EnrollmentsTab.test.tsx` | small, comment lines only | commit 4 |

The other nine must not appear at all.

- [ ] **Step 6: Assemble the verification report**

It must contain, at minimum:

1. Four per-commit gate outputs, both forms, labelled by commit SHA. Not a single tip-only run: a tip-only run cannot distinguish commit 3's licensed edit from a commit 1 leak.
2. The list of suites that went RED at Task 1.2 Step 3, which is the free positive control that the seven wirings are exercised.
3. M1 and M2 results: what was mutated, the `Compare-Object` output proving each applied, which tests died, and the restore proof. If M1 survived anywhere, say so plainly and name what the guard file caught instead.
4. The greps behind criteria 1.B, 2.A, 2.D, 2.F, 3.C, 4.A and 4.B, with their outputs.
5. The statement that commit 4's diff was read line by line and is comment-only.
6. The `git status --porcelain` proof that `web/dist` is clean.

- [ ] **Step 7: Open the PR**

Use `gh pr create --body-file <path>` with the body written to a scratchpad file. A long inline `--body` heredoc trips the classifier. The body should carry the four commit summaries, the gate table, and the M1/M2 results.

**Do not run `/backlog close`.** The four items in spec section 5.1 are the conductor's to close after the merge, with the `git mv` into `docs/backlog/closed/` that the close command performs. Resolutions must record: that `next_cursor` was made required (refuting the item's sketch) and why; that `WorkersPage`'s field union is `SortField` in `WorkersTable.tsx`, which is what ruled out the template-literal generic, and that "five casts become one" is one only in the single-return shape; that the one-file footer option was taken over the seven-file explicit-locale one; and that the item's `deleteWarning()` does not exist and the real symbol is `confirmDeleteBody`.

---

## Phase 4 note for the conductor

There is no Go diff on this branch, so there is no Go lane to run and no `make generate` step anywhere. The four proposed follow-up items in spec section 5.2 are proposals only and are the conductor's to file via `/backlog`; nothing in this plan files them.

A browser lane is available but is not required by this plan. `make test-e2e` needs a Postgres at `postgres://relay:relay@127.0.0.1:5432` and Playwright browsers installed. The only user-visible rendered-text change on this branch is the schedules footer, which is text rather than layout and is pinned by two jsdom assertions, so the integration slot is better spent on the mutation evidence than on a browser run. If the conductor wants a browser check anyway, the surface is the schedules page footer with a four-digit total and with zero rows.

---

## Spec coverage map

| Spec section | Covered by |
|---|---|
| 0.1 per-commit enumeration-free gate | Tasks 1.6, 2.6, 3.3, 4.5, each capturing its own output |
| 0.2 twelve-file corroborating table | Task 0 Step 2 (GATE-12) and Task 5 Step 5 |
| 0.3 mutation obligation preconditions | Task 1.5 procedure block |
| 1.2 what changes by symbol | Tasks 1.2 and 1.3 |
| 1.3 Decisions 1.1 to 1.6 | Task 1.2's hook body and doc comments |
| 1.5 the hook's own tests, all eight adapted plus one new | Task 1.1 |
| 1.6 M1 and M2 | Task 1.5, plus the guard file in Task 1.4 |
| 1.7 criteria 1.A to 1.I | 1.A/1.C/1.D/1.E/1.F Task 1.1; 1.B Task 1.3 Steps 3-4; 1.G Task 1.2 (the file imports only `react`); 1.H Task 1.6; 1.I Task 1.5 |
| 2.2 what changes by symbol | Tasks 2.3 and 2.4 |
| 2.3 Decisions 2.1 to 2.3 | Task 2.3's single-cast body and comment; Task 2.4's deletions |
| 2.5 five helper tests, each proven RED first | Tasks 2.1 and 2.2 |
| 2.6 criteria 2.A to 2.F | 2.A/2.D/2.F Task 2.5 Step 4 and Step 3; 2.B Task 2.1; 2.C Task 2.5 Step 1; 2.E Task 2.6 |
| 3.3 what changes by symbol, and the empty-case note | Task 3.2 |
| 3.5 two new tests with the 2341 and 1234 totals | Task 3.1 |
| 3.6 criteria 3.A to 3.E | 3.A/3.B Task 3.1; 3.C/3.D Task 3.3 Step 3; 3.E Task 3.3 Step 1 |
| 4.2 three replacements | Tasks 4.1 and 4.2 |
| 4.3 the `web/CLAUDE.md` note | Task 4.4 |
| 4.4 comment-only diff review | Task 4.3 Step 3 and Task 4.6 Step 3 |
| 4.5 criteria 4.A to 4.E | 4.A/4.B Task 4.3 Steps 1-2; 4.C Task 4.3 Step 3; 4.D Task 4.5; 4.E Task 4.3 Step 4 |
| 5.1 closing the four items | Task 5 Step 7, explicitly handed to the conductor |
| 5.2 follow-up proposals | Phase 4 note, handed to the conductor |

## Deviations from the spec, and why

1. **The gate commands use the recorded merge-base SHA rather than the literal ref `origin/main`.** If `origin/main` advances mid-branch, a diff against the moving tip folds other people's changes into this branch's evidence. Task 0 Step 1 records the SHA once. Where `origin/main` has not moved the two are the same commit.
2. **`WorkersPage.revokedPager.test.tsx` is created unconditionally, not only if M1 survives.** Spec section 1.6 makes the new sibling file the remedy for a surviving M1. The existing `WorkersPage.pager.test.tsx` does kill M1, but only because its active-workers fixture happens to carry an empty `next_cursor`, which turns the substitution into a silent no-op. That kill is a property of a fixture inside a frozen file, so it cannot be strengthened and could be lost to an unrelated fixture edit later. The wrong-page substitution is the error class the single-argument form newly admits, and it deserves a permanent subject of its own (`reference_mutation_proof_must_leave_a_test`). The file is new, so `--diff-filter=M` never sees it and the gate is unaffected.
3. **The ReservationsTab control comment keeps its reasoning but drops its narrative.** Spec section 4.2 says to keep the reasoning intact, and the original wording carries it as history ("the previous control was ..."), which the comment policy forbids. The replacement states the same constraint as a rule about what a control phrase must not do, so the reader still learns why this phrase and not the obvious one, without a record of a moment living in a comment.
4. **WorkersPage's `type SortField` import is dropped at commit 2.** The spec's section 2.2 change list does not mention it. `web/tsconfig.json` sets `noUnusedLocals: true` and `SortField` is referenced only by the local `toggleSort`, so leaving the import is a hard `tsc -b` error rather than a style preference.
5. **The five comment blocks are four.** Spec section 2.2 says "the comment block immediately above each of them". `WorkersPage.tsx`'s copy has no comment block, so five declarations are deleted and four blocks go with them. Nothing about the outcome changes.
