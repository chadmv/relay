# Schedule Detail Page and Edit Action Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `/schedules/:id` detail page that reads a schedule, edits its cron / timezone / overlap policy inline through a **changed-fields-only** PATCH, shows the server's next fire and the latest 20 runs, deletes behind a confirm dialog, and is reached from two new links on the Schedules list.

**Architecture:** Five new files under `web/src/schedules/` (page, inline trigger form, runs panel, two query hooks) plus four surgical edits to shipped modules (`schedules/api.ts`, `schedules/useScheduleActions.ts`, `schedules/SchedulesTable.tsx`, `jobs/api.ts`) and one new route. Every endpoint already exists and is already owner-or-admin. The single highest-risk behaviour in the slice is that `PATCH` recomputes `next_run_at` from `time.Now()` whenever the body merely *carries* a cron or timezone key, so the patch body is built from a diff and Task 8 exists solely to regression-test that.

**Tech Stack:** React 18, TypeScript, TanStack Query v5, react-router-dom v7, Tailwind v4 (Holo tokens), Vitest + Testing Library + user-event + MSW, jsdom.

**Spec:** `docs/superpowers/specs/2026-08-12-schedule-detail-page.md` (approved; do not reopen its decisions)

**Backlog item closed by this slice:** `docs/backlog/idea-2026-06-05-schedule-detail-page.md` (close it with `/backlog close idea-2026-06-05-schedule-detail-page`, which `git mv`s it into `docs/backlog/closed/`; never hand-edit `status:`).

---

## Slice independence declaration

- **Backend slice: NONE. Zero Go files change. Zero `.sql` files change, therefore no `make generate`,** and no `*.sql.go` / `models.go` involvement, no migration. I re-read every backend claim in the spec (see "Verified backend surface" below) and all of them hold; none required a code change to make the frontend work. None of the six Invariants in CLAUDE.md is in play server-side. Three frontend analogues **do** apply and are called out per task: every request goes through `apiFetch` (`web/src/lib/api.ts:29`), a poll must not clobber a dirty form, and a settled mutation must not write into state a later action already replaced.
- **Frontend slice: ONE ENGINEER, SEQUENTIAL.** Do not split these tasks across two engineers and do not run any of them in parallel. The dependency chain is linear: Task 2 imports Task 1, Task 4 imports Task 1, Task 7 imports Tasks 1-6, Tasks 8 and 9 are regression tests over Task 7, Task 10 edits two shipped test files that Task 7's route must already exist for. Tasks 1, 4 and 10 all write into shipped shared modules in `web/src/schedules/`; concurrent writers there have burned this project before.
- **Parallelism available to the conductor for Phase 3: none within this plan.** Unrelated work elsewhere in the repo can run alongside it.

---

## Verified backend surface (re-verified against the tree; do not trust the spec alone)

Read: `internal/api/scheduled_jobs.go:147-169`, `:508-519`, `:521-613`, `:615-634`, `:636-679`; `internal/api/jobs.go:409-454`; `internal/api/server.go:163-168`; `internal/store/query/jobs.sql:60-81`; `internal/schedrunner/cron.go:14-61`.

| Claim | Verdict | Evidence |
|---|---|---|
| All four `/v1/scheduled-jobs/{id}*` routes are `auth(...)`, none `AdminOnly` | Confirmed | `server.go:163-168` |
| Owner-or-admin, **404 on deny** (hidden, not refused), same 404 as a missing row | Confirmed | `ownedScheduledJob`, `scheduled_jobs.go:147-169` |
| `GET /v1/scheduled-jobs/{id}` never calls `fillOwnerEmails`, so `owner_email` is always `""` | Confirmed | `:508-519` writes `toScheduledJobResponse(row)` directly; both list arms call `s.fillOwnerEmails` (`:371`, `:504`) |
| `PATCH` accepts six pointer fields; an omitted key means "leave alone" | Confirmed | `patchScheduledJobRequest` `:521-528`; the six `if req.X != nil` merges at `:546-582` |
| `overlap_policy` accepts **only** `skip` or `allow`; anything else 400s | Confirmed | `:561-564` |
| **`next_run_at` is recomputed from `time.Now()` whenever the body CARRIES `cron_expr` or `timezone`, changed or not** | Confirmed, and this is the defect driver | `:585` `if req.CronExpr != nil \|\| req.Timezone != nil \|\| (req.Enabled != nil && *req.Enabled && !row.Enabled)` then `:595` `sched.Next(time.Now())` |
| `PATCH` returns 200 with the full updated row including the recomputed `next_run_at` | Confirmed | `:598-612` |
| `DELETE` is 204 with no body | Confirmed | `:633` |
| `POST .../run-now` is owner-or-admin (not admin-only) and attributes the job to the schedule owner | Confirmed | `:642`, `:661-666` |
| `GET /v1/jobs?scheduled_job_id=` exists, auth-gated **before** pagination | Confirmed | `jobs.go:424-454`, auth at `:431-434` with the comment saying exactly this |
| `?sort=` combined with any filter is a hard **400** | Confirmed | `jobs.go:417-422`; `hasFilter` includes `scheduled_job_id` |
| Runs come back newest-first | Confirmed | `ListJobsByScheduledJobWithEmailPage`, `internal/store/query/jobs.sql:77` orders `j.created_at DESC, j.id DESC` |
| No cron parser, no YAML lib, no date lib in `web/` | Confirmed | `web/package.json:13-20` is exactly six runtime deps |

**Nullability, which the client types must match exactly:**

- `last_run_at` and `last_job_id` carry `omitempty`, so when NULL **the key is absent**, not `null`. TS: `last_run_at?: string`, `last_job_id?: string` - already correct in the shipped `Schedule` interface (`web/src/schedules/api.ts:5-20`), reused unchanged.
- `owner_email` has **no** `omitempty` and is always present and always `""` on the detail endpoint.
- `next_run_at` is `NOT NULL` and always present.
- `job_spec` is opaque JSON; the client keeps it `unknown`.
- On the runs rows, `started_at` / `finished_at` keys are **absent** when the job has no started/finished task. The shipped `Job` type (`web/src/jobs/api.ts:5-20`) already models this.
- Timestamps are Go `time.Time`, RFC3339 with nanoseconds. Parse with `new Date()`; never string-compare.

---

## Existing precedent for every new artifact

"Mirror X at `file:line`" is a literal instruction: copy the shape, change the nouns. Read each before writing the file that mirrors it.

| New artifact | Shipped file it mirrors |
|---|---|
| `ScheduleDetailPage.tsx` | `web/src/workers/WorkerDetailPage.tsx:23-93` - loading/404/error triad, breadcrumb + name + pill + `ml-auto` action bar, mono identity sub-line, `grid grid-cols-2 gap-3` body of `Panel`s |
| `ScheduleTriggerForm.tsx` | `web/src/workers/WorkerEditForm.tsx:17-46`, especially the changed-fields patch construction at `:42-45` |
| `ScheduleRunsPanel.tsx` | `web/src/jobs/JobsTable.tsx:30-81` - `Table`/`TableRow`/`TableCell` with the footer rendered **outside** the `role="table"` subtree |
| `useSchedule.ts` | `web/src/workers/useWorker.ts:6-13` (single-resource polled query, injectable interval) |
| `useScheduleRuns.ts` | `web/src/schedules/useSchedules.ts:8-15` (10s interval, `keepPreviousData`) |
| `listJobsBySchedule` in `jobs/api.ts` | sibling of `listJobs` at `web/src/jobs/api.ts:50-56` |
| `update` / `remove` mutations | `web/src/schedules/useScheduleActions.ts:9-17` (bare-prefix invalidation, no optimistic write) |
| Delete behind a confirm | `web/src/components/ConfirmDialog.tsx:17-61` - **reuse as-is, do not modify it** |
| NAME cell link, `Edit` action link | `web/src/jobs/JobsTable.tsx:46`, `web/src/workers/WorkersTable.tsx:46`; row identity in `aria-label` per `web/src/admin/users/UsersTable.tsx:169-199` |
| Local 1s clock, zero requests | `web/src/lib/useNow.ts:8-15` |
| Read-only JSON as mono text | `web/src/jobs/NewJobPage.tsx:51-59` (textarea there; a `<pre>` here) |
| Absent-value placeholder | plain ASCII hyphen `-`, as `web/src/schedules/format.ts:16-19` and `SchedulesTable.tsx:69` already do |

Holo primitives available and sufficient - **no new primitive is needed**: `GlassPanel, Eyebrow, ProgressBar, Chip, PillButton, KpiStat, Panel, StatusDot, Table, TableRow, TableCell, ariaSort, sortCaret` (`web/src/components/holo/index.ts:3-12`).

### THIRD-CONSUMER FLAG (read this before Task 7)

The project rule is *extract a shared primitive before its third consumer*. The **detail-page state triad** - `if (isLoading && !data) return <GlassPanel className="h-40" />` followed by a 404-vs-retryable error card with a back link - is already duplicated **verbatim in two shipped files**:

- `web/src/workers/WorkerDetailPage.tsx:30-55`
- `web/src/jobs/JobDetailPage.tsx:57-78`

`ScheduleDetailPage` is the **third consumer**. This plan deliberately ships the third copy rather than extracting the primitive, for two stated reasons: (1) the extraction would have to migrate two shipped pages and gate on a byte-identical-test refactor, which is its own slice with its own risk profile; (2) the spec's acceptance criterion 13 confines this change set to `web/src/schedules/`, `web/src/jobs/api.ts` and `web/src/app/router.tsx`, and silently widening it would put the whole slice behind an unrelated refactor. **This is a recorded deviation from the house rule, not an oversight.** Task 7 requires a code comment at the triad naming the enabler, and the Phase 6 proposal list grows from three items to four:

- `idea-2026-08-12-detail-page-state-triad-primitive.md` - extract the loading / not-found / retryable-error triad into a shared component and migrate `WorkerDetailPage`, `JobDetailPage` and `ScheduleDetailPage` onto it, gated on a zero-line diff to the three pages' existing test files.

No other new artifact in this slice is a third consumer of a non-primitive pattern. The inline-edit-form-with-diff pattern gets its **second** consumer (`WorkerEditForm` is the first).

---

## Test-environment constraints (pin these; they have bitten this repo before)

- **Runner:** vitest 2.1 + jsdom 29 + `@testing-library/react` 16 + `user-event` 14. MSW 2.7 with `onUnhandledRequest: 'error'` (`web/src/test/setup.ts:5`) - **every endpoint a test touches needs a handler or the test errors**.
- **`renderWithQuery` does NOT provide a router** (`web/src/test/renderWithQuery.tsx:7-12`). Any component rendering a react-router `<Link>` needs an explicit `MemoryRouter`, or you get `useHref() may be used only in the context of a <Router>`. This is why Task 10 must edit two shipped test files.
- **A TanStack `invalidateQueries` test needs an ACTIVE OBSERVER.** Mount the query with `renderHook`; a `client.fetchQuery` / `setQueryData` seed leaves no observer, `invalidateQueries`' default `refetchType: 'active'` never fires, and the assertion passes vacuously no matter what key was invalidated. Cited in Task 4.
- **Dialogs:** `inert` and focus-trap libraries are unusable in this jsdom, and native `<dialog>` is not viable. This slice therefore **reuses `ConfirmDialog`/`DialogShell` unmodified** and adds no new modal machinery. Cited in Task 7.
- **Fake timers + user-event:** use the shipped idiom - `vi.useFakeTimers({ shouldAdvanceTime: true })`, `const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })`, advance inside `act(...)`, and `afterEach(() => vi.useRealTimers())`. Precedent: `web/src/admin/reservations/ReservationsTab.test.tsx:6`, `:57`, `:426-444` and `web/src/admin/enrollments/EnrollmentsTab.test.tsx:226-256`.
- **Plan-supplied test bodies are guesses until run RED.** Every step below states the expected failure text. If a test goes green before the implementation exists, it is vacuous - fix the test, do not proceed.

---

## Conventions for every task

- All commands run from the `web/` directory of the worktree: `D:/dev/relay/.claude/worktrees/pr-merge-session-f5796e/web`.
- Single file: `npx vitest run src/<path>.test.tsx`. Full suite: `npm test`.
- TDD per step: write the failing test, run it and watch it fail with the stated message, implement, run it and watch it pass, commit.
- House rule: **never an em dash or en dash**, in code, comments, copy or this document. Placeholders are the plain ASCII hyphen `-`.
- Never reformat code you were not asked to change. Never edit a shipped test's assertions to make new code pass - an assertion needing adjustment IS the finding.

---

## Scope guard - do NOT build

- **No cron parser, no cron explainer, no multi-entry next-fires preview.** `web/` has no cron dependency and must not gain one. The Next fire panel shows exactly one value, the server's `next_run_at`.
- **No client-side cron or timezone validation.** The server is the validator of record; its 400 message is rendered verbatim. A client pre-check is a second cron implementation by another name.
- **No `queue` overlap option.** The server accepts only `skip` and `allow`; a `queue` button would always 400.
- **No `name` editing, no `job_spec` editing, no YAML.** The Job spec panel is a read-only `<pre>`.
- **No pager, no cursor stack, no sort control on Recent runs.** Fixed latest-20 window; `?sort=` must never be sent.
- **No owner fallback to `owner_id`.** Omit the owner line entirely while `owner_email` is empty.
- **No delete control on the Schedules list.** One destructive control, on the detail page.
- **No edits to `ConfirmDialog`, `DialogShell`, `Table`, `Panel`, `Chip`, `PillButton` or any other shared primitive.**
- **No edits to `listJobs`.** Its sort/status branching is exactly what the runs call must not do.
- **No optimistic cache writes.** Mutations invalidate; they never `setQueryData` and never push a response into form state.

---

## File Structure

**New files** (all under `web/src/schedules/`)

| File | Responsibility |
|---|---|
| `useSchedule.ts` | `useQuery(['schedules','detail',id])`, 10s poll, `keepPreviousData`. |
| `useSchedule.test.tsx` | Key shape, request path, poll cadence proven with fake timers. |
| `useScheduleRuns.ts` | `useQuery(['schedules','runs',id])`, 10s poll, `SCHEDULE_RUNS_LIMIT = 20`. |
| `useScheduleRuns.test.tsx` | Key shape, `limit=20`, `sort` absent, poll cadence. |
| `ScheduleTriggerForm.tsx` | Inline cron/tz/overlap form. Seeds draft once; emits a **changed-fields-only** `SchedulePatch`; no-ops when clean. |
| `ScheduleTriggerForm.test.tsx` | The diff, both directions; the two overlap options and the absence of a third; no client validation; server error rendered verbatim. |
| `ScheduleRunsPanel.tsx` | Latest-20 table, `STARTED / DUR / STATUS / JOB ID / OWNER`, `latest N of <total>` footer, empty state. |
| `ScheduleRunsPanel.test.tsx` | Columns, absent `started_at`/`finished_at`, job id link target, footer, empty state, table roles. |
| `ScheduleDetailPage.tsx` | Route component: triad, header + action bar, identity line, two-column body, delete behind `ConfirmDialog`. |
| `ScheduleDetailPage.test.tsx` | Triad, header, owner omission both directions, next fire, delete flow, error placement. |
| `ScheduleDetailPage.lifecycle.test.tsx` | The two lifecycle regressions (Task 9), kept in their own file so they are findable. |

**Modified files**

| File | Change |
|---|---|
| `web/src/schedules/api.ts` | Append `getSchedule`, `SchedulePatch`, `updateSchedule`, `deleteSchedule`; re-express `setScheduleEnabled` (`:51-53`) through `updateSchedule` keeping its exported signature byte-identical. |
| `web/src/schedules/api.test.ts` | Append tests. The import statement at `:5` grows to name the new functions; **no existing test body or assertion may change**. |
| `web/src/schedules/useScheduleActions.ts` | Append `update` and `remove`. The two existing mutations are untouched. |
| `web/src/schedules/useScheduleActions.test.tsx` | Append tests. **Zero edits to the two existing tests.** |
| `web/src/jobs/api.ts` | Append `listJobsBySchedule` next to `listJobs` (`:50-56`). `listJobs` itself is untouched. |
| `web/src/jobs/api.test.ts` | Append one test. The shipped `listJobs` tests at `:20-44` stay byte-identical and **are** the regression guard. |
| `web/src/schedules/SchedulesTable.tsx:53-56`, `:72-89` | NAME becomes a `<Link>`; an `Edit` `<Link>` joins the ACTIONS cell. |
| `web/src/schedules/SchedulesTable.test.tsx` | Wrapper-only: each `render(...)` gains a `MemoryRouter`. **No assertion may change.** Plus new link tests. |
| `web/src/schedules/SchedulesPage.test.tsx` | Wrapper-only: each `renderWithQuery(<SchedulesPage />)` becomes `renderWithQuery(<MemoryRouter><SchedulesPage /></MemoryRouter>)`. **No assertion may change.** |
| `web/src/app/router.tsx:31` | One new route after `/schedules`. |

**Reused unchanged:** `GlassPanel`, `Panel`, `Chip`, `PillButton`, `Button`, `Field`, `Input`, `Table`/`TableRow`/`TableCell`, `ConfirmDialog`, `apiFetch`, `ApiError`, `useNow`, `formatRelativeTime`, `nextRunDisplay`, `shortId`, `statusColor`, `formatDuration`, `formatStarted`, the `Schedule` and `Job` types.

---

## Task 1: Schedule detail / update / delete API clients

The contract lives here, including the comment that explains why a patch is a diff. Mirror the shape of the shipped functions at `web/src/schedules/api.ts:39-53`.

**Files:**
- Modify: `web/src/schedules/api.ts` (append; re-express `:51-53`)
- Test: `web/src/schedules/api.test.ts` (append; import line at `:5` grows)

- [ ] **Step 1: Write the failing tests**

Change the import at `web/src/schedules/api.test.ts:5` to:

```ts
import {
  deleteSchedule,
  getSchedule,
  listSchedules,
  runScheduleNow,
  setScheduleEnabled,
  updateSchedule,
  type SchedulesPage,
} from './api'
```

Append to the same file:

```ts
const ROW = {
  id: 's1',
  name: 'nightly-build',
  owner_id: 'o1',
  owner_email: '',
  cron_expr: '0 2 * * *',
  timezone: 'UTC',
  job_spec: { name: 'nightly-build', tasks: [] },
  overlap_policy: 'skip',
  enabled: true,
  next_run_at: '2099-01-01T00:00:00Z',
  created_at: '2026-06-01T00:00:00Z',
  updated_at: '2026-06-05T11:00:00Z',
}

test('getSchedule GETs the id path and parses the row', async () => {
  let path: string | undefined
  server.use(
    http.get('/v1/scheduled-jobs/s1', ({ request }) => {
      path = new URL(request.url).pathname
      return HttpResponse.json(ROW)
    }),
  )
  const s = await getSchedule('s1')
  expect(path).toBe('/v1/scheduled-jobs/s1')
  expect(s.name).toBe('nightly-build')
  expect(s.cron_expr).toBe('0 2 * * *')
  // ALWAYS present and ALWAYS "" on THIS endpoint: handleGetScheduledJob never
  // calls fillOwnerEmails (internal/api/scheduled_jobs.go:508-519, unlike both
  // list arms at :371 and :504) and OwnerEmail has no omitempty (:25).
  expect(s.owner_email).toBe('')
  // last_run_at / last_job_id carry omitempty, so the KEY IS ABSENT when NULL -
  // not null. Consumers must handle undefined, never `=== null`.
  expect('last_run_at' in s).toBe(false)
  expect('last_job_id' in s).toBe(false)
  // Positive control on the same instrument: a key that is always present, so the
  // two absence assertions above are about omitempty and not about a dead `in`.
  expect('next_run_at' in s).toBe(true)
})

test('getSchedule surfaces the owner-or-admin deny as ApiError(404)', async () => {
  // ownedScheduledJob 404s a non-owner non-admin exactly as it 404s a missing row
  // (internal/api/scheduled_jobs.go:147-169): the resource is hidden, not refused.
  // The client cannot and must not try to distinguish the two.
  server.use(
    http.get('/v1/scheduled-jobs/nope', () =>
      HttpResponse.json({ error: 'scheduled job not found' }, { status: 404 }),
    ),
  )
  const err = await getSchedule('nope').catch((e) => e)
  expect(err).toBeInstanceOf(ApiError)
  expect(err).toMatchObject({ status: 404, code: 'scheduled job not found' })
})

test('updateSchedule PATCHes exactly the keys it is given and NO others', async () => {
  let body: Record<string, unknown> | undefined
  let method: string | undefined
  server.use(
    http.patch('/v1/scheduled-jobs/s1', async ({ request }) => {
      method = request.method
      body = (await request.json()) as Record<string, unknown>
      return HttpResponse.json({ ...ROW, timezone: 'Europe/Berlin' })
    }),
  )
  await updateSchedule('s1', { timezone: 'Europe/Berlin' })
  expect(method).toBe('PATCH')
  expect(body).toEqual({ timezone: 'Europe/Berlin' })
  // The absence assertion that matters: a body CARRYING cron_expr recomputes
  // next_run_at from time.Now() even when the value is unchanged
  // (internal/api/scheduled_jobs.go:585, :595).
  expect('cron_expr' in body!).toBe(false)
  expect('enabled' in body!).toBe(false)
})

test('updateSchedule passes cron_expr through when it IS given (positive control)', async () => {
  // Without this, the previous test passes against a client that always sends {}.
  let body: Record<string, unknown> | undefined
  server.use(
    http.patch('/v1/scheduled-jobs/s1', async ({ request }) => {
      body = (await request.json()) as Record<string, unknown>
      return HttpResponse.json(ROW)
    }),
  )
  await updateSchedule('s1', { cron_expr: '@every 30m', overlap_policy: 'allow' })
  expect(body).toEqual({ cron_expr: '@every 30m', overlap_policy: 'allow' })
})

test('updateSchedule surfaces the server 400 message verbatim', async () => {
  // There is no client-side cron validation by design, so this message is the
  // ONLY feedback a bad cron produces. It comes from internal/schedrunner/cron.go:39.
  server.use(
    http.patch('/v1/scheduled-jobs/s1', () =>
      HttpResponse.json(
        { error: 'invalid cron expression "nope": expected exactly 5 fields, found 1: [nope]' },
        { status: 400 },
      ),
    ),
  )
  const err = await updateSchedule('s1', { cron_expr: 'nope' }).catch((e) => e)
  expect(err).toBeInstanceOf(ApiError)
  expect(err.status).toBe(400)
  expect(err.message).toContain('invalid cron expression')
})

test('deleteSchedule DELETEs the id path and tolerates the empty 204', async () => {
  let method: string | undefined
  let path: string | undefined
  server.use(
    http.delete('/v1/scheduled-jobs/s1', ({ request }) => {
      method = request.method
      path = new URL(request.url).pathname
      // A real 204 has NO body at all (internal/api/scheduled_jobs.go:633). A client
      // that unconditionally calls res.json() throws 'Unexpected end of JSON input'.
      return new HttpResponse(null, { status: 204 })
    }),
  )
  await expect(deleteSchedule('s1')).resolves.toBeUndefined()
  expect(method).toBe('DELETE')
  expect(path).toBe('/v1/scheduled-jobs/s1')
})

test('setScheduleEnabled still sends EXACTLY { enabled } after the re-expression', async () => {
  let body: Record<string, unknown> | undefined
  server.use(
    http.patch('/v1/scheduled-jobs/s1', async ({ request }) => {
      body = (await request.json()) as Record<string, unknown>
      return HttpResponse.json({ ...ROW, enabled: false })
    }),
  )
  await setScheduleEnabled('s1', false)
  // Routing this through updateSchedule must not smuggle extra keys in: a
  // cron_expr or timezone here would recompute next_run_at on every single
  // Enable/Disable click (internal/api/scheduled_jobs.go:585).
  expect(body).toEqual({ enabled: false })
})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `npx vitest run src/schedules/api.test.ts`

Expected: FAIL at import time - `No matching export in "src/schedules/api.ts" for import "getSchedule"` (esbuild/Vite transform error naming `getSchedule`, `updateSchedule` and `deleteSchedule`). The whole file fails to load, which is the correct RED for a missing export.

- [ ] **Step 3: Implement**

Append to `web/src/schedules/api.ts`, and replace the existing `setScheduleEnabled` at `:51-53`:

```ts
// One scheduled job. NOTE the asymmetry with the list endpoint: handleGetScheduledJob
// (internal/api/scheduled_jobs.go:508-519) never calls fillOwnerEmails, unlike both
// list arms (:371, :504), and OwnerEmail has no omitempty (:25) - so `owner_email` is
// ALWAYS present and ALWAYS "" here. Callers must omit the owner rather than render an
// empty label, and must not substitute owner_id (36 opaque characters).
//
// Rejects with ApiError(404) both for a missing row and for a non-owner non-admin:
// ownedScheduledJob hides rather than refuses (:147-169). The two are indistinguishable
// on the wire by design; do not try to tell them apart.
export function getSchedule(id: string): Promise<Schedule> {
  return apiFetch<Schedule>(`/scheduled-jobs/${id}`)
}

// Every field optional. An OMITTED key means "leave alone" server-side, because
// patchScheduledJobRequest is all pointers (internal/api/scheduled_jobs.go:521-528).
//
// SENDING A KEY YOU DID NOT CHANGE IS NOT A NO-OP. next_run_at is recomputed from
// time.Now() whenever the body merely CARRIES cron_expr or timezone, changed or not
// (:585, :595). Re-sending an unchanged cron on an `@every 1h` schedule whose next
// fire is five minutes away pushes that fire out by 55 minutes. Always build this
// from a diff against the loaded row, never from the whole form.
export interface SchedulePatch {
  name?: string
  cron_expr?: string
  timezone?: string
  overlap_policy?: string
  enabled?: boolean
  job_spec?: unknown
}

// 200 with the full updated row, including the recomputed next_run_at (:598-612).
// Concurrent edits are last-writer-wins: UpdateScheduledJob is a bare WHERE id = $1
// (internal/store/query/scheduled_jobs.sql:32-43) with no version column and there is
// no 409. The changed-fields-only body narrows the overlap to fields actually touched.
export function updateSchedule(id: string, patch: SchedulePatch): Promise<Schedule> {
  return apiFetch<Schedule>(`/scheduled-jobs/${id}`, { method: 'PATCH', json: patch })
}

// 204 with no body (internal/api/scheduled_jobs.go:633); apiFetch returns undefined for
// 204 (lib/api.ts:57), so no special handling is needed here.
//
// What it does to history: jobs.scheduled_job_id is ON DELETE SET NULL
// (internal/store/migrations/000006_scheduled_jobs.up.sql:20-21), so jobs the schedule
// already produced SURVIVE but are unlinked from it - the run history becomes
// unreachable. A run already in flight is not cancelled. That is the confirm copy.
export function deleteSchedule(id: string): Promise<void> {
  return apiFetch<void>(`/scheduled-jobs/${id}`, { method: 'DELETE' })
}
```

Replace `web/src/schedules/api.ts:50-53` with:

```ts
// Toggles the enabled flag via PATCH. Expressed through updateSchedule so there is one
// PATCH client; the exported signature is byte-identical, so no call site and no
// existing test moved. It sends ONLY { enabled }: adding cron_expr or timezone here
// would recompute next_run_at on every toggle. Note the server recomputes anyway on a
// disabled -> enabled transition (internal/api/scheduled_jobs.go:585), which is the
// intended never-catch-up semantic.
export function setScheduleEnabled(id: string, enabled: boolean): Promise<Schedule> {
  return updateSchedule(id, { enabled })
}
```

- [ ] **Step 4: Run the tests to verify they pass, including the untouched shipped tests**

Run: `npx vitest run src/schedules/api.test.ts`

Expected: PASS, 13 tests (6 shipped + 7 new). If the shipped `setScheduleEnabled PATCHes the enabled flag` test at `:72-82` needed any edit, the re-expression was not signature-preserving - fix the implementation, not the test.

- [ ] **Step 5: Commit**

```bash
git add web/src/schedules/api.ts web/src/schedules/api.test.ts
git commit -m "feat(web): schedule detail, update and delete API clients"
```

---

## Task 2: useSchedule detail query

Mirror `web/src/workers/useWorker.ts:6-13`. The cadence assertion is behavioral on purpose: asserting an exported constant would prove nothing about the code consuming it.

**Files:**
- Create: `web/src/schedules/useSchedule.ts`
- Test: `web/src/schedules/useSchedule.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/schedules/useSchedule.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { afterEach, expect, test, vi } from 'vitest'
import { server } from '../test/setup-helpers'
import { useSchedule } from './useSchedule'
import type { Schedule } from './api'

const ROW = {
  id: 's1',
  name: 'nightly-build',
  owner_id: 'o1',
  owner_email: '',
  cron_expr: '0 2 * * *',
  timezone: 'UTC',
  job_spec: {},
  overlap_policy: 'skip',
  enabled: true,
  next_run_at: '2099-01-01T00:00:00Z',
  created_at: '2026-06-01T00:00:00Z',
  updated_at: '2026-06-05T11:00:00Z',
}

function makeWrapper(client: QueryClient) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  )
}

function newClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

afterEach(() => vi.useRealTimers())

test('requests the id path and caches under ["schedules","detail",id]', async () => {
  let path: string | undefined
  server.use(
    http.get('/v1/scheduled-jobs/s1', ({ request }) => {
      path = new URL(request.url).pathname
      return HttpResponse.json(ROW)
    }),
  )
  const client = newClient()
  const { result } = renderHook(() => useSchedule('s1'), { wrapper: makeWrapper(client) })
  await waitFor(() => expect(result.current.status).toBe('success'))

  expect(path).toBe('/v1/scheduled-jobs/s1')
  // Nested under the BARE ['schedules'] prefix so useScheduleActions' existing
  // invalidations reach it with NO change to that shared hook's two shipped
  // mutations. 'detail' is not a ScheduleSort (api.ts:28-36), so it cannot collide
  // with useSchedules' ['schedules', sort, cursor] key (useSchedules.ts:10).
  const cached = client.getQueryData<Schedule>(['schedules', 'detail', 's1'])
  expect(cached?.name).toBe('nightly-build')
  // The colliding key must be EMPTY, or the assertion above proves nothing about
  // which key was written.
  expect(client.getQueryData(['schedules', 's1'])).toBeUndefined()
})

test('polls on the DEFAULT 10s interval, and not before', async () => {
  // Behavioral, not constant-reading: this proves the literal at the call site is
  // wired to refetchInterval. A test that imported a constant and compared it to
  // 10000 would stay green if the hook hardcoded 3000.
  vi.useFakeTimers({ shouldAdvanceTime: true })
  let calls = 0
  server.use(
    http.get('/v1/scheduled-jobs/s1', () => {
      calls++
      return HttpResponse.json(ROW)
    }),
  )
  const { result } = renderHook(() => useSchedule('s1'), { wrapper: makeWrapper(newClient()) })
  await waitFor(() => expect(result.current.status).toBe('success'))
  expect(calls).toBe(1)

  // 9 seconds: still one call. This is the half that fails if someone copies
  // useJobs' 3000 (useJobs.ts:7).
  await act(async () => {
    vi.advanceTimersByTime(9_000)
  })
  expect(calls).toBe(1)

  // Past 10s: it fires. Positive control on the SAME counter, so the equality
  // above is about the interval and not about a dead instrument.
  await act(async () => {
    vi.advanceTimersByTime(2_000)
  })
  await waitFor(() => expect(calls).toBeGreaterThanOrEqual(2))
})

test('an injected interval overrides the default (test seam, same shape as useSchedules)', async () => {
  let calls = 0
  server.use(
    http.get('/v1/scheduled-jobs/s1', () => {
      calls++
      return HttpResponse.json(ROW)
    }),
  )
  renderHook(() => useSchedule('s1', 20), { wrapper: makeWrapper(newClient()) })
  await waitFor(() => expect(calls).toBeGreaterThanOrEqual(2))
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npx vitest run src/schedules/useSchedule.test.tsx`

Expected: FAIL - `Failed to resolve import "./useSchedule" from "src/schedules/useSchedule.test.tsx"`.

- [ ] **Step 3: Implement**

Create `web/src/schedules/useSchedule.ts`:

```ts
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { getSchedule } from './api'

// Polls one schedule. 10s matches useSchedules (useSchedules.ts:8) - a schedule row
// is equally low-churn. useJobs' 3s (useJobs.ts:7) is for a live fleet view and is
// deliberately NOT copied here. The relative countdown on the page is ticked by
// useNow(1000), a local clock that issues no request, not by this poll.
//
// The key nests under the existing bare ['schedules'] prefix so the invalidations
// useScheduleActions already performs (useScheduleActions.ts:11, :16) reach it with
// NO change to those two shipped mutations. 'detail' is not a ScheduleSort
// (api.ts:28-36), so it cannot collide with ['schedules', sort, cursor].
export function useSchedule(id: string, intervalMs = 10000) {
  return useQuery({
    queryKey: ['schedules', 'detail', id],
    queryFn: () => getSchedule(id),
    refetchInterval: intervalMs,
    placeholderData: keepPreviousData,
  })
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `npx vitest run src/schedules/useSchedule.test.tsx`

Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/schedules/useSchedule.ts web/src/schedules/useSchedule.test.tsx
git commit -m "feat(web): useSchedule detail query hook"
```

---

## Task 3: listJobsBySchedule and useScheduleRuns

This is the task where the `sort`-with-filter 400 gets designed out. `listJobs` (`web/src/jobs/api.ts:50-56`) sets `sort` by default, so building this by copying it is the plausible wrong move.

**Files:**
- Modify: `web/src/jobs/api.ts` (append after `:56`)
- Modify: `web/src/jobs/api.test.ts` (append one test; extend the import at `:6-16`)
- Create: `web/src/schedules/useScheduleRuns.ts`
- Test: `web/src/schedules/useScheduleRuns.test.tsx`

- [ ] **Step 1: Write the failing tests**

Add `listJobsBySchedule` to the import block at `web/src/jobs/api.test.ts:6-16` and append:

```ts
test('listJobsBySchedule sends scheduled_job_id and limit and NEVER sends sort', async () => {
  let captured: URLSearchParams | undefined
  server.use(
    http.get('/v1/jobs', ({ request }) => {
      captured = new URL(request.url).searchParams
      return HttpResponse.json(emptyPage)
    }),
  )
  await listJobsBySchedule('sched-1', 20)
  // Presence controls FIRST: without them the absence assertion below passes
  // against a function that sends no parameters at all.
  expect(captured?.get('scheduled_job_id')).toBe('sched-1')
  expect(captured?.get('limit')).toBe('20')
  // The real failure this guards: ?sort= combined with ANY filter is a hard 400,
  // 'sort not supported on filtered list variant' (internal/api/jobs.go:417-422),
  // which the runs panel would render as a generic error box. listJobs sets sort by
  // default (api.ts:50-56), so a copy-paste reintroduces it.
  expect(captured?.has('sort')).toBe(false)
  expect(captured?.has('status')).toBe(false)
})
```

The shipped `listJobs` tests at `web/src/jobs/api.test.ts:20-44` are the regression guard that `listJobs` itself was not edited. Do not duplicate them and do not touch them.

Create `web/src/schedules/useScheduleRuns.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { SCHEDULE_RUNS_LIMIT, useScheduleRuns } from './useScheduleRuns'
import type { JobsPage } from '../jobs/api'

const JOB = {
  id: 'j1',
  name: 'nightly-build',
  priority: 'normal',
  status: 'done',
  labels: null,
  created_at: '2026-06-05T02:00:00Z',
  updated_at: '2026-06-05T02:04:00Z',
}

function makeWrapper(client: QueryClient) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  )
}

function newClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

test('requests the latest 20 runs for the schedule with no sort, cached under ["schedules","runs",id]', async () => {
  let params: URLSearchParams | undefined
  server.use(
    http.get('/v1/jobs', ({ request }) => {
      params = new URL(request.url).searchParams
      return HttpResponse.json({ items: [JOB], next_cursor: '', total: 37 })
    }),
  )
  const client = newClient()
  const { result } = renderHook(() => useScheduleRuns('s1'), { wrapper: makeWrapper(client) })
  await waitFor(() => expect(result.current.status).toBe('success'))

  expect(SCHEDULE_RUNS_LIMIT).toBe(20)
  expect(params?.get('scheduled_job_id')).toBe('s1')
  expect(params?.get('limit')).toBe('20')
  expect(params?.has('sort')).toBe(false)
  const cached = client.getQueryData<JobsPage>(['schedules', 'runs', 's1'])
  expect(cached?.items[0].id).toBe('j1')
  // total is the FULL count from CountJobsByScheduledJob (jobs.sql:80-81), not the
  // page size: the footer says "latest 20 of 37", which is the honest claim for a
  // fixed window with no pager.
  expect(cached?.total).toBe(37)
})

test('an injected interval drives the poll', async () => {
  let calls = 0
  server.use(
    http.get('/v1/jobs', () => {
      calls++
      return HttpResponse.json({ items: [], next_cursor: '', total: 0 })
    }),
  )
  renderHook(() => useScheduleRuns('s1', 20), { wrapper: makeWrapper(newClient()) })
  await waitFor(() => expect(calls).toBeGreaterThanOrEqual(2))
})
```

- [ ] **Step 2: Run the tests to verify they fail**

```
npx vitest run src/jobs/api.test.ts src/schedules/useScheduleRuns.test.tsx
```

Expected: FAIL - `No matching export in "src/jobs/api.ts" for import "listJobsBySchedule"` on the first file, and `Failed to resolve import "./useScheduleRuns"` on the second.

- [ ] **Step 3: Implement**

Append to `web/src/jobs/api.ts` (after `:56`, leaving `listJobs` untouched):

```ts
// Runs produced by one schedule, newest first. Ordering is the server's:
// ListJobsByScheduledJobWithEmailPage orders `j.created_at DESC, j.id DESC`
// (internal/store/query/jobs.sql:77) over idx_jobs_sched_created_id.
//
// Deliberately NOT expressed through listJobs. ?sort= combined with ANY filter is a
// hard 400 - 'sort not supported on filtered list variant; remove the filter or
// remove the sort' (internal/api/jobs.go:417-422) - and listJobs sets sort by default
// on its unfiltered branch (:50-56). This function must never send sort. Do not
// "unify" the two.
//
// Auth runs BEFORE pagination (jobs.go:431-434): a non-owner non-admin gets a 404
// from ownedScheduledJob, not a paginated empty page, so a schedule's job ids cannot
// be enumerated by paging.
export function listJobsBySchedule(scheduledJobId: string, limit: number): Promise<JobsPage> {
  const q = new URLSearchParams({ scheduled_job_id: scheduledJobId, limit: String(limit) })
  return apiFetch<JobsPage>(`/jobs?${q}`)
}
```

Create `web/src/schedules/useScheduleRuns.ts`:

```ts
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { listJobsBySchedule } from '../jobs/api'

// A fixed latest-N window on a detail page, NOT a list page: no cursor stack, no
// pager, no sort control, and none of the computePageRange/offsets machinery. The
// panel's footer states the window honestly as "latest N of <total>".
export const SCHEDULE_RUNS_LIMIT = 20

// 10s, matching useSchedule and useSchedules (useSchedules.ts:8). The key nests under
// the bare ['schedules'] prefix so runNow/setEnabled/update/remove all refresh this
// panel through the invalidation they already perform - a job created by Run now
// appears without a reload. 'runs' is not a ScheduleSort (api.ts:28-36).
export function useScheduleRuns(id: string, intervalMs = 10000) {
  return useQuery({
    queryKey: ['schedules', 'runs', id],
    queryFn: () => listJobsBySchedule(id, SCHEDULE_RUNS_LIMIT),
    refetchInterval: intervalMs,
    placeholderData: keepPreviousData,
  })
}
```

- [ ] **Step 4: Run the tests to verify they pass**

```
npx vitest run src/jobs/ src/schedules/useScheduleRuns.test.tsx
```

Expected: PASS for the whole `src/jobs/` directory - the shipped `listJobs` tests included. If anything under `src/jobs/` changed behaviour, the addition was not additive.

- [ ] **Step 5: Commit**

```bash
git add web/src/jobs/api.ts web/src/jobs/api.test.ts web/src/schedules/useScheduleRuns.ts web/src/schedules/useScheduleRuns.test.tsx
git commit -m "feat(web): listJobsBySchedule client and the schedule runs query"
```

---

## Task 4: useScheduleActions gains update and remove

Purely additive. The two shipped mutations at `useScheduleActions.ts:9-17` are untouched, and `useScheduleActions.test.tsx`'s two tests must need **zero** edits.

**Files:**
- Modify: `web/src/schedules/useScheduleActions.ts`
- Modify: `web/src/schedules/useScheduleActions.test.tsx` (append)

- [ ] **Step 1: Write the failing tests**

Append to `web/src/schedules/useScheduleActions.test.tsx` (and add `import { useSchedule } from './useSchedule'` to the imports):

```tsx
test('update PATCHes the patch verbatim and invalidates the BARE ["schedules"] prefix', async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const spy = vi.spyOn(client, 'invalidateQueries')
  let body: unknown
  server.use(
    http.patch('/v1/scheduled-jobs/s1', async ({ request }) => {
      body = await request.json()
      return HttpResponse.json({ id: 's1', cron_expr: '@every 30m' })
    }),
  )

  const { result } = renderHook(() => useScheduleActions(), { wrapper: makeWrapper(client) })
  await result.current.update.mutateAsync({ id: 's1', patch: { cron_expr: '@every 30m' } })

  // The hook must be a pass-through: it must not merge, default or "helpfully" add
  // fields. Any key it adds here recomputes next_run_at server-side.
  expect(body).toEqual({ cron_expr: '@every 30m' })
  await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: ['schedules'] }))
  // Every invalidation call must use the BARE prefix. A fully-qualified key would
  // only refresh the one sort/page combination that happens to be mounted.
  for (const call of spy.mock.calls) {
    expect((call[0] as { queryKey: unknown[] }).queryKey).toEqual(['schedules'])
  }
})

test('remove DELETEs the id, resolves on the empty 204, and invalidates the bare prefix', async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const spy = vi.spyOn(client, 'invalidateQueries')
  let path: string | undefined
  server.use(
    http.delete('/v1/scheduled-jobs/s1', ({ request }) => {
      path = new URL(request.url).pathname
      return new HttpResponse(null, { status: 204 })
    }),
  )

  const { result } = renderHook(() => useScheduleActions(), { wrapper: makeWrapper(client) })
  await result.current.remove.mutateAsync('s1')

  expect(path).toBe('/v1/scheduled-jobs/s1')
  await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: ['schedules'] }))
})

test('update refetches a MOUNTED detail query (active observer, not a cache seed)', async () => {
  let detailCalls = 0
  server.use(
    http.get('/v1/scheduled-jobs/s1', () => {
      detailCalls++
      return HttpResponse.json({ id: 's1', name: 'nightly', owner_email: '', cron_expr: '0 2 * * *', timezone: 'UTC', job_spec: {}, overlap_policy: 'skip', enabled: true, next_run_at: '2099-01-01T00:00:00Z', created_at: '2026-06-01T00:00:00Z', updated_at: '2026-06-01T00:00:00Z' })
    }),
    http.patch('/v1/scheduled-jobs/s1', () => HttpResponse.json({ id: 's1' })),
  )
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const wrapper = makeWrapper(client)

  // The detail query MUST be mounted via renderHook so it has an ACTIVE OBSERVER.
  // A client.fetchQuery / setQueryData seed leaves no observer, invalidateQueries'
  // default refetchType:'active' never fires, and this assertion would pass
  // vacuously no matter which key the mutation invalidated. A long polling interval
  // is injected so the increment below can only come from the invalidation.
  const { result: detail } = renderHook(() => useSchedule('s1', 600_000), { wrapper })
  await waitFor(() => expect(detail.current.status).toBe('success'))
  expect(detailCalls).toBe(1)

  const { result: actions } = renderHook(() => useScheduleActions(), { wrapper })
  await actions.current.update.mutateAsync({ id: 's1', patch: { overlap_policy: 'allow' } })

  await waitFor(() => expect(detailCalls).toBeGreaterThanOrEqual(2))
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npx vitest run src/schedules/useScheduleActions.test.tsx`

Expected: FAIL - `TypeError: Cannot read properties of undefined (reading 'mutateAsync')` on `result.current.update`, three times.

- [ ] **Step 3: Implement**

Replace `web/src/schedules/useScheduleActions.ts` entirely (the two existing mutations are copied through unchanged):

```ts
import { useMutation, useQueryClient } from '@tanstack/react-query'
import {
  deleteSchedule,
  runScheduleNow,
  setScheduleEnabled,
  updateSchedule,
  type SchedulePatch,
} from './api'

// Mutations for the row actions and the detail page. All four invalidate the same
// BARE ['schedules'] prefix on success, which reaches the list key
// ['schedules', sort, cursor] (useSchedules.ts:10), the detail key
// ['schedules','detail',id] and the runs key ['schedules','runs',id] alike - so a
// Run now refreshes the runs panel and a save refreshes the header without a reload.
//
// None of them writes the response into the cache or into any form state. A settled
// mutation must never reanimate state a later action has already replaced; the fresh
// value arrives through the invalidated refetch, which is the server's own row.
export function useScheduleActions() {
  const qc = useQueryClient()

  const runNow = useMutation({
    mutationFn: (id: string) => runScheduleNow(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['schedules'] }),
  })

  const setEnabled = useMutation({
    mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) => setScheduleEnabled(id, enabled),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['schedules'] }),
  })

  // Pass-through: the patch is sent exactly as given. It is the CALLER's job to have
  // built it from a diff - any extra key recomputes next_run_at server-side
  // (internal/api/scheduled_jobs.go:585).
  const update = useMutation({
    mutationFn: ({ id, patch }: { id: string; patch: SchedulePatch }) => updateSchedule(id, patch),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['schedules'] }),
  })

  const remove = useMutation({
    mutationFn: (id: string) => deleteSchedule(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['schedules'] }),
  })

  return { runNow, setEnabled, update, remove }
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `npx vitest run src/schedules/useScheduleActions.test.tsx`

Expected: PASS (5 tests). The two shipped tests must be byte-identical; if either needed an edit, the addition was not additive.

- [ ] **Step 5: Commit**

```bash
git add web/src/schedules/useScheduleActions.ts web/src/schedules/useScheduleActions.test.tsx
git commit -m "feat(web): update and remove mutations in useScheduleActions"
```

---

## Task 5: ScheduleTriggerForm - the changed-fields-only diff

This is the file where the whole slice's correctness lives. Mirror `web/src/workers/WorkerEditForm.tsx:17-46`.

**Files:**
- Create: `web/src/schedules/ScheduleTriggerForm.tsx`
- Test: `web/src/schedules/ScheduleTriggerForm.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/schedules/ScheduleTriggerForm.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'
import { ScheduleTriggerForm } from './ScheduleTriggerForm'
import type { Schedule } from './api'

function sched(over: Partial<Schedule> = {}): Schedule {
  return {
    id: 's1',
    name: 'nightly-build',
    owner_id: 'o1',
    owner_email: '',
    cron_expr: '0 2 * * *',
    timezone: 'UTC',
    job_spec: {},
    overlap_policy: 'skip',
    enabled: true,
    next_run_at: '2099-01-01T00:00:00Z',
    created_at: '2026-06-01T00:00:00Z',
    updated_at: '2026-06-05T11:00:00Z',
    ...over,
  }
}

function renderForm(over: Partial<Schedule> = {}) {
  const onSubmit = vi.fn()
  render(<ScheduleTriggerForm schedule={sched(over)} pending={false} onSubmit={onSubmit} />)
  return { onSubmit }
}

test('changing ONLY the timezone emits a patch with NO cron_expr key', async () => {
  const { onSubmit } = renderForm()
  const tz = screen.getByLabelText('Timezone')
  await userEvent.clear(tz)
  await userEvent.type(tz, 'Europe/Berlin')
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))

  expect(onSubmit).toHaveBeenCalledTimes(1)
  const patch = onSubmit.mock.calls[0][0]
  // toEqual on the WHOLE object, not a property check: the failure mode is an extra
  // key, and a property check cannot see one. Sending an unchanged cron_expr
  // recomputes next_run_at from time.Now() server-side
  // (internal/api/scheduled_jobs.go:585, :595), silently delaying the next fire by
  // up to a full interval.
  expect(patch).toEqual({ timezone: 'Europe/Berlin' })
  expect('cron_expr' in patch).toBe(false)
  expect('overlap_policy' in patch).toBe(false)
})

test('changing ONLY the cron emits a patch WITH cron_expr and no timezone (positive control)', async () => {
  // Without this the test above passes against a form that emits {} for everything.
  const { onSubmit } = renderForm()
  const cron = screen.getByLabelText('Cron expression')
  await userEvent.clear(cron)
  await userEvent.type(cron, '@every 30m')
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))

  expect(onSubmit.mock.calls[0][0]).toEqual({ cron_expr: '@every 30m' })
})

test('changing all three emits all three', async () => {
  const { onSubmit } = renderForm()
  const cron = screen.getByLabelText('Cron expression')
  await userEvent.clear(cron)
  await userEvent.type(cron, '@hourly')
  const tz = screen.getByLabelText('Timezone')
  await userEvent.clear(tz)
  await userEvent.type(tz, 'America/New_York')
  await userEvent.click(screen.getByRole('button', { name: 'allow' }))
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))

  expect(onSubmit.mock.calls[0][0]).toEqual({
    cron_expr: '@hourly',
    timezone: 'America/New_York',
    overlap_policy: 'allow',
  })
})

test('Save with nothing changed emits NOTHING', async () => {
  const { onSubmit } = renderForm()
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  expect(onSubmit).not.toHaveBeenCalled()
})

test('typing a value and typing it back emits nothing (the diff is against the row, not against dirtiness)', async () => {
  const { onSubmit } = renderForm()
  const tz = screen.getByLabelText('Timezone')
  await userEvent.clear(tz)
  await userEvent.type(tz, 'Europe/Berlin')
  await userEvent.clear(tz)
  await userEvent.type(tz, 'UTC')
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  // A "hasBeenEdited" flag instead of a value comparison would fail here, and that
  // implementation would drift next_run_at on every visit to the field.
  expect(onSubmit).not.toHaveBeenCalled()
})

test('Cancel restores the loaded values so a following Save emits nothing', async () => {
  const { onSubmit } = renderForm()
  const cron = screen.getByLabelText('Cron expression')
  await userEvent.clear(cron)
  await userEvent.type(cron, '@every 5m')
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  expect(cron).toHaveValue('0 2 * * *')
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  expect(onSubmit).not.toHaveBeenCalled()
})

test('the overlap control offers EXACTLY skip and allow', async () => {
  renderForm()
  expect(screen.getByRole('button', { name: 'skip' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'allow' })).toBeInTheDocument()
  // The hi-fi offers a third, `queue` (hifi3-holo-pages.jsx:1773). The server
  // rejects it with 400 'overlap_policy must be skip or allow'
  // (internal/api/scheduled_jobs.go:561-564), so it would be a control that always
  // fails. Both directions asserted: presence alone would pass against a form that
  // renders every string it can think of.
  expect(screen.queryByRole('button', { name: 'queue' })).toBeNull()
  expect(screen.getAllByRole('button', { name: /^(skip|allow|queue)$/ })).toHaveLength(2)
})

test('the current overlap value is the pressed option', () => {
  renderForm({ overlap_policy: 'allow' })
  expect(screen.getByRole('button', { name: 'allow' })).toHaveAttribute('aria-pressed', 'true')
  expect(screen.getByRole('button', { name: 'skip' })).toHaveAttribute('aria-pressed', 'false')
})

test('an obviously invalid cron is still submitted - there is NO client-side validation', async () => {
  const { onSubmit } = renderForm()
  const cron = screen.getByLabelText('Cron expression')
  await userEvent.clear(cron)
  await userEvent.type(cron, 'not a cron at all')
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  // Deliberate: a client-side check would be a second implementation of
  // robfig/cron/v3's grammar (internal/schedrunner/cron.go:14-16) that can disagree
  // with the server. The server is the validator of record.
  expect(onSubmit).toHaveBeenCalledWith({ cron_expr: 'not a cron at all' })
})

test('a server error is rendered verbatim in an alert beside the controls', () => {
  const msg = 'schedule fires faster than minimum interval 30s (observed 1s)'
  render(
    <ScheduleTriggerForm schedule={sched()} pending={false} error={msg} onSubmit={vi.fn()} />,
  )
  // role="alert" and inside the form, not in a page-level banner: an error routed
  // away from the control that caused it can end up rendered behind other content.
  const alert = screen.getByRole('alert')
  expect(alert).toHaveTextContent(msg)
})

test('Save is disabled while a save is pending', () => {
  render(<ScheduleTriggerForm schedule={sched()} pending onSubmit={vi.fn()} />)
  expect(screen.getByRole('button', { name: 'Save changes' })).toBeDisabled()
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npx vitest run src/schedules/ScheduleTriggerForm.test.tsx`

Expected: FAIL - `Failed to resolve import "./ScheduleTriggerForm"`.

- [ ] **Step 3: Implement**

Create `web/src/schedules/ScheduleTriggerForm.tsx`:

```tsx
import { useState } from 'react'
import { Field } from '../components/Field'
import { PillButton } from '../components/holo'
import { Input } from '../components/Input'
import type { Schedule, SchedulePatch } from './api'

// The two values the server accepts (internal/api/scheduled_jobs.go:561-564). The
// hi-fi offers a third, `queue` (hifi3-holo-pages.jsx:1773); it always 400s, so it
// is not rendered. A queueing overlap policy is a scheduler product decision, not a
// UI gap, so no enabler is filed for it.
const OVERLAP_OPTIONS = ['skip', 'allow'] as const

// A NON-CONSTRAINING suggestion list. The server accepts any name time.LoadLocation
// resolves (internal/schedrunner/cron.go:33-36), so a fixed <select> like the hi-fi's
// six-entry dropdown (hifi3-holo-pages.jsx:1766) would make an existing schedule's
// zone unselectable and silently rewrite it on the next save.
// Intl.supportedValuesOf('timeZone') is deliberately NOT used: it was not verified
// against this repo's jsdom version, and a convenience list is not worth a runtime
// risk in the test environment.
const COMMON_TIMEZONES = [
  'UTC',
  'America/Los_Angeles',
  'America/New_York',
  'Europe/London',
  'Europe/Berlin',
  'Asia/Tokyo',
  'Australia/Sydney',
]

interface ScheduleTriggerFormProps {
  schedule: Schedule
  pending: boolean
  error?: string
  onSubmit: (patch: SchedulePatch) => void
}

// Inline edit surface for cron / timezone / overlap policy, on the page rather than
// in a dialog: the hi-fi puts it inline (hifi3-holo-pages.jsx:1745-1791) and the
// shipped precedent for editing a resource from its detail page is WorkerEditForm
// (web/src/workers/WorkerEditForm.tsx). `name`, `enabled` and `job_spec` are also
// PATCH-able and are deliberately NOT here: enabled has its own header button, and
// the other two are out of scope for this slice.
export function ScheduleTriggerForm({ schedule, pending, error, onSubmit }: ScheduleTriggerFormProps) {
  // Seeded ONCE from the schedule and never re-derived on re-render. The page polls
  // every 10s; a refetch landing mid-edit must not overwrite what the user typed.
  // The draft is reset only by an explicit Cancel.
  const [cron, setCron] = useState(schedule.cron_expr)
  const [tz, setTz] = useState(schedule.timezone)
  const [overlap, setOverlap] = useState(schedule.overlap_policy)

  function submit(e: React.FormEvent) {
    e.preventDefault()
    // CHANGED FIELDS ONLY, compared against the currently loaded row - not against a
    // "has been edited" flag. Sending an unchanged cron_expr or timezone is NOT a
    // harmless no-op: internal/api/scheduled_jobs.go:585 recomputes next_run_at from
    // time.Now() whenever the body merely CARRIES either key, so a re-sent unchanged
    // cron on an `@every 1h` schedule pushes the next fire out by up to an hour.
    // Same construction as WorkerEditForm.tsx:42-45.
    //
    // Values are sent exactly as typed, untrimmed: the server does not trim, and
    // trimming here would silently alter the user's input.
    const patch: SchedulePatch = {}
    if (cron !== schedule.cron_expr) patch.cron_expr = cron
    if (tz !== schedule.timezone) patch.timezone = tz
    if (overlap !== schedule.overlap_policy) patch.overlap_policy = overlap

    // Nothing changed: issue no request at all rather than an empty PATCH.
    if (Object.keys(patch).length === 0) return

    // No client-side cron or timezone validation, by design. A pre-check would be a
    // second implementation of robfig/cron/v3's grammar and IANA zone resolution
    // (internal/schedrunner/cron.go:14-16, :33-36) that can disagree with the server.
    // The server is the validator of record and the caller renders its 400 verbatim.
    onSubmit(patch)
  }

  function cancel() {
    setCron(schedule.cron_expr)
    setTz(schedule.timezone)
    setOverlap(schedule.overlap_policy)
  }

  return (
    <form onSubmit={submit} className="px-4 py-3">
      <Field label="Cron expression" htmlFor="schedule-cron" hint="5-field cron, @hourly / @daily, or @every <duration>.">
        <Input
          id="schedule-cron"
          value={cron}
          spellCheck={false}
          onChange={(e) => setCron(e.target.value)}
          className="font-mono"
        />
      </Field>

      <Field label="Timezone" htmlFor="schedule-tz" hint="Any IANA zone name the server can resolve.">
        <Input
          id="schedule-tz"
          list="schedule-tz-options"
          value={tz}
          spellCheck={false}
          onChange={(e) => setTz(e.target.value)}
          className="font-mono"
        />
      </Field>
      <datalist id="schedule-tz-options">
        {COMMON_TIMEZONES.map((z) => (
          <option key={z} value={z} />
        ))}
      </datalist>

      <div className="mb-3">
        <span className="mb-1 block font-mono text-[10px] uppercase tracking-[0.16em] text-fg-mute">
          Overlap policy
        </span>
        <div role="group" aria-label="Overlap policy" className="flex gap-1.5">
          {OVERLAP_OPTIONS.map((o) => (
            <button
              key={o}
              type="button"
              aria-pressed={overlap === o}
              onClick={() => setOverlap(o)}
              className={`rounded-md border px-2.5 py-1 font-mono text-[11px] ${
                overlap === o ? 'border-accent/50 bg-accent/15 text-fg' : 'border-border bg-white/5 text-fg-mute'
              }`}
            >
              {o}
            </button>
          ))}
        </div>
      </div>

      {/* The server's message, verbatim, INSIDE the form next to the control that
          produced it. An error routed to a page-level banner can end up rendered
          somewhere the user is not looking. */}
      {error ? (
        <div
          role="alert"
          className="mb-3 rounded-card border border-err/40 bg-err/10 px-3 py-2 font-mono text-[11px] leading-relaxed text-err"
        >
          {error}
        </div>
      ) : null}

      <div className="flex justify-end gap-2">
        <PillButton onClick={cancel}>Cancel</PillButton>
        <PillButton type="submit" variant="primary" disabled={pending}>
          Save changes
        </PillButton>
      </div>
    </form>
  )
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `npx vitest run src/schedules/ScheduleTriggerForm.test.tsx`

Expected: PASS (11 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/schedules/ScheduleTriggerForm.tsx web/src/schedules/ScheduleTriggerForm.test.tsx
git commit -m "feat(web): inline schedule trigger form with a changed-fields-only patch"
```

---

## Task 6: ScheduleRunsPanel

Presentational only. Mirror `web/src/jobs/JobsTable.tsx:30-81` for the `Table` usage and keep the footer outside the `role="table"` subtree - `Panel`'s `footer` slot does that for free (`web/src/components/holo/Panel.tsx:24-28`).

**Files:**
- Create: `web/src/schedules/ScheduleRunsPanel.tsx`
- Test: `web/src/schedules/ScheduleRunsPanel.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/schedules/ScheduleRunsPanel.test.tsx`:

```tsx
import { render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { expect, test } from 'vitest'
import { ScheduleRunsPanel } from './ScheduleRunsPanel'
import type { Job } from '../jobs/api'

function job(over: Partial<Job> = {}): Job {
  return {
    id: 'aaaaaaaa-1111-2222-3333-444444444444',
    name: 'nightly-build',
    priority: 'normal',
    status: 'done',
    labels: null,
    created_at: '2026-06-05T02:00:00Z',
    updated_at: '2026-06-05T02:04:00Z',
    started_at: '2026-06-05T02:00:00Z',
    finished_at: '2026-06-05T02:04:00Z',
    submitted_by_email: 'dev@studio.com',
    ...over,
  }
}

// The panel renders react-router Links, so a router is required.
function renderPanel(runs: Job[], total: number) {
  return render(
    <MemoryRouter>
      <ScheduleRunsPanel runs={runs} total={total} />
    </MemoryRouter>,
  )
}

test('renders the five columns and one row per run', () => {
  renderPanel([job(), job({ id: 'bbbbbbbb-1111-2222-3333-444444444444', status: 'failed' })], 2)
  const table = screen.getByRole('table', { name: 'Recent runs' })
  const headers = within(table).getAllByRole('columnheader').map((h) => h.textContent)
  expect(headers).toEqual(['STARTED', 'DUR', 'STATUS', 'JOB ID', 'OWNER'])
  // 1 header row + 2 data rows.
  expect(within(table).getAllByRole('row')).toHaveLength(3)
  expect(within(table).getAllByRole('cell')).toHaveLength(10)
})

test('the job id links to the job detail page', () => {
  renderPanel([job()], 1)
  const link = screen.getByRole('link', { name: 'aaaaaaaa' })
  expect(link).toHaveAttribute('href', '/jobs/aaaaaaaa-1111-2222-3333-444444444444')
})

test('a run that never started renders hyphens, not blanks or NaN', () => {
  // started_at / finished_at KEYS ARE ABSENT when the job has no started or finished
  // task (applyJobEnrichment, internal/api/jobs.go:119-137), so this is the real
  // wire shape for a pending run, not a contrived one.
  renderPanel([job({ started_at: undefined, finished_at: undefined, status: 'pending' })], 1)
  const table = screen.getByRole('table', { name: 'Recent runs' })
  const cells = within(table).getAllByRole('cell').map((c) => c.textContent)
  expect(cells[0]).toBe('-')
  expect(cells[1]).toBe('-')
  expect(cells.join(' ')).not.toMatch(/NaN|Invalid/)
})

test('a run with no submitter email renders a hyphen', () => {
  renderPanel([job({ submitted_by_email: undefined })], 1)
  const table = screen.getByRole('table', { name: 'Recent runs' })
  const cells = within(table).getAllByRole('cell')
  expect(cells[4]).toHaveTextContent('-')
})

test('the footer states the window honestly as latest N of total', () => {
  renderPanel([job(), job({ id: 'bbbbbbbb-1111-2222-3333-444444444444' })], 37)
  // "latest 2 of 37", not "1-2 of 37": this is a fixed window with no pager, and the
  // footer must not imply one exists.
  expect(screen.getByText('latest 2 of 37')).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: /next|prev/i })).toBeNull()
})

test('the footer is OUTSIDE the role="table" subtree', () => {
  renderPanel([job()], 1)
  const table = screen.getByRole('table', { name: 'Recent runs' })
  // A footer is not a valid child of role="table". Same rule as JobsTable.tsx:77-79.
  expect(table).not.toContainElement(screen.getByText('latest 1 of 1'))
})

test('a schedule that never fired renders an empty state and no table', () => {
  renderPanel([], 0)
  expect(screen.getByText('this schedule has never fired')).toBeInTheDocument()
  expect(screen.queryByRole('table')).toBeNull()
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npx vitest run src/schedules/ScheduleRunsPanel.test.tsx`

Expected: FAIL - `Failed to resolve import "./ScheduleRunsPanel"`.

- [ ] **Step 3: Implement**

Create `web/src/schedules/ScheduleRunsPanel.tsx`:

```tsx
import { Link } from 'react-router-dom'
import { Panel, Table, TableCell, TableRow, type TableColumn } from '../components/holo'
import type { Job } from '../jobs/api'
import { formatDuration, formatStarted, statusColor } from '../jobs/status'

const COLS = 'grid-cols-[130px_70px_110px_100px_1fr]'

const HEADERS: TableColumn[] = [
  { label: 'STARTED' },
  { label: 'DUR' },
  { label: 'STATUS' },
  { label: 'JOB ID' },
  { label: 'OWNER' },
]

// A fixed latest-N window of the runs this schedule produced, newest first (the
// server's own order: internal/store/query/jobs.sql:77). No sorting affordance and no
// pager: this is a summary on a detail page, not a list page, and the footer says so.
// Every column is a real field on the list-enriched jobResponse
// (internal/api/jobs.go:55-73); nothing here is fabricated.
export function ScheduleRunsPanel({ runs, total }: { runs: Job[]; total: number }) {
  return (
    <Panel
      title="Recent runs"
      meta="GET /v1/jobs?scheduled_job_id="
      footer={<span>{`latest ${runs.length} of ${total}`}</span>}
    >
      {runs.length === 0 ? (
        <div className="px-4 py-6 font-mono text-[11px] tracking-[0.04em] text-fg-dim">
          this schedule has never fired
        </div>
      ) : (
        <Table label="Recent runs" columns={COLS} headers={HEADERS} headerClassName="px-4 py-2.5 tracking-wider">
          {runs.map((j) => {
            const c = statusColor(j.status)
            return (
              <TableRow
                key={j.id}
                className="border-b border-border/40 px-4 py-2 font-mono text-[11px]"
              >
                {/* started_at / finished_at keys are ABSENT for a run with no started
                    or finished task (internal/api/jobs.go:119-137); both helpers
                    return '-' for undefined (jobs/status.ts:33-34, :48-49). */}
                <TableCell className="text-fg-mute">{formatStarted(j.started_at)}</TableCell>
                <TableCell className="text-fg-mute">{formatDuration(j.started_at, j.finished_at)}</TableCell>
                <TableCell className={`flex items-center gap-2 ${c.text}`}>
                  <span className={`h-1.5 w-1.5 rounded-full ${c.dot}`} />
                  {j.status}
                </TableCell>
                <TableCell>
                  <Link to={`/jobs/${j.id}`} className="text-accent hover:text-accent-b">
                    {j.id.slice(0, 8)}
                  </Link>
                </TableCell>
                <TableCell className="truncate text-[10.5px] text-fg-mute">
                  {j.submitted_by_email ?? '-'}
                </TableCell>
              </TableRow>
            )
          })}
        </Table>
      )}
    </Panel>
  )
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `npx vitest run src/schedules/ScheduleRunsPanel.test.tsx`

Expected: PASS (7 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/schedules/ScheduleRunsPanel.tsx web/src/schedules/ScheduleRunsPanel.test.tsx
git commit -m "feat(web): schedule recent-runs panel"
```

---

## Task 7: ScheduleDetailPage and its route

Composition. Mirror `web/src/workers/WorkerDetailPage.tsx:23-93`. **Read the THIRD-CONSUMER FLAG above before writing the triad**: this is the third verbatim copy, it ships deliberately, and it must carry the comment naming the enabler.

Reuse `ConfirmDialog` **unmodified** for Delete. Do not build a new modal: `inert` and focus-trap libraries are unusable in this jsdom and native `<dialog>` is not viable, which is exactly why `DialogShell` exists and why `ConfirmDialog` composes it.

**Files:**
- Create: `web/src/schedules/ScheduleDetailPage.tsx`
- Modify: `web/src/app/router.tsx` (import + one route after `:31`)
- Test: `web/src/schedules/ScheduleDetailPage.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/schedules/ScheduleDetailPage.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { ScheduleDetailPage } from './ScheduleDetailPage'
import type { Schedule } from './api'

const ID = 's1'

function sched(over: Partial<Schedule> = {}): Schedule {
  return {
    id: ID,
    name: 'nightly-build',
    owner_id: 'o1',
    owner_email: '',
    cron_expr: '0 2 * * *',
    timezone: 'UTC',
    job_spec: { name: 'nightly-build', tasks: [{ name: 'render', command: 'echo hi' }] },
    overlap_policy: 'skip',
    enabled: true,
    next_run_at: '2099-01-01T00:00:00Z',
    created_at: '2026-06-01T00:00:00Z',
    updated_at: '2026-06-05T11:00:00Z',
    ...over,
  }
}

const EMPTY_RUNS = { items: [], next_cursor: '', total: 0 }

function LocationProbe() {
  return <span data-testid="location">{useLocation().pathname}</span>
}

// The page does not use useAuth (the endpoints are owner-or-admin server-side and the
// SPA adds no gate of its own), so no AuthProvider and no /v1/users/me handler.
function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return {
    client,
    ...render(
      <QueryClientProvider client={client}>
        <MemoryRouter initialEntries={[`/schedules/${ID}`]}>
          <LocationProbe />
          <Routes>
            <Route path="/schedules/:id" element={<ScheduleDetailPage />} />
            <Route path="/schedules" element={<span>SCHEDULES LIST</span>} />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>,
    ),
  }
}

function handlers(row: Schedule, runs = EMPTY_RUNS) {
  return [
    http.get(`/v1/scheduled-jobs/${ID}`, () => HttpResponse.json(row)),
    http.get('/v1/jobs', () => HttpResponse.json(runs)),
  ]
}

test('renders the breadcrumb, name, ENABLED pill and the three header actions', async () => {
  server.use(...handlers(sched()))
  renderPage()
  expect(await screen.findByText('nightly-build')).toBeInTheDocument()
  expect(screen.getByRole('link', { name: /Schedules/ })).toHaveAttribute('href', '/schedules')
  expect(screen.getByText('ENABLED')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Run now' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Disable' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Delete' })).toBeInTheDocument()
})

test('a paused schedule reads PAUSED, offers Enable, and the next-fire panel is dimmed', async () => {
  server.use(...handlers(sched({ enabled: false })))
  renderPage()
  expect(await screen.findByText('PAUSED')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Enable' })).toBeInTheDocument()
  expect(screen.getByText('paused - no fires queued')).toBeInTheDocument()
  expect(screen.queryByTestId('next-fire-abs')).toBeNull()
})

test('the owner line is ABSENT while owner_email is empty', async () => {
  server.use(...handlers(sched({ owner_email: '' })))
  renderPage()
  await screen.findByText('nightly-build')
  // GET /v1/scheduled-jobs/{id} never calls fillOwnerEmails, so owner_email is
  // always "" today (internal/api/scheduled_jobs.go:508-519). The page must omit the
  // owner entirely rather than render an empty label - and must NOT fall back to
  // owner_id, which is 36 opaque characters.
  expect(screen.queryByText(/owner/i)).toBeNull()
  expect(screen.queryByText('o1')).toBeNull()
})

test('the owner line APPEARS when owner_email is populated (positive control)', async () => {
  // Without this, the test above passes against a page that simply forgot the field.
  // It also pins the behaviour for when the filed enabler lands.
  server.use(...handlers(sched({ owner_email: 'dev@studio.com' })))
  renderPage()
  await screen.findByText('nightly-build')
  expect(screen.getByText('dev@studio.com')).toBeInTheDocument()
})

test('the identity line omits last run and last job when the keys are absent', async () => {
  server.use(...handlers(sched()))
  renderPage()
  await screen.findByText('nightly-build')
  expect(screen.queryByText(/last job/)).toBeNull()
})

test('the last job renders as a link to the job when last_job_id is present', async () => {
  const jobId = 'abcdef12-3456-7890-abcd-ef1234567890'
  server.use(...handlers(sched({ last_job_id: jobId, last_run_at: '2026-06-05T11:00:00Z' })))
  renderPage()
  await screen.findByText('nightly-build')
  expect(screen.getByRole('link', { name: 'abcdef12' })).toHaveAttribute('href', `/jobs/${jobId}`)
})

test('the job spec renders as read-only pretty-printed JSON with no editor', async () => {
  server.use(...handlers(sched()))
  renderPage()
  await screen.findByText('nightly-build')
  // Two-space indented JSON.stringify output, not YAML and not a single line.
  expect(screen.getByText(/"tasks": \[/)).toBeInTheDocument()
  expect(screen.queryByRole('textbox', { name: /spec/i })).toBeNull()
  expect(screen.queryByRole('button', { name: /edit spec/i })).toBeNull()
})

test('the next-fire panel shows exactly one entry', async () => {
  server.use(...handlers(sched()))
  renderPage()
  await screen.findByText('nightly-build')
  expect(screen.getAllByTestId('next-fire-abs')).toHaveLength(1)
  // The hi-fi previews five; a multi-entry preview needs a cron parser web/ does not
  // have. One honest server-supplied value instead.
  expect(screen.getAllByTestId('next-fire-rel')).toHaveLength(1)
})

test('a 404 renders the not-found card with a back link and NO edit or action controls', async () => {
  server.use(
    http.get(`/v1/scheduled-jobs/${ID}`, () =>
      HttpResponse.json({ error: 'scheduled job not found' }, { status: 404 }),
    ),
    http.get('/v1/jobs', () => HttpResponse.json({ error: 'scheduled job not found' }, { status: 404 })),
  )
  renderPage()
  expect(await screen.findByText('Schedule not found.')).toBeInTheDocument()
  expect(screen.getByRole('link', { name: /Schedules/ })).toHaveAttribute('href', '/schedules')
  // ownedScheduledJob 404s a non-owner non-admin identically, so this IS the access
  // denied surface. No Retry (it is not transient), no action bar, no form.
  expect(screen.queryByRole('button', { name: 'Retry' })).toBeNull()
  expect(screen.queryByRole('button', { name: 'Run now' })).toBeNull()
  expect(screen.queryByRole('button', { name: 'Delete' })).toBeNull()
  expect(screen.queryByLabelText('Cron expression')).toBeNull()
})

test('a 500 renders the retryable card and Retry issues exactly one more request', async () => {
  let calls = 0
  server.use(
    http.get(`/v1/scheduled-jobs/${ID}`, () => {
      calls++
      return HttpResponse.json({ error: 'boom' }, { status: 500 })
    }),
    http.get('/v1/jobs', () => HttpResponse.json(EMPTY_RUNS)),
  )
  renderPage()
  const retry = await screen.findByRole('button', { name: 'Retry' })
  const before = calls
  await userEvent.click(retry)
  await waitFor(() => expect(calls).toBe(before + 1))
})

test('Run now POSTs run-now', async () => {
  let posted = false
  server.use(
    ...handlers(sched()),
    http.post(`/v1/scheduled-jobs/${ID}/run-now`, () => {
      posted = true
      return HttpResponse.json({ id: 'job9' }, { status: 201 })
    }),
  )
  renderPage()
  await screen.findByText('nightly-build')
  await userEvent.click(screen.getByRole('button', { name: 'Run now' }))
  await waitFor(() => expect(posted).toBe(true))
})

test('Disable PATCHes exactly { enabled: false }', async () => {
  let body: unknown
  server.use(
    ...handlers(sched()),
    http.patch(`/v1/scheduled-jobs/${ID}`, async ({ request }) => {
      body = await request.json()
      return HttpResponse.json(sched({ enabled: false }))
    }),
  )
  renderPage()
  await screen.findByText('nightly-build')
  await userEvent.click(screen.getByRole('button', { name: 'Disable' }))
  await waitFor(() => expect(body).toEqual({ enabled: false }))
})

test('Delete opens a confirm whose copy states the verified consequences, and Cancel issues NO request', async () => {
  let deletes = 0
  server.use(
    ...handlers(sched()),
    http.delete(`/v1/scheduled-jobs/${ID}`, () => {
      deletes++
      return new HttpResponse(null, { status: 204 })
    }),
  )
  renderPage()
  await screen.findByText('nightly-build')
  await userEvent.click(screen.getByRole('button', { name: 'Delete' }))

  const dialog = await screen.findByRole('dialog')
  // jobs.scheduled_job_id is ON DELETE SET NULL
  // (internal/store/migrations/000006_scheduled_jobs.up.sql:20-21).
  expect(dialog).toHaveTextContent(/unlinked/i)
  expect(dialog).toHaveTextContent(/not cancelled/i)

  await userEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  expect(deletes).toBe(0)
})

test('confirming Delete issues exactly one DELETE and navigates to /schedules', async () => {
  // Positive control on the same counter as the test above.
  let deletes = 0
  server.use(
    ...handlers(sched()),
    http.delete(`/v1/scheduled-jobs/${ID}`, () => {
      deletes++
      return new HttpResponse(null, { status: 204 })
    }),
  )
  renderPage()
  await screen.findByText('nightly-build')
  await userEvent.click(screen.getByRole('button', { name: 'Delete' }))
  const dialog = await screen.findByRole('dialog')
  await userEvent.click(within(dialog).getByRole('button', { name: 'Delete' }))

  await waitFor(() => expect(screen.getByTestId('location')).toHaveTextContent('/schedules'))
  expect(deletes).toBe(1)
})

test('a failed save renders the server message inside the form, not in a page banner', async () => {
  server.use(
    ...handlers(sched()),
    http.patch(`/v1/scheduled-jobs/${ID}`, () =>
      HttpResponse.json(
        { error: 'schedule fires faster than minimum interval 30s (observed 1s)' },
        { status: 400 },
      ),
    ),
  )
  renderPage()
  await screen.findByText('nightly-build')
  const cron = screen.getByLabelText('Cron expression')
  await userEvent.clear(cron)
  await userEvent.type(cron, '@every 1s')
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))

  const alert = await screen.findByRole('alert')
  expect(alert).toHaveTextContent('minimum interval 30s')
  // The message must sit beside the control that produced it. The form element is
  // the nearest ancestor form; assert containment rather than mere presence.
  expect(cron.closest('form')).toContainElement(alert)
})
```

Add `within` to the `@testing-library/react` import.

- [ ] **Step 2: Run the test to verify it fails**

Run: `npx vitest run src/schedules/ScheduleDetailPage.test.tsx`

Expected: FAIL - `Failed to resolve import "./ScheduleDetailPage"`.

- [ ] **Step 3: Implement**

Create `web/src/schedules/ScheduleDetailPage.tsx`:

```tsx
import { useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { Button } from '../components/Button'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { Chip, GlassPanel, Panel, PillButton } from '../components/holo'
import { ApiError } from '../lib/api'
import { useNow } from '../lib/useNow'
import { formatStarted } from '../jobs/status'
import type { SchedulePatch } from './api'
import { formatRelativeTime, nextRunDisplay, shortId } from './format'
import { ScheduleRunsPanel } from './ScheduleRunsPanel'
import { ScheduleTriggerForm } from './ScheduleTriggerForm'
import { useSchedule } from './useSchedule'
import { useScheduleActions } from './useScheduleActions'
import { useScheduleRuns } from './useScheduleRuns'

export function ScheduleDetailPage() {
  const { id = '' } = useParams()
  const navigate = useNavigate()
  const { data: schedule, error, isLoading, refetch } = useSchedule(id)
  const { data: runs } = useScheduleRuns(id)
  const { runNow, setEnabled, update, remove } = useScheduleActions()
  const [confirmDelete, setConfirmDelete] = useState(false)
  // Local 1s clock so the relative countdown stays fresh between 10s polls. It issues
  // NO request (lib/useNow.ts:8-15). SchedulesPage rolls its own setTick
  // (SchedulesPage.tsx:43-47); the shared hook is used here rather than adding a
  // second local-timer idiom to the codebase.
  const now = useNow(1000)

  // THIRD CONSUMER of this triad, shipped deliberately. The identical block lives in
  // web/src/workers/WorkerDetailPage.tsx:30-55 and web/src/jobs/JobDetailPage.tsx:57-78.
  // Extracting a shared primitive would have to migrate both shipped pages behind a
  // byte-identical-test refactor gate, which is its own slice.
  // Enabler: idea-2026-08-12-detail-page-state-triad-primitive (to be filed).
  if (isLoading && !schedule) {
    return <GlassPanel className="h-40" />
  }

  if (error && !schedule) {
    // ownedScheduledJob 404s a non-owner non-admin exactly as it 404s a missing row
    // (internal/api/scheduled_jobs.go:147-169): the resource is hidden, not refused.
    // That server check is the ENTIRE access-control story - the SPA adds no gate and
    // must not. A 404 is not transient, so it gets no Retry.
    const notFound = error instanceof ApiError && error.status === 404
    return (
      <GlassPanel className="mx-auto mt-10 max-w-md p-6 text-center">
        {notFound ? (
          <div className="text-[13px] text-fg-mute">Schedule not found.</div>
        ) : (
          <>
            <div className="mb-3 text-[13px] text-err">{(error as Error).message}</div>
            <Button className="w-auto px-4" onClick={() => refetch()}>
              Retry
            </Button>
          </>
        )}
        <div className="mt-4">
          <Link to="/schedules" className="font-mono text-[11px] text-accent">
            &larr; Schedules
          </Link>
        </div>
      </GlassPanel>
    )
  }

  if (!schedule) return null

  const busy = runNow.isPending || setEnabled.isPending || remove.isPending
  const actionError = (runNow.error ?? setEnabled.error ?? remove.error) as Error | null

  return (
    <div className="flex flex-col gap-4">
      {/* Breadcrumb + name + state pill + right-aligned action bar. */}
      <div className="flex items-center gap-2.5">
        <Link to="/schedules" className="text-[12px] text-fg-mute hover:text-fg">
          &larr; Schedules
        </Link>
        <span className="text-fg-dim">/</span>
        <span className="font-mono text-[14px] tracking-[0.04em] text-fg">{schedule.name}</span>
        <Chip tone={schedule.enabled ? 'accent' : 'muted'}>{schedule.enabled ? 'ENABLED' : 'PAUSED'}</Chip>
        <div className="ml-auto flex gap-2">
          {/* All three are owner-or-admin server-side, including run-now
              (internal/api/scheduled_jobs.go:642), contrary to the hi-fi's
              admin-only footnote. No client-side role gate. */}
          <PillButton onClick={() => runNow.mutate(id)} disabled={busy}>
            Run now
          </PillButton>
          <PillButton onClick={() => setEnabled.mutate({ id, enabled: !schedule.enabled })} disabled={busy}>
            {schedule.enabled ? 'Disable' : 'Enable'}
          </PillButton>
          <PillButton variant="danger" onClick={() => setConfirmDelete(true)} disabled={busy}>
            Delete
          </PillButton>
        </div>
      </div>

      {/* Identity sub-line. The OWNER is deliberately conditional and therefore today
          always absent: GET /v1/scheduled-jobs/{id} never calls fillOwnerEmails
          (internal/api/scheduled_jobs.go:508-519, unlike both list arms at :371 and
          :504) and owner_email has no omitempty (:25), so it is always "". Falling
          back to owner_id would render 36 opaque characters, and carrying the value
          over from the cached list row would make a deep link behave differently from
          a click-through.
          Enabler: bug-2026-08-12-scheduled-job-detail-missing-owner-email (to be filed). */}
      <div className="font-mono text-[11px] tracking-[0.04em] text-fg-mute">
        {schedule.owner_email ? (
          <>
            owner <span className="text-fg">{schedule.owner_email}</span> ·{' '}
          </>
        ) : null}
        created <span className="text-fg">{formatStarted(schedule.created_at)}</span> · updated{' '}
        <span className="text-fg">{formatRelativeTime(schedule.updated_at)}</span> · next fire{' '}
        <span className="text-fg">
          {schedule.enabled ? nextRunDisplay(schedule.next_run_at, now) : '-'}
        </span>{' '}
        · last run{' '}
        <span className="text-fg">
          {schedule.last_run_at ? formatRelativeTime(schedule.last_run_at) : '-'}
        </span>
        {schedule.last_job_id ? (
          <>
            {' '}
            · last job{' '}
            <Link to={`/jobs/${schedule.last_job_id}`} className="text-accent">
              {shortId(schedule.last_job_id)}
            </Link>
          </>
        ) : null}
      </div>

      {actionError ? (
        <div role="alert" className="rounded-card border border-err/40 bg-err/10 px-4 py-2 text-[12px] text-err">
          {actionError.message}
        </div>
      ) : null}

      <div className="grid grid-cols-2 gap-3">
        <div className="flex flex-col gap-3">
          <Panel title="Trigger" meta="PATCH /v1/scheduled-jobs">
            <ScheduleTriggerForm
              schedule={schedule}
              pending={update.isPending}
              error={(update.error as Error | null)?.message}
              onSubmit={(patch: SchedulePatch) => {
                // Clear a stale server error before re-submitting, matching the
                // NewJobPage/JobActions convention.
                update.reset()
                // The patch is already a diff; it is forwarded verbatim. The settled
                // mutation writes nothing back into the form's draft state - the
                // fresh row arrives through the invalidated refetch.
                update.mutate({ id, patch })
              }}
            />
          </Panel>

          {/* Read-only. The stored value is JSON (scheduledJobResponse.JobSpec is a
              json.RawMessage, internal/api/scheduled_jobs.go:26); web/ has no YAML
              serializer, and the app's only spec editor is already a JSON textarea
              (jobs/NewJobPage.tsx:51-59). Rendered as a React TEXT CHILD in a <pre>:
              never dangerouslySetInnerHTML, and nothing from job_spec goes into a
              URL, a title attribute or a log line - it can carry env values a user
              chose to store.
              Enabler: idea-2026-08-12-schedule-job-spec-editor (to be filed). */}
          <Panel title="Job spec" meta="READ-ONLY">
            <pre className="max-h-[360px] overflow-auto px-4 py-3 font-mono text-[11px] leading-relaxed text-fg-mute">
              {JSON.stringify(schedule.job_spec, null, 2)}
            </pre>
          </Panel>
        </div>

        <div className="flex flex-col gap-3">
          {/* ONE entry, the server's own next_run_at. The hi-fi previews five
              (hifi3-holo-pages.jsx:1814-1828), which needs a cron parser: web/ has
              none (package.json:13-20), so a preview would be a second implementation
              of @every / @hourly / IANA-zone semantics that has to agree with
              robfig/cron/v3 (internal/schedrunner/cron.go:14-16), and a preview that
              silently disagrees is worse than one honest value. This is not a
              degraded placeholder: PATCH recomputes next_run_at server-side and
              returns it (scheduled_jobs.go:585-596), so after a cron save this shows
              the authoritative first fire of the edit just made.
              Enabler: idea-2026-08-12-schedule-next-fires-preview (to be filed). */}
          <Panel title="Next fire" meta="NEXT_RUN_AT">
            {schedule.enabled ? (
              <div className="flex flex-col gap-1 px-4 py-3">
                <span data-testid="next-fire-rel" className="font-mono text-[13px] text-fg">
                  {nextRunDisplay(schedule.next_run_at, now)}
                </span>
                <span data-testid="next-fire-abs" className="font-mono text-[11px] text-fg-mute">
                  {formatStarted(schedule.next_run_at)}
                </span>
              </div>
            ) : (
              <div className="px-4 py-3 font-mono text-[11px] text-fg-dim">paused - no fires queued</div>
            )}
          </Panel>

          <ScheduleRunsPanel runs={runs?.items ?? []} total={runs?.total ?? 0} />
        </div>
      </div>

      {/* ConfirmDialog is reused UNMODIFIED; it composes DialogShell/dialogStack,
          which own the portal, focus handling, scroll lock and scoped Escape. Do not
          hand-roll a modal here. */}
      {confirmDelete && (
        <ConfirmDialog
          title="Delete schedule"
          body={`Delete "${schedule.name}"? Jobs it already produced are kept, but they are unlinked from this schedule, so its run history becomes unreachable. A run already in flight is not cancelled. This cannot be undone.`}
          confirmLabel="Delete"
          destructive
          onCancel={() => setConfirmDelete(false)}
          onConfirm={() => {
            setConfirmDelete(false)
            remove.mutate(id, { onSuccess: () => navigate('/schedules') })
          }}
        />
      )}
    </div>
  )
}
```

Edit `web/src/app/router.tsx`: add `import { ScheduleDetailPage } from '../schedules/ScheduleDetailPage'` after `:11`, and add this route immediately after `:31`:

```tsx
        {/* No AdminRoute: every /v1/scheduled-jobs/{id} route is auth(...) and
            owner-or-admin, 404-on-deny (internal/api/server.go:163-168,
            internal/api/scheduled_jobs.go:147-169). */}
        <Route path="/schedules/:id" element={<ScheduleDetailPage />} />
```

- [ ] **Step 4: Run the test to verify it passes**

```
npx vitest run src/schedules/ScheduleDetailPage.test.tsx src/app/
```

Expected: PASS (15 tests in the page file), and the shipped router/route tests under `src/app/` still green.

- [ ] **Step 5: Commit**

```bash
git add web/src/schedules/ScheduleDetailPage.tsx web/src/schedules/ScheduleDetailPage.test.tsx web/src/app/router.tsx
git commit -m "feat(web): schedule detail page and its route"
```

---

## Task 8: The next_run_at drift regression

**This task exists for one defect and nothing else.** A PATCH whose body carries `cron_expr` or `timezone` makes the server recompute `next_run_at` from `time.Now()` **even when the value did not change** (`internal/api/scheduled_jobs.go:585`, `:595`). Sending the whole form is the obvious implementation, it passes every naive test, and its only symptom is a schedule that drifts later every time someone opens the editor.

The test is **behavioral, not a body-shape assertion** - Task 5 already covers the body shape at the unit level. Here the MSW handler transcribes the server rule, so the assertion is on the user-visible next fire time. That is a property only the correct implementation produces.

**Files:**
- Test: `web/src/schedules/ScheduleDetailPage.test.tsx` (append)

- [ ] **Step 1: Write the failing test**

Append to `web/src/schedules/ScheduleDetailPage.test.tsx`:

```tsx
// The schedule's next fire is soon; a recompute would push it out by a full hour.
const NEXT_SOON = '2099-01-01T00:05:00Z'
const NEXT_DRIFTED = '2099-01-01T01:00:00Z'

// Transcription of internal/api/scheduled_jobs.go:585-596: next_run_at is recomputed
// from time.Now() whenever the request CARRIES a cron_expr or timezone key, changed
// or not. `current` is mutated so the invalidated GET refetch serves the same row the
// PATCH produced, exactly as the real server does.
function driftServer(initial: Schedule) {
  let current = initial
  const bodies: Record<string, unknown>[] = []
  server.use(
    http.get(`/v1/scheduled-jobs/${ID}`, () => HttpResponse.json(current)),
    http.get('/v1/jobs', () => HttpResponse.json(EMPTY_RUNS)),
    http.patch(`/v1/scheduled-jobs/${ID}`, async ({ request }) => {
      const body = (await request.json()) as Record<string, unknown>
      bodies.push(body)
      const recomputes = 'cron_expr' in body || 'timezone' in body
      current = {
        ...current,
        ...(body as Partial<Schedule>),
        next_run_at: recomputes ? NEXT_DRIFTED : current.next_run_at,
      }
      return HttpResponse.json(current)
    }),
  )
  return { bodies }
}

test('the two next-fire fixtures render differently, so the panel assertions below can discriminate', () => {
  // Guards the instrument itself: if formatStarted collapsed these two instants to
  // the same string in the runner's timezone, both drift tests would be vacuous.
  expect(formatStarted(NEXT_SOON)).not.toBe(formatStarted(NEXT_DRIFTED))
})

test('DRIFT REGRESSION: saving only the overlap policy does NOT move the next fire time', async () => {
  const { bodies } = driftServer(sched({ next_run_at: NEXT_SOON }))
  renderPage()
  await screen.findByText('nightly-build')
  expect(screen.getByTestId('next-fire-abs')).toHaveTextContent(formatStarted(NEXT_SOON))

  await userEvent.click(screen.getByRole('button', { name: 'allow' }))
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))

  await waitFor(() => expect(bodies).toHaveLength(1))
  // The body must not carry cron_expr or timezone. This is the cause...
  expect(bodies[0]).toEqual({ overlap_policy: 'allow' })
  // ...and this is the user-visible effect, which is what actually matters: an
  // implementation that posts the whole form pushes the next fire out by 55 minutes
  // and nothing else in the app complains.
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'allow' })).toHaveAttribute('aria-pressed', 'true'),
  )
  expect(screen.getByTestId('next-fire-abs')).toHaveTextContent(formatStarted(NEXT_SOON))
  expect(screen.getByTestId('next-fire-abs')).not.toHaveTextContent(formatStarted(NEXT_DRIFTED))
})

test('POSITIVE CONTROL: saving a changed cron DOES move the next fire time', async () => {
  // Without this, the test above passes against a page whose next-fire panel is
  // static and never reflects a save at all.
  const { bodies } = driftServer(sched({ next_run_at: NEXT_SOON }))
  renderPage()
  await screen.findByText('nightly-build')
  expect(screen.getByTestId('next-fire-abs')).toHaveTextContent(formatStarted(NEXT_SOON))

  const cron = screen.getByLabelText('Cron expression')
  await userEvent.clear(cron)
  await userEvent.type(cron, '@every 1h')
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))

  await waitFor(() => expect(bodies).toEqual([{ cron_expr: '@every 1h' }]))
  await waitFor(() =>
    expect(screen.getByTestId('next-fire-abs')).toHaveTextContent(formatStarted(NEXT_DRIFTED)),
  )
})

test('a clean Save issues ZERO requests', async () => {
  const { bodies } = driftServer(sched({ next_run_at: NEXT_SOON }))
  renderPage()
  await screen.findByText('nightly-build')
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  // No PATCH at all - not an empty one. An empty PATCH is harmless server-side today,
  // but "no change means no request" is the property being pinned.
  await new Promise((r) => setTimeout(r, 50))
  expect(bodies).toEqual([])
})

test('saving twice in a row issues exactly one request: after a save the draft is clean again', async () => {
  const { bodies } = driftServer(sched({ next_run_at: NEXT_SOON }))
  renderPage()
  await screen.findByText('nightly-build')
  await userEvent.click(screen.getByRole('button', { name: 'allow' }))
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  await waitFor(() => expect(bodies).toHaveLength(1))

  // The refetched row now says overlap_policy: 'allow', so the draft matches it and a
  // second Save must be a no-op. An implementation that re-armed the form from the
  // mutation response, or that tracked dirtiness with a flag, would fire again here -
  // and if it re-sent cron_expr, that second request would drift next_run_at.
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  await new Promise((r) => setTimeout(r, 50))
  expect(bodies).toHaveLength(1)
})
```

Add `formatStarted` to the imports: `import { formatStarted } from '../jobs/status'`.

- [ ] **Step 2: Run the test to verify it fails - AND VERIFY IT FAILS FOR THE RIGHT REASON**

Run: `npx vitest run src/schedules/ScheduleDetailPage.test.tsx`

Expected on the tree as it stands after Task 7: these five tests **PASS**, because Task 5 already implemented the diff. A green test here proves nothing on its own. **Do not accept that.** Prove the test is discriminating by temporarily mutating `web/src/schedules/ScheduleTriggerForm.tsx`'s `submit` to send the whole form:

```ts
    const patch: SchedulePatch = { cron_expr: cron, timezone: tz, overlap_policy: overlap }
```

Re-run. Expected: FAIL with

- `DRIFT REGRESSION`: `expected element to have text content "<formatted NEXT_SOON>" but got "<formatted NEXT_DRIFTED>"`, and the `bodies[0]` equality failing with all three keys present.
- `a clean Save issues ZERO requests`: `expected [ { cron_expr: ... } ] to deeply equal []`.
- `saving twice`: `expected length 2 to be 1`.

Then **revert the mutation** (restore the diff implementation) and confirm all five pass again. Record both outputs in the task notes. The mutation is discarded; the tests are what survive.

- [ ] **Step 3: No implementation step**

There is deliberately no code change in this task. The behaviour was implemented in Task 5; this task's deliverable is the permanent regression test plus the recorded mutation evidence that it discriminates. If Step 2's mutation did **not** turn the tests red, the tests are vacuous - fix them before continuing.

- [ ] **Step 4: Run the whole schedules directory to verify it is green**

Run: `npx vitest run src/schedules/`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/schedules/ScheduleDetailPage.test.tsx
git commit -m "test(web): regression test for next_run_at drift on an unchanged cron"
```

---

## Task 9: Lifecycle regressions - a poll must not clobber a dirty form

Two house invariants in their frontend form. Kept in their own file so they are findable.

**Files:**
- Create: `web/src/schedules/ScheduleDetailPage.lifecycle.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/schedules/ScheduleDetailPage.lifecycle.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, expect, test, vi } from 'vitest'
import { server } from '../test/setup-helpers'
import { ScheduleDetailPage } from './ScheduleDetailPage'
import type { Schedule } from './api'

const ID = 's1'

function sched(over: Partial<Schedule> = {}): Schedule {
  return {
    id: ID,
    name: 'nightly-build',
    owner_id: 'o1',
    owner_email: '',
    cron_expr: '0 2 * * *',
    timezone: 'UTC',
    job_spec: {},
    overlap_policy: 'skip',
    enabled: true,
    next_run_at: '2099-01-01T00:00:00Z',
    created_at: '2026-06-01T00:00:00Z',
    updated_at: '2026-06-05T11:00:00Z',
    ...over,
  }
}

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[`/schedules/${ID}`]}>
        <Routes>
          <Route path="/schedules/:id" element={<ScheduleDetailPage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

afterEach(() => vi.useRealTimers())

test('a 10s poll landing mid-edit does NOT overwrite the typed cron', async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true })
  const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })

  let detailCalls = 0
  server.use(
    http.get(`/v1/scheduled-jobs/${ID}`, () => {
      detailCalls++
      // After the first response the SERVER's value changes - someone else edited it,
      // or the schedule was touched elsewhere. The poll must not push this into the
      // user's half-typed input.
      return HttpResponse.json(sched({ cron_expr: detailCalls === 1 ? '0 2 * * *' : '0 9 * * 1' }))
    }),
    http.get('/v1/jobs', () => HttpResponse.json({ items: [], next_cursor: '', total: 0 })),
  )

  renderPage()
  const cron = await screen.findByLabelText('Cron expression')
  expect(cron).toHaveValue('0 2 * * *')
  const callsAfterLoad = detailCalls

  await user.clear(cron)
  await user.type(cron, '@every 45m')
  expect(cron).toHaveValue('@every 45m')

  // Cross the 10s poll boundary while the form is dirty.
  await act(async () => {
    vi.advanceTimersByTime(11_000)
  })
  // The poll MUST have fired, or this test proves nothing: the input could hold the
  // typed value simply because nothing ever arrived.
  await waitFor(() => expect(detailCalls).toBeGreaterThan(callsAfterLoad))

  // The typed value survives.
  expect(cron).toHaveValue('@every 45m')
})

test('POSITIVE CONTROL: a fresh mount DOES pick up the new server value', async () => {
  // Proves the server fixture really changed and that the form is not simply
  // hardcoded, which would make the test above vacuous.
  server.use(
    http.get(`/v1/scheduled-jobs/${ID}`, () => HttpResponse.json(sched({ cron_expr: '0 9 * * 1' }))),
    http.get('/v1/jobs', () => HttpResponse.json({ items: [], next_cursor: '', total: 0 })),
  )
  renderPage()
  expect(await screen.findByLabelText('Cron expression')).toHaveValue('0 9 * * 1')
})

test('Cancel restores the CURRENTLY loaded server value, not the value at mount', async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true })
  const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })

  let detailCalls = 0
  server.use(
    http.get(`/v1/scheduled-jobs/${ID}`, () => {
      detailCalls++
      return HttpResponse.json(sched({ cron_expr: detailCalls === 1 ? '0 2 * * *' : '0 9 * * 1' }))
    }),
    http.get('/v1/jobs', () => HttpResponse.json({ items: [], next_cursor: '', total: 0 })),
  )

  renderPage()
  const cron = await screen.findByLabelText('Cron expression')
  await user.clear(cron)
  await user.type(cron, '@every 45m')

  await act(async () => {
    vi.advanceTimersByTime(11_000)
  })
  await waitFor(() => expect(detailCalls).toBeGreaterThan(1))

  await user.click(screen.getByRole('button', { name: 'Cancel' }))
  // Discarding an edit must land on what the server says NOW. Restoring the
  // mount-time value would silently reintroduce a stale cron on the next save - and
  // because that save would carry cron_expr, it would also drift next_run_at.
  expect(cron).toHaveValue('0 9 * * 1')
})
```

- [ ] **Step 2: Run the test to verify it fails, and prove it discriminates**

Run: `npx vitest run src/schedules/ScheduleDetailPage.lifecycle.test.tsx`

Expected: the first and third tests **PASS** as written, because Task 5's `useState` seeding already has this property. As in Task 8, prove they discriminate by temporarily adding a re-seeding effect to `ScheduleTriggerForm.tsx` - the plausible wrong implementation:

```ts
  useEffect(() => {
    setCron(schedule.cron_expr)
    setTz(schedule.timezone)
    setOverlap(schedule.overlap_policy)
  }, [schedule])
```

Re-run. Expected: FAIL on `a 10s poll landing mid-edit ...` with `expected element to have value "@every 45m" but got "0 9 * * 1"`. **Revert the effect** and confirm green. Record both outputs.

If the mutation does not turn the test red, the test is vacuous - most likely the poll never fired, so check the `detailCalls` guard before anything else.

- [ ] **Step 3: No implementation step**

The behaviour was implemented in Task 5 (seed once, never re-derive). This task's deliverable is the permanent test plus the recorded mutation evidence.

- [ ] **Step 4: Run the whole schedules directory**

Run: `npx vitest run src/schedules/`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/schedules/ScheduleDetailPage.lifecycle.test.tsx
git commit -m "test(web): a schedule poll must not clobber a dirty trigger form"
```

---

## Task 10: The Schedules list entry points

Two `<Link>`s. Because `SchedulesTable` starts rendering react-router `<Link>`s, **two shipped test files need a `MemoryRouter` wrapper** - `renderWithQuery` does not provide one (`web/src/test/renderWithQuery.tsx:7-12`). That edit is **wrapper-only**: not one assertion may change. An assertion that needs adjusting is itself the finding.

**Files:**
- Modify: `web/src/schedules/SchedulesTable.tsx:53-56`, `:72-89`
- Modify: `web/src/schedules/SchedulesTable.test.tsx` (wrappers + new tests)
- Modify: `web/src/schedules/SchedulesPage.test.tsx` (wrappers only)

- [ ] **Step 1: Write the failing test**

In `web/src/schedules/SchedulesTable.test.tsx`, add `import { MemoryRouter } from 'react-router-dom'` and introduce a helper, then route every existing `render(...)` call through it **without touching any assertion**:

```tsx
function renderTable(ui: React.ReactElement) {
  return render(<MemoryRouter>{ui}</MemoryRouter>)
}
```

Every existing `render(<SchedulesTable ... />)` becomes `renderTable(<SchedulesTable ... />)`. Nothing else in those tests changes.

Append the new tests:

```tsx
test('the NAME cell links to the schedule detail page', () => {
  renderTable(
    <SchedulesTable schedules={[sched()]} pendingId={null} onRunNow={() => {}} onToggleEnabled={() => {}} />,
  )
  expect(screen.getByRole('link', { name: 'nightly-build' })).toHaveAttribute('href', '/schedules/s1')
})

test('ACTIONS carries an Edit link to the same place, named by row', () => {
  renderTable(
    <SchedulesTable schedules={[sched()]} pendingId={null} onRunNow={() => {}} onToggleEnabled={() => {}} />,
  )
  // Row identity in the accessible name, matching UsersTable.tsx:169-199, so a
  // multi-row table does not present several identically-named controls.
  const edit = screen.getByRole('link', { name: 'Edit nightly-build' })
  expect(edit).toHaveAttribute('href', '/schedules/s1')
  expect(edit).toHaveTextContent('Edit')
})

test('both entry points target the same href and there are exactly two per row', () => {
  renderTable(
    <SchedulesTable
      schedules={[sched(), sched({ id: 's2', name: 'weekly-clean' })]}
      pendingId={null}
      onRunNow={() => {}}
      onToggleEnabled={() => {}}
    />,
  )
  const toS1 = screen.getAllByRole('link').filter((a) => a.getAttribute('href') === '/schedules/s1')
  expect(toS1).toHaveLength(2)
  expect(screen.getByRole('link', { name: 'weekly-clean' })).toHaveAttribute('href', '/schedules/s2')
})

test('clicking Edit does NOT fire Run now or the enable toggle', async () => {
  const onRunNow = vi.fn()
  const onToggleEnabled = vi.fn()
  renderTable(
    <SchedulesTable
      schedules={[sched()]}
      pendingId={null}
      onRunNow={onRunNow}
      onToggleEnabled={onToggleEnabled}
    />,
  )
  await userEvent.click(screen.getByRole('link', { name: 'Edit nightly-build' }))
  expect(onRunNow).not.toHaveBeenCalled()
  expect(onToggleEnabled).not.toHaveBeenCalled()
})

test('Edit is a link, not a button, so middle-click and open-in-new-tab work', () => {
  renderTable(
    <SchedulesTable schedules={[sched()]} pendingId={null} onRunNow={() => {}} onToggleEnabled={() => {}} />,
  )
  // A useNavigate handler on a <button> would satisfy a naive "clicking Edit goes to
  // the page" test while silently breaking both affordances.
  expect(screen.queryByRole('button', { name: /^Edit/ })).toBeNull()
})

test('the ACTIONS cell still holds exactly nine cells per row after the Edit link', () => {
  renderTable(
    <SchedulesTable
      schedules={[sched(), sched({ id: 's2', name: 'weekly-clean' })]}
      pendingId={null}
      onRunNow={() => {}}
      onToggleEnabled={() => {}}
    />,
  )
  // The Edit link joins the existing ACTIONS cell; it must not become a tenth column,
  // which would desynchronise the grid template from the header row.
  expect(screen.getAllByRole('columnheader')).toHaveLength(9)
  const firstDataRow = screen.getAllByRole('row')[1]
  expect(within(firstDataRow).getAllByRole('cell')).toHaveLength(9)
})
```

In `web/src/schedules/SchedulesPage.test.tsx`, add `import { MemoryRouter } from 'react-router-dom'` and change every `renderWithQuery(<SchedulesPage />)` to:

```tsx
  renderWithQuery(
    <MemoryRouter>
      <SchedulesPage />
    </MemoryRouter>,
  )
```

No assertion in that file changes.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `npx vitest run src/schedules/SchedulesTable.test.tsx`

Expected: FAIL - `Unable to find an accessible element with the role "link" and name "nightly-build"` on the first new test, and the same for `Edit nightly-build`. The six re-wrapped shipped tests must still PASS at this point; if any of them changed behaviour, the wrapper edit was not wrapper-only.

- [ ] **Step 3: Implement**

In `web/src/schedules/SchedulesTable.tsx`, add `import { Link } from 'react-router-dom'` and replace the NAME cell body at `:55`:

```tsx
                <Link
                  to={`/schedules/${s.id}`}
                  className="truncate font-sans text-[13px] text-fg hover:text-accent"
                >
                  {s.name}
                </Link>
```

and append to the ACTIONS cell, after the Enable/Disable button at `:81-88`:

```tsx
                {/* A react-router <Link>, not a useNavigate handler on a button, so
                    middle-click and open-in-new-tab work and no callback has to be
                    threaded through this component's props. Row identity in the
                    accessible name, matching UsersTable.tsx:169-199. */}
                <Link
                  to={`/schedules/${s.id}`}
                  aria-label={`Edit ${s.name}`}
                  className="rounded-md border border-border bg-white/5 px-2.5 py-1 text-[11px] text-fg-mute"
                >
                  Edit
                </Link>
```

- [ ] **Step 4: Run the tests to verify they pass**

```
npx vitest run src/schedules/
```

Expected: PASS for the whole directory, including all ten shipped `SchedulesPage` tests and all ten shipped `SchedulesTable` tests with their assertions unchanged.

- [ ] **Step 5: Commit**

```bash
git add web/src/schedules/SchedulesTable.tsx web/src/schedules/SchedulesTable.test.tsx web/src/schedules/SchedulesPage.test.tsx
git commit -m "feat(web): link the Schedules list to the detail page from NAME and Edit"
```

---

## Task 11: Verification gate

- [ ] **Step 1: Full web suite**

From `web/`:

```
npm test
```

Expected: PASS, zero failures. The shipped baseline is 811 tests; this slice adds roughly 60, so expect about 870. Any pre-existing failure must be measured **both with and without** this change before it is called pre-existing - never merge past a red gate on the strength of an assumption.

- [ ] **Step 2: Type-check and production build**

From `web/`:

```
npm run build
```

Expected: `tsc -b` clean, then a successful `vite build`. A TypeScript error here that the test run did not catch is real: vitest transpiles without type-checking.

- [ ] **Step 3: Revert the build output**

`web/dist` is **tracked but stale** from the original scaffold and is not maintained per-PR. `npm run build` rewrites it and dirties the working tree. From the repo root:

```bash
git checkout -- web/dist/
git status --short
```

Expected: `web/dist/` shows no modifications. The only paths in the final change set are the ones this plan names.

- [ ] **Step 4: Go gate (proving no backend regression)**

From the repo root:

```
make test
```

Expected: PASS. This slice changes zero Go files, so a failure here is unrelated to it - but run it, and if it is red, get a number with and without the change rather than assuming.

`make test-integration` is **not** required: no Go file, no `.sql` file and no migration changed, so no `make generate` and no database surface is touched.

- [ ] **Step 5: Confirm the change set**

```bash
git status --short
git diff --stat origin/main...HEAD
```

Expected file set, and nothing else:

```
web/src/app/router.tsx
web/src/jobs/api.ts
web/src/jobs/api.test.ts
web/src/schedules/api.ts
web/src/schedules/api.test.ts
web/src/schedules/useSchedule.ts
web/src/schedules/useSchedule.test.tsx
web/src/schedules/useScheduleRuns.ts
web/src/schedules/useScheduleRuns.test.tsx
web/src/schedules/useScheduleActions.ts
web/src/schedules/useScheduleActions.test.tsx
web/src/schedules/ScheduleTriggerForm.tsx
web/src/schedules/ScheduleTriggerForm.test.tsx
web/src/schedules/ScheduleRunsPanel.tsx
web/src/schedules/ScheduleRunsPanel.test.tsx
web/src/schedules/ScheduleDetailPage.tsx
web/src/schedules/ScheduleDetailPage.test.tsx
web/src/schedules/ScheduleDetailPage.lifecycle.test.tsx
web/src/schedules/SchedulesTable.tsx
web/src/schedules/SchedulesTable.test.tsx
web/src/schedules/SchedulesPage.test.tsx
```

Plus, at the phase boundary, the `git mv` of `docs/backlog/idea-2026-06-05-schedule-detail-page.md` into `docs/backlog/closed/` performed by `/backlog close idea-2026-06-05-schedule-detail-page`. That `git mv` is required scope, not optional cleanup.

---

## Phase 6 proposals (propose, do NOT auto-file)

Four items, up from the spec's three. Each is a **proposal** for human accept.

1. `idea-2026-08-12-schedule-next-fires-preview.md` - a server-computed `next_fires` array (or `?preview=N`) on `GET /v1/scheduled-jobs/{id}` using the authoritative `robfig/cron/v3` parser, so the frontend can render N rows with no new dependency. A server-side human gloss of the cron could ride along.
2. `bug-2026-08-12-scheduled-job-detail-missing-owner-email.md` (low) - `handleGetScheduledJob` (`internal/api/scheduled_jobs.go:508-519`) never calls `fillOwnerEmails`, unlike both list arms (`:371`, `:504`), so the same response struct is populated by one handler and empty from another. One-line fix.
3. `idea-2026-08-12-schedule-job-spec-editor.md` - frontend-only; extract the JSON spec editor and `validateSpecText` from `NewJobPage` into a shared component and reuse it here, since `PATCH` already accepts `job_spec`.
4. `idea-2026-08-12-detail-page-state-triad-primitive.md` - extract the loading / not-found / retryable-error triad now duplicated in `WorkerDetailPage.tsx:30-55`, `JobDetailPage.tsx:57-78` and `ScheduleDetailPage.tsx`, gated on a zero-line diff to the three pages' existing test files.

No enabler is filed for the `queue` overlap policy (a scheduler product decision, not a UI gap), for `Pause`/`Resume` as distinct endpoints (no such routes; the mechanism is `PATCH { enabled }`), for a pager on recent runs (deliberately a window), or for renaming from the detail page (deferred).

---

## Self-review

**Spec coverage.** All fourteen acceptance criteria map to a task: 1 -> Tasks 2 and 7; 2 -> Task 10; 3 -> Task 7; 4 -> Tasks 1, 5 and 8; 5 -> Task 5; 6 -> Tasks 5 and 9; 7 -> Task 7; 8 -> Tasks 7 and 8; 9 -> Tasks 3 and 6; 10 -> Tasks 1 and 7; 11 -> Tasks 2, 3 and 7; 12 -> Tasks 2, 3 and 4; 13 -> Task 11; 14 -> the Phase 6 proposals section.

**Deviations from the spec, each with a reason.**

1. **The Next fire panel is refreshed by the invalidated GET refetch, not by writing the PATCH response into the cache.** The spec's testing note says the panel shows "the `next_run_at` from the PATCH response". Substantively identical - the value the PATCH computed is exactly what the immediately-following refetch returns - but achieved without a `setQueryData`, which keeps the "a settled mutation never writes back" rule literal. Task 8's MSW handler mutates its `current` row so the test models the real server.
2. **`web/src/schedules/api.test.ts` and `useScheduleActions.test.tsx` do get edits.** The spec says the re-expression and the additions must require zero edits to those two files. Held in substance: no existing test body or assertion changes; only the import statements grow to name the newly added functions, and new tests are appended. Stated so no reviewer reads a diff as a broken promise.
3. **`SchedulesTable.test.tsx` and `SchedulesPage.test.tsx` need a `MemoryRouter` wrapper** (Task 10). The spec lists them under "must stay green" without noting this. `renderWithQuery` provides no router, so a `<Link>` in `SchedulesTable` makes them throw `useHref() may be used only in the context of a <Router>`. The edit is wrapper-only; no assertion changes.
4. **The third-consumer rule is knowingly not applied to the detail-page state triad.** Rationale in the THIRD-CONSUMER FLAG section; a fourth Phase 6 proposal is added rather than silently shipping the copy.
5. **Tasks 8 and 9 have no implementation step.** Their behaviour is implemented in Task 5, so the plan replaces "run it and watch it fail" with a mandatory mutation-and-revert that proves the test discriminates, with both outputs recorded. Stated explicitly because a task whose RED cannot be a missing implementation must name its substitute evidence.

**Placeholder scan.** No TBD, no "handle edge cases", no "similar to Task N". Every code step carries the literal code. Every test step carries the literal test.

**Type consistency.** `SchedulePatch` is defined once in Task 1 and referenced by the same name in Tasks 4, 5 and 7. `getSchedule` / `updateSchedule` / `deleteSchedule` / `listJobsBySchedule` / `SCHEDULE_RUNS_LIMIT` / `useSchedule` / `useScheduleRuns` are spelled identically everywhere. `ScheduleTriggerForm`'s props (`schedule`, `pending`, `error`, `onSubmit`) match its call site in Task 7. `ScheduleRunsPanel`'s props (`runs`, `total`) match its call site in Task 7. The `data-testid`s `next-fire-rel` and `next-fire-abs` are introduced in Task 7 and used in Tasks 7 and 8. `useScheduleActions` returns `{ runNow, setEnabled, update, remove }` in Task 4 and is destructured that way in Task 7.
