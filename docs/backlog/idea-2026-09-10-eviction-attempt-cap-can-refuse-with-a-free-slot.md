---
title: The exclusion-ceiling eviction attempt cap can refuse while an evictable candidate remains untried
type: idea
status: open
created: 2026-09-10
priority: low
source: Disclosed by the workspace exclusion-set ceiling slice (PR #211) as a deliberate bounded-work tradeoff
---

# The exclusion-ceiling eviction attempt cap can refuse while an evictable candidate remains untried

## Summary

When the per-stream exclusion-set ceiling is reached, the gate tries to evict an unheld workspace
before admitting a new one, and it bounds how many candidates it will try. With more candidates than
the ceiling, the last ones are never attempted - so five held-but-one-free candidates at a ceiling of
4 can refuse even though the fifth was evictable.

## Context

Deliberate and documented at the code, not discovered later. The cap exists because each attempt is a
`p4 client -d` round trip bounded only by `RELAY_EVICTION_TIMEOUT`, so an unbounded scan turns one
prepare into an unbounded sequence of p4 calls - which is the cost the ceiling exists to bound in the
first place.

It is recorded as a **work bound rather than a convergence guarantee**, because an earlier draft
claimed the pass converges in one prepare and that was refuted by simulation: 20 entries at a ceiling
of 4 takes five prepares.

**Why it is low priority.** The refusal is a failed prepare for one task on one stream on one agent,
the task is retried, and the candidate set changes between attempts as holders release. It is not a
stuck state. The shape to watch for is an operator reporting refusals while `relay workers workspaces`
shows a free slot.

## Proposal

Sketch only. Candidates: raise the attempt cap independently of the ceiling; order candidates so the
most likely evictable come first (the ordering already puts empty-baseline entries first, which is the
attacker's junk, so this would be a second key); or retry once after a short delay. Each trades p4
round trips for success rate, so measure how often it actually fires before changing it.

## Acceptance / Done When

- Either the refusal-with-a-free-slot case cannot happen, or it is observable enough that an operator
  can tell it apart from a genuinely full keyspace.

## Related

- `internal/agent/source/perforce/exclusion_ceiling.go` (the attempt cap and the candidate ordering)
- [[idea-2026-09-04-a-job-author-controls-how-many-p4-clients-each-agent-creates]] - closed; introduced this
- [[idea-2026-09-04-no-workspace-size-or-eviction-instrumentation]] - why the churn is unmeasurable
