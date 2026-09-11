---
title: BaselineHashFromAPISpec is recomputed inside selectWorker's worker loop
type: idea
status: open
created: 2026-09-10
priority: low
source: Found while specifying the source.sync entry-count bound (PR #207); it is the cost factor that item omitted
---

# BaselineHashFromAPISpec is recomputed inside selectWorker's worker loop

## Summary

`selectWorker` calls `BaselineHashFromAPISpec(taskSrc)` inside its loop over candidate workers. The
value is invariant in the worker being scored - it is a function of the task's source spec alone - so
it is re-sorted and re-hashed once per candidate worker holding a matching warm workspace, on the
coordinator, on every dispatch attempt.

## Context

`warmKey`, one branch above, is already hoisted out of the loop with a comment explaining why, so the
pattern is established and this call was simply missed.

The call is gated on a warm-key match and `break`s on the first hit, so the multiplier is the number
of candidate workers that hold a matching warm workspace rather than the whole fleet. That is why
this is an idea rather than a bug: it is wasted work on the dispatch path, not an unbounded one.

Scale is set by the sync entry count, since `BaselineHash` builds and sorts one element per entry -
now bounded at 512 per spec.

## Proposal

Hoist it beside `warmKey`, with the same shape of comment.

## Acceptance / Done When

- The hash is computed once per dispatch attempt rather than once per matching candidate worker.
- A guard pins that it is outside the loop, since the defect is invisible to any behavioural test -
  the result is identical either way.

## Related

- `internal/scheduler/dispatch.go` (`selectWorker`),
  `internal/agent/source/perforce/baseline.go` (`BaselineHash`)
