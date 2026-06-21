# SPA Pagination Cursor Corruption Fix Implementation Plan

> Conductor-authored plan (autopilot). REQUIRED SUB-SKILL for the implementer:
> superpowers:test-driven-development. Steps use `- [ ]` checkboxes.

**Goal:** Stop the cursor stack from being corrupted when "next" (or "prev") is clicked during
an in-flight page fetch on JobsPage and SchedulesPage.

**Root cause:** Both pages use React Query `placeholderData: keepPreviousData`. During a page
transition, `data` is still the PREVIOUS page's payload, so `data.next_cursor` is the stale
cursor of the page just left. The next button is gated only by `!data?.next_cursor`, so a second
"next" click during the fetch pushes a duplicate cursor onto the stack without advancing; a later
"prev" then appears to do nothing.

**Fix (from the backlog proposal):** destructure `isPlaceholderData` from the query result and
disable BOTH paging buttons while it is true, so no cursor mutation happens while the displayed
rows do not match the current cursor.

**Slice:** Frontend-only (`web/src/jobs/JobsPage.tsx`, `web/src/schedules/SchedulesPage.tsx`).
No backend/API change. Two independent page slices; do them in sequence in one engineer pass.

**Test seam:** vitest + Testing Library + the existing MSW-style handler harness already used in
`web/src/jobs/JobsPage.test.tsx` ("paginates forward and back via the cursor stack") and
`web/src/schedules/SchedulesPage.test.tsx`. The deterministic, RED-able property: **while a page
transition's fetch is in flight (placeholder data shown), both next and prev are disabled.** On
the unfixed code next stays enabled (gated by the stale `next_cursor`) and prev is enabled (gated
by `stack.length`); with the fix both are disabled until the new page's data arrives. Make the
page-2 request hang via a controllable/deferred handler (a promise the test resolves explicitly),
click next, assert both buttons disabled, then resolve and assert the page advanced and prev
returns to page 1.

---

## Task 1: JobsPage - RED regression test, then disable paging during placeholder

**Files:** `web/src/jobs/JobsPage.test.tsx` (add test), `web/src/jobs/JobsPage.tsx` (fix).

- [ ] **Step 1 (RED):** Add a test, e.g. `next and prev are disabled while a page fetch is in
  flight`. Reuse the existing harness. Sequence: load page 1 (has `next_cursor: 'CUR1'`); make
  the request for `cursor=CUR1` hang on a deferred promise the test controls; `userEvent.click`
  next; assert `screen.getByRole('button', { name: /next/i })` is `toBeDisabled()` AND the prev
  button is `toBeDisabled()` (this is the placeholder window). Then resolve the deferred page-2
  response, `waitFor` job-B to render, assert next is disabled (no next_cursor) and prev enabled,
  click prev, assert page 1 (job-A) with prev disabled. Run `npm test -- JobsPage` (in `web/`) and
  confirm it FAILS on the two "disabled during placeholder" assertions (buttons are enabled
  without the fix). Capture the RED output. Commit the failing test.

- [ ] **Step 2 (GREEN):** In `JobsPage.tsx`, destructure `isPlaceholderData` from `useJobs(...)`
  (line 30) and add it to both buttons:
  - prev (line 142): `disabled={stack.length === 0 || isPlaceholderData}`
  - next (line 150): `disabled={!data?.next_cursor || isPlaceholderData}`
  Run `npm test -- JobsPage` -> GREEN. Commit the fix.

## Task 2: SchedulesPage - RED regression test, then disable paging during placeholder

**Files:** `web/src/schedules/SchedulesPage.test.tsx` (add test), `web/src/schedules/SchedulesPage.tsx` (fix).

- [ ] **Step 1 (RED):** Mirror Task 1's test against SchedulesPage's `cursorStack` model
  (prev = `setCursorStack((s) => s.slice(0, -1))`, next = push `data.next_cursor`). Same property:
  during the in-flight page-2 fetch, both next and prev disabled. Run `npm test -- SchedulesPage`,
  confirm RED, commit.

- [ ] **Step 2 (GREEN):** In `SchedulesPage.tsx`, destructure `isPlaceholderData` from
  `useSchedules(...)` (line 32) and add to both buttons:
  - prev (line 150): `disabled={cursorStack.length === 0 || isPlaceholderData}`
  - next (line 158): `disabled={!data?.next_cursor || isPlaceholderData}`
  Run `npm test -- SchedulesPage` -> GREEN. Commit.

## Task 3: Full verification

- [ ] Run the whole web test suite (`npm test` in `web/`) and the type/lint/build checks the
  project uses (`npm run build` / `tsc`) -> all green.
- [ ] Verify in a browser preview: load Jobs, click next, confirm next/prev are briefly disabled
  during the fetch and re-enable when rows load; confirm rapid double-next no longer strands prev.

## Notes / Invariants

- Pure presentational gating change; no query-key, hook, or API change. `useJobs`/`useSchedules`
  already return `isPlaceholderData` (standard React Query field) - no hook edit needed.
- Keep it surgical: only the two destructures and the four `disabled` expressions change in the
  page components, plus the two regression tests.
