---
date: 2026-08-12
topic: schedule-detail-page
branch: claude/pr-merge-session-f5796e
range: origin/main..HEAD (green, not yet merged)
---

# Session Retro: 2026-08-12 - Schedule Detail Page and Edit Action

**TL;DR:** Shipped `/schedules/:id` - a full detail page with an inline cron/timezone/overlap
editor, the server's next fire, the latest 20 runs, a confirmed Delete, and two new entry points
from the list - closing `idea-2026-06-05-schedule-detail-page`. 100% frontend; the Go diff is
empty. The central design point is a backend behaviour the item never mentioned: `PATCH`
recomputes `next_run_at` from `time.Now()` whenever the body merely *carries* a `cron_expr` or
`timezone` key, changed or not (`internal/api/scheduled_jobs.go:584-596`), so the patch body had
to be a diff and not a form dump. Two things distinguish this iteration from the three that
preceded it. First, **the conductor's own `/code-review` finally returned findings** (3 mediums,
after three consecutive zeros) - and was still partially refuted, with its reachability claim on
the top finding downgraded to latent by all three lenses independently. Second, **the prose
defect was caught before shipping rather than after**: the implementing engineer reported a
false justification about a lifecycle test, the correctness lens measured it and refuted it, and
the conductor grepped the tree to confirm the claim never reached a committed file. That is the
first iteration in four where the arc's signature failure did not ship.

## What Was Built

- **Spec** `docs/superpowers/specs/2026-08-12-schedule-detail-page.md`, **plan**
  `docs/superpowers/plans/2026-08-12-schedule-detail-page.md` (11 sequential tasks, one frontend
  engineer, no parallelism available - the dependency chain is linear and three tasks write into
  shipped shared modules under `web/src/schedules/`).
- New route `/schedules/:id` in `web/src/app/router.tsx`, inside `ProtectedRoute` and **not**
  `AdminRoute`: every underlying endpoint is owner-or-admin via `ownedScheduledJob`, which 404s a
  non-owner rather than 403ing.
- New files under `web/src/schedules/`: `ScheduleDetailPage.tsx`, `ScheduleTriggerForm.tsx`,
  `ScheduleRunsPanel.tsx`, `useSchedule.ts`, `useScheduleRuns.ts`, plus six test files including
  two the plan did not anticipate (`ScheduleDetailPage.lifecycle.test.tsx` and
  `ScheduleDetailPage.transition.test.tsx`, the latter created by a Phase 4 finding).
- `web/src/jobs/api.ts` gains `listJobsBySchedule`, deliberately **not** expressed through
  `listJobs`, whose default `sort` would make every request a hard 400 against the filtered
  branch (`internal/api/jobs.go:417-422`).
- `web/src/schedules/api.ts` gains `getSchedule`, `SchedulePatch`, `updateSchedule`,
  `deleteSchedule`; `setScheduleEnabled` re-expressed through `updateSchedule` with a
  byte-identical exported signature. All five path interpolations use `encodeURIComponent`.
- `web/src/schedules/useScheduleActions.ts` gains `update` and `remove`; the two shipped
  mutations are untouched.
- `web/src/schedules/SchedulesTable.tsx` - NAME becomes a `<Link>` and an `Edit` `<Link>` joins
  ACTIONS. Both are router links, so middle-click and open-in-new-tab work.
- Tests 813 -> 890 (reported by the lanes; this pass had no shell, so the count is not
  independently re-run here - see Verification).

## Key Decisions

- **The patch body is a diff against the loaded row, not the form's contents.** This is the whole
  slice's correctness. `internal/api/scheduled_jobs.go:585` triggers on
  `req.CronExpr != nil || req.Timezone != nil`, and `:595` then binds `sched.Next(time.Now())`,
  so re-sending an unchanged cron on an `@every 1h` schedule whose fire is five minutes away
  pushes it out by 55 minutes. Sending the whole form is the obvious implementation, passes every
  naive test, and its only symptom is a schedule that quietly drifts later every time somebody
  opens the editor. Guarded by a dedicated regression test that also **guards itself against
  vacuity**, asserting the two formatted timestamps differ before relying on the distinction.
- **One next-fire entry, not the hi-fi's five.** `web/package.json` has no cron parser, so a
  preview would be a second implementation of `@every`/`@hourly`/IANA-zone semantics that has to
  agree with `robfig/cron/v3` (`internal/schedrunner/cron.go:14-16`). A preview that silently
  disagrees is worse than one honest value, because the panel's whole purpose is trust. Not a
  degraded placeholder either: PATCH returns the recomputed `next_run_at`, so after a save the
  panel shows the authoritative first fire of the edit just made.
- **The owner line is omitted rather than faked, and the fallback to `owner_id` was explicitly
  rejected.** The page must behave identically on a deep link and on a click-through; a field
  that appears only when you arrive by one route is worse than a consistent omission. Carrying
  the value over from the cached list row was considered and refused for the same reason.
- **The overlap control offers exactly two options.** The hi-fi's third, `queue`, always 400s
  (`internal/api/scheduled_jobs.go:561-564`). No enabler filed: a queueing policy is a scheduler
  product decision, not a UI gap.
- **No client-side cron or timezone validation at all.** The server is the validator of record
  and its 400 message is rendered verbatim beside the control. Same reasoning as the next-fires
  decision, applied to a second surface.
- **Recent runs is a fixed latest-20 window with no pager**, with a `latest N of <total>` footer
  that states the window honestly rather than importing the list pages' cursor machinery.
- **The third copy of the detail-page state triad shipped deliberately.** See Problem 5.

## Problems Encountered

1. **Delete violated CLAUDE.md Invariant 1, and the mechanism was traced through library source
   rather than reasoned about.** `remove`'s `onSuccess` invalidated the bare `['schedules']`
   prefix, which matched the just-deleted row's own `['schedules','detail',id]` and
   `['schedules','runs',id]` - two guaranteed 404s fired at a row the server had already
   destroyed - *before* `navigate` ran. The ordering is not incidental: the hook-level `onSuccess`
   resolves strictly before the mutate-level `onSuccess` that navigates
   (`@tanstack/query-core@5.101.0` `mutation.js:123`, `mutationObserver.js:87`), so the page was
   guaranteed to still be mounted with active observers on both keys. This is exactly "end the
   generation before releasing the resource" in its cache-invalidation form: the resource was
   released (deleted) while its own continuations still looked current. Fixed with two
   `removeQueries` calls ahead of the broad invalidate, with the ordering argument written at the
   call site (`web/src/schedules/useScheduleActions.ts:42-55`).

   The generalization worth keeping: **an invalidation is a continuation.** The invariant's
   frontend instance to date was `abort()` on an SSE stream; this is the same shape with no
   `abort()`, no `close()`, and no `cancel()` anywhere in the diff to grep for. The tell is not a
   teardown call - it is a broad key prefix that still matches something that has ceased to exist.

2. **The page acted on the route's `id` while rendering a different schedule's row, and one guard
   closed three symptoms.** `useSchedule` uses `keepPreviousData`, so a detail-to-detail
   transition (`useParams().id` changes, no unmount) leaves `isLoading` false and `schedule`
   non-null throughout - none of the triad's checks catch it. Meanwhile every id-bearing control
   read `id` from `useParams`. The three observable consequences: a clean Save with **no typing**
   emitted `PATCH s2 {cron_expr: <s1's cron>}` (the trigger form seeds its draft from `schedule`
   exactly once, so it diffed s1's values against s2's id - the precise defect the whole
   changed-fields design exists to prevent, arriving through a different door); `ConfirmDialog`
   could name schedule A in its copy while deleting schedule B; and Run now / Disable could fire
   at the wrong row. One `schedule.id !== id` early return
   (`web/src/schedules/ScheduleDetailPage.tsx:76-78`) closes all three, with a
   `ScheduleDetailPage.transition.test.tsx` that freezes the transition with a never-resolving
   handler so the assertions are not racing a fast response.

   Worth recording precisely because **the transition is not reachable today** - nothing in the
   shipped app links from one schedule detail to another. The test constructs the transition
   directly against the component rather than waiting for a link to ship. The three lenses
   independently downgraded the conductor's `/code-review` claim that this was reachable, and
   fixing a latent defect anyway was still right: the guard costs one comparison and the next
   slice that adds a sibling link would otherwise ship a wrong-row PATCH with no test to catch it.

3. **A test-load regression the branch caused, and the measurement is the point.** The six new
   test files destabilized `web/src/admin/users/UsersTab.test.tsx`, an unrelated real-timer
   debounce test. The cheap read was "pre-existing flake" - and the file's own comment invited it,
   since a previous session had already once weakened the assertion to tame the same instability.
   The conductor measured both ways instead: `origin/main` 3 runs, 0 red; the branch 4 runs, 3
   red; the file alone 5 runs, 5 green. That is not a flake, it is a load-sensitive race that this
   branch's extra parallel workers pushed over the line.

   The fix went in the strengthening direction. `vi.advanceTimersByTimeAsync(10)` inside `act`
   crosses the 10ms debounce window in one deterministic jump instead of waiting for
   `shouldAdvanceTime`'s real-time ticking, and the assertion was **restored** from
   `toBeLessThanOrEqual(1)` to `toBe(1)` (`UsersTab.test.tsx:144`). Mutation-checked: no-op the
   debounce and it fails with `expected 14 to be 1`. **The prior weakening is the lesson.**
   Weakening an assertion to tame a flake buys quiet, not correctness, and the quiet expires the
   next time load increases - which is exactly what happened here, one branch later. The
   assertion was the only thing standing between the suite and a silently broken debounce, and it
   had been the first thing sacrificed.

4. **The implementing engineer shipped a false verification claim, and it was caught before the
   tree.** The engineer reported that a plan-supplied lifecycle test had been vacuous and that
   switching to `vi.advanceTimersByTimeAsync` was *required* to fix it. The correctness lens did
   not take the report: it mutated the component and ran both variants, and they go RED
   identically - the test's `waitFor` guard already flushed the pending state, so the timer
   variant changed nothing about what the test could detect. The conductor then grepped the
   committed file and confirmed the false justification **never reached the tree**.

   Set that against the three preceding iterations, where the arc's headline was "the code was
   right and the prose was wrong" every single time and the wrong prose shipped every single
   time. The difference here is not that the engineer was more careful - it made the same class of
   claim - it is that a lens was **obliged to derive the fact fresh** rather than confirm a
   report, and that the conductor treated "the lens refuted it" as a reason to check the artifact
   rather than as a closed question. Both halves were necessary.

5. **The detail-page state triad now has three verbatim consumers, and the third shipped on
   purpose.** The block - `if (isLoading && !data) return <GlassPanel className="h-40" />`
   followed by a 404-vs-retryable error card with a back link - is identical in
   `WorkerDetailPage.tsx:30-55`, `JobDetailPage.tsx:57-78` and now `ScheduleDetailPage.tsx:35-64`.
   The house rule is *extract before the third consumer*, so this is a **recorded deviation, not
   an oversight**: the extraction has to migrate two shipped pages and should be gated on a
   zero-line diff to their existing test files, which is its own slice with its own risk profile,
   and folding it in would have put the whole feature behind an unrelated refactor. The plan
   flagged it before Task 7, the page carries a comment naming the enabler, and the item is filed.

   The honest counter-argument, recorded so the next person does not have to re-derive it: the
   rule exists precisely to stop this reasoning from being used a fourth time. If a fourth detail
   page arrives before the extraction, the deviation stops being a deferral and becomes a policy.

6. **Four smaller confirmed findings, each a one-line class of bug worth naming.** A stale
   action-error masked a newer one because `runNow.error ?? setEnabled.error ?? remove.error`
   short-circuits on the *first* non-null and react-query never clears a settled error on its own;
   fixed by resetting the other two mutations before firing a new one. The form's error banner was
   undismissable because the empty-patch early return preceded `update.reset()`. `busy` omitted
   `update.isPending`, so a header action stayed live during a Save with no version column and no
   409 to catch the collision. And `web/src/schedules/api.ts` interpolated ids into paths
   unencoded - `fetch` normalizes dot segments before dispatch, so `/schedules/..%2Fjobs%2F<uuid>`
   turned the page's Delete into `DELETE /v1/jobs/<uuid>`.

7. **The conductor's `/code-review` found 3 mediums - and was still partially refuted.** After
   three consecutive zero-finding runs, the broad pass produced real findings. The lenses still
   found more than it did, and independently downgraded its severity claim on the top finding.
   The calibration from the previous retro survives intact, with one amendment: a non-zero broad
   review does not close the question either.

## Findings Triage

- **8 confirmed and fixed, 1 HIGH.** The HIGH is the delete-invalidation ordering (Problem 1).
  The rest: the route-id/rendered-row mismatch, the `??` error mask, the undismissable banner,
  `busy` missing `update.isPending`, unencoded path interpolation, an overstated determinism
  comment, and a cadence default pinned only by an exported-constant assertion.
- **The cadence finding is the one that generalizes past this slice.** `useScheduleRuns`' default
  10s interval was "covered" by a test asserting `SCHEDULE_RUNS_LIMIT === 20` and a second test
  exercising only the *injected* interval seam. A hardcoded `3000` copy-pasted from `useJobs.ts:7`
  would have passed both. The fix is a behavioral test that reads the cadence off the observer -
  one call at 9s, a second past 10s, on the same counter so the equality is about the interval
  and not about a dead instrument (`useScheduleRuns.test.tsx:68-99`). This is the repo's existing
  "a cadence test must assert the wiring" note recurring on a *new* hook, which means the note is
  not reaching the point of authorship.
- **Phase 4 ran four lanes, not three.** With the Go diff empty, the integration slot went to a
  real-browser lane instead. That was the right trade for a frontend-only slice and is worth
  making standard: the integration tester has nothing to run when no Go file moved.
- **The real-browser lane measured rather than screenshotted.** Compositing was unavailable, so it
  used `elementFromPoint` and `scrollWidth`/`scrollHeight`. It confirmed the page renders against
  a mock backend on the actual Vite dev server, the 10s poll cadence empirically, that the job
  spec `<pre>` contains its own scroll (`scrollHeight` 14789 vs `clientHeight` 345) with zero page
  overflow at 1280px, and that the Delete dialog is genuinely on top by a 5-point hit test - no
  repeat of the profile-dropdown stacking bug. A screenshot would have shown none of the last
  three.

## Deferred Findings

Proposed and filed, not fixed:

1. `bug-2026-08-12-web-narrow-viewport-horizontal-overflow` (**bug/medium**) - the real-browser
   lane measured page-level horizontal overflow below roughly 840px: 89px at 768px, 278px at
   375px, and 58-73px on a merely un-maximized ~785-800px desktop window. **Ruled out of scope for
   this slice and filed as app-wide**, because `WorkerDetailPage.tsx:114` is byte-identical in the
   respect that matters and every shipped table uses fixed-px column templates with no
   `overflow-x-auto` wrapper. `ServerTab.tsx:70` is the single place already doing it right with
   `md:grid-cols-2`. This slice reproduces an existing app-wide defect; it does not introduce one,
   and fixing it here would have meant editing shared primitives the plan's scope guard forbids.
2. `bug-2026-08-12-scheduled-job-detail-missing-owner-email` (**bug/medium**) - the enabler behind
   the omitted owner line. One handler skips a call two sibling handlers make, and the struct has
   no `omitempty`, so the endpoint ships a permanently-empty string.
3. `idea-2026-08-12-schedule-next-fires-preview` - the hi-fi's five-entry preview, with the real
   constraint written down: any JS implementation must agree with `robfig/cron/v3` including
   `@every`, descriptors and IANA zones, and a server-computed `next_fires` is the alternative
   worth weighing precisely because it uses the authoritative parser.
4. `idea-2026-08-12-schedule-job-spec-editor` (**idea/low**) - PATCH accepts `job_spec`; the panel
   is read-only. Any editor must defer semantics to the server per the single job-spec pipeline
   invariant, and should extract `NewJobPage`'s textarea rather than write a second one.
5. `idea-2026-08-12-detail-page-state-triad-primitive` (**idea/low**) - Problem 5's enabler. The
   item's substance is the **gate**, not the extraction: a zero-line diff to the three pages'
   existing test files, because an assertion needing adjustment during a behavior-preserving
   refactor is itself the finding.
6. `bug-2026-08-12-unencoded-path-interpolation-api-clients` (**bug/low**) - the defect fixed in
   `schedules/api.ts` this slice survives at **15 call sites across four files**: `jobs/api.ts` (3),
   `workers/api.ts` (8, one interpolating two segments), `admin/users/api.ts` (3) and
   `admin/reservations/api.ts` (1). The Phase 4 finding named two of those files; reading the tree
   while filing the item found the other two, which is the "verify each proposal bullet against
   current code" rule paying out on the retro's own output. Low overall because the id is
   normally the caller's own route param or a server UUID, so the reachable targets are endpoints
   that caller can already call - **with one exception worth a human's eyes**:
   `workers/api.ts:154`'s second segment is `Workspace.short_id`, which the server stores verbatim
   from the gRPC wire (`internal/worker/handler.go:919,942`), so an enrolled agent chooses a
   string that is later interpolated into a `POST` path in an **admin's** browser. That row crosses
   a trust boundary the other fourteen do not.

Considered and **not** filed: the `queue` overlap option (a scheduler product decision, not a UI
gap - the same treatment `selector` got in the reservations spec); a cron *explainer* string (it
is a cron parser by another name, and folds into the next-fires item); `name` editing from the
detail page (one input whenever somebody wants it, with no unanswered design question worth
carrying an item for); and optimistic concurrency on `PATCH` (`UpdateScheduledJob` is a bare
`WHERE id = $1` with no version column - real, but a backend design question with a blast radius
far beyond this page, and stating it in the spec is the honest treatment until somebody is
actually bitten).

## Known Limitations

- **The detail-to-detail transition guarded in Problem 2 is unreachable from the shipped UI.** No
  link goes from one schedule detail to another. The guard and its test are pre-emptive, and the
  test constructs the transition synthetically. If that ever looks like dead code, read
  Problem 2 before deleting it.
- **Concurrent edits are last-writer-wins.** No version column, no 409. Narrowed by the
  changed-fields body and surfaced within 10s by the poll, but two admins editing one schedule
  still silently overwrite each other.
- **The owner is never shown.** By construction, until the filed backend item lands.
- **Recent runs is a 20-row window with no pager**, and its footer is the only thing telling the
  user so.
- **Narrow viewports overflow horizontally**, measured and filed, unfixed. A user on a 768px
  tablet or a half-width desktop window gets a horizontal scrollbar on this page and on most
  others.
- **No end-to-end coverage in CI.** The real-browser lane ran once, by hand, against a mock
  backend. Nothing in `npm test` exercises a real browser, so every one of its findings is a
  point-in-time measurement, not a regression guard. `idea-2026-06-03-web-e2e-harness` remains
  open and this is the strongest evidence yet for it.
- **The page was never exercised against a real `relay-server`.** The mock backend served
  hand-written fixtures. Contract drift between those fixtures and the Go handlers would not be
  caught by anything in this branch.

## Improvement Goals

Carried forward:

- **Verify a backlog item's technical claims against the code during spec** - honored, fourth
  iteration running, and it paid the most this time. The item implied recent-runs was
  unavailable; spec verification found `GET /v1/jobs?scheduled_job_id=` is a real, auth-gated,
  indexed branch (`internal/api/jobs.go:424-454`) with auth deliberately ahead of pagination, so
  the panel shipped fully real instead of being scoped out.
- **A backlog proposal is not a contract** - honored in both directions, which is new. The item
  said "wire the list's Edit action to it"; there was no Edit action to wire, so it was built.
  And the spec found **three** things the item never mentioned - multi-next-fires, the owner line,
  job-spec editing - and scoped each out with a named enabler rather than fabricating a surface.
  It applied a third time inside this retro: the path-interpolation finding named two files and
  the tree had four.
- **Stage the work so RED is behavioral** - honored; every task states its expected failure text
  and the plan says outright that a test going green before the implementation exists is vacuous
  and must be fixed rather than proceeded past.
- **A cadence test must assert the wiring** - **inherited and initially failed** (Findings
  Triage), then fixed in Phase 4. The note existed; it did not reach the author of a new hook.
- **A zero-finding broad review does not close the question** - amended rather than confirmed:
  this time the broad review found 3 mediums and was *still* partially refuted, so the rule
  generalizes to "the broad review's output is an input to the lenses, whatever its count".
- **Backlog housekeeping is required scope** - the conductor closes
  `idea-2026-06-05-schedule-detail-page` via `/backlog close`, and per the previous retro's
  Problem 5 must grep the tree for the slug and repair inbound references in the same commit.

New from this iteration:

- **An invalidation is a continuation, and a broad key prefix is a teardown hazard.** "End the
  generation before releasing the resource" has a cache form with no `abort`/`close`/`cancel` to
  grep for. When a mutation destroys a resource, remove that resource's own keys *before* any
  broad invalidate, and check the library's callback ordering rather than assuming the component
  has unmounted. **Candidate for durable memory**, alongside the existing SSE-abort note.
- **Weakening an assertion to tame a flake is a debt with an expiry date.** `UsersTab.test.tsx`
  had already once been weakened for this exact instability; the weakening stopped being
  sufficient one branch later, and the real fix was to make the test deterministic and *restore*
  the strong assertion. When a flake forces a choice, remove the nondeterminism - never the
  discrimination.
- **A new test file is a load change.** Six new files pushed an unrelated real-timer test over a
  race threshold. Treat "new tests destabilized an old test" as a first-class outcome to measure,
  not as noise to wait out.
- **Read the route param and the rendered row as one question.** With `keepPreviousData`, a
  detail page can render row A while its params say B, and the loading/error triad does not
  catch it. Any control that carries an id must assert `data.id === params.id` first. This is the
  frontend twin of the epoch fence: the cached row establishes *content*, the route param
  establishes *identity*, and acting on one while displaying the other is the whole bug.
- **When the Go diff is empty, spend the integration lane on a real browser.** It produced three
  findings no jsdom test could have produced (scroll containment, hit-test stacking, page
  overflow) and one empirical confirmation of a cadence.
- **A refutation of a subagent's report should trigger a tree grep, not just a note.** That is
  the step that turned this iteration's prose defect into a non-event.
- **A finding's stated scope is a starting point, not a census.** Filing the path-interpolation
  item meant grepping every `api.ts` rather than the two the finding named, and the count went
  from 12 to 15 across two more files. Enumerate at filing time, or the item ships an
  under-specified fix.

## Files Most Touched

- `web/src/schedules/ScheduleDetailPage.tsx` - new. Carries the triad (with the third-consumer
  deviation and its enabler named in a comment), the `schedule.id !== id` guard from Problem 2 and
  the reasoning behind it, the `resetOtherActionErrors` helper from Problem 6, the owner-omission
  rationale, and the delete confirm copy stating the verified `ON DELETE SET NULL` consequence.
- `web/src/schedules/useScheduleActions.ts` - the `remove` mutation's `removeQueries`-before-
  `invalidateQueries` ordering and the Invariant-1 argument for it (Problem 1).
- `web/src/schedules/ScheduleTriggerForm.tsx` - the changed-fields diff. The one file where a
  plausible implementation is silently wrong in production and green in tests.
- `web/src/schedules/api.ts` - the four new clients, all `encodeURIComponent`-encoded, with the
  `next_run_at` recomputation hazard documented at `SchedulePatch` so a future caller cannot
  reintroduce a whole-form PATCH without reading why not.
- `web/src/schedules/useScheduleRuns.ts` / `.test.tsx` - the 20-row window, and the behavioral
  cadence test that replaced the constant assertion.
- `web/src/jobs/api.ts` - `listJobsBySchedule`, with the "do not unify this with `listJobs`"
  instruction and the 400 it prevents.
- `web/src/admin/users/UsersTab.test.tsx` - not part of the feature; the determinism fix, the
  restored `toBe(1)`, and the corrected comment about what `shouldAdvanceTime` does and does not
  guarantee (Problem 3).
- `web/src/schedules/ScheduleDetailPage.transition.test.tsx` - new, created by a Phase 4 finding
  rather than by the plan.

## Verification

- **Web suite reported green at 890 tests** (813 before), by the implementing engineer and the
  lanes. **This pass had no shell**, so neither the count nor the green state is independently
  re-run in this document - it is reported, not verified here. The exact-file-set check and the
  final gate run are the conductor's, per the standing rule that subagent claims are verified
  against the tree rather than trusted.
- Every factual claim in this retro that could be checked by reading was checked: the
  `removeQueries` ordering, the `schedule.id !== id` guard, `encodeURIComponent` at all five
  schedules call sites and its **absence** at 15 sites across four other client files,
  `UsersTab.test.tsx:144`'s restored `toBe(1)`, `handleGetScheduledJob`'s missing
  `fillOwnerEmails` against `:504`'s presence, the three verbatim triads, the `grid-cols-2`
  instances and `ServerTab.tsx:70`'s `md:grid-cols-2`, the nine table column templates, the
  agent-supplied origin of `Workspace.short_id`, the behavioral cadence test, and the absence of
  the refuted lifecycle justification from the committed test file.
- **Not verified:** the production build, the browser measurements (single manual run, no
  artifact in the repo), the test count, and anything requiring execution.
- Phase 4 was four lanes dispatched in one message - invariants, correctness, security, and a
  real-browser lane in the integration slot - over a conductor-run `/code-review` that supplied 3
  mediums as prior findings. Each lane confirmed or refuted those independently before adding its
  own.
- `web/dist` is tracked but stale from the scaffold; a frontend build dirties it, and it must be
  reverted before the change set is assembled.
