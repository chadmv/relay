# Session Retro: 2026-05-04 — Flaky Notify Listener Test

## What Was Built

Fixed the pre-existing flaky integration test `TestNotifyListener_TriggersOnNotify` in `internal/scheduler/notify_test.go`. The root cause was a race: the test sent `pg_notify` after a fixed 200 ms sleep, but Postgres silently drops notifications sent before a session has `LISTEN`ed. Under integration-suite load the sleep was insufficient, causing the second `Eventually >= 2` assertion to time out.

The fix replaces the fixed sleep with a `sendUntilConsumed` helper that retries `pg_notify` every 20 ms (5 s window) until the trigger counter increments. A 200 ms drain sleep was added between the two channel calls and the "unrelated channel" negative check — this was not in the original spec but was discovered necessary: the retry loop can queue multiple NOTIFYs in the Postgres pipeline, and without draining them first the `before` baseline is captured while extras are still in flight, causing the negative-check assertion to fail spuriously. Passed 5/5 consecutive runs and the full integration suite.

Also in this range: `CLAUDE.md` was slimmed below 70 lines, and a `fakeRunner` fix landed that makes unknown fixture keys fail loudly rather than silently.

## Key Decisions

**Test-only fix, no production surface area.** The alternative was to expose a `Ready() <-chan struct{}` on `NotifyListener` so the test could wait for `LISTEN` to attach rather than polling. Rejected because no production caller needs this ordering guarantee — adding it would be production surface area solely for a test. The retry loop is entirely self-contained in the test file.

**Drain sleep is correct.** The spec's proposed code did not include it, and the code quality reviewer incorrectly flagged it as unnecessary. Analysis: without the drain, residual queued NOTIFYs from the retry loop fire during the 200 ms observation window *after* `before` is snapshotted, not before — breaking the negative-check assertion. The drain ensures the pipeline is flushed before `before` is captured.

## Problems Encountered

**Secondary flake not caught by spec.** The spec's `sendUntilConsumed` helper was correct for the race it was designed to fix, but introduced a secondary issue: the retry loops can leave duplicate queued NOTIFYs on valid channels. The implementer subagent caught and fixed this during the 5-run stability check. The spec was updated in the retrospective only (not re-committed).

**Code quality reviewer incorrectly diagnosed the drain sleep.** The reviewer recommended removing the 200 ms drain sleep on the grounds that `before` is re-snapshotted after the sleep. This reasoning is backwards — the whole point of the drain is to ensure all pending NOTIFYs flush *before* `before` is captured. The diagnosis was overridden by the coordinating agent.

## What We Did Not Do Well

- The spec did not anticipate the secondary flake introduced by the retry loop (duplicate queued NOTIFYs corrupting the negative-check baseline). The drain sleep was a correct and necessary fix discovered only during the 5-run stability check — it should have been part of the original design.
- The code quality reviewer incorrectly diagnosed the drain sleep as unnecessary, demonstrating that the review step is not infallible for subtle timing logic. The coordinating agent had to override the reviewer's analysis.

## Improvement Goals

- When writing specs for retry-loop tests, explicitly reason about accumulated side effects: does the loop leave queued messages in the system that could affect subsequent assertions? Add a drain/settle step to the spec before the next assertion if so.
- For timing-sensitive test logic, flag code quality review findings as "verify the analysis independently" rather than accepting them at face value.

## What We Did Well

- Brainstorming surfaced the root cause and three clean approaches before touching code; the right one was obvious once laid out.
- Subagent-driven development caught the secondary flake during the 5-run stability check, exactly the kind of thing that would have slipped through a single-run verification.
- The spec and plan were tight enough that the implementer had everything needed without asking questions.

## Files Most Touched

- `internal/scheduler/notify_test.go` — rewrote `TestNotifyListener_TriggersOnNotify` with retry loop and drain sleep
- `docs/superpowers/specs/2026-05-04-flaky-notify-listener-test-design.md` — new spec
- `docs/superpowers/plans/2026-05-04-flaky-notify-listener-test.md` — new plan
- `CLAUDE.md` — slimmed to under 70 lines (separate session cleanup)
- `internal/agent/source/perforce/fakerunner_test.go` — fakeRunner unknown-key fix (prior session)

## Commit Range

0932d18..c688c01
