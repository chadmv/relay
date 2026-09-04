---
title: sendFinalStatus carries no ErrorMessage, so a cmd.Start failure lands as a failed task with an empty log
type: feature
status: open
created: 2026-09-03
priority: medium
source: Phase 4 correctness lens of the prepare-failure-visibility batch, assessed by the Lane B engineer, 2026-09-03
---

# sendFinalStatus carries no ErrorMessage

## Summary

When `cmd.Start()` fails - a typo'd binary, a tool missing on that worker - `Runner.Run` breaks out
of the step loop and calls `sendFinalStatus`, which sets no `ErrorMessage`. The coordinator
therefore has no cause to store, and the task lands `failed` with an empty log. **That is the exact
shape of the defect the 2026-09-03 batch was filed to fix, one branch to the left of where it was
fixed.**

## Context

The batch gave this path a host-log line, so the cause now exists on the worker host. It did not
give the coordinator anything, because that requires `sendFinalStatus` to carry a message and the
batch's scope was the prepare phase.

The receiving side is already built and needs no change: `handleTaskStatus` stores any
`ErrorMessage` on a terminal report, bounded at `MaxAgentErrorMessageBytes`, NUL-stripped and
UTF-8-coerced, fenced on identity and epoch. The exposure is not new either - `sendStepMarker`
already writes the full argv into `task_logs`, which is [[bug-2026-09-03-sendstepmarker-writes-full-argv-to-task-logs]].

## Proposal

Give `sendFinalStatus` an error-message parameter and populate it on the `cmd.Start` failure path.

**The cost, and the reason this was scoped out rather than done inline:** every call site must then
decide what it carries, including two the batch never reasoned about - the nil-argv break and the
empty-commands early return. Neither has a natural cause text, and inventing one for them is a
decision, not a mechanical edit. Decide those two explicitly rather than defaulting them to empty
and rediscovering this item a third time.

## Acceptance / Done When

- A task whose binary is missing carries the `exec` error as a log line readable through
  `GET /v1/tasks/{id}/logs`, the same way a prepare failure now does.
- The nil-argv and empty-commands paths each carry a deliberate decision about what they report,
  recorded in the commit rather than left implicit.

## Related

- `internal/agent/runner.go` - `Runner.Run`'s `cmd.Start` break, `sendFinalStatus`
- `internal/worker/handler.go` - `handleTaskStatus`, which already stores and bounds the message
- [[bug-2026-09-03-prepare-failure-error-message-is-discarded]] - the same defect on the prepare path
- [[bug-2026-09-03-sendstepmarker-writes-full-argv-to-task-logs]] - the audience question this inherits
