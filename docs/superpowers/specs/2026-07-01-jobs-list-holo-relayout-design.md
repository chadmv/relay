# Jobs List Holo Relayout

- Date: 2026-07-01
- Status: Draft (design; awaiting review)
- Owner: relay-tpm
- Scope: web SPA only (`web/src/jobs/`). No backend or Go changes.

## Problem

The shipped Jobs list (`web/src/jobs/JobsPage.tsx` + `JobsTable.tsx`) is a working page
but predates the picked "Holo" hi-fi design and the shared primitive set at
`web/src/components/holo/`. It renders inline `bg-white/5 border-border backdrop-blur`
boxes and a hand-built header/toolbar/footer, duplicating the Holo vocabulary that now
lives in reusable primitives (used by the worker pages, which are the migration
reference). The hi-fi target is `HoloJobsList` in
`design_handoff_relay_holo/hifi3-holo-pages.jsx` (~line 469).

This is a **restyle/relayout of an existing, working page**. Every data path, query key,
pagination behavior, sort rule, and navigation target is preserved exactly. Only
structure and styling change, rebuilt from the shared primitives.

## Design authority and token mapping

Follows the same approach as the worker relayout
(`docs/superpowers/specs/2026-07-01-holo-primitives-worker-detail-design.md`).

The **authoritative look** is the hi-fi Holo prototype (`HoloJobsList`), not the lo-fi
`reference/screens/*` sketch. The app keeps its cyan accent and its fixed `#050410`
background. The prototype threads a `C` token bag (inline styles) and a density switch
`D` (e.g. `D.pad`, `D.gap`, `D.rowPad`, `D.rowFs`, `D.nameFs`) into every component. We
**do not** port the `C` bag, the HSV `makeTokens` machinery, or the `D` density switch:
`C.*` maps onto the existing `tokens.css` Tailwind classes, and `D.*` collapses to fixed
comfortable Tailwind values. The prototype-to-app token mapping is identical to the table
in the worker spec (`C.bg`->`bg-bg`, `C.fg`->`text-fg`, `C.fgMute`->`text-fg-mute`,
`C.accent`->`text-accent`/`bg-accent`, `C.accentB`->`text-accent-b`, `C.ok/warn/err`,
`C.border`->`border-border`, glass radius `14`->`rounded-card`).

The `web/src/components/holo/` primitives are already merged to main. This spec consumes
them; it does not add or modify any primitive.

## Backend reality (confirmed against `internal/api/jobs.go`)

Every column and stat the hi-fi `HoloJobsList` table shows is backed by a **real** field
today. No part of the target table is backend-blocked.

### `GET /v1/jobs` list rows (`jobResponse` + `applyJobEnrichment`)

Confirmed list-row fields, mapped to the client `Job` type (`web/src/jobs/api.ts`) and
the hi-fi columns:

| Hi-fi column | Real field(s) | Client `Job` field | Notes |
| --- | --- | --- | --- |
| ID | `id` | `id` (sliced to 6 in UI) | real |
| NAME | `name` | `name` | real; links to `/jobs/:id` |
| (name schedule chip) | `scheduled_job_name` | `scheduled_job_name?` | real; enrichment, present only for scheduled-origin jobs |
| STATUS | `status` | `status` (`JobStatus`) | real; `pending`/`running`/`done`/`failed`/`cancelled` |
| PROGRESS | `done_tasks`,`total_tasks` | `done_tasks?`,`total_tasks?` | real; `progressPct()` derives % |
| STARTED | `started_at` | `started_at?` | real; enrichment (nullable) |
| DUR | `started_at`,`finished_at` | `started_at?`,`finished_at?` | real; `formatDuration()` derives |
| OWNER | `submitted_by_email` | `submitted_by_email?` | real |

The hi-fi sample also carries `tasks` (e.g. `48/64`) and `priority` columns in its raw
row tuple, but its rendered table does **not** show a dedicated tasks or priority column
(priority is a sort key only, and the tasks count feeds the progress bar). We match the
rendered hi-fi columns, which equal the current app columns. No column is added or
removed.

### `GET /v1/jobs/stats` (`jobStatsResponse`)

Returns exactly `running`, `queued`, `done_24h`, `failed_24h` - the four KPI values the
hi-fi summary strip shows. All real. Consumed via `useJobStats` (`['job-stats']` query
key), unchanged.

Known caveat, already tracked: `done_24h`/`failed_24h` window on `jobs.updated_at` as a
finish-time proxy (`docs/backlog/bug-2026-06-05-jobs-stats-24h-updated-at-proxy.md`). Not
in scope here; the KPI strip renders whatever the endpoint returns.

### Not implemented -> OUT of scope (do not add)

The hi-fi `HoloJobsList` includes three affordances with no backend support. They are
tracked as separate backlog items and are explicitly **excluded** from this relayout:

| Hi-fi affordance | Status | Backlog item |
| --- | --- | --- |
| View toggle: **Lanes** (swimlanes) | not implemented; needs per-status fan-out | `docs/backlog/idea-2026-06-05-jobs-lanes-swimlanes-view.md` |
| View toggle: **Timeline** (6h/24h/7d) | not implemented; needs a new time-window server query | `docs/backlog/idea-2026-06-05-jobs-timeline-view.md` |
| **My jobs** toggle + free-text filter input | not implemented; needs a server `?mine=` predicate (and a search predicate) | `docs/backlog/idea-2026-06-05-my-jobs-toggle-mine-filter.md` |

Because Lanes and Timeline are excluded, the hi-fi **view-switch segmented control**
(Table / Lanes / Timeline) has only one live option (Table). We therefore **omit the
view-switch control entirely** in this pass - rendering a segmented control with two dead
buttons would read as broken. The page shows the Table view unconditionally. When the
Lanes/Timeline backlog items land, that work re-introduces the view switch (its own spec
notes this). Similarly, the hi-fi free-text filter input and "My jobs" pill are **not
rendered** (they have no backing filter and a client-only filter is misleading under
cursor pagination - see the My-jobs backlog item). This keeps the page honest: no control
appears that does not do real work.

## Target layout

Top to bottom, built from the shared primitives. The page keeps its outer
`flex flex-col gap-4` container.

### 1. Header row (eyebrow + title + KPI strip + live/new-job)

A single `flex flex-wrap items-end gap-6` row (as today), restyled to match the worker
page header idiom:

- **Left:** `Eyebrow` primitive with `OVERVIEW`, then `<h1 className="text-[32px]
  font-normal tracking-tight">Jobs</h1>`. (Replaces the current inline
  `font-mono text-[11px] tracking-widest text-fg-mute` eyebrow string with the `Eyebrow`
  primitive, matching `WorkersPage`.)
- **Center:** the KPI summary strip (see section 2).
- **Right (`ml-auto`):** the existing live indicator (`● live · auto-refreshing`, the dot
  colored by `isFetching`) and the `+ New job` `<Link to="/jobs/new">`. **Both preserved
  exactly** - same copy, same target, same accent styling. The `+ New job` link keeps its
  current classes (it is a `Link`, styled as an accent button; it is not converted to
  `PillButton`, since `PillButton` is a `<button>` and this must remain a router `Link`).

### 2. KPI strip (via `KpiStat`)

The hi-fi renders the four counts as an inline mono strip inside the header
(`3 RUNNING · 1 QUEUED · 487 DONE·24H · 12 FAILED·24H`). The app already renders this
exact inline strip. **Decision: keep the compact inline strip, do not convert to a
four-up `KpiStat` grid.**

Rationale: the hi-fi `HoloJobsList` itself uses the inline strip form (the big four-up
`KpiStat` card grid is the *worker detail* idiom, not the jobs-list idiom). Converting to
cards would diverge from the hi-fi and consume vertical space the table needs. The inline
strip is restyled only to match tokens: each count is `<b>` in its status color
(`text-accent` running, `text-warn` queued, `text-ok` done, `text-err` failed) with a
mono `text-fg-mute` label, fed by `stats?.running ?? 0` etc. from `useJobStats`.

`KpiStat` is therefore **not used** on this page. (If a later redesign wants the four-up
card treatment, `KpiStat` is ready; see Open Decisions.) This keeps the relayout faithful
to the actual `HoloJobsList` mock rather than importing the worker-detail vocabulary.

### 3. Toolbar (status filter chips + sort)

A `flex flex-wrap items-center gap-2` row (as today):

- **Status filter chips:** `All / Running / Queued / Done / Failed`, mapping to
  `status=''/running/pending/done/failed` via the existing `FILTERS` array. These stay as
  the current toggle buttons (`aria-pressed`, accent-tinted when active) - they are **not**
  swapped for the `Chip` primitive, because `Chip` is a mono pill for labels/tags, whereas
  these are pressable sans-serif filter toggles with `aria-pressed` semantics that the
  tests and a11y rely on. Their styling is already close to the hi-fi filter pills; we
  keep the existing classes (they already use `rounded-full border ... bg-accent/15`).
- **Sort control (`ml-auto`):** the existing `SortControl` (`web/src/jobs/SortControl.tsx`,
  a native `<select aria-label="Sort jobs">`) is **preserved unchanged**, including its
  `disabled` + `disabledHint` behavior. The sort-disabled-while-status-filtered rule
  stays: `pickFilter` snaps `sort` back to `DEFAULT_SORT` and the control is disabled when
  `statusFiltered`. This is load-bearing (the server 400s sort+status together) and is
  covered by an existing test asserting `getByLabelText('Sort jobs')` is disabled.

The hi-fi's richer custom `SortControl` dropdown (mono value tokens, animated menu) is a
separate polish item and is **not** adopted here; the native `<select>` is kept for
accessibility and to preserve the existing test surface. (See Open Decisions.)

### 4. Table shell (via `Panel`)

Replace the current bare `rounded-card border border-border bg-white/5 backdrop-blur`
wrapper with the `Panel` primitive so the table sits in a proper glass surface with a
header/footer frame, matching the hi-fi `glassPanel` table container:

- `<Panel>` wraps the whole table block. The `Panel` header carries the column-header row
  concept, and the `Panel` `footer` slot carries the pagination footer (section 5).
- **However**, `Panel`'s built-in header is a single title/meta row - the jobs table needs
  a multi-column grid header (`ID NAME STATUS PROGRESS STARTED DUR OWNER`). So the
  column-header grid stays as a custom row rendered as the first child inside the `Panel`
  body (or the table keeps a `GlassPanel` wrapper with an explicit grid header + footer,
  if that reads cleaner). **Decision:** use `GlassPanel` as the table container (not
  `Panel`), with the existing grid column-header row and the pagination footer rendered as
  explicit `border-b`/`border-t` rows inside it. `GlassPanel` gives the exact hi-fi glass
  surface (gradient + inset/drop shadow) that the current flat `bg-white/5` lacks, which
  is the one intentional visual upgrade. `Panel` is a poorer fit here because its header is
  single-cell; forcing a 7-column grid through it fights the primitive.

The `JobsTable` component owns the `GlassPanel` container, the grid header row, and the
data rows. The empty state ("No jobs yet.") restyles from the current
`bg-white/5` box to a `GlassPanel` (matching `WorkersPage`'s empty/error cards).

### 5. Pagination footer

The `SHOWING x-y of total · SORT <sort> · CURSOR PAGINATED` line plus `← prev` /
`next 50 →` buttons. **All pagination logic is preserved verbatim** from the current
`JobsPage`: the `cursor`/`stack`/`startOffset`/`offsets` state, `next()`/`prev()`,
`computePageRange`, `rangeText`, the `SORT status=<s>` vs `SORT <sort>` display when
filtered, and the `disabled` conditions (`stack.length === 0 || isPlaceholderData` for
prev; `!data?.next_cursor || isPlaceholderData` for next). Only styling changes: the
footer renders inside the table `GlassPanel` as a `border-t` row (matching the hi-fi
footer position) rather than as a detached row below the table.

The prev/next buttons restyle to the hi-fi ghost-pill look. **Decision:** keep them as
plain `<button>`s with the current pill classes (they already match:
`rounded-full border border-border ... disabled:opacity-40`) rather than swapping to the
`PillButton` primitive, because `PillButton`'s `ghost` variant uses `px-4 py-2` sizing and
these footer buttons are the smaller `px-3 py-1 text-[11px]` variant the hi-fi uses
(`pillBtn(C,'ghost')` at `padding:'4px 12px', fontSize:11`). Matching that compact size
via `PillButton` would require a `className` size override on every use; the existing
inline classes are simpler and the buttons are not reused elsewhere. (See Open Decisions
for the alternative.)

## JobsTable restyle (rows and cells)

`JobsTable.tsx` keeps its data contract (`{ jobs: Job[] }`), its `COLS` grid, and all
`status.ts` helpers (`statusColor`, `progressPct`, `formatDuration`, `formatStarted`).
Changes are container + per-cell styling to hi-fi tokens:

- **Container:** `GlassPanel` (gradient glass) instead of flat `bg-white/5`. Column-header
  grid row and the data rows keep the same `grid-cols-[...]` template. Consider aligning
  the template to the hi-fi widths
  (`90px 1fr 110px 140px 90px 80px 130px 32px`, including a trailing `›` chevron column);
  **Decision:** keep the current 7-column template
  (`90px_1fr_120px_150px_120px_70px_150px`) and do **not** add the hi-fi trailing chevron
  column - the whole row is already click-navigable via the name `Link`, and adding a
  decorative chevron column is cosmetic. Widths may be nudged to match the hi-fi feel but
  the column set is unchanged.
- **ID cell:** `text-fg-mute`, `id.slice(0, 6)` (unchanged).
- **NAME cell:** the `<Link to={`/jobs/${j.id}`}>` is **preserved exactly** - same target,
  same `truncate font-sans text-[13px] text-fg hover:text-accent`. Row-click navigation is
  this link (unchanged). The schedule chip (`⟳ {scheduled_job_name}`) stays; **Decision:**
  keep the current inline `accent-b` chip span rather than the `Chip` primitive, because
  the schedule chip is an `accent-b`-toned pill with a `⟳` glyph and a `title` tooltip that
  the `Chip` primitive's three tones (`accent`/`muted`/`warn`) do not cover, and it is
  used only here. (See Open Decisions - a `Chip` `accentB` tone could absorb it later.)
- **STATUS cell:** **keep the `status.ts`-driven dot + label** (`statusColor(j.status)`
  gives `{ text, dot }`; render `<span className="h-1.5 w-1.5 rounded-full ${c.dot}"/>` +
  the status text in `c.text`). We do **not** use the shared holo `StatusDot` here: that
  primitive takes `WorkerStatus` and drives off `livenessView` (worker liveness
  vocabulary), whereas jobs have a distinct `JobStatus` vocabulary
  (`pending/running/done/failed/cancelled`) with its own `status.ts` color map. Forcing
  jobs through the worker `StatusDot` would misclassify statuses. The existing inline
  pattern is correct and is preserved.
- **PROGRESS cell:** the current inline track + fill bar with `progressPct` and a
  status-toned fill (`done`->ok, `failed`->err, else accent) plus the `{pct}%` label.
  **Decision:** the fill bar **may** adopt the `ProgressBar` primitive
  (`web/src/components/holo/ProgressBar.tsx`) for the accent case, but `ProgressBar`'s fill
  is always the accent gradient (with a `muted` tone), and the jobs bar needs per-status
  fill colors (ok/err/accent). Since `ProgressBar` cannot express ok/err fills without an
  extension, **keep the current inline bar** (it already matches the hi-fi row bar:
  `h-1 rounded bg-white/10` track, status-colored fill). This mirrors the worker spec's
  choice to keep existing per-metric colors rather than extend a primitive prematurely.
- **STARTED / DUR / OWNER cells:** `text-fg-mute`, fed by `formatStarted`,
  `formatDuration`, `submitted_by_email ?? '-'` (unchanged). OWNER keeps `truncate`.
- **Row emphasis:** the hi-fi tints running rows (`background: rgba(accent,0.04)`).
  **Decision:** add a subtle running-row tint (`bg-accent/[0.04]`) to match the hi-fi, a
  small, purely-visual enhancement keyed on `j.status === 'running'`. Low risk; no
  behavior change.

## Preserved vs changed

**Preserved exactly (behavior, contracts, tests-relevant):**

- `useJobs(sort, status, cursor)` hook and its `['jobs', sort, status, cursor]` query key,
  `refetchInterval`, `keepPreviousData`.
- `useJobStats()` hook and its `['job-stats']` query key.
- Cursor pagination: `cursor`/`stack`/`startOffset`/`offsets` state machine, `next()`,
  `prev()`, `computePageRange`, `rangeText`, `isPlaceholderData`-gated button disabling.
- Sort behavior: `SortControl` options, the disabled-while-filtered rule, `pickSort`
  resetting pagination, `pickFilter` snapping sort to `DEFAULT_SORT`.
- Status filter chips (`FILTERS` mapping, `aria-pressed`), including `Queued`->`pending`.
- `+ New job` `<Link to="/jobs/new">` (any authenticated user).
- Row -> `/jobs/:id` navigation via the name `Link`.
- All `status.ts` helpers and the `Job`/`JobStats`/`JobsPage`/`JobSort` API types.
- Loading skeleton and error-with-retry states (restyled, not removed).

**Changed (structure/styling only):**

- Eyebrow inline string -> `Eyebrow` primitive.
- Table/empty/error/skeleton wrappers: flat `bg-white/5` -> `GlassPanel` (gradient glass
  fidelity upgrade - the one intentional visual change, applied uniformly, matching the
  worker pages).
- Pagination footer moves inside the table `GlassPanel` as a `border-t` row.
- KPI strip and filter chips: token/spacing polish to match hi-fi; no structural change.
- Running-row tint added.
- View-switch control, free-text filter input, and "My jobs" pill from the hi-fi: **not
  rendered** (backend-blocked / out of scope, per Backend reality).

## Backend-blocked / graceful handling

Nothing in the **target** (columns + KPI strip) is backend-blocked - all fields are real.
The only "blocked" items are the three hi-fi affordances excluded above (Lanes, Timeline,
My jobs / search). Graceful handling = **omit** them rather than render dead controls, and
each omission is traceable to its backlog item via a short code comment in `JobsPage.tsx`
(e.g. `// View switch (Lanes/Timeline) omitted until idea-2026-06-05-jobs-*-view lands`).
This is the honest treatment for a list page: unlike the worker-detail panels (where an
empty shell communicates "coming soon"), a dead toolbar control on a list reads as broken.

## Shared status primitive (note for later)

Jobs and workers each maintain their own status-color mapping (`jobs/status.ts`
`statusColor` for `JobStatus`; `workers/liveness.ts` `livenessView` for `WorkerStatus`),
and the holo `StatusDot` is hard-wired to the worker vocabulary. As more pages adopt
status dots (scheduled jobs, tasks, admin), a **generic status-dot primitive** -
`StatusDot` taking a resolved `{ label, dotClass, textClass }` view (or a small
`tone` union) rather than a domain status enum - would let both domains share it without
one importing the other's vocabulary. This is **not** done in this pass (YAGNI: only two
consumers today, and the jobs table's inline dot is trivial). Recommend capturing it as a
backlog idea when a third status-dot consumer appears; noted here so the future extraction
is discoverable. Not a blocker for this relayout.

## Test impact

Vitest (`cd web && npm test`). No Go tests affected. Behavior assertions are preserved;
only structural/class-based assertions change.

- **`JobsTable.test.tsx`** - should largely survive: it queries by visible text
  (`film-x / shot-042 render`, `mira@studio.dev`, `75%`, `100%`, `nightly-etl`), by the
  empty-state text (`/no jobs/i`), and by the name `link` role/href (`/jobs/9F4E1C`). All
  of those are preserved. Only update needed if any assertion keys on the old container
  classes (none currently do). The schedule-chip and status assertions remain valid since
  that markup is preserved.
- **`JobsPage.test.tsx`** - behavior assertions preserved:
  - KPI strip (`487`, `12`) - preserved (inline strip retained).
  - Status chip re-request + sort disabled (`getByRole('button', { name: 'Running' })`,
    `getByLabelText('Sort jobs')` disabled, `status=pending` for Queued) - preserved
    (chips and native `SortControl` unchanged).
  - Error banner + retry, pagination range/prev/next/disabled-in-flight tests - preserved
    (logic unchanged; buttons still match `getByRole('button', { name: /prev|next/i })`).
  - `+ New job` link (`getByRole('link', { name: /new job/i })` -> `/jobs/new`) -
    preserved.
  - Any assertion that (implicitly) depends on the footer being a sibling of the table vs
    a child of the table `GlassPanel` should still pass, since queries are by text/role,
    not DOM position. Re-check after implementation; adjust only if a structural query
    breaks.
- **New tests:** none required (no new component; primitives are already tested). If the
  running-row tint or the omission of the view switch warrants a guard, a light assertion
  ("no Lanes/Timeline toggle rendered") may be added, but it is optional.

## Open decisions

1. **KPI strip form.** Recommendation (baked in): keep the compact inline strip to match
   the actual `HoloJobsList` mock; do **not** convert to a four-up `KpiStat` card grid
   (that is the worker-detail idiom). Confirm, or opt into cards for cross-page
   consistency.
2. **Table container primitive.** Recommendation (baked in): use `GlassPanel` + explicit
   grid header/footer rows rather than `Panel`, because `Panel`'s single-cell header
   fights the 7-column grid. Confirm, or extend `Panel` to accept a custom header node.
3. **Prev/next + footer buttons.** Recommendation (baked in): keep plain `<button>`s with
   the existing compact pill classes rather than `PillButton` (whose `ghost` variant is a
   larger `px-4 py-2`). Confirm, or add a `size` prop to `PillButton` and adopt it.
4. **Schedule chip + status dot as `Chip`/`StatusDot`.** Recommendation (baked in): keep
   both inline - the schedule chip needs an `accent-b` tone `Chip` lacks, and the status
   dot needs the `JobStatus` vocabulary the worker `StatusDot` lacks. Confirm, or extend
   `Chip` (add `accentB` tone) and extract a generic status-dot primitive now.
5. **Sort control fidelity.** Recommendation (baked in): keep the native `<select>`
   `SortControl` for accessibility and test stability; defer the hi-fi custom dropdown
   (mono `?sort=` value tokens, animated menu) as a later polish item. Confirm.
6. **Shared status-dot primitive.** Recommendation (baked in): do not extract now (two
   consumers; YAGNI); capture as a backlog idea when a third consumer appears. Confirm the
   backlog-when-needed approach.
