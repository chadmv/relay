---
title: A job author controls how many p4 client specs and workspace directories every agent creates
type: idea
status: closed
created: 2026-09-04
priority: medium
source: Invariants and security lenses of the sync-spec exclusion paths slice (PR #203), which both reached it independently
closed: 2026-09-10
resolution: fixed
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

## Resolution

Fixed in PR #211, squashed as `fa9971be`. Two enforcement points: a per-(stream, agent) ceiling over
**exclusion-derived keys only** in `Prepare`'s cold branch, evict-then-admit; and a per-batch row bound
in `applyInventory` that TRUNCATES rather than refusing, because refusing would re-create the freeze
PR #209 removed. `RELAY_WORKSPACE_MAX_EXCLUSION_SETS`, default 4, hard max 64, with no off token and
`0` not one of them - both neighbouring `RELAY_WORKSPACE_*` variables mean "disabled" at zero, so a
bare comparison against an unset field would have refused every cold exclusion prepare.

**This item's framing rested on an unverified claim, and it was measured first with an explicit
stop branch.** "A bound moved, not removed" holds only if `p4 client -o -S` refuses a non-existent
stream. Reframed away from exit status, on the `PathHasFiles` precedent that p4 exits zero on
conditions it also reports on stderr: the question is whether a client spec for a non-existent stream
can be PERSISTED. Against real p4d 2026.1 - control on a real stream exits 0 with a full spec; the
bogus stream exits 1 with EMPTY stdout and `Stream '...' doesn't exist.` on stderr; `client -i` is
never reached because no spec exists to pipe; and `p4 clients -e` afterwards is empty. The refusal is
at the FIRST of the two calls, so nothing is minted. That is now a permanent guard with the written
lane excuse CLAUDE.md requires, and it cannot move to the default lane because the property is what
REAL p4 does with a stream name that is not there.

**Three things in this item would have produced the wrong control.**

- Acceptance criterion 3 was ALREADY satisfied at HEAD. README's source-workspaces paragraph already
  stated that each exclusion set mints its own directory and client spec before the paths are checked,
  so working from the criterion as written would have added a duplicate sentence. What the paragraph
  lacked is the ceiling, the number, and what happens at it.
- "Checked before `allocateShortID`" understates the requirement. That function mints nothing - it is
  a hash plus a collision probe - and the artifacts appear 44 and 58 lines later at `os.MkdirAll` and
  `CreateStreamClient`. Read as written, the check lands where the found/not-found distinction is
  invisible and every WARM prepare gets gated.
- Read literally, "a ceiling on distinct source keys per stream" puts the BASE workspace inside the
  ceiling, letting an author with junk strings evict the shared warm workspace every non-exclusion task
  on that stream uses. That is the attacker's best outcome from the control.

**The mitigation this item names does not exist.** It says the sweeper's pressure pass is "off by
default"; `Sweeper.Run` returns immediately when both `MaxAge` and `MinFreeGB` are zero and the sweeper
is only constructed when one is set, so on a default agent nothing reclaims a workspace ever. The
ceiling cannot delegate reclamation and evicts inline. Candidates are ordered empty-`BaselineHash`
first then LRU, because the empty baseline is exactly the residue a failed bogus prepare leaves - so
the attacker's junk is reclaimed before any legitimate warm workspace. The base workspace is never a
candidate, proven with a decoy that sorts first under both arms.

The refusal never names the occupants: task logs are readable by any authenticated user, so occupant
keys would be a cross-tenant disclosure. Short ids go to the agent's own log.

**Question 4 is answered rather than left open.** The client must exist before the probe can run, so a
ceiling is the only practical answer. Rejected: moving the probe earlier (the filespec is client-form
and `ResolveHead` has the same dependency); a shared client name (the preempt writes the CLIENT's
have-list, so one client across two exclusion sets is one have-list across them - the exact poisoning
the composite key exists to prevent); and rollback of the cold mint on probe failure, because the
preempt loop also runs on a warm workspace whose baseline moved, so one typo would destroy a populated
multi-terabyte workspace.

Two honest limits are documented rather than hidden. The concurrency bound is
`K + concurrent prepares - 1`, not `K`, since the check and the mint are not under one lock; the
overshoot is bounded by the operator's slot count rather than the author's input. And with more
candidates than the ceiling the attempt cap leaves the last ones untried, so five held-but-one-free
candidates at ceiling 4 can refuse even though the fifth was evictable - recorded as a work bound, not
as convergence. A "converges in one prepare" claim was written and then refuted by simulation: 20
entries at ceiling 4 takes five prepares.
