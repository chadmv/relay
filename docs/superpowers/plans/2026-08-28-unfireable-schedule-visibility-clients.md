# A permanently un-fireable schedule must be visible - Implementation Plan, part 2 of 2 (clients)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**THIS IS THE SECOND HALF OF ONE PLAN. READ PART 1 FIRST:**
`docs/superpowers/plans/2026-08-28-unfireable-schedule-visibility.md`

Part 1 carries the goal, the architecture, the **slice independence declaration**, the six spec refutations, the standing constraints (S1 to S5), the file structure, and Slices A (backend, migration, response, README) and A2 (startup sweep), as Tasks A1 to A10. This file carries Slices B (SPA), C (CLI) and D (Python SDK), as Tasks B1 to D1, plus the verification gates and the conductor steps for the whole plan. It is split across two files because it is one plan too large for one document, following the same convention as `2026-08-20-grpc-admission-bounds-tasks-1-6.md` and `-tasks-7-12.md`.

**Spec:** `docs/superpowers/specs/2026-08-28-unfireable-schedule-visibility.md`
**Backlog item closed:** `docs/backlog/bug-2026-08-23-unfireable-schedule-is-invisible.md`

---

## The three constraints from part 1 you will get wrong if you skip them

Repeated here because this file may be handed to an engineer on its own.

**S1. `make` is NOT installed on this machine.** Every command below is the literal underlying command transcribed from the `Makefile`. Never type `make <target>`.

**S2. Never use em dashes or en dashes.** Not in code, comments, operator strings, README edits or commit messages. Hyphens only.

**S3. This is a CRLF repo and `git diff` and `git status` disagree by design.** After any programmatic edit to a tracked text file: `git diff --ignore-all-space`, `git status --short`, `git diff --stat`, and `git ls-files --eol <paths>` - every touched path must read `i/lf`. Never conclude "nothing to revert" from `git diff` alone.

**S5. Every test body below is a GUESS until you run it.** Each task states what makes its test RED at HEAD and what makes it GREEN. If the observed RED is not the stated RED, stop and report rather than adjusting the test until it passes.

## The wire contract these three slices consume

Frozen by Slice A, Task A6. Do not re-derive it:

```
last_error     string, omitempty
last_error_at  RFC3339 string, omitempty
```

**Absent means healthy.** Never `""`, never `null`. A schedule that has never failed carries neither key. The write site never stores an empty string precisely so that `omitempty` on a string is safe here.

## Dependency on Slice A

- **Tasks B1, B2, B3, C1, C2, C3, D1** are fixture-driven and complete against a tree without the column.
- **Task C4** needs Slice A's migration and response fields in the tree. If C4 runs before A lands, it fails at the SQL `UPDATE` with `column "last_error" of relation "scheduled_jobs" does not exist`.
- **Task B4** does NOT need Slice A, by design. See its Step 1 for why the failing row is injected rather than seeded.

**Before starting any B task:** `web/dist` is tracked but stale from the scaffold, and a frontend build dirties it. Run `git checkout -- web/dist/` before assembling anything from this slice, and never commit a `web/dist` change from a task other than B4's explicit restore step.

---

# Slice B - the SPA

Zero Go diff. Independent of C and D; dispatch all three concurrently.

## Task B1: The interface fields, and the rotted citations

**Files:**
- Modify: `web/src/schedules/api.ts`

- [ ] **Step 1: There is no test for a type declaration, and inventing one is worse than none**

This task adds two optional interface fields. The RED that matters is Task B2's and B3's: both open with a genuine test failure that depends on this declaration, and both fail to COMPILE without it (`Object literal may only specify known properties, and 'last_error' does not exist in type 'Partial<Schedule>'`). Do not write a test asserting that a TypeScript interface has a field.

- [ ] **Step 2: Add the fields**

In `web/src/schedules/api.ts`, in the `Schedule` interface, after `last_job_id`:

```ts
  last_job_id?: string
  // Why the last SCHEDULER fire failed, and when. ABSENT MEANS HEALTHY - the
  // server omits both keys entirely (scheduledJobResponse carries `omitempty` on
  // each), never "" and never null, so `schedule.last_error ? ... : null` is the
  // correct and only test. A schedule that has never failed renders exactly as
  // it did before these fields existed.
  //
  // THE TEXT IS OPERATOR-SUPPLIED, and partly attacker-chosen in the admin case:
  // it is derived from the stored job_spec and embeds a task name the schedule's
  // owner picked, and an admin can read any user's schedule. The server strips
  // control characters and truncates it, and the SPA must render it as a React
  // TEXT CHILD inside a panel whose heading names its provenance - never as
  // chrome, never through dangerouslySetInnerHTML, and never into a URL, a title
  // attribute or a log line. Same rule, same reason, as the Job spec panel on the
  // detail page.
  last_error?: string
  last_error_at?: string
```

- [ ] **Step 3: Convert the rotted line citations to symbols**

The spec's own R4 records that this file's citations have already rotted, and this slice moves one of the statements they point at. Replace, exactly:

| Current text | Replacement |
|---|---|
| `ownedScheduledJob hides rather than refuses (:147-169).` | `ownedScheduledJob hides rather than refuses.` |
| `patchScheduledJobRequest is all pointers (internal/api/scheduled_jobs.go:521-528).` | `patchScheduledJobRequest is all pointers.` |
| `changed or not (:585, :595).` | `changed or not - see handlePatchScheduledJob.` |
| `(internal/store/query/scheduled_jobs.sql:32-43)` | `(internal/store/query/scheduled_jobs.sql)` |
| `204 with no body (internal/api/scheduled_jobs.go:633); apiFetch returns undefined for 204 (lib/api.ts:57)` | `204 with no body (handleDeleteScheduledJob); apiFetch returns undefined for 204 (lib/api.ts)` |
| `(internal/store/migrations/000006_scheduled_jobs.up.sql:20-21)` | `(internal/store/migrations/000006_scheduled_jobs.up.sql)` |
| `the server recomputes anyway on a disabled -> enabled transition (internal/api/scheduled_jobs.go:585)` | `the server recomputes anyway on a disabled -> enabled transition (handlePatchScheduledJob)` |

**Add no new line-number citations anywhere in this slice.**

- [ ] **Step 4: `SchedulePatch` now has a SECOND consequence, and it is a new hazard**

The comment on `SchedulePatch` currently warns that sending an unchanged `cron_expr` pushes the next fire out. After Slice A it also **erases a live failure signal**. Replace the paragraph beginning `// SENDING A KEY YOU DID NOT CHANGE IS NOT A NO-OP.` with:

```ts
// SENDING A KEY YOU DID NOT CHANGE IS NOT A NO-OP, AND THERE ARE NOW TWO
// CONSEQUENCES. next_run_at is recomputed from time.Now() whenever the body
// merely CARRIES cron_expr or timezone, changed or not - see
// handlePatchScheduledJob - so re-sending an unchanged cron on an `@every 1h`
// schedule whose next fire is five minutes away pushes that fire out by 55
// minutes. AND a body carrying job_spec, cron_expr or timezone CLEARS
// last_error/last_error_at, on the reasoning that the handler validated the new
// values before storing them so any record about the OLD ones is stale by
// construction. Re-sending an unchanged cron therefore also erases the only
// signal that the schedule is broken. Always build this from a diff against the
// loaded row, never from the whole form.
```

- [ ] **Step 5: Type check**

```powershell
cd web
npx tsc -b
cd ..
```

Expected: no output.

- [ ] **Step 6: Commit**

```powershell
git ls-files --eol web/src/schedules/api.ts
git add web/src/schedules/api.ts
git commit -m "feat(web): Schedule carries last_error and last_error_at, and api.ts citations name symbols"
```

---

## Task B2: The FAILING chip in the schedules list

**No tenth column.** `SchedulesTable` already carries nine columns and a 1040px minimum width, the widest in the app, and its own `COLS`/`MIN_W` comment says so: 580px of fixed track before any `fr` gets a pixel.

**Files:**
- Modify: `web/src/schedules/SchedulesTable.tsx`
- Modify: `web/src/schedules/SchedulesTable.test.tsx`

- [ ] **Step 1: Write the failing test**

Append to `web/src/schedules/SchedulesTable.test.tsx` (`screen`, `within`, `renderTable` and `sched` are already in scope at the top of that file):

```tsx
// A CHIP IN THE NAME CELL, NOT A TENTH COLUMN. COLS already has nine tracks and
// 580px of fixed width before any fr gets a pixel - the worst case in the app -
// so a tenth would push the 1040px floor up again. The NAME cell is already
// `flex min-w-0 items-center gap-2` and already holds the status dot, so a chip
// fits without touching the grid template at all.
//
// TEXT, NOT A COLOUR CHANGE TO THE DOT. A bare colour is not accessible, and the
// dot's two states are already spoken for by `enabled` - failure is a separate
// axis from the operator's own on/off setting and gets a separate element.
test('a schedule carrying last_error shows a FAILING chip inside the NAME cell', () => {
  renderTable(
    <SchedulesTable
      schedules={[sched({ last_error: 'task render: retries must be between 0 and 10' })]}
      pendingId={null}
      onRunNow={() => {}}
      onToggleEnabled={() => {}}
    />,
  )
  const link = screen.getByRole('link', { name: 'nightly-build' })
  const cell = link.closest('[role="cell"]')
  expect(cell).not.toBeNull()
  expect(within(cell as HTMLElement).getByText('FAILING')).toBeInTheDocument()
})

// THE ABSENCE CASE. A healthy schedule's row must be identical to what it was
// before this slice: the chip is the only new element and it must not render.
test('a healthy schedule renders no FAILING chip', () => {
  renderTable(
    <SchedulesTable schedules={[sched()]} pendingId={null} onRunNow={() => {}} onToggleEnabled={() => {}} />,
  )
  expect(screen.queryByText('FAILING')).toBeNull()
})

// THE ENABLED AXIS IS UNTOUCHED, deliberately. A failing schedule IS still
// enabled - relay does not auto-disable one, see the spec's section 9.1 - so the
// dot and the Disable button keep telling the truth about the operator's own
// setting, and the chip carries the other axis.
test('a failing schedule is still rendered as enabled', () => {
  renderTable(
    <SchedulesTable
      schedules={[sched({ enabled: true, last_error: 'task render: retries must be between 0 and 10' })]}
      pendingId={null}
      onRunNow={() => {}}
      onToggleEnabled={() => {}}
    />,
  )
  expect(screen.getByRole('button', { name: 'Disable' })).toBeInTheDocument()
  expect(screen.getByText('FAILING')).toBeInTheDocument()
})

// PER-ROW, NOT PER-TABLE. Without this the first test would pass on an
// implementation that marked every row.
test('only the failing row carries the chip', () => {
  renderTable(
    <SchedulesTable
      schedules={[
        sched({ id: 's1', name: 'broken', last_error: 'task render: retries must be between 0 and 10' }),
        sched({ id: 's2', name: 'fine' }),
      ]}
      pendingId={null}
      onRunNow={() => {}}
      onToggleEnabled={() => {}}
    />,
  )
  expect(screen.getAllByText('FAILING')).toHaveLength(1)
  const fineCell = screen.getByRole('link', { name: 'fine' }).closest('[role="cell"]')
  expect(within(fineCell as HTMLElement).queryByText('FAILING')).toBeNull()
})
```

- [ ] **Step 2: Run test to verify it fails**

```powershell
cd web
npx vitest run src/schedules/SchedulesTable.test.tsx
cd ..
```

Expected: tests 1, 3 and 4 FAIL with `Unable to find an element with the text: FAILING`. Test 2 passes vacuously and becomes non-vacuous once the chip exists.

If the run instead fails with a TypeScript error on `last_error`, Task B1 has not landed. Do that first.

- [ ] **Step 3: Write the implementation**

In `web/src/schedules/SchedulesTable.tsx`, replace the NAME `TableCell`:

```tsx
              <TableCell className="flex min-w-0 items-center gap-2">
                <span className={`h-1.5 w-1.5 shrink-0 rounded-full ${s.enabled ? 'bg-ok' : 'bg-fg-dim'}`} />
                <Link
                  to={`/schedules/${s.id}`}
                  className="truncate font-sans text-[13px] text-fg hover:text-accent"
                >
                  {s.name}
                </Link>
                {/* THE FAILURE MARKER LIVES INSIDE THE NAME CELL RATHER THAN IN A
                    TENTH COLUMN. COLS above is already nine tracks with 580px of
                    fixed width, the worst case in the app; a tenth would push
                    MIN_W up again and this table is already the app's widest.
                    This cell is already a flex row with a gap, so the chip costs
                    no grid change at all.

                    TEXT, NOT A COLOUR. A bare colour is not accessible, and the
                    dot's two states are already spoken for by `enabled`. A
                    failing schedule IS still enabled - relay does not
                    auto-disable one - so this is a second, independent axis with
                    its own element.

                    `shrink-0` is load-bearing: the Link beside it is `truncate`,
                    so under a narrow viewport the NAME should lose characters
                    before the marker disappears. Measured in a real browser by
                    web/e2e/layout.spec.ts's `schedules-failing` surface; jsdom
                    performs no layout and can say nothing about it. */}
                {s.last_error ? (
                  <span
                    title="The scheduler could not produce a job from this schedule. Open it for the reason."
                    className="shrink-0 rounded-full border border-err/40 bg-err/10 px-1.5 py-0.5 text-[9.5px] tracking-wider text-err"
                  >
                    FAILING
                  </span>
                ) : null}
              </TableCell>
```

- [ ] **Step 4: Run test to verify it passes**

```powershell
cd web
npx vitest run src/schedules/SchedulesTable.test.tsx
cd ..
```

Expected: PASS, all tests in the file including the six pre-existing ones.

- [ ] **Step 5: Commit**

```powershell
git checkout -- web/dist/
git ls-files --eol web/src/schedules/SchedulesTable.tsx web/src/schedules/SchedulesTable.test.tsx
git add web/src/schedules/SchedulesTable.tsx web/src/schedules/SchedulesTable.test.tsx
git commit -m "feat(web): FAILING chip in the schedules list NAME cell"
```

---

## Task B3: The detail page sub-line marker and the Last failure panel

**Files:**
- Modify: `web/src/schedules/ScheduleDetailPage.tsx`
- Modify: `web/src/schedules/ScheduleDetailPage.test.tsx`

- [ ] **Step 1: Write the failing test**

Append to `web/src/schedules/ScheduleDetailPage.test.tsx` (`server`, `handlers`, `sched`, `renderPage` and `screen` are already in scope):

```tsx
const FAILURE = 'task render: retries must be between 0 and 10'

// THE SUB-LINE MARKER is what makes the failure visible WITHOUT SCROLLING. The
// identity line already reads created / updated / next fire / last run, and a
// dead schedule's tell is that "last run" stopped moving while "next fire" kept
// going - a pair the reader has to interpret. "last failure 4 minutes ago"
// beside "last run 22 days ago" is the sentence an operator understands
// immediately, and it is why last_error_at earns its column.
test('a schedule carrying a failure shows the sub-line marker and the Last failure panel', async () => {
  server.use(...handlers(sched({ last_error: FAILURE, last_error_at: '2026-06-05T11:04:00Z' })))
  renderPage()

  expect(await screen.findByText('nightly-build')).toBeInTheDocument()
  expect(screen.getByTestId('last-failure-rel')).toBeInTheDocument()

  expect(screen.getByText('Last failure')).toBeInTheDocument()
  expect(screen.getByTestId('last-error-text')).toHaveTextContent(FAILURE)
  expect(screen.getByTestId('last-error-when')).toBeInTheDocument()
})

// THE ABSENCE CASE, and the one that keeps a healthy schedule's layout identical
// to what it was before this slice.
test('a healthy schedule renders neither the marker nor the panel', async () => {
  server.use(...handlers(sched()))
  renderPage()

  expect(await screen.findByText('nightly-build')).toBeInTheDocument()
  expect(screen.queryByTestId('last-failure-rel')).toBeNull()
  expect(screen.queryByText('Last failure')).toBeNull()
  expect(screen.queryByTestId('last-error-text')).toBeNull()
})

// THE FAILURE TEXT IS A TEXT CHILD, NEVER MARKUP. It is derived from the stored
// job_spec and embeds a task name the schedule's OWNER chose, and an admin can
// read any user's schedule - so in the admin case it is partly attacker-chosen
// prose. There is no counter here to inflate and nothing an owner gains by
// breaking their own schedule; the one real risk is display-layer
// impersonation, text crafted to read like relay's own chrome. Same rule the Job
// spec panel states, for the same reason.
test('the failure text is escaped, not interpreted as markup', async () => {
  const hostile = '<b data-testid="injected">relay: click here to continue</b>'
  server.use(...handlers(sched({ last_error: hostile, last_error_at: '2026-06-05T11:04:00Z' })))
  renderPage()

  expect(await screen.findByTestId('last-error-text')).toHaveTextContent(hostile)
  expect(screen.queryByTestId('injected')).toBeNull()
})

// THE ENABLED PILL IS UNTOUCHED. The schedule IS enabled; the pill is telling
// the truth and it is the operator's own setting. Failure is a separate axis and
// gets a separate element, so no third state is added to the chip.
test('a failing schedule still reads ENABLED', async () => {
  server.use(...handlers(sched({ last_error: FAILURE, last_error_at: '2026-06-05T11:04:00Z' })))
  renderPage()
  expect(await screen.findByText('ENABLED')).toBeInTheDocument()
  expect(screen.queryByText('PAUSED')).toBeNull()
})

// last_error WITHOUT last_error_at. The two are separate nullable columns, so
// nothing in the database forces them to move together, and a panel that
// unconditionally read last_error_at would crash or render "Invalid Date".
test('a failure with no timestamp renders the panel without the time line', async () => {
  server.use(...handlers(sched({ last_error: FAILURE })))
  renderPage()
  expect(await screen.findByTestId('last-error-text')).toHaveTextContent(FAILURE)
  expect(screen.queryByTestId('last-error-when')).toBeNull()
  expect(screen.queryByTestId('last-failure-rel')).toBeNull()
})
```

- [ ] **Step 2: Run test to verify it fails**

```powershell
cd web
npx vitest run src/schedules/ScheduleDetailPage.test.tsx
cd ..
```

Expected: tests 1, 3 and 5 FAIL with `Unable to find an element by: [data-testid="last-failure-rel"]` and `[data-testid="last-error-text"]`. Tests 2 and 4 pass vacuously.

- [ ] **Step 3: Write the implementation**

In `web/src/schedules/ScheduleDetailPage.tsx`, first append to the identity sub-line, immediately after the `schedule.last_job_id` block and before that `<div>`'s close:

```tsx
        {schedule.last_error_at ? (
          <>
            {' '}
            · last failure{' '}
            <span data-testid="last-failure-rel" className="text-err">
              {formatRelativeTime(schedule.last_error_at)}
            </span>
          </>
        ) : null}
```

Then insert the panel at the TOP of the right-hand column, above "Next fire":

```tsx
        <div className="flex flex-col gap-3">
          {/* TOP OF THE COLUMN, and conditional, so a healthy schedule's layout is
              unchanged. The heading names the text's PROVENANCE because the text
              is operator-supplied: it is derived from the stored job_spec and
              embeds a task name the schedule's owner chose, and an admin reading
              someone else's schedule is reading partly attacker-chosen prose.
              There is no counter here to inflate and nothing an owner gains by
              breaking their own schedule; the one real risk is display-layer
              impersonation, text crafted to read like relay's own chrome. So it
              renders as a React TEXT CHILD in a <pre>, never through
              dangerouslySetInnerHTML, and it goes into no URL, no title
              attribute and no log line. Same rule as the Job spec panel below.

              The remedy copy names Run now first because it returns the
              UNTRUNCATED message: the stored value is capped at 1 KB server-side
              and this is the only place that says so. */}
          {schedule.last_error ? (
            <Panel title="Last failure" meta="FROM THE STORED JOB SPEC">
              <div className="flex flex-col gap-2 px-4 py-3">
                <pre
                  data-testid="last-error-text"
                  className="max-h-[180px] overflow-auto whitespace-pre-wrap break-words font-mono text-[11px] leading-relaxed text-err"
                >
                  {schedule.last_error}
                </pre>
                {schedule.last_error_at ? (
                  <span data-testid="last-error-when" className="font-mono text-[11px] text-fg-mute">
                    {formatRelativeTime(schedule.last_error_at)} · {formatStarted(schedule.last_error_at)}
                  </span>
                ) : null}
                <span className="text-[11px] text-fg-mute">
                  The scheduler re-validates the stored job spec on every fire. Use Run now to re-check and
                  see the message in full, then repair the spec, or disable the schedule if it should not run.
                </span>
              </div>
            </Panel>
          ) : null}

          <Panel title="Next fire" meta="NEXT_RUN_AT">
```

`formatRelativeTime`, `formatStarted` and `Panel` are already imported at the top of the file; add no imports.

- [ ] **Step 4: Run test to verify it passes**

```powershell
cd web
npx vitest run src/schedules/
cd ..
```

Run the whole directory, not the one file: `ScheduleDetailPage.lifecycle.test.tsx` and `ScheduleDetailPage.transition.test.tsx` render the same page, and a new conditional panel can change what an unscoped locator resolves to.

Expected: PASS.

- [ ] **Step 5: Full frontend unit lane and type check**

```powershell
cd web
npx tsc -b
npm test
cd ..
```

Expected: type check clean; `npm test` (which is `vitest run` over the whole suite) all green. Both run in CI via `web-ci.yml`.

- [ ] **Step 6: Commit**

```powershell
git checkout -- web/dist/
git ls-files --eol web/src/schedules/ScheduleDetailPage.tsx web/src/schedules/ScheduleDetailPage.test.tsx
git add web/src/schedules/ScheduleDetailPage.tsx web/src/schedules/ScheduleDetailPage.test.tsx
git commit -m "feat(web): Last failure panel and identity sub-line marker on the schedule detail page"
```

---

## Task B4: The browser lane, measured against a populated table

**This is a real task with a real command, not an aside.** jsdom performs NO layout: every `offsetWidth`, `scrollWidth` and `getBoundingClientRect()` across `web/src`'s test files returns 0, and `web/e2e/layout.spec.ts` is the only place in the repo where a width is a real number. A chip added inside a `1.4fr` track in a nine-column grid at a 1040px floor is exactly the horizontal-overflow class that 890 green jsdom unit tests were once silent about on this project.

**Files:**
- Modify: `web/e2e/surfaces.ts`
- Modify: `web/e2e/layout.spec.ts`

- [ ] **Step 1: Understand why the failing row is INJECTED and not SEEDED. Read before writing**

`web/e2e/fixtures.ts` carries a written rule: fixtures are created through the REST API, the exact path the SPA uses, and **not** by direct SQL, because a direct-SQL fixture could encode a state production cannot produce.

**No REST path can produce a `last_error`.** `handleCreateScheduledJob` and `handlePatchScheduledJob` both run `jobspec.Validate` before storing, so a spec that fails validation cannot be written through either. The state is produced only by the schedrunner, from a row an earlier release stored under a rule a later release tightened.

The two ways to seed one for real are both worse than the alternative:

- **Direct SQL from `web/e2e/global.setup.ts`** needs `pg` in a `.ts` file. `web/tsconfig.json` includes `e2e` under `strict`, and `pg` ships no types - `ensure-db.mjs` is `.mjs` specifically "so `pg` needs no @types". It costs a new `@types/pg` devDependency and a lockfile change, to check a layout property.
- **Waiting for a real tick** means planting an invalid spec by SQL anyway (same problem) and then racing a 10-second ticker.

So this surface **intercepts the real server's real list response and adds `last_error` to one real seeded row.** The request, the response envelope, every other field, the router, the query client, the production CSS bundle and the layout are all real; one field's value is not. That is honest for what this lane is for here, which is **layout**. The wire contract is pinned in CI by `internal/api/scheduled_jobs_response_test.go` (untagged) and end to end by `internal/cli/schedules_failure_integration_test.go` (Task C4, which runs in CI). Write that argument into the code, not just here.

- [ ] **Step 2: Add the `prepare` hook**

In `web/e2e/surfaces.ts`, add a field to the `Surface` interface, immediately after `ready`:

```ts
  // Optional per-surface setup run BEFORE page.goto, for a surface whose state
  // the seeded REST fixtures cannot produce. A function of Page and Seed for the
  // same collection-vs-execution reason as `path` and `ready`.
  //
  // USE THIS SPARINGLY AND JUSTIFY IT AT THE CALL SITE. fixtures.ts's rule is
  // that fixtures go through the REST API so a surface cannot assert about a
  // state production cannot produce. A `prepare` that fabricates data is a
  // deliberate exception and must name (a) why no REST path can produce the
  // state and (b) where the real wire contract for that state is pinned instead.
  prepare?: (page: Page, seed: Seed) => Promise<void>
```

- [ ] **Step 3: Add the injector and the surface**

Above `export function surfaces()`, in the same file:

```ts
// injectScheduleFailure rewrites the REAL GET /v1/scheduled-jobs response so ONE
// real seeded row carries a last_error, letting the schedules list be measured
// with the FAILING chip present.
//
// WHY IT IS NOT SEEDED THROUGH THE REST API like every other fixture in this
// harness: it cannot be. handleCreateScheduledJob and handlePatchScheduledJob
// both run jobspec.Validate BEFORE storing, so a spec that fails validation
// cannot be written through either. last_error is produced only by schedrunner,
// from a row an earlier release stored under a rule a later release tightened.
// The alternatives are a direct-SQL seed (which needs `pg` in a .ts file, and
// web/tsconfig.json type-checks e2e/ under strict while pg ships no types) or
// racing the 10-second ticker after planting an invalid spec by SQL. Both cost
// more than the property is worth HERE.
//
// WHAT THIS SURFACE IS FOR IS LAYOUT, and the interception leaves layout
// entirely real: a real request to a real server, a real response envelope,
// every other field real, the real router, the real query client, the real
// production CSS bundle. One field's value is fabricated.
//
// WHERE THE REAL CONTRACT IS PINNED INSTEAD, both in CI:
//   - internal/api/scheduled_jobs_response_test.go (untagged, go-ci `test` job):
//     the field names, absent-not-zero, and the row/response arity.
//   - internal/cli/schedules_failure_integration_test.go (go-ci
//     `cli-integration` job): last_error planted in a real database and read
//     back through a real internal/api server over HTTP.
//
// THE ROUTE STAYS INSTALLED FOR THE PAGE'S LIFETIME, which matters: useSchedules
// sets a refetchInterval, so the list re-fetches every few seconds and an
// interception that fired once would let the chip vanish mid-measurement.
async function injectScheduleFailure(page: Page, scheduleName: string): Promise<void> {
  await page.route(/\/v1\/scheduled-jobs\?/, async (route) => {
    const response = await route.fetch()
    const body = (await response.json()) as { items?: Array<Record<string, unknown>> }
    let hits = 0
    for (const item of body.items ?? []) {
      if (item.name === scheduleName) {
        item.last_error = 'task nightly: retries must be between 0 and 10'
        item.last_error_at = new Date().toISOString()
        hits++
      }
    }
    // A SILENT ZERO-HIT INTERCEPTION would make this surface measure the HEALTHY
    // table while wearing the failing one's name - the empty-table misdiagnosis
    // this whole file exists to avoid, one level up. `ready` gates on the chip
    // too; this throw is belt and braces with a message that names the cause.
    if (hits !== 1) {
      throw new Error(
        `injectScheduleFailure matched ${hits} rows named ${JSON.stringify(scheduleName)}, want exactly 1`,
      )
    }
    await route.fulfill({ response, json: body })
  })
}
```

And the surface entry, immediately after the existing `schedules` entry inside `surfaces()`:

```ts
    {
      // THE SAME PATH as `schedules` above, deliberately. The question is what
      // the FAILING chip does to a nine-column grid at a 1040px floor - the
      // widest table in the app, 580px of fixed track before any fr gets a
      // pixel. The healthy surface above is the CONTROL: if both overflow, the
      // chip is not the cause.
      name: 'schedules-failing',
      path: () => '/schedules',
      population: 'populated',
      prepare: (p, seed) => injectScheduleFailure(p, seed.scheduleName),
      ready: async (p, seed) => {
        // GATED ON THE CHIP, not merely on the row. A ready() that waited only
        // for the schedule link would pass on a table with no chip in it at all,
        // which is a measurement of the HEALTHY state wearing this surface's
        // name - the "measure the populated state" lesson applied to the
        // specific population this surface claims.
        //
        // exact:true on the link for the same reason the surface above uses it:
        // SchedulesTable also renders an aria-label="Edit ${name}" link per row,
        // so the substring-matching default resolves two elements here.
        const row = p
          .getByRole('row')
          .filter({ has: p.getByRole('link', { name: seed.scheduleName, exact: true }) })
        await expect(row.getByText('FAILING')).toBeVisible()
      },
    },
```

- [ ] **Step 4: Call the hook**

In `web/e2e/layout.spec.ts`, immediately before `const path = s.path(seed)`:

```ts
        if (s.prepare) {
          await s.prepare(page, seed)
        }
        const path = s.path(seed)
```

- [ ] **Step 5: Run the browser lane**

The build order is load-bearing. `web/embed.go` snapshots `web/dist` at compile time (`//go:embed all:dist`), so `relay-server` must be rebuilt AFTER the SPA build or it serves the previous bundle - or, from a clean checkout, the 7-line "has not been built" placeholder, which makes every spec fail with no `#root` and no obvious cause.

Requires: node, go, Playwright browsers installed once (`cd web; npx playwright install chromium webkit`), and a Postgres at `postgres://relay:relay@127.0.0.1:5432` - the `relay-postgres` container `scripts/dev.ps1` manages.

This is `make test-e2e` transcribed:

```powershell
cd web
npm run build
cd ..
go build -o bin/relay-server.exe ./cmd/relay-server
cd web
npm run test:e2e
cd ..
git checkout -- web/dist/index.html
```

Expected at this point: **PASS**, because Task B2 already added the chip. To establish the new surface is not decorative, run Step 6.

If it fails with `injectScheduleFailure matched 0 rows`, the seeded schedule did not appear on the first page - check `seedAll` still creates it and that the 50-row page is not cursored past it.

- [ ] **Step 6: Prove the new surface measures something**

Temporarily change `MIN_W` in `web/src/schedules/SchedulesTable.tsx` from `min-w-[1040px]` to `min-w-[2400px]`, then rebuild and re-run the five commands in Step 5.

Expected: FAIL on **both** `schedules` and `schedules-failing`, at 320px and 375px, with `<main> overflows at 320px`. Both, because the mutation is in the shared constant. That establishes the harness reaches this page and this surface; it does not establish that the chip specifically is measured, which nothing short of a chip that actually overflows can.

Revert:

```powershell
git checkout -- web/src/schedules/SchedulesTable.tsx
```

- [ ] **Step 7: Open the artifacts. An artifact nobody opens is worth nothing**

`layout.spec.ts` writes one full-page PNG per surface per width on every run, pass or fail, and the merge of that harness's own slice included a process commitment to a human pass over them. Open the three `schedules-failing-*.png` under `web/test-results/` and check the chip does not collide with the truncated name or push the ACTIONS column off screen at 320px. **Say in the task report that you looked, and what you saw.**

- [ ] **Step 8: Commit**

```powershell
git status --short
git checkout -- web/dist/
git ls-files --eol web/e2e/surfaces.ts web/e2e/layout.spec.ts
git add web/e2e/surfaces.ts web/e2e/layout.spec.ts
git commit -m "test(web): measure the schedules list with a FAILING chip present, in a real browser"
```

`git status --short` before the commit must show no `web/dist` entry other than a restored `index.html`, and no `bin/` artifact.

---

# Slice C - the CLI

Independent of B and D. **Task C4 needs Slice A in the tree.**

## Task C1: `scheduleResp` fields, and what `schedules show` prints

**Files:**
- Modify: `internal/cli/schedules.go`
- Modify: `internal/cli/schedules_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/cli/schedules_test.go`:

```go
// THE FIXTURE BODY IS HAND-WRITTEN JSON, NOT relayclient.PageEnvelope[scheduleResp]
// AND NOT scheduleResp. A fixture marshalled through the CLI's own response
// struct agrees with the decoder by construction, on the field names AND on the
// omitempty behaviour, so it can never detect drift in either direction. This
// file already carries three such vacuous fixtures; do not add a fourth. The
// exemplar to copy is writeTaskLogPage's locally-declared logRow in
// internal/cli/logs_test.go - read that one narrowly, since the same file gets
// it wrong 23 times for its job bodies.
func TestSchedulesShow_PrintsLastRunAndTheFailureWithItsProvenance(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "GET", r.Method)
		require.Equal(t, "/v1/scheduled-jobs/abc", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"id":"abc","name":"nightly","cron_expr":"@hourly","timezone":"UTC",
			"overlap_policy":"skip","enabled":true,
			"next_run_at":"2099-01-01T00:00:00Z",
			"last_run_at":"2026-08-01T03:00:00Z",
			"last_error":"task t: retries must be between 0 and 10",
			"last_error_at":"2026-08-28T11:59:00Z"
		}`)
	}))
	defer srv.Close()
	cfg := &Config{ServerURL: srv.URL, Token: "tkn"}

	var buf bytes.Buffer
	require.NoError(t, doSchedules(context.Background(), cfg, []string{"show", "abc"}, &buf))
	out := buf.String()

	// LAST RUN IS THE OTHER HALF OF THE SIGNAL. Without it a failing schedule
	// looks identical to a healthy one in the single command an operator runs to
	// inspect a schedule: Next keeps advancing and nothing says the last actual
	// run was three weeks ago. It is one line and the field was already in the
	// struct, unprinted.
	require.Contains(t, out, "Last run:")
	require.Contains(t, out, "2026-08-01T03:00:00Z")

	// THE PROVENANCE PREFIX IS PART OF THE CONTRACT, not decoration. The text is
	// derived from the stored job_spec and embeds a task name the schedule's
	// owner chose, so an admin inspecting another user's schedule is reading
	// partly attacker-chosen prose. Naming where it came from is what stops
	// crafted text reading like relay's own output.
	require.Contains(t, out, "Last error (from the stored job_spec")
	require.Contains(t, out, "task t: retries must be between 0 and 10")
	require.Contains(t, out, "2026-08-28T11:59:00Z")

	// THE REMEDY MUST BE NAMED WHERE THE SIGNAL IS READ, and it must be a
	// command that exists. run-now returns the UNTRUNCATED message; the stored
	// value is capped at 1 KB.
	require.Contains(t, out, "relay schedules run-now abc")
}

// THE ABSENCE CASE. A healthy schedule's output must not grow an empty label:
// absent means healthy and there is nothing to say.
func TestSchedulesShow_HealthyScheduleMentionsNoFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"id":"abc","name":"nightly","cron_expr":"@hourly","timezone":"UTC",
			"overlap_policy":"skip","enabled":true,
			"next_run_at":"2099-01-01T00:00:00Z"
		}`)
	}))
	defer srv.Close()
	cfg := &Config{ServerURL: srv.URL, Token: "tkn"}

	var buf bytes.Buffer
	require.NoError(t, doSchedules(context.Background(), cfg, []string{"show", "abc"}, &buf))
	out := buf.String()

	require.NotContains(t, out, "Last error")
	require.NotContains(t, out, "run-now")
	require.NotContains(t, out, "Last run:",
		"an absent last_run_at prints nothing, matching how Next is already handled")
	// CONTROL: the command still works and still prints the fields it always did.
	require.Contains(t, out, "Name:     nightly")
	require.Contains(t, out, "Enabled:  true")
}
```

- [ ] **Step 2: Run test to verify it fails**

```powershell
go test ./internal/cli/... -run "TestSchedulesShow_PrintsLastRunAndTheFailureWithItsProvenance|TestSchedulesShow_HealthyScheduleMentionsNoFailure" -v -timeout 60s
```

Expected: the first FAILS at `require.Contains(t, out, "Last run:")`. The second passes vacuously.

- [ ] **Step 3: Write the implementation**

In `internal/cli/schedules.go`, extend `scheduleResp`:

```go
type scheduleResp struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	CronExpr      string     `json:"cron_expr"`
	Timezone      string     `json:"timezone"`
	OverlapPolicy string     `json:"overlap_policy"`
	Enabled       bool       `json:"enabled"`
	NextRunAt     *time.Time `json:"next_run_at,omitempty"`
	LastRunAt     *time.Time `json:"last_run_at,omitempty"`
	// Absent means healthy. The server omits both keys entirely, never "" and
	// never null, so a nil pointer is the whole test.
	//
	// NOTE THIS STRUCT IS ALREADY A LOSSY VIEW of scheduledJobResponse: it
	// carries no owner_email and no last_job_id. That is pre-existing and this
	// slice does not fix it. It adds these two because a schedule that has
	// silently stopped producing jobs is otherwise indistinguishable from a
	// working one in every CLI output there is.
	LastError   *string    `json:"last_error,omitempty"`
	LastErrorAt *time.Time `json:"last_error_at,omitempty"`
}
```

And replace the tail of `doSchedulesShow`, from the `NextRunAt` block to the end:

```go
	if out.NextRunAt != nil {
		fmt.Fprintf(w, "Next:     %s\n", out.NextRunAt.Format(time.RFC3339))
	}
	if out.LastRunAt != nil {
		fmt.Fprintf(w, "Last run: %s\n", out.LastRunAt.Format(time.RFC3339))
	}
	if out.LastError != nil && *out.LastError != "" {
		// THE PROVENANCE PREFIX IS DELIBERATE. The message is derived from the
		// stored job_spec and embeds a task name the schedule's owner chose, so
		// an admin inspecting another user's schedule is reading partly
		// attacker-chosen prose, and the one real risk is text crafted to read
		// like relay's own output. Naming where it came from is what closes that.
		// The server has already stripped control characters at the write site,
		// which closes ANSI escape injection into this terminal.
		fmt.Fprintf(w, "Last error (from the stored job_spec, operator-supplied): %s\n", *out.LastError)
		if out.LastErrorAt != nil {
			fmt.Fprintf(w, "Failed at: %s\n", out.LastErrorAt.Format(time.RFC3339))
		}
		// The stored text is truncated to 1 KB; run-now returns it in full and
		// re-checks the spec live. Naming a command that exists is the point:
		// before `relay schedules update --spec`, the fix this signal points at
		// was reachable only from the Python SDK or curl.
		fmt.Fprintf(w, "Re-check with: relay schedules run-now %s\n", out.ID)
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

```powershell
go test ./internal/cli/... -timeout 120s
```

Expected: PASS, including the pre-existing schedules tests.

- [ ] **Step 5: Commit**

```powershell
git ls-files --eol internal/cli/schedules.go internal/cli/schedules_test.go
git add internal/cli/schedules.go internal/cli/schedules_test.go
git commit -m "feat(cli): relay schedules show prints the last run and the recorded failure"
```

---

## Task C2: A STATE column on `relay schedules list`

**Files:**
- Modify: `internal/cli/schedules.go`
- Modify: `internal/cli/schedules_test.go`

- [ ] **Step 1: Write the failing test**

```go
// A SEVENTH COLUMN, NOT A MARKER APPENDED TO THE NEXT CELL. The spec left the
// choice to the planner; a separate column is taken because NEXT is a timestamp
// that internal/cli/schedules_integration_test.go matches with a regex, and
// appending prose to it would make one cell mean two things. tabwriter has no
// layout budget to blow, unlike the SPA's nine-column grid, so the argument that
// forced a chip there does not apply here.
//
// It must be TEXT and it must be visible WITHOUT --json: the whole point is that
// an operator scanning the list sees WHICH schedule to suspect. run-now already
// explains one you have already picked out.
func TestSchedulesList_StateColumnDistinguishesAFailingSchedule(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// HAND-WRITTEN, not marshalled through relayclient.PageEnvelope[scheduleResp].
		_, _ = io.WriteString(w, `{"items":[
			{"id":"s1","name":"broken","cron_expr":"@hourly","timezone":"UTC","enabled":true,
			 "next_run_at":"2099-01-01T00:00:00Z",
			 "last_error":"task t: retries must be between 0 and 10",
			 "last_error_at":"2026-08-28T12:00:00Z"},
			{"id":"s2","name":"fine","cron_expr":"@hourly","timezone":"UTC","enabled":true,
			 "next_run_at":"2099-01-01T00:00:00Z"}
		],"next_cursor":"","total":2}`)
	}))
	defer srv.Close()
	cfg := &Config{ServerURL: srv.URL, Token: "tkn"}

	var buf bytes.Buffer
	require.NoError(t, doSchedules(context.Background(), cfg, []string{"list"}, &buf))
	out := buf.String()

	require.Contains(t, out, "STATE", "the header must name the new column")
	require.Contains(t, out, "FAILING")

	// PER-ROW, NOT PER-TABLE. Without this the two assertions above would pass on
	// an implementation that marked every row.
	var brokenLine, fineLine string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "broken") {
			brokenLine = l
		}
		if strings.Contains(l, "fine") {
			fineLine = l
		}
	}
	require.NotEmpty(t, brokenLine)
	require.NotEmpty(t, fineLine)
	require.Contains(t, brokenLine, "FAILING")
	require.NotContains(t, fineLine, "FAILING",
		"a healthy row must not be marked, or the marker tells an operator nothing")
	require.Contains(t, fineLine, "OK")
}
```

- [ ] **Step 2: Run test to verify it fails**

```powershell
go test ./internal/cli/... -run TestSchedulesList_StateColumnDistinguishesAFailingSchedule -v -timeout 60s
```

Expected: FAIL at `require.Contains(t, out, "STATE")`.

- [ ] **Step 3: Write the implementation**

In `doSchedulesList`, replace the header line and the row loop:

```go
	fmt.Fprintf(w, "Total: %d\n", total)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tCRON\tTZ\tENABLED\tNEXT\tSTATE")
	for _, s := range schedules {
		next := ""
		if s.NextRunAt != nil {
			next = s.NextRunAt.Format("2006-01-02 15:04")
		}
		// STATE IS A SEPARATE AXIS FROM ENABLED. A schedule that has stopped
		// producing jobs is still enabled - relay does not auto-disable one - so
		// ENABLED keeps telling the truth about the operator's own setting and
		// this column says whether the scheduler can actually use the schedule.
		//
		// PUTTING IT IN THE LIST IS THE POINT. run-now already explains a
		// schedule you SUSPECT; what was missing was any way to see which one to
		// suspect without suspecting anything first.
		state := "OK"
		if s.LastError != nil && *s.LastError != "" {
			state = "FAILING"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%t\t%s\t%s\n",
			s.ID, s.Name, s.CronExpr, s.Timezone, s.Enabled, next, state)
	}
	return tw.Flush()
```

If `strings` is not already imported in `schedules_test.go`, add it (it is - `TestSchedulesDelete_Success` uses `strings.ToLower`).

- [ ] **Step 4: Run test to verify it passes**

```powershell
go test ./internal/cli/... -timeout 120s
```

Expected: PASS. The pre-existing `TestSchedulesList_Success` asserts only `Contains "abc"` and `Contains "n"`, so a seventh column does not disturb it.

- [ ] **Step 5: Commit**

```powershell
git ls-files --eol internal/cli/schedules.go internal/cli/schedules_test.go
git add internal/cli/schedules.go internal/cli/schedules_test.go
git commit -m "feat(cli): relay schedules list marks a failing schedule in a STATE column"
```

---

## Task C3: `relay schedules update --spec FILE`

The remedy this slice's whole signal points at. At HEAD neither the CLI nor the SPA can perform it: `doSchedulesUpdate` has `--cron`, `--tz`, `--enable`, `--disable`, `--overlap` and no `--spec`; the SPA's Job spec panel is read-only and `ScheduleTriggerForm` submits only `cron_expr`, `timezone` and `overlap_policy`. Only the Python SDK and a raw HTTP call can repair a schedule. Shipping a failure surface whose documented fix is unreachable from relay's own clients is the "verify a prescribed command exists" defect, in the direction where the command genuinely does not exist.

**Files:**
- Modify: `internal/cli/schedules.go`
- Modify: `internal/cli/schedules_test.go`

- [ ] **Step 1: Write the failing test**

```go
// It mirrors doSchedulesCreate's read-parse-send exactly: read the file,
// json.Unmarshal into map[string]any to confirm it PARSES, put the object on the
// body. THE SERVER REMAINS THE VALIDATOR OF RECORD - the CLI checks syntax,
// never semantics - so the bound the operator tripped is reported by the one
// place that owns it, and its 400 renders verbatim.
func TestSchedulesUpdate_SpecFlagSendsTheJobSpec(t *testing.T) {
	var receivedBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "PATCH", r.Method)
		require.Equal(t, "/v1/scheduled-jobs/abc", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&receivedBody))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"abc","name":"nightly","cron_expr":"@hourly","timezone":"UTC","enabled":true,"next_run_at":"2099-01-01T00:00:00Z"}`)
	}))
	defer srv.Close()
	cfg := &Config{ServerURL: srv.URL, Token: "tkn"}

	dir := t.TempDir()
	specPath := filepath.Join(dir, "spec.json")
	require.NoError(t, os.WriteFile(specPath,
		[]byte(`{"name":"repaired","tasks":[{"name":"t","command":["echo","hi"],"retries":3}]}`), 0600))

	var buf bytes.Buffer
	require.NoError(t, doSchedules(context.Background(), cfg,
		[]string{"update", "abc", "--spec", specPath}, &buf))

	spec, ok := receivedBody["job_spec"].(map[string]any)
	require.True(t, ok, "the PATCH body must carry job_spec as an object, got %#v", receivedBody["job_spec"])
	require.Equal(t, "repaired", spec["name"])

	// NOTHING ELSE MAY RIDE ALONG. Sending cron_expr or timezone the user did not
	// supply recomputes next_run_at server-side, pushing the next fire out by up
	// to a full period on a schedule the operator is trying to REPAIR, not
	// reschedule. patchScheduledJobRequest is all pointers, so an omitted key
	// means leave alone - which is only true if the CLI actually omits it.
	require.NotContains(t, receivedBody, "cron_expr")
	require.NotContains(t, receivedBody, "timezone")
	require.NotContains(t, receivedBody, "enabled")
	require.NotContains(t, receivedBody, "overlap_policy")
}

// SYNTAX ONLY, AND THE REFUSAL COMES BEFORE ANY REQUEST. That is why this test
// belongs in the default lane by definition: no server is involved in the
// outcome.
func TestSchedulesUpdate_SpecFlagRefusesUnparseableJSONWithoutCallingTheServer(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()
	cfg := &Config{ServerURL: srv.URL, Token: "tkn"}

	dir := t.TempDir()
	specPath := filepath.Join(dir, "spec.json")
	require.NoError(t, os.WriteFile(specPath, []byte(`{"name": `), 0600))

	err := doSchedules(context.Background(), cfg,
		[]string{"update", "abc", "--spec", specPath}, io.Discard)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid spec JSON")
	require.False(t, called, "an unparseable spec must be refused before any request is made")
}

func TestSchedulesUpdate_SpecFlagReportsAMissingFile(t *testing.T) {
	cfg := &Config{ServerURL: "http://127.0.0.1:1", Token: "tkn"}
	err := doSchedules(context.Background(), cfg,
		[]string{"update", "abc", "--spec", filepath.Join(t.TempDir(), "nope.json")}, io.Discard)
	require.Error(t, err)
	require.Contains(t, err.Error(), "read spec file")
}

// --spec COMBINES with the existing flags rather than replacing them, so an
// operator can repair the spec and re-enable in one call.
func TestSchedulesUpdate_SpecFlagCombinesWithEnable(t *testing.T) {
	var receivedBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&receivedBody))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"abc","name":"nightly","cron_expr":"@hourly","timezone":"UTC","enabled":true,"next_run_at":"2099-01-01T00:00:00Z"}`)
	}))
	defer srv.Close()
	cfg := &Config{ServerURL: srv.URL, Token: "tkn"}

	dir := t.TempDir()
	specPath := filepath.Join(dir, "spec.json")
	require.NoError(t, os.WriteFile(specPath,
		[]byte(`{"name":"repaired","tasks":[{"name":"t","command":["echo","hi"]}]}`), 0600))

	require.NoError(t, doSchedules(context.Background(), cfg,
		[]string{"update", "abc", "--spec", specPath, "--enable"}, io.Discard))

	require.Contains(t, receivedBody, "job_spec")
	require.Equal(t, true, receivedBody["enabled"])
}
```

- [ ] **Step 2: Run test to verify it fails**

```powershell
go test ./internal/cli/... -run TestSchedulesUpdate_Spec -v -timeout 60s
```

Expected: all four FAIL with `flag provided but not defined: -spec`.

- [ ] **Step 3: Write the implementation**

In `doSchedulesUpdate`, add the flag after `overlap`:

```go
	overlap := fs.String("overlap", "", "new overlap policy: skip|allow")
	// --spec is THE REMEDY for a schedule whose stored job_spec no longer
	// validates, which is what `relay schedules list`'s FAILING marker and
	// `relay schedules show`'s Last error line point an operator at. Before it,
	// the only routes were the Python SDK and curl: the SPA's Job spec panel is
	// read-only and this command had no spec flag, so relay advertised a failure
	// whose fix relay could not perform.
	//
	// SYNTAX ONLY, mirroring doSchedulesCreate: this unmarshals to confirm the
	// file PARSES and sends the object. The server is the validator of record and
	// its 400 renders verbatim.
	specFile := fs.String("spec", "", "path to a replacement job spec JSON file")
```

And in the body-building block, after the `overlap` clause and before the `enable`/`disable` clauses:

```go
	if *specFile != "" {
		data, err := os.ReadFile(*specFile)
		if err != nil {
			return fmt.Errorf("read spec file: %w", err)
		}
		var spec map[string]any
		if err := json.Unmarshal(data, &spec); err != nil {
			return fmt.Errorf("invalid spec JSON: %w", err)
		}
		body["job_spec"] = spec
	}
```

And update the usage string:

```go
		return fmt.Errorf("usage: relay schedules update <id> [--cron EXPR] [--tz ZONE] [--spec FILE] [--enable|--disable] [--overlap skip|allow]")
```

- [ ] **Step 4: Run test to verify it passes**

```powershell
go test ./internal/cli/... -timeout 120s
```

Expected: PASS.

- [ ] **Step 5: Verify the command relay now advertises actually parses**

The README ladder (Task A9, part 1) and `schedules show`'s output both name commands. "The remedy is named" and "the remedy exists" are different properties. Prove the spelling by parsing, not by reading:

```powershell
go build -o bin/relay.exe ./cmd/relay
.\bin\relay.exe schedules update 2>&1 | Select-String -Pattern "--spec"
```

Expected: the usage error names `--spec FILE`. (The command refuses with the usage string when no id is given, which is the path this exercises.)

- [ ] **Step 6: Commit**

```powershell
git ls-files --eol internal/cli/schedules.go internal/cli/schedules_test.go
git add internal/cli/schedules.go internal/cli/schedules_test.go
git commit -m "feat(cli): relay schedules update --spec FILE repairs a stored job spec"
```

---

## Task C4: The CI-running proof that the field crosses the wire

**Requires Slice A in the tree.** This is the only test in the whole plan that exercises a real handler against a real database AND runs in CI.

**Files:**
- Create: `internal/cli/schedules_failure_integration_test.go`

- [ ] **Step 1: Write the failing test**

```go
//go:build integration

package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestIntegration_SchedulesFailureCrossesTheWire is the ONE test covering
// bug-2026-08-23-unfireable-schedule-is-invisible that RUNS IN CI.
//
// .github/workflows/go-ci.yml has a `cli-integration` job that runs
// `make test-cli-integration` against a `services: postgres`, and
// startRelayServer gives this file a real internal/api server over HTTP, a real
// migrated database, and a raw pool. The headline test
// (internal/api/scheduled_jobs_failure_visibility_integration_test.go) is
// integration-tagged in internal/api and is in NEITHER CI job.
//
// WHAT THIS COVERS, END TO END, IN CI: the column exists ->
// toScheduledJobResponse maps it -> the JSON key is spelled last_error ->
// scheduleResp decodes it -> the CLI renders it. That whole chain is exactly
// what a fixture-driven default-lane test cannot see, because a hand-written
// fixture pins what a HUMAN believes the server sends, not what it sends.
//
// WHAT IT DOES NOT COVER: that the SCHEDRUNNER writes the record. This harness
// deliberately does not wire schedrunner (see startRelayServer's own comment),
// which is also why the planted row is stable for the length of the test. That
// half is covered only by tests CI does not run. Do not report this file as
// covering the fix; it covers the fix's RESPONSE half.
//
// THE ROW IS PLANTED WITH SQL, and that is not a shortcut: POST and PATCH both
// validate before storing, so no REST path can produce a last_error. The value
// planted is verbatim what jobspec.Validate emits for a spec over the retry
// bound, so a change to that message shows up here as a stale literal rather
// than as a silent pass.
func TestIntegration_SchedulesFailureCrossesTheWire(t *testing.T) {
	s := startRelayServer(t)
	specPath := writeSpecFile(t, laneJobSpec)

	var createOut bytes.Buffer
	require.NoError(t, doSchedules(testCtx(t), s.adminCfg(), []string{
		"create", "--name", "failing-lane", "--cron", "0 3 * * *", "--spec", specPath,
	}, &createOut))
	require.Contains(t, createOut.String(), "created: failing-lane")

	// THE CONTROL, created through the same POST. Without it, the assertions
	// below would pass on an implementation that marked every row FAILING.
	var healthyOut bytes.Buffer
	require.NoError(t, doSchedules(testCtx(t), s.adminCfg(), []string{
		"create", "--name", "healthy-lane", "--cron", "0 4 * * *", "--spec", specPath,
	}, &healthyOut))
	require.Contains(t, healthyOut.String(), "created: healthy-lane")

	var scheduleID string
	require.NoError(t, s.Pool.QueryRow(t.Context(),
		`SELECT id::text FROM scheduled_jobs WHERE name = 'failing-lane'`).Scan(&scheduleID))

	const failure = "task t: retries must be between 0 and 10"
	tag, err := s.Pool.Exec(t.Context(), `
		UPDATE scheduled_jobs
		   SET last_error = $1, last_error_at = NOW()
		 WHERE name = 'failing-lane'`, failure)
	require.NoError(t, err)
	require.EqualValues(t, 1, tag.RowsAffected(), "precondition: exactly one row must be planted")

	// show: the detail endpoint's body, through the real handler.
	var showOut bytes.Buffer
	require.NoError(t, doSchedules(testCtx(t), s.adminCfg(), []string{"show", scheduleID}, &showOut))
	show := showOut.String()
	require.Contains(t, show, "Last error (from the stored job_spec")
	require.Contains(t, show, failure,
		"the exact message the server stored must survive toScheduledJobResponse, the JSON key, and the decode")
	require.Contains(t, show, "relay schedules run-now "+scheduleID)

	// list: the DISCOVERY half, and the one the item exists for. A response-shape
	// drift here is invisible to internal/cli's httptest fixtures and reddens
	// this lane instead.
	var listOut bytes.Buffer
	require.NoError(t, doSchedules(testCtx(t), s.adminCfg(), []string{"list"}, &listOut))
	list := listOut.String()
	require.Contains(t, list, "STATE")

	var failingLine, healthyLine string
	for _, l := range strings.Split(list, "\n") {
		if strings.Contains(l, "failing-lane") {
			failingLine = l
		}
		if strings.Contains(l, "healthy-lane") {
			healthyLine = l
		}
	}
	require.NotEmpty(t, failingLine)
	require.NotEmpty(t, healthyLine)
	require.Contains(t, failingLine, "FAILING")
	require.NotContains(t, healthyLine, "FAILING",
		"CONTROL: a healthy schedule created through the same POST must carry neither key, so the marker "+
			"is a claim about ONE row rather than about the column being non-null everywhere")

	// A PATCH that supplies a valid job_spec clears the record - through the real
	// handler and the real clear_failure argument, which nothing else in CI
	// exercises.
	var updateOut bytes.Buffer
	require.NoError(t, doSchedules(testCtx(t), s.adminCfg(), []string{
		"update", scheduleID, "--spec", specPath,
	}, &updateOut))

	var showAfter bytes.Buffer
	require.NoError(t, doSchedules(testCtx(t), s.adminCfg(), []string{"show", scheduleID}, &showAfter))
	require.NotContains(t, showAfter.String(), "Last error",
		"a PATCH carrying a validated job_spec must clear the record: the handler validated the new value "+
			"before storing it, so any record about the old one is stale by construction")
}
```

- [ ] **Step 2: Run test to verify it fails**

Two modes. With a Postgres already running at `postgres://relay:relay@127.0.0.1:5432` (the `relay-postgres` container `scripts/dev.ps1` manages), the fast one:

```powershell
$env:RELAY_TEST_DATABASE_URL = "postgres://relay:relay@127.0.0.1:5432/postgres?sslmode=disable"
go test -tags integration -count=1 ./internal/cli/... -run TestIntegration_SchedulesFailureCrossesTheWire -v -timeout 480s
```

Without it, leave `RELAY_TEST_DATABASE_URL` unset for one testcontainer per test (needs Docker).

Expected before Tasks C1 to C3: FAIL at `require.Contains(t, show, "Last error (from the stored job_spec")`. Executed in this task order, C1 to C3 have landed, so expect PASS - then run Step 3.

If it fails at the `UPDATE` with `column "last_error" of relation "scheduled_jobs" does not exist`, Slice A is not in the tree. That is the one hard cross-slice dependency in this plan.

- [ ] **Step 3: Prove it is load-bearing on the axis a fixture cannot reach**

Temporarily change `scheduleResp`'s tag from `json:"last_error,omitempty"` to `json:"lastError,omitempty"` and re-run Step 2.

Expected: FAIL at the same `require.Contains`. **That is the whole reason this test exists.** `internal/cli/schedules_test.go`'s hand-written fixtures catch the same drift, but only against a literal a human typed; this one proves the CLI agrees with the SERVER's actual spelling.

Revert:

```powershell
git checkout -- internal/cli/schedules.go
go test -tags integration -count=1 ./internal/cli/... -run TestIntegration_SchedulesFailureCrossesTheWire -v -timeout 480s
```

Expected: PASS.

- [ ] **Step 4: Commit**

```powershell
git ls-files --eol internal/cli/schedules_failure_integration_test.go
git add internal/cli/schedules_failure_integration_test.go
git commit -m "test(cli): a recorded schedule failure crosses the real wire, in the one lane CI runs"
```

---

## Task C5: README - the CLI half

Part 1's Task A9 corrects the "only record is one line in the server log" sentence and adds the field subsection. This task corrects the three CLI-facing claims that Slice C makes false, including the one that says `--spec` does not exist.

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Locate the three sites**

```powershell
Select-String -Path README.md -Pattern "Output columns: ``ID``"
Select-String -Path README.md -Pattern "relay schedules update"
Select-String -Path README.md -Pattern "has no ``--spec`` flag"
```

- [ ] **Step 2: Make the corrections**

**(a)** In `#### relay schedules list`, replace:

```markdown
Output columns: `ID`, `NAME`, `CRON`, `TZ`, `ENABLED`, `NEXT` (next scheduled run time).
```

with:

```markdown
Output columns: `ID`, `NAME`, `CRON`, `TZ`, `ENABLED`, `NEXT` (next scheduled run time), `STATE`.

`STATE` is `OK`, or `FAILING` when the scheduler last failed to produce a job from the schedule's stored spec. It is a separate axis from `ENABLED`: a failing schedule is still enabled, because relay does not disable one on its own. Run `relay schedules show <id>` for the reason.
```

**(b)** In `#### relay schedules show`, after the code fence, add:

```markdown
Prints the id, name, cron expression, timezone, enabled flag, next run and last run. When the schedule's last fire failed it also prints `Last error`, when it failed, and the `run-now` command that re-checks it. The error text is derived from the stored `job_spec` and is operator-supplied, and the label says so; it is truncated to 1 KB, so use `run-now` for the full message.
```

**(c)** In `#### relay schedules update`, add a row to the flag table between `--tz ZONE` and `--overlap skip|allow`:

```markdown
| `--spec FILE` | Replace the stored job spec with the contents of FILE (same format as `relay schedules create --spec`). The server validates it and reports any problem verbatim. This is the repair for a schedule whose `STATE` reads `FAILING`. |
```

and add one example to that section's code fence:

```sh
relay schedules update <schedule-id> --spec repaired-job.json
```

**(d)** If part 1's Task A9 has not already done so, correct the sentence `` `relay schedules update` has no `--spec` flag, so from the CLI it is delete plus `relay schedules create --spec`. `` to:

```markdown
Replacing the stored spec is a `PATCH /v1/scheduled-jobs/{id}` with a new `job_spec`, or `relay schedules update <id> --spec FILE`.
```

Check first - A9 owns that sentence, and doing it twice will conflict.

- [ ] **Step 3: Verify the edit did not corrupt the file**

README has been reclassified as binary once on this repo by a programmatic edit that produced `\r\r\n`, turning a two-line change into 1845 insertions. It was caught from the diffstat, not by any gate.

```powershell
git diff --stat README.md
git ls-files --eol README.md
git diff README.md | Select-String -Pattern "^\+" | Select-String -Pattern "[–—]"
```

Expected: a diffstat in the range of roughly 10 to 15 changed lines; `i/lf`; and the em/en dash grep returns nothing. If the insertion count is in the hundreds, `git checkout -- README.md` and redo the edit by hand.

- [ ] **Step 4: Commit**

```powershell
git add README.md
git commit -m "docs: README documents the STATE column, the show output, and relay schedules update --spec"
```

---

# Slice D - the Python SDK

Two lines and two tests. Independent of A, B and C: `ScheduledJob` is `extra="ignore"`, so it does not break without this - it simply cannot see the fields.

## Task D1: `ScheduledJob.last_error` and `last_error_at`

**Files:**
- Modify: `python/src/relay/models.py`
- Modify: `python/tests/unit/test_models.py`

- [ ] **Step 1: Write the failing test**

Append to `python/tests/unit/test_models.py`, after `test_scheduled_job_spec_is_still_required_when_absent`:

```python
def test_scheduled_job_failure_fields_are_none_when_the_server_omits_them() -> None:
    """ABSENT MEANS HEALTHY. scheduledJobResponse carries `omitempty` on both
    last_error and last_error_at, and the server's write site never stores an
    empty string, so a schedule that has never failed sends NEITHER KEY - not
    "", not null. `is None` is therefore the whole test for "healthy", and a
    default of None is what makes that reading correct.
    """
    sj = ScheduledJob.model_validate(_scheduled_job_wire())
    assert sj.last_error is None
    assert sj.last_error_at is None


def test_scheduled_job_failure_fields_parse_when_present() -> None:
    """Why the scheduler last failed to produce a job from this schedule.

    RED AT HEAD FOR A SPECIFIC REASON WORTH KNOWING: the model is
    extra="ignore", so before the fields exist these keys are silently DROPPED
    rather than raising, and the failure is an AttributeError on the read, not a
    ValidationError. That is the same silence this test exists to close - a
    server field the SDK cannot see.

    The text is derived from the stored job_spec and is OPERATOR-SUPPLIED: the
    message embeds a task name the schedule's owner chose. A consumer rendering
    it into a terminal or a web page must treat it as untrusted text. The server
    strips control characters and truncates to 1 KB at the write site;
    run_scheduled_job_now() returns the untruncated message.
    """
    sj = ScheduledJob.model_validate(
        _scheduled_job_wire(
            last_error="task t: retries must be between 0 and 10",
            last_error_at="2026-08-28T12:00:00Z",
        )
    )
    assert sj.last_error == "task t: retries must be between 0 and 10"
    assert sj.last_error_at is not None
    assert sj.last_error_at.year == 2026
    assert sj.last_error_at.month == 8
```

- [ ] **Step 2: Run test to verify it fails**

```powershell
python\.venv\Scripts\python.exe -m pytest python/tests/unit/test_models.py -k "scheduled_job_failure" -v
```

Expected: BOTH tests FAIL with `AttributeError: 'ScheduledJob' object has no attribute 'last_error'`. `extra="ignore"` means the keys are dropped rather than raising at validation, so the failure is on the read.

If the first test passes, the field already exists and something is out of order.

- [ ] **Step 3: Write the implementation**

In `python/src/relay/models.py`, in `class ScheduledJob`, after `last_job_id`:

```python
    last_job_id: Optional[str] = None
    # Why the scheduler last failed to produce a job from this schedule, and
    # when. ABSENT MEANS HEALTHY: the server omits both keys entirely
    # (scheduledJobResponse carries `omitempty` on each) and never sends "" or
    # null, so `is None` is the correct and only test.
    #
    # last_error is derived from the stored job_spec and is OPERATOR-SUPPLIED -
    # it embeds a task name the schedule's owner chose - so treat it as untrusted
    # text. It is sanitized and truncated to 1 KB server-side;
    # run_scheduled_job_now() returns the untruncated message.
    last_error: Optional[str] = None
    last_error_at: Optional[datetime] = None
```

`Optional` and `datetime` are already imported in that module (`last_run_at: Optional[datetime]` sits four lines above).

- [ ] **Step 4: Run test to verify it passes**

```powershell
python\.venv\Scripts\python.exe -m pytest python/tests/unit -v
```

Expected: PASS, the whole unit suite.

- [ ] **Step 5: Lint and type check**

```powershell
python\.venv\Scripts\python.exe -m ruff check python/src python/tests
python\.venv\Scripts\python.exe -m mypy python/src
```

Expected: clean.

- [ ] **Step 6: Commit**

```powershell
git ls-files --eol python/src/relay/models.py python/tests/unit/test_models.py
git add python/src/relay/models.py python/tests/unit/test_models.py
git commit -m "feat(python): ScheduledJob carries last_error and last_error_at"
```

---

# Verification gates for the whole plan

Run all of these at the end, from the worktree root, as literal commands. `make` is not installed.

## G1: the untagged lane, every package

```powershell
go test ./... -timeout 120s
```

Expected: `ok` for every package. This is what CI's `test` job runs (as `go test -race ./...`), and it is where `TestScheduledJobRowStillCarriesNoFailureSurface`, `internal/schedrunner/failure_test.go` and `internal/api/scheduled_jobs_response_test.go` live.

## G2: vet, both tags

```powershell
go vet ./...
go vet -tags integration ./...
```

Expected: no output from either. The second is CI's `Integration-tagged build check` and is the only thing that compiles `//go:build integration` files.

## G3: the integration packages this plan touches

Needs Docker Desktop running.

```powershell
go test -tags integration -p 1 ./internal/store/... ./internal/schedrunner/... ./internal/api/... -timeout 900s
```

Expected: PASS. `internal/api` alone runs roughly 320 to 340 seconds because every test spins up its own container. `-p 1` prevents parallel container conflicts on Windows.

## G4: the CLI real-server lane, the one CI runs

```powershell
$env:RELAY_TEST_DATABASE_URL = "postgres://relay:relay@127.0.0.1:5432/postgres?sslmode=disable"
go test -tags integration -count=1 ./internal/cli/... -timeout 480s
```

Or leave `RELAY_TEST_DATABASE_URL` unset for one testcontainer per test. `-count=1` is not optional: the Go test cache says nothing about whether a live TCP connection actually succeeded, so without it this can report `ok (cached)` in under a second having contacted no database.

## G5: the frontend lanes

```powershell
cd web
npx tsc -b
npm test
cd ..
```

Expected: type check clean, vitest all green. Both run in CI (`web-ci.yml`).

## G6: the browser lane

```powershell
cd web
npm run build
cd ..
go build -o bin/relay-server.exe ./cmd/relay-server
cd web
npm run test:e2e
cd ..
git checkout -- web/dist/index.html
```

The build order is load-bearing (`//go:embed all:dist` snapshots at compile time). Needs node, go, Playwright browsers, and a Postgres at `postgres://relay:relay@127.0.0.1:5432`. Runs in CI (`web-ci.yml`).

## G7: the Python lanes

```powershell
python\.venv\Scripts\python.exe -m pytest python/tests/unit -v
python\.venv\Scripts\python.exe -m ruff check python/src python/tests
python\.venv\Scripts\python.exe -m mypy python/src
```

## G8: the race lane

`make test-race` is the canonical target and its literal form is `go test -race ./... -timeout 180s`. **On this machine the native Windows lane is unreliable and the Linux container is the route that actually works**, and it closes a second gap for free: `go test` on Windows silently skips every `//go:build !windows` file. From Git Bash:

```bash
MSYS_NO_PATHCONV=1 docker run --rm -v "$(pwd -W):/src" -w /src -e CGO_ENABLED=1 \
  golang:1.26 go test -race ./... -count=1 -timeout 600s
```

Two native failure modes, easy to confuse. A **compiler** failure (`exit status 0xc0000139` from the default Strawberry Perl gcc; fix with MSYS2 mingw64 and `CC=/c/msys64/mingw64/bin/gcc.exe` with its `bin` on PATH). And a **runtime** ThreadSanitizer shadow-arena allocation failure (`ThreadSanitizer failed to allocate 0x... bytes (error code: 87)`), which is environmental, memory-pressure related and intermittent, and has reproduced on untouched packages at `origin/main`. Its distinguishing symptom is that it names ThreadSanitizer and an allocation and is attached to no test. Before concluding this plan caused it, re-run at `origin/main` on an untouched package.

**If the lane is genuinely unavailable, say so plainly rather than substituting.** `-count=N` repetition is NOT equivalent: it re-runs under the ordinary scheduler and cannot observe an unsynchronised access that never happens to interleave badly. It raises confidence in flakiness, not in race-freedom. State that `-race` did not run.

Nothing in this plan adds a goroutine or a shared mutable value, so a race regression is not expected. Run it anyway.

## G9: the working tree, checked independently

Do not trust a subagent's "all green". Check the tree yourself before assembling the PR:

```powershell
git status --short
git diff --stat origin/main
git diff --name-only origin/main | ForEach-Object { git ls-files --eol $_ }
```

Expected: no `web/dist` entries beyond a restored `index.html`; no `bin/` artifacts staged; every touched path `i/lf`; a diffstat whose size matches the work.

---

# Conductor steps, not engineer steps

These are outside the engineers' scope. The conductor performs them.

## Close the backlog item

Use the command, never a hand edit of the `status` field:

```
/backlog close unfireable-schedule
```

The command runs the whole sequence the skill enforces: `git mv` from `docs/backlog/` into `docs/backlog/closed/`, stamps `status: closed` plus `closed:` and `resolution:` frontmatter, appends a `## Resolution` note, and commits. Flipping `status` alone leaves the file in the open directory, and `/backlog list` then reports it as a malformed open item. **The `git mv` is required scope for this slice, not optional cleanup.**

## Append the eighth instance to the CI-gap item

`docs/backlog/idea-2026-08-23-integration-only-guards-ci-never-runs.md` already carries seven appended instances. This plan supplies an eighth, and it is a different SHAPE from the seventh: the seventh was a shipped security fix with no CI proof; this is a fix whose **response half** got CI coverage through an unexpected route while its **write half** did not.

Propose (do not auto-file) an append along these lines:

> **Appended 2026-08-28 - an eighth instance, and the first where this item's own mechanism partly closed the gap by accident.**
>
> The un-fireable-schedule slice's headline test,
> `TestScheduledJobFailure_IsVisibleOnGetAndOnList_AndClearedByPatch`, is `//go:build integration` in
> `internal/api`. It is in neither CI job, and its own comment says so at length - which is this item's
> accepted "a written decision in the test's own comment" form.
>
> What is new: the slice found that `make test-cli-integration`, the `services: postgres` job this item
> recorded on 2026-08-27 as "the generalisable half", **already reaches `internal/api`'s handlers**.
> `internal/cli/relayharness_integration_test.go`'s `startRelayServer` stands up a real `api.Server`
> over `httptest` against a real migrated database and exposes the raw `*pgxpool.Pool`.
> `internal/cli/schedules_failure_integration_test.go` uses that to prove, in CI, that `last_error`
> crosses column -> handler -> JSON -> client.
>
> So the sixth instance's claim that "`internal/api`'s default lane cannot observe its own handlers" is
> still true and is now narrower than it reads: `internal/api`'s handlers ARE observed in CI, through
> `internal/cli`'s harness, for any behaviour a CLI command exercises. What stays uncovered is
> everything with no CLI surface - and, in this slice specifically, the **schedrunner write path**,
> which needs a tick and therefore a database of its own.
>
> That changes the pricing of the remedy: extending the `cli-integration` job to `internal/api` is a
> smaller move than the 2026-08-27 note assumed, because the harness that runs a real server already
> exists on the other side of the same job.
>
> Add to Related: `internal/cli/relayharness_integration_test.go` (`startRelayServer`),
> `internal/api/scheduled_jobs_failure_visibility_integration_test.go`.

## PR body

Include:

- The two new columns and their **absent means healthy** semantics.
- The clearing table: a successful fire clears; a skip preserves; a PATCH of `job_spec`, `cron_expr` or `timezone` clears; a PATCH of `name`, `overlap_policy` or `enabled` preserves; a transient database fault preserves; `run-now` touches nothing.
- The explicit statement that **relay does not auto-disable a failing schedule**, and the first of the five reasons: the failure mode this item exists for is "a release retroactively invalidated stored data", and answering that by turning the operator's schedule off compounds a server-driven change to user data with a server-driven change to user configuration.
- The honest CI split: the headline test does not run in CI; the response half does, via `cli-integration` and two untagged siblings; the schedrunner write path has no CI coverage.
- An **upgrade note**: the startup validation sweep populates `last_error` on existing broken schedules within seconds of the deploy, so operators should expect the surface to be non-empty immediately rather than to fill up gradually over the following month. If Slice A2 was dropped, say so and say that the surface fills at each schedule's next fire instead.
- That `relay schedules update --spec FILE` is new, and that before it the repair this slice's signal points at was reachable only from the Python SDK or curl.
