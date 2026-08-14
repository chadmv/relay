---
title: The web suite's waitFor assertions flake under high vitest concurrency, in files a change never touched
type: idea
status: open
created: 2026-08-13
priority: low
source: measured during Phase 4/5 of the 2026-08-13-cross-generation-401 slice, by two lanes independently; the specific reported failure was never reproduced
---

# The web suite's `waitFor` assertions flake under high vitest concurrency, in files a change never touched

## Summary

This is a **measurement to preserve, not a diagnosis**. During the 2026-08-13 cross-generation-401 slice
two lanes independently observed web-suite failures that no code change explains, and the investigation
that followed found a general property of the harness rather than a specific offender.

What was observed:

- A positive-control test failed **3 times** early in the slice.
- A cold run reddened **4 tests across 3 files**, including a file the slice does not modify.

What the investigation found:

- The specific reported failure **could not be reproduced in roughly 90 runs at 4x to 20x contention.**
- What did reproduce was **suite-wide `waitFor` flakiness under high vitest concurrency, reaching
  unrelated unmodified files.**

What was separately found and fixed (recorded so this item is not credited with it): two tests in the
slice's own new file polled `getToken()` and never awaited the second half of `login()`, and one asserted
status with no wait at all. Those were real test defects, fixed additively by awaiting `login()`'s own
promise, with no assertion loosened. They are a **plausible** cause of the 3 early positive-control
failures and are **not proven** to be the cause, and they explain nothing about the unmodified file in
the cold run.

## Repro / Symptoms

Not reliably reproducible, which is the item. The shape when it appears:

1. Run `npm test` from `web/` on a loaded machine, or with vitest's worker count high relative to
   available cores.
2. One or more `waitFor` assertions time out in files unrelated to whatever changed.
3. Re-run. Green.

The tell that distinguishes this from a real regression is **the failing file having no diff**. That is
also exactly the reasoning a tired reviewer uses to dismiss a genuine failure, which is why the standing
project rule is to measure both ways rather than to argue from "that was already broken".

## Context

The cost is not the failing test. It is that **a suite that flakes under load degrades every green gate
this project's process rests on.** The lifecycle treats a green `npm test` as the verification artifact
at three separate phases, and the standing rules ("diagnose a red gate, measure both ways", "verify the
tree, not subagent claims") both assume a red run means something. When a red run might mean nothing, the
cheapest response is to re-run until green, which is precisely how a real regression ships.

Two properties make the web suite a plausible venue for this, both worth checking rather than assuming:

- **jsdom plus fake-free async.** The project has deliberately rejected timer-based tests in favour of
  real awaits and `waitFor` polling. That is the right call and it makes assertion latency dependent on
  how much CPU the worker actually gets.
- **Default worker pool sizing.** Nothing in the project's vitest configuration pins a worker count, so
  concurrency is a function of the machine, and every measurement taken on one machine is silent about
  another.

**This item claims only what was measured.** It does not claim the flakiness has a single cause, does not
name an offending test, and does not assert the suite is unreliable in normal use - the routine gate has
been green across many slices.

## Proposal

**The first step is a diagnosis, not a fix.** Resist reaching for a global `waitFor` timeout bump: that
hides the signal, is unfalsifiable, and makes a genuinely slow assertion indistinguishable from a
starved one.

1. **Establish whether it is concurrency-dependent, with a number.** Run the full suite N times at the
   default worker count and N times pinned to a low `maxThreads` (or `--pool=forks --poolOptions...`,
   whichever the installed vitest version supports), on the same machine, and record failure counts for
   each. Per the standing both-ways rule, a claim without both numbers is not a measurement.
2. **If it is concurrency-dependent, identify the offenders.** Collect the failing test names across runs.
   If they cluster, the item becomes a specific fix in specific files and should be re-scoped to those.
   If they are scattered uniformly, the answer is a harness setting, not a test edit.
3. **Only then choose a remedy.** Candidates, in preference order: fix the specific tests that poll where
   they should await (the pattern the slice already found once); pin the worker count in
   `web/vite.config.*` so the suite's concurrency is a property of the repository rather than of the
   machine; raise a timeout only for a named, justified test.

Deliberately **not** proposed: retry-on-failure, `test.retry`, or any mechanism that makes a red run
green without explaining it. That converts a diagnosable problem into a permanently invisible one.

## Acceptance / Done When

- A both-ways measurement exists: failure counts at default concurrency versus pinned low concurrency,
  same machine, stated as numbers, written into this item or its resolution.
- Either a named set of offending tests with fixes, or a documented harness setting with the measurement
  that justifies it.
- **If the measurement shows no concurrency dependence, close the item and say so.** A recorded negative
  result is a valid outcome here and is more useful than an open item nobody can act on.
- No assertion is loosened and no test gains a retry.

## Related

- The slice that produced the observations, and the test-timing defect found alongside them:
  `docs/retros/2026-08-13-cross-generation-401.md` (Problems 1)
- The tests where a poll was replaced by a real await, which is the concrete instance of the pattern step
  3 asks for: `web/src/auth/AuthProvider.crossgen.test.tsx:30-39`, `:128`, `:251`
- Would change the toolchain this item measures, so the measurement must be retaken after it lands:
  [[feature-2026-06-05-upgrade-vite-vitest]]
- The gate this suite is a proxy for, and the reason its reliability matters:
  [[idea-2026-06-03-web-e2e-harness]]

## Notes

Filed as an idea at low priority, and with its own risk stated plainly: **without a reproducible offender
list this is the kind of item that can absorb triage without converging.** It is filed anyway because the
observations are real, they were made by two lanes independently, and the alternative is that the next
person to see a red run on an unmodified file starts the same investigation from zero. Step 1 is bounded
- two batches of runs and two numbers - and a negative result closes the item honestly.
