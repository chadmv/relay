---
title: The provider's progress callback blocks on a send bounded only by the agent context, while a workspace handle is held
type: bug
status: open
created: 2026-09-03
priority: medium
source: Phase 4 invariants and security lenses of the prepare-failure-visibility batch, 2026-09-03
---

# The provider's progress callback parks while holding the workspace

## Summary

`makePrepareProgressFn`'s callback flushes through `Runner.send`, which selects only on `r.sendCh`
and `r.ctx` - and `r.ctx` is the **agent** context, documented as living for the agent lifetime
rather than the connection. `sendOrAbort` exists precisely because `r.send` cannot be woken by a
per-task cancel. So on a healthy agent whose coordinator has stopped draining, a progress call
parks until process shutdown - and the provider makes those calls while holding a workspace handle.

## Repro / Symptoms

Coordinator stops reading (a partition, or a wedged server-side reader). The 64-slot `sendCh`
fills. A task takes `ModeExclusive` (any spec with `unshelves`), its `p4 sync` fails fast on a case
that prints nothing to stdout - bad `P4PORT`, unknown client - and the progress call parks. Every
subsequent task for that stream on that worker then blocks in `Workspace.Acquire` until its own
per-task deadline expires.

## Context

This is the release direction of the first Invariant, in its general shape: do not interpose an
unbounded wait between a failure and the release of the resource that failure has doomed.

The 2026-09-03 batch closed **one branch** of this by moving `handle.Release()` above the
`[sync] failed` progress line, pinned by a test that observes the holder count from inside the
callback. It did not close the capability: `progress("[sync] starting: ...")` and the `[recover]`
line both still park while holding the workspace, and **they cannot be reordered away**, because
the sync itself must run under the hold. `lastFlush` is the zero `time.Time` on the first call, so
the starting bracket always flushes rather than batching.

## Proposal

Give provider progress the `sendOrAbort` treatment that `chunkWriter.flush` already has, so a
parked send is woken by the task's own cancel rather than only by agent shutdown. The comment at
`sendOrAbort` already states the reason it exists; this is the second caller that needs it.

Consider whether the progress callback should drop lines rather than block at all - task log
output is best-effort by design, and the fence already discards a stale chunk.

## Acceptance / Done When

- A wedged coordinator cannot make a provider progress call hold a workspace past the task's own
  cancellation.
- The guard observes the holder count from inside the callback, as the existing release-ordering
  test does - an assertion made after `Prepare` returns cannot discriminate the ordering.

## Related

- `internal/agent/runner.go` - `makePrepareProgressFn`, `send`, `sendOrAbort`
- `internal/agent/source/perforce/perforce.go` - the `[sync]` brackets and the `[recover]` line
- `internal/agent/source/perforce/workspace.go` - `Acquire`, `Release`
- [[bug-2026-08-20-a-late-dispatch-send-can-start-an-orphaned-subprocess]] - a different unbounded-send
  window on the dispatch side
