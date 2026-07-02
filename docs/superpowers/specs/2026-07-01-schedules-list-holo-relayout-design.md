# Schedules List Holo Relayout

- Date: 2026-07-01
- Status: Draft (design; awaiting review)
- Owner: relay-tpm
- Scope: web SPA only (`web/src/schedules/`). No backend or Go changes.

## Problem

The shipped Schedules list (`web/src/schedules/SchedulesPage.tsx` + `SchedulesTable.tsx`)
is a working page but predates the picked "Holo" hi-fi design and the shared primitive set
at `web/src/components/holo/`. It renders inline `bg-white/5 border-border backdrop-blur`
boxes and a hand-built header/toolbar/footer, duplicating the Holo vocabulary that now
lives in reusable primitives (used by the worker pages and, as of the just-merged jobs-list
relayout, the jobs page). The hi-fi target is `HoloSchedules` in
`design_handoff_relay_holo/hifi3-holo-pages.jsx` (~line 1510).

This is a **restyle/relayout of an existing, working page**, mirroring the just-shipped
jobs-list relayout (`docs/superpowers/specs/2026-07-01-jobs-list-holo-relayout-design.md`).
Every data path, query key, pagination behavior, sort rule, row action, and navigation
target is preserved exactly. Only structure and styling change, rebuilt from the shared
primitives.

## Design authority and token mapping

Follows the same approach as the jobs-list and worker relayouts. The **authoritative look**
is the hi-fi Holo prototype (`HoloSchedules`), not the lo-fi `reference/screens/*` sketch.
The app keeps its cyan accent and its fixed `#050410` background. The prototype threads a
`C` token bag (inline styles) and a density switch `D` into every component. We **do not**
port the `C` bag, the HSV `makeTokens` machinery, or the `D` density switch: `C.*` maps onto
the existing `tokens.css` Tailwind classes, and `D.*` collapses to fixed comfortable
Tailwind values. The prototype-to-app token mapping is identical to the jobs-list spec's
table (`C.bg`->`bg-bg`, `C.fg`->`text-fg`, `C.fgMute`->`text-fg-mute`, `C.fgDim`->`text-fg-dim`,
`C.accent`->`text-accent`/`bg-accent`, `C.accentB`->`text-accent-b`, `C.ok/warn/err`,
`C.border`->`border-border`, glass radius `14`->`rounded-card`).

The `web/src/components/holo/` primitives are already merged to main. This spec consumes
them; it does not add or modify any primitive.

## Backend reality (confirmed against `internal/api/scheduled_jobs.go` and `server.go`)

The schedules API surface is CRUD plus run-now. Confirmed routes:

- `POST /v1/scheduled-jobs`, `GET /v1/scheduled-jobs`, `GET /v1/scheduled-jobs/{id}`,
  `PATCH /v1/scheduled-jobs/{id}`, `DELETE /v1/scheduled-jobs/{id}`,
  `POST /v1/scheduled-jobs/{id}/run-now`.

**There is no `GET /v1/scheduled-jobs/stats` endpoint** (grep of `internal/api` confirms:
no stats handler, no route). This is the single biggest difference from the jobs-list
relayout, where `GET /v1/jobs/stats` backed the KPI strip. Here the summary strip must stay
page-scoped.

### `GET /v1/scheduled-jobs` list rows (`scheduledJobResponse`)

Confirmed list-row fields (mapped to the client `Schedule` type in
`web/src/schedules/api.ts`, matched field-for-field to the Go response) and the hi-fi
columns:

| Hi-fi column | Real field | Client `Schedule` field | Notes |
| --- | --- | --- | --- |
| NAME | `name` | `name` | real; plain text (no link - no detail route, see below) |
| (name status dot) | `enabled` | `enabled` | real; enabled->ok dot, paused->dim dot |
| CRON | `cron_expr` | `cron_expr` | real |
| TZ | `timezone` | `timezone` | real |
| OVERLAP | `overlap_policy` | `overlap_policy` | real; `allow`/`skip` pill |
| NEXT RUN | `next_run_at` | `next_run_at` | real; `nextRunDisplay()` derives "in Xm"/"due" |
| LAST RUN | `last_run_at` | `last_run_at?` | real; `formatRelativeTime()`; `-` when null |
| LAST JOB | `last_job_id` | `last_job_id?` | real; `shortId()` (first 8 chars); `-` when null |
| OWNER | `owner_email` | `owner_email` | real (resolved server-side via `fillOwnerEmails`) |
| ACTIONS | (derived) | - | Run now + Enable/Disable |

The list response envelope is `{ items, next_cursor, total }` (Go `page[scheduledJobResponse]`).
`total` is `CountScheduledJobs` (admin) or `CountScheduledJobsByOwner` (owner) - a real
fleet/owner-wide count, so the footer's `of {total}` is accurate even though the summary
strip's ENABLED/PAUSED counts are page-scoped.

Every column the current app renders is backed by a real field. No column is added or
removed by this relayout.

### Differences from the hi-fi `HoloSchedules` mock

Three hi-fi affordances have no backend or router support and are **not** rendered
(matching the current app, which already omits them). Each traces to a backlog item:

| Hi-fi affordance | Status | Backlog item |
| --- | --- | --- |
| Summary stat: **`N FAILED · 24H`** (third strip stat) | not implemented; needs a failed-runs aggregate | `docs/backlog/idea-2026-06-05-failed-24h-stat.md` |
| Toolbar **filter chips** (All / Enabled / Disabled) + **free-text search input** ("Filter by name, owner, cron...") | not implemented; needs server `enabled=` + `q=` predicates | `docs/backlog/idea-2026-06-05-schedules-filter-search.md` |
| Row **`Edit`** button (`onEdit`) and clickable **LAST JOB** link (`onOpenJob`) | not implemented; no schedule-detail route exists (`/schedules/:id`), and the list does not wire a job-detail nav on the last-job cell today | see "No detail route" below |

Additionally the hi-fi conditionally hides `Run now` on paused rows (shows only `Enable`).
The current app **always shows `Run now`** (owner/admin may fire a paused schedule's spec
on demand) plus the Enable/Disable toggle. We preserve the current always-Run-now behavior;
this is a deliberate divergence from the mock and is load-bearing (a test asserts a disabled
row shows both `Run now` and `Enable`).

A fleet-wide **ENABLED/PAUSED summary strip** (accurate across all schedules, not just the
loaded page) would require the stats endpoint
(`docs/backlog/idea-2026-06-05-schedules-stats-endpoint.md`). Until then the strip stays
page-scoped, exactly as today.

### No detail route

The router (`web/src/app/router.tsx`) exposes only `/schedules` for this domain - there is
**no `/schedules/:id` (or `/schedules/:name`) detail page**. So:

- The NAME cell is **plain text**, not a `Link` (unlike the jobs NAME cell). There is no
  row-click navigation. This matches the current app.
- The hi-fi `Edit` button (which would navigate to a detail/edit page) is **not rendered**.
- The hi-fi LAST JOB cell is a clickable link to the job detail (`onOpenJob(jobId)`). The
  current app renders LAST JOB as **plain `shortId` text**. Wiring it to `/jobs/:id` would
  be a behavior change (a job-detail route does exist), but it is **out of scope** for a
  pure restyle and would need its own decision (see Open Decisions). This relayout keeps
  LAST JOB as plain text.

## Target layout

Top to bottom, built from the shared primitives. The page keeps its outer
`flex flex-col gap-4` container.

### 1. Header row (eyebrow + title + summary strip)

A single `flex flex-wrap items-end gap-6` row (as today), restyled to match the jobs/worker
page header idiom:

- **Left:** `Eyebrow` primitive with `RECURRING`, then
  `<h1 className="text-[32px] font-normal tracking-tight">Schedules</h1>`. Replaces the
  current inline `font-mono text-[11px] tracking-widest text-fg-mute` eyebrow string with
  the `Eyebrow` primitive, matching `JobsPage`/`WorkersPage`.
- **Center:** the page-scoped summary strip (see section 2).
- **Right (`ml-auto`):** the sort control (see section 3). The current page places the sort
  control in the header's `ml-auto` slot; we keep it there rather than in a separate toolbar
  row, because - unlike jobs - schedules has no live filter chips to anchor a toolbar (see
  section 3). There is no `+ New schedule` button today (schedule creation is not yet a
  page); none is added.

### 2. Summary strip (page-scoped ENABLED / PAUSED)

The hi-fi renders three inline mono stats (`N ENABLED · N PAUSED · 2 FAILED · 24H`). The app
already renders the first two (`countEnabled` over the loaded page) plus a `{total} schedules`
tail. **Decision: keep the compact inline strip in its current page-scoped form, do not
convert to a `KpiStat` grid, and do not add the FAILED·24H stat.**

Rationale:
- `HoloSchedules` itself uses the inline strip form (the four-up `KpiStat` card grid is the
  worker-detail idiom). Converting to cards would diverge from the mock and eat table space.
  This mirrors the jobs-list decision to keep the inline strip. `KpiStat` is therefore
  **not used** on this page.
- The ENABLED/PAUSED counts are **page-scoped** (they count only the loaded 50), which is a
  known limitation tracked by `docs/backlog/idea-2026-06-05-schedules-stats-endpoint.md`.
  A stats endpoint would make them fleet-wide; until it lands we keep the honest page-scoped
  counts and the `{total} schedules` tail (which *is* fleet/owner-wide, from the envelope's
  `total`).
- The **FAILED·24H** stat has no backing aggregate and is **omitted**
  (`docs/backlog/idea-2026-06-05-failed-24h-stat.md`). Rendering a hardcoded or fabricated
  count would be misleading.

The strip is restyled only for token/spacing fidelity: `<b className="text-ok">{enabled}</b>
ENABLED`, `<b className="text-fg">{paused}</b> PAUSED`, and the `{total} schedules` tail in
`text-fg-dim`/`text-fg-mute`, fed by `countEnabled(schedules)` and `data.total` exactly as
today.

### 3. Sort control (native `<select>`, header-right)

The current page uses a plain inline `<select aria-label="Sort">` in the header (not the
`web/src/jobs/SortControl.tsx` component - schedules has never had a shared SortControl).
**Decision: keep the native inline `<select aria-label="Sort">`** with its eight
`SORT_OPTIONS` (`-created_at`/`created_at`/`name`/`-name`/`next_run_at`/`-next_run_at`/
`-updated_at`/`updated_at`), restyled only to hi-fi tokens (rounded, `bg-black/25` border).

Rationale, matching the jobs-list philosophy:
- The hi-fi uses its richer custom `SortControl` dropdown (mono `?sort=` value tokens,
  animated menu). Adopting it is a separate cross-page polish item; the native `<select>`
  is kept for accessibility and to preserve the existing test surface (a test does
  `selectOptions(getByLabelText('Sort'), 'name')`).
- Unlike jobs, schedules has **no status-filter chips** and therefore **no
  sort-disabled-while-filtered rule** - the sort control is always enabled. `chooseSort`
  resets pagination (`setCursorStack([])`, `setOffsets([])`, `setStartOffset(0)`) and is
  preserved verbatim.
- The hi-fi filter chips (All/Enabled/Disabled) and the search input are backend-blocked
  (`idea-2026-06-05-schedules-filter-search.md`) and are **not rendered**. Because there are
  no live filter controls, we do not introduce a separate toolbar row; the sort control
  stays in the header. A client-only Enabled/Disabled filter over the loaded page would be
  misleading under cursor pagination (it would filter only 50 rows), so it is deliberately
  not added - same honesty rule as the jobs-list "My jobs" omission.

### 4. Table shell (via `GlassPanel`)

Replace the current bare `rounded-card border border-border bg-white/5 backdrop-blur`
wrapper (in `SchedulesTable.tsx`) with the `GlassPanel` primitive so the table sits in the
hi-fi glass surface (gradient + inset/drop shadow) that the flat `bg-white/5` lacks. This is
the one intentional visual upgrade, applied uniformly.

**Decision:** use `GlassPanel` as the table container (not `Panel`), with the existing grid
column-header row and the pagination footer rendered as explicit `border-b`/`border-t` rows
inside it. This mirrors the jobs-list decision exactly: `Panel`'s built-in header is a
single title/meta cell and fights the 9-column grid header
(`NAME CRON TZ OVERLAP NEXT RUN LAST RUN LAST JOB OWNER ACTIONS`).

`SchedulesTable` owns the `GlassPanel` container, the grid header row, and the data rows.
The empty state ("No schedules yet.") and the error card restyle from the current
`bg-white/5` box to a `GlassPanel` (matching `JobsPage`/`WorkersPage` empty/error cards).
The loading skeleton keeps its six placeholder bars, restyled to `GlassPanel` tone.

### 5. Pagination footer (moves inside the table `GlassPanel`)

The current `SHOWING x-y of total · OWNED + ADMINISTRATIVE` line plus `← prev` / `next 50 →`
buttons live in a detached row below the table. **Decision:** move the footer **inside** the
table `GlassPanel` as a `border-t` row (matching the hi-fi footer position), and restyle the
`SHOWING ... · SORT <sort> · OWNED + ADMINISTRATIVE` line to the hi-fi form.

The hi-fi footer reads `SHOWING 1-N OF total · SORT <sort> · OWNED + ADMINISTRATIVE`. The
current app reads `SHOWING x-y of total · OWNED + ADMINISTRATIVE` (no SORT segment).
**Decision:** add the `· SORT <sort>` segment (rendering the current `sort` value token in
`text-accent-b`) to match the hi-fi, a small display-only addition; the `OWNED +
ADMINISTRATIVE` scope tail is preserved.

**All pagination logic is preserved verbatim:** the `cursorStack`/`startOffset`/`offsets`
state, `goNext()`/`goPrev()` (plain-setter batching under StrictMode, per the current
comment), `computePageRange` (`x`,`y`), the `total` display, and the `disabled` conditions
(`cursorStack.length === 0 || isPlaceholderData` for prev; `!data?.next_cursor ||
isPlaceholderData` for next). Only styling and the added SORT segment change.

The prev/next buttons keep their current compact pill classes (`rounded-full border
border-border px-3 py-1 text-[11px] ... disabled:opacity-40`) rather than swapping to the
`PillButton` primitive - identical rationale to the jobs-list spec: `PillButton`'s `ghost`
variant is the larger `px-4 py-2`, and the hi-fi footer buttons are the compact
`padding:'4px 12px', fontSize:11` variant. Existing inline classes match and are simpler.

### 6. Footer note (Run now attribution)

The hi-fi renders a small mono note below the table:
`Run now is admin-only · submits a fresh job from the stored job_spec, attributed to the
schedule's owner.` The current app does not render this note. **Decision:** do not add it in
this pass (it is copy, not layout, and "admin-only" is inaccurate - run-now is allowed for
the owner *or* an admin per `ownedScheduledJob`). If we want an attribution hint later, it
should say "owner or admin". Noted in Open Decisions.

## SchedulesTable restyle (rows and cells)

`SchedulesTable.tsx` keeps its data contract
(`{ schedules, pendingId, onRunNow, onToggleEnabled }`), its `COLS` grid, and all `format.ts`
helpers (`nextRunDisplay`, `formatRelativeTime`, `shortId`). Changes are container + per-cell
styling to hi-fi tokens:

- **Container:** `GlassPanel` (gradient glass) instead of flat `bg-white/5`. The column-header
  grid row and the data rows keep the same `grid-cols-[...]` template. **Decision:** keep the
  current 9-column template (`1.4fr 120px 110px 90px 1fr 1fr 110px 1.3fr 150px`); widths may
  be nudged toward the hi-fi feel (`1.2fr 120px 130px 80px 1.1fr 1.1fr 130px 130px 140px`)
  but the column *set* is unchanged and no trailing chevron column is added (there is no
  row-click nav to signpost).
- **NAME cell:** the enabled/paused status dot (`h-1.5 w-1.5 rounded-full bg-ok` when
  enabled, `bg-fg-dim` when paused) plus the name as **plain text**
  (`truncate font-sans text-[13px] text-fg`). **Decision: keep the inline enabled dot; do
  NOT use the shared holo `StatusDot`** - that primitive takes `WorkerStatus` and drives off
  `livenessView` (worker liveness vocabulary), whereas a schedule's indicator is a simple
  boolean enabled/paused, not a worker status. Forcing it through the worker `StatusDot`
  would misclassify it. This is the same rationale the jobs-list spec used for keeping its
  `JobStatus` dot inline. The name stays plain text (no detail route; see "No detail route").
- **CRON cell:** `text-fg` mono (unchanged).
- **TZ cell:** `truncate text-fg-mute` (unchanged).
- **OVERLAP cell:** the current inline pill (`rounded-full border border-border px-1.5 py-0.5
  ... uppercase`, tinted `text-accent` when `allow`, else `text-fg-mute`). **Decision: keep
  the inline pill; do NOT use the `Chip` primitive** - the overlap pill is a tiny
  `9.5px`-uppercase token whose color keys on the `allow`/`skip` value, which the `Chip`
  primitive's `accent`/`muted`/`warn` tones do not cleanly express, and it is used only here.
  Same "don't force `Chip`" rationale as the jobs-list schedule chip.
- **NEXT RUN cell:** `nextRunDisplay(s.next_run_at)` with the leading play glyph when enabled
  (hi-fi uses `▸` in `accent-b`; current uses `&#9658;` in `text-accent`). **Decision:** keep
  the current glyph, retint to `text-accent-b` to match the hi-fi. Color is `text-fg` when
  enabled, `text-fg-dim` when paused (unchanged).
- **LAST RUN cell:** `formatRelativeTime(s.last_run_at)` or `-`, in `text-fg-mute` (unchanged).
- **LAST JOB cell:** `shortId(s.last_job_id)` as **plain text** in `text-fg-mute`. The hi-fi
  makes this a clickable job link with a status dot; we keep it as plain text (out of scope,
  see "No detail route" and Open Decisions). No status dot is added (the list row does not
  carry the last job's status - only its id).
- **OWNER cell:** `truncate text-fg-mute`, `s.owner_email` (unchanged).
- **Row dimming:** paused rows keep `opacity-[0.55]` (matches the hi-fi `opacity:0.55`).
  **Decision:** do not add a running-row tint (schedules rows have no run-status; the jobs
  running-row tint has no analogue here).
- **ACTIONS cell (preserved behavior):** `Run now` (accent pill:
  `rounded-md border border-accent/50 bg-accent/15 px-2.5 py-1`) and `Enable`/`Disable`
  (ghost pill: `rounded-md border border-border bg-white/5`). **Both buttons render on every
  row regardless of enabled state** (current behavior; diverges from the hi-fi's conditional
  Run-now). Both `disabled` when `pendingId === s.id`. `onRunNow(s.id)` and
  `onToggleEnabled(s.id, !s.enabled)` wiring is preserved verbatim. **Decision: keep plain
  `<button>`s, not `PillButton`** - they are the compact `miniBtn` size and the accent/ghost
  variants map to the current classes; swapping to `PillButton` would need per-use size
  overrides. The `Edit` button from the hi-fi is not added (no detail/edit route).

## Preserved vs changed

**Preserved exactly (behavior, contracts, tests-relevant):**

- `useSchedules(sort, cursor)` hook and its `['schedules', sort, cursor ?? '']` query key,
  `refetchInterval` (10s), `placeholderData: keepPreviousData`.
- `useScheduleActions()` `runNow`/`setEnabled` mutations and their
  `invalidateQueries({ queryKey: ['schedules'] })` on success.
- The 1s `setTick` interval that refreshes relative "next run"/"last run" strings between
  polls.
- Cursor pagination: `cursorStack`/`startOffset`/`offsets` state, `goNext()`, `goPrev()`,
  `computePageRange`, the range/total display, `isPlaceholderData`-gated button disabling.
- Sort behavior: the eight `SORT_OPTIONS`, `chooseSort` resetting pagination, the native
  `<select aria-label="Sort">`.
- Row actions: `onRunNow` / `onToggleEnabled` handlers, the `pendingId` single-flight guard,
  `Run now` on every row, `Enable`/`Disable` toggle label keyed on `s.enabled`.
- Action-error banner (`runNow.error ?? setEnabled.error`), restyled not removed.
- All `format.ts` helpers and the `Schedule`/`SchedulesPage`/`ScheduleSort` API types.
- Loading skeleton, empty state ("No schedules yet."), and error-with-retry states
  (restyled, not removed).
- LAST JOB as plain text; NAME as plain text (no navigation - no detail route).

**Changed (structure/styling only):**

- Eyebrow inline string -> `Eyebrow` primitive.
- Table/empty/error/skeleton wrappers: flat `bg-white/5` -> `GlassPanel` (gradient glass
  fidelity upgrade, applied uniformly, matching the jobs/worker pages).
- Pagination footer moves inside the table `GlassPanel` as a `border-t` row.
- Footer gains the `· SORT <sort>` segment (`text-accent-b` value token) to match the hi-fi.
- NEXT RUN play glyph retinted to `text-accent-b`.
- Summary strip and action/prev/next buttons: token/spacing polish; no structural change.
- Column widths may be nudged toward the hi-fi template; column set unchanged.
- Backend-blocked hi-fi affordances (FAILED·24H stat, filter chips, search input, Edit
  button, clickable last-job link): **not rendered**, each traceable to its backlog item via
  a short code comment in `SchedulesPage.tsx`/`SchedulesTable.tsx`.

## Backend-blocked / graceful handling

Everything the **current app renders** is backed by real fields. The "blocked" items are the
hi-fi affordances above. Graceful handling = **omit** them rather than render dead controls
or fabricated numbers, matching the jobs-list honesty rule: on a list page a dead toolbar
control or a fake stat reads as broken. Each omission carries a short code comment pointing
at its backlog item (e.g.
`// FAILED·24H stat omitted until idea-2026-06-05-failed-24h-stat lands`;
`// filter chips + search omitted until idea-2026-06-05-schedules-filter-search lands`;
`// summary strip is page-scoped until idea-2026-06-05-schedules-stats-endpoint lands`).

When the stats endpoint lands, that work re-introduces the fleet-wide ENABLED/PAUSED counts
and the FAILED·24H stat (its own spec); when the filter/search endpoint lands, that work
re-introduces the chips + search input and a proper toolbar row.

## Shared status primitive (note for later)

As in the jobs-list spec: jobs (`JobStatus`), workers (`WorkerStatus`), and now schedules
(a boolean enabled/paused) each maintain their own status/indicator vocabulary, and the holo
`StatusDot` is hard-wired to the worker vocabulary. A **generic status-dot primitive** -
taking a resolved `{ label, dotClass, textClass }` view (or a small `tone` union) rather than
a domain enum - would let all three share it. Schedules is now the **third** status-dot
consumer, which is the trigger the jobs-list spec named for capturing this as a backlog idea.
**Recommend filing that backlog idea** (generic `StatusDot`/tone-based indicator) as part of
triaging this work. Not done in this pass (still trivial inline dots), and not a blocker for
this relayout.

## Test impact

Vitest (`cd web && npm test`). No Go tests affected. Behavior assertions are preserved; only
structural/class-based assertions change.

- **`SchedulesTable.test.tsx`** - should survive unchanged: it queries by visible text
  (`nightly-build`, `0 2 * * *`, `dev@studio.com`, `abcdef12` short last_job_id), by the
  action buttons' roles/names (`Run now`, `Enable`, `Disable`), by the pending-disabled
  state, and by the absence of a short id when `last_job_id` is undefined. All of that markup
  is preserved (name stays plain text, LAST JOB stays plain `shortId`, both actions on every
  row). Only update needed if any assertion keys on the old container classes (none do).
- **`SchedulesPage.test.tsx`** - behavior assertions preserved:
  - Summary strip (`2 schedules`) - preserved (page-scoped inline strip retained).
  - Empty state (`No schedules yet.`) and error state (`Retry`) - preserved (restyled).
  - Sort re-request (`selectOptions(getByLabelText('Sort'), 'name')`) - preserved (native
    `<select aria-label="Sort">` unchanged).
  - Pagination: cursor walk, `1-50 of 120`, partial-page absolute range (`51-63 of 63`),
    back-nav range restore, in-flight prev/next disabling
    (`getByRole('button', { name: /prev|next/i })`) - preserved (logic unchanged; footer
    moving inside the `GlassPanel` does not affect text/role queries).
  - `Disable` PATCH (`getAllByRole('button', { name: 'Disable' })[0]` -> `{ enabled: false }`)
    - preserved.
  - The added `· SORT <sort>` footer segment renders alongside the existing `1-50 of 120`
    text; existing range assertions use substring matchers (`/1-50 of 120/i`) so they still
    pass. Re-check after implementation; adjust only if a structural query breaks.
- **New tests:** none required (no new component; primitives are already tested). An optional
  light guard ("no filter chips / search input / Edit / FAILED·24H stat rendered") may be
  added to lock the omissions, but it is optional.

## Open decisions

1. **Summary strip form and scope.** Recommendation (baked in): keep the compact inline strip
   in its page-scoped ENABLED/PAUSED form to match the actual `HoloSchedules` mock; do **not**
   convert to `KpiStat` cards, and do **not** add the FAILED·24H stat (no backing aggregate).
   Confirm, or opt into cards for cross-page consistency.
2. **Table container primitive.** Recommendation (baked in): `GlassPanel` + explicit grid
   header/footer rows, not `Panel` (single-cell header fights the 9-column grid). Same as
   jobs-list. Confirm.
3. **Sort control fidelity.** Recommendation (baked in): keep the native inline
   `<select aria-label="Sort">` for accessibility and test stability; defer the hi-fi custom
   dropdown as a later cross-page polish item. Confirm.
4. **LAST JOB as a job link.** Recommendation (baked in): keep LAST JOB as plain `shortId`
   text this pass (pure restyle). A job-detail route *does* exist (`/jobs/:id`), so wiring the
   last-job cell to it is feasible and would match the hi-fi's `onOpenJob` - but it is a
   behavior change, so surface it as a small follow-up rather than smuggling it into a
   restyle. Confirm the defer, or opt to wire the link now.
5. **Run-now attribution footer note.** Recommendation (baked in): do not add the hi-fi note
   this pass; if added later, correct the copy to "owner or admin" (run-now is not admin-only
   per `ownedScheduledJob`). Confirm.
6. **Prev/next + action buttons as `PillButton`.** Recommendation (baked in): keep plain
   `<button>`s with the existing compact pill classes (the hi-fi footer/action buttons are
   the compact size `PillButton`'s ghost variant does not match). Same as jobs-list. Confirm.
7. **Generic status-dot primitive.** Schedules is the third status-dot consumer - the trigger
   the jobs-list spec named. Recommendation (baked in): **file a backlog idea** for a generic
   tone-based `StatusDot` now, but do not extract it in this relayout (inline dots stay).
   Confirm the file-backlog-now approach.
