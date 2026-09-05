---
title: Implement sync-spec exclusion paths per the 2026-09-04 design spec
type: feature
status: closed
closed: 2026-09-04
resolution: fixed
created: 2026-09-04
priority: medium
source: Carries the implementation half of idea-2026-09-03-sync-spec-exclusion-paths-design, whose spec landed 2026-09-04
---

# Implement sync-spec exclusion paths per the 2026-09-04 design spec

## Summary
`docs/superpowers/specs/2026-09-04-sync-spec-exclusion-paths.md` settles the mechanism, the
spec field, the workspace-identity rule, validation, operator visibility, migration and the
out-of-disk interaction. This item is the code.

## Context
The design item was scoped to the spec alone ("A design item; run it through the brainstorming
flow and write the spec before any code") and is closed on that basis. Its second acceptance
bullet is a property of shipped code, not of a document, so it moves here intact:

> No task can observe a workspace missing files it asked for because a previous task excluded them.

Read the spec's section 3.4 before scheduling this. The disk trade is NEGATIVE for any stream
that carries a mixed exclusion set on one agent (`2S - X` with `X < S` always), so the feature
pays only where the exclusion is uniform for that stream on that agent. That is the fork's
deployment shape and the reason the fork never observed the poisoning this design exists to
prevent. The overflow converts to sweeper eviction churn, which is unmeasurable today.

## Acceptance / Done When
- No task can observe a workspace missing files it asked for because a previous task excluded them.
- `TestPerforce_E2E_AnExcludingTaskDoesNotStripFilesFromAnUnexcludingPeer` passes in the p4d lane,
  with the excluding task running FIRST (spec section 11 - the reverse order hides the defect,
  because the unexcluding task's full sync leaves the files on disk).
- The mutation that must kill that test: making `SourceKey` ignore exclusions.
- `BaselineHash` covers the exclusion set. It reads `entry{path, rev}` today, so an exclusion
  field hashes identically to its absence.
- The dispatcher's warm bias keys on the new key. `selectWorker` compares `ws.SourceKey` to
  `taskSrc.Stream` directly today, so a key function has to be created server-side.
- A task with no exclusions keeps today's key byte for byte.

## Related
- The spec: `docs/superpowers/specs/2026-09-04-sync-spec-exclusion-paths.md`
- [[idea-2026-09-03-sync-spec-exclusion-paths-design]] - the design item this carries forward
- [[bug-2026-09-04-a-subpath-of-a-renaming-remap-does-not-resolve]] - meets this in `toClientPath`
- [[bug-2026-09-04-p4-sync-reports-not-in-client-view-and-exits-zero]] - the preempt inherits it
- `internal/agent/source/perforce/perforce.go`, `baseline.go`, `internal/jobspec/jobspec.go`,
  `internal/scheduler/dispatch.go`

## Resolution

Closed by PR #203 (`f1849cd`), Stage 1 of the design spec. Every acceptance bullet is met: the
excluding-first p4d test passes, `SourceKey` ignoring exclusions kills it, `BaselineHash` covers the
exclusion set, the dispatcher's warm bias keys on a server-side key function, and a task with no
exclusions keeps today's key and hash byte for byte against a golden captured at HEAD.

**Review found a security defect in the first implementation and it was reproduced against live
p4d.** The refusal for an inert exclusion matched one phrase, `"no such file"`. p4 has a family of
these and all exit zero, and on a REMAPPED stream - one of the two routes the design names as its
reason for existing - `sync -k` returns "file(s) not in client view", the predicate returned false,
the excluded subtree transferred in full, and the task reported success. Replaced with a positive
assertion: `p4 files -m1` before each preempt, refusing on empty stdout. That states the property
rather than reading p4's prose, is independent of the have-list (so a warm workspace still reads as
success, which `sync -n` would not), and closed a phrase-match false positive for free.

A second review finding was a missing guard rather than a bug: the preempt-failure `handle.Release()`
calls were correct and deleting them left the whole package green, while the control on the
sync-failure release reddened at once. Three refusals now have three subtests and three orthogonal
mutation kills.

Stage 2 - the agent/coordinator version skew, where protobuf drops the unknown field and an older
agent syncs everything and reports success - is deliberately NOT in this slice. README states it as
a live limitation on its own terms, naming the mechanism, the observable and the operator action,
promising no future fix so it does not become a lie if Stage 2 never lands.

One finding filed rather than fixed:
[[idea-2026-09-04-a-job-author-controls-how-many-p4-clients-each-agent-creates]]. The Go/Python
validator pair (`jobspec.SyncEntry` against `Sync`) remains guarded by nothing; the SDK is
uniformly looser than the server, which is drift in the safe direction rather than a hole.
