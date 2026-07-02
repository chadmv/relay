# Job Detail Holo Relayout Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restyle the shipped Job detail page (`JobDetailPage.tsx` plus `TaskDag`, `TasksTable`, `SpecTab`, `LogTab`, `JobActions`) onto the merged Holo primitives (`GlassPanel`, `Eyebrow`, `Chip`, `PillButton`), matching the hi-fi `HoloJobDetail`, with zero change to any data path, query key, polling rule, task-selection state machine, progress derivation, cancel flow, or the just-landed SpecTab null-safety.

**Architecture:** Frontend-only, presentational relayout of six existing components plus the page shell. Every flat `bg-white/5` surface becomes a `GlassPanel`; the header adopts the breadcrumb idiom from `WorkerDetailPage`; the labels row becomes `Chip`s; the `JobActions` buttons become `PillButton`s. The fixed 55/45 two-column split is kept (the accessible drag-resizer stays a filed follow-up). Unbacked hi-fi elements (elapsed/ETA, image/runtime/cluster/parallelism, per-task duration/%, live-follow log, donut) are omitted, not faked, each traced to a backlog item via a code comment. The static Log tab gains an honest `STATIC` / `HISTORY` marker and a "live tailing pending" note (never a fake `LIVE` badge). The `dagLayout` engine and `TaskDag` SVG are kept and token-restyled (the mock's coordinate-baked `DAGSVG` is not adopted).

**Tech Stack:** React 18 + TypeScript, Tailwind v4 (literal class strings so the JIT includes them), TanStack Query, React Router, Vitest + Testing Library + MSW.

---

## Slice independence

This is a **frontend-only** slice. It touches no Go code and adds no endpoints, no `.sql`, no `.proto`, no migration. There is no backend counterpart, so there is nothing to run in parallel: Phase 3 runs a single frontend track. This slice depends only on the already-merged Holo primitives (`web/src/components/holo/`) and on the already-shipped Job detail page. **For the conductor: no frontend/backend parallelism to schedule; Phase 3 is a single sequential frontend track.**

The backlog items that would unblock the omitted elements (`feature-2026-06-26-sse-task-log-publishing.md`, `feature-2026-07-01-job-detail-timing-enrichment.md`, `feature-2026-07-01-per-task-timing.md`, `idea-2026-07-01-job-detail-resizable-split.md`) are explicitly **out of scope** for this plan; this plan only references them in code comments.

## Hard-preserve constraints (do NOT break these)

This page has real logic - a polling hook, a second decoupled log query, a task-selection state machine, a derived-progress computation, a gated cancel flow, and a just-fixed null-safety guard. Unlike the pure list restyles, a careless edit here can silently break behavior that the tests only partially cover. The following are load-bearing and MUST behave identically after every task. If a step would change any of these, the step is wrong.

- **`useJob` polling + query key.** `useJob(id)` (`web/src/jobs/useJob.ts`) with key `['job', id]`, `refetchInterval`, `placeholderData: keepPreviousData`. This hook file is **not modified**. The 3s poll is what keeps task status/progress live without SSE.
- **`useTaskLogs` STATIC fetch-once, decoupled key, caller-gated.** `useTaskLogs(taskId, enabled)` (`web/src/jobs/useTaskLogs.ts`) keeps its `['task-logs', taskId]` key **deliberately OFF the `['job', ...]` prefix** (a job poll invalidation must never disturb the log query), `staleTime: Infinity`, and no `refetchInterval`. The page's `enabled` gate stays exactly `selectedTaskId !== '' && tab === 'log'` - so a log is never fetched for an unopened task and never fetched while the Spec tab is active. This hook file is **not modified**.
- **Task-selection state machine.** `defaultTaskId(tasks)` picks the first `running`/`failed`/`timed_out` task, else the first task, else `''`. `selectedTaskId` = an explicit `pickedTaskId` if it still matches a live task, else the default (a poll that changes the task list re-derives selection cleanly). `selectedTask = tasks.find(...)` drives both the Spec pane and (Log tab active) `useTaskLogs`. Copy this logic verbatim; do not "clean it up".
- **Derive-progress-from-tasks.** `done` = count `status === 'done'`, `total` = `tasks.length`, `active` = count `running`/`dispatched`, `pct = progressPct(done, total)`. The detail endpoint returns NO `total_tasks`/`done_tasks`/`started_at`/`finished_at` (list-only fields), so progress is derived client-side. The `derives progress from the tasks array (1 of 2 done)` test asserts `/1\s*\/\s*2 tasks done/i` - the progress text must keep that exact shape.
- **`JobActions` behavior.** The two buttons (`Cancel`, `Force cancel`), the `useJobActions` cancel/force mutations (one mutation, `force` is its argument), the `ConfirmDialog` confirm flow (primary label `Cancel job`/`Force cancel`), the inline error banner, the terminal-state hiding (`cancelled`/`done` hide the buttons; `failed` STAYS cancellable), and the `canManage = is_admin || submitted_by === user.id` gate all stay exactly. There is **no revoke** on jobs (revoke is a worker concept); do not add one. The three-key invalidation (`['job', id]`, `['jobs']`, `['job-stats']`) in `useJobActions` is **unchanged** - `web/src/jobs/useJobActions.ts` is not modified. Buttons may be restyled to `PillButton` variants; the `getByRole('button', { name: 'Cancel' | 'Force cancel' })` surface is unaffected.
- **CRITICAL - SpecTab null-safety (PR #96).** `env`/`requires`/`commands` are nullable on the wire (the server returns `null`, not `{}`/`[]`, for an omitted field - `json.Marshal(nil map)` is `null`). The guards `Object.entries(task.env ?? {})`, `Object.entries(task.requires ?? {})`, `task.commands ?? []` MUST NOT regress - a regression re-blanks the whole job-detail page. The `renders placeholders when the API returns null env/requires/commands` test asserts exactly three `(none)` placeholders and is the guard.
- **`TasksTable` selection semantics.** Rows are selection controls, not navigation: `role="row"` + `aria-selected` on each row `<button>`, `onSelect(t.id)` on click. `queryByRole('link')` must stay absent (no anchors in a row). Columns stay `NAME / STATUS / RETRY / WORKER / DEPS` - **no** per-task `pct`/`dur` column. The `worker_id` cell stays plain text (the worker-cell link is a deferred follow-up).
- **`TaskDag` engine + a11y + cycle safety.** The `dagLayout` layout engine (`web/src/jobs/dagLayout.ts`) with its longest-path layering, unknown-dep filtering, and visited-guard cycle break is **not modified**. The SVG keeps `role="img"` + the summarizing `aria-label` (`Task dependency graph: N tasks, M dependency edges`). Only token classes on the existing SVG change; the mock's coordinate-baked `DAGSVG` is not adopted.
- **`LogTab` static behavior + stream distinction.** Static, fetch-once, no SSE, no follow toggle, no auto-scroll. The stdout/stderr color distinction (`text-err` for stderr) is preserved (asserted). Loading / empty (`No log output.`) / error-with-retry states preserved.
- **Derive/format helpers.** `statusColor` / `progressPct` (`web/src/jobs/status.ts`) for the job status dot + progress, `taskStatusColor` (`web/src/jobs/taskStatus.ts`) for task/DAG statuses. Both files are **not modified**. Do NOT route jobs or tasks through the worker `StatusDot` primitive (it is hard-wired to `WorkerStatus`/`livenessView` and would misclassify the `JobStatus`/`TaskStatus` vocabularies). Keep the inline dot + label. A shared generic status-dot primitive is a deferred follow-up.
- **Tab roles + default.** The `role="tablist"` with `Spec` and `Log` tabs (`getByRole('tab', { name: /log/i })`) and default-to-Spec are preserved.
- **Loading / 404 / error-with-retry / empty states** are preserved (restyled to `GlassPanel`, not removed). The `job-actions` `data-testid` slot must survive (tests query it).

## Primitives used vs deliberately NOT used

**Used** (all already merged and tested; import from the barrel `web/src/components/holo`):

- `GlassPanel({ as?, className?, children, ...rest })` - `web/src/components/holo/GlassPanel.tsx`. Gradient glass surface; base classes are literal; a caller `className` **appends** after the base; `...rest` spreads onto the tag so `role`/`aria-*`/`data-testid` pass through. Replaces every flat `bg-white/5`.
- `Eyebrow({ children, className? })` - `web/src/components/holo/Eyebrow.tsx`. Mono uppercase micro-label; section-label variant via `className="text-[10px] tracking-[0.16em]"`.
- `Chip({ children, tone?, dashed?, onClick? })` - `web/src/components/holo/Chip.tsx`. `accent` tone (default) fits the job labels directly. Renders a `<span>` when no `onClick`.
- `PillButton({ variant?, ...buttonProps })` - `web/src/components/holo/PillButton.tsx`. `ghost` variant for Cancel, `danger` variant for Force cancel. It is a `<button type="button">`; all button attributes (`disabled`, `onClick`) pass through.

**Deliberately NOT used** (merged and available, but a mismatch here - do not import them into these files):

- `StatusDot` - hard-wired to `WorkerStatus`/`livenessView`; jobs and tasks have their own `JobStatus`/`TaskStatus` maps in `status.ts`/`taskStatus.ts`. Keep the inline dot + label. (This page is arguably the third status-dot consumer; extracting a generic primitive is a deferred follow-up, NOT part of this plan.)
- `ProgressBar` - only `accent`/`muted` tones (confirmed in `web/src/components/holo/ProgressBar.tsx`); the job bar needs per-status `done`->ok / `failed`->err / else->accent fills it lacks. Keep the inline status-toned bar.
- `KpiStat` - the hi-fi Elapsed/ETA/Tasks/Owner stat block is mostly unbacked (Elapsed/ETA do not exist on the detail endpoint). Do not render a stat grid of mock values. The two real numbers (Tasks done/total, Owner) already live in the header sub-line and progress line.
- `Panel` - optional for the DAG/tasks panel headers, but a custom grid header is cleaner for the tasks list (same call as the jobs-list table). This plan uses inline `GlassPanel` + a `border-b` header row rather than `Panel`, to keep the header meta (derived counts) inline with the panel body.

## Omitted hi-fi elements and their backlog traces (for code comments)

Each omission gets a short code comment in the component where the hi-fi would have placed it. Exact paths (verified to exist):

- **Elapsed / ETA / overall duration / STARTED** (job header stat block): `docs/backlog/feature-2026-07-01-job-detail-timing-enrichment.md` - the detail endpoint returns no `started_at`/`finished_at`.
- **Per-task DUR / per-task %** (tasks table columns): `docs/backlog/feature-2026-07-01-per-task-timing.md` - the task response carries no per-task timing.
- **image / runtime / cluster / parallelism / source** (spec pane header rows): no backlog item warranted (mock inventions, not deferred features); a one-line comment where the hi-fi would place them.
- **Live log stream / LIVE badge / Follow tail / full-screen log route** (Log tab): `docs/backlog/feature-2026-06-26-sse-task-log-publishing.md` (backend enabler) and `docs/backlog/feature-2026-06-26-task-log-view-sse-tailing.md` (web consumer).
- **Resizable split** (fixed 55/45 kept): `docs/backlog/idea-2026-07-01-job-detail-resizable-split.md`.

## Context the engineer needs before starting

Read these before Task 1; hold them in context throughout.

- **Restyle targets** (all under `web/src/jobs/`): `JobActions.tsx`, `SpecTab.tsx`, `TasksTable.tsx`, `TaskDag.tsx`, `LogTab.tsx`, `JobDetailPage.tsx`.
- **Unchanged data/logic** (do NOT edit any of these): `web/src/jobs/useJob.ts`, `web/src/jobs/useTaskLogs.ts`, `web/src/jobs/useJobActions.ts`, `web/src/jobs/dagLayout.ts`, `web/src/jobs/status.ts`, `web/src/jobs/taskStatus.ts`, `web/src/jobs/api.ts`. `web/src/components/ConfirmDialog.tsx` and `web/src/components/Button.tsx` are also unchanged (used as-is).
- **Styling reference** (already on the primitives): `web/src/workers/WorkerDetailPage.tsx` - the breadcrumb header idiom (`← Workers / <name>` with an inline status on the right, then a mono sub-line), the `GlassPanel` skeleton (`<GlassPanel className="h-40" />`), and the error card (`<GlassPanel className="mx-auto mt-10 max-w-md p-6 text-center">`). Mirror these exact patterns. `web/src/jobs/JobsTable.tsx` shows the just-shipped list restyle pattern: `GlassPanel data-testid` container, `COLS` grid preserved, running-row `bg-accent/[0.04]` tint, status dot from `status.ts`.
- **Tests to keep green** (behavior is the regression guard; touch only where a structural query breaks): `web/src/jobs/JobActions.test.tsx`, `web/src/jobs/SpecTab.test.tsx`, `web/src/jobs/TasksTable.test.tsx`, `web/src/jobs/TaskDag.test.tsx`, `web/src/jobs/LogTab.test.tsx`, `web/src/jobs/JobDetailPage.test.tsx`. Their queries are by visible text, `role`, `aria-*`, and `data-testid` - all preserved by this restyle. Only add a **light** structural or behavioral assertion where it locks in an intended change (the null-safety guard, the `STATIC`/pending marker, the omission guards).

### Commands

- Run one test file: `cd web && npx vitest run src/jobs/JobActions.test.tsx`
- Run one test by name: `cd web && npx vitest run src/jobs/SpecTab.test.tsx -t "null env/requires/commands"`
- Run all jobs + holo tests: `cd web && npx vitest run src/jobs src/components/holo`
- Typecheck: `cd web && npx tsc --noEmit`

Use PowerShell (the primary shell) or the Bash tool; either works for `cd web && ...`. Commit with the Bash tool (Git Bash) using a heredoc, not a PowerShell here-string.

> **Note on `web/dist`:** `web/dist` is tracked but stale from the scaffold. Do **not** run a production build in these tasks (none is needed). If any step dirties `web/dist`, run `git checkout -- web/dist/` before assembling the commit so the diff stays to source only.

### Task collision ordering

Tasks are sequenced to avoid file collisions: the leaf components first (each edits one component file plus its test), then the page shell last (Task 6 edits `JobDetailPage.tsx` and its test only, importing the already-restyled children). Run tasks in order.

---

## Task 1: JobActions - restyle the two buttons to PillButton (behavior unchanged)

Swap the two inline `rounded-md border ...` buttons for `PillButton` (`ghost` for Cancel, `danger` for Force cancel). The `useJobActions` mutations, the `ConfirmDialog` flow, the inline error banner, the terminal-state hiding, and every `name`-based test query are unaffected. This is a pure visual swap of two elements.

**Files:**
- Modify: `web/src/jobs/JobActions.tsx` (import; the two `<button>` elements at lines 54-69)
- Test: `web/src/jobs/JobActions.test.tsx` (existing; no change expected - run to confirm)

- [ ] **Step 1: Run the existing JobActions tests to confirm the baseline is green**

Run: `cd web && npx vitest run src/jobs/JobActions.test.tsx`
Expected: PASS (all tests: buttons present, graceful/force DELETE, confirm dialog, dismiss/Escape, terminal-state hiding, three-key invalidation, 409 inline banner).

- [ ] **Step 2: Add the `PillButton` import**

In `web/src/jobs/JobActions.tsx`, after the existing imports (the last is `import type { JobDetail } from './api'`), add:

```tsx
import { PillButton } from '../components/holo'
```

- [ ] **Step 3: Replace the two inline buttons with PillButton**

Replace the current button block (lines 52-71, the `{!terminal && ( ... )}` region's inner two `<button>`s):

```tsx
      {!terminal && (
        <div className="flex items-center gap-2">
          <button
            type="button"
            disabled={cancel.isPending}
            onClick={() => openConfirm('cancel')}
            className="rounded-md border border-border bg-white/5 px-3 py-1.5 text-[12px] text-fg-mute disabled:opacity-40"
          >
            Cancel
          </button>
          <button
            type="button"
            disabled={cancel.isPending}
            onClick={() => openConfirm('force')}
            className="rounded-md border border-err/50 bg-err/10 px-3 py-1.5 text-[12px] text-err disabled:opacity-40"
          >
            Force cancel
          </button>
        </div>
      )}
```

with (the `ghost`/`danger` variants carry the same visual intent - neutral vs destructive - and `PillButton` already sets `type="button"` and `disabled:opacity-40`):

```tsx
      {!terminal && (
        <div className="flex items-center gap-2">
          <PillButton variant="ghost" disabled={cancel.isPending} onClick={() => openConfirm('cancel')}>
            Cancel
          </PillButton>
          <PillButton variant="danger" disabled={cancel.isPending} onClick={() => openConfirm('force')}>
            Force cancel
          </PillButton>
        </div>
      )}
```

- [ ] **Step 4: Run the JobActions tests to verify they still pass (no behavior change)**

Run: `cd web && npx vitest run src/jobs/JobActions.test.tsx`
Expected: PASS (unchanged count). The button queries are by `name` (`'Cancel'`, `'Force cancel'`), preserved; `disabled` behavior and `onClick` are preserved by `PillButton` passing them through.

- [ ] **Step 5: Typecheck**

Run: `cd web && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git checkout -- web/dist/ 2>/dev/null || true
git add web/src/jobs/JobActions.tsx
git commit -m "style(web): job actions buttons on PillButton"
```

---

## Task 2: SpecTab - restyle to GlassPanel section styling, KEEP the null-guards

Restyle the COMMANDS / ENV / REQUIRES sections to the hi-fi token look (mono, the `$` accent prompt on command lines, a `GlassPanel` command block instead of flat `bg-black/20`). The null-safety guards (`task.env ?? {}`, `task.requires ?? {}`, `task.commands ?? []`) and the three `(none)` placeholders MUST stay. We reinforce the guard with the existing test plus one explicit reinforcement.

**Files:**
- Modify: `web/src/jobs/SpecTab.tsx`
- Test: `web/src/jobs/SpecTab.test.tsx` (existing null-safety test is the guard; reinforce with a stronger assertion)

- [ ] **Step 1: Run the existing SpecTab tests to confirm the baseline is green**

Run: `cd web && npx vitest run src/jobs/SpecTab.test.tsx`
Expected: PASS (4 tests: command lines, env/requires entries, no-task placeholder, null env/requires/commands -> three `(none)`).

- [ ] **Step 2: Strengthen the null-safety regression assertion (should still pass; locks in PR #96)**

Replace the existing `renders placeholders when the API returns null env/requires/commands` test in `web/src/jobs/SpecTab.test.tsx` (currently lines 35-39) with a version that also asserts the page did NOT throw/blank (the task name still renders and no error boundary swallowed the tree):

```tsx
// The real GET /v1/jobs/:id returns env/requires/commands as `null` (not `{}`/`[]`)
// for a task that omits them - json.Marshal(nil map/slice) => null, passed through
// server-side. PR #96 added the `?? {}` / `?? []` guards; without them Object.entries
// throws on null and blanks the whole job-detail page. This test is the regression
// guard: the restyle must keep the guards.
test('renders placeholders when the API returns null env/requires/commands', () => {
  const bare = { ...task, commands: null, env: null, requires: null } as unknown as TaskDetail
  render(<SpecTab task={bare} />)
  // All three sections fall back to "(none)" rather than throwing.
  expect(screen.getAllByText('(none)')).toHaveLength(3)
  // And the section labels still render, proving the tree did not blank.
  expect(screen.getByText('COMMANDS')).toBeInTheDocument()
  expect(screen.getByText('ENV')).toBeInTheDocument()
  expect(screen.getByText('REQUIRES')).toBeInTheDocument()
})
```

- [ ] **Step 3: Run the reinforced test to confirm it passes against the current (pre-restyle) code**

Run: `cd web && npx vitest run src/jobs/SpecTab.test.tsx -t "null env/requires/commands"`
Expected: PASS - the current `SpecTab` already renders `COMMANDS`/`ENV`/`REQUIRES` labels and three `(none)` placeholders. This confirms the reinforced assertion is a valid guard before the restyle.

- [ ] **Step 4: Restyle SpecTab to GlassPanel section styling, keeping the guards verbatim**

Rewrite `web/src/jobs/SpecTab.tsx` to the following. The `env ?? {}` / `requires ?? {}` / `commands ?? []` guards, the `(none)` placeholders, the `$ ` command prompt, and the no-task placeholder are preserved exactly; only the surfaces move to `GlassPanel` and section labels adopt the `Eyebrow` section-label variant. The command block gets the hi-fi `$` accent prompt.

```tsx
import type { TaskDetail } from './api'
import { Eyebrow, GlassPanel } from '../components/holo'

// Renders the selected task's spec: commands, env, requires. No per-task source
// block (handleGetJob does not echo `source`); the hi-fi image/runtime/cluster/
// source rows are also omitted - JobDetail/TaskDetail return none of them (mock
// inventions, not deferred features).
export function SpecTab({ task }: { task: TaskDetail | undefined }) {
  if (!task) {
    return <div className="p-4 text-[12px] text-fg-mute">Select a task to view its spec.</div>
  }
  // env/requires/commands are nullable on the wire (see TaskDetail); coerce to
  // empty so an omitted field renders "(none)" instead of throwing. DO NOT REMOVE
  // these guards - PR #96; a regression re-blanks the whole job-detail page.
  const env = Object.entries(task.env ?? {})
  const requires = Object.entries(task.requires ?? {})
  const commands = task.commands ?? []
  return (
    <div className="flex flex-col gap-4 p-4">
      <section>
        <Eyebrow className="mb-1 text-[10px] tracking-[0.16em]">COMMANDS</Eyebrow>
        <GlassPanel className="flex flex-col gap-1 p-3 font-mono text-[11.5px] text-fg">
          {commands.length === 0 ? (
            <span className="text-fg-mute">(none)</span>
          ) : (
            commands.map((cmd, i) => (
              <div key={i}>
                <span className="text-accent">$</span> {cmd.join(' ')}
              </div>
            ))
          )}
        </GlassPanel>
      </section>
      <section>
        <Eyebrow className="mb-1 text-[10px] tracking-[0.16em]">ENV</Eyebrow>
        <GlassPanel className="p-3 font-mono text-[11.5px] text-fg-mute">
          {env.length === 0 ? '(none)' : env.map(([k, v]) => <div key={k}>{k}={v}</div>)}
        </GlassPanel>
      </section>
      <section>
        <Eyebrow className="mb-1 text-[10px] tracking-[0.16em]">REQUIRES</Eyebrow>
        <GlassPanel className="p-3 font-mono text-[11.5px] text-fg-mute">
          {requires.length === 0 ? '(none)' : requires.map(([k, v]) => <div key={k}>{k}={v}</div>)}
        </GlassPanel>
      </section>
    </div>
  )
}
```

> **Preserve check:** the `Eyebrow` uppercases via CSS, so the label text (`COMMANDS`/`ENV`/`REQUIRES`) is passed as-is and still matches `getByText('COMMANDS')`. The `$ ` prompt is now `<span className="text-accent">$</span> {cmd.join(' ')}` - the `renders each command line` test matches `/blender -b scene\.blend/` on the joined command text, which is unaffected by wrapping the `$` in a span.

- [ ] **Step 5: Run the full SpecTab test file to verify all pass**

Run: `cd web && npx vitest run src/jobs/SpecTab.test.tsx`
Expected: PASS (4 tests: command lines still match `/blender -b scene\.blend/`, env/requires entries, no-task placeholder, and the reinforced null-safety guard with the three `(none)` + three section labels).

- [ ] **Step 6: Typecheck**

Run: `cd web && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 7: Commit**

```bash
git checkout -- web/dist/ 2>/dev/null || true
git add web/src/jobs/SpecTab.tsx web/src/jobs/SpecTab.test.tsx
git commit -m "style(web): spec tab on glass sections, keep null-safety guards"
```

---

## Task 3: TasksTable - GlassPanel surface + tokens, keep aria-selected selection

Restyle the `TasksTable` container and empty state to `GlassPanel`, keep the `COLS` grid, the column-header row, the `taskStatus.ts` dot+label cell, the `role="row"` + `aria-selected` selection semantics, and the plain-text worker cell exactly. Add the hi-fi selected-row treatment (`border-l-2 border-accent` + a stronger `bg-accent/[0.08]` tint). Columns stay `NAME / STATUS / RETRY / WORKER / DEPS` - no per-task pct/dur column.

**Files:**
- Modify: `web/src/jobs/TasksTable.tsx`
- Test: `web/src/jobs/TasksTable.test.tsx` (existing behavior is the guard; no change expected - run to confirm)

- [ ] **Step 1: Run the existing TasksTable tests to confirm the baseline is green**

Run: `cd web && npx vitest run src/jobs/TasksTable.test.tsx`
Expected: PASS (4 tests: names+status, aria-selected selection, onSelect-not-nav + no-link, empty state).

- [ ] **Step 2: Restyle TasksTable to GlassPanel + selected-row treatment**

Rewrite `web/src/jobs/TasksTable.tsx` to the following. The `COLS` grid, the column headers, the `taskStatusColor` dot+label, the `role="table"`/`role="row"`/`role="cell"` structure, `aria-selected`, `onSelect(t.id)`, the plain-text `worker_id.slice(0,6)` cell, and the deps cell are preserved exactly; only the container and empty state become `GlassPanel`, and the selected row gains `border-l-2 border-accent bg-accent/[0.08]`.

```tsx
import { GlassPanel } from '../components/holo'
import type { TaskDetail } from './api'
import { taskStatusColor } from './taskStatus'

const COLS = 'grid grid-cols-[1fr_110px_80px_120px_1fr]'

// Tasks table. Rows are SELECTION controls, not navigation: clicking a row sets
// the selected task that drives the Spec/Log panes. Uses aria-selected on each
// row (role=row inside role=table). No per-task duration/percent column: the API
// returns neither per-task timing nor a percent
// (docs/backlog/feature-2026-07-01-per-task-timing.md). The worker cell stays
// plain text; a link to the worker is a deferred follow-up.
export function TasksTable({
  tasks,
  selectedTaskId,
  onSelect,
}: {
  tasks: TaskDetail[]
  selectedTaskId: string
  onSelect: (id: string) => void
}) {
  if (tasks.length === 0) {
    return (
      <GlassPanel className="p-4 text-[12px] text-fg-mute">No tasks.</GlassPanel>
    )
  }
  return (
    <GlassPanel as="div" role="table" aria-label="Tasks">
      <div
        role="row"
        className={`${COLS} border-b border-border px-4 py-2 font-mono text-[10px] tracking-wider text-fg-mute`}
      >
        <span role="columnheader">NAME</span>
        <span role="columnheader">STATUS</span>
        <span role="columnheader">RETRY</span>
        <span role="columnheader">WORKER</span>
        <span role="columnheader">DEPS</span>
      </div>
      {tasks.map((t) => {
        const c = taskStatusColor(t.status)
        const selected = t.id === selectedTaskId
        return (
          <button
            key={t.id}
            type="button"
            role="row"
            aria-selected={selected}
            onClick={() => onSelect(t.id)}
            className={`${COLS} w-full items-center border-b border-border/40 px-4 py-2 text-left font-mono text-[11.5px] ${
              selected ? 'border-l-2 border-accent bg-accent/[0.08]' : ''
            }`}
          >
            <span role="cell" className="truncate font-sans text-[13px] text-fg">{t.name}</span>
            <span role="cell" className={`flex items-center gap-2 ${c.text}`}>
              <span className={`h-1.5 w-1.5 rounded-full ${c.dot}`} />
              {t.status}
            </span>
            <span role="cell" className="text-fg-mute">{t.retry_count}/{t.retries}</span>
            <span role="cell" className="truncate text-fg-mute">
              {t.worker_id ? t.worker_id.slice(0, 6) : '-'}
            </span>
            <span role="cell" className="truncate text-fg-mute">
              {t.depends_on && t.depends_on.length > 0 ? t.depends_on.join(', ') : '-'}
            </span>
          </button>
        )
      })}
    </GlassPanel>
  )
}
```

> **Preserve check:** `GlassPanel` spreads `...rest` onto its tag, so `role="table"` and `aria-label="Tasks"` still land on the container (the `getAllByRole('row')` and `aria-selected` queries are unaffected). The row is still a `<button>` (never an anchor), so `queryByRole('link')` stays absent.

- [ ] **Step 3: Run the full TasksTable test file to verify all pass**

Run: `cd web && npx vitest run src/jobs/TasksTable.test.tsx`
Expected: PASS (4 tests unchanged). Selection queries (`aria-selected`), the onSelect assertion, and the no-link assertion all survive; the empty state text `No tasks.` still matches `/no tasks/i`.

- [ ] **Step 4: Typecheck**

Run: `cd web && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 5: Commit**

```bash
git checkout -- web/dist/ 2>/dev/null || true
git add web/src/jobs/TasksTable.tsx
git commit -m "style(web): tasks table on GlassPanel, keep selection semantics"
```

---

## Task 4: TaskDag - token restyle on the existing dagLayout SVG

Wrap the `TaskDag` SVG in a `GlassPanel` and token-restyle the empty state. The `dagLayout` engine, the positioning math, `role="img"`, the summarizing `aria-label`, the node/edge counts, and the unknown-dep/cycle guards are all preserved. The mock's coordinate-baked `DAGSVG` is NOT adopted. Only the container surface and the empty-state surface change; the SVG node/edge classes already use `taskStatusColor` tokens and stay.

**Files:**
- Modify: `web/src/jobs/TaskDag.tsx` (the two container `<div>`s at lines 24-28 and line 50)
- Test: `web/src/jobs/TaskDag.test.tsx` (existing behavior is the guard; no change expected - run to confirm)

- [ ] **Step 1: Run the existing TaskDag tests to confirm the baseline is green**

Run: `cd web && npx vitest run src/jobs/TaskDag.test.tsx`
Expected: PASS (3 tests: accessible image with node/edge counts, each name as a node label, empty-state note).

- [ ] **Step 2: Restyle the two container divs to GlassPanel**

In `web/src/jobs/TaskDag.tsx`, add the import after line 3 (`import { dagLayout, type DagNode } from './dagLayout'`):

```tsx
import { GlassPanel } from '../components/holo'
```

Replace the empty-state return (lines 23-29):

```tsx
  if (tasks.length === 0) {
    return (
      <div className="rounded-card border border-border bg-white/5 p-4 text-[12px] text-fg-mute">
        No tasks to graph.
      </div>
    )
  }
```

with:

```tsx
  if (tasks.length === 0) {
    return (
      <GlassPanel className="p-4 text-[12px] text-fg-mute">No tasks to graph.</GlassPanel>
    )
  }
```

Replace the SVG wrapper div (line 50, the `<div className="overflow-x-auto rounded-card border border-border bg-white/5 p-2">`) and its matching closing `</div>` (line 97) so the SVG is wrapped in a `GlassPanel`. Change the opening tag:

```tsx
  return (
    <div className="overflow-x-auto rounded-card border border-border bg-white/5 p-2">
```

to:

```tsx
  return (
    <GlassPanel className="overflow-x-auto p-2">
```

and change the matching closing `</div>` on line 97 to `</GlassPanel>`.

> **Preserve check:** the `<svg role="img" aria-label={label} ...>` and every node/edge element are untouched - only the wrapper surface changes. The `getByRole('img', { name: /task dependency graph/i })`, the `/3 tasks/` + `/2 dependency edges/` aria-label assertions, and the node-label `getByText` queries are all unaffected.

- [ ] **Step 3: Run the full TaskDag test file to verify all pass**

Run: `cd web && npx vitest run src/jobs/TaskDag.test.tsx`
Expected: PASS (3 tests unchanged). The empty-state text `No tasks to graph.` still matches `/no tasks/i`.

- [ ] **Step 4: Typecheck**

Run: `cd web && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 5: Commit**

```bash
git checkout -- web/dist/ 2>/dev/null || true
git add web/src/jobs/TaskDag.tsx
git commit -m "style(web): task DAG on GlassPanel, keep dagLayout + a11y"
```

---

## Task 5: LogTab - glass restyle + honest STATIC/HISTORY marker + pending note

Restyle the static log to the hi-fi mono look, and add an honest `STATIC` / `HISTORY` marker plus a one-line "live tailing pending" note where the hi-fi shows a green `LIVE` badge. The static fetch-once behavior, the stdout/stderr color distinction, and the loading/empty/error states are preserved. No `LIVE` badge, no follow toggle, no auto-scroll.

**Files:**
- Modify: `web/src/jobs/LogTab.tsx`
- Test: `web/src/jobs/LogTab.test.tsx` (existing behavior is the guard; add one assertion for the STATIC marker + pending note, and one that a LIVE badge is absent)

- [ ] **Step 1: Run the existing LogTab tests to confirm the baseline is green**

Run: `cd web && npx vitest run src/jobs/LogTab.test.tsx`
Expected: PASS (3 tests: stdout/stderr distinction, empty state, retry on error).

- [ ] **Step 2: Add the failing assertions for the STATIC marker and the absent LIVE badge**

Append to `web/src/jobs/LogTab.test.tsx` (after the existing tests):

```tsx
test('shows a STATIC history marker and a live-pending note, never a LIVE badge', () => {
  render(<LogTab items={items} isLoading={false} isError={false} onRetry={() => {}} />)
  // Honest signalling: the log is fetch-once history, not a live stream. SSE
  // tailing is backend-blocked (feature-2026-06-26-sse-task-log-publishing).
  expect(screen.getByText(/static|history/i)).toBeInTheDocument()
  expect(screen.getByText(/live tailing pending/i)).toBeInTheDocument()
  // A green LIVE badge would imply a stream we cannot deliver.
  expect(screen.queryByText(/^live$/i)).toBeNull()
})
```

- [ ] **Step 3: Run the new assertions to verify they fail**

Run: `cd web && npx vitest run src/jobs/LogTab.test.tsx -t "STATIC history marker"`
Expected: FAIL - the current `LogTab` renders no marker header (`Unable to find an element with the text: /static|history/i`).

- [ ] **Step 4: Restyle LogTab with the STATIC marker header + pending note**

Rewrite `web/src/jobs/LogTab.tsx` to the following. The loading / error-with-retry / empty states and the stdout/stderr distinction are preserved exactly; a mono header row with a `STATIC · HISTORY` marker and a "live tailing pending" note is added above the lines when there is output, and the log body moves onto a `bg-black/25` mono surface for the hi-fi look.

```tsx
import { Button } from '../components/Button'
import type { LogEntry } from './api'

// Static historical log renderer. Fetch-once semantics live in the hook; this is
// a pure view over the resolved items plus loading/error/empty states. NO SSE, no
// follow toggle, no auto-scroll-to-tail: live tailing is backend-blocked
// (docs/backlog/feature-2026-06-26-sse-task-log-publishing.md enabler +
// docs/backlog/feature-2026-06-26-task-log-view-sse-tailing.md web consumer). We
// signal that honestly with a STATIC/HISTORY marker, not a fake LIVE badge.
export function LogTab({
  items,
  isLoading,
  isError,
  onRetry,
}: {
  items: LogEntry[]
  isLoading: boolean
  isError: boolean
  onRetry: () => void
}) {
  if (isLoading) {
    return <div className="p-4 text-[12px] text-fg-mute">Loading logs...</div>
  }
  if (isError) {
    return (
      <div className="flex flex-col items-start gap-2 p-4">
        <div className="text-[12px] text-err">Failed to load logs.</div>
        <Button className="w-auto px-4" onClick={onRetry}>Retry</Button>
      </div>
    )
  }
  if (items.length === 0) {
    return <div className="p-4 text-[12px] text-fg-mute">No log output.</div>
  }
  return (
    <div className="flex flex-col">
      <div className="flex items-center justify-between border-b border-border px-3 py-2 font-mono text-[10px] tracking-[0.14em] text-fg-mute">
        <span className="text-fg-dim">STATIC · HISTORY</span>
        <span>live tailing pending</span>
      </div>
      <div className="flex flex-col gap-0.5 bg-black/25 p-3 font-mono text-[11px]">
        {items.map((l) => (
          <div key={l.seq} className={l.stream === 'stderr' ? 'text-err' : 'text-fg'}>
            {l.content}
          </div>
        ))}
      </div>
    </div>
  )
}
```

- [ ] **Step 5: Run the full LogTab test file to verify all pass**

Run: `cd web && npx vitest run src/jobs/LogTab.test.tsx`
Expected: PASS (4 tests: the 3 originals - stdout/stderr `text-err`, empty state, retry - plus the new STATIC-marker/no-LIVE-badge assertion). The `warning: x` stderr line still carries `text-err`; the `STATIC · HISTORY` and `live tailing pending` text render; `/^live$/i` matches nothing.

- [ ] **Step 6: Typecheck**

Run: `cd web && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 7: Commit**

```bash
git checkout -- web/dist/ 2>/dev/null || true
git add web/src/jobs/LogTab.tsx web/src/jobs/LogTab.test.tsx
git commit -m "style(web): static log tab with honest STATIC marker"
```

---

## Task 6: JobDetailPage - assemble the header, fixed 55/45 glass split, and omission comments

Restyle the page shell: the breadcrumb header (`← Jobs / <id8> / <name>` + inline status + the `JobActions` slot), the `Chip` labels row, the derived progress strip, and the fixed 55/45 `GlassPanel` split wiring the already-restyled children. Restyle the loading/404/error/empty surfaces to `GlassPanel`. Add code comments tracing every omitted hi-fi element to its backlog item. The task-selection state machine, the `useJob`/`useTaskLogs` wiring, the derived progress, and the `canManage` gate are copied verbatim.

**Files:**
- Modify: `web/src/jobs/JobDetailPage.tsx`
- Test: `web/src/jobs/JobDetailPage.test.tsx` (existing 13 behavior tests are the guard; add one omission guard + one static-log-marker check)

- [ ] **Step 1: Run the existing JobDetailPage tests to confirm the baseline is green**

Run: `cd web && npx vitest run src/jobs/JobDetailPage.test.tsx`
Expected: PASS (all tests: identity+tasks, 404+back link, generic error+Retry, default-to-Spec+spec, no-log-on-Spec, Log-fetches-once, task selection updates aria-selected+drives spec, the three canManage cases, derives-progress `1 / 2 done`).

- [ ] **Step 2: Add the failing assertions (omission guard + no-fabricated-timing + static log marker)**

Append to `web/src/jobs/JobDetailPage.test.tsx` (after the existing tests):

```tsx
test('does not fabricate unbacked timing or the live-log affordances', async () => {
  server.use(http.get(`/v1/jobs/${ID}`, () => HttpResponse.json(JOB)))
  renderDetail()
  await screen.findByText('shot-042 render')
  // Omitted per spec (no backend field / SSE blocked): elapsed, ETA, and a
  // Retry/Abort header pill are not rendered. A dead control reads as broken.
  expect(screen.queryByText(/elapsed/i)).toBeNull()
  expect(screen.queryByText(/\beta\b/i)).toBeNull()
  expect(screen.queryByRole('button', { name: /^abort$/i })).toBeNull()
  expect(screen.queryByRole('button', { name: /^retry$/i })).toBeNull() // no 404 -> no Retry
})

test('the Log tab shows a static/history marker, not a LIVE badge', async () => {
  server.use(http.get(`/v1/jobs/${ID}`, () => HttpResponse.json(JOB)))
  server.use(
    http.get('/v1/tasks/t2/logs', () =>
      HttpResponse.json({
        items: [{ seq: 1, stream: 'stdout', content: 'rendering', created_at: '2026-07-01T00:00:00Z' }],
        next_seq: 0,
        total: 1,
      }),
    ),
  )
  renderDetail()
  await screen.findByText('shot-042 render')
  await userEvent.click(screen.getByRole('tab', { name: /log/i }))
  await screen.findByText('rendering')
  expect(screen.getByText(/static|history/i)).toBeInTheDocument()
  expect(screen.queryByText(/^live$/i)).toBeNull()
})
```

> **Note:** the `queryByRole('button', { name: /^retry$/i })` guard is exact-anchored so it does not collide with the not-found/error branch's `Retry` (which only renders on a 404/500, not the happy-path `JOB`). The happy path renders no Retry button, so this guard passes.

- [ ] **Step 3: Run the new omission guard to verify it passes against the current code**

Run: `cd web && npx vitest run src/jobs/JobDetailPage.test.tsx -t "does not fabricate"`
Expected: PASS - the current page renders none of those elements. This is a regression guard that locks in the intended omission before the restyle, so a future edit that adds a dead timing/abort control fails here.

Run: `cd web && npx vitest run src/jobs/JobDetailPage.test.tsx -t "static/history marker"`
Expected: FAIL - the current `LogTab` (before Task 5) or the current page has no `STATIC`/`HISTORY` marker. If Task 5 already landed, this passes; either way it is green after Task 6 completes.

- [ ] **Step 4: Rewrite JobDetailPage with the breadcrumb header, Chip labels, and fixed 55/45 glass split**

Rewrite `web/src/jobs/JobDetailPage.tsx` to the following. The `defaultTaskId` helper, the `useJob`/`useTaskLogs` wiring, the `selectedTaskId`/`selectedTask` derivation, the `enabled` gate, the derived `done`/`total`/`active`/`pct`, the `chips` build, and the `canManage` gate are copied verbatim; only the JSX surfaces change (breadcrumb header, `Chip` labels, `GlassPanel` split, `GlassPanel` loading/error surfaces, the DAG/tasks panels stay their own restyled components, and omission comments are added).

```tsx
import { useMemo, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { ApiError } from '../lib/api'
import { Button } from '../components/Button'
import { Chip, GlassPanel } from '../components/holo'
import { useAuth } from '../auth/AuthProvider'
import { statusColor, progressPct } from './status'
import { TasksTable } from './TasksTable'
import { TaskDag } from './TaskDag'
import { SpecTab } from './SpecTab'
import { LogTab } from './LogTab'
import { JobActions } from './JobActions'
import { useJob } from './useJob'
import { useTaskLogs } from './useTaskLogs'
import type { TaskDetail } from './api'

type Tab = 'spec' | 'log'

// Picks the most useful default task: the first running/failed one if present,
// else the first task. Returns '' for an empty job.
function defaultTaskId(tasks: TaskDetail[]): string {
  const active = tasks.find((t) => t.status === 'running' || t.status === 'failed' || t.status === 'timed_out')
  return active?.id ?? tasks[0]?.id ?? ''
}

export function JobDetailPage() {
  const { id = '' } = useParams()
  const { user } = useAuth()
  const { data: job, error, isLoading, refetch } = useJob(id)
  const [tab, setTab] = useState<Tab>('spec')
  const [pickedTaskId, setPickedTaskId] = useState<string>('')

  const tasks = job?.tasks ?? []

  // Effective selection: an explicit pick if it still matches a task, else the
  // default. This falls back automatically when a poll changes the task list.
  const selectedTaskId = useMemo(() => {
    if (pickedTaskId && tasks.some((t) => t.id === pickedTaskId)) return pickedTaskId
    return defaultTaskId(tasks)
  }, [pickedTaskId, tasks])

  const selectedTask = tasks.find((t) => t.id === selectedTaskId)

  // Log query is decoupled from ['job', ...] and gated to the Log tab, so a job
  // poll never disturbs it and we never fetch logs for an unopened tab.
  const logs = useTaskLogs(selectedTaskId, selectedTaskId !== '' && tab === 'log')

  if (isLoading && !job) {
    return <GlassPanel className="h-40" />
  }

  if (error && !job) {
    const notFound = error instanceof ApiError && error.status === 404
    return (
      <GlassPanel className="mx-auto mt-10 max-w-md p-6 text-center">
        {notFound ? (
          <div className="text-[13px] text-fg-mute">Job not found.</div>
        ) : (
          <>
            <div className="mb-3 text-[13px] text-err">{(error as Error).message}</div>
            <Button className="w-auto px-4" onClick={() => refetch()}>Retry</Button>
          </>
        )}
        <div className="mt-4">
          <Link to="/jobs" className="font-mono text-[11px] text-accent">&larr; Jobs</Link>
        </div>
      </GlassPanel>
    )
  }

  if (!job) return null

  const canManage = Boolean(user && (user.is_admin || job.submitted_by === user.id))

  const c = statusColor(job.status)
  // Progress is DERIVED from tasks[]: the detail endpoint returns no total_tasks/
  // done_tasks/started_at/finished_at (those are list-only enrichment). The hi-fi
  // header also shows STARTED/elapsed/ETA/duration - all omitted (no field on the
  // wire): docs/backlog/feature-2026-07-01-job-detail-timing-enrichment.md.
  const done = tasks.filter((t) => t.status === 'done').length
  const total = tasks.length
  const active = tasks.filter((t) => t.status === 'running' || t.status === 'dispatched').length
  const pct = progressPct(done, total)
  const queued = tasks.filter((t) => t.status === 'pending').length
  const chips = Object.entries(job.labels ?? {}).map(([k, v]) => (v ? `${k}=${v}` : k))

  return (
    <div className="flex flex-col gap-5">
      {/* Breadcrumb + header row: back link, id, name, inline status; the reserved
          JobActions slot (ml-auto). No Retry/Abort header pill - there is no per-job
          retry endpoint and "Abort" is just cancel; the real Cancel/Force cancel
          live in JobActions. */}
      <div className="flex flex-col gap-1">
        <div className="flex items-center gap-2.5">
          <Link to="/jobs" className="font-mono text-[11px] text-fg-mute hover:text-fg">&larr; Jobs</Link>
          <span className="text-fg-dim">/</span>
          <span className="font-mono text-[12px] text-accent">{job.id.slice(0, 8)}</span>
          <span className="text-fg-dim">/</span>
          <h1 className="text-[28px] font-normal tracking-tight">{job.name}</h1>
          {/* Inline status uses the JobStatus map (status.ts), NOT the worker
              StatusDot (WorkerStatus vocabulary). */}
          <span className={`flex items-center gap-2 font-mono text-[12px] ${c.text}`}>
            <span className={`h-1.5 w-1.5 rounded-full ${c.dot}`} />
            {job.status}
          </span>
          <div data-testid="job-actions" className="ml-auto flex items-center gap-2">
            {canManage && <JobActions job={job} />}
          </div>
        </div>
        <div className="font-mono text-[11px] text-fg-mute">
          id {job.id.slice(0, 8)} · submitted by {job.submitted_by_email ?? '-'} · priority {job.priority}
        </div>
        {chips.length > 0 && (
          <div className="mt-1 flex flex-wrap gap-1">
            {chips.map((ch) => (
              <Chip key={ch} tone="accent">{ch}</Chip>
            ))}
          </div>
        )}
      </div>

      {/* Body: fixed 55/45 split. The accessible drag-resizer is a filed follow-up:
          docs/backlog/idea-2026-07-01-job-detail-resizable-split.md. */}
      <div className="flex flex-col gap-5 lg:flex-row">
        <div className="flex flex-col gap-4 lg:w-[55%]">
          {/* Derived progress strip: done/total + active, status-toned bar. Kept as
              an inline per-status bar (ProgressBar has only accent/muted tones). */}
          <div className="flex flex-col gap-2">
            <div className="flex items-baseline justify-between font-mono text-[11px] text-fg-mute">
              <span>{done} / {total} tasks done</span>
              <span>{active} active</span>
            </div>
            <span className="relative h-1.5 overflow-hidden rounded bg-white/10">
              <span
                className={`absolute inset-y-0 left-0 rounded ${
                  job.status === 'done' ? 'bg-ok' : job.status === 'failed' ? 'bg-err' : 'bg-accent'
                }`}
                style={{ width: `${pct}%` }}
              />
            </span>
          </div>

          {/* Pipeline panel header carries the real derived active/queued counts
              (replaces the hi-fi "STAGE 4 / 8" + "CLICK TO STREAM" mock strings;
              click-to-stream implies live logs we cannot deliver). */}
          <div className="flex items-center justify-between px-1 font-mono text-[10px] tracking-[0.14em] text-fg-mute">
            <span>PIPELINE</span>
            <span>{active} ACTIVE · {queued} QUEUED</span>
          </div>
          <TaskDag tasks={tasks} />
          <TasksTable tasks={tasks} selectedTaskId={selectedTaskId} onSelect={setPickedTaskId} />
        </div>

        <div className="flex flex-col lg:w-[45%]">
          <div role="tablist" aria-label="Task detail" className="flex gap-1 border-b border-border">
            <button
              type="button"
              role="tab"
              aria-selected={tab === 'spec'}
              onClick={() => setTab('spec')}
              className={`px-3 py-2 text-[12px] ${tab === 'spec' ? 'border-b-2 border-accent text-fg' : 'text-fg-mute'}`}
            >
              Spec
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={tab === 'log'}
              onClick={() => setTab('log')}
              className={`px-3 py-2 text-[12px] ${tab === 'log' ? 'border-b-2 border-accent text-fg' : 'text-fg-mute'}`}
            >
              Log
            </button>
          </div>
          <GlassPanel className="rounded-t-none border-t-0">
            {tab === 'spec' ? (
              <SpecTab task={selectedTask} />
            ) : (
              <LogTab
                items={logs.data?.items ?? []}
                isLoading={logs.isLoading}
                isError={logs.isError}
                onRetry={() => logs.refetch()}
              />
            )}
          </GlassPanel>
        </div>
      </div>
    </div>
  )
}
```

> **Preserve check (do NOT deviate):**
> - The `useJob(id)` call, `['job', id]` key, and 3s poll are unchanged (hook untouched).
> - `useTaskLogs(selectedTaskId, selectedTaskId !== '' && tab === 'log')` - the `enabled` expression is byte-for-byte identical; the log is never fetched on the Spec tab or for an empty selection.
> - `selectedTaskId` `useMemo` + `selectedTask` are unchanged (the selection state machine).
> - `done`/`total`/`active`/`pct` derivation is unchanged; the progress text `{done} / {total} tasks done` still matches `/1\s*\/\s*2 tasks done/i`.
> - `canManage` and the `data-testid="job-actions"` slot are unchanged.
> - Both status renders use `statusColor(job.status)` (`c.dot`/`c.text`), NOT the worker `StatusDot`.
> - The tab `role`/`aria-selected` structure and default-to-Spec are unchanged.

- [ ] **Step 5: Run the full JobDetailPage test file to verify all pass**

Run: `cd web && npx vitest run src/jobs/JobDetailPage.test.tsx`
Expected: PASS (13 originals + 2 new). Structural notes: the `shot-042 render` name still renders as an `<h1>`; the breadcrumb adds an extra `id8`/`/` separators but the identity test queries by name text and the `mira@studio.dev` regex, both preserved; the `job-actions` slot survives; the `1 / 2 tasks done` regex still matches; the Log tab now shows the STATIC marker.

- [ ] **Step 6: Run the full jobs + holo suite to catch cross-file regressions**

Run: `cd web && npx vitest run src/jobs src/components/holo`
Expected: PASS across the board (all six restyled components plus the page, plus the untouched holo primitive tests).

- [ ] **Step 7: Typecheck**

Run: `cd web && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 8: Restore `web/dist` if the test run touched it, then commit**

```bash
git checkout -- web/dist/ 2>/dev/null || true
git add web/src/jobs/JobDetailPage.tsx web/src/jobs/JobDetailPage.test.tsx
git commit -m "style(web): job detail page Holo relayout with fixed 55/45 glass split"
```

---

## Final verification

- [ ] **Run all jobs + holo tests together**

Run: `cd web && npx vitest run src/jobs src/components/holo`
Expected: PASS across the board.

- [ ] **Typecheck the whole web package**

Run: `cd web && npx tsc --noEmit`
Expected: no errors.

- [ ] **Confirm no unintended files are staged and no invariant files were touched**

Run: `git status`
Expected: only the six restyled components and their touched tests across the six commits, `web/dist` clean. Confirm these were NEVER opened for edit: `web/src/jobs/useJob.ts`, `web/src/jobs/useTaskLogs.ts`, `web/src/jobs/useJobActions.ts`, `web/src/jobs/dagLayout.ts`, `web/src/jobs/status.ts`, `web/src/jobs/taskStatus.ts`, `web/src/jobs/api.ts`, and any Go file.

---

## Self-review against the spec

- **Fixed 55/45 split, NOT a drag-resizer** (resolved decision 1) - Task 6, Step 4: `lg:w-[55%]`/`lg:w-[45%]` kept, resizer traced to `idea-2026-07-01-job-detail-resizable-split.md` via comment.
- **Keep `dagLayout` engine + restyle `TaskDag` with tokens, do not adopt `DAGSVG`** (resolved decision 2 / spec section 3) - Task 4: only container surfaces change; `dagLayout.ts` untouched; `role="img"` + aria-label + cycle safety preserved.
- **Static Log tab with a STATIC/HISTORY marker + "live tailing pending", NOT a fake LIVE badge** (resolved decision 3) - Task 5, Step 4; guarded by the STATIC-marker/no-LIVE test in Tasks 5 and 6.
- **Pragmatic restyle: OMIT unbacked mock elements** (resolved decision 4) - elapsed/ETA/duration (Task 6 comment -> timing-enrichment backlog), image/runtime/cluster/parallelism/source (Task 2 comment), per-task dur/% (Task 3 comment -> per-task-timing backlog), donut (not rendered; inline bar kept), Retry/Abort header pill (Task 6 comment); guarded by the `does not fabricate` test.
- **Progress kept as the inline status-toned bar** (resolved decision 5) - Task 6; `progressPct` derivation preserved; `1 / 2 tasks done` text intact; `ProgressBar` not used (accent/muted only).
- **JobActions buttons -> PillButton** (resolved decision 6) - Task 1; `ghost`/`danger` variants; mutations/dialog/gate unchanged; `name`-based queries intact.
- **Labels -> Chip (accent tone)** (resolved decision 7) - Task 6, Step 4.
- **Job status dot inline (status.ts), NOT the worker StatusDot; worker-cell link + shared status-dot deferred** (resolved decisions 8, 9) - Task 6 (inline `c.dot`/`c.text`), Task 3 (plain-text worker cell), with comments; `StatusDot` deliberately not imported.
- **CRITICAL - SpecTab null-safety (PR #96) must not regress** - Task 2 keeps `env ?? {}`/`requires ?? {}`/`commands ?? []` verbatim; reinforced by the strengthened `null env/requires/commands -> three (none) + three labels` test.
- **useJob polling + `['job', id]` key; useTaskLogs static/fetch-once, decoupled key, gated to Log tab + a selected task** - hard-preserve; hooks never edited; `enabled` expression copied byte-for-byte in Task 6.
- **Task-selection state machine + fallback on poll** - hard-preserve; `defaultTaskId`/`selectedTaskId`/`selectedTask` copied verbatim in Task 6.
- **Derive-progress-from-tasks (no total_tasks/done_tasks on the detail endpoint)** - hard-preserve; derivation copied verbatim in Task 6.
- **JobActions cancel/force + ConfirmDialog + banners + canManage gate + three-key invalidation, no revoke** - hard-preserve; `useJobActions.ts` never edited; only button elements swapped in Task 1.
- **TasksTable aria-selected selection, no link, columns unchanged; TaskDag cycle-safe dagLayout; derive/format helpers unchanged** - hard-preserve; Tasks 3 and 4; `status.ts`/`taskStatus.ts`/`dagLayout.ts` never edited.
- **All 13 JobDetailPage behavior tests + the five component test files preserved as the regression guard** - each task runs the existing file green before and after; new assertions are light and additive (null-safety reinforcement, STATIC marker, omission guards).
- **Frontend-only slice, declared** - Slice independence section; no Go/`.sql`/`.proto` touched.
