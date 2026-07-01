# Job detail page and row-click navigation - design

- Date: 2026-07-01
- Status: proposed
- Author: relay-tpm (autopilot)
- Backlog: `docs/backlog/idea-2026-06-05-job-detail-page-row-click.md`
- Type: frontend-only slice on existing endpoints

## Summary

Build the job detail page at `/jobs/:id` (the Holo `HoloJobDetail` "split pane"
surface) and make `JobsTable` rows navigate to it on click. This is a frontend
slice: every endpoint it consumes already ships. No backend, store, or proto
change is in scope.

The page renders three panes:

1. Header - job identity, status, owner, labels, and a reserved (empty) actions
   slot on the right.
2. Left column - overall progress strip, a task-DAG dependency strip, and the
   tasks table.
3. Right column - a tabbed detail pane with a **Spec** tab (the job/task spec:
   commands, env, requires, source) and a **Log** tab (static historical task
   log via GET).

## Verified facts (source of truth checked against current code)

These were confirmed by reading the code, not the backlog proposal.

### `GET /v1/jobs/{id}` response shape (`internal/api/jobs.go`, `handleGetJob` -> `toJobResponse`)

```jsonc
{
  "id": "uuid",
  "name": "string",
  "priority": "string",
  "status": "pending|running|done|failed|cancelled",  // JOB status vocabulary
  "submitted_by": "uuid",
  "submitted_by_email": "string",   // omitempty
  "labels": { } | null,             // json.RawMessage passthrough
  "tasks": [ /* taskResponse, see below */ ],
  "created_at": "RFC3339",
  "updated_at": "RFC3339"
  // NOTE: total_tasks / done_tasks / started_at / finished_at /
  // scheduled_job_id / scheduled_job_name are LIST-ONLY enrichment fields.
  // handleGetJob does NOT populate them. Do not rely on them here; derive
  // progress from the tasks array instead.
}
```

`taskResponse` (one per task, ordered by `created_at` per `ListTasksByJob`):

```jsonc
{
  "id": "uuid",
  "name": "string",
  "status": "pending|dispatched|running|done|failed|timed_out",  // TASK vocabulary
  "commands": [["p4","sync",".."],["blender",".."]],  // json.RawMessage: [][]string
  "env": { } ,        // json.RawMessage: object
  "requires": { },    // json.RawMessage: object
  "timeout_seconds": 3600 | null,
  "retries": 2,
  "retry_count": 0,
  "depends_on": ["frame-001", "frame-002"],  // omitempty; TASK NAMES, not IDs
  "worker_id": "uuid"                          // omitempty
}
```

Key points that shape the design:

- **Task status vocabulary differs from job status.** Per migration
  `000019_status_vocabulary_checks`, tasks are `pending | dispatched | running |
  done | failed | timed_out`; jobs are `pending | running | done | failed |
  cancelled`. The existing `web/src/jobs/status.ts` `JobStatus` union and
  `statusColor` cover only the job set and do NOT know `dispatched` or
  `timed_out`. This page needs a separate `TaskStatus` type and task color map.
- **On cancel, tasks become `failed`, not `cancelled`** (`CancelJobTasks`
  marks non-terminal tasks `failed`; only the job row goes `cancelled`). So the
  tasks table never shows a `cancelled` task; the job header can.
- **`depends_on` is an array of task NAMES**, resolved server-side from
  dependency UUIDs (`handleGetJob` maps dep UUID -> task name via `uuidToName`).
  The DAG can be built purely from `tasks[].name` + `tasks[].depends_on` with no
  extra fetch. Edge direction: a dependency name in `task.depends_on` is a
  predecessor, so the edge is `dep -> task`.
- **`commands` / `env` / `requires` are opaque JSON** (`json.RawMessage`).
  In TypeScript they are `string[][]`, `Record<string,string>`, and
  `Record<string,string>` respectively. `env`/`requires` are always present
  (may be `{}`); `commands` is always present.
- **No `source` field on `taskResponse`.** The Holo mock shows a Perforce
  source block in the spec tab, but `handleGetJob` does not return per-task
  source. `SourceSpec` exists on input (`taskSpec.Source`) but is not echoed in
  the response. The spec tab therefore renders commands/env/requires only; the
  source block is out of scope (see Open decisions).

### `GET /v1/tasks/{id}/logs` (`internal/api/tasks.go`, `handleGetTaskLogs`)

Confirmed: static, GET-only, seq-paginated. NO SSE, no streaming today.

```jsonc
{
  "items": [
    { "seq": 12, "stream": "stdout|stderr", "content": "line", "created_at": "RFC3339" }
  ],
  "next_seq": 0,   // 0 = drained; else pass as ?since_seq= for the next page
  "total": 340
}
```

Query params: `?limit=` (1..200, default 50), `?since_seq=` (non-negative).
Returns 404 if the task does not exist. The log tab fetches once (optionally
paging forward) - it does NOT poll or tail.

### Routing and existing patterns

- Route table (`internal/api/server.go`): `GET /v1/jobs/{id}`,
  `GET /v1/tasks/{id}/logs` already registered under `auth`.
- SPA router (`web/src/app/router.tsx`): worker detail is wired as
  `<Route path="/workers/:id" element={<WorkerDetailPage />} />` inside
  `<ProtectedRoute>`. We add `<Route path="/jobs/:id" element={<JobDetailPage />} />`
  the same way.
- Row-click nav pattern (`web/src/workers/WorkersTable.tsx`): the primary cell
  is a `react-router` `<Link to={`/workers/${w.id}`}>`. This is the accessible,
  copy-safe, middle-click-friendly pattern to mirror. JobsTable rows are
  currently plain `<div>`s with no link.
- Detail-page shell pattern (`web/src/workers/WorkerDetailPage.tsx`): reads
  `useParams()`, uses a polling `useQuery` hook, handles `isLoading && !data`
  skeleton, `error && !data` with a 404-vs-generic split and a back-Link,
  `!data` null. We follow this exact structure.
- Data-hook convention (`web/src/workers/useWorker.ts`): `useQuery` with
  `queryKey: ['worker', id]`, `refetchInterval: 3000`,
  `placeholderData: keepPreviousData`, `intervalMs` param default 3000 for
  test injection. The jobs list uses `['jobs', sort, status, cursor]`
  (`web/src/jobs/useJobs.ts`).
- API-fetch convention (`web/src/lib/api.ts`): `apiFetch<T>(path)` prefixes
  `/v1`, attaches bearer, throws `ApiError(status, code, msg)` on non-2xx.

## Scope

### In scope

- `/jobs/:id` detail page with the split layout below.
- Row-click navigation from `JobsTable` to `/jobs/:id`, mirroring the workers
  Link pattern.
- Tasks table with status, progress-ish state, retries, worker, and deps.
- Task-DAG dependency strip rendered from `tasks[].name` + `depends_on`.
- Spec tab (commands / env / requires).
- Log tab: static historical log via `GET /v1/tasks/{id}/logs`, fetch-once.
- Loading / empty / error states.
- Tests (unit + component) per the test plan.

### Explicitly deferred (do NOT build here)

- **Live SSE log tailing.** The Holo mock references `/v1/events?job_id=...`
  and a "follow" toggle. That depends on the unmerged `sse-task-log-publishing`
  enabler and is tracked in `feature-2026-06-26-task-log-view-sse-tailing`.
  This slice shows the STATIC historical log only (fetch-once via the GET
  endpoint). No `EventSource`, no follow toggle, no auto-scroll-to-tail.
- **Job write-actions (submit / cancel / retry).** Tracked in
  `feature-2026-06-26-job-actions-submit-cancel-retry`. Leave a clearly marked,
  empty header actions slot (a right-aligned `<div>` region) where the cancel /
  force-cancel controls will later mount. Do NOT wire `DELETE /v1/jobs/{id}` or
  render any action button in this slice.
- Single-task drill-down page (`/jobs/:id/tasks/:name`, Holo v3). Not requested.
- Per-task source/workspace block in the spec tab (not returned by the API;
  see Open decisions).
- Pagination controls on the tasks table. A job's tasks come back in one
  response; render them all.

## File layout

New files under `web/src/jobs/`, following the workers module shape:

- `JobDetailPage.tsx` - route component; owns layout, tabs, and the currently
  selected task.
- `useJob.ts` - `useQuery(['job', id])` hook, polling.
- `detailApi.ts` (or extend `api.ts`) - `getJob(id): Promise<JobDetail>` and
  `getTaskLogs(taskId, sinceSeq?)` plus the `JobDetail` / `TaskDetail` /
  `LogEntry` / `TaskLogPage` TS types and `TaskStatus`.
  - Decision: extend the existing `web/src/jobs/api.ts` rather than add a file,
    to keep one jobs API surface. Add the new types + fetchers there.
- `useTaskLogs.ts` - `useQuery(['task-logs', taskId])` for the log tab
  (enabled only when a task is selected and the Log tab is active).
- `taskStatus.ts` - `TaskStatus` type + `taskStatusColor()` mirroring
  `status.ts` but covering `dispatched` and `timed_out`.
- `TasksTable.tsx` - the tasks table (rows select a task; not navigation).
- `TaskDag.tsx` - the DAG strip (SVG).
- `SpecTab.tsx` - commands / env / requires renderer.
- `LogTab.tsx` - static log renderer.
- Tests: `JobDetailPage.test.tsx`, `TaskDag.test.tsx`, `taskStatus.test.ts`,
  and (if logic warrants) `dagLayout.test.ts`.

Modified files:

- `web/src/app/router.tsx` - add the `/jobs/:id` route.
- `web/src/jobs/JobsTable.tsx` - wrap the job name cell (and/or ID cell) in a
  `<Link to={`/jobs/${j.id}`}>`.
- `web/src/jobs/api.ts` - add detail types + fetchers.

## Data flow and hooks

- `useJob(id, intervalMs = 3000)`:
  `useQuery({ queryKey: ['job', id], queryFn: () => getJob(id),
  refetchInterval: intervalMs, placeholderData: keepPreviousData })`.
  Polling keeps task status/progress live without SSE. Matches `useWorker`.
- `useTaskLogs(taskId, enabled)`:
  `useQuery({ queryKey: ['task-logs', taskId], queryFn: () => getTaskLogs(taskId),
  enabled })`. No `refetchInterval` (static log, fetch-once). `enabled` is
  `taskId != '' && activeTab === 'log'` so we do not fetch logs for tasks the
  user never opens, and we do not fetch when the Spec tab is showing.
- Selected task: `JobDetailPage` holds `selectedTaskId` state. Default to the
  first task, or first running/failed task if one exists (running is the most
  useful default for a live job). The Spec and Log tabs both operate on the
  selected task.

## Layout and components

Grid mirroring Holo v1 "split pane", using the existing Tailwind token classes
(`border-border`, `bg-white/5`, `rounded-card`, `text-fg`, `text-fg-mute`,
`font-mono`, accent/ok/warn/err) already used across `web/src`.

```
Header
  breadcrumb: <- Jobs
  <h1> job.name </h1>  <status pill>
  meta line: id 9f4e1c · submitted by mira@ · priority high · labels [chips]
  [ actions slot: right-aligned empty region reserved for cancel/retry ]

Two-column body (flex row, gap):
  LEFT (~55%):
    overall strip: "N/M tasks done" + progress bar (derived from tasks)
    DAG strip (TaskDag): fixed-height SVG box, "solid edges done · dashed waiting"
    tasks table (TasksTable): scrollable; row click selects task
  RIGHT (~45%):
    tab bar: [ Spec ] [ Log ]   (Spec default; see Open decisions)
    Spec tab (SpecTab): commands[] block, env block, requires block for selected task
    Log tab (LogTab): static log lines for selected task
```

### Resizable split

The backlog and Holo mock call for a resizable split. Recommendation:
**start with a fixed 55/45 flex split, no drag handle, in this slice.** A
keyboard-and-mouse-accessible resizer (ARIA `separator`, `aria-valuenow`,
arrow-key handling, pointer drag, persisted width) is real work with its own
a11y surface and would inflate this slice. Ship the layout first; file a small
follow-up backlog item for the drag-resizer if desired. (Recorded in Open
decisions - the alternative is to include a minimal resizer now.)

### Overall progress

Derived client-side from the tasks array (the enrichment fields are not on the
detail response): `done = tasks.filter(status === 'done').length`,
`total = tasks.length`, `pct = round(done/total*100)`. Reuse `progressPct` from
`status.ts`. A "tasks active" count = tasks in `running`/`dispatched`.

### TasksTable

Columns (from `taskResponse`): name, status dot, retry (`retry_count/retries`),
worker (`worker_id` short, or "-"), deps (`depends_on` joined, or "-"). No
per-task duration or percent (the API returns neither started/finished per task
nor a percent; the Holo percent/dur were mock data). Rows are **selection**
controls, not navigation: a `<button>`/`role=row` with `aria-selected`,
`onClick` sets `selectedTaskId`. The selected row is visually highlighted.
Status colors come from `taskStatusColor` (covers `dispatched`, `timed_out`).

## DAG rendering approach

Input: `tasks[]` where each has `name` and `depends_on: string[]` (predecessor
names). Build a small directed graph and render as SVG (the Holo mock is
hand-rolled SVG; there is no graph library in the project and adding one is out
of scope).

- **Nodes**: one per task, keyed by name. Node fill/stroke by task status
  (done = ok, running/dispatched = accent, failed/timed_out = err, pending =
  muted).
- **Edges**: for each `task`, for each `dep` in `task.depends_on`, an edge
  `dep -> task`. Style: solid when the predecessor is `done`, dashed otherwise
  (matches "solid edges done · dashed waiting").
- **Layout**: compute a longest-path layer index per node (Kahn-style
  topological pass; a node's layer = 1 + max(layer of its deps), roots at 0).
  Lay layers out left-to-right in columns, nodes stacked vertically within a
  column. This handles the common relay shape (fan-in like `denoise-all`
  depending on `frame-00N`) without a physics/force layout. Keep it in a pure
  `dagLayout(tasks)` helper so it is unit-testable independent of SVG.
- **Scale / overflow**: for large graphs the strip is a fixed-height,
  horizontally scrollable SVG. If a job has more than a threshold of tasks
  (say > 40) the DAG can degrade to a compact "N tasks, M edges" summary or a
  scrollable strip rather than an unreadable tangle. Recommendation: render the
  scrollable SVG and cap node label length; do not build clustering in this
  slice.
- **A11y**: wrap the SVG with `role="img"` and an `aria-label` summarizing the
  graph (e.g. "Task dependency graph: 8 tasks, 2 dependency edges"). The
  authoritative, screen-reader-navigable representation of dependencies is the
  tasks table's deps column; the SVG is a visual aid.

## Row-click navigation and accessibility

- In `JobsTable`, wrap the job **name** in a `<Link to={`/jobs/${j.id}`}>`
  (mirroring `WorkersTable`'s name-cell Link). This gives real anchor semantics:
  keyboard focus, Enter to open, middle-click / cmd-click to open in a new tab,
  copyable URL - all for free, no `onKeyDown`/`tabIndex`/`role=button` needed.
- Keep the rest of the row non-interactive (or, optionally, make the whole row a
  hover-highlighted link target - but the minimal, accessible change is the
  name-cell Link, matching workers). Recommendation: name-cell Link only, to
  mirror workers exactly and avoid nested-interactive-content pitfalls.
- Give the name Link the existing `text-fg hover:text-accent` treatment used in
  `WorkersTable`.

## Loading / empty / error states

Mirror `WorkerDetailPage`:

- `isLoading && !job`: skeleton card (`h-40 rounded-card border ...`).
- `error && !job`: if `ApiError` with `status === 404`, show "Job not found."
  else show the error message + a Retry button; both include a
  `<- Jobs` back Link.
- `!job`: return null.
- Empty tasks: a job with zero tasks is not expected in practice, but render a
  small "No tasks." panel in the tasks area and an empty DAG rather than crash.
- Log tab empty: "No log output." when `items` is empty. Log tab error (e.g. a
  404 if the task vanished): inline "Failed to load logs" with a Retry.
- Selected-task guard: if `selectedTaskId` no longer matches any task after a
  poll (task list changed), fall back to the first task.

## Test plan

Component and unit tests using the established stack (vitest + Testing Library +
MSW), following `WorkerDetailPage.test.tsx` and existing jobs tests.

`JobDetailPage.test.tsx` (MSW mocks `GET /v1/jobs/:id`, `GET /v1/tasks/:id/logs`,
`GET /v1/users/me`):
- Renders job identity (name, status pill, owner email, id, labels).
- Renders the tasks table with each task name and status.
- 404 job -> "Job not found." + back Link.
- Non-404 job error -> Retry button.
- Selecting a task row updates `aria-selected` and drives the Spec/Log panes.
- Spec tab shows the selected task's commands/env/requires.
- Log tab (after switching) fetches once and renders log lines with
  stdout/stderr distinction; empty-log state shows "No log output."
- Log fetch is NOT triggered while the Spec tab is active (assert the log
  endpoint got zero hits until the tab is switched, mirroring the
  workers "non-admins never fetch workspaces" count assertion).
- Reserved actions slot renders but contains no action buttons (assert no
  Cancel/Retry button exists, guarding the deferral).

`taskStatus.test.ts`:
- `taskStatusColor` returns distinct classes for all six task statuses,
  including `dispatched` and `timed_out` (the ones `status.ts` lacks).

`dagLayout.test.ts`:
- Roots (no deps) land in layer 0.
- A fan-in node (`denoise-all` depends on `frame-001..006`) lands one layer
  past its deepest predecessor.
- Edge list is `dep -> task` for every `depends_on` entry.
- A chain `a -> b -> c` yields layers 0,1,2.

`JobsTable` row-click (extend existing jobs table test or add one):
- The job name renders as a link to `/jobs/:id` (assert `href`).

## Invariant / security lens

- Read-only slice: consumes existing `GET` endpoints only. No writes, so the
  epoch fence, job-spec pipeline, and teardown invariants are untouched.
- No new backend surface; auth is already enforced by `BearerAuth` on the
  routes. The page relies on `apiFetch`'s bearer attachment and the existing
  401 -> logout listener.
- Ownership: `handleGetJob` does not gate by owner (any authenticated user can
  read any job today). This slice does not change that; if job-detail access
  should be owner/admin-gated, that is a separate backend decision and backlog
  item, not this frontend slice.
- Load: the job poll (3s) and one-shot log fetch are lightweight. The log
  endpoint caps `limit` at 200 server-side. The DAG layout is O(nodes+edges)
  and runs client-side; large jobs degrade to a scrollable strip, not a hang.

## Open decisions

1. **Default tab: Spec vs Log.** Holo defaults to the live log. Since live tail
   is deferred and the static log may be empty for pending jobs, I recommend
   defaulting to the **Spec** tab (always has content) and letting the user
   switch to Log. Flag for review; easy to flip.
2. **Resizable split now or later.** Recommendation: fixed 55/45 split this
   slice, file a follow-up for an accessible drag-resizer. Confirm whether the
   resizer is required for this slice to be "done".
3. **Per-task source/workspace block.** The Holo spec tab shows a Perforce
   source/workspace block, but `handleGetJob` does not return per-task `source`.
   Rendering it needs a backend change (echo `source` in `taskResponse`). Out of
   scope here; note as a possible backlog item if the source block is wanted.
4. **Whole-row vs name-cell link.** Recommendation: name-cell Link only, to
   mirror workers and avoid nested interactive content. Confirm if a full-row
   click target is preferred.
5. **Task status vocabulary drift.** The frontend `JobStatus` union does not
   include task-only statuses (`dispatched`, `timed_out`). This spec adds a
   separate `TaskStatus` type + color map rather than widening `JobStatus`, to
   keep the job-list semantics clean. Confirm that separation is acceptable.
