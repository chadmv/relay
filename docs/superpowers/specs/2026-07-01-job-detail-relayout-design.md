# Job Detail Holo Relayout

- Date: 2026-07-01
- Status: Draft (design; awaiting review)
- Owner: relay-tpm
- Scope: web SPA only (`web/src/jobs/`). No backend or Go changes.

## Problem

The shipped Job detail page (`web/src/jobs/JobDetailPage.tsx` at `/jobs/:id`, plus its
sub-components `TaskDag`, `TasksTable`, `SpecTab`, `LogTab`, `JobActions`) works but
predates the picked "Holo" hi-fi design and the shared primitive set at
`web/src/components/holo/`. It renders hand-built `bg-white/5 border-border` boxes, a
custom header, and a fixed 55/45 two-column split, duplicating the Holo glass/eyebrow
vocabulary that now lives in reusable primitives (used by the worker pages and the
just-relaid-out jobs list, both of which are the migration reference). The hi-fi target
is `HoloJobDetail` in `design_handoff_relay_holo/hifi3-holo-pages.jsx` (~line 2427), with
the full-screen `HoloTaskLog` (~line 2694) and the shared `DAGSVG` / `StatusDot` in
`hifi2-shared.jsx`.

This is the **richest** page in the app and the one page the user wants to make explicit
design decisions on. It is a **restyle/relayout of an existing, working page**: every data
path, query key, polling rule, task-selection behavior, cancel flow, and the
just-landed SpecTab null-safety are preserved exactly. Structure and styling change,
rebuilt from the shared primitives; nothing that lacks a backend gains a live control.

## Design authority and token mapping

Follows the same approach as the jobs-list and worker-detail relayouts
(`docs/superpowers/specs/2026-07-01-jobs-list-holo-relayout-design.md`,
`docs/superpowers/specs/2026-07-01-holo-primitives-worker-detail-design.md`).

The **authoritative look** is the hi-fi Holo prototype (`HoloJobDetail` / `HoloTaskLog`),
not the lo-fi `reference/screens/job-detail.js` sketch (orange accent, cursive; structure
only). The app keeps its cyan accent and fixed `#050410` background. The prototype threads
a `C` token bag (inline styles) and a density switch `D` into every component; we **do
not** port the `C` bag, the HSV `makeTokens` machinery, or the `D` switch. `C.*` maps onto
the existing `tokens.css` Tailwind classes (identical mapping table to the worker spec:
`C.bg`->`bg-bg`, `C.fg`->`text-fg`, `C.fgMute`->`text-fg-mute`, `C.fgDim`->`text-fg-dim`,
`C.accent`/`C.accentB`, `C.ok`/`C.warn`/`C.err`, `C.border`->`border-border`, glass radius
`14`->`rounded-card`), and `D.*` collapses to fixed comfortable Tailwind values.

The `web/src/components/holo/` primitives are already merged. This spec **consumes** them;
it does not add or modify a primitive. (One optional exception is called out in Open
Decision 2, the DAG; the recommendation there is still "no new primitive".)

## Backend reality (confirmed against `internal/api/jobs.go`, `tasks.go`, `api.ts`)

The hi-fi `HoloJobDetail` and `HoloTaskLog` are driven by rich mock data (`JOB_DETAIL`,
`TASKS`, `DAG`, `LOG` in `hifi2-data.js`). Much of that mock surface has **no backing** in
the real `GET /v1/jobs/:id` (`JobDetail`) or `GET /v1/tasks/:id/logs` (`TaskLogPage`). This
table is the load-bearing part of the spec: it draws the line between what we relayout and
what we omit.

### Real today (relayout these)

| Hi-fi element | Real field(s) | Client type | Notes |
| --- | --- | --- | --- |
| Job id (breadcrumb) | `id` | `JobDetail.id` (sliced) | real |
| Job name | `name` | `JobDetail.name` | real |
| Job status pill | `status` | `JobDetail.status` (`JobStatus`) | real; `status.ts` color map |
| Owner | `submitted_by_email` | `JobDetail.submitted_by_email?` | real |
| Priority | `priority` | `JobDetail.priority` | real |
| Labels chips | `labels` | `JobDetail.labels` | real (current page already renders these) |
| Progress (`done/total`, %) | derived from `tasks[]` | `JobDetail.tasks` | real; **derived client-side** (detail endpoint omits `total_tasks`/`done_tasks`) |
| Tasks list (name, status, retry, worker, deps) | `tasks[]` | `TaskDetail[]` | real; the current `TasksTable` columns |
| DAG (nodes + edges) | `tasks[].name` + `depends_on` | `TaskDetail.depends_on` | real; `dagLayout` builds it |
| Task spec (commands, env, requires) | `tasks[].commands/env/requires` | `TaskDetail` (nullable) | real; the current `SpecTab` |
| Task log lines (static) | `GET /v1/tasks/:id/logs` items | `LogEntry[]` | real, but **fetch-once only** (no live stream; see below) |
| Cancel / Force cancel | `DELETE /v1/jobs/:id[?force=true]` | `cancelJob` | real; the current `JobActions` |

### Not backed -> OMIT (do not render)

The hi-fi mock invents fields the API does not return. Each is omitted, not faked:

| Hi-fi element | Why omitted |
| --- | --- |
| **Donut % ring** (`JOB_DETAIL.pct`) | The % is derivable (`progressPct`), but a donut is a visual re-expression of the same progress the bar already shows. Not omitted for data reasons; see Open Decision 5 (donut vs bar). The **numbers** it wraps (Elapsed/ETA/Tasks/Owner) are the real question, below. |
| **Elapsed** (`JOB_DETAIL.elapsed`) | `GET /v1/jobs/:id` (`JobDetail`) returns **no** `started_at`/`finished_at` (those are list-only enrichment). No start time -> no elapsed. Omit. |
| **ETA** (`JOB_DETAIL.eta`) | No server-side ETA exists anywhere. Pure mock. Omit. |
| **image / runtime / cluster / parallelism** (Spec pane header rows) | `JobDetail` and `TaskDetail` return none of these. The relay job model has no image/cluster/parallelism concept. Omit. |
| **source** (`JOB_DETAIL.source` = "cli") | Not echoed by `handleGetJob` (the current `SpecTab` comment already notes "no per-task source block ... out of scope"). Omit. |
| **Per-task progress %** (`TASKS[].pct`) | `TaskDetail` has no per-task percent or timing. The current `TasksTable` correctly has no percent/duration column. Omit (keep the current columns). |
| **Per-task duration** (`TASKS[].dur`) | Same: no per-task timing on the wire. Omit. |
| **"STAGE 4 / 8" pipeline counter** | Mock string; there is no stage concept. Omit (or replace with the real node/edge count the current DAG aria-label already carries). |
| **"4 ACTIVE · 12 QUEUED · CLICK TO STREAM" tasks meta** | "click to stream" implies live log streaming (blocked). The active/queued counts ARE derivable from `tasks[]`; keep those, drop "click to stream". |
| **Live log stream / LIVE badge / "waiting for stream" / Follow tail / EventSource** | **Backend-blocked.** See below. |
| **Full-screen `HoloTaskLog` route** (`/jobs/:id/tasks/:n`) | Backend-blocked (its whole reason for being is the live single-task stream). Out of scope. |
| **Retry / Abort buttons** (hi-fi header) | There is no per-job "retry" endpoint; "Abort" is just cancel under a different name. Keep the real **Cancel / Force cancel** (`JobActions`); do not add a dead "Retry". |
| **GPU/util sparklines in the log** (`HoloTaskLog` DEBUG lines) | Those are mock log content, not a metrics feed. The real log is whatever `LogEntry.content` carries. No sparkline. |

### Live log tailing is backend-blocked (confirmed)

The hi-fi log pane is a **live stream**: a green `LIVE` badge, `/v1/events?task_id=` in the
header, "waiting for stream", a "Follow tail" toggle, and auto-scroll. **None of this is
possible today.** `handleTaskLog` persists log chunks to the DB but never publishes them to
`events.Broker`, and `/v1/events` carries only status payloads - there is no live-log
source at all. This is tracked by:

- `docs/backlog/feature-2026-06-26-sse-task-log-publishing.md` (the **backend enabler**,
  unshipped) - publish log lines to the broker without blocking ingest.
- `docs/backlog/feature-2026-06-26-task-log-view-sse-tailing.md` (the **web consumer** -
  the EventSource hook + full-screen `HoloTaskLog` view), which is blocked on the enabler.

Until the enabler ships, the Log tab **stays STATIC** (fetch-once via `useTaskLogs`, exactly
as today). This relayout restyles the static log; it does **not** add a `LIVE` badge, a
follow toggle, or the full-screen route. Open Decision 3 settles how honestly we signal
"static, live pending" in the restyled header.

## Target layout

Top to bottom, built from the shared primitives, matching the shipped worker-detail header
idiom (breadcrumb + name + inline status, sub-line, then a body). The page keeps its outer
`flex flex-col gap-4/5` container. The **structure below assumes the recommended answers to
the Open Decisions** (fixed split, restyled `dagLayout` SVG, static log with a pending
affordance, pragmatic restyle). If the user picks differently, the marked sections change.

### 1. Breadcrumb + header row

Matches `WorkerDetailPage`'s header idiom (which the hi-fi also uses: `← Jobs / <id> /
<name>`):

- **Left:** `<Link to="/jobs">← Jobs</Link>`, a `text-fg-dim` `/` separator, the job id in
  mono (`id.slice(0, 8)`, `text-accent`), then the job name (`<h1>` or a `text-[16px]`
  span per the hi-fi; keep the current `text-[28px]` H1 if we prefer the shipped scale -
  see Open Decision 4). The **inline status** stays as the current `status.ts`-driven dot +
  label (NOT the worker `StatusDot`, which is hard-wired to `WorkerStatus`; jobs have their
  own `JobStatus` vocabulary and `status.ts` map - same reasoning as the jobs-list spec).
- **Right (`ml-auto`):** the existing `data-testid="job-actions"` slot, filled by
  `JobActions` for `canManage` users. **`JobActions` is preserved wholesale** - its two
  buttons, `useJobActions` mutations, `ConfirmDialog` confirm flow, inline error, and the
  `canManage = is_admin || submitted_by === user.id` gate. Its buttons may be restyled to
  the `PillButton` `ghost`/`danger` variants for hi-fi fidelity (see Open Decision 6); the
  behavior and the `getByRole('button', { name: 'Cancel' | 'Force cancel' })` test surface
  stay identical.

### 2. Identity sub-line + labels

A mono `text-fg-mute` sub-line (as today): `id <id8> · submitted by <email> · priority
<priority>`. Below it, the **labels chips** row, preserved. Open Decision 7: keep the
current inline `accent/40` label span, or adopt the `Chip` primitive (`accent` tone) - the
`Chip` tone matches these closely, unlike the jobs-list schedule chip which needed an
`accentB` tone. Recommendation: adopt `Chip` here (clean fit, one fewer inline pattern).

### 3. Body: two-column split (tasks/graph left, detail right)

The current page is a fixed `lg:w-[55%]` / `lg:w-[45%]` split. **Recommendation: keep the
fixed split now** (Open Decision 1), restyled. The columns:

**Left column (tasks + DAG + progress):**

- **Progress summary** (top of the left column, as today): the `done / total tasks done` +
  `active` line and the progress bar. The bar **may adopt the `ProgressBar` primitive** for
  the accent/running case, but `ProgressBar`'s fill is always the accent gradient (with a
  `muted` tone) and the job bar needs per-status fill (`done`->ok, `failed`->err, else
  accent). Same call as the jobs-list spec: **keep the current inline bar** (status-toned
  fill) unless we extend `ProgressBar` with `ok`/`err` tones. The `done`/`total`/`active`
  counts and `progressPct` derivation are **preserved exactly** (the
  `derives progress from the tasks array` test asserts `1 / 2 tasks done`).
- **DAG panel** (`TaskDag`): wrapped in a `GlassPanel` (gradient glass) instead of the flat
  `bg-white/5`, with a small header row (`Pipeline` + the real node/edge count, replacing
  the mock `STAGE 4 / 8`). The SVG itself: **keep the current `dagLayout` layout engine and
  the `TaskDag` SVG, restyled to tokens** (Open Decision 2). Its `role="img"` +
  summarizing `aria-label` and the `dagLayout` unknown-dep/cycle guards are preserved (the
  `TaskDag.test.tsx` assertions on the aria-label node/edge counts and the empty-state note
  stay valid). Token restyle only: node fill/stroke via `taskStatusColor`, edge dash for
  incomplete deps, the glass container.
- **Tasks list** (`TasksTable`): wrapped in / restyled to `GlassPanel`, with an optional
  panel header (`Tasks · <done> / <total>` + the derived `<active> ACTIVE · <queued>
  QUEUED` meta, dropping the hi-fi "CLICK TO STREAM"). **Rows remain selection controls,
  not navigation** (`role="row"` + `aria-selected`, `onSelect(t.id)`), which the tests and
  a11y depend on (`clicking a row calls onSelect ... selection, not navigation`; `queryByRole('link')`
  is asserted absent). Columns stay `NAME / STATUS / RETRY / WORKER / DEPS` - **no** per-task
  `pct`/`dur` column (unbacked). The status cell keeps the `taskStatus.ts` dot + label.
  Restyle: hi-fi row idiom (active row gets a `border-l-2 border-accent` + `bg-accent/[0.08]`
  tint on selection, mono cells). The `worker_id` cell stays plain text (the hi-fi makes it
  a worker link; Open Decision 8 - recommend deferring the worker link as it is a new nav
  edge and small).

**Right column (Spec / Log tabs):**

- The `role="tablist"` with `Spec` and `Log` tabs is **preserved** (the tab roles/labels
  are asserted: `getByRole('tab', { name: /log/i })`, "defaults to the Spec tab"). Restyle
  the tab strip and wrap the panel body in `GlassPanel`.
- **Spec tab** (`SpecTab`): preserved wholesale, including the **just-fixed null-safety** -
  `task.env ?? {}`, `task.requires ?? {}`, `task.commands ?? []` must NOT regress (the
  `renders placeholders when the API returns null env/requires/commands` test asserts three
  `(none)` placeholders; a regression re-blanks the whole page). Restyle the COMMANDS/ENV/
  REQUIRES sections to hi-fi tokens (mono, the `$` accent prompt, `bg-black/20` command
  block). We do **not** add the hi-fi's image/runtime/cluster/source rows (unbacked, per
  Backend reality).
- **Log tab** (`LogTab`): **STATIC**, restyled. Fetch-once via `useTaskLogs` (unchanged:
  its `['task-logs', taskId]` key is deliberately **off** the `['job', ...]` prefix so a
  job poll never disturbs it; `enabled` gates it to the Log tab so we never fetch for an
  unopened tab). The stdout/stderr color distinction (`text-err` for stderr) is preserved
  (asserted). Restyle to the hi-fi mono log look. Open Decision 3 settles the header:
  recommend a small mono `STATIC` / `HISTORY` marker (NOT the green `LIVE` badge) plus a
  one-line "live tailing pending" affordance, so the pane reads honestly rather than
  implying a stream it cannot deliver.

## Task selection behavior (preserved exactly)

The selection state machine is load-bearing and unchanged:

- `defaultTaskId(tasks)` picks the first `running`/`failed`/`timed_out` task, else the
  first task, else `''`.
- `selectedTaskId` = an explicit `pickedTaskId` if it still matches a live task, else the
  default - so a poll that changes the task list re-derives selection cleanly.
- `selectedTask` drives both the Spec pane and (when the Log tab is active) `useTaskLogs`.
- The `does NOT hit the log endpoint while the Spec tab is active` and `switching to the
  Log tab fetches once` behaviors are preserved (the `enabled = selectedTaskId !== '' &&
  tab === 'log'` gate stays).

## Loading / error / empty states (preserved, restyled)

- **Loading skeleton** (`isLoading && !job`): the flat `bg-white/5` box becomes a
  `GlassPanel` skeleton (matching `WorkerDetailPage`).
- **404**: "Job not found." card + a `← Jobs` back link, restyled to `GlassPanel`.
- **Non-404 error**: error message + `Retry` button (`refetch()`), restyled to `GlassPanel`.
- **Empty task list**: `TaskDag` "No tasks to graph." and `TasksTable` "No tasks." notes,
  restyled to the glass surface. `SpecTab` "Select a task to view its spec." when no task
  is selected, `LogTab` "No log output." on empty items - all preserved.

## Primitives used vs not

**Used:** `GlassPanel` (all panel/skeleton/error surfaces - the one intentional fidelity
upgrade over flat `bg-white/5`), `Eyebrow` (section micro-labels if any), `Chip` (labels
row, per Open Decision 7), `PillButton` (JobActions restyle, per Open Decision 6).
Optionally `Panel` for the DAG/tasks panels if the single-cell header fits (it does for
`Pipeline` + meta; for the tasks list a custom grid header is cleaner, same call as the
jobs-list table).

**Not used (with reason):**

- **`StatusDot`** (worker vocabulary) - jobs and tasks have their own `JobStatus`/
  `TaskStatus` maps in `status.ts`/`taskStatus.ts`; forcing them through the worker
  `livenessView` would misclassify. Keep the inline dot + label. (Same note as the
  jobs-list spec: a **generic** status-dot primitive taking a resolved `{label, dotClass,
  textClass}` view is worth extracting once a third consumer appears; the job-detail page
  is arguably that third consumer - jobs list, tasks table, job header - so this spec flags
  it as a candidate but does not extract it, YAGNI unless the user wants it now.)
- **`ProgressBar`** for the job/task bars - needs per-status (`ok`/`err`) fills it lacks;
  keep inline bars.
- **`KpiStat`** - the hi-fi Elapsed/ETA/Tasks/Owner stat block is mostly unbacked (Elapsed/
  ETA don't exist on the detail endpoint). We do not render a stat grid of mock values. The
  two real numbers (Tasks done/total, Owner) already live in the header sub-line and
  progress line. `KpiStat` is not used here.

## Preserved vs changed

**Preserved exactly (behavior, contracts, test-relevant):**

- `useJob(id)` polling hook, `['job', id]` key, `refetchInterval`, `keepPreviousData`.
- `useTaskLogs(taskId, enabled)` - static fetch-once, `['task-logs', taskId]` key **off**
  the `['job']` prefix, `staleTime: Infinity`, caller-gated `enabled`.
- Progress derived from `tasks[]` (`done`/`total`/`active`/`progressPct`), NOT from
  list-only fields.
- Task selection: `defaultTaskId`, `selectedTaskId` fallback, `selectedTask`.
- `SpecTab` null-safety (`env ?? {}`, `requires ?? {}`, `commands ?? []`) - **must not
  regress**.
- `TasksTable` rows as selection controls (`aria-selected`, no anchors).
- `TaskDag` `dagLayout` engine, `role="img"` + aria-label, unknown-dep/cycle guards.
- `LogTab` stdout/stderr distinction; static, no SSE.
- `JobActions` - buttons, `useJobActions` mutations, `ConfirmDialog`, `canManage` gate,
  terminal-state hiding (`cancelled`/`done` hide buttons; `failed` stays cancellable).
- Spec/Log tab roles and labels; default-to-Spec.
- 404 / error-with-retry / loading / empty states (restyled, not removed).

**Changed (structure/styling only):**

- All panel/card surfaces: flat `bg-white/5` -> `GlassPanel` gradient glass (the one
  intentional visual upgrade, applied uniformly, matching the worker + jobs-list pages).
- Header adopts the breadcrumb idiom (`← Jobs / <id> / <name>`) to match `WorkerDetailPage`.
- Labels row -> `Chip` (Open Decision 7). `JobActions` buttons -> `PillButton` (Open
  Decision 6). Both behavior-preserving.
- DAG + tasks panels gain hi-fi headers (Pipeline / Tasks meta) fed by **real derived
  counts** (node/edge, active/queued), replacing mock strings (`STAGE 4/8`, `CLICK TO
  STREAM`).
- Log tab header gains an honest `STATIC` marker + "live pending" note (Open Decision 3),
  NOT a `LIVE` badge.
- Unbacked hi-fi elements (donut-wrapped Elapsed/ETA, image/runtime/cluster/source, per-task
  pct/dur, Retry/Abort, full-screen log route, live stream) are **not rendered**, each
  traceable to a Backend-reality row and, where a follow-up exists, to its backlog item via
  a short code comment.

## Backend-blocked / graceful handling

The honest treatment, same philosophy as the jobs-list relayout: **omit** unbacked controls
rather than render dead ones. Specifically:

- Live log tailing: Log tab stays static; a mono "live tailing pending" affordance (not a
  fake `LIVE` badge) signals the interim state. Traceable to
  `feature-2026-06-26-sse-task-log-publishing.md` (enabler) and
  `feature-2026-06-26-task-log-view-sse-tailing.md` (web consumer) via a code comment.
- Resizable split: fixed 55/45 kept; traceable to
  `idea-2026-07-01-job-detail-resizable-split.md` via a code comment.
- Elapsed/ETA/image/runtime/cluster/source/per-task-pct-dur: omitted (no field on the wire);
  no backlog item is warranted (they are mock inventions, not deferred features) beyond a
  one-line comment where the hi-fi would have placed them, if useful.

## Test impact

Vitest (`cd web && npm test`). No Go tests affected. Behavior assertions are preserved;
only structural/class-based assertions may shift.

- **`JobDetailPage.test.tsx`** - all 13 tests are behavior/text/role queries and should
  survive: job identity + tasks render, 404 not-found + back link, generic error + Retry,
  default-to-Spec + selected task spec, no-log-fetch-on-Spec, Log-fetches-once, task
  selection updates `aria-selected` + drives Spec, the three `canManage` visibility cases,
  and `derives progress from the tasks array (1 / 2 done)`. None key on container classes.
  Re-check the `job-actions` `data-testid` slot survives (it must; keep the testid) and the
  progress-text regex (`/1\s*\/\s*2 tasks done/i`) still matches after restyle.
- **`TasksTable.test.tsx`** - name/status text, `aria-selected` selection, onSelect-not-nav,
  `queryByRole('link')` absent, empty state - all preserved. Only update if an assertion
  keyed on old container classes (none do).
- **`TaskDag.test.tsx`** - `role="img"` + aria-label node/edge counts, node labels, empty
  state - all preserved (layout engine + aria unchanged).
- **`SpecTab.test.tsx`** - command lines, env/requires, no-task placeholder, and the
  **null env/requires/commands -> three `(none)`** guard - all preserved (this is the
  regression that must not return).
- **`LogTab.test.tsx`** - stdout/stderr color, empty state, error-with-retry - preserved.
- **`JobActions.test.tsx`** - button presence, graceful/force DELETE, confirm dialog,
  terminal-state hiding - preserved (behavior unchanged; only button classes may change,
  and the tests query by `name`, not class).
- **New tests:** none required. If Open Decision 3 adds a "live pending" affordance, a light
  assertion that the Log tab shows a static/pending marker (and NOT a live badge) is
  optional. If the DAG panel header renders a real derived count, an optional assertion may
  guard the "no STAGE 4/8 mock string" replacement.

## Implementation-plan recommendation

**Yes - warrant a separate implementation plan.** This is the largest page in the SPA (six
sub-components, a selection state machine, three preserved test files plus the page test,
and several intertwined Open Decisions). The relayout touches `JobDetailPage.tsx` plus
`TaskDag.tsx`, `TasksTable.tsx`, `SpecTab.tsx`, `LogTab.tsx`, and possibly `JobActions.tsx`.
A `writing-plans` pass should sequence it as: (1) header + labels + sub-line, (2) left
column (progress + DAG panel + tasks panel), (3) right column (tab strip + Spec + static
Log), (4) loading/error/empty restyle, each behind its preserved tests, with the Open
Decision answers resolved **before** the plan is written (they change the component
boundaries - especially decisions 1, 2, and 5).

## Open decisions

Lead forks for the user. Recommendations are baked in but every one is a genuine choice
the user asked to weigh in on; **do not treat the recommendation as decided.**

1. **Resizable split vs fixed 55/45 (the big one).** The hi-fi has a draggable
   tasks/detail split (a `col-resize` handle, `useLayoutEffect` initial width, mousemove
   drag). The current page is a fixed `lg:w-[55%]` / `lg:w-[45%]`. There is a filed backlog
   item for an **accessible** drag-resizer with keyboard support and `localStorage`
   persistence (`idea-2026-07-01-job-detail-resizable-split.md`), explicitly noting the
   fixed split is an intentional interim state.
   - **Option A (recommend): ship fixed now**, restyled; do the accessible resizer as its
     own slice (it is a candidate shared primitive, best built once for all future detail
     splits, and doing it right means `role="separator"` + `aria-valuenow` + arrow-key
     resize, not just the hi-fi's mouse-only drag).
   - **Option B: include the drag-resizer in this relayout.** Faithful to the hi-fi, but
     the hi-fi's version is **mouse-only and not accessible** (no keyboard, no ARIA), so
     doing it in-scope means either shipping an inaccessible control (bad) or absorbing the
     full accessible-resizer backlog item into this page (larger scope, and it stops being
     reusable-first). Recommendation stands: fixed now.

2. **DAG treatment.** Keep the current `dagLayout` SVG restyled with tokens, or move closer
   to the hi-fi `DAGSVG` / adopt a shared DAG primitive?
   - **Option A (recommend): keep `dagLayout` + `TaskDag`, restyle only.** The current
     engine already builds the graph from real `depends_on`, handles unknown deps and
     cycles defensively, and is tested. The hi-fi `DAGSVG` uses **pre-baked x/y coordinates
     from mock data** (`DAG.nodes = [id,label,status,x,y]`) - it has no layout engine at
     all, so we cannot "adopt" it; we would have to keep our layout and only borrow its
     visual treatment (curved bezier edges, the running-node animated underline, node
     circle + label). Recommendation: keep our engine, optionally borrow the bezier-edge and
     status-circle styling for fidelity (low risk, visual only).
   - **Option B: extract a shared DAG primitive.** Only one consumer today (this page).
     YAGNI unless a second DAG surface is imminent. Not recommended now.
   - Sub-fork within A: how much hi-fi visual polish to borrow (straight vs curved edges,
     the animated running-node underline). Cheap and visual; user's aesthetic call.

3. **Log tab: how to signal "static, live pending".** Static-only is forced (SSE blocked).
   - **Option A (recommend): restyle the static log + a small mono affordance** - a
     `STATIC` / `HISTORY` marker where the hi-fi shows `LIVE`, and a one-line note like
     "live tailing pending" (or nothing intrusive). Honest; sets the expectation.
   - **Option B: restyle the static log with no marker at all** - cleanest visually, but a
     user familiar with the hi-fi may expect a stream and read the absence as a bug.
   - **Option C: render the hi-fi `LIVE` badge anyway** - rejected (dishonest; there is no
     stream). Listed only to close it off.

4. **Layout fidelity level (the framing decision).** How close to `HoloJobDetail`'s full
   surface vs a pragmatic restyle of the existing structure onto the primitives?
   - **Option A (recommend): pragmatic restyle.** Keep the current page's proven structure
     (header, left tasks/DAG/progress column, right Spec/Log tabs) and re-skin it with
     `GlassPanel`/`Chip`/`PillButton` + the breadcrumb header, omitting every unbacked hi-fi
     element. This is the same philosophy the jobs-list and worker specs used and keeps the
     large test surface green.
   - **Option B: high-fidelity rebuild** toward the hi-fi's exact composition (donut, stat
     grid, resizable split, live log). This forces us to either fake unbacked data (donut
     numbers, ETA) or ship empty shells, and pulls in decisions 1/3/5 as "yes". Not
     recommended; it trades honesty and test stability for pixel-fidelity to a mock that
     assumes a richer backend than we have.
   - This decision is upstream of the others: choosing A makes 1/5 lean "fixed/bar", B
     leans "resizer/donut".

5. **Progress: donut vs the existing bar.** The hi-fi wraps progress in a `Donut` ring
   (`JOB_DETAIL.pct`). The current page uses a horizontal `progressPct` bar.
   - **Option A (recommend): keep the bar.** The % is the same derived number either way;
     the bar is simpler, already tested (the `1 / 2 tasks done` text), and consistent with
     the jobs-list row bars. The donut is pure visual re-expression.
   - **Option B: adopt the donut** for header emphasis. Nice hero element, but it is a new
     SVG component for no new information, and the hi-fi donut sits inside the (mostly
     unbacked) Elapsed/ETA stat block we are omitting - lifting just the donut out of that
     block is a design decision the user should make deliberately.

6. **`JobActions` buttons: `PillButton` vs current inline.** The hi-fi header pills are the
   `PillButton` idiom. `JobActions` currently uses inline `rounded-md border ...` buttons.
   - **Option A (recommend): restyle to `PillButton`** (`ghost` for Cancel, `danger` for
     Force cancel). Behavior and the `name`-based test queries are unaffected; better hi-fi
     fidelity and one fewer inline pattern.
   - **Option B: keep inline** if we want to minimize churn in a preserved component.

7. **Labels row: `Chip` vs inline.** Unlike the jobs-list schedule chip (which needed an
   `accentB` tone `Chip` lacks), the job labels are a plain `accent`-toned pill the `Chip`
   primitive covers directly.
   - **Option A (recommend): adopt `Chip` (`accent` tone).** Clean fit, removes an inline
     pattern.
   - **Option B: keep the inline `accent/40` span** to minimize churn.

8. **Worker cell as a link.** The hi-fi tasks list makes the worker name a link to the
   worker. The current `TasksTable` renders `worker_id.slice(0,6)` as plain text.
   - **Option A (recommend): keep plain text now.** It is a new navigation edge into
     `/workers/:id` and a small addition; defer to keep this relayout a pure restyle. (Note:
     the row is a selection `<button>`, so an inner `<Link>` would need `stopPropagation` -
     an extra interaction wrinkle.) Could be a tiny follow-up backlog item.
   - **Option B: add the worker link** for hi-fi fidelity, accepting the nested-interactive
     wrinkle.

9. **Generic status-dot primitive (smaller, cross-cutting).** This page is plausibly the
   third status-dot consumer (job header, tasks table, plus jobs list + workers already).
   The jobs-list spec flagged extracting a generic `StatusDot` (taking a resolved
   `{label, dotClass, textClass}` view) "when a third consumer appears."
   - **Option A (recommend): still defer** - keep the inline dots here; capture the
     extraction as a backlog idea now that the third consumer is real, and do it as its own
     small slice so jobs/tasks/workers converge deliberately rather than mid-relayout.
   - **Option B: extract the generic primitive as part of this work.** Cleaner long-term,
     but expands scope beyond a restyle and touches the workers page's `StatusDot`.
