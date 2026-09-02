---
date: 2026-09-02
topic: web-spa-batch
branch: claude/web-spa-batch-fanin
range: 4ad8d6552bad9318f2f915900b946481f380fae2..f2326be79e9028e0b1f9e38479f8457b382a22ee
---

# Session Retro: 2026-09-02 - The Web SPA Batch

**TL;DR:** The roadmap said the embedded web app was the active phase, but recent sessions had been
taking backend items off the top of the list instead. This session took the ten highest web items and
ran them as six parallel streams of work, each through the project's full spec, plan, implement,
review and pull-request pipeline, with at most five streams active at once. All ten shipped as six
pull requests: the header menu now collapses on phones instead of clipping, the page-by-page list
walker takes the page it is leaving instead of a number it cannot check, sort toggling has one shared
helper, the schedules footer formats like the other six, keyboard focus is preserved across sign-out,
a worker's page lists what it is running, the task-log view opens at the end of a long log, and the
jobs page gained a lane-per-status view. Review found no shipped logic defects; what it found, every
time, was tests that could not fail, comments that claimed more than the code showed, and one browser
check that was green against the exact bug it was written for.

## Handoff

Six unmerged PRs, one per lane, all green on their own gates and re-verified by the conductor:
[#169](https://github.com/chadmv/relay/pull/169) lane C (`claude/web-c-usermenu-focus`, closes
`idea-2026-08-13-usermenu-outside-mousedown-drops-focus` and
`idea-2026-08-13-post-logout-focus-lands-on-body`); [#170](https://github.com/chadmv/relay/pull/170)
lane B (`claude/web-b-pager-chain`, four commits closing `idea-2026-08-14-cursor-pager-next-takes-the-page`,
`idea-2026-08-14-toggle-sort-generic`, `bug-2026-08-14-schedules-footer-range-not-localized`,
`bug-2026-08-14-stale-citations-in-gate-frozen-test-files`); [#171](https://github.com/chadmv/relay/pull/171)
lane A (`claude/web-a-header-nav`, closes `bug-2026-08-24-header-nav-is-clipped-at-narrow-viewports`);
[#172](https://github.com/chadmv/relay/pull/172) lane E (`claude/web-e-worker-tasks`, closes
`feature-2026-06-05-worker-detail-activity-panel` with the aggregate carved out);
[#173](https://github.com/chadmv/relay/pull/173) lane F (`claude/web-f-jobs-lanes`, closes
`idea-2026-06-05-jobs-lanes-swimlanes-view`); [#174](https://github.com/chadmv/relay/pull/174) lane D
(`claude/web-d-tasklog-tail`, closes `idea-2026-08-09-task-log-tail-and-paging-improvements` with
virtualization and export re-filed). No merge grant was given in-conversation, so nothing was merged.
Merge order: B before F is the only constraint (both touch `web/src/jobs/JobsPage.tsx`; F kept the
`JobsTable` element and its `footer` prop byte-identical, so the reverse order should also apply, but
B first is the tested direction). Each lane's spec is `docs/superpowers/specs/2026-09-01-<lane>-design.md`
and its plan `docs/superpowers/plans/2026-09-01-<lane>.md`, committed on the lane branch. Worktrees
live under `.claude/worktrees/web-<lane>` with `web/node_modules` installed; remove them after merge.

This fan-in branch (`claude/web-spa-batch-fanin`) carries 18 follow-up backlog items filed from the
lanes' Phase 4 findings and this retro. `ROADMAP.md` was NOT refreshed: its web-frontend section still
leads with the ten closed items. Next session: merge the six PRs (B before F), run `/roadmap`, then
start at the Now section, where `post-v1-jobs-is-not-rate-limited` still leads.

Two operational facts for the next session: the `internal/api` integration lane now runs about 9.5
minutes, so `-timeout 600s` is inside its variance band (one red run this batch had no failing test,
only the deadline); and the session limit kills every running agent at once, but each resumes cleanly
from its transcript with a `SendMessage` that names the worktree state it should re-read first.

## What Was Built

- **Lane A, header nav.** One DOM copy of the four destinations inside a `Main navigation` landmark,
  a `Menu` toggle with `aria-expanded` and `aria-controls` in both states, a header-anchored full-bleed
  panel driven by `max-md:` classes (the repo's first `max-*` variant), and UserMenu's handler set for
  Escape, outside mousedown, focusout and modifier clicks. `web/e2e/nav.ts` gained a reachability
  predicate using `toBeInViewport()` plus a no-scroll assertion on the panel at 768 and 1280.
- **Lane B, the pager chain.** `useCursorPager.next(page)` with a required `next_cursor` and readonly
  `items`; one `toggleSort<S>` in `web/src/lib/` whose field parameter is constrained to the union's
  base names via a `SortFieldOf<S>` conditional type, pinned by a `@ts-expect-error` line the `tsc -b`
  lane compiles; the schedules footer's zero-rows branch and localization; three test citations by
  symbol; a `web/CLAUDE.md` rule against line citations in comments.
- **Lane C, focus.** The dead-space press stays an accepted gap, recorded in the mousedown handler and
  pinned by a test; `autoFocus` on the first control of both auth screens, with three route-level tests
  (direct visit, real sign-out, real 401 teardown) and a Playwright assertion.
- **Lane D, task-log tail.** `order=desc`, `before_seq`, `prev_seq` on `GET /v1/tasks/{id}/logs`, items
  ascending within a page in both directions, a pure `parseTaskLogQuery`, two bounded store statements,
  no migration; the SPA opens at the tail, subscribes, backfills, and offers `Load earlier` with an
  exact seam join and a predictive line-cap guard.
- **Lane E, worker tasks.** `GET /v1/workers/{id}/tasks` (active tasks, any authenticated user, the
  existing partial index, `total` as the dispatcher's used-slot number, `assignment_epoch` absent and
  pinned by closed-set key tests); a current-tasks panel on the worker detail page and a real Slots KPI.
- **Lane F, Jobs Lanes.** A persisted Table / Lanes switch; five status lanes from a `JOB_STATUSES`
  tuple the `JobStatus` type derives from; one `useQueries` hook on the table's cadence with the table
  poll disabled while lanes show; per-lane failure isolation; `+ N more` routing to the table with that
  status chip (adding `Cancelled`); a keyboard-reachable horizontal scroller; a `jobs-lanes` e2e surface.

## Key Decisions

- **Six lanes, cap five, one worktree and one branch per lane, one PR per lane.** Items 2, 3, 5 and 4
  chained on one branch because they share seven surfaces and their gate-frozen tests; items 7 and 6
  chained because they share a component; the two backend-blocked items ran backend then frontend in
  one worktree; the Jobs Lanes item waited for a slot and carried a written merge contract with the
  pager chain instead of waiting for it to merge.
- **Autonomous gates, with every user question decided and recorded.** Each spec's Decisions section
  lists the question, the options and the reason; each spec's Escalations section names the calls a
  human might make the other way.
- **Combined review lenses on zero-Go diffs, three lenses plus the integration tester on Go diffs.**
  The trivial lane got one combined reviewer; the two Go lanes got the full fan-out, and both times the
  integration tester's probes became committed tests in the fix round.
- **PRs, not merges.** No in-conversation grant; the PRs are the deliverable and the merge is the
  user's step.
- **Spec-level scope cuts filed, never silently dropped**: the jobs-today aggregate (E), row
  virtualization and export (D), the cards-per-lane stepper and fluid desktop grid (F).

## What Went Wrong and What Changes

Ledger: the prior retro's entries were all promoted or ruled one-off, so none carries.
[[feedback_commit_heredoc_shell]] (its one CARRIED entry) was applied throughout and did not recur.
Promoted lessons that fired this session: [[feedback_backlog_proposal_not_contract]] (five of six
specs refuted their item: a symbol that did not exist, a page-envelope premise the endpoint never
shared, an `enabled` gate that could never gate, a `+N more` link target the SPA cannot express, an
`ActiveTaskCounts` statement that is really `CountActiveTasksByAllWorkers` with no worker filter);
[[reference_plan_supplied_tests_untrusted]] and [[feedback_regression_test_must_distinguish_fix]]
(three plan-supplied tests were vacuous as written); [[reference_uniqueness_claim_is_about_the_complement]]
and the CLAUDE.md Comments policy (complement claims were the dominant finding in every lane, and
deletion-first held); [[feedback_verify_tree_not_subagent_claims]] (every gate re-run by the conductor
before each PR); [[reference_tailwind_scans_prose]] (extended below);
[[feedback_concurrent_agents_share_one_git_index]] (applied by sequencing, extended below).

- **A browser predicate was green against the exact bug it was written for.** Lane A's plan supplied
  a Playwright reachability check built on `isVisible()`, with a comment claiming it failed at HEAD.
  Nobody had run it against the old shell. `isVisible()` is `checkVisibility` plus a non-empty box and
  never considers clipping by an ancestor scroller, so all four clipped links reported visible and
  the loop never asserted. The correctness lens read Playwright's bundled source; the fix round measured
  the new `toBeInViewport()` form against `origin/main`'s shell and got 13 failures at 320 and 13 at 375.
  -> **What changes:** when an e2e assertion claims a link or control is "visible", ask which
  instrument decides it; Playwright's `isVisible` and `toBeVisible` cannot see clipping inside a
  scroll container, so a reachability claim needs `toBeInViewport` or a `scrollWidth <= clientWidth`
  assertion on the scroller. And a plan-supplied "fails at HEAD" claim is measured before it is
  believed, by putting the pre-fix file back and running the test. (promoted to `web/CLAUDE.md`)

- **Class names spelled in a comment kept the emitted-CSS control green with the classes deleted.**
  Lane A's A/B control (strip the breakpoint classes from the component, rebuild, require the rules to
  vanish) reported three of sixteen utilities still present, and all three were the ones two comments
  in the same file spelled literally. The test file had been disciplined (`NARROW + 'absolute'`); the
  component's own prose had not.
  -> **What changes:** a producer check for a scanned class must strip the class from every scannable
  site, and the cheapest way to make that true is to never spell a utility in prose: name the CSS
  property. Run the A/B control after any comment edit in the file, not only after code edits.
  (promoted: extended [[reference_tailwind_scans_prose]])

- **A mutation kill was pinning a different guard than the one it was named for, twice.** Lane D's
  `a stale earlier page is discarded` switched tasks, so the effect cleanup set `cancelled` and the
  generation fence was never evaluated; deleting the fence left the test green. Lane B's wrong-page
  substitution was killed by an existing pager test only because that test's fixture carried an empty
  `next_cursor`, which turned the substitution into a no-op. Both kills were real and both were
  attributed to the wrong branch.
  -> **What changes:** when a mutation kills a test, read the failure and name the branch that
  produced it; if it is not the guard under proof, the guard is still unpinned. A degenerate fixture
  value (an empty cursor, a zero total) can make a wrong-target mutation collapse into a no-op that a
  neighbouring test kills for its own reasons. (promoted to [[reference_a_kill_must_name_its_guard]])

- **Review-lens probes passed and were deleted, and the lanes had to be told to commit them.** Both
  integration testers wrote probe tests (a NULL keyset arm, an exact-multiple walk, a before-page
  ordering, a gap-value cursor), all green, then deleted them per their brief; every one was a
  coverage gap the committed suite did not have, and each became a real test only in the fix round.
  -> **What changes:** brief the integration lens to leave its probe files in place for the owning
  engineer to adopt, and treat a green probe as a test candidate, not a verdict. (promoted: extended
  [[reference_mutation_proof_must_leave_a_test]])

- **Backend and frontend halves in one worktree cost a round trip per fix round.** Lanes D and E
  ran both engineers against one worktree; the second fix round had to wait for the first to land
  because two agents committing into one index collide. The sequencing worked, and it was the slowest
  step in both lanes.
  -> **What changes:** when a plan declares backend-then-frontend in one worktree, give the second
  half its own worktree branched from the first's tip once the first half has landed, or accept the
  sequencing and dispatch nothing else against that worktree in between. (promoted: extended
  [[feedback_concurrent_agents_share_one_git_index]])

- **The session limit killed all five running agents at once, twice, mid-task.** Every agent resumed
  cleanly from its transcript when sent a message that named the worktree state it should re-read
  before continuing (commits present, files modified); none re-did finished work and none trusted its
  own memory over `git status`.
  -> **What changes:** on a rate-limit kill, inspect each worktree first (`git log`, `git status`,
  running test binaries), then resume each agent with a message that states what it will find and
  where it stopped; never re-dispatch a fresh agent for work whose transcript still exists.
  (promoted to [[feedback_resume_agents_after_a_rate_limit_kill]])

- **A 600 s integration timeout was inside the lane's variance band.** Lane D's first api-lane run
  reported `FAIL` at 601 s with no failing test; the package's real runtime is 540 to 600 s. The engineer
  diagnosed it rather than calling it a flake and re-ran at 1800 s.
  -> **What changes:** run the `internal/api` integration lane with `-timeout 1800s`; a `FAIL` line
  with no `--- FAIL` beneath it is the deadline, not a test. (promoted to `CLAUDE.md`)

- **A spec rejected a hi-fi treatment it had not read.** Lane F's spec argued against any desktop
  breakpoint because "stacking nests a vertical scroller in a vertical scroller", but the hi-fi's
  `HoloLanes` at desktop is a four-column fluid grid, neither stacking nor scrolling. The row now
  always scrolls at 1280 with the fifth lane clipped mid-word.
  -> **What changes:** when a spec rejects a hi-fi mechanism, quote the hi-fi's actual CSS for that
  breakpoint, not the item's paraphrase of it. Filed as a follow-up rather than promoted; one instance.

## Recommended Backlog Items

Intake, not a priority order; all eighteen were filed on this branch before the retro was written.

- See [`idea-2026-09-02-collapsed-nav-touch-dismiss-has-no-lane`](../backlog/idea-2026-09-02-collapsed-nav-touch-dismiss-has-no-lane.md) - the nav dismisses on mousedown and no lane emulates touch
- See [`bug-2026-09-02-taskdag-scroller-has-no-tab-stop-or-name`](../backlog/bug-2026-09-02-taskdag-scroller-has-no-tab-stop-or-name.md) - the DAG panel clips behind an unlabelled scroller
- See [`idea-2026-09-02-footer-range-tolocalestring-halves-unpinned`](../backlog/idea-2026-09-02-footer-range-tolocalestring-halves-unpinned.md) - no footer test ranges past 999
- See [`idea-2026-09-02-line-citation-retrofit-under-web-src`](../backlog/idea-2026-09-02-line-citation-retrofit-under-web-src.md) - roughly 342 matching lines after the rule landed
- See [`idea-2026-09-02-no-web-test-pins-an-unsupported-sort-400`](../backlog/idea-2026-09-02-no-web-test-pins-an-unsupported-sort-400.md) - the server 400 is the backstop and no surface renders it
- See [`bug-2026-09-02-publiconlyroute-renders-auth-screens-during-loading`](../backlog/bug-2026-09-02-publiconlyroute-renders-auth-screens-during-loading.md) - an authenticated direct visit flashes and focuses a form that unmounts
- See [`idea-2026-09-02-route-change-focus-and-announcement-policy`](../backlog/idea-2026-09-02-route-change-focus-and-announcement-policy.md) - after sign-in, arrival at /jobs lands on body
- See [`idea-2026-09-02-task-log-row-virtualization`](../backlog/idea-2026-09-02-task-log-row-virtualization.md) - carved out of the tail item
- See [`idea-2026-09-02-task-log-export-endpoint`](../backlog/idea-2026-09-02-task-log-export-endpoint.md) - carved out, with the byte-exactness foreclosure
- See [`idea-2026-09-02-tail-mode-for-cli-sdk-and-mcp`](../backlog/idea-2026-09-02-tail-mode-for-cli-sdk-and-mcp.md) - MCP now passes a permanent prev_seq 0 through
- See [`idea-2026-09-02-load-earlier-failure-is-silent-and-unbounded`](../backlog/idea-2026-09-02-load-earlier-failure-is-silent-and-unbounded.md) - a swallowed failure and an un-abortable fetch
- See [`idea-2026-09-02-shared-task-log-fixture-helper`](../backlog/idea-2026-09-02-shared-task-log-fixture-helper.md) - an omitted prev_seq fails open
- See [`idea-2026-09-02-agent-reported-task-progress`](../backlog/idea-2026-09-02-agent-reported-task-progress.md) - the enabler for the hi-fi's progress bar
- See [`idea-2026-09-02-measure-the-populated-worker-detail-panels`](../backlog/idea-2026-09-02-measure-the-populated-worker-detail-panels.md) - no lane renders the page with a task row
- See [`idea-2026-09-02-jobs-go-hand-written-store-job-copies`](../backlog/idea-2026-09-02-jobs-go-hand-written-store-job-copies.md) - thirteen partial literals, not six
- See [`idea-2026-09-02-jobs-lanes-fluid-columns-at-desktop`](../backlog/idea-2026-09-02-jobs-lanes-fluid-columns-at-desktop.md) - the hi-fi does not scroll at desktop
- See [`idea-2026-09-02-extract-usepersistedview`](../backlog/idea-2026-09-02-extract-usepersistedview.md) - two view switches already diverge
- See [`idea-2026-09-02-cards-per-lane-stepper`](../backlog/idea-2026-09-02-cards-per-lane-stepper.md) - the deferred per-lane cap control

## Files Most Touched

Across the six lane branches; this fan-in branch itself touches only `docs/backlog/` and this file.

- `web/src/shell/HoloShell.tsx` and its test - the collapsed nav, its handler set and eighteen new tests (A)
- `web/e2e/nav.ts`, `header-nav.spec.ts`, `layout.spec.ts` - the reachability predicate and breakpoint pins (A)
- `web/src/lib/useCursorPager.ts`, `toggleSort.ts` and the seven surfaces' pager wiring (B)
- `web/src/auth/LoginScreen.tsx`, `RegisterScreen.tsx`, `authArrivalFocus.test.tsx` (C)
- `internal/api/tasks.go`, `task_log_query.go`, `tasks_integration_test.go` - the descending mode and its wire battery (D)
- `web/src/jobs/useTaskLogStream.ts`, `logBuffer.ts`, `LogView.tsx` - tail-open, prepend, Load earlier (D)
- `internal/api/workers.go`, `workers_tasks_integration_test.go`, `internal/store/query/tasks.sql` - the worker tasks endpoint (E)
- `web/src/workers/WorkerTasksPanel.tsx`, `WorkerDetailPage.tsx` - the panel and the Slots KPI (E)
- `web/src/jobs/JobsLanes.tsx`, `useJobLanes.ts`, `lanes.ts`, `JobsPage.tsx` - the Lanes view and switch (F)
- `internal/store/tasks_status_vocabulary_lockstep_test.go` - the census corrected and widened (E, F)
