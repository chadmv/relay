# Worker Detail Current-Tasks Panel - Design

Date: 2026-09-01
Status: Approved (autonomous gate; see Decisions for every question answered in place of a human)
Backlog item: `docs/backlog/feature-2026-06-05-worker-detail-activity-panel.md`
Lane: E of the six-lane web SPA batch. Backend first, frontend second, one branch, one PR.

A note on quoted literals. Several strings in the current page and its tests contain a non-ASCII
character (an em dash used as a "no value" placeholder, and a middle dot used as a separator). This
document never reproduces those bytes; it describes them in words, so that a search built from this
spec cannot be silently wrong about a character nobody can verify by eye.

## Summary

`/workers/:id` renders three placeholders that all cite the same missing capability: there is no HTTP
endpoint that answers "what is this worker doing right now". This slice adds one read endpoint,
`GET /v1/workers/{id}/tasks`, returning the worker's currently assigned tasks in the standard page
envelope, and builds the "Current tasks" panel on it. Two of the three placeholders become real; the
third, the "Jobs today" activity aggregate, is deliberately deferred with its own item, because it
needs a new index, a new migration, and a product decision the hi-fi gets wrong for relay's data
model.

Nothing about a task's execution state is fabricated. The hi-fi shows a per-task progress bar; relay
has no progress column and no agent message that reports one, so no progress is rendered and the
enabler is filed instead.

## Context at HEAD (by symbol)

Everything below was checked against the working tree, not taken from the item.

### The three placeholders exist, and there are three, not two

`web/src/workers/WorkerDetailPage.tsx` at HEAD:

- `<Panel title="Current tasks" meta="ACTIVITY ENDPOINT PENDING">` wrapping the literal
  `no per-worker task feed yet`. Comment: "Backend-blocked: no per-worker task feed endpoint exists
  yet. Enabler: feature-2026-06-05-worker-detail-activity-panel."
- A `<KpiStat label="Jobs today" ... />` whose `value` is a single em dash character and whose `sub`
  is `activity endpoint pending`. Same cited enabler.
- **A `<KpiStat label="Slots" ... />`** whose `value` is a template string of an em dash, a space, a
  forward slash, a space, then `worker.max_slots`, with `progress={{ used: 0, max: worker.max_slots }}`.
  The item does not mention this one, and its comment cites this same backlog item as its enabler:
  "`used` (active slots) is not on the Worker type yet". So closing this item without addressing the
  Slots card would leave a dangling enabler reference.

Their tests are in `web/src/workers/WorkerDetailPage.test.tsx`:
`renders the current-tasks placeholder note, not an empty table`,
`the current-tasks panel contains no fabricated task rows`,
`renders the Jobs-today placeholder KPI with no fabricated data`, and
`renders the CPU/RAM and Slots KPI cards`, which asserts a literal made of an em dash, a space, a
forward slash, a space, and the digit 4.

### Refutations of the item

The item was filed 2026-06-05. Two of its claims are wrong at HEAD:

1. **`ActiveTaskCounts` does not exist.** The item names it twice, in Context and in Related. The
   statement at HEAD is `CountActiveTasksByAllWorkers` in `internal/store/query/tasks.sql`. Its
   shape is as described (`worker_id, count(*)` grouped, scheduler-only, not exposed over HTTP) but
   it is **not reusable here**: it has no worker filter, so calling it to serve one worker's page
   would aggregate the entire `tasks` table on every poll.
2. **"Both need a new backend endpoint" understates the aggregate.** The current-tasks list needs an
   endpoint. The aggregate needs an endpoint *and a new index and a migration*, because there is no
   index on `tasks(worker_id)` that covers terminal rows (see Store query design). The item presents
   them as one unit of work; they are not.

Not refuted, and confirmed at HEAD: no per-worker task HTTP surface exists; the wireframe does show
both panels; `internal/api/tasks.go` and `internal/api/workers.go` are the right files.

Nothing else in the item was found to be false.

### Auth posture of the existing worker reads

`internal/api/server.go`:

- `GET /v1/workers/{id}` maps to `auth(...)`. Any authenticated user.
- `GET /v1/workers/{id}/metrics` maps to `auth(...)`. Any authenticated user.
- `GET /v1/workers/{id}/workspaces` maps to `auth(admin(...))`. Admin.

And the task reads, which is the other posture this endpoint has to agree with:

- `GET /v1/jobs/{id}/tasks`, `GET /v1/tasks/{id}`, `GET /v1/tasks/{id}/logs` are all `auth(...)`,
  under a comment that states the choice deliberately: "Read routes (GET) are intentionally global:
  any authenticated user may read any job's metadata, task list, and logs. This is deliberate
  render-farm semantics."

So `auth(...)` is the only posture consistent with both sides. See Decision 6.

### The task JSON rendering to reuse

`internal/api/jobs.go`: `type taskResponse` and `func toTaskResponse(t store.Task, dependsOn []string) taskResponse`.
Fields: `id`, `name`, `status`, `commands`, `env`, `requires`, `timeout_seconds`, `retries`,
`retry_count`, `depends_on` (omitempty), `worker_id` (omitempty). It takes a `store.Task`, which
matters for Decision 4.

`internal/api/tasks.go`'s `handleListTasks` is the existing consumer:
`resp[i] = toTaskResponse(t, nil)` over `s.q.ListTasksByJob`, which returns `[]store.Task` because
the statement is a bare `SELECT * FROM tasks`.

The embedding pattern for "the existing shape plus extras" is already in `internal/api/workers.go`:
`disableWorkerResponse` and `deleteWorkerResponse` both embed `workerResponse`.

### Page envelope conventions

`internal/api/pagination.go`:

- `page[T]{Items, NextCursor, Total}` with json tags `items`, `next_cursor`, `total`.
- `parsePage(w, r, spec)` validates `?limit=` (range `[1, 200]`, default 50, **400 not a clamp** on
  out-of-range), `?sort=` against a per-endpoint `SortSpec` allow-list, and `?cursor=`. It writes
  its own 400 and returns `ok=false`.
- A cursor is tagged with the sort it was issued under; resending it under a different sort is a 400.
- `buildPage(rows, limit, sort, conv, key)` fetches `limit+1`, trims, and encodes the cursor from
  the **last kept** row.
- `RevokedWorkersSortSpec` plus the explicit `if pp.Sort != "-revoked_at"` refusal in
  `handleListRevokedWorkers` is the in-repo pattern for a **fixed-order** paginated endpoint.
- `ListWorkersPageByLastSeenDesc` (`internal/store/query/workers.sql`) is the in-repo pattern for a
  `DESC NULLS LAST` keyset cursor, with the `CASE WHEN @cursor_is_null` split. `workersRowKeyByLastSeen`
  returns a nil `*time.Time` for the NULL case and `encodeCursorV2` encodes it as `N: true`.
- Filtered list endpoints already scope `total` to the filter: `CountJobsByStatus` exists alongside
  `CountJobs`.

### The tasks table and its indexes

Columns (migrations 000001, 000004, 000007, 000008, 000021): `id`, `job_id`, `name`, `commands`,
`env`, `requires`, `timeout_seconds`, `retries`, `retry_count`, `status`, `worker_id`, `started_at`,
`finished_at`, `created_at`, `assignment_epoch`, `source`, `assigned_at`.

**There is no `progress` column and no percentage anywhere in the schema.** The hi-fi's progress bar
has no data behind it.

Indexes on `tasks`:

- `idx_tasks_job_id ON tasks(job_id)` (000001)
- `idx_tasks_status ON tasks(status)` (000001)
- `idx_tasks_worker_active ON tasks(worker_id) WHERE status IN ('dispatched', 'running')` (000018)

`idx_tasks_worker_active` covers the exact predicate this slice needs, which is why no migration is
required. Note what it does **not** cover: a Postgres foreign key creates no index, so there is no
plain index on `tasks(worker_id)`. Any query over this worker's *terminal* rows is a sequential scan
of the whole table. That is the cost argument in Decision 2.

`assigned_at` is non-NULL for every row in the assignment partition in practice:
`ClaimTaskForWorker` is the only route into `('dispatched','running')` and stamps `assigned_at` in
the same statement, and migration 000021 backfilled in-flight rows with `NOW()`. It is not
structurally guaranteed (no `NOT NULL`), so the ordering keeps a `NULLS LAST` branch.

### The status vocabulary lockstep guard

`internal/store/tasks_status_vocabulary_lockstep_test.go`, `TestTasksStatusVocabularyIsExactly`
(integration lane, reads the live `tasks_status_check` constraint). Its comment enumerates thirteen
statements plus two non-statement sites, and its failure message names six statements as carrying
the "currently assigned" partition `status IN ('dispatched','running')`:
`ListOverdueAssignedTasks`, `GetActiveTasksForWorker`, `ListGraceCandidates`, `RequeueTaskByID`,
`RequeueWorkerTasks`, `RequeueWorkerTasksIfEpoch`.

That census is a claim about the complement, and this slice adds two more members to the partition.
Updating it is required scope, not tidying. See Decision 3.

### Frontend primitives

- `web/src/components/holo/Panel.tsx` - `Panel({title, meta, footer, className, bodyClassName, children})`.
  Its own comment names "Current tasks" as an intended consumer.
- `web/src/components/holo/Table.tsx` - `Table`, `TableRow`, `TableCell`, `TableColumn`. `label`
  becomes the accessible name. **`minWidth` is a required prop**, a literal Tailwind `min-w-[Npx]`
  utility, applied to the header row and every body row, and it always wraps the `role="table"`
  subtree in an `overflow-x-auto` focusable group. Every `fr` cell must carry `truncate` or
  `min-w-0` or the min-width budget is wrong.
- `web/src/workers/WorkspacesPanel.tsx` is the sibling panel in the same page column. Its
  `MIN_W = 'min-w-[600px]'` carries a measured comment: "this sits in a detail-page column of about
  614px at 1280, so 600 is deliberately tight: it is the largest value that does not put a scrollbar
  on a maximized desktop window."
- `web/src/components/holo/responsive.guard.test.ts` asserts that no bare `grid-cols-N` or
  `col-span-N` appears without a breakpoint prefix, **and** pins an exact four-element list of files
  containing `md:grid-cols-`. `grid-cols-[...]` arbitrary track lists are not matched by either rule.
- `web/src/jobs/taskStatus.ts` - `taskStatusColor(status: TaskStatus)` returning `{text, dot}`
  Tailwind classes for the task vocabulary. `web/src/jobs/api.ts` -
  `export type TaskStatus = 'pending' | 'dispatched' | 'running' | 'done' | 'failed' | 'timed_out'`.
  Cross-feature import precedent exists: `web/src/schedules/ScheduleRunsPanel.tsx` imports
  `statusColor` and the `Job` type from `../jobs/`.
- Cadence at HEAD: `useWorker` polls at 3000, `useWorkerMetrics` at 10000, `useWorkerWorkspaces` at
  15000 with an `enabled` admin guard.
- Routes at HEAD (`web/src/app/router.tsx`): `/jobs/:id` (JobDetailPage) and
  `/jobs/:id/tasks/:taskId` (TaskLogPage) both exist.

### Layout headroom

`docs/backlog/idea-2026-08-24-e2e-harness-slice-2-agent-in-harness.md`: "`/workers/:id` is the
specific page the 2026-08-13 retro flagged as having under 15px of layout headroom, so it is the
highest-value single surface this unlocks." The same item records that the harness has **no agent**,
so `/workers/:id` "is never visited at all" by the browser lane and no worker in it ever has a task.
That bounds what this slice can measure. See Decision 11.

## Decisions

Autonomous run: no human was available, so each question below was decided here. Options, choice and
reason are recorded so the choice can be overturned on review without re-deriving it.

### Decision 1 - filter semantics: active only, no `?status=` widening

**Question.** Default to the active statuses with an optional `?status=` to widen, or active only?

**Options.** (a) Active only, no parameter. (b) Active by default plus `?status=` accepting a
comma-separated allow-list over the six-value vocabulary. (c) Two endpoints, one active and one
historical.

**Choice: (a).**

**Why.** The predicate `status IN ('dispatched','running')` is exactly the partial index
`idx_tasks_worker_active`, so option (a) needs no migration and no plan change. Any widening reads
terminal rows, which have no index on `worker_id` at all, so option (b) ships a parameter whose
every non-default value sequentially scans `tasks` - a parameter that is a footgun the first time
somebody sends it on a large deployment. Option (b) also has to answer what `total` means and how
the cursor orders rows whose `assigned_at` is stale, and (c) doubles the surface for a panel nobody
has asked for. A per-worker task **history** is a real feature with a real UI (its own paging, its
own time filter, its own index); it is filed as a follow-up rather than smuggled in as a query
parameter.

Consequence stated plainly: this endpoint answers one question, "what is assigned to this worker
right now", and cannot answer any other. The route name says `tasks` rather than `active-tasks`
because the history slice should extend this endpoint with a parameter once it brings its own index,
not create a second path.

### Decision 2 - the "jobs today" aggregate is OUT of this slice

**Question.** Does the activity aggregate (count, failures, average duration over a window) ship as
a second endpoint, as a field on this one, or is it deferred?

**Options.** (a) Ship it as `GET /v1/workers/{id}/activity`. (b) Ship it as extra top-level fields on
this endpoint's envelope. (c) Defer with a follow-up item.

**Choice: (c), deferred.**

**Why, three independent reasons, any one of which is sufficient.**

1. **Cost.** The aggregate reads terminal rows for one worker in a 24h window. There is no index on
   `tasks(worker_id)` covering terminal rows - `idx_tasks_worker_active` is partial and excludes
   them, and the FK creates nothing. So it is a sequential scan of `tasks`, on a page that polls
   every 3 seconds, once per open worker-detail tab. It needs a new partial index such as
   `tasks(worker_id, finished_at) WHERE status IN ('done','failed','timed_out')`, which is a
   migration.
2. **The label is a category error and the spec should not enshrine it.** The hi-fi
   (`design_handoff_relay_holo/hifi3-holo-pages.jsx`) renders a KPI whose label is `Jobs today`,
   whose value is the literal `47`, and whose sub-line reads `3 failed`, a middle dot, then
   `avg 4m 12s`. Relay assigns **tasks** to workers, never jobs: `tasks.worker_id` exists, `jobs`
   has no worker column, and one job's tasks routinely span several workers. So "jobs today" for a
   worker is either "distinct jobs this worker touched", which is a `COUNT(DISTINCT job_id)` and a
   different query again, or it is the wrong word for "tasks". Which one an operator wants is a
   product decision, and shipping the wrong word under a KPI card is the defect class this project
   sees most.
3. **Slice size.** Lane E is one branch and one PR with a backend half and a frontend half already.
   Adding a migration, a second query family, a second endpoint and a second KPI wiring doubles it.

What ships instead: the "Jobs today" `KpiStat` stays a placeholder, and **its comment is repointed at
the new follow-up item** so it does not cite an item that has just been closed. That repointing is
required scope.

### Decision 3 - the new statements join the assignment-partition census

**Question.** The two new statements carry `status IN ('dispatched','running')`. Is updating
`TestTasksStatusVocabularyIsExactly` optional?

**Choice: no, it is required scope, and both the comment and the failure message must be updated.**

**Why.** That test's failure message names six statements as the members of the partition and tells
the next reader to "Revisit ALL OF THEM". After this slice there are eight. A census that says six
when the answer is eight is a claim about the complement that is false, and the two new members are
in the **inverted** camp - the one the test's comment spells out at length. Trace `preparing`, that
comment's own named candidate: a task sitting in a long P4 sync is exactly the row an operator opens
this panel to find, and if `preparing` is added to the vocabulary but not to these two statements,
the panel shows the worker as idle while it is busy, and the Slots KPI under-reports its load. No
error, no log line.

Direction to record at the new sites: these two are **read-only and display-only**. Omitting a new
non-terminal status silently under-reports the worker's assignment set in the UI; it can neither
admit nor refuse a write. A new terminal status must stay out - a terminal task holds no slot.

The same paragraph must note that `idx_tasks_worker_active`'s own `WHERE` clause is a ninth copy of
this predicate, and that a status added to the statements but not to the index turns both new
queries into sequential scans rather than making them wrong.

### Decision 4 - two statements, not a join, to avoid a hand-written struct copy

**Question.** Fetch the job name with a `JOIN jobs` in the list statement, or with a second
statement over the page's job ids?

**Options.** (a) `SELECT t.*, j.name AS job_name FROM tasks t JOIN jobs j ON j.id = t.job_id ...`,
which sqlc flattens into a row struct; the handler then hand-builds a `store.Task` from it to call
`toTaskResponse`. (b) `SELECT * FROM tasks WHERE ...`, which sqlc emits as `[]store.Task`, plus
`GetJobNamesByIDs` over the page's distinct job ids.

**Choice: (b).**

**Why.** Option (a) is the repo's existing pattern and it is the repo's existing hazard. `jobs.go`
contains **six** copies of

```go
job := store.Job{
    ID: r.ID, Name: r.Name, Priority: r.Priority, Status: r.Status,
    SubmittedBy: r.SubmittedBy, Labels: r.Labels,
    CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
}
```

each a partial, hand-maintained copy that silently loses any column `jobs` gains. `tasks` has
seventeen columns. Option (b) keeps `toTaskResponse` on a real `store.Task` produced by sqlc, so the
conversion cannot drift by construction, and it matches `handleListTasks` exactly.

The cost is one extra round trip. It is bounded: at most `limit` distinct job ids, looked up on the
`jobs` primary key. `internal/store/query/users.sql` already carries the exact exemplar -
`GetUserEmailsByIDs :many SELECT id, email FROM users WHERE id = ANY($1::uuid[])` - so this is a
copy of a working shape, not a new technique.

If a reviewer prefers (a), the compensating control is mandatory: a `NumField` arity test comparing
the copy against `store.Task`, per the repo's hand-written-copy rule.

### Decision 5 - ordering is `assigned_at DESC NULLS LAST, id DESC`, fixed

**Question.** `started_at` descending, or assignment order?

**Choice: `assigned_at DESC NULLS LAST, id DESC`. No `?sort=` beyond the single default key.**

**Why not `started_at`.** It is NULL for every `dispatched` row, and a task spends the entire
workspace sync as `dispatched` - `ListOverdueAssignedTasks`'s comment states this: "a task spends
the whole workspace sync as `dispatched` (handleTaskStatus has no case for TASK_STATUS_PREPARING, so
the row does not move)". Ordering by `started_at` therefore dumps every not-yet-running task into an
undifferentiated NULL bucket at the bottom, and the long-sync task is the single most interesting
row on this panel.

**Why `assigned_at`.** It is written by `ClaimTaskForWorker`, the only route into this partition, in
the same statement that sets the status, so every row this endpoint returns has one. It is the
worker's actual work order. `NULLS LAST` is kept anyway because the column has no `NOT NULL`
constraint, and the DESC-NULLS-LAST keyset cursor is already a solved problem here:
`ListWorkersPageByLastSeenDesc` plus `workersRowKeyByLastSeen` is copied verbatim in shape.

**Why fixed order.** Nothing on the panel offers a sort control, and `handleListRevokedWorkers`
already establishes the pattern for a fixed-order paginated endpoint: declare a one-key `SortSpec`
so `parsePage` resolves the default and tags the cursor, then refuse any other resolved sort with a
400. That keeps limit and cursor validation for free without pretending to offer sorts the SQL
cannot serve.

### Decision 6 - authorization is `auth(...)`, any authenticated user

**Question.** Match worker detail (`auth`) or worker workspaces (`auth(admin)`)?

**Choice: `auth(...)`.**

**Why.** Both neighbours agree. `GET /v1/workers/{id}` and `GET /v1/workers/{id}/metrics` are
`auth(...)`, and every task read route is `auth(...)` under an explicit comment declaring global
task reads to be deliberate render-farm semantics. This endpoint is a projection of task rows keyed
by worker, so gating it on admin would be stricter than either thing it is made of, while gating the
worker page's own header on `auth` and this panel on `admin` would give non-admins a permanently
empty panel with no explanation.

Disclosure check: the embedded `taskResponse` carries `commands`, `env` and `requires`. These are
**already** readable by any authenticated user through `GET /v1/tasks/{id}` and
`GET /v1/jobs/{id}`, so this endpoint discloses no field that was previously privileged. It does
make them reachable by a new *index* (by worker rather than by job), which is a convenience for a
legitimate operator and not a new capability, since `GET /v1/jobs` already enumerates every job.

### Decision 7 - `assignment_epoch` is NOT exposed

**Question.** The row carries it and it would be free to include. Include it?

**Choice: no, and this is a security decision rather than a tidiness one.**

**Why.** The epoch is a fence token. `RequeueTask`'s comment in `internal/store/query/tasks.sql`
records the exact residual it protects: "handleTaskStatus takes the epoch OFF THE WIRE and compares
it to the DB's current value, so a connected agent that is the current assignee could report
'running' at an epoch it was never dispatched. That needs a task id it would not otherwise know."
This endpoint would publish, to any authenticated user, a live list of `(task id, current epoch)`
pairs for a named worker - precisely the two values that residual says an attacker would otherwise
have to guess. No existing response exposes `assignment_epoch`: `taskResponse` does not carry it and
neither does `GET /v1/tasks/{id}`. Keep it that way, and pin it with a test that asserts absence
rather than trusting the struct definition.

### Decision 8 - no progress field; omit rather than fabricate

**Question.** The hi-fi's rows carry a progress bar and a percentage. What does the backend supply?

**Choice: nothing. The column does not exist and is not rendered.**

**Why.** There is no progress column on `tasks`, no progress field on any proto message, and no
agent-side computation of one. The batch rule is to omit what the backend cannot supply and file the
enabler. The existing test `the current-tasks panel contains no fabricated task rows` and its
sibling `renders the Jobs-today placeholder KPI with no fabricated data` (which explicitly guards
against the hi-fi's literal `47`) show this page already holds that line; this slice keeps it.

Filed as a follow-up: agent-reported task progress, which is a protocol change, not a UI change.

### Decision 9 - `total` is the filtered count, and it feeds the Slots KPI

**Question.** What is `total`, and can the Slots card use it?

**Choice: `total` is the count of this worker's active tasks, and yes, the Slots card renders
`total` over `max_slots`.**

**Why.** Filtered list endpoints in this repo already scope `total` to the filter -
`CountJobsByStatus` exists beside `CountJobs`. And the count of rows in
`status IN ('dispatched','running')` for a worker is, by definition, the same number the dispatcher
calls a used slot: `CountActiveTasksByAllWorkers` computes exactly this partition to derive
available slots. So the Slots KPI stops being a placeholder without inventing anything.

Two honesty notes that must reach the code and the README:

- `total` and `items` come from two statements, so under concurrent dispatch they can disagree by
  one for an instant. Every list endpoint here has that property.
- `used` can legitimately **exceed** `max_slots`, because `max_slots` is a dispatcher input, not a
  database constraint, and lowering it via `PATCH /v1/workers/{id}` does not requeue anything. The
  progress bar must clamp its fill and must not render a negative or over-100% width.

### Decision 10 - poll cadence 3000, matching `useWorker`

**Question.** What refetch interval?

**Options.** 3000 (matching `useWorker`), 10000 (matching metrics), or SSE.

**Choice: 3000.**

**Why.** The header status and the current-tasks panel answer the same question at the same
timescale: what is this worker doing now. Task assignment changes at dispatcher speed, which is
NOTIFY-driven and far faster than 10s. And the Slots card is now composed from two queries -
`used` from this one, `max` from `useWorker` - so a mismatched cadence puts the two halves of one
displayed fraction an interval apart. SSE is rejected: `internal/events` frames carry a `JobID`
filter, not a worker filter, so there is no worker-scoped subscription to open and inventing one is
a backend slice of its own.

### Decision 11 - placement, width, and what this slice can honestly measure

**Question.** Where does the panel go without introducing overflow on a page with under 15px of
headroom, and how is that verified?

**Choice.** The panel replaces the existing "Current tasks" `Panel` **in place**, first item in the
left column of the existing `md:grid-cols-2` body. No new panel, no new column, no change to the
page's grid.

Width budget, derived rather than guessed:

- The left column measures about 614px at a 1280 viewport, per `WorkspacesPanel`'s measured comment.
- `WorkspacesPanel`, the sibling directly below, uses `min-w-[600px]` and is documented as the
  largest value that avoids a scrollbar there.
- The tasks table therefore takes `min-w-[560px]`, below its sibling, so it can never be the widest
  element in the column.
- Track template `grid-cols-[1fr_1fr_100px_90px_60px]`: fixed tracks sum to 250, well under 560, and
  both `fr` cells carry `truncate` so their content minimum is zero, which is the condition
  `Table.tsx` states for the min-width budget to hold.
- It is an arbitrary-value track list, so `responsive.guard.test.ts`'s numeric `grid-cols-N` rule
  does not match it, and the panel must **not** introduce `md:grid-cols-` or that test's pinned
  four-file list goes RED.

**What cannot be measured in this slice, stated rather than glossed.** The rule is that the
populated state is what gets measured, and jsdom performs no layout, so no vitest test can measure
anything here. The browser lane could, but `web/e2e/` runs no agent, so no worker in the harness
ever has an assigned task, and `/workers/:id` is not even a covered surface there. This slice
therefore ships an **arithmetic** argument (fixed-track sum under a min-width that is under a
sibling's measured-safe value) and not a measurement. That residual is filed as a follow-up rather
than papered over: measuring the populated panel is a task for
`idea-2026-08-24-e2e-harness-slice-2-agent-in-harness`, or for a narrower item that seeds a
`dispatched` row directly.

### Decision 12 - panel meta is the route, not the hi-fi's slot count

**Question.** The hi-fi's panel header meta reads "{used} OF {max} SLOTS".

**Choice: `GET /v1/workers/{id}/tasks`.**

**Why.** Both sibling panels on this page already put the route there ("`/v1/workers/.../workspaces`"
and "`GET /v1/workers/{id}/metrics`"), so the page has its own established convention and the hi-fi
diverges from it. The used-over-max fraction now lives in the Slots KPI card, where this page
already put it, so the hi-fi's version would be a second copy of one number that can momentarily
disagree with the first.

### Decision 13 - two cell links, no row link

**Question.** The brief says the row links to the job detail page; the hi-fi links the task id to a
task view.

**Choice.** The JOB cell links to `/jobs/{job_id}` and the TASK cell links to
`/jobs/{job_id}/tasks/{task_id}`. The row itself is not a link.

**Why.** Both routes exist at HEAD. An operator on a worker page most often wants to see what the
running task is printing, which is the task log route; the brief's requirement is satisfied
literally by the JOB cell. A row-level link plus two inner links would nest interactive elements, so
the row stays a plain `TableRow`.

### Decision 14 - one empty message, not the hi-fi's two

**Question.** The hi-fi branches between a worker-is-offline message and an idle message.

**Choice: one message, "No active tasks."**

**Why.** The two-message version asserts a cause, and the cause is not always true: an offline
worker inside `RELAY_WORKER_GRACE_WINDOW` still has its tasks assigned (the requeue is deferred by
`GraceRegistry`), so an offline worker can legitimately show rows, and an empty list for an offline
worker does not establish that offline is the reason. Wrong prose beside correct code is this
project's dominant defect; one accurate sentence beats two evocative ones.

### Decision 15 - the item is proposed for closure, with the aggregate carved out

**Question.** This slice satisfies two of the item's three "Done When" bullets. Close it or amend it?

**Choice: propose closing it, and propose a new item for the aggregate.**

**Why.** The item's value was the backend enabler, and that is delivered. Leaving it open with only
the aggregate outstanding would duplicate the new follow-up item, and a half-satisfied item is
harder to schedule than a fresh, correctly scoped one. The conductor confirms, and the close must go
through `/backlog close` so the file is moved with `git mv` into `docs/backlog/closed/`, not
status-flipped in place.

## API design

### Route

```
GET /v1/workers/{id}/tasks
```

Registered in `internal/api/server.go` beside the other worker reads:

```go
mux.Handle("GET /v1/workers/{id}/tasks", auth(http.HandlerFunc(s.handleListWorkerTasks)))
```

`auth(...)`, no `admin(...)`. Go's `ServeMux` prefers the more specific pattern, so this coexists
with `GET /v1/workers/{id}` exactly as `GET /v1/workers/{id}/metrics` already does.

### Parameters and validation

| Param | Handling |
|-------|----------|
| `{id}` | `parseUUID(r.PathValue("id"))`; on error `400 "invalid worker id"`, byte-identical to `handleGetWorker`. |
| `?limit=` | Via `parsePage`. Default 50, range `[1, 200]`, **400 on out-of-range, not a clamp**. |
| `?cursor=` | Via `parsePage`. Opaque; a cursor issued under a different sort is a 400. |
| `?sort=` | Via `parsePage` against `WorkerTasksSortSpec`. Default `-assigned_at`. Any other resolved value is refused with a 400, mirroring `handleListRevokedWorkers`. |

```go
var WorkerTasksSortSpec = SortSpec{
    Default: "-assigned_at",
    Keys:    map[string]SortKeyKind{"assigned_at": SortKeyTimestamp},
}
```

Existence: the handler calls `s.q.GetWorker(ctx, id)` first and returns
`404 "worker not found"` on `pgx.ErrNoRows`, `500 "db error"` otherwise - the same shape and the same
strings as `handleGetWorker`, and the same "verify the parent exists before paginating" ordering
`handleGetTaskLogs` uses. A revoked worker is returned by `GetWorker` and is therefore not a 404
here; that matches `GET /v1/workers/{id}` and `GET /v1/workers/{id}/metrics`.

### Response

```go
// workerTaskResponse is one currently-assigned task. It EMBEDS taskResponse so
// this endpoint cannot drift from GET /v1/jobs/{id} and GET /v1/tasks/{id} on the
// task's own fields, exactly as disableWorkerResponse embeds workerResponse.
// assignment_epoch is deliberately absent - see the handler comment.
type workerTaskResponse struct {
    taskResponse
    JobID      string     `json:"job_id"`
    JobName    string     `json:"job_name"`
    AssignedAt *time.Time `json:"assigned_at,omitempty"`
    StartedAt  *time.Time `json:"started_at,omitempty"`
}
```

Envelope: `page[workerTaskResponse]`, so `{items, next_cursor, total}`.

`taskResponse.WorkerID` is present and always equals the path id here. That redundancy is accepted
rather than special-cased, because suppressing it would mean not reusing `toTaskResponse`.

`depends_on` is `omitempty` and this endpoint passes `nil`, so it is absent. Resolving dependency
names would be a second query per row for no panel value.

### Example response

```json
{
  "items": [
    {
      "id": "6f1c2a4e-5b7d-4a10-9e33-0c8a2b114d77",
      "name": "render-shot-042",
      "status": "running",
      "commands": [["blender", "-b", "shot042.blend"]],
      "env": null,
      "requires": null,
      "timeout_seconds": 3600,
      "retries": 2,
      "retry_count": 1,
      "worker_id": "2bce9f31-77aa-4c02-8f4d-9a1b2c3d4e5f",
      "job_id": "b41d0e7a-9c33-4e51-8a0f-77de11a2b3c4",
      "job_name": "nightly-render",
      "assigned_at": "2026-09-01T09:14:02.118Z",
      "started_at": "2026-09-01T09:16:40.902Z"
    },
    {
      "id": "1a2b3c4d-5e6f-4708-9a0b-1c2d3e4f5061",
      "name": "sync-depot",
      "status": "dispatched",
      "commands": [["p4", "sync"]],
      "env": null,
      "requires": null,
      "timeout_seconds": null,
      "retries": 0,
      "retry_count": 0,
      "worker_id": "2bce9f31-77aa-4c02-8f4d-9a1b2c3d4e5f",
      "job_id": "b41d0e7a-9c33-4e51-8a0f-77de11a2b3c4",
      "job_name": "nightly-render",
      "assigned_at": "2026-09-01T09:13:55.004Z"
    }
  ],
  "next_cursor": "",
  "total": 2
}
```

The second row is the case the ordering choice exists for: `dispatched`, syncing a Perforce
workspace, with no `started_at` at all.

### README

`README.md`'s Workers table gains one row, matching the style of the `/metrics` row beside it:

> `GET` | `/v1/workers/{id}/tasks` | List the tasks currently assigned to a worker (`dispatched` or
> `running`), newest assignment first. Paginated, standard `page` envelope; `total` is the count of
> ACTIVE tasks for this worker, which is the same number the dispatcher treats as used slots. Fixed
> order, sortable only by `-assigned_at` (the default). 404 if the worker does not exist. Same
> bearer-auth as `GET /v1/workers/{id}`.

Plus a sentence recording that this endpoint does not expose terminal tasks and that a per-worker
task history has no endpoint. Writing "not yet" there would promise a schedule; write what is true.

## Store query design

Two new statements in `internal/store/query/tasks.sql`, plus one in `jobs.sql`. **No migration.**

### `ListActiveTasksForWorkerPage :many`

```sql
-- name: ListActiveTasksForWorkerPage :many
-- The worker-detail Current-tasks panel (GET /v1/workers/{id}/tasks). Read-only.
--
-- THE PARTITION IS ('dispatched','running') - "currently assigned" - the same set
-- GetActiveTasksForWorker, ListGraceCandidates, ListOverdueAssignedTasks,
-- RequeueWorkerTasks(IfEpoch) and RequeueTaskByID carry, and the same set
-- idx_tasks_worker_active is partial on. READ THE ALLOW-LIST BACKWARDS, like those
-- sites: a new NON-TERMINAL status omitted here is invisible in the panel and
-- uncounted by CountActiveTasksForWorker below, so an operator sees an idle worker
-- that is busy - no error, no log line. `preparing` is the live candidate. A new
-- TERMINAL status must stay OUT; a finished task holds no slot.
-- TestTasksStatusVocabularyIsExactly names this statement.
--
-- SELECT * so sqlc emits []store.Task and the handler can call toTaskResponse
-- directly. The job name comes from a separate GetJobNamesByIDs call rather than a
-- JOIN, so no hand-written store.Task copy exists to drift.
--
-- assigned_at DESC NULLS LAST, id DESC. started_at would bury every dispatched row
-- in a NULL bucket - a task is `dispatched` for the whole workspace sync - and
-- those are the rows this panel exists to show. Every row here has an assigned_at
-- in practice (ClaimTaskForWorker is the only route into this partition and stamps
-- it in the same statement), but the column has no NOT NULL constraint, so the
-- NULLS LAST branch stays. Cursor predicate copied in shape from
-- ListWorkersPageByLastSeenDesc.
SELECT * FROM tasks
WHERE worker_id = sqlc.arg(worker_id)
  AND status IN ('dispatched', 'running')
  AND (
       NOT sqlc.arg(cursor_set)::bool
    OR (
       CASE WHEN sqlc.arg(cursor_is_null)::bool THEN
            assigned_at IS NULL AND id < sqlc.arg(cursor_id)::uuid
       ELSE
            (assigned_at IS NOT NULL AND
             (assigned_at, id) < (sqlc.arg(cursor_ts)::timestamptz, sqlc.arg(cursor_id)::uuid))
         OR assigned_at IS NULL
       END
   ))
ORDER BY assigned_at DESC NULLS LAST, id DESC
LIMIT sqlc.arg(page_limit)::int + 1;
```

### `CountActiveTasksForWorker :one`

```sql
-- name: CountActiveTasksForWorker :one
-- `total` for the page above, and the used-slot count the Slots KPI renders. The
-- status predicate must stay byte-identical to ListActiveTasksForWorkerPage's -
-- change both or neither - and is the same partition CountActiveTasksByAllWorkers
-- computes for the dispatcher. Do NOT serve one worker from that statement: it has
-- no worker filter and aggregates the whole table.
SELECT COUNT(*) FROM tasks
WHERE worker_id = sqlc.arg(worker_id)
  AND status IN ('dispatched', 'running');
```

### `GetJobNamesByIDs :many` (in `jobs.sql`)

```sql
-- name: GetJobNamesByIDs :many
-- Job names for one page of tasks. Mirrors GetUserEmailsByIDs; the handler builds a
-- map and reads it per row. Bounded by the page limit, on the jobs primary key.
SELECT id, name FROM jobs WHERE id = ANY($1::uuid[]);
```

### Index

`idx_tasks_worker_active ON tasks(worker_id) WHERE status IN ('dispatched', 'running')`, migration
000018, already exists and covers both new statements' `WHERE` clause exactly. **No migration is
required by this slice.**

The sort is then a top-N over the matched rows, which is bounded in practice by the worker's slot
count. If a deployment ever makes that sort measurable, the change is to widen the index to
`(worker_id, assigned_at DESC)` keeping the same partial predicate - filed as a follow-up if it is
ever measured, not built on speculation.

`sqlc` regeneration: run `make generate` and then apply the repo's CRLF discipline - `git diff
--ignore-all-space`, keep only real content changes, restore the LF-only hunks with
`git checkout --`, and check `git ls-files --eol` reads `i/lf` on every touched path. `git diff`
alone cannot tell you nothing changed.

## Frontend design

### Files

New, under `web/src/workers/`:

- `WorkerTasksPanel.tsx` - the table, its states, and its links. Mounted inside the page's existing
  `Panel`, exactly as `WorkspacesPanel` is.
- `useWorkerTasks.ts` - the query hook.
- `WorkerTasksPanel.test.tsx`, `useWorkerTasks.test.tsx`.

Modified:

- `web/src/workers/api.ts` - `WorkerTask` type, `WorkerTasksPage` type, `listWorkerTasks(id)`.
- `web/src/workers/WorkerDetailPage.tsx` - panel body, Slots KPI, Jobs-today comment pointer.
- `web/src/workers/WorkerDetailPage.test.tsx` - see Acceptance criteria; several existing tests are
  scaffolded on the placeholders and on the absence of a second table.
- `web/src/workers/api.test.ts` - the new client.

### Types

```ts
// web/src/workers/api.ts
import type { TaskStatus } from '../jobs/api'

// One currently-assigned task. Field-for-field the Go workerTaskResponse: the
// embedded taskResponse fields plus job_id, job_name, assigned_at, started_at.
// assignment_epoch is deliberately not on the wire.
export interface WorkerTask {
  id: string
  name: string
  status: TaskStatus
  commands: string[][] | null
  env: Record<string, string> | null
  requires: Record<string, string> | null
  timeout_seconds: number | null
  retries: number
  retry_count: number
  worker_id?: string
  job_id: string
  job_name: string
  assigned_at?: string
  started_at?: string
}

export interface WorkerTasksPage {
  items: WorkerTask[]
  next_cursor: string
  total: number
}

// The worker's currently assigned tasks (dispatched or running), newest assignment
// first. First page only: `total` is the active count, which the Slots KPI renders
// as used slots, so the panel needs no paging control to be correct about it.
export function listWorkerTasks(id: string): Promise<WorkerTasksPage> {
  return apiFetch<WorkerTasksPage>(`/workers/${id}/tasks`)
}
```

The `TaskStatus` and `taskStatusColor` imports from `../jobs/` follow the precedent in
`web/src/schedules/ScheduleRunsPanel.tsx`.

### Hook

```ts
// Polls a worker's currently assigned tasks. Default 3000 matches useWorker: the
// header status and this panel answer the same question at the same timescale, and
// the Slots KPI is composed from both queries. Tests inject a small value.
export function useWorkerTasks(id: string, intervalMs = 3000) {
  return useQuery({
    queryKey: ['worker', id, 'tasks'],
    queryFn: () => listWorkerTasks(id),
    refetchInterval: intervalMs,
    placeholderData: keepPreviousData,
  })
}
```

`keepPreviousData` matches its two siblings. Note the hazard it carries: with a changing route id,
`keepPreviousData` can render worker A's rows under worker B's id for one tick. This panel is
read-only and issues no writes, so the confused-deputy version of that hazard does not apply; the
query key includes `id`, so the stale render is transient and self-corrects.

### Component

```
Panel title="Current tasks" meta="GET /v1/workers/{id}/tasks"
  -> <WorkerTasksPanel workerId={id} />
```

Table configuration:

```ts
const COLS = 'grid-cols-[1fr_1fr_100px_90px_60px]'   // fixed tracks sum to 250
const MIN_W = 'min-w-[560px]'                         // under WorkspacesPanel's measured 600
const HEADERS: TableColumn[] = [
  { label: 'TASK' },
  { label: 'JOB' },
  { label: 'STATUS' },
  { label: 'STARTED' },
  { label: 'RETRY', align: 'right' },
]
```

`<Table label="Current tasks" ...>` so the accessible name matches the visible Panel title, the same
relation `WorkspacesPanel` documents.

Columns:

| Column | Source | Rendering |
|--------|--------|-----------|
| TASK | `name` | `truncate`; links to `/jobs/{job_id}/tasks/{id}`. |
| JOB | `job_name` | `truncate`; links to `/jobs/{job_id}`. |
| STATUS | `status` | Dot plus label, colored by `taskStatusColor(status)`. |
| STARTED | `started_at` | `formatRelativeTime(started_at)`, or the literal `not started` when absent. |
| RETRY | `retry_count`, `retries` | `retry_count/retries` when `retries > 0`, otherwise a single ASCII hyphen. |

Both `fr` cells carry `truncate`, which is the precondition `Table.tsx` states for the min-width
budget to hold.

States, following `WorkspacesPanel`'s structure - the table always renders so its header row is
present, and everything else is a **sibling** of the `role="table"` subtree, never a child:

- **Loading** (`isLoading` and no data): a mono line, `loading tasks...`, matching the page's
  low-key panel-level treatment rather than the page-level `GlassPanel` skeleton.
- **Error**: a bordered `text-err` banner with the message and a Retry control wired to `refetch()`.
  The page-level error card is for the worker query; a panel owns its own error surface.
- **Empty** (`!isLoading && !error && items.length === 0`): `No active tasks.` One message; see
  Decision 14.

### Page changes

1. The "Current tasks" `Panel` keeps its position, first in the left column. Its `meta` becomes the
   route and its body becomes `<WorkerTasksPanel workerId={id} />`. The
   "Backend-blocked / Enabler" comment is deleted.
2. Slots KPI becomes real: its `value` becomes a template string of `used`, a space, a forward
   slash, a space, then `worker.max_slots`, with `progress={{ used, max: worker.max_slots }}`,
   where `used` is `tasks?.total ?? 0`. The em-dash placeholder and its comment go. The progress
   fill must clamp, because `used` can exceed `max_slots` after a `max_slots` reduction (Decision
   9); if `ProgressBar` does not already clamp, the panel clamps before passing the value.
3. The "Jobs today" `KpiStat` is unchanged **except** its comment, which must cite the new
   aggregate follow-up item instead of this one.
4. No change to the page grid, the column split, or any `md:grid-cols-` literal.

## Acceptance criteria

Each maps to one named test and its lane. Lanes: **go default** (`make test`), **go integration**
(`-tags integration -p 1`, real Postgres), **vitest** (`web`, `npm test`).

### Backend, go integration (`internal/api/workers_tasks_integration_test.go`)

| # | Criterion | Test |
|---|-----------|------|
| B1 | Only `dispatched` and `running` rows are returned. Seeds one task per status of the full six-value vocabulary for one worker; asserts exactly two come back. | `TestListWorkerTasks_ReturnsOnlyTheAssignmentPartition` |
| B2 | Rows are scoped to the path worker. Two workers, one active task each; asserts no cross-leak in either direction. | `TestListWorkerTasks_DoesNotLeakAnotherWorkersTasks` |
| B3 | Order is `assigned_at` descending, `id` descending as the tiebreak. Three rows with distinct `assigned_at`, plus two sharing one `assigned_at` to exercise the tiebreak. | `TestListWorkerTasks_OrdersByAssignedAtDescThenIDDesc` |
| B4 | Paging crosses a real page boundary: `limit=1` over three rows, following `next_cursor`, asserting the union is the full set with no duplicate and no skip, and that the final page returns an empty cursor. | `TestListWorkerTasks_PagesAcrossARealBoundary` |
| B5 | `total` is the ACTIVE count, not the worker's total task count and not a table count. Seeds two active and three terminal rows for the worker; asserts `total == 2` on every page. | `TestListWorkerTasks_TotalIsTheActiveCount` |
| B6 | `job_name` is the seeded job's name, and two tasks of the same job both carry it. | `TestListWorkerTasks_CarriesTheJobName` |
| B7 | `assignment_epoch` appears nowhere in the response. Decodes into `map[string]any` and asserts the key is absent from the envelope and from every item. | `TestListWorkerTasks_DoesNotExposeAssignmentEpoch` |
| B8 | A non-admin authenticated user gets 200. This is the posture pin: it goes RED if the route is wrapped in `admin(...)`. | `TestListWorkerTasks_IsReadableByANonAdmin` |
| B9 | No bearer token is 401. | `TestListWorkerTasks_RequiresAuthentication` |
| B10 | Unknown worker id is 404 `worker not found`; a malformed id is 400 `invalid worker id`. | `TestListWorkerTasks_UnknownWorkerIs404AndMalformedIdIs400` |
| B11 | `?limit=0`, `?limit=201` and `?limit=abc` are each 400, not a clamp; `?sort=name` is 400. | `TestListWorkerTasks_RejectsBadLimitAndUnsupportedSort` |
| B12 | A task with `started_at IS NULL` (a `dispatched` row mid-sync) is returned, and its `started_at` key is absent rather than a zero timestamp. | `TestListWorkerTasks_ADispatchedTaskWithNoStartTimeIsReturned` |

### Backend, go default (`internal/api/`)

| # | Criterion | Test |
|---|-----------|------|
| B13 | `workerTaskResponse`'s JSON key set is a strict superset of `taskResponse`'s, computed by reflection over both structs. Goes RED if the embedding is replaced by a hand-written copy that drops a field. | `TestWorkerTaskResponseCarriesEveryTaskResponseField` |
| B14 | `WorkerTasksSortSpec` accepts only `assigned_at` in either direction and rejects every other key, via `parseSort` directly. | `TestWorkerTasksSortSpecAllowsOnlyAssignedAt` |

### Store, go integration (`internal/store/`)

| # | Criterion | Test |
|---|-----------|------|
| S1 | `ListActiveTasksForWorkerPage` returns `running` as well as `dispatched`. Halving the `IN` list to one member must go RED - the exact mutation that stayed green across four suites for `RequeueTaskByID`. | `TestListActiveTasksForWorkerPage_ReturnsBothAssignedStatuses` |
| S2 | `CountActiveTasksForWorker` counts the same partition as the list statement over the same seed. | `TestCountActiveTasksForWorker_MatchesTheListStatement` |
| S3 | The lockstep guard names the two new statements. `TestTasksStatusVocabularyIsExactly`'s comment and failure message go from six partition members to eight, with the read-only/display-only direction recorded and the `idx_tasks_worker_active` coupling noted. The edit is the deliverable; no new test. | `TestTasksStatusVocabularyIsExactly` (existing) |

### Frontend, vitest

| # | Criterion | Test |
|---|-----------|------|
| F1 | `listWorkerTasks` requests `/v1/workers/{id}/tasks` and decodes the envelope. MSW fixture is hand-written JSON, never marshalled through the app's own response type. | `web/src/workers/api.test.ts` - `listWorkerTasks requests the worker tasks route` |
| F2 | The hook polls at 3000 by default, asserted by reading the interval off the mounted observer rather than off an exported constant. | `useWorkerTasks.test.tsx` - `polls at the worker cadence` |
| F3 | Populated: two rows render, the table has the accessible name `Current tasks`, and both cell links point at `/jobs/{job_id}` and `/jobs/{job_id}/tasks/{id}`. | `WorkerTasksPanel.test.tsx` - `renders a row per assigned task with links to the job and the task log` |
| F4 | Empty: `No active tasks.` renders and no data row does. | `WorkerTasksPanel.test.tsx` - `shows an empty state when the worker has no active tasks` |
| F5 | Error: the message and a Retry control render, and Retry refetches. | `WorkerTasksPanel.test.tsx` - `shows the error and a Retry inside the panel` |
| F6 | Loading: the loading line renders before data arrives. | `WorkerTasksPanel.test.tsx` - `shows a loading line before the first page arrives` |
| F7 | A `dispatched` row with no `started_at` renders `not started` and does not render `Invalid Date` or an epoch. | `WorkerTasksPanel.test.tsx` - `renders a dispatched task with no start time` |
| F8 | No fabricated progress. Asserts the panel renders no `progressbar` role and no percent-suffixed text. | `WorkerTasksPanel.test.tsx` - `renders no progress affordance` |
| F9 | The Slots KPI renders the used-over-max fraction from the tasks total, replacing the em-dash placeholder assertion. | `WorkerDetailPage.test.tsx` - `renders the CPU/RAM and Slots KPI cards` (rewritten assertion) |
| F10 | The Jobs-today placeholder still renders and still guards against the hi-fi's literal `47`. | `WorkerDetailPage.test.tsx` - `renders the Jobs-today placeholder KPI with no fabricated data` (assertions unchanged) |

### Test scaffolding that must be REMOVED or REWRITTEN, not left to fail

This is called out separately because it is invisible to a diff that only adds files. Every test in
`web/src/workers/WorkerDetailPage.test.tsx` now needs an MSW handler for the new route, or the panel
renders its error state under every existing test. In addition:

- `renders the current-tasks placeholder note, not an empty table` - asserts
  `no per-worker task feed yet`. Delete; replaced by F3/F4.
- `the current-tasks panel contains no fabricated task rows` - asserts
  `queryByRole('row')` and `queryByRole('table')` are both absent page-wide. Delete; the assertion is
  now false by design. Its intent (no fabricated data) survives as F8.
- `the reservations panel contains no fabricated reservation rows` - asserts
  `getAllByRole('table')` has length 1 with accessible name `Source workspaces` and exactly one row.
  **Rewrite**, do not delete: it is a real guard against the reservations placeholder growing a
  fabricated table. It becomes two tables, identified by accessible name, with the Current-tasks
  table's row count driven by its own fixture.
- `renders the CPU/RAM and Slots KPI cards` - the em-dash placeholder literal becomes the real
  fraction (F9).

### Gates

`make test`, `-tags integration -p 1` over `internal/api` and `internal/store`, the web unit suite,
and the production web build. `-race` in the `golang:1.26` container if the backend half touches
anything concurrent - it does not, so a stated skip with the reason is acceptable here rather than a
substitute. `web/dist` is tracked but not maintained per-PR: restore it with
`git checkout -- web/dist/` before assembling the PR.

## Out of scope

- The "jobs today" activity aggregate in any form. Decision 2.
- Per-task progress of any kind. Decision 8.
- A per-worker task **history** (terminal tasks), and the index and migration it needs. Decision 1.
- Adding `assigned_at` to `taskResponse` itself. It would change the contract of
  `GET /v1/jobs/{id}`, `GET /v1/jobs/{id}/tasks` and `GET /v1/tasks/{id}` for no consumer in this
  slice.
- Exposing `assignment_epoch` anywhere. Decision 7.
- The per-worker reservations panel. It has its own item,
  `feature-2026-06-05-worker-detail-reservations-panel.md`, and its own missing endpoint.
- Any write action. This endpoint and this panel are read-only.
- A browser measurement of the populated panel. Decision 11 states why it is not available and what
  ships instead.
- Any change to `web/e2e/`. `/workers/:id` is not a covered surface there and cannot become one
  without an agent in the harness.

## Backlog items this closes

- `docs/backlog/feature-2026-06-05-worker-detail-activity-panel.md`

Proposed for closure with the aggregate carved out into a new item (Decision 15). The close must run
through `/backlog close`, which moves the file into `docs/backlog/closed/` with `git mv`, stamps the
frontmatter and appends the resolution note. A status flip in place leaves a malformed open item.

Closing it also requires repointing the one comment that cites it and is NOT resolved by this slice:
the "Jobs today" `KpiStat` comment. The Current-tasks and Slots comments are resolved by the slice
itself and are deleted rather than repointed.

## Proposed follow-up backlog items

Intake, not a priority order. The human accepts or rejects each; nothing is auto-filed.

1. **`feature-2026-09-01-worker-activity-aggregate`** - the deferred half of the item this slice
   closes. Needs a decision on whether the KPI counts distinct jobs or tasks (relay assigns tasks to
   workers, never jobs, so the hi-fi's "Jobs today" label is wrong as written), a partial index on
   `tasks(worker_id, finished_at) WHERE status IN ('done','failed','timed_out')` plus its migration,
   a windowed aggregate statement, and the KPI wiring. The `KpiStat label="Jobs today"` comment
   points here.
2. **`feature-2026-09-01-per-worker-task-history`** - terminal tasks for a worker, the widening
   Decision 1 refused. Shares item 1's index. Needs its own UI with real paging, and would extend
   `GET /v1/workers/{id}/tasks` with a status parameter rather than adding a second route.
3. **`idea-2026-09-01-agent-reported-task-progress`** - the enabler for the hi-fi's progress bar.
   A protocol change: a progress field on the agent's status message, a column or a derived store,
   and a decision about what a fraction means for an arbitrary subprocess. Named here so Decision
   8's omission is falsifiable rather than a permanent silence.
4. **`idea-2026-09-01-measure-the-populated-worker-detail-panels`** - the residual from Decision 11.
   `/workers/:id` has under 15px of headroom, this slice adds a table to it, and no lane in the repo
   can measure the populated state. Either depends on
   `idea-2026-08-24-e2e-harness-slice-2-agent-in-harness`, or seeds a `dispatched` task row directly
   in the harness fixtures as a cheaper alternative.
5. **`idea-2026-09-01-jobs-go-hand-written-store-job-copies`** - six partial hand-written
   `store.Job{...}` copies in `internal/api/jobs.go`, each of which silently drops any column `jobs`
   gains. Decision 4 avoided adding a seventh; it did not fix the six. A `NumField` arity guard, or
   a switch to `sqlc.embed`, would close them as a set.
