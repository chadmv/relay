# Jobs Lanes (swimlanes-by-status) view - design

Date: 2026-09-01
Backlog item: `docs/backlog/idea-2026-06-05-jobs-lanes-swimlanes-view.md`
Lane: F of the six-lane web SPA batch
Author: relay-tpm (autonomous run; see Decisions for every question answered in place of a human)

## Summary

Add a second view to the Jobs page: five vertical lanes, one per job status, each fed by its own
capped `GET /v1/jobs?status=<s>&limit=10` call. A lane shows up to ten job cards plus its
server-reported total for that status, and a "+ N more" control when the total exceeds what is
shown. The view is chosen by a Table / Lanes switch in the page header, persisted to
`localStorage` exactly as the Workers page persists its Grid / Table choice. Nothing about the
table view's data flow, sorting, filtering or pagination changes. No backend change is required
and none is proposed.

The whole feature is additive and frontend-only: one new hook, one new view component, one new
config module, a view switch and one guarded branch in `JobsPage.tsx`.

## Context at HEAD (by symbol)

Everything below was checked against this worktree, not taken from the 2026-06-05 item.

**The server contract.**

- `handleListJobs` (`internal/api/jobs.go:412`) has three branches. Branch 2
  (`internal/api/jobs.go:459-480`) reads `r.URL.Query().Get("status")`, so it takes **exactly one**
  status value; a second `status=` parameter is silently ignored. A merged lane
  ("failed + timed_out") is therefore two requests, not one, whatever the handoff says.
- The status branch calls `ListJobsByStatusWithEmailPage` and `CountJobsByStatus`
  (`internal/store/query/jobs.sql:37-58`). The list orders `j.created_at DESC, j.id DESC` and takes
  no sort argument. `CountJobsByStatus` is `SELECT COUNT(*) FROM jobs WHERE status = $1`: an
  **all-time** count for that status, not a windowed one.
- `?sort=` together with `?status=` is a hard 400 (`internal/api/jobs.go:420-425`,
  "sort not supported on filtered list variant"). Lane requests must never send `sort`.
  `web/src/jobs/api.ts:50-56` (`listJobs`) already encodes that mutual exclusion.
- `?limit=` is validated in `parsePage` (`internal/api/pagination.go:240-248`): default 50, and a
  value outside `[1, 200]` is **rejected with 400 "invalid limit"**. It is not clamped. A per-lane
  cap of 10 is well inside the range; any future stepper maximum must stay at or below 200 or the
  request fails rather than degrading.
- The response envelope is `page[jobResponse]{items, next_cursor, total}`
  (`internal/api/jobs.go:478`), so a lane's overflow count is available from the same response that
  supplies its rows.
- Job status vocabulary is exactly `pending, running, done, failed, cancelled`
  (`internal/store/migrations/000019_status_vocabulary_checks.up.sql:14`, pinned by
  `TestJobsStatusVocabularyIsExactly` in `internal/store/jobs_status_vocabulary_lockstep_test.go:63`).
  There is no `queued`, no `dispatched` and no `timed_out` job status; those three belong to the
  task vocabulary (same migration, line 23).
- `idx_jobs_status_created_id ON jobs(status, created_at DESC, id DESC)`
  (`internal/store/migrations/000011_pagination_indexes.up.sql:7`) serves both the per-lane list and
  the per-lane count. The count is index-servable but still proportional to the number of rows in
  that status.
- No ownership predicate exists on the status branch, so any authenticated user already sees
  fleet-wide job rows and a fleet-wide per-status total through the existing table view's chips.
  Lanes discloses nothing the table view does not.

**The frontend at HEAD.**

- `JobsPage.tsx` holds `sort`, `filter` and `useCursorPager()` in component state. `FILTERS`
  (`web/src/jobs/JobsPage.tsx:13-19`) maps five chip keys to status strings, with `queued -> pending`
  and `all -> ''`. `pickFilter` resets paging and snaps sort back to `-created_at`.
- `useJobs(sort, status, cursor, intervalMs = 3000)` (`web/src/jobs/useJobs.ts`) is a single
  `useQuery` keyed `['jobs', sort, status, cursor]` with `refetchInterval` 3000 and
  `placeholderData: keepPreviousData`. `useJobStats` is keyed `['job-stats']`, deliberately not
  under the `'jobs'` prefix; `web/src/jobs/queryKeyDecoupling.test.tsx` pins that separation.
- `useJobStats` returns `{running, queued, done_24h, failed_24h}` only. **Lane counts cannot come
  from it**: it has no `cancelled` bucket and two of its four numbers are 24-hour-scoped while a
  lane's rows are all-time newest-first. Using it would put a number over a list it does not
  describe. Lane counts come from each lane's own `total`.
- `status.ts` exports `statusColor(status: JobStatus)`, `progressPct`, `formatDuration`,
  `formatStarted`. `statusColor` already covers the real five-value vocabulary with a `default` arm
  for `cancelled`.
- `JobStatus` in `web/src/jobs/api.ts:3` is a hand-written union of the five real statuses. It is
  correct today and there is no runtime list beside it.
- `web/src/components/holo` exports `GlassPanel`, `Eyebrow`, `ProgressBar`, `Chip`, `PillButton`,
  `KpiStat`, `Panel`, `StatusDot`, `Table`. `StatusDot` is **worker**-status-shaped: it takes a
  `WorkerStatus` and routes through `livenessView`, so it is not reusable for job statuses.
  `JobsTable.tsx` renders its own dot from `statusColor`; lanes will do the same.
- `WorkersPage.tsx:14-21,47-50` is the repo's shipped view-switch pattern: a `View` union, a
  `loadView()` reading `localStorage` with a safe default, a `chooseView` that sets state and
  writes the key, and a pill group of `aria-pressed` buttons. Key name `relay.workers.view`.
- **The SPA has no URL search-param state at all.** `useSearchParams`, `createSearchParams` and
  `location.search` have zero occurrences under `web/src`. The table view's status filter is
  component-local `useState`, so there is no `/jobs?status=done` route for anything to link to.
- `web/e2e/layout.spec.ts` measures document, header and main scroll widths at 320, 375 and 1280
  across the surfaces in `web/e2e/surfaces.ts`. `web/e2e/README.md:100-109` records the gate's
  limit: content that overflows into its own `overflow-x-auto` wrapper reads as zero document
  overflow and passes.
- `components/holo/Table.tsx:176-193` is the precedent for a horizontal scroll container:
  `overflow-x-auto` plus `tabIndex={0} role="group" aria-label="<label>, scrolls horizontally"`,
  added because a scroll region with no focusable descendants is an axe
  `scrollable-region-focusable` violation and because WebKit does not grant implicit scroller
  focusability.
- `web/CLAUDE.md`: Tailwind v4 scans every file under `web/` including comments and tests, so any
  class-shaped literal must live in the component that means it. This spec lives under `docs/` and
  is outside the scanner's root.

**What I refuted.**

1. *"Dispatched merges with running; failed merges with timed_out"*
   (`design_handoff_relay_holo/reference/screens/jobs-list.js:296`, and the status set listed at
   its line 2). Those are task statuses. No job row can hold them
   (migration `000019`, line 14), so there is nothing to merge, and `?status=` takes one value
   anyway. Dropped.
2. *The "Done - 24h / 487" lane header* (`hifi3-holo-pages.jsx:690`). The only per-status count the
   API offers is `CountJobsByStatus`, which is all-time. There is no `?since=`. The lane is labelled
   "Done" and its number is the all-time total. This deliberately differs from the KPI strip's
   `DONE-24H` directly above it, which is why the lane's number is rendered with the word "total"
   next to it rather than bare.
3. *"clamp at 200 server-side"* (`reference/screens/jobs-list.js:282`). `parsePage` **rejects** an
   out-of-range limit with 400; it does not clamp. Harmless at cap 10, load-bearing for any future
   stepper.
4. *"'+N more' links to the table filtered by that status"* (the backlog item's own wording). There
   is no such link target: the SPA has no query-param state and the table's filter is local
   component state. Designed against what exists - see Decision 5. This is the batch rule applied
   in its "omit what the backend cannot supply" spirit, one layer up: omit the affordance the
   *frontend* cannot supply, and replace it with the one it can.
5. *"the table view's test files should have a zero-line diff"* (the lane brief's own gate).
   Refuted for exactly one file: `web/src/jobs/JobsPage.test.tsx:46-55` is a test named
   "does not render the backend-blocked view-switch, My-jobs, or search controls", and it asserts
   `queryByRole('button', { name: /lanes/i })` is null. That assertion pins the absence of this
   feature; it must go RED and be narrowed. Decision 8 restates the gate in a form that is true.
6. *The `?view=lanes&per_lane=10` URL in the reference mock* (`jobs-list.js:168`) is decorative
   chrome. No SPA surface reads a search param today.

Checked and found nothing: there is no existing lanes/kanban component anywhere under `web/src`, no
`?status=` handling in the router, no `since`/`window` parameter on `/v1/jobs`, and no invariant in
`CLAUDE.md` that this frontend-only slice touches. The one invariant with a frontend form - end the
generation before releasing the resource, whose frontend instance is `useTaskLogStream`'s
`AbortController` - has no analogue here: this feature creates no `AbortController`, no event source
and no subscription. TanStack Query owns every fetch lifecycle in it.

## Decisions

Autonomous run: no human was available, so each question below was decided here. The question, the
options, the choice and the reason are recorded so the human can overturn any of them cheaply.

**1. Which statuses become lanes, in what order, and where does the vocabulary come from?**

- Options: (a) the four lanes the hi-fi draws (pending, running, done, failed); (b) all five real
  job statuses; (c) a lane per KPI-strip bucket.
- Chosen: **(b), all five, in the order Queued, Running, Done, Failed, Cancelled.**
- Why: a status with no lane is invisible in this view, and "cancelled" is the state an operator
  looks for right after cancelling something. The lifecycle order matches the hi-fi for its first
  four; `cancelled` goes last as the least-trafficked terminal state. (c) is refuted above: the KPI
  buckets are 24-hour-scoped and have no `cancelled`.
- Vocabulary source: `web/src/jobs/api.ts` gains
  `export const JOB_STATUSES = ['pending','running','done','failed','cancelled'] as const`, and
  `JobStatus` becomes `(typeof JOB_STATUSES)[number]`. The type and the runtime list then cannot
  drift from each other. The lane order and labels live in `web/src/jobs/lanes.ts` as a
  `Record<JobStatus, string>` label map, so **adding a status to the tuple without giving it a lane
  label is a tsc error**, not a silent omission. What this does NOT do is pin the tuple against the
  server: nothing in CI compares it to migration `000019`. That gap is stated in the module comment
  and filed as a follow-up rather than claimed away.
- Label wording: the pending lane is labelled **"Queued"**, matching the existing table chip
  (`JobsPage.tsx:16`), not the hi-fi's "Pending / Queued". One page should not call one state two
  things.

**2. Is the per-lane cap user-adjustable in this slice?**

- Options: (a) fixed at 10; (b) the hi-fi's "CARDS / LANE" stepper with min 3 / max 50, persisted to
  `localStorage`; (c) fixed, but read from an existing preference.
- Chosen: **(a), fixed at 10**, exported as `LANE_LIMIT` from `web/src/jobs/lanes.ts`.
- Why: the stepper is inert in the hi-fi too (`hifi3-holo-pages.jsx:554-562` renders the buttons with
  no handler), so nothing is lost against the reference. The cost of (b) is not the widget, it is
  that a cap of 50 across five lanes makes this view fetch 250 enriched job rows every three seconds
  per open tab, five times the table view's page, and that is a load argument the spec would then
  have to win. The hook already takes the limit as a parameter, so (b) is a later slice that changes
  no fetch shape. Filed as a follow-up.
- No new `localStorage` key is introduced for the cap. The only key this slice adds is the view
  preference (Decision 3).

**3. Where does the Table / Lanes choice live?**

- Options: (a) URL search param; (b) component `useState` only; (c) `useState` seeded from and
  written to `localStorage`.
- Chosen: **(c)**, key `relay.jobs.view`, values `table` (default) and `lanes`, read through a
  `loadView()` that returns `table` for anything other than the literal `lanes`.
- Why: (a) has no precedent anywhere in this SPA (zero `useSearchParams` occurrences) and would make
  this slice the first to introduce URL state, which is its own design decision and its own set of
  interactions with the pager and the sort control. (b) loses the preference on every navigation,
  which for a view switch is the whole point of having one. (c) is the shipped pattern
  (`WorkersPage.tsx:17-21,47-50`) down to the key naming and the fail-safe default, so a reader of
  one understands the other.
- Coexistence with sort and pager: the chip row, the `SortControl` and the pagination footer are
  **table-view furniture** and render only in the table branch, exactly as the hi-fi does
  (`hifi3-holo-pages.jsx:545,592` both gate on `view === 'table'`). The lanes branch returns before
  them. No sort or pager state is read, written or reset by anything in the lanes view.

**4. What does the lane count show, given the KPI strip is right above it?**

- Options: (a) the lane's own `total` from its response; (b) the matching `useJobStats` bucket;
  (c) the number of cards shown.
- Chosen: **(a)**, rendered as the number followed by the word "total".
- Why: (b) does not exist for `cancelled` and is 24-hour-scoped for `done` and `failed`, so it would
  caption an all-time list with a windowed number. (c) is always at most 10 and says nothing. (a) is
  the same value the overflow control is computed from, so the header and the overflow can never
  disagree. The word "total" is there because `DONE-24H` sits a few pixels above and the two numbers
  are legitimately different.

**5. What is the "+ N more" control, and where does it go?**

- Options: (a) a `<Link>` to a status-filtered table URL; (b) a button that switches to the table
  view and selects that status's chip; (c) omit the control and file an enabler.
- Chosen: **(b)**.
- Why: (a) does not exist to be built - see refutation 4 - and inventing URL state for it is
  Decision 3 all over again, in a slice that should not own that call. (c) leaves a user who can see
  "487 done jobs" with no route to them from this view, when the page one click away can already
  show them. (b) uses machinery `JobsPage` already has: `pickFilter(chipKey)` plus
  `chooseView('table')`.
- `N = lane.total - lane.items.length`, computed per lane from one response, and the control renders
  only when `N > 0`.
- **This requires a sixth chip.** `FILTERS` has no `cancelled` entry, and routing an unknown key
  into `filter` would make `FILTERS.find(...)` return undefined, `status` fall back to `''`, and the
  table silently show *all* jobs while looking filtered - a wrong-answer failure, not a missing one.
  So `FILTERS` gains `{ key: 'cancelled', label: 'Cancelled', status: 'cancelled' }`. This is
  additive: no existing chip, handler or test changes, and `?status=cancelled` is a real server
  query (`cancelled` is in the check constraint). It is the enabler the overflow control needs, and
  it is one line rather than a filed follow-up that leaves a lane's overflow dead in the meantime.
  The alternative considered and rejected was five lanes where one lane's overflow is silently
  inert. The added chip gets its own acceptance criterion (AC-15) rather than riding along
  untested.

**6. Fetch strategy and cadence.**

- Options: (a) five `useQuery` calls in five child components; (b) one `useQueries` in a
  `useJobLanes` hook; (c) one combined request.
- Chosen: **(b)**. (c) does not exist on the server. (a) works but scatters the cadence and the
  query-key convention across five call sites.
- Cadence **3000 ms, matching `useJobs`**, so the header's "live - auto-refreshing" claim stays true
  in both views.
- Query keys are `['job-lanes', status, limit]`. Deliberately **not** under the `'jobs'` prefix: a
  broad `invalidateQueries(['jobs'])` should not fan out into five more requests, which is the same
  argument `queryKeyDecoupling.test.tsx` already makes for `['job-stats']`.
- Each lane query carries `placeholderData: keepPreviousData`, matching `useJobs`, so a lane does
  not flash empty between polls.
- **The table query is disabled while the lanes view is active.** `useJobs` gains a fifth optional
  parameter `enabled = true`, and `JobsPage` passes `view === 'table'`. Without this the page polls
  six endpoints in lanes view, one of which is a 50-row enriched page nobody is looking at. The
  default keeps every existing call site and `useJobs.test.tsx` unchanged. Per the
  added-a-property-forgot-its-guard rule, the new capability gets its own test with itself as the
  subject (AC-9), not a passing mention in an existing one.

**7. Failure isolation.**

- Chosen: a lane owns its loading, empty and error states. A failed lane renders its own message and
  its own Retry, which calls that query's `refetch`; the other four keep their data and keep
  polling. `JobsLanes` has no page-level early return on error, and the existing page-level error
  panel and loading skeleton in `JobsPage` (`JobsPage.tsx:44-63`) belong to the table query and are
  not reached in lanes view, because the lanes branch returns before them.
- Why: the alternative (one page-level error) turns a single lane's 500 into a blank page, which is
  strictly worse than the table view it replaces.

**8. What "do not disturb the table view" means as a checkable gate.**

Restated so it is true and checkable, since the brief's original form is refuted above:

- `web/src/jobs/JobsTable.test.tsx`, `web/src/jobs/JobsPage.pager.test.tsx`,
  `web/src/jobs/useJobs.test.tsx`, `web/src/jobs/status.test.ts` and
  `web/src/jobs/queryKeyDecoupling.test.tsx`: **zero-line diff**.
- `web/src/jobs/JobsPage.test.tsx`: exactly one existing test changes - the `/lanes/i` clause is
  removed from the absence assertions at lines 51-54, and the `/timeline/i`, `/my jobs/i` and
  `searchbox` clauses stay. No other line of any existing test in that file changes. New tests are
  added in `web/src/jobs/JobsPage.lanes.test.tsx`, a new sibling file, following the precedent set
  by `JobsPage.pager.test.tsx`.
- `web/src/jobs/JobsPage.tsx`: every edit lands **above** the `<JobsTable ...>` element. The
  `<JobsTable>` element and the entire `footer` prop it carries (lines 132-160 at HEAD) are
  byte-identical after this slice. This is the merge contract with lane B - see Risks.

## Design

### Component tree

```
JobsPage                              (existing; gains `view` state and one guarded branch)
 |- pageHeader                        (existing markup, extracted to a const; gains the view switch)
 |   |- Eyebrow / h1 / KPI strip / live indicator / "+ New job"   (unchanged)
 |   \- ViewSwitch                    (new: two aria-pressed buttons, Table | Lanes)
 |- view === 'lanes':
 |   \- JobsLanes                     (new)
 |       \- JobLane x5                (new; one <section> per status)
 |           |- lane header           (dot, label, "<total> total")
 |           |- ul > li > JobLaneCard (new; a Link to /jobs/:id)
 |           \- overflow button       ("+ N more", when N > 0)
 \- view === 'table':                 (existing, untouched)
     |- chip row + SortControl
     \- JobsTable + pagination footer
```

New files:

- `web/src/jobs/lanes.ts` - `LANE_LIMIT = 10`, `LANE_ORDER: JobStatus[]`, `LANE_LABELS:
  Record<JobStatus, string>`, and `laneChipKey: Record<JobStatus, string>` mapping a lane's status to
  the table chip key the overflow control selects (`pending -> 'queued'`, the rest identity). Pure
  data, no React, so its vocabulary guard is a plain vitest.
- `web/src/jobs/useJobLanes.ts` - the `useQueries` hook.
- `web/src/jobs/JobsLanes.tsx` - the view, including `JobLane` and the card. One file: the three
  pieces are about 120 lines together and are never used apart.
- Tests: `lanes.test.ts`, `useJobLanes.test.tsx`, `JobsLanes.test.tsx`, `JobsPage.lanes.test.tsx`.

Changed files: `web/src/jobs/api.ts` (the `JOB_STATUSES` tuple, a `listJobsByStatus` helper),
`web/src/jobs/useJobs.ts` (the `enabled` parameter), `web/src/jobs/JobsPage.tsx` (view state, the
switch, the lanes branch, one `FILTERS` entry, one `useJobs` argument).

### Data hooks

`api.ts` gains:

```
export function listJobsByStatus(status: JobStatus, limit: number): Promise<JobsPage>
```

It sends `status` and `limit` and **never** `sort` or `cursor`. It is deliberately a separate
function from `listJobs`, for the same reason `listJobsBySchedule` is: `listJobs` sets `sort` on its
unfiltered branch, and the server 400s sort plus status. Do not unify them.

`useJobLanes.ts`:

```
export function useJobLanes(enabled: boolean, limit = LANE_LIMIT, intervalMs = 3000)
```

One `useQueries` over `LANE_ORDER`, each entry keyed `['job-lanes', status, limit]`, with
`queryFn: () => listJobsByStatus(status, limit)`, `refetchInterval: intervalMs`,
`placeholderData: keepPreviousData` and `enabled`. Returns the raw results array; `JobsLanes` zips it
with `LANE_ORDER` by index.

### States, per lane

| State | Condition | Render |
| --- | --- | --- |
| Loading | `isLoading && !data` | three skeleton `GlassPanel` blocks inside the lane |
| Error | `error && !data` | the error message plus a Retry button calling that lane's `refetch` |
| Empty | `data.items.length === 0` | muted "No jobs" text; the header still shows "0 total" |
| Populated | otherwise | up to `LANE_LIMIT` cards, then the overflow control when `N > 0` |

A lane in any of the first three states leaves the other four untouched. The lane header (dot, label,
count) renders in every state, so the lane never disappears from the layout and the column count is
constant.

### The view switch

```
type View = 'table' | 'lanes'
const VIEW_KEY = 'relay.jobs.view'
function loadView(): View { return localStorage.getItem(VIEW_KEY) === 'lanes' ? 'lanes' : 'table' }
```

Two buttons in a pill group with `aria-pressed`, placed in the header's right-hand cluster next to
the live indicator, mirroring `WorkersPage.tsx:201-213`. `chooseView` sets state and writes the key.
Table stays the default for a first-time user, matching the design's marked default and the
2026-06-05 decision that shipped it.

The lanes branch sits immediately after the hooks and before the existing loading and error early
returns, so those two blocks need no edit at all:

```
if (view === 'lanes') {
  return (<div className="flex flex-col gap-4">{pageHeader}<JobsLanes onShowAll={showAll} /></div>)
}
```

where `showAll(status)` calls `pickFilter(laneChipKey[status])` and `chooseView('table')`.
`pickFilter` already resets the pager and snaps sort to the default, which is exactly what a fresh
filtered table needs.

### Keyboard and ARIA

- The lanes container is a single horizontal scroll region: `overflow-x-auto`, plus `tabIndex={0}`,
  `role="group"` and `aria-label="Job lanes, scrolls horizontally"`, following
  `components/holo/Table.tsx:189-193`. The tab stop is not redundant in every state: with all five
  lanes empty there is no focusable descendant at all, and the container can still overflow, which
  is the axe `scrollable-region-focusable` case that precedent exists for.
- Each lane is a `<section aria-labelledby={id}>` containing an `<h2 id={id}>` whose text is the lane
  label. That gives one named region and one heading per lane, so a screen-reader user can jump lane
  to lane, and it puts the status in **text**, not only in the dot's colour.
- The cards are a `<ul>` of `<li>`, so the lane announces its item count.
- Each card is a `<Link to={/jobs/:id}>` whose accessible name is the job name. Progress is exposed
  as text ("48/64 tasks, 75%") beside the bar rather than being carried by the bar's width alone.
- Keyboard order is document order: lane 1's cards then its overflow control, then lane 2, and so
  on. No `tabindex` above 0 anywhere.
- The overflow control is a `<button>`, not an anchor. It changes the page's view; it has no URL.

### Layout

- Row: a flex row inside the scroll container, with a small gap, and `min-w-0` on the container's
  parent so the scroller can actually shrink instead of widening the document.
- Lane: fixed width (about 280 CSS pixels) and no flex growth or shrink, so five lanes are about
  1450 pixels wide and the row scrolls horizontally inside its own container at every viewport this
  app is tested at. The exact Tailwind literal lives in `JobsLanes.tsx`; it must be a literal there
  for Tailwind v4's static scan, and it must not be written as a class-shaped string anywhere else
  under `web/`.
- Lane body: capped height with `overflow-y-auto`, so a ten-card lane scrolls within the lane rather
  than stretching the page. This is the hi-fi's own structure (`hifi3-holo-pages.jsx:718`).
- **Narrow viewports behave identically.** No breakpoint, no stacking: at 320 and 375 the lanes are
  the same width and the same row scrolls. Stacking five lanes vertically would nest a vertical
  scroller inside a vertical scroller five times, and it would mean the widths measured by
  `layout.spec.ts` at 320/375 are not the widths shipped at 1280. One layout, one thing to reason
  about.
- The gate this must satisfy is `layout.spec.ts`: `documentElement.scrollWidth <= clientWidth` and
  the same for `<header>` and `<main>` at 320, 375 and 1280.

### Load, failure and threat

- **Load.** Lanes issues five list queries plus five `COUNT(*)` per poll, against the table view's
  one list plus one count. Row volume is unchanged (5 lanes times 10 equals one 50-row table page),
  and the enrichment `LATERAL` runs per returned row, so the row-shaped cost is flat. The count is
  the part that scales with the table: `CountJobsByStatus` is index-servable on
  `idx_jobs_status_created_id` but still proportional to the rows in that status, and `done` is the
  status that grows without bound. This is the same shape as the open
  `bug-2026-06-05-index-jobstatuscounts-full-table-scan`; a follow-up is proposed rather than
  blocking this slice, because the count is what "+ N more" is built from and the poll cadence and
  the fixed cap are what bound it.
- Disabling the table query in lanes view (Decision 6) keeps the concurrent-request count at six
  (five lanes plus stats), which also keeps it at the HTTP/1.1 per-host connection limit rather than
  over it.
- **Failure.** A lane's 500 is contained to that lane (Decision 7). A total server outage shows five
  identical lane errors, each with its own Retry, and the page header still renders.
- **Threat model.** No new endpoint, no new parameter values that were not already reachable from the
  table view's chips, and `status` is always one of five module constants, never user input. The
  per-lane total exposes the same fleet-wide count the table footer already shows for the same
  filter. No token, id or email is rendered anywhere it was not already rendered by `JobsTable`.
  Nothing here writes.
- **Invariants.** Checked all seven. Six are backend-shaped and untouched. The seventh in its
  frontend form (end the generation before releasing the resource) has no subject here: this slice
  creates no abort, close, cancel or unregister call.

## Acceptance criteria

Each maps to a named vitest test. AC-11 is the only Playwright item.

| # | Criterion | Test |
| --- | --- | --- |
| AC-1 | The lane set is exactly the five real job statuses, in the order Queued, Running, Done, Failed, Cancelled | `lanes.test.ts` - "lane order is exactly the job status vocabulary" |
| AC-2 | Every status in `JOB_STATUSES` has a lane label; adding one without a label fails tsc | `lanes.test.ts` - "every job status has a lane label" (plus the `Record<JobStatus, string>` type) |
| AC-3 | Each lane issues one request carrying its own `status` and `limit=10`, and no `sort` and no `cursor` | `useJobLanes.test.tsx` - "each lane requests its own status at the cap and never sends sort" |
| AC-4 | Lane queries poll on the shared interval and are keyed outside the `'jobs'` prefix, so `invalidateQueries(['jobs'])` does not refetch them | `useJobLanes.test.tsx` - "invalidating the jobs list does not refetch the lanes" |
| AC-5 | One lane failing renders that lane's error and Retry while the other four still show their rows | `JobsLanes.test.tsx` - "a failing lane does not blank the other lanes" |
| AC-6 | An empty lane renders its header and a no-jobs message, not a skeleton and not an error | `JobsLanes.test.tsx` - "an empty lane keeps its header and shows no jobs" |
| AC-7 | A lane whose total exceeds the cards shown renders "+ N more" with N equal to total minus shown; a lane whose total equals the cards shown renders no such control | `JobsLanes.test.tsx` - "overflow shows total minus shown, and is absent when nothing is hidden" |
| AC-8 | The view switch renders, persists the choice to `relay.jobs.view`, and a remount restores it | `JobsPage.lanes.test.tsx` - "the view switch persists the choice to localStorage" |
| AC-9 | In lanes view no unfiltered `GET /v1/jobs` is issued (the table query is disabled) | `JobsPage.lanes.test.tsx` - "the lanes view issues no unfiltered jobs request" |
| AC-10 | Clicking a lane's "+ N more" switches to the table view with that status's chip selected, and the next request carries that status and no cursor | `JobsPage.lanes.test.tsx` - "overflow routes to the table filtered by that lane's status" |
| AC-11 | The lanes view does not overflow the document, `<header>` or `<main>` at 320, 375 or 1280 | `web/e2e/layout.spec.ts` via a new `jobs-lanes` entry in `surfaces.ts` |
| AC-12 | Each lane is a labelled region with a heading, and each card is a link to its job detail page | `JobsLanes.test.tsx` - "each lane is a labelled region and each card links to its job" |
| AC-13 | The chip row, sort control and pagination footer do not render in lanes view | `JobsPage.lanes.test.tsx` - "table-view controls are absent in lanes view" |
| AC-14 | The table view's existing behaviour is unchanged: same requests, same sort and pager semantics, same chips plus one | The zero-line-diff list in Decision 8, plus the whole existing `web/src/jobs` suite green |
| AC-15 | The new Cancelled chip requests `status=cancelled` and sends no `sort`, like every other chip | `JobsPage.lanes.test.tsx` - "the Cancelled chip requests status=cancelled" |

**Should the Playwright surface list gain the view? Yes.** jsdom performs no layout, so every one of
AC-1 through AC-10 is silent about the thing most likely to break here: this is the first
horizontally-scrolling multi-column layout in the app, and `layout.spec.ts` is the only place in the
repo where a width is a real number. The new surface:

```
name: 'jobs-lanes', path: () => '/jobs', population: 'populated',
prepare: (p) => p.addInitScript(() => window.localStorage.setItem('relay.jobs.view', 'lanes')),
ready: gate on the region named Queued containing the link named seed.jobName
```

`prepare` is justified under `surfaces.ts`'s own rule: it fabricates no data, it sets the same
preference key the shipped UI writes, and it must run before the SPA's first render, which is what
`addInitScript` is for. The `ready` gate is scoped to the Queued lane rather than to the bare link,
so a run where the seeded job is not where this surface assumes fails loudly instead of measuring an
empty lanes view under a populated name. That assumption is sound at HEAD for the stated reason
recorded in `web/e2e/README.md:77-81`: no `relay-agent` runs in slice 1, so a seeded job never leaves
`pending`.

**State the limit honestly in the surface's comment**: per `web/e2e/README.md:100-109`, a
`scrollWidth <= clientWidth` gate cannot tell "fits" from "clipped behind a scroller", and this view
is deliberately a scroller. The surface pins that lanes do not widen the document. It does not pin
that the lanes are readable, and no automated check in this repo can. The screenshots at three
widths are the artifact for that, and someone has to open them.

**No `keyboard.spec.ts` case is added**, deliberately. Its two existing cases exist because
`EnrollmentsTable` and `InvitesTable` have zero focusable elements in any row, so their clipped
columns are reachable only through the wrapper's own tab stop. A populated lanes view is the
opposite: every card is a link, so the clipped columns are reachable by ordinary tabbing. The
wrapper's `tabIndex` is there for the all-empty state, which the harness does not seed - and that gap
is named in the follow-ups rather than papered over with a case that would pass for the wrong reason.

## Risks, including the merge with lane B

Lane B is concurrently changing `useCursorPager.next` to take the page object instead of
`(cursor, rows.length)`, which edits one line inside `JobsPage.tsx`'s pagination footer.

- **This design must not depend on the pager's signature, and does not.** The lanes view does its own
  capped per-status fetches and never constructs, reads or resets a cursor. `useCursorPager` is not
  imported by any new file.
- **The merge contract** is Decision 8's third bullet: every `JobsPage.tsx` edit lands above the
  `<JobsTable ...>` element, and the element plus its `footer` prop (lines 132-160 at HEAD) come out
  byte-identical. Lane B's edit is inside that region; this lane's edits are the `FILTERS` array
  (line 13-19), the `useJobs` call (line 30), a header extraction (lines 74-97) and a new branch
  after the hooks. Those are separate hunks with several lines of untouched context between them, so
  git merges them without conflict.
- **What would break the merge** is extracting the table branch into a new component file, or
  re-indenting the existing return block. Both would turn lane B's one-line edit into a
  delete-plus-add conflict. Neither is in this design, and an implementer who finds themselves
  reaching for either should stop and raise it.
- Residual risk: if lane B also touches `useJobs`'s signature (it is not expected to; its subject is
  `useCursorPager`), the added `enabled` parameter would conflict. Cheap to resolve, and the
  parameter is optional and additive by construction.

## Out of scope

- The Timeline view (`docs/backlog/idea-2026-06-05-jobs-timeline-view.md`). The view switch has two
  options, not three, and `JobsPage.test.tsx`'s `/timeline/i` absence assertion stays green.
- The "My jobs" toggle and the free-text search box, still backend-blocked
  (`idea-2026-06-05-my-jobs-toggle-mine-filter`, `idea-2026-06-05-job-search-box-q-filter`).
- The cards-per-lane stepper (Decision 2).
- Any change to `/v1/jobs`, to the store queries, or to any Go file. This slice is frontend-only.
- Drag-and-drop between lanes. Nothing in this design implies a job's status is settable by moving a
  card, and no endpoint would accept it.
- Sorting within a lane. The status branch takes no `sort`; lane order is the server's
  `created_at DESC, id DESC`.
- URL-addressable view or filter state.

## Backlog items this closes

- `docs/backlog/idea-2026-06-05-jobs-lanes-swimlanes-view.md`. Closing it is required scope for the
  implementing work, via `/backlog close jobs-lanes`, which does the `git mv` into
  `docs/backlog/closed/`. Note when closing that the item's per-lane stepper (default 10, min 3, max
  50) is deliberately deferred, and cite the follow-up item that carries it, so the close does not
  read as "shipped everything the item described".

## Proposed follow-up backlog items

Proposals only. Per the backlog rule, none of these is filed here; the human accepts or drops each.

1. **Cards-per-lane stepper.** The hi-fi's "CARDS / LANE" control: default 10, minimum 3, maximum 50,
   persisted per user. Must note that the server rejects, rather than clamps, a limit above 200, and
   that the maximum multiplies this view's per-poll row volume by five.
2. **No lockstep guard between the client job-status tuple and migration `000019`.** `JOB_STATUSES`
   in `web/src/jobs/api.ts` is a hand-maintained copy of a constraint that
   `TestJobsStatusVocabularyIsExactly` pins on the Go side. Adding a sixth job status would leave the
   SPA silently dropping it from the lanes view with every test green. The cross-language claim
   should be a guard, not a comment.
3. **Five `COUNT(*)` per poll per open tab in lanes view.** Sibling to
   `bug-2026-06-05-index-jobstatuscounts-full-table-scan`. Worth measuring against a large `jobs`
   table before the stepper raises the cap, and worth considering whether the lane header needs an
   exact count at all.
4. **No way to scope the Done lane to a window.** The hi-fi's "Done - 24h" lane cannot be built:
   `/v1/jobs` has no `?since=`. If the windowed lane is wanted, it needs a server-side filter, and
   that filter would also let the KPI strip and the lane agree.
5. **The all-empty lanes state is not covered by the browser harness.** The e2e seed always creates a
   job, so the layout gate never sees five empty lanes, which is the state the scroll wrapper's
   `tabIndex` exists for.
