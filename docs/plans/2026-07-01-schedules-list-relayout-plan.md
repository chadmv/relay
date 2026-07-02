# Schedules List Holo Relayout Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restyle the shipped Schedules list (`SchedulesTable.tsx` + `SchedulesPage.tsx`) onto the merged Holo primitives (`GlassPanel`, `Eyebrow`) matching the hi-fi `HoloSchedules`, with zero change to any data hook, query key, cursor pagination behavior, sort rule, row action, summary count, or navigation target.

**Architecture:** Frontend-only, presentational relayout of two existing components. `SchedulesTable.tsx` swaps its flat `bg-white/5` container for `GlassPanel`, keeps the `COLS` grid and every cell (inline enabled/paused dot, overlap pill, NEXT-RUN glyph, plain-text NAME and LAST JOB, Run-now + Enable/Disable actions), takes over the empty state, and gains an optional `footer` slot rendered in BOTH the empty and non-empty branches. `SchedulesPage.tsx` swaps its inline eyebrow string for the `Eyebrow` primitive, restyles the skeleton/error surfaces to `GlassPanel`, moves the pagination footer inside `SchedulesTable`'s `GlassPanel` as a `border-t` row (via the footer slot), adds the `· SORT <sort>` footer segment, and adds code comments tracing the omitted hi-fi affordances (filter chips, search, FAILED·24H stat) to their backlog items. No hook, query key, pagination state machine, sort rule, action wiring, or summary logic changes. No new components; the primitives are already tested.

**Tech Stack:** React 18 + TypeScript, Tailwind v4 (literal class strings so the JIT includes them), TanStack Query, React Router, Vitest + Testing Library + MSW.

---

## Slice independence

This is a **frontend-only** slice. It touches no Go code and adds no endpoints, no `.sql`, no `.proto`. There is no backend counterpart, so there is nothing to run in parallel: Phase 3 runs a single frontend track. This slice depends only on the already-merged Holo primitives (`web/src/components/holo/`) and on the already-shipped Schedules list. **For the conductor: no frontend/backend parallelism to schedule.**

## Hard-preserve constraints (do NOT break these)

This is a pure restyle. The following are load-bearing and MUST behave byte-for-byte the same after every task. If a step would change any of these, the step is wrong.

- **Data hooks + query keys.** `useSchedules(sort, cursor)` with key `['schedules', sort, cursor ?? '']`, `refetchInterval` (10s), `placeholderData: keepPreviousData`. `useScheduleActions()` `runNow`/`setEnabled` mutations, each with `invalidateQueries({ queryKey: ['schedules'] })` on success. **`useSchedules.ts` and `useScheduleActions.ts` are never opened for edit.**
- **Cursor pagination state machine.** The `cursorStack` / `cursor` / `startOffset` / `offsets` / `pendingId` state, `goNext()`, `goPrev()`, `computePageRange` (`x`,`y`), the range/total display, and the `isPlaceholderData`-gated button disabling (`cursorStack.length === 0 || isPlaceholderData` for prev; `!data?.next_cursor || isPlaceholderData` for next). The plain-setter batching (not functional updaters, per the in-file comment guarding StrictMode desync) is intentional. Copy this logic verbatim; do not "clean it up".
- **Sort behavior.** The native `<select aria-label="Sort">` with its eight `SORT_OPTIONS` (`-created_at`/`created_at`/`name`/`-name`/`next_run_at`/`-next_run_at`/`-updated_at`/`updated_at`) is unchanged. Schedules has **no** status-filter chips and therefore **no** sort-disabled-while-filtered rule; the sort control is always enabled. `chooseSort` resets pagination (`setCursorStack([])`, `setOffsets([])`, `setStartOffset(0)`) and is preserved verbatim. A test does `selectOptions(getByLabelText('Sort'), 'name')` - the `aria-label="Sort"` must not change.
- **Row actions.** `onRunNow(s.id)` and `onToggleEnabled(s.id, !s.enabled)` wiring, the `pendingId` single-flight guard (both buttons `disabled` when `pendingId === s.id`), and **`Run now` on every row** (never conditionally hidden on paused rows - this diverges from the hi-fi mock and is load-bearing: a test asserts a disabled row shows both `Run now` and `Enable`). The `Enable`/`Disable` toggle label keys on `s.enabled`.
- **Page-scoped summary counts.** The ENABLED/PAUSED strip counts only the loaded page via `countEnabled(schedules)`, and the `{total} schedules` tail comes from the envelope's `data.total` (fleet/owner-wide). This split is intentional and preserved: counts stay page-scoped, `total` stays fleet-wide. A test asserts `2 schedules`.
- **Action-error banner.** `(runNow.error ?? setEnabled.error)` rendered as an err-tinted banner; restyled, not removed.
- **No row-click nav.** There is **no `/schedules/:id` detail route** in `web/src/app/router.tsx`. The NAME cell stays **plain text** (not a `Link`). The LAST JOB cell stays **plain `shortId` text** (not a link to `/jobs/:id`). No trailing chevron column. Do not add any navigation.
- **`format.ts` helpers.** `nextRunDisplay`, `formatRelativeTime`, `shortId` are unchanged and drive the NEXT RUN / LAST RUN / LAST JOB cells. The 1s `setTick` interval that refreshes relative strings between polls stays. Do NOT open `format.ts` or `api.ts` for edit.

## Deliberately NOT used primitives (per spec)

`KpiStat`, `Panel`, `PillButton`, `Chip`, `StatusDot`, `ProgressBar` are all merged and available (barrel: `web/src/components/holo/index.ts`), but the spec deliberately does **not** use them here. Only `Eyebrow` and `GlassPanel` are consumed.

- **`KpiStat` / `Panel`** - `HoloSchedules` uses the compact inline summary strip, not a four-up `KpiStat` card grid (that is the worker-detail idiom) and not a single-cell `Panel` header (which fights the 9-column grid header). Keep the inline strip and use `GlassPanel` + explicit grid header/footer rows.
- **`PillButton`** - its `ghost` variant is the larger `px-4 py-2`; the footer prev/next buttons and the row action buttons are the compact `px-3 py-1`/`px-2.5 py-1` `text-[11px]` variant. Keep the existing inline pill classes.
- **`Chip`** - the overlap pill is a tiny `9.5px`-uppercase token whose color keys on the `allow`/`skip` value; `Chip`'s `accent`/`muted`/`warn` tones do not cleanly express it, and it is used only here. Keep the inline overlap pill.
- **`StatusDot`** - hard-wired to `WorkerStatus`/`livenessView`; a schedule's indicator is a simple boolean enabled/paused, not a worker status. Keep the inline enabled/paused dot.
- **`ProgressBar`** - schedules have no progress bar at all; irrelevant here.

Do not import any of these six into these files. Import only `import { Eyebrow, GlassPanel } from '../components/holo'`.

## Context the engineer needs before starting

Read these before Task 1; hold them in context throughout.

- **Restyle targets:** `web/src/schedules/SchedulesTable.tsx`, `web/src/schedules/SchedulesPage.tsx`.
- **Consumed primitives (do NOT modify):**
  - `Eyebrow({ children, className? })` - `web/src/components/holo/Eyebrow.tsx`. Mono uppercase micro-label. Base classes are `font-mono text-[11px] uppercase tracking-[0.18em] text-fg-mute`; it uppercases via CSS, so pass normal-case text.
  - `GlassPanel({ as?, className?, children, ...rest })` - `web/src/components/holo/GlassPanel.tsx`. Gradient glass surface; base classes include `rounded-card border border-border ... backdrop-blur-[8px] shadow-[...]`, and a caller `className` **appends** after the base. `...rest` spreads onto the tag (so `data-testid`, `role`, `aria-*` pass through).
  - Both are re-exported from the barrel `web/src/components/holo/index.ts`; import as `import { Eyebrow, GlassPanel } from '../components/holo'`.
- **Reference (the just-shipped jobs-list relayout, the concrete target pattern):** `web/src/jobs/JobsPage.tsx` (Eyebrow header, GlassPanel skeleton/error surfaces, footer passed as a prop into the table, omission comment block) and `web/src/jobs/JobsTable.tsx` (GlassPanel container with `data-testid="jobs-table"`, grid header row, footer rendered in BOTH the empty branch and the non-empty branch as a `border-t` row). Mirror these patterns.
- **Styling reference on the primitives:** `web/src/workers/WorkersPage.tsx` - uses `Eyebrow` for the header micro-label and `GlassPanel` for its skeleton (`<GlassPanel key={i} className="h-28" />`), error (`<GlassPanel className="mx-auto mt-10 max-w-md p-6 text-center">`), and empty surfaces.
- **Unchanged data/logic (do NOT edit):** `web/src/schedules/useSchedules.ts`, `web/src/schedules/useScheduleActions.ts`, `web/src/schedules/format.ts`, `web/src/schedules/api.ts`, and `web/src/lib/pageRange.ts` (`computePageRange`).
- **Tests to keep green (behavior) and touch only where a structural query breaks:** `web/src/schedules/SchedulesTable.test.tsx`, `web/src/schedules/SchedulesPage.test.tsx`. Their queries are by visible text, `role`, and `aria-label` - all preserved by this restyle, so they should survive with no edits. Only add **light** structural assertions where they lock in an intended change (the `GlassPanel` surface, the footer rendering in BOTH empty and non-empty states, "no filter chips / search / FAILED-24H stat rendered").

### Empty-state ownership shift (important structural note)

Today the empty state (`No schedules yet.`) is an **early return inside `SchedulesPage`** (SchedulesPage.tsx:118-124), before `<SchedulesTable>` is rendered. The pagination footer today is a **detached row below `<SchedulesTable>`** (SchedulesPage.tsx:173-195). To move the footer inside the table `GlassPanel` AND keep it visible on an empty page (the gap the jobs-list plan flagged - the jobs `JobsTable` renders `footer` in both its empty and non-empty branches), we must:

1. Move the empty-state rendering **into `SchedulesTable`** (like `JobsTable` does), so `SchedulesTable` owns both the empty card and the populated table, and can render the `footer` slot in either.
2. Remove the `SchedulesPage` empty early-return, always render `<SchedulesTable>` (passing the possibly-empty `schedules`), and pass the footer as a prop.

The summary strip's `counts`/`total` and the footer's `x`,`y`,`total` are still computed in `SchedulesPage`; only the render location moves. On an empty page today the page shows the empty card and no header/footer at all; after this change the empty page shows the header + summary strip + the empty `GlassPanel` card + the footer (range reads `0-0 of 0`), matching the jobs-list behavior. The `No schedules yet.` text is preserved so the existing empty-state test still passes.

### Backlog items the omitted controls trace to (for the code comments)

Three hi-fi affordances are backend-blocked and are **not rendered**. Each omission needs a short code comment in `SchedulesPage.tsx` naming its backlog item:

- Filter chips (All / Enabled / Disabled) + free-text search input: `docs/backlog/idea-2026-06-05-schedules-filter-search.md`
- Fleet-wide ENABLED/PAUSED (the summary strip stays page-scoped): `docs/backlog/idea-2026-06-05-schedules-stats-endpoint.md`
- FAILED·24H summary stat: `docs/backlog/idea-2026-06-05-failed-24h-stat.md`

(All three files were confirmed to exist under `docs/backlog/`.)

### Commands

- Run one test file: `cd web && npx vitest run src/schedules/SchedulesTable.test.tsx`
- Run one test file: `cd web && npx vitest run src/schedules/SchedulesPage.test.tsx`
- Run one test by name: `cd web && npx vitest run src/schedules/SchedulesPage.test.tsx -t "renders schedules and the page-scoped summary"`
- Run all schedules + holo tests: `cd web && npx vitest run src/schedules src/components/holo`
- Typecheck: `cd web && npx tsc --noEmit`

Use PowerShell (the primary shell) or the Bash tool; either works for `cd web && ...`. Commit with the Bash tool (Git Bash) using a heredoc.

> **Note on `web/dist`:** `web/dist` is tracked but stale from the scaffold. Do **not** run a production build in these tasks (none is needed). If any step dirties `web/dist`, run `git checkout -- web/dist/` before assembling the commit so the diff stays to source only.

---

## Task 1: SchedulesTable - swap the container to GlassPanel, take over the empty state, add a footer slot

Restyle `SchedulesTable`'s container to `GlassPanel`, keep the `COLS` grid, the column-header row, and every cell exactly (inline enabled/paused dot, overlap pill, NEXT-RUN glyph, plain-text NAME and LAST JOB, Run-now + Enable/Disable actions with their `pendingId` disabling). Move the empty state into this component and add an optional `footer` slot rendered in BOTH the empty branch and the non-empty branch (the non-empty footer sits inside the `GlassPanel` under a `border-t`). The one intentional visual change to a cell: the NEXT-RUN play glyph retints from `text-accent` to `text-accent-b`.

**Files:**
- Modify: `web/src/schedules/SchedulesTable.tsx`
- Test: `web/src/schedules/SchedulesTable.test.tsx` (existing; add three assertions)

- [ ] **Step 1: Run the existing SchedulesTable tests to confirm the baseline is green**

Run: `cd web && npx vitest run src/schedules/SchedulesTable.test.tsx`
Expected: PASS (6 tests: renders core columns, enabled row shows Run now + Disable, disabled row shows Run now + Enable, clicking fires callbacks, pending row disables, missing last_job_id dash).

- [ ] **Step 2: Add the three structural assertions (they should fail against the current markup)**

Append these three tests to `web/src/schedules/SchedulesTable.test.tsx`, after the existing `missing last_job_id renders a dash` test. Note this file uses a plain `render` (no router) because `SchedulesTable` has no `Link` - keep that; do NOT add `MemoryRouter`.

```tsx
test('wraps the table in a GlassPanel surface', () => {
  render(<SchedulesTable schedules={[sched()]} pendingId={null} onRunNow={() => {}} onToggleEnabled={() => {}} />)
  // The GlassPanel base classes carry the gradient glass fidelity upgrade.
  const surface = screen.getByTestId('schedules-table')
  expect(surface).toHaveClass('rounded-card', 'border', 'border-border', 'backdrop-blur-[8px]')
})

test('renders a footer slot inside the table surface when rows are present', () => {
  render(
    <SchedulesTable
      schedules={[sched()]}
      pendingId={null}
      onRunNow={() => {}}
      onToggleEnabled={() => {}}
      footer={<span>FOOTER-MARKER</span>}
    />,
  )
  const surface = screen.getByTestId('schedules-table')
  const footer = screen.getByText('FOOTER-MARKER')
  expect(surface).toContainElement(footer)
})

test('renders the empty state and still shows the footer slot when there are no rows', () => {
  render(
    <SchedulesTable
      schedules={[]}
      pendingId={null}
      onRunNow={() => {}}
      onToggleEnabled={() => {}}
      footer={<span>FOOTER-MARKER</span>}
    />,
  )
  expect(screen.getByText('No schedules yet.')).toBeInTheDocument()
  expect(screen.getByText('FOOTER-MARKER')).toBeInTheDocument()
})
```

- [ ] **Step 3: Run the three new tests to verify they fail**

Run: `cd web && npx vitest run src/schedules/SchedulesTable.test.tsx -t "GlassPanel surface"`
Expected: FAIL - `Unable to find an element by: [data-testid="schedules-table"]` (the container is a flat `<div>` with no testid, no `backdrop-blur-[8px]`).

Run: `cd web && npx vitest run src/schedules/SchedulesTable.test.tsx -t "footer slot inside"`
Expected: FAIL - `SchedulesTable` does not accept a `footer` prop (TS error on the render, or `FOOTER-MARKER` not found).

Run: `cd web && npx vitest run src/schedules/SchedulesTable.test.tsx -t "empty state and still shows the footer"`
Expected: FAIL - `SchedulesTable` currently always renders the table shell (never the empty card) and takes no `footer`, so `No schedules yet.` is not found.

- [ ] **Step 4: Restyle SchedulesTable to GlassPanel + empty state + footer slot**

Rewrite `web/src/schedules/SchedulesTable.tsx` to the following. The `COLS` template, the column-header row, the inline enabled/paused dot, the CRON/TZ cells, the inline overlap pill, the NEXT-RUN cell, the LAST RUN / LAST JOB / OWNER cells, the paused-row `opacity-[0.55]` dimming, and the Run-now + Enable/Disable action buttons with their `pendingId` disabling are all preserved exactly. Changes: the container becomes `GlassPanel` with `data-testid="schedules-table"`; an empty branch renders a `GlassPanel` card (matching `WorkersPage`/`JobsTable`); an optional `footer` slot renders in both branches (non-empty: a `border-t` row inside the `GlassPanel`; empty: a plain `px-1` row under the card); and the NEXT-RUN play glyph retints from `text-accent` to `text-accent-b`.

```tsx
import type { ReactNode } from 'react'
import { GlassPanel } from '../components/holo'
import type { Schedule } from './api'
import { formatRelativeTime, nextRunDisplay, shortId } from './format'

const COLS = 'grid grid-cols-[1.4fr_120px_110px_90px_1fr_1fr_110px_1.3fr_150px]'

export function SchedulesTable({
  schedules,
  pendingId,
  onRunNow,
  onToggleEnabled,
  footer,
}: {
  schedules: Schedule[]
  pendingId: string | null
  onRunNow: (id: string) => void
  onToggleEnabled: (id: string, nextEnabled: boolean) => void
  footer?: ReactNode
}) {
  if (schedules.length === 0) {
    return (
      <div className="flex flex-col gap-4">
        <GlassPanel className="mx-auto mt-10 max-w-md p-6 text-center text-[13px] text-fg-mute">
          No schedules yet.
        </GlassPanel>
        {footer && <div className="px-1">{footer}</div>}
      </div>
    )
  }
  return (
    <GlassPanel data-testid="schedules-table">
      <div className={`${COLS} border-b border-border px-4 py-3 font-mono text-[10px] tracking-wider text-fg-mute`}>
        <span>NAME</span>
        <span>CRON</span>
        <span>TZ</span>
        <span>OVERLAP</span>
        <span>NEXT RUN</span>
        <span>LAST RUN</span>
        <span>LAST JOB</span>
        <span>OWNER</span>
        <span className="text-right">ACTIONS</span>
      </div>
      {schedules.map((s) => {
        const pending = pendingId === s.id
        return (
          <div
            key={s.id}
            className={`${COLS} items-center border-b border-border/40 px-4 py-2 font-mono text-[11.5px] ${s.enabled ? '' : 'opacity-[0.55]'}`}
          >
            <span className="flex min-w-0 items-center gap-2">
              <span className={`h-1.5 w-1.5 shrink-0 rounded-full ${s.enabled ? 'bg-ok' : 'bg-fg-dim'}`} />
              <span className="truncate font-sans text-[13px] text-fg">{s.name}</span>
            </span>
            <span className="text-fg">{s.cron_expr}</span>
            <span className="truncate text-[10.5px] text-fg-mute">{s.timezone}</span>
            <span>
              <span
                className={`rounded-full border border-border px-1.5 py-0.5 text-[9.5px] uppercase tracking-wider ${s.overlap_policy === 'allow' ? 'text-accent' : 'text-fg-mute'}`}
              >
                {s.overlap_policy}
              </span>
            </span>
            <span className={s.enabled ? 'text-fg' : 'text-fg-dim'}>
              {s.enabled ? <span className="text-accent-b">&#9658;</span> : null} {nextRunDisplay(s.next_run_at)}
            </span>
            <span className="text-fg-mute">{s.last_run_at ? formatRelativeTime(s.last_run_at) : '-'}</span>
            <span className="text-[10.5px] text-fg-mute">{shortId(s.last_job_id)}</span>
            <span className="truncate text-[10.5px] text-fg-mute">{s.owner_email}</span>
            <span className="flex justify-end gap-1.5">
              <button
                type="button"
                disabled={pending}
                onClick={() => onRunNow(s.id)}
                className="rounded-md border border-accent/50 bg-accent/15 px-2.5 py-1 text-[11px] text-fg disabled:opacity-40"
              >
                Run now
              </button>
              <button
                type="button"
                disabled={pending}
                onClick={() => onToggleEnabled(s.id, !s.enabled)}
                className="rounded-md border border-border bg-white/5 px-2.5 py-1 text-[11px] text-fg-mute disabled:opacity-40"
              >
                {s.enabled ? 'Disable' : 'Enable'}
              </button>
            </span>
          </div>
        )
      })}
      {footer && <div className="border-t border-border px-4 py-3">{footer}</div>}
    </GlassPanel>
  )
}
```

- [ ] **Step 5: Run the full SchedulesTable test file to verify all pass**

Run: `cd web && npx vitest run src/schedules/SchedulesTable.test.tsx`
Expected: PASS (9 tests: the 6 original behavior tests survive unchanged - name is still plain text, LAST JOB is still plain `shortId`, both actions render on every row, pending disables both - plus the 3 new structural tests).

- [ ] **Step 6: Typecheck**

Run: `cd web && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 7: Commit**

```bash
git checkout -- web/dist/ 2>/dev/null || true
git add web/src/schedules/SchedulesTable.tsx web/src/schedules/SchedulesTable.test.tsx
git commit -m "style(web): schedules table on GlassPanel with empty state + footer slot"
```

---

## Task 2: SchedulesPage - Eyebrow primitive + GlassPanel skeleton/error surfaces

Swap the inline eyebrow string for the `Eyebrow` primitive (matching `JobsPage`/`WorkersPage`) and restyle the loading-skeleton and error-with-retry surfaces to `GlassPanel`. The summary strip, sort control, action-error banner, and all pagination logic are untouched in this task. The existing `SchedulesPage.test.tsx` behavior tests (summary `2 schedules`, error banner + `Retry`) are the regression guard; no test edits are needed because the queries are by text/role, not classes or DOM shape.

**Files:**
- Modify: `web/src/schedules/SchedulesPage.tsx` (add import; eyebrow block SchedulesPage.tsx:134-137; skeleton block SchedulesPage.tsx:96-104; error block SchedulesPage.tsx:106-115)
- Test: `web/src/schedules/SchedulesPage.test.tsx` (existing; no change expected - run to confirm)

- [ ] **Step 1: Run the existing SchedulesPage tests to confirm the baseline is green**

Run: `cd web && npx vitest run src/schedules/SchedulesPage.test.tsx`
Expected: PASS (all tests: summary strip, empty state, error + retry, sort re-request, cursor walk, in-flight disabling, footer ranges, Disable PATCH).

- [ ] **Step 2: Add the `Eyebrow` and `GlassPanel` imports**

In `web/src/schedules/SchedulesPage.tsx`, after the existing imports (the last import is `import { computePageRange } from '../lib/pageRange'`), add:

```tsx
import { Eyebrow, GlassPanel } from '../components/holo'
```

- [ ] **Step 3: Replace the inline eyebrow string with the `Eyebrow` primitive**

In the returned JSX header (currently SchedulesPage.tsx:134-137), replace:

```tsx
        <div>
          <div className="font-mono text-[11px] tracking-widest text-fg-mute">RECURRING</div>
          <h1 className="text-[32px] font-normal tracking-tight">Schedules</h1>
        </div>
```

with:

```tsx
        <div>
          <Eyebrow>RECURRING</Eyebrow>
          <h1 className="text-[32px] font-normal tracking-tight">Schedules</h1>
        </div>
```

- [ ] **Step 4: Restyle the loading skeleton to `GlassPanel` tiles**

Replace the skeleton early-return (currently SchedulesPage.tsx:96-104):

```tsx
  if (isLoading && !data) {
    return (
      <div className="flex flex-col gap-2">
        {Array.from({ length: 6 }).map((_, i) => (
          <div key={i} className="h-10 rounded-card border border-border bg-white/5" />
        ))}
      </div>
    )
  }
```

with (each skeleton row becomes a `GlassPanel` tile, mirroring `JobsPage`'s `<GlassPanel key={i} className="h-9" />` skeleton idiom; keep the six-bar count and the `h-10` height):

```tsx
  if (isLoading && !data) {
    return (
      <div className="flex flex-col gap-2">
        {Array.from({ length: 6 }).map((_, i) => (
          <GlassPanel key={i} className="h-10" />
        ))}
      </div>
    )
  }
```

- [ ] **Step 5: Restyle the error surface to `GlassPanel`**

Replace the error early-return (currently SchedulesPage.tsx:106-115):

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

with (mirroring `JobsPage`/`WorkersPage`'s error card exactly):

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

- [ ] **Step 6: Run the SchedulesPage tests to verify they still pass (no behavior change)**

Run: `cd web && npx vitest run src/schedules/SchedulesPage.test.tsx`
Expected: PASS (unchanged count). The error+retry test (`shows the error state with a Retry button`) queries by text `Retry`, preserved; the summary test queries by text `2 schedules`, preserved.

- [ ] **Step 7: Typecheck**

Run: `cd web && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 8: Commit**

```bash
git checkout -- web/dist/ 2>/dev/null || true
git add web/src/schedules/SchedulesPage.tsx
git commit -m "style(web): schedules page eyebrow + glass skeleton/error surfaces"
```

---

## Task 3: SchedulesPage - drop the empty early-return, move the pagination footer inside the table, add the SORT segment and omission comments

Remove the `SchedulesPage` empty early-return (the empty card now lives in `SchedulesTable` from Task 1), always render `<SchedulesTable>`, and move the detached pagination footer into `SchedulesTable`'s `footer` slot so it renders inside the table `GlassPanel` (and still shows on an empty page). Add the `· SORT <sort>` segment to the footer to match the hi-fi. Add the omission comment block tracing the backend-blocked filter chips / search / FAILED-24H stat to their backlog items. All pagination logic (`cursorStack`/`startOffset`/`offsets`, `goNext`/`goPrev`, `computePageRange`, disabled conditions) stays in `SchedulesPage` **verbatim** - only where the footer JSX renders moves, and the SORT segment is a display-only addition.

**Files:**
- Modify: `web/src/schedules/SchedulesPage.tsx` (remove the empty early-return SchedulesPage.tsx:117-124; move the footer JSX into a `footer` prop on `<SchedulesTable>`; add the SORT segment; add omission comments)
- Test: `web/src/schedules/SchedulesPage.test.tsx` (existing pagination + empty-state tests are the guard; add one light "no filter/search/FAILED-24H rendered" assertion)

- [ ] **Step 1: Run the schedules tests to confirm the baseline (post Task 1 and 2) is green**

Run: `cd web && npx vitest run src/schedules`
Expected: PASS (SchedulesTable 9 tests, SchedulesPage full set).

- [ ] **Step 2: Add the omission-guard assertion**

Append to `web/src/schedules/SchedulesPage.test.tsx` (after the `renders schedules and the page-scoped summary` test) a guard that the backend-blocked hi-fi controls are not rendered. `http`, `HttpResponse`, `server`, `renderWithQuery`, and `page` are already imported/defined at the top of the file.

```tsx
test('does not render the backend-blocked filter chips, search, or FAILED-24H stat', async () => {
  server.use(http.get('/v1/scheduled-jobs', () => HttpResponse.json(page)))
  renderWithQuery(<SchedulesPage />)
  await screen.findByText('nightly-build')
  // Omitted per spec: All/Enabled/Disabled filter chips, free-text search, FAILED-24H stat.
  expect(screen.queryByRole('button', { name: /^enabled$/i })).toBeNull()
  expect(screen.queryByRole('button', { name: /^disabled$/i })).toBeNull()
  expect(screen.queryByRole('searchbox')).toBeNull()
  expect(screen.queryByText(/failed.*24h/i)).toBeNull()
})
```

- [ ] **Step 3: Run the omission-guard assertion to verify it passes immediately**

Run: `cd web && npx vitest run src/schedules/SchedulesPage.test.tsx -t "backend-blocked"`
Expected: PASS immediately - the current page renders none of those controls. This is a regression guard, not a red-then-green step; it locks in the intended omission so a future edit that adds a dead control fails here. (Note: the `Disable`/`Enable` row action buttons are named `Disable`/`Enable`, so the anchored `/^enabled$/i` / `/^disabled$/i` chip matchers do not collide with them.)

- [ ] **Step 4: Remove the empty early-return in `SchedulesPage`**

The empty state now lives in `SchedulesTable`. Delete the empty early-return block (currently SchedulesPage.tsx:117-124):

```tsx
  const schedules = data?.items ?? []
  if (schedules.length === 0) {
    return (
      <div className="mx-auto mt-10 max-w-md rounded-card border border-border bg-white/5 p-6 text-center text-[13px] text-fg-mute">
        No schedules yet.
      </div>
    )
  }
```

and replace it with just the `schedules` binding (the early-return is gone; `SchedulesTable` renders the empty card when `schedules` is empty):

```tsx
  const schedules = data?.items ?? []
```

- [ ] **Step 5: Add the omission comment block above the header row**

In `web/src/schedules/SchedulesPage.tsx`, immediately inside the returned `<div className="flex flex-col gap-4">` and before the `<div className="flex flex-wrap items-end gap-6">` header row, add:

```tsx
      {/*
        The hi-fi HoloSchedules also shows filter chips (All/Enabled/Disabled), a
        free-text search input, and a FAILED-24H summary stat. All three are
        backend-blocked and deliberately omitted here (a dead list control or a
        fabricated stat reads as broken):
          - filter chips + search: docs/backlog/idea-2026-06-05-schedules-filter-search.md
          - FAILED-24H stat:       docs/backlog/idea-2026-06-05-failed-24h-stat.md
        The ENABLED/PAUSED summary strip below is page-scoped (counts only the
        loaded page) until the stats endpoint lands:
          - fleet-wide counts:     docs/backlog/idea-2026-06-05-schedules-stats-endpoint.md
      */}
```

- [ ] **Step 6: Move the footer JSX into `<SchedulesTable>` and add the SORT segment**

In `web/src/schedules/SchedulesPage.tsx`, replace the current `<SchedulesTable ... />` element and the detached footer `<div>` that follows it (currently SchedulesPage.tsx:166-195):

```tsx
      <SchedulesTable
        schedules={schedules}
        pendingId={pendingId}
        onRunNow={onRunNow}
        onToggleEnabled={onToggleEnabled}
      />

      <div className="flex items-center justify-between font-mono text-[10.5px] tracking-wide text-fg-mute">
        <span>
          SHOWING <span className="text-fg">{x}-{y} of {total}</span> · OWNED + ADMINISTRATIVE
        </span>
        <div className="flex gap-1.5">
          <button
            type="button"
            disabled={cursorStack.length === 0 || isPlaceholderData}
            onClick={goPrev}
            className="rounded-full border border-border px-3 py-1 text-[11px] text-fg-mute disabled:opacity-40"
          >
            ← prev
          </button>
          <button
            type="button"
            disabled={!data?.next_cursor || isPlaceholderData}
            onClick={goNext}
            className="rounded-full border border-border px-3 py-1 text-[11px] text-fg-mute disabled:opacity-40"
          >
            next 50 →
          </button>
        </div>
      </div>
```

with the footer passed into the table (the prev/next markup is identical; the `· SORT <sort>` segment is added between the range and the `OWNED + ADMINISTRATIVE` scope tail, rendering the current `sort` value token in `text-accent-b` to match the hi-fi):

```tsx
      <SchedulesTable
        schedules={schedules}
        pendingId={pendingId}
        onRunNow={onRunNow}
        onToggleEnabled={onToggleEnabled}
        footer={
          <div className="flex items-center justify-between font-mono text-[10.5px] tracking-wide text-fg-mute">
            <span>
              SHOWING <span className="text-fg">{x}-{y} of {total}</span>
              {' · '}SORT <span className="text-accent-b">{sort}</span> · OWNED + ADMINISTRATIVE
            </span>
            <div className="flex gap-1.5">
              <button
                type="button"
                disabled={cursorStack.length === 0 || isPlaceholderData}
                onClick={goPrev}
                className="rounded-full border border-border px-3 py-1 text-[11px] text-fg-mute disabled:opacity-40"
              >
                ← prev
              </button>
              <button
                type="button"
                disabled={!data?.next_cursor || isPlaceholderData}
                onClick={goNext}
                className="rounded-full border border-border px-3 py-1 text-[11px] text-fg-mute disabled:opacity-40"
              >
                next 50 →
              </button>
            </div>
          </div>
        }
      />
```

> **Preserve check:** the `goPrev`/`goNext` `disabled` expressions (`cursorStack.length === 0 || isPlaceholderData` and `!data?.next_cursor || isPlaceholderData`), the `{x}-{y} of {total}` range, and the `OWNED + ADMINISTRATIVE` scope tail are copied character-for-character. The only additions are the `{' · '}SORT <span className="text-accent-b">{sort}</span>` segment. Do not simplify the disabled expressions.

- [ ] **Step 7: Run the full schedules test suite to verify everything passes**

Run: `cd web && npx vitest run src/schedules`
Expected: PASS. The pagination tests (`pagination footer shows 1-N of total`, `partial last page`, `restores prior range`, `next and prev are disabled while a page fetch is in flight`, `next/prev pagination walks the cursor`) query the footer by text (`/1-50 of 120/i`, `/51-63 of 63/i`) and by button role (`/prev/i`, `/next/i`); the range assertions use substring matchers, so the inserted `· SORT <sort>` segment does not break them, and moving the footer inside the `GlassPanel` does not change text/role queries. The empty-state test (`shows the empty state when there are no schedules`) still finds `No schedules yet.` (now rendered by `SchedulesTable`). The Disable-PATCH and summary tests are unaffected. The new omission-guard test passes.

- [ ] **Step 8: Typecheck**

Run: `cd web && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 9: Restore `web/dist` if the test run touched it, then commit**

```bash
git checkout -- web/dist/ 2>/dev/null || true
git add web/src/schedules/SchedulesPage.tsx web/src/schedules/SchedulesPage.test.tsx
git commit -m "style(web): move schedules pagination footer inside the table glass panel"
```

---

## Final verification

- [ ] **Run all schedules + holo tests together**

Run: `cd web && npx vitest run src/schedules src/components/holo`
Expected: PASS across the board.

- [ ] **Typecheck the whole web package**

Run: `cd web && npx tsc --noEmit`
Expected: no errors.

- [ ] **Confirm no unintended files are staged**

Run: `git status`
Expected: only `web/src/schedules/SchedulesTable.tsx`, `web/src/schedules/SchedulesPage.tsx`, `web/src/schedules/SchedulesTable.test.tsx`, `web/src/schedules/SchedulesPage.test.tsx` across the three commits. `web/dist` clean. No changes to `useSchedules.ts`, `useScheduleActions.ts`, `format.ts`, `api.ts`, or any Go file.

---

## Self-review against the spec

- **Eyebrow inline string -> `Eyebrow` primitive** (spec section 1) - Task 2, Step 3.
- **Summary strip kept page-scoped inline, `KpiStat`/FAILED-24H not added** (spec section 2) - unchanged; no task modifies `countEnabled`/`total`; the `2 schedules` test guards it, and the omission comment (Task 3, Step 5) traces the page-scoped limitation and the missing FAILED-24H stat to their backlog items.
- **Sort control native `<select aria-label="Sort">` unchanged, always enabled, no filter-disabled rule** (spec section 3) - not modified; guarded by `selectOptions(getByLabelText('Sort'), 'name')`.
- **Filter chips + search NOT rendered, traced to backlog** (spec section 3 / backend-blocked) - Task 3, Step 5 comment; guarded by the `backend-blocked` omission test.
- **Table container flat `bg-white/5` -> `GlassPanel`** (spec section 4 / SchedulesTable restyle) - Task 1, Step 4; guarded by the `GlassPanel surface` assertion.
- **Empty state -> `GlassPanel` and moved into `SchedulesTable` so the footer shows on empty** (spec section 4 + jobs-list gap learning) - Task 1 (empty branch + footer in empty branch), Task 3 Step 4 (remove page early-return); guarded by the `empty state and still shows the footer` assertion and the `shows the empty state` page test.
- **Loading skeleton -> `GlassPanel` tone, six bars kept** (spec section 4) - Task 2, Step 4.
- **Pagination footer moves inside the table `GlassPanel` as a `border-t` row, logic verbatim, prev/next kept as plain compact `<button>`s (`PillButton` not used)** (spec section 5) - Task 3, Steps 6; preserve check in Step 6; guarded by all pagination tests plus the footer-slot assertions.
- **Footer gains `· SORT <sort>` segment in `text-accent-b`** (spec section 5 / changed list) - Task 3, Step 6.
- **`OWNED + ADMINISTRATIVE` scope tail preserved** (spec section 5) - Task 3, Step 6 preserve check.
- **NEXT-RUN play glyph retinted `text-accent` -> `text-accent-b`** (SchedulesTable restyle / changed list) - Task 1, Step 4.
- **NAME plain text (no detail route), LAST JOB plain `shortId` (no job link), no chevron column** (spec "No detail route" / preserved) - preserved verbatim in Task 1, Step 4; guarded by the `renders core columns` and `missing last_job_id renders a dash` tests.
- **Inline enabled/paused dot kept (`StatusDot` not used), inline overlap pill kept (`Chip` not used), paused-row `opacity-[0.55]`, no running-row tint** (SchedulesTable restyle) - preserved verbatim in Task 1, Step 4.
- **Run now on EVERY row + Enable/Disable, `pendingId` disabling, `onRunNow`/`onToggleEnabled` wiring** (hard-preserve) - preserved verbatim in Task 1, Step 4; guarded by `enabled row shows Run now + Disable`, `disabled row shows Run now + Enable`, `clicking Run now and Disable fires callbacks`, `pending row disables`.
- **Action-error banner restyled not removed** (preserved list) - unchanged; no task touches it.
- **No hook/query-key/pagination-state/sort/action change** - hard-preserve section; `useSchedules.ts`, `useScheduleActions.ts`, `format.ts`, `api.ts` are never opened for edit.
- **Only `Eyebrow` + `GlassPanel` consumed; the other six primitives not imported** (Deliberately NOT used) - only `../components/holo` `{ Eyebrow, GlassPanel }` is imported in both files.
- **No new tests strictly required; light structural guards added only where they lock in an intended change** (spec Test impact) - added exactly four: `GlassPanel surface`, footer-in-non-empty, footer-in-empty + `No schedules yet.`, plus the omission guard.
