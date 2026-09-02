---
title: A failed Load earlier fetch is swallowed with no user signal, and the un-abortable fetch has no timeout
type: idea
status: open
created: 2026-09-02
priority: low
source: Phase 4 lenses on the 2026-09-01 tail-paging slice (lane D)
---

# A failed Load earlier fetch is silent and unbounded

## Summary
loadEarlierPage catches every failure, resets its flags and returns, so a 500 or a dropped connection
makes the click flash and do nothing, indistinguishable from a legitimately empty page. By design
(spec D17) the fetch carries no AbortSignal, so a hung socket leaves loadingEarlier true for the run's
lifetime with no ceiling.

## Proposal
A transient error line under the control (the drop-marker machinery is the precedent for visible
rather than silent failure), a retryable state, and a bounded wait on the fetch that does not reopen
the generation-ordering hazard D17 avoided.

## Related
- web/src/jobs/useTaskLogStream.ts, docs/superpowers/specs/2026-09-01-task-log-tail-paging-design.md (D17)
