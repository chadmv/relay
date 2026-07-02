# Jobs List Holo Relayout Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restyle the shipped Jobs list (`JobsTable.tsx` + `JobsPage.tsx`) onto the merged Holo primitives (`GlassPanel`, `Eyebrow`) matching the hi-fi `HoloJobsList`, with zero change to any data path, query key, pagination behavior, sort rule, or navigation target.

**Architecture:** Frontend-only, presentational relayout of two existing components. `JobsTable.tsx` swaps its flat `bg-white/5` container for `GlassPanel`, keeps the `COLS` grid and every `status.ts`-driven cell, and gains a subtle running-row tint. `JobsPage.tsx` swaps its inline eyebrow string for the `Eyebrow` primitive, moves the pagination footer inside the table `GlassPanel` as a `border-t` row, and restyles the skeleton/error/empty surfaces to `GlassPanel` (mirroring `WorkersPage`). No hook, query key, pagination state machine, sort rule, filter mapping, or link target changes. No new components; the primitives are already tested.

**Tech Stack:** React 18 + TypeScript, Tailwind v4 (literal class strings so the JIT includes them), TanStack Query, React Router, Vitest + Testing Library + MSW.

---

## Slice independence

This is a **frontend-only** slice. It touches no Go code and adds no endpoints, no `.sql`, no `.proto`. There is no backend counterpart, so there is nothing to run in parallel: Phase 3 runs a single frontend track. This slice depends only on the already-merged Holo primitives (`web/src/components/holo/`) and on the already-shipped Jobs list. **For the conductor: no frontend/backend parallelism to schedule.**

## Hard-preserve constraints (do NOT break these)

This is a pure restyle. The following are load-bearing and MUST behave byte-for-byte the same after every task. If a step would change any of these, the step is wrong.

- **Hooks + query keys.** `useJobs(sort, status, cursor)` with key `['jobs', sort, status, cursor]`, `refetchInterval`, `placeholderData: keepPreviousData`. `useJobStats()` with key `['job-stats']`. Neither file may be modified.
- **Cursor pagination state machine.** The `cursor` / `stack` / `startOffset` / `offsets` state, `next()`, `prev()`, `computePageRange`, `rangeText`, and the `isPlaceholderData`-gated button disabling (`stack.length === 0 || isPlaceholderData` for prev; `!data?.next_cursor || isPlaceholderData` for next). Copy this logic verbatim; do not "clean it up".
- **Sort-disabled-while-status-filtered rule.** `SortControl` (`web/src/jobs/SortControl.tsx`) is a native `<select aria-label="Sort jobs">` and is **not modified at all**. `pickFilter` snaps `sort` back to `DEFAULT_SORT` and disables the control when `statusFiltered`. The server 400s sort+status together, so this is correctness, not cosmetics.
- **Filter chips.** The `FILTERS` array (`All`/`Running`/`Queued`/`Done`/`Failed` mapping to `''`/`running`/`pending`/`done`/`failed`, including **`Queued` -> `pending`**), `aria-pressed`, and the accent-tinted active state. Keep the pressable `<button>` toggles; do NOT swap for the `Chip` primitive.
- **`+ New job` link.** `<Link to="/jobs/new">` styled as an accent button. Stays a router `Link` (not `PillButton`, which is a `<button>`); same copy, same target, same classes.
- **Row -> `/jobs/:id` navigation.** The name `<Link to={`/jobs/${j.id}`}>` in `JobsTable` is the row nav; same target, same `truncate font-sans text-[13px] text-fg hover:text-accent` classes.
- **`status.ts` helpers.** `statusColor` (jobs `JobStatus` dot+label), `progressPct`, `formatDuration`, `formatStarted` are unchanged and drive the cells. Do NOT route jobs through the worker `StatusDot` or `ProgressBar` primitives (they carry worker/accent-only vocabulary the spec explains is a mismatch).

## Deliberately NOT used primitives (per spec)

`KpiStat`, `Panel`, `PillButton`, `Chip`, `StatusDot`, `ProgressBar` are all merged and available, but the spec deliberately does **not** use them here:

- `KpiStat` / `Panel` - the hi-fi `HoloJobsList` uses the inline KPI strip and a `GlassPanel` grid, not the four-up card grid or single-cell `Panel` header (those are the worker-detail idiom).
- `PillButton` - its `ghost` variant is `px-4 py-2`; the footer buttons are the compact `px-3 py-1 text-[11px]` variant. Keep the existing inline pill classes.
- `Chip` - lacks the `accent-b` tone the schedule chip uses.
- `StatusDot` - hard-wired to `WorkerStatus`/`livenessView`; jobs have their own `JobStatus` map in `status.ts`.
- `ProgressBar` - accent-only fill; the jobs bar needs per-status ok/err/accent fills.

Only `Eyebrow` and `GlassPanel` are consumed. Do not import the others into these files.

## Context the engineer needs before starting

Read these before Task 1; hold them in context throughout.

- **Restyle targets:** `web/src/jobs/JobsTable.tsx`, `web/src/jobs/JobsPage.tsx`.
- **Consumed primitives (do NOT modify):**
  - `Eyebrow({ children, className? })` - `web/src/components/holo/Eyebrow.tsx`. Mono uppercase micro-label. Base classes are `font-mono text-[11px] uppercase tracking-[0.18em] text-fg-mute`; it uppercases via CSS, so pass normal-case text.
  - `GlassPanel({ as?, className?, children, ...rest })` - `web/src/components/holo/GlassPanel.tsx`. Gradient glass surface; base classes are literal and a caller `className` **appends** after the base. `...rest` spreads onto the tag (so `role`, `aria-*` pass through).
  - Both are re-exported from the barrel `web/src/components/holo/index.ts`; import as `import { Eyebrow, GlassPanel } from '../components/holo'`.
- **Styling reference (already on the primitives):** `web/src/workers/WorkersPage.tsx` - uses `Eyebrow` for the header micro-label and `GlassPanel` for its skeleton (`<GlassPanel key={i} className="h-28" />`), error (`<GlassPanel className="mx-auto mt-10 max-w-md p-6 text-center">`), and empty (`<GlassPanel className="mx-auto mt-10 max-w-md p-6 text-center text-[13px] text-fg-mute">`) surfaces. Mirror these exact wrapper class patterns.
- **Unchanged data/logic:** `web/src/jobs/useJobs.ts`, `web/src/jobs/useJobStats.ts`, `web/src/jobs/SortControl.tsx`, `web/src/jobs/status.ts`, `web/src/jobs/api.ts`, and `web/src/lib/pageRange.ts` (`computePageRange`). Do NOT edit any of these.
- **Tests to keep green (behavior) and touch only where a structural query breaks:** `web/src/jobs/JobsTable.test.tsx`, `web/src/jobs/JobsPage.test.tsx`. Their queries are by visible text, `role`, `aria-label`, and `href` - all preserved by this restyle, so they should survive with no edits. Only add a **light** structural assertion where it locks in an intended change (the `GlassPanel` surface, "no view-switch/My-jobs/search rendered").

### Backlog items the omitted controls trace to (for the code comments)

The hi-fi view-switch (Lanes/Timeline), the free-text filter input, and the "My jobs" pill are backend-blocked and are **not rendered**. Each omission needs a short code comment in `JobsPage.tsx` naming its backlog item:

- View switch Lanes: `docs/backlog/idea-2026-06-05-jobs-lanes-swimlanes-view.md`
- View switch Timeline: `docs/backlog/idea-2026-06-05-jobs-timeline-view.md`
- My jobs toggle + free-text filter: `docs/backlog/idea-2026-06-05-my-jobs-toggle-mine-filter.md`

### Commands

- Run one test file: `cd web && npx vitest run src/jobs/JobsTable.test.tsx`
- Run one test file: `cd web && npx vitest run src/jobs/JobsPage.test.tsx`
- Run one test by name: `cd web && npx vitest run src/jobs/JobsPage.test.tsx -t "renders jobs and the KPI strip"`
- Run all jobs + holo tests: `cd web && npx vitest run src/jobs src/components/holo`
- Typecheck: `cd web && npx tsc --noEmit`

Use PowerShell (the primary shell) or the Bash tool; either works for `cd web && ...`. Commit with the Bash tool (Git Bash) using a heredoc.

> **Note on `web/dist`:** `web/dist` is tracked but stale from the scaffold. Do **not** run a production build in these tasks (none is needed). If any step dirties `web/dist`, run `git checkout -- web/dist/` before assembling the commit so the diff stays to source only.

---

## Task 1: JobsTable - swap the container to GlassPanel and add the running-row tint

Restyle `JobsTable`'s container and empty state to `GlassPanel`, keep the `COLS` grid, the column-header row, and every `status.ts`-driven cell (dot+label, per-status progress fill, schedule chip, name `Link`) exactly, and add a subtle `bg-accent/[0.04]` tint to running rows. This is a pure visual change: the existing `JobsTable.test.tsx` behavior assertions (text, `link` role/href, empty-state text) are the regression guard. We add two light structural assertions to lock in the `GlassPanel` surface and the running-row tint.

**Files:**
- Modify: `web/src/jobs/JobsTable.tsx`
- Test: `web/src/jobs/JobsTable.test.tsx` (existing; add two assertions)

- [ ] **Step 1: Run the existing JobsTable tests to confirm the baseline is green**

Run: `cd web && npx vitest run src/jobs/JobsTable.test.tsx`
Expected: PASS (4 tests: renders rows, schedule chip, empty state, name link).

- [ ] **Step 2: Add the two structural assertions (they should fail against the current flat markup)**

The current empty state and container use flat `bg-white/5`, and rows have no running tint. Add a `data-testid` reference in the test now so the failing assertions describe the intended markup. Append these two tests to `web/src/jobs/JobsTable.test.tsx` (after the existing `the job name links to the job detail page` test):

```tsx
test('wraps the table in a GlassPanel surface', () => {
  renderTable(jobs)
  // The GlassPanel base classes carry the gradient glass fidelity upgrade.
  const surface = screen.getByTestId('jobs-table')
  expect(surface).toHaveClass('rounded-card', 'border', 'border-border', 'backdrop-blur-[8px]')
})

test('tints the running row with a subtle accent background', () => {
  renderTable(jobs)
  // film-x / shot-042 render is status:running; ci build is status:done.
  const runningRow = screen.getByTestId('job-row-9F4E1C')
  const doneRow = screen.getByTestId('job-row-C41A02')
  expect(runningRow).toHaveClass('bg-accent/[0.04]')
  expect(doneRow).not.toHaveClass('bg-accent/[0.04]')
})
```

- [ ] **Step 3: Run the two new tests to verify they fail**

Run: `cd web && npx vitest run src/jobs/JobsTable.test.tsx -t "GlassPanel surface"`
Expected: FAIL - `Unable to find an element by: [data-testid="jobs-table"]` (the container is a flat `<div>` with no testid, no `backdrop-blur-[8px]`).

Run: `cd web && npx vitest run src/jobs/JobsTable.test.tsx -t "tints the running row"`
Expected: FAIL - `Unable to find an element by: [data-testid="job-row-9F4E1C"]`.

- [ ] **Step 4: Restyle JobsTable to GlassPanel + running-row tint**

Rewrite `web/src/jobs/JobsTable.tsx` to the following. The `COLS` template, the column-header row, the `status.ts` dot+label, the inline per-status progress fill, the inline `accent-b` schedule chip, and the name `Link` are all preserved exactly; only the container becomes `GlassPanel`, the empty state becomes `GlassPanel` (matching `WorkersPage`), each row keys off `data-testid`, and running rows gain `bg-accent/[0.04]`.

```tsx
import { Link } from 'react-router-dom'
import { GlassPanel } from '../components/holo'
import type { Job } from './api'
import { statusColor, progressPct, formatDuration, formatStarted } from './status'

const COLS = 'grid grid-cols-[90px_1fr_120px_150px_120px_70px_150px]'

export function JobsTable({ jobs }: { jobs: Job[] }) {
  if (jobs.length === 0) {
    return (
      <GlassPanel className="mx-auto mt-10 max-w-md p-6 text-center text-[13px] text-fg-mute">
        No jobs yet.
      </GlassPanel>
    )
  }
  return (
    <GlassPanel data-testid="jobs-table">
      <div className={`${COLS} border-b border-border px-4 py-3 font-mono text-[10px] tracking-wider text-fg-mute`}>
        <span>ID</span>
        <span>NAME</span>
        <span>STATUS</span>
        <span>PROGRESS</span>
        <span>STARTED</span>
        <span>DUR</span>
        <span>OWNER</span>
      </div>
      {jobs.map((j) => {
        const c = statusColor(j.status)
        const pct = progressPct(j.done_tasks, j.total_tasks)
        return (
          <div
            key={j.id}
            data-testid={`job-row-${j.id}`}
            className={`${COLS} items-center border-b border-border/40 px-4 py-2 font-mono text-[11.5px] ${
              j.status === 'running' ? 'bg-accent/[0.04]' : ''
            }`}
          >
            <span className="text-fg-mute">{j.id.slice(0, 6)}</span>
            <span className="flex min-w-0 items-center gap-2">
              <Link to={`/jobs/${j.id}`} className="truncate font-sans text-[13px] text-fg hover:text-accent">
                {j.name}
              </Link>
              {j.scheduled_job_name && (
                <span className="flex-none rounded-full border border-accent-b/40 bg-accent-b/10 px-1.5 py-0.5 text-[9.5px] text-accent-b">
                  ⟳ {j.scheduled_job_name}
                </span>
              )}
            </span>
            <span className={`flex items-center gap-2 ${c.text}`}>
              <span className={`h-1.5 w-1.5 rounded-full ${c.dot}`} />
              {j.status}
            </span>
            <span className="grid grid-cols-[1fr_36px] items-center gap-2 pr-4">
              <span className="relative h-1 overflow-hidden rounded bg-white/10">
                <span
                  className={`absolute inset-y-0 left-0 rounded ${
                    j.status === 'done' ? 'bg-ok' : j.status === 'failed' ? 'bg-err' : 'bg-accent'
                  }`}
                  style={{ width: `${pct}%` }}
                />
              </span>
              <span className="text-right text-fg">{pct}%</span>
            </span>
            <span className="text-fg-mute">{formatStarted(j.started_at)}</span>
            <span className="text-fg-mute">{formatDuration(j.started_at, j.finished_at)}</span>
            <span className="truncate text-[11px] text-fg-mute">{j.submitted_by_email ?? '-'}</span>
          </div>
        )
      })}
    </GlassPanel>
  )
}
```

- [ ] **Step 5: Run the full JobsTable test file to verify all pass**

Run: `cd web && npx vitest run src/jobs/JobsTable.test.tsx`
Expected: PASS (6 tests: the 4 original behavior tests survive unchanged plus the 2 new structural tests).

- [ ] **Step 6: Typecheck**

Run: `cd web && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 7: Commit**

```bash
git add web/src/jobs/JobsTable.tsx web/src/jobs/JobsTable.test.tsx
git commit -m "style(web): jobs table on GlassPanel with running-row tint"
```

---

## Task 2: JobsPage - Eyebrow primitive + GlassPanel skeleton/error surfaces

Swap the inline eyebrow string for the `Eyebrow` primitive (matching `WorkersPage`) and restyle the loading-skeleton and error-with-retry surfaces to `GlassPanel`. The KPI strip, filter chips, `SortControl`, `+ New job` link, and all pagination logic are untouched in this task. The existing `JobsPage.test.tsx` behavior tests (KPI numbers, error banner + retry) are the regression guard; no test edits are needed for this task because the queries are by text/role, not classes or DOM shape.

**Files:**
- Modify: `web/src/jobs/JobsPage.tsx` (import; eyebrow block ~lines 116-119; skeleton block ~lines 84-92; error block ~lines 94-103)
- Test: `web/src/jobs/JobsPage.test.tsx` (existing; no change expected - run to confirm)

- [ ] **Step 1: Run the existing JobsPage tests to confirm the baseline is green**

Run: `cd web && npx vitest run src/jobs/JobsPage.test.tsx`
Expected: PASS (all tests: KPI strip, status chip re-request + sort disabled, Queued -> pending, error + retry, pagination in-flight, pagination ranges, empty range, new-job link, cursor forward/back).

- [ ] **Step 2: Add the `Eyebrow` and `GlassPanel` imports**

In `web/src/jobs/JobsPage.tsx`, after the existing imports (the last import is `import type { JobSort } from './api'`), add:

```tsx
import { Eyebrow, GlassPanel } from '../components/holo'
```

- [ ] **Step 3: Replace the inline eyebrow string with the `Eyebrow` primitive**

In the returned JSX header (currently lines 116-119), replace:

```tsx
        <div>
          <div className="font-mono text-[11px] tracking-widest text-fg-mute">OVERVIEW</div>
          <h1 className="text-[32px] font-normal tracking-tight">Jobs</h1>
        </div>
```

with:

```tsx
        <div>
          <Eyebrow>OVERVIEW</Eyebrow>
          <h1 className="text-[32px] font-normal tracking-tight">Jobs</h1>
        </div>
```

- [ ] **Step 4: Restyle the loading skeleton to `GlassPanel` tiles**

Replace the skeleton early-return (currently lines 84-92):

```tsx
  if (isLoading && !data) {
    return (
      <div className="flex flex-col gap-2">
        {Array.from({ length: 8 }).map((_, i) => (
          <div key={i} className="h-9 rounded border border-border bg-white/5" />
        ))}
      </div>
    )
  }
```

with (each skeleton row becomes a `GlassPanel` tile, mirroring `WorkersPage`'s `<GlassPanel key={i} className="h-28" />` skeleton idiom, sized to the table row height):

```tsx
  if (isLoading && !data) {
    return (
      <div className="flex flex-col gap-2">
        {Array.from({ length: 8 }).map((_, i) => (
          <GlassPanel key={i} className="h-9" />
        ))}
      </div>
    )
  }
```

- [ ] **Step 5: Restyle the error surface to `GlassPanel`**

Replace the error early-return (currently lines 94-103):

```tsx
  if (error && !data) {
    return (
      <div className="mx-auto mt-10 max-w-md rounded-card border border-border bg-white/5 p-6 text-center">
        <div className="mb-3 text-[13px] text-err">{(error as Error).message}</div>
        <Button className="w-auto px-4" onClick={() => refetch()}>
          Retry
        </Button>
      </div>
    )
  }
```

with (mirroring `WorkersPage`'s error card exactly):

```tsx
  if (error && !data) {
    return (
      <GlassPanel className="mx-auto mt-10 max-w-md p-6 text-center">
        <div className="mb-3 text-[13px] text-err">{(error as Error).message}</div>
        <Button className="w-auto px-4" onClick={() => refetch()}>
          Retry
        </Button>
      </GlassPanel>
    )
  }
```

- [ ] **Step 6: Run the JobsPage tests to verify they still pass (no behavior change)**

Run: `cd web && npx vitest run src/jobs/JobsPage.test.tsx`
Expected: PASS (unchanged count). The error+retry test (`shows the error banner with retry, then recovers`) queries by button role and by text, both preserved; the KPI test queries by text (`487`, `12`), preserved.

- [ ] **Step 7: Typecheck**

Run: `cd web && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 8: Commit**

```bash
git add web/src/jobs/JobsPage.tsx
git commit -m "style(web): jobs page eyebrow + glass skeleton/error surfaces"
```

---

## Task 3: JobsPage - move the pagination footer inside the table GlassPanel, and add omission comments

Move the pagination footer from a detached row below `<JobsTable>` to a `border-t` row **inside** the table `GlassPanel`, matching the hi-fi footer position. Because the footer must render inside the same `GlassPanel` as the rows, `JobsTable` needs to accept the footer as a `footer` slot (rendered as its last child, under a `border-t`). The pagination logic itself (state, `next`/`prev`, `computePageRange`, `rangeText`, disabled conditions) stays in `JobsPage` **verbatim** - only where the footer JSX renders moves. Add the code comments that trace the omitted view-switch / My-jobs / search controls to their backlog items.

The empty-state branch of `JobsTable` renders a `GlassPanel` card, not the table, so there is no footer slot in that case - the footer only renders when there are rows. Because `JobsPage` already computes `rangeText` and passes `jobs` in, and `JobsTable` early-returns the empty card when `jobs.length === 0`, we render the footer inside `JobsTable`'s non-empty branch and pass the footer content down as a prop.

**Files:**
- Modify: `web/src/jobs/JobsTable.tsx` (accept an optional `footer` prop; render it as a `border-t` row inside the `GlassPanel`)
- Modify: `web/src/jobs/JobsPage.tsx` (move the footer JSX into a `footer` prop on `<JobsTable>`; add omission comments)
- Test: `web/src/jobs/JobsPage.test.tsx` (existing pagination tests are the guard; add one light "no view-switch/My-jobs/search rendered" assertion)
- Test: `web/src/jobs/JobsTable.test.tsx` (add one assertion that the `footer` slot renders inside the surface)

- [ ] **Step 1: Run the jobs tests to confirm the baseline (post Task 1 and 2) is green**

Run: `cd web && npx vitest run src/jobs`
Expected: PASS (JobsTable 6 tests, JobsPage full set).

- [ ] **Step 2: Add the failing assertions**

Append to `web/src/jobs/JobsTable.test.tsx` (after the running-row-tint test from Task 1):

```tsx
test('renders a footer slot inside the table surface when provided', () => {
  render(
    <MemoryRouter>
      <JobsTable jobs={jobs} footer={<span>FOOTER-MARKER</span>} />
    </MemoryRouter>,
  )
  const surface = screen.getByTestId('jobs-table')
  const footer = screen.getByText('FOOTER-MARKER')
  expect(surface).toContainElement(footer)
})
```

Append to `web/src/jobs/JobsPage.test.tsx` (after the `renders jobs and the KPI strip` test), a guard that the backend-blocked hi-fi controls are not rendered:

```tsx
test('does not render the backend-blocked view-switch, My-jobs, or search controls', async () => {
  server.use(http.get('/v1/jobs', () => HttpResponse.json(page)))
  renderPage()
  await screen.findByText('film-x render')
  // Omitted per spec: Lanes/Timeline view switch, My jobs pill, free-text search.
  expect(screen.queryByRole('button', { name: /lanes/i })).toBeNull()
  expect(screen.queryByRole('button', { name: /timeline/i })).toBeNull()
  expect(screen.queryByRole('button', { name: /my jobs/i })).toBeNull()
  expect(screen.queryByRole('searchbox')).toBeNull()
})
```

- [ ] **Step 3: Run the new assertions to verify they fail (footer slot) and pass (omission guard)**

Run: `cd web && npx vitest run src/jobs/JobsTable.test.tsx -t "footer slot"`
Expected: FAIL - `JobsTable` does not accept a `footer` prop (TS error on the render, or `FOOTER-MARKER` not found).

Run: `cd web && npx vitest run src/jobs/JobsPage.test.tsx -t "backend-blocked"`
Expected: PASS immediately - the current page already renders none of those controls. This assertion is a regression guard, not a red-then-green step; it locks in the intended omission so a future edit that adds a dead control fails here.

- [ ] **Step 4: Add the `footer` prop to `JobsTable`**

In `web/src/jobs/JobsTable.tsx`, change the component signature and render the footer as a `border-t` row inside the `GlassPanel` (after the row `.map`). Replace the signature line:

```tsx
export function JobsTable({ jobs }: { jobs: Job[] }) {
```

with:

```tsx
import type { ReactNode } from 'react'
// ...existing imports unchanged...

export function JobsTable({ jobs, footer }: { jobs: Job[]; footer?: ReactNode }) {
```

(Place the `import type { ReactNode } from 'react'` at the top of the file with the other imports; do not add it inside the function.)

Then, inside the non-empty return, after the closing `)}` of the `{jobs.map(...)}` block and before the closing `</GlassPanel>`, add:

```tsx
      {footer && <div className="border-t border-border px-4 py-3">{footer}</div>}
```

The empty-state branch (`jobs.length === 0`) is unchanged and never renders a footer.

- [ ] **Step 5: Move the footer JSX into `JobsPage` and pass it as the `footer` prop**

In `web/src/jobs/JobsPage.tsx`, replace the current `<JobsTable jobs={jobs} />` line and the detached footer `<div>` that follows it (currently lines 163-188):

```tsx
      <JobsTable jobs={jobs} />

      <div className="flex items-center justify-between px-1 font-mono text-[10.5px] tracking-wider text-fg-mute">
        <span>
          SHOWING <span className="text-fg">{rangeText}</span>
          {' · '}SORT <span className="text-accent-b">{statusFiltered ? `status=${status}` : sort}</span> · CURSOR PAGINATED
        </span>
        <div className="flex gap-2">
          <button
            type="button"
            onClick={prev}
            disabled={stack.length === 0 || isPlaceholderData}
            className="rounded-full border border-border px-3 py-1 text-[11px] text-fg-mute disabled:opacity-40"
          >
            ← prev
          </button>
          <button
            type="button"
            onClick={next}
            disabled={!data?.next_cursor || isPlaceholderData}
            className="rounded-full border border-border px-3 py-1 text-[11px] text-fg-mute disabled:opacity-40"
          >
            next 50 →
          </button>
        </div>
      </div>
```

with the footer passed into the table (the inner markup is identical; the `px-1` outer padding is dropped because the footer now sits inside the `GlassPanel`'s own `px-4 py-3` row):

```tsx
      <JobsTable
        jobs={jobs}
        footer={
          <div className="flex items-center justify-between font-mono text-[10.5px] tracking-wider text-fg-mute">
            <span>
              SHOWING <span className="text-fg">{rangeText}</span>
              {' · '}SORT <span className="text-accent-b">{statusFiltered ? `status=${status}` : sort}</span> · CURSOR PAGINATED
            </span>
            <div className="flex gap-2">
              <button
                type="button"
                onClick={prev}
                disabled={stack.length === 0 || isPlaceholderData}
                className="rounded-full border border-border px-3 py-1 text-[11px] text-fg-mute disabled:opacity-40"
              >
                ← prev
              </button>
              <button
                type="button"
                onClick={next}
                disabled={!data?.next_cursor || isPlaceholderData}
                className="rounded-full border border-border px-3 py-1 text-[11px] text-fg-mute disabled:opacity-40"
              >
                next 50 →
              </button>
            </div>
          </div>
        }
      />
```

> **Preserve check:** the `prev`/`next` `disabled` expressions (`stack.length === 0 || isPlaceholderData` and `!data?.next_cursor || isPlaceholderData`), the `rangeText`, and the `SORT status=<s>` vs `SORT <sort>` display are copied character-for-character. Do not simplify them.

- [ ] **Step 6: Add the omission comments in `JobsPage`**

In `web/src/jobs/JobsPage.tsx`, at the top of the toolbar row (immediately before the `<div className="flex flex-wrap items-center gap-2">` that renders the filter chips), add a comment block tracing the omitted hi-fi controls to their backlog items:

```tsx
      {/*
        The hi-fi HoloJobsList also shows a view-switch (Table/Lanes/Timeline), a
        "My jobs" pill, and a free-text search input. All three are backend-blocked
        and deliberately omitted here (a dead list control reads as broken):
          - Lanes view:    docs/backlog/idea-2026-06-05-jobs-lanes-swimlanes-view.md
          - Timeline view: docs/backlog/idea-2026-06-05-jobs-timeline-view.md
          - My jobs + search: docs/backlog/idea-2026-06-05-my-jobs-toggle-mine-filter.md
        When those land, the view switch and filters re-appear with real backing.
      */}
      <div className="flex flex-wrap items-center gap-2">
```

- [ ] **Step 7: Run the full jobs test suite to verify everything passes**

Run: `cd web && npx vitest run src/jobs`
Expected: PASS. The pagination tests (`pagination footer shows 1-N of total`, `updates to next range`, `partial last page`, `restores prior range`, `0 of 0`, `next and prev are disabled while a page fetch is in flight`, `paginates forward and back`) all query the footer by text (`/1-50 of 2,341/i`, `/0 of 0/i`) and by button role (`/prev/i`, `/next/i`); moving the footer inside the `GlassPanel` does not change those, so they pass unchanged. The new footer-slot and omission-guard tests pass.

- [ ] **Step 8: Typecheck**

Run: `cd web && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 9: Restore `web/dist` if the test run touched it, then commit**

```bash
git checkout -- web/dist/ 2>/dev/null || true
git add web/src/jobs/JobsTable.tsx web/src/jobs/JobsPage.tsx web/src/jobs/JobsTable.test.tsx web/src/jobs/JobsPage.test.tsx
git commit -m "style(web): move jobs pagination footer inside the table glass panel"
```

---

## Final verification

- [ ] **Run all jobs + holo tests together**

Run: `cd web && npx vitest run src/jobs src/components/holo`
Expected: PASS across the board.

- [ ] **Typecheck the whole web package**

Run: `cd web && npx tsc --noEmit`
Expected: no errors.

- [ ] **Confirm no unintended files are staged**

Run: `git status`
Expected: only `web/src/jobs/JobsTable.tsx`, `web/src/jobs/JobsPage.tsx`, `web/src/jobs/JobsTable.test.tsx`, `web/src/jobs/JobsPage.test.tsx` in the three commits. `web/dist` clean. No changes to `useJobs.ts`, `useJobStats.ts`, `SortControl.tsx`, `status.ts`, `api.ts`, or any Go file.

---

## Self-review against the spec

- **Eyebrow inline string -> `Eyebrow` primitive** (spec section 1) - Task 2, Step 3.
- **KPI strip kept as the inline mono strip, `KpiStat` not used** (spec section 2) - unchanged; no task modifies it, and the `renders jobs and the KPI strip` test guards `487`/`12`.
- **Filter chips unchanged, `aria-pressed`, `Queued`->`pending`, `Chip` not used** (spec section 3) - unchanged; hard-preserve constraint; guarded by `selecting a status chip...` and `selecting the Queued chip...` tests.
- **`SortControl` native `<select>` unchanged, disabled-while-filtered rule** (spec section 3) - not modified; guarded by `getByLabelText('Sort jobs')` disabled assertion.
- **Table container flat `bg-white/5` -> `GlassPanel`** (spec section 4 / JobsTable restyle) - Task 1, Step 4; guarded by the `GlassPanel surface` assertion.
- **Empty state -> `GlassPanel`** (spec section 4) - Task 1, Step 4 (empty branch); `renders the empty state` test still passes.
- **Pagination footer moves inside the table `GlassPanel` as a `border-t` row, logic verbatim** (spec section 5) - Task 3; guarded by all pagination tests plus the footer-slot assertion.
- **prev/next kept as plain `<button>`s with compact pill classes, `PillButton` not used** (spec section 5) - preserved verbatim in Task 3, Step 5.
- **`+ New job` stays a `Link`** (spec section 1 / preserved list) - unchanged; guarded by the `+ New job` link test.
- **Name `Link` to `/jobs/:id`, schedule chip `accent-b` inline, status dot from `status.ts`, per-status progress fill inline** (JobsTable restyle) - preserved verbatim in Task 1, Step 4; guarded by name-link and schedule-chip tests.
- **Running-row tint `bg-accent/[0.04]`** (JobsTable restyle) - Task 1, Step 4; guarded by the running-row-tint assertion.
- **Skeleton/error surfaces -> `GlassPanel` (mirror `WorkersPage`)** (changed list) - Task 2, Steps 4-5.
- **View-switch, My-jobs, free-text search NOT rendered, with backlog-tracing code comments** (Backend reality / graceful handling) - Task 3, Step 6 comment block; guarded by the `backend-blocked` omission test.
- **No new tests strictly required; light structural guards added only where they lock in an intended change** (spec Test impact) - added exactly three: `GlassPanel surface`, running-row tint, footer slot, plus the omission guard (four light assertions total, all structural).
- **No hook/query-key/pagination-state/sort-rule/filter-mapping/link-target change** - hard-preserve section; `useJobs.ts`, `useJobStats.ts`, `SortControl.tsx`, `status.ts`, `api.ts` are never opened for edit.
