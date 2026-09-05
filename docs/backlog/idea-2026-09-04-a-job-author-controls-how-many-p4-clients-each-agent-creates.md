---
title: A job author controls how many p4 client specs and workspace directories every agent creates
type: idea
status: open
created: 2026-09-04
priority: medium
source: Invariants and security lenses of the sync-spec exclusion paths slice (PR #203), which both reached it independently
---

# A job author controls how many p4 client specs and workspace directories every agent creates

## Summary

Since sync exclusions shipped, `shortID` derives from `SourceKey`, which folds in the exclusion set. The p4 client name and the workspace directory are both functions of that short id, and `CreateStreamClient` runs on **every** Prepare - roughly 200 lines before anything can refuse a bogus exclusion. So a distinct exclusion set mints a distinct source key, short id, workspace directory and a persistent client spec **on the shared Perforce server**, and only then can the prepare fail.

## Context

Found independently by two review lenses on PR #203 and confirmed by the implementer.

**Before that change the count was bounded by something an author cannot invent.** A workspace was keyed on the stream, and `p4 client -o -S <stream>` fails for a stream that does not exist - so the number of distinct client specs an agent could be made to create was bounded by the number of real streams. Now `validateSourceSpec` requires only that an exclusion path start with `//` and sit under the stream; nothing checks that it names anything. Sixteen arbitrary strings per task therefore yield a fresh key per task.

**The cheap variant needs no valid depot path at all**, which is what makes this worth an item rather than a footnote in the disk story. The refusal is downstream of the minting, so a spec whose exclusions resolve to nothing still creates the artifacts and then fails with zero bytes transferred. Each row flows into `worker_workspaces` through `applyInventory`, which is a single transaction over an un-count-bounded slice.

**It is a bound moved, not removed.** Each artifact is small, the prepare fails loudly, and the registry row is deliberately reclaimable by the sweeper. But the sweeper's pressure pass is off by default and cannot evict a workspace that is in use, so reclamation is not guaranteed either.

**The obvious fix does not work, and this is the part to read before scoping.** Moving the resolve-probe ahead of `CreateStreamClient` would make a bogus exclusion cost nothing - except the probe uses a client-form filespec, and a not-in-client-view path is only detectable *through* the client's view. The probe cannot run before the client exists.

## Proposal

Sketch only.

A per-agent ceiling on distinct source keys per stream, checked before `allocateShortID`. Note it interacts with the sweeper: a ceiling that refuses rather than evicts turns a full keyspace into a denial of the feature for legitimate specs, so decide what happens at the ceiling before picking the number.

Consider also whether the client spec has to be created before the exclusion set is known to resolve - a cheaper client, or a probe that does not need the full client, would close it at the source.

## Acceptance / Done When

- The number of p4 client specs and workspace directories one job author can cause an agent to create is bounded by something the author does not control.
- What happens at the ceiling is decided, and it is not simply refusing every subsequent legitimate spec.
- The disk paragraph in README describes the author-controlled case, not only the benign uniform one.

## Related

- `internal/agent/source/perforce/perforce.go` (`allocateShortID`, `CreateStreamClient`, the probe and preempt loop), `internal/agent/source/perforce/sourcekey.go`
- `internal/worker/handler.go` (`applyInventory`, the single transaction)
- [[idea-2026-09-04-worker-workspaces-source-key-is-unbounded-in-a-primary-key]] - the same table, the other axis
- [[idea-2026-09-04-no-workspace-size-or-eviction-instrumentation]] - why the churn this causes is currently unmeasurable
