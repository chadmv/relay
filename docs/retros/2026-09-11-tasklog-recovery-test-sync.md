---
date: 2026-09-11
topic: tasklog-recovery-test-sync
branch: claude/github-ci-failure-9d29dc
range: fa9971be..1bae9a2c
---

# Session Retro: 2026-09-11 - Task Log Recovery Test Sync

**TL;DR:** Continuous integration went red on the project's main branch because one frontend test
failed. The test was not catching a real bug in the app. It checked that the task-log viewer had
finished recovering from a dropped connection by waiting for a status value that was *already* set
to the value it was waiting for, so it never actually waited for anything. On a fast machine the
recovery happened to finish in time anyway; on a loaded CI machine it did not. Four tests in that
file had the same mistake, and all four now wait for something that is genuinely false until the
recovery has finished. No application code was changed.

## Handoff

`web-ci` on `main` (run 34563371016) failed exactly one test,
`src/jobs/useTaskLogStream.test.tsx > a recovery pumps since_seq from next_seq until next_seq is 0`,
with `expected [ 'line-10' ] to deeply equal [ 'line-10', 'line-20', 'line-30' ]` (1 failed / 1503
passed). The same tree passed web-ci on PR #212's run, so the tree was not regressed.

Cause: `fakeSseServer.emit()` enqueues into a `ReadableStream`, so a `dropped` frame is delivered
asynchronously and the hook has not reached `'recovering'` when the next statement runs. An
`await waitFor(() => expect(result.current.status).toBe('live'))` placed after the emit is satisfied
by the status already held: it passes on `waitFor`'s first synchronous check and never polls. A
probe measured the callback running exactly once, seeing `status=live` with `searches.length === 1`.
The recovery therefore completed only inside RTL `asyncWrapper`'s trailing `setTimeout(0)` drain.
Injecting one macrotask boundary before the final page's response reproduces CI exactly, including
its odd shape - `searches` holds all three requests (they were ISSUED) while `rows` still holds
`['line-10']`, because `ingest()` only schedules a `FLUSH_MS` publish and the loop's closing
`flushNow()` had not run.

Fixed at four sites in `web/src/jobs/useTaskLogStream.test.tsx` (c25298fa); the four comments it
added were tightened in 1bae9a2c. `useTaskLogStream.ts` is byte-identical to HEAD. Mutation battery
over a green baseline: neutralizing the forward pump (`since = page.next_seq`) and making
`onDropped` a no-op each take all four sites RED. `npx tsc -b` clean; 1504 tests across 189 files
pass. `vite build` was deliberately not run because `web/dist/index.html` is tracked.

Next session starts here: the branch `claude/github-ci-failure-9d29dc` is committed but **NOT pushed
and has no PR**, so `main`'s web-ci is still red until it lands.

## What Was Built

Two commits, one file, tests only:

- **c25298fa** - four drop-recovery tests now wait on the recovery's own observable output instead
  of a status that is already true:
  - `a recovery pumps since_seq ...` waits for the pumped rows, then pins the exact `searches`.
    That assertion now runs strictly later than before, so it can see MORE requests, never fewer.
  - `a discarded earlier page re-enables the control` waits for the recovery's page before
    `release()`, so the gated page cannot land against the generation the test needs it to miss.
  - `an event: dropped frame produces exactly ONE re-backfill ...` gates on the re-backfill request
    with `toBeGreaterThanOrEqual(2)`, keeping the exact `toBe(2)` as its sibling.
  - `a manual reconnect after a drop ...` waits for the re-backfill's page.
- **1bae9a2c** - tightened the four comments c25298fa added (see the lessons below).

No assertion was dropped or weakened. `logSecrecy.test.tsx` emits the same frame but waits on a real
false-to-true transition on `dropped`, so it was left alone.

## Key Decisions

- **Fixed all four sites, not just the one that went red.** The other three are the identical
  defect; shipping only the red one leaves three latent CI failures and no record of why.
- **Diagnosed before touching anything.** The probe and the injected-boundary reproduction came
  first; the exact CI assertion string was reproduced locally before a line of the fix was written.
- **Did not run `vite build`.** `web/dist/index.html` is tracked, so building dirties the tree with
  churn unrelated to a test-only change - and that step had already passed on this exact tree in
  PR #212's run.
- **Committed the comment tightening separately rather than amending.** The user had already
  approved c25298fa; a follow-up commit keeps what changed after that approval visible.

## What Went Wrong and What Changes

Ledger for the prior retro (`2026-09-10-workspace-exclusion-set-ceiling`), whose entries were
unpromoted: its **absolute-words grep** rule was *applied* and fired again here, catching four
over-claiming comments in this session's own diff - it is promoted below rather than carried again.
Its **measure-the-external-tool-first** rule was *applied* in spirit: the diagnosis rested on how
`waitFor`, MSW and React schedule work, and every claim was probed rather than reasoned about. Its
**spec's discriminating input** rule was *applied* - the reproduction was worked through by hand
before the fix was written. The remaining entries (artifact-absence over error, state the quantity a
bound bounds, argv versus stdin, re-read a quantifier when a second writer appears) were *not
exercised*. Promoted memories that fired: [[feedback_a_green_rerun_bounds_not_retires]] - the red was
not closed as "passes on retry", cause was established instead;
[[feedback_web_dist_not_maintained]] - the reason `vite build` was skipped;
[[feedback_never_git_checkout_to_revert_a_mutation]] and
[[feedback_mutation_testing_needs_isolated_tree]] - the mutated source was restored from a saved copy
and verified byte-identical; [[reference_verify_the_mutation_applied]] - each mutant was grepped for
before its run, and the first attempt's anchor did not match and was corrected;
[[reference_a_loosened_assertion_needs_an_exact_sibling]] - the `toBeGreaterThanOrEqual(2)` gate kept
`toBe(2)` beside it; [[feedback_concurrent_agents_share_one_git_index]] - both commits used an
explicit pathspec; [[feedback_assert_encoding_after_a_programmatic_edit]] and the repo's CRLF rules -
eol, lone-CR, non-ASCII and diffstat were checked after every programmatic edit.

- **A wait can be satisfied by the state that existed before the event it is supposed to wait for.**
  `await waitFor(() => expect(status).toBe('live'))` after `emit('dropped')` never waited: the drop
  is delivered asynchronously, so the status was still the pre-drop `'live'` on `waitFor`'s first
  synchronous check. The test passed for two months on the strength of RTL's trailing
  `setTimeout(0)` drain happening to be long enough. The existing rule asks whether a *stranger* can
  satisfy a wait; this is the same failure with the test's own past as the stranger.
  -> **What changes:** before writing a wait, read the line above it and ask what the awaited value
  was one statement earlier. If it already satisfies the condition, the wait is a no-op - predicate
  it on something that is false until the awaited work completes. A status that returns to its
  starting value is never a valid signal for the round trip in between.
  (promoted to [[reference_concurrency_wait_must_be_owned]])
- **A CI-only failure was nearly filed as scheduling noise.** The same tree had passed on another
  run, which is exactly the evidence that terminates an investigation. What established the cause
  was not repetition but a probe recording what `waitFor`'s callback actually observed, and a single
  injected macrotask boundary that turned an unreproducible race into a deterministic failure
  carrying the exact CI assertion string.
  -> **What changes:** to prove a timing hypothesis, inject the scheduling boundary into the fixture
  at the point the hypothesis names, rather than re-running the test. One
  `await new Promise((r) => setTimeout(r, 0))` in the async fixture is usually enough, and a
  reproduction that carries the original error string is proof where a failure count is not.
  (promoted to [[reference_inject_the_boundary_to_reproduce_a_race]])
- **Four comments written for this fix over-claimed, and the prior retro's own rule caught them.**
  Grepping the added lines for absolute words turned up "waits for nothing" three times. The phrase
  is wrong in a way that matters: the *condition* contributes no waiting, but RTL's trailing drain
  does pass time, and that drain is precisely why the old shape passed locally. A reader taking the
  comment literally would conclude the old test could never have passed.
  -> **What changes:** after writing new comments, grep your own added lines for "only", "never",
  "always", "every", "nothing", "any", "cannot" and check each against the code you just wrote.
  (carried from the prior retro, where it caught two defects; fired again here, and now
  promoted to [[reference_wrong_prose_is_the_dominant_defect]])
- **A duplicated assertion was written while fixing a synchronization bug.** The first attempt at the
  fourth site gated with `waitFor(() => expect(requests).toBe(2))` and then repeated
  `expect(requests).toBe(2)` on the next line - two identical assertions microseconds apart, the
  second adding nothing.
  -> **What changes:** when a wait and an assertion share a predicate, loosen the wait to the
  threshold that gates (`toBeGreaterThanOrEqual`) and keep the exact form as the assertion. If the
  two read identically, one of them is not doing a job.
  (already in [[reference_a_loosened_assertion_needs_an_exact_sibling]])

## Recommended Backlog Items

Backlog intake, not a priority order.

- See [`idea-2026-08-13-web-suite-waitfor-flakiness-under-concurrency`](../backlog/idea-2026-08-13-web-suite-waitfor-flakiness-under-concurrency.md) - appended the first
  established cause for that item's shape: a `waitFor` already satisfied when written, which is not
  the concurrency-dependence the item hypothesizes, plus the directed sweep it now prescribes.
