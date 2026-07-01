# Job cancel actions (graceful + force) - design

Date: 2026-07-01
Status: proposed
Owner: relay-tpm
Backlog: `docs/backlog/feature-2026-06-26-job-actions-submit-cancel-retry.md` (cancel slice)

## Summary

Wire graceful cancel and force-cancel of a job into the job-detail page header.
This is a deliberately narrowed, frontend-only slice of the "Job write-actions:
submit / cancel / retry" backlog item. The backend endpoint already exists and
is unchanged by this work; the slice adds a `JobActions` component into the
reserved, currently-empty actions slot on `JobDetailPage`, backed by a
`useJobActions` mutation hook that mirrors the established `useWorkerActions`
pattern, with confirmation through the shared `ConfirmDialog`.

## Scope

### In scope

- A cancel action and a force-cancel action on the job-detail header, both wired
  to `DELETE /v1/jobs/{id}` (force via `?force=true`).
- Both actions gated behind the same confirmation primitive
  (`web/src/components/ConfirmDialog.tsx`), with distinct copy for graceful vs
  force.
- A `useJobActions(id)` mutation hook following the invalidate-on-success
  strategy of `web/src/workers/useWorkerActions.ts`.
- Permission gating in the UI that matches what the server enforces
  (owner-or-admin).
- Correct cache handling for a cancelled job that remains viewable.

### Out of scope (carved to follow-up backlog items)

- The "+ New job" submit form (`POST /v1/jobs` with a job-spec body). The
  job-spec editor (YAML/form) is a non-trivial surface; the backlog item itself
  states it "may warrant splitting into its own slice." It will be a separate
  backlog item and gets its own spec. Not designed here.
- Retry (`POST /v1/jobs/{id}/retry`). That route does not exist yet
  (backend-blocked). It also ties to the jobs-stats-24h updated_at-proxy bug and
  the retry-resurrects-cancelled-task concern. Not designed here; blocked on a
  backend endpoint first.

## Verified backend contract

Verified against `internal/api/jobs.go` (`handleCancelJob`), the route table in
`internal/api/server.go`, and `internal/api/cancel_signals.go`.

- Route: `DELETE /v1/jobs/{id}`, registered as
  `mux.Handle("DELETE /v1/jobs/{id}", auth(...))`. Any authenticated user may
  call it; the handler applies its own owner-or-admin check.
- Force param: `?force=true`. Parsed with `strconv.ParseBool` on
  `r.URL.Query().Get("force")`; anything not parseable as true is treated as
  false (graceful).
- Graceful vs force semantics: the database effect is identical in both modes.
  The handler cancels every non-terminal task in one statement
  (`CancelJobTasks`, which bumps `assignment_epoch` to fence late agent
  updates), then sets the job status to `cancelled` (`UpdateJobStatus`). The
  ONLY difference is the `force` bool carried in the best-effort `CancelTask`
  signal sent to agents that currently hold a running or dispatched task:
  - graceful (`force=false`): the agent is asked to stop the task; the
    subprocess is given a chance to exit.
  - force (`force=true`): the agent is asked to force-kill the subprocess.
  In both cases the job and its tasks are already marked terminal in the DB
  before the signal fans out. The signal is best-effort (return value ignored);
  a lost signal just means the agent already lost the task.
- Terminal-state guard: if the job's current status is `cancelled` or `done`,
  the handler returns `409 Conflict` ("job is already in a terminal state").
  Note: `failed` is NOT treated as terminal by this guard, so cancelling a
  failed job succeeds and re-marks it `cancelled` (see Edge cases).
- Response codes:
  - `200 OK` with the updated job body on success (a `jobResponse` with
    `status: "cancelled"`; the body carries no task array and no
    `submitted_by_email`, since the handler calls `toJobResponse(job, "", nil, nil)`).
  - `400 Bad Request` on an unparseable job id.
  - `404 Not Found` if the job does not exist, OR if the caller is neither the
    owner nor an admin (existence is hidden from non-owners, matching
    `ownedScheduledJob`).
  - `409 Conflict` if the job is already `cancelled` or `done`.
  - `401 Unauthorized` if there is somehow no user in context.
- Post-cancel visibility: a cancelled job is still returned by
  `GET /v1/jobs/{id}` (unlike worker revoke, which makes the worker 404). The
  detail page stays valid and viewable after cancel.

## Frontend design

### Component placement

`JobDetailPage.tsx` already renders a reserved, empty actions slot in the header:

```
<div data-testid="job-actions" className="ml-auto flex items-center gap-2" />
```

We render a new `<JobActions job={job} />` component into that slot (replacing
the empty div, keeping the `data-testid="job-actions"` and the existing
`ml-auto flex items-center gap-2` classes on the wrapper so header layout is
unchanged). `JobActions` owns the two buttons, the confirm dialog, and the
inline error, mirroring how `WorkerActions` is structured.

### New files

- `web/src/jobs/useJobActions.ts` - the mutation hook.
- `web/src/jobs/JobActions.tsx` - the header action bar.
- Add `cancelJob(id, force)` to `web/src/jobs/api.ts`.

### API client

Add to `web/src/jobs/api.ts`, matching the `revokeWorkerToken` shape (DELETE, no
request body). The server returns a 200 with a body, but the caller does not
need it (the hook invalidates rather than writing the response into cache), so
typing the result as `JobDetail` is fine and mirrors nothing we depend on:

- `cancelJob(id: string, force: boolean): Promise<JobDetail>` calling
  `apiFetch<JobDetail>(\`/jobs/${id}${force ? '?force=true' : ''}\`, { method: 'DELETE' })`.

### Mutation hook (`useJobActions`)

Mirror `useWorkerActions` with the invalidate-on-success strategy. A single
mutation taking `force` as its variable:

- `mutationFn: (force: boolean) => cancelJob(id, force)`.
- `onSuccess`: invalidate THREE keys - `['job', id]`, `['jobs']`, and
  `['job-stats']`.
  - `['job', id]` is invalidated (NOT skipped) because a cancelled job is still
    viewable; this is the opposite of worker revoke, which deliberately skips
    `['worker', id]` because that query 404s post-revoke. The user stays on the
    job-detail page and the status pill flips to `cancelled` on the refetch.
  - `['jobs']` (bare prefix) is invalidated so any list views keyed off
    `['jobs', sort, status, cursor]` (see `web/src/jobs/useJobs.ts`) refresh.
  - `['job-stats']` MUST be invalidated explicitly. The jobs-stats query is
    keyed `['job-stats']` (see `web/src/jobs/useJobStats.ts`), NOT nested under
    `['jobs']`. This decoupling is deliberate and load-bearing: the existing
    regression test `web/src/jobs/queryKeyDecoupling.test.tsx` asserts that
    invalidating `['jobs']` does NOT refetch the stats query. So the bare
    `['jobs']` invalidation alone will not update the KPI strip's running/queued
    counts after a cancel; the separate `['job-stats']` invalidation is required
    to keep those counts honest.
- No optimistic update. `JobDetailPage` already polls `['job', id]` every 3s via
  `useJob`, so the status pill updates within one poll even without an optimistic
  flip; keeping it plain-invalidate matches the majority of `useWorkerActions`
  mutations (only disable/enable there are optimistic) and avoids
  hand-maintaining a `cancelled` status in cache. This is the simplest correct
  choice for this slice.

Return `{ cancel }` where `cancel` is the mutation. The `force` distinction is a
call-site argument (`cancel.mutate(false)` vs `cancel.mutate(true)`), not two
separate mutations, since both share identical cache handling.

### Permission gating

The server gate is owner-or-admin. The SPA has both inputs:

- `useAuth().user` provides `{ id, is_admin }` (see `web/src/lib/types.ts`).
- `GET /v1/jobs/{id}` returns `submitted_by` (the owner's user id) in
  `JobDetail` (see `web/src/jobs/api.ts`).

Render `<JobActions>` only when `user.is_admin || job.submitted_by === user.id`.
This mirrors `WorkerDetailPage`, which conditionally renders `<WorkerActions>`
behind `user?.is_admin`. Gating in the UI is a usability affordance, not the
security boundary; the server enforces the real check and returns 404 to
unauthorized callers regardless. Keeping the client gate aligned avoids showing
buttons that would 404.

### Action bar layout and copy

Two buttons in the header slot, both opening `ConfirmDialog` (no immediate
mutation on click), following `WorkerActions`:

- "Cancel" - neutral/destructive styling consistent with the worker Disable/Drain
  buttons.
- "Force cancel" - error-accent styling consistent with the worker Revoke button
  (`border-err/50 bg-err/10 text-err`), since force-kill is the more disruptive
  action.

Both buttons are `disabled` while `cancel.isPending`. Both are hidden entirely
when the job is already terminal (see Edge cases) so the user is never offered an
action the server will 409.

Confirm copy (a `Pending` union of `'cancel' | 'force'`, same shape as
`WorkerActions`' `confirmCopy` record):

- cancel:
  - title: `Cancel ${job.name}?`
  - body: "Running tasks are asked to stop and the job is marked cancelled.
    Tasks that have not started are dropped."
  - label: "Cancel job"
  - destructive: true
- force:
  - title: `Force cancel ${job.name}?`
  - body: "Running tasks are force-killed immediately and the job is marked
    cancelled. Use this when a graceful cancel is not stopping the work."
  - label: "Force cancel"
  - destructive: true

Note the confirm dialog's own dismiss button is labelled "Cancel" (the dialog
primitive). To avoid "Cancel / Cancel job" ambiguity in the cancel dialog, the
primary action label is "Cancel job", not "Cancel". The dialog's dismiss stays
"Cancel" (unchanged primitive).

### Error handling

Mirror `WorkerActions`: surface `cancel.error` as an inline error banner
(`rounded-card border border-err/40 ... text-err`) below the buttons. A 409
(terminal state, e.g. a concurrent cancel landed between poll and click) shows
the server message. On error the dialog closes and the banner appears; the user
can retry or reload. `cancel.reset()` is called when re-opening a dialog to clear
a stale error, matching the worker enable path.

## States and edge cases

- Already-terminal job (`cancelled` or `done`): both action buttons are hidden.
  Detection uses `job.status`. This keeps the UI from offering an action the
  server rejects with 409. A job that becomes terminal via the background poll
  while the page is open simply loses its buttons on the next render.
- Failed job: the server does NOT treat `failed` as terminal for cancel, so
  cancelling a failed job succeeds (200) and flips it to `cancelled`. Decision:
  treat only `cancelled` and `done` as "hide the buttons" states, matching the
  server's guard exactly, so the buttons remain available on a `failed` job.
  This is the least surprising behavior (client and server agree on what is
  cancellable) and needs no extra client-side status list to maintain.
- Non-owner, non-admin viewer: `<JobActions>` is not rendered at all. Even if it
  were, the server returns 404.
- Concurrent cancel / race with the 3s poll: if two clients cancel, the second
  gets 409; the banner shows the message and the poll converges the status pill.
- No running tasks (all pending): cancel still succeeds; there are simply no
  agent signals to send. Graceful and force are indistinguishable in this case.
- Dialog dismissal (Escape, Cancel button, backdrop is not clickable in the
  primitive): closes without mutating, per the `ConfirmDialog` contract.

## Test plan (Vitest + Testing Library + MSW)

Add `web/src/jobs/JobActions.test.tsx` and extend the API client test. The
existing `JobDetailPage.test.tsx` already sets `submitted_by: 'u1'` with the
current user `id: 'u1'` and `is_admin: false`, so the owner path is testable with
existing fixtures.

1. Graceful cancel: render as the owner on a running job, click "Cancel",
   confirm in the dialog; assert `DELETE /v1/jobs/{id}` was called WITHOUT
   `?force=true` (assert the request URL has no `force` param). Assert the
   confirm dialog closed.
2. Force cancel: click "Force cancel", confirm; assert `DELETE /v1/jobs/{id}`
   was called WITH `?force=true` (assert `force=true` in the request URL). This
   distinguishes the two actions - the ONLY observable difference is the query
   param.
3. Confirm-then-dismiss (dialog cancel): open either dialog, dismiss via the
   dialog's Cancel button (and separately via Escape); assert NO DELETE request
   was made.
4. Gating - non-owner non-admin: render with a current user whose id differs
   from `submitted_by` and `is_admin: false`; assert the `job-actions` slot has
   no cancel/force buttons.
5. Gating - admin non-owner: render with `is_admin: true` and a non-matching id;
   assert both buttons are present (admin override).
6. Gating - owner non-admin: the existing fixture (`u1` owner, non-admin);
   assert both buttons are present.
7. Terminal job hides buttons: render a `done` job and a `cancelled` job; assert
   no cancel/force buttons in either. Render a `failed` job; assert the buttons
   ARE present (matches server behavior).
8. Cache invalidation: after a successful cancel, assert `['job', id]`,
   `['jobs']`, AND `['job-stats']` are all invalidated (e.g. spy on
   `invalidateQueries` and assert all three keys, or assert a re-fetch of
   `GET /v1/jobs/{id}` occurs and that the stats query refetches). Include an
   explicit assertion that `['job-stats']` refetches, since the
   `queryKeyDecoupling` design means the `['jobs']` invalidation alone would not
   trigger it - this test is what catches a regression to only-two-key
   invalidation. The user stays on the detail page (no navigation), unlike
   worker revoke.
9. Error surfacing: mock the DELETE to return 409; confirm the action; assert
   the inline error banner shows the server message and no navigation occurs.

## Invariants and system-design lens

- Epoch fence: the server's `CancelJobTasks` bumps `assignment_epoch`; nothing in
  this frontend slice touches task status or epochs. No invariant surface is
  added.
- One bounded sender per stream: agent cancel signals go through the existing
  best-effort `sendCancelSignals` fan-out; unchanged and untouched.
- Threat model: the action is owner-or-admin gated server-side; the client gate
  is cosmetic. No new authz path is introduced. Force vs graceful is an
  authenticated capability difference only in agent-signal aggressiveness, not in
  who may call it.
- Load/failure mode: the mutation is a single DELETE; on failure the UI shows an
  error and the 3s poll keeps the displayed status honest. No optimistic state to
  reconcile.

## Open decisions

1. Button labels/placement: two separate header buttons ("Cancel" and "Force
   cancel") vs a single "Cancel" button whose dialog offers a "force" checkbox.
   This spec proposes two buttons to match the flat `WorkerActions` bar and make
   the force path an explicit, higher-friction choice. A checkbox variant is
   viable if the header gets crowded.
2. Owner-gate trust source: the client gate uses `job.submitted_by === user.id`.
   The `submitted_by` field is present on `JobDetail` and reliable, so this is
   settled for the common case. The only residual: a job whose owner account was
   later changed is not a scenario relay supports, so no special handling.
